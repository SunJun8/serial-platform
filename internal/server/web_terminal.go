package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/google/uuid"
	"nhooyr.io/websocket"

	"serial-platform/internal/protocol"
	"serial-platform/internal/storage"
)

type ControlOwner struct {
	mu     sync.Mutex
	owners map[string]string
}

func NewControlOwner() *ControlOwner {
	return &ControlOwner{owners: make(map[string]string)}
}

func (o *ControlOwner) Acquire(channelID, owner string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if current := o.owners[channelID]; current != "" {
		return errors.New("channel is busy")
	}
	o.owners[channelID] = owner
	return nil
}

func (o *ControlOwner) Release(channelID, owner string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.owners[channelID] == owner {
		delete(o.owners, channelID)
	}
}

func (srv *Server) handleTerminalWebSocket(w http.ResponseWriter, r *http.Request) {
	channelID := r.PathValue("channelID")
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}

	channel, err := srv.lookupTerminalChannel(channelID)
	if errors.Is(err, storage.ErrNotFound) {
		_ = conn.Close(websocket.StatusPolicyViolation, "channel not found")
		return
	}
	if err != nil {
		_ = conn.Close(websocket.StatusInternalError, err.Error())
		return
	}
	if err := srv.controlOwner.Acquire(channelID, "web"); err != nil {
		_ = conn.Close(websocket.StatusTryAgainLater, err.Error())
		return
	}
	defer srv.controlOwner.Release(channelID, "web")

	sessionID := uuid.NewString()
	if err := srv.agentRegistry.send(r.Context(), channel.AgentID, protocol.TerminalOpen{
		Type:      protocol.MessageTerminalOpen,
		SessionID: sessionID,
		ChannelID: channel.ID,
	}); err != nil {
		_ = conn.Close(websocket.StatusInternalError, err.Error())
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	defer func() {
		_ = srv.agentRegistry.send(context.Background(), channel.AgentID, protocol.TerminalClose{
			Type:      protocol.MessageTerminalClose,
			SessionID: sessionID,
			ChannelID: channel.ID,
		})
	}()

	srv.serveTerminalSession(r.Context(), conn, channel, sessionID)
}

func (srv *Server) lookupTerminalChannel(channelID string) (storage.Channel, error) {
	if srv.db == nil {
		return storage.Channel{}, errors.New("channel database is not configured")
	}
	return srv.db.GetChannel(channelID)
}

func (srv *Server) serveTerminalSession(ctx context.Context, conn *websocket.Conn, channel storage.Channel, sessionID string) {
	for {
		_, payload, err := conn.Read(ctx)
		if err != nil {
			return
		}
		message, requestID, err := terminalAgentControlMessage(payload, sessionID, channel.ID)
		if err == nil {
			err = srv.agentRegistry.send(ctx, channel.AgentID, message)
		}
		result := terminalResultFromSend(requestID, err)
		if err := protocol.WriteJSON(ctx, conn, result); err != nil {
			return
		}
	}
}

func terminalAgentControlMessage(payload []byte, sessionID, channelID string) (any, string, error) {
	var envelope struct {
		Type      protocol.MessageType `json:"type"`
		RequestID string               `json:"request_id"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return nil, "", err
	}

	switch envelope.Type {
	case protocol.MessageTerminalWrite:
		var msg protocol.TerminalWrite
		if err := json.Unmarshal(payload, &msg); err != nil {
			return nil, envelope.RequestID, err
		}
		msg.SessionID = sessionID
		msg.ChannelID = channelID
		return msg, envelope.RequestID, nil
	case protocol.MessageSerialSetConfig:
		var msg protocol.SerialSetConfig
		if err := json.Unmarshal(payload, &msg); err != nil {
			return nil, envelope.RequestID, err
		}
		msg.SessionID = sessionID
		msg.ChannelID = channelID
		return msg, envelope.RequestID, nil
	case protocol.MessageSerialSetDTR:
		var msg protocol.SerialSetDTR
		if err := json.Unmarshal(payload, &msg); err != nil {
			return nil, envelope.RequestID, err
		}
		msg.SessionID = sessionID
		msg.ChannelID = channelID
		return msg, envelope.RequestID, nil
	case protocol.MessageSerialSetRTS:
		var msg protocol.SerialSetRTS
		if err := json.Unmarshal(payload, &msg); err != nil {
			return nil, envelope.RequestID, err
		}
		msg.SessionID = sessionID
		msg.ChannelID = channelID
		return msg, envelope.RequestID, nil
	case protocol.MessageSerialSendBreak:
		var msg protocol.SerialSendBreak
		if err := json.Unmarshal(payload, &msg); err != nil {
			return nil, envelope.RequestID, err
		}
		msg.SessionID = sessionID
		msg.ChannelID = channelID
		return msg, envelope.RequestID, nil
	default:
		return nil, envelope.RequestID, fmt.Errorf("unsupported terminal message type %q", envelope.Type)
	}
}

func terminalResultFromSend(requestID string, err error) protocol.OperationResult {
	if err != nil {
		return terminalResult(requestID, err)
	}
	return protocol.OperationResult{
		Type:      protocol.MessageOperationResult,
		RequestID: requestID,
		OK:        true,
	}
}

func terminalResult(requestID string, err error) protocol.OperationResult {
	return protocol.OperationResult{
		Type:      protocol.MessageOperationResult,
		RequestID: requestID,
		OK:        false,
		Error:     err.Error(),
	}
}
