package server

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"nhooyr.io/websocket"

	"serial-platform/internal/protocol"
	"serial-platform/internal/storage"
)

func (srv *Server) handleAgentWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	ctx := r.Context()
	messageType, data, err := conn.Read(ctx)
	if err != nil {
		return
	}
	if messageType != websocket.MessageText {
		conn.Close(websocket.StatusPolicyViolation, "agent hello must be text")
		return
	}
	var hello protocol.AgentHello
	if err := json.Unmarshal(data, &hello); err != nil {
		return
	}
	if hello.Type != protocol.MessageAgentHello || hello.AgentID == "" {
		conn.Close(websocket.StatusPolicyViolation, "malformed agent hello")
		return
	}

	now := time.Now().UTC()
	status := storage.AgentStatusPending
	existing, err := srv.db.GetAgent(hello.AgentID)
	if err == nil && existing.Status == storage.AgentStatusActive {
		status = storage.AgentStatusActive
	} else if err != nil && err != storage.ErrNotFound {
		conn.Close(websocket.StatusInternalError, err.Error())
		return
	}
	if err := srv.db.UpsertAgent(storage.Agent{
		ID:        hello.AgentID,
		Name:      hello.Hostname,
		Status:    status,
		Hostname:  hello.Hostname,
		OS:        hello.OS,
		Arch:      hello.Arch,
		MachineID: hello.MachineID,
		UpdatedAt: now,
	}); err != nil {
		conn.Close(websocket.StatusInternalError, err.Error())
		return
	}

	agentConn := newAgentConnection(hello.AgentID, conn, now)
	srv.agentRegistry.upsert(agentConn)
	defer srv.agentRegistry.remove(hello.AgentID, conn)

	accepted := protocol.AgentAccepted{
		Type:   protocol.MessageAgentAccepted,
		Status: string(status),
	}
	if err := agentConn.send(ctx, accepted); err != nil {
		return
	}

	if err := srv.sendInitialChannelSync(ctx, agentConn); err != nil {
		log.Printf("send channel sync to %s: %v", hello.AgentID, err)
		return
	}

	srv.readAgentControlMessages(ctx, hello.AgentID, conn)
}

func (srv *Server) sendChannelSync(ctx context.Context, agentID string) error {
	message, err := srv.channelSyncMessage(agentID)
	if err != nil {
		return err
	}
	return srv.agentRegistry.send(ctx, agentID, message)
}

func (srv *Server) sendInitialChannelSync(ctx context.Context, conn AgentConnection) error {
	message, err := srv.channelSyncMessage(conn.AgentID)
	if err != nil {
		return err
	}
	return conn.send(ctx, message)
}

func (srv *Server) channelSyncMessage(agentID string) (protocol.ChannelSync, error) {
	channels, err := srv.db.ListChannels()
	if err != nil {
		return protocol.ChannelSync{}, err
	}
	message := protocol.ChannelSync{
		Type:     protocol.MessageChannelSync,
		Channels: make([]protocol.ChannelConfigMessage, 0, len(channels)),
	}
	for _, channel := range channels {
		if channel.AgentID != agentID {
			continue
		}
		message.Channels = append(message.Channels, channelConfigMessage(channel))
	}
	return message, nil
}

func channelConfigMessage(channel storage.Channel) protocol.ChannelConfigMessage {
	return protocol.ChannelConfigMessage{
		ID:              channel.ID,
		AgentID:         channel.AgentID,
		DevName:         channel.DevName,
		IDPath:          channel.IDPath,
		IDPathTag:       channel.IDPathTag,
		Status:          string(channel.Status),
		DefaultBaud:     channel.DefaultBaud,
		DefaultDataBits: channel.DefaultDataBits,
		DefaultParity:   channel.DefaultParity,
		DefaultStopBits: channel.DefaultStopBits,
		DefaultFlow:     channel.DefaultFlow,
	}
}

