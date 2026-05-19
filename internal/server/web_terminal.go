package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"nhooyr.io/websocket"

	"serial-platform/internal/protocol"
	"serial-platform/internal/serial"
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

	control, ok := srv.resolveSerialControl(channelID)
	if !ok {
		_ = conn.Close(websocket.StatusPolicyViolation, "serial channel not found")
		return
	}
	if err := srv.controlOwner.Acquire(channelID, "web"); err != nil {
		_ = conn.Close(websocket.StatusTryAgainLater, err.Error())
		return
	}
	defer srv.controlOwner.Release(channelID, "web")

	session, err := control.OpenControlSession(r.Context(), "web")
	if err != nil {
		_ = conn.Close(websocket.StatusInternalError, err.Error())
		return
	}
	defer session.Close()
	defer conn.Close(websocket.StatusNormalClosure, "")

	srv.serveTerminalSession(r.Context(), conn, session)
}

func (srv *Server) resolveSerialControl(channelID string) (serial.SerialControl, bool) {
	if srv.serialResolver == nil {
		return nil, false
	}
	return srv.serialResolver(channelID)
}

func (srv *Server) serveTerminalSession(ctx context.Context, conn *websocket.Conn, session serial.ControlSession) {
	for {
		_, payload, err := conn.Read(ctx)
		if err != nil {
			return
		}
		result := handleTerminalControlMessage(payload, session)
		if err := protocol.WriteJSON(ctx, conn, result); err != nil {
			return
		}
	}
}

func handleTerminalControlMessage(payload []byte, session serial.ControlSession) protocol.OperationResult {
	var envelope struct {
		Type      protocol.MessageType `json:"type"`
		RequestID string               `json:"request_id"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return terminalResult("", err)
	}

	var err error
	switch envelope.Type {
	case protocol.MessageTerminalWrite:
		var msg protocol.TerminalWrite
		if err = json.Unmarshal(payload, &msg); err == nil {
			err = session.Write(msg.Data)
		}
	case protocol.MessageSerialSetConfig:
		var msg protocol.SerialSetConfig
		if err = json.Unmarshal(payload, &msg); err == nil {
			err = session.SetConfig(serial.Config{
				Baud:     msg.Baud,
				DataBits: msg.DataBits,
				Parity:   msg.Parity,
				StopBits: msg.StopBits,
				Flow:     msg.Flow,
			})
		}
	case protocol.MessageSerialSetDTR:
		var msg protocol.SerialSetDTR
		if err = json.Unmarshal(payload, &msg); err == nil {
			err = session.SetDTR(msg.Value)
		}
	case protocol.MessageSerialSetRTS:
		var msg protocol.SerialSetRTS
		if err = json.Unmarshal(payload, &msg); err == nil {
			err = session.SetRTS(msg.Value)
		}
	case protocol.MessageSerialSendBreak:
		var msg protocol.SerialSendBreak
		if err = json.Unmarshal(payload, &msg); err == nil {
			err = session.SendBreak(time.Duration(msg.DurationMS) * time.Millisecond)
		}
	default:
		err = errors.New("unsupported terminal message type")
	}
	if err != nil {
		return terminalResult(envelope.RequestID, err)
	}
	return protocol.OperationResult{
		Type:      protocol.MessageOperationResult,
		RequestID: envelope.RequestID,
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