func (srv *Server) readAgentControlMessages(ctx context.Context, agentID string, conn *websocket.Conn) {
	for {
		var envelope struct {
			Type protocol.MessageType `json:"type"`
		}
		messageType, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		if messageType != websocket.MessageText {
			_ = conn.Close(websocket.StatusPolicyViolation, "agent control messages must be text")
			return
		}
		if err := json.Unmarshal(data, &envelope); err != nil {
			log.Printf("decode agent control envelope from %s: %v", agentID, err)
			continue
		}
		switch envelope.Type {
		case protocol.MessageDeviceSnapshot:
			var snapshot protocol.DeviceSnapshot
			if err := json.Unmarshal(data, &snapshot); err != nil {
				log.Printf("decode device snapshot from %s: %v", agentID, err)
				continue
			}
			if err := retryOnBusy(func() error {
				return srv.handleDeviceSnapshot(agentID, snapshot)
			}); err != nil {
				log.Printf("handle device snapshot from %s: %v", agentID, err)
			}
		case protocol.MessageChannelStatus:
			var update protocol.ChannelStatusUpdate
			if err := json.Unmarshal(data, &update); err != nil {
				log.Printf("decode channel status from %s: %v", agentID, err)
				continue
			}
			if err := retryOnBusy(func() error {
				return srv.handleChannelStatusUpdate(agentID, update)
			}); err != nil {
				log.Printf("handle channel status from %s: %v", agentID, err)
			}
		case protocol.MessageTunnelOpened:
			var opened protocol.TunnelOpened
			if err := json.Unmarshal(data, &opened); err != nil {
				log.Printf("decode tunnel opened from %s: %v", agentID, err)
				continue
			}
			log.Printf("agent %s opened tunnel %s mode %s", agentID, opened.TunnelID, opened.Mode)
		case protocol.MessageTunnelError:
			var tunnelError protocol.TunnelError
			if err := json.Unmarshal(data, &tunnelError); err != nil {
				log.Printf("decode tunnel error from %s: %v", agentID, err)
				continue
			}
			log.Printf("agent %s tunnel %s error: %s", agentID, tunnelError.TunnelID, tunnelError.Error)
			srv.tunnels.CancelWithError(tunnelError.TunnelID, errors.New(tunnelError.Error))
		default:
			log.Printf("agent %s sent unsupported control message type %q", agentID, envelope.Type)
		}
	}
}

func (srv *Server) handleDeviceSnapshot(agentID string, snapshot protocol.DeviceSnapshot) error {
	if snapshot.AgentID != "" && snapshot.AgentID != agentID {
		return nil
	}
	channels, err := srv.db.ListChannels()
	if err != nil {
		return err
	}
	configuredIDPaths := make(map[string]struct{}, len(channels))
	for _, channel := range channels {
		if channel.AgentID == agentID && channel.IDPath != "" {
			configuredIDPaths[channel.IDPath] = struct{}{}
		}
	}
	now := time.Now().UTC()
	for _, device := range snapshot.Devices {
		if device.IDPath != "" {
			if _, configured := configuredIDPaths[device.IDPath]; configured {
				continue
			}
		}
		candidate := candidateFromDevice(agentID, device, now)
		if candidate.ID == "" {
			continue
		}
		if err := srv.db.UpsertCandidate(candidate); err != nil {
			return err
		}
	}
	return nil
}

func candidateFromDevice(agentID string, device protocol.DeviceIdentity, seenAt time.Time) storage.Candidate {
	key := firstNonEmpty(device.IDPath, device.IDPathTag, device.DevName)
	if key == "" {
		return storage.Candidate{}
	}
	return storage.Candidate{
		ID:           stableCandidateID(agentID, key),
		AgentID:      agentID,
		DevName:      device.DevName,
		IDPath:       device.IDPath,
		IDPathTag:    device.IDPathTag,
		SysfsDevpath: device.SysfsDevpath,
		Interface:    device.Interface,
		VID:          device.VID,
		PID:          device.PID,
		Serial:       device.Serial,
		Driver:       device.Driver,
		Manufacturer: device.Manufacturer,
		Product:      device.Product,
		FirstSeen:    seenAt,
		LastSeen:     seenAt,
	}
}

func stableCandidateID(agentID, key string) string {
	replacer := strings.NewReplacer("/", "_", ":", "_", " ", "_", "\t", "_")
	return agentID + ":" + replacer.Replace(key)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func retryOnBusy(fn func() error) error {
	var err error
	for attempt := 0; attempt < 20; attempt++ {
		err = fn()
		if !isSQLiteBusy(err) {
			return err
		}
		time.Sleep(10 * time.Millisecond)
	}
	return err
}

func isSQLiteBusy(err error) bool {
	if err == nil {
		return false
	}
	text := err.Error()
	return strings.Contains(text, "database is locked") || strings.Contains(text, "SQLITE_BUSY")
}

func (srv *Server) handleChannelStatusUpdate(agentID string, update protocol.ChannelStatusUpdate) error {
	if update.AgentID != "" && update.AgentID != agentID {
		return nil
	}
	for _, status := range update.Statuses {
		if status.ChannelID == "" {
			continue
		}
		channelStatus := storage.ChannelStatus(status.Status)
		if !isValidChannelStatus(channelStatus) {
			continue
		}
		if err := srv.db.UpdateChannelStatusForAgent(
			status.ChannelID,
			agentID,
			channelStatus,
			status.DevName,
			status.ErrorMessage,
			time.Now().UTC(),
		); err != nil && err != storage.ErrNotFound {
			return err
		}
	}
	return nil
}

func isValidChannelStatus(status storage.ChannelStatus) bool {
	switch status {
	case storage.ChannelStatusOnline,
		storage.ChannelStatusOffline,
		storage.ChannelStatusBusy,
		storage.ChannelStatusDisabled,
		storage.ChannelStatusError:
		return true
	default:
		return false
	}
}
