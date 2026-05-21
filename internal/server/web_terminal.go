package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

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

type terminalOperationRegistry struct {
	mu      sync.Mutex
	pending map[string]chan protocol.OperationResult
}

func newTerminalOperationRegistry() *terminalOperationRegistry {
	return &terminalOperationRegistry{pending: make(map[string]chan protocol.OperationResult)}
}

func (r *terminalOperationRegistry) register(requestID string) <-chan protocol.OperationResult {
	result := make(chan protocol.OperationResult, 1)
	r.mu.Lock()
	r.pending[requestID] = result
	r.mu.Unlock()
	return result
}

func (r *terminalOperationRegistry) cancel(requestID string) {
	r.mu.Lock()
	delete(r.pending, requestID)
	r.mu.Unlock()
}

func (r *terminalOperationRegistry) complete(result protocol.OperationResult) bool {
	r.mu.Lock()
	ch, ok := r.pending[result.RequestID]
	if ok {
		delete(r.pending, result.RequestID)
	}
	r.mu.Unlock()
	if ok {
		ch <- result
	}
	return ok
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
	releaseOwner := true
	defer func() {
		if releaseOwner {
			srv.controlOwner.Release(channelID, "web")
		}
	}()

	sessionID := uuid.NewString()
	openRequestID := terminalAgentRequestID(sessionID)
	openResult, err := srv.sendTerminalOperation(r.Context(), channel.AgentID, openRequestID, protocol.TerminalOpen{
		Type:      protocol.MessageTerminalOpen,
		RequestID: openRequestID,
		SessionID: sessionID,
		ChannelID: channel.ID,
	})
	if err != nil {
		_ = conn.Close(websocket.StatusInternalError, err.Error())
		return
	}
	if !openResult.OK {
		_ = conn.Close(websocket.StatusInternalError, openResult.Error)
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	defer func() {
		srv.controlOwner.Release(channelID, "web")
		releaseOwner = false
		srv.sendTerminalClose(channel.AgentID, channel.ID, sessionID)
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
		message, browserRequestID, agentRequestID, err := terminalAgentControlMessage(payload, sessionID, channel.ID)
		var result protocol.OperationResult
		if err != nil {
			result = terminalResult(browserRequestID, err)
		} else {
			var agentResult protocol.OperationResult
			agentResult, err = srv.sendTerminalOperation(ctx, channel.AgentID, agentRequestID, message)
			result = terminalBrowserResult(browserRequestID, agentResult, err)
		}
		if err := protocol.WriteJSON(ctx, conn, result); err != nil {
			return
		}
	}
}

func terminalAgentRequestID(sessionID string) string {
	return "terminal-" + sessionID + "-" + uuid.NewString()
}

func (srv *Server) sendTerminalOperation(ctx context.Context, agentID, requestID string, message any) (protocol.OperationResult, error) {
	resultCh := srv.terminalOps.register(requestID)
	defer srv.terminalOps.cancel(requestID)
	if err := srv.agentRegistry.send(ctx, agentID, message); err != nil {
		return protocol.OperationResult{}, err
	}
	select {
	case result := <-resultCh:
		return result, nil
	case <-ctx.Done():
		return protocol.OperationResult{}, ctx.Err()
	}
}

func (srv *Server) sendTerminalClose(agentID, channelID, sessionID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	requestID := terminalAgentRequestID(sessionID)
	_, _ = srv.sendTerminalOperation(ctx, agentID, requestID, protocol.TerminalClose{
		Type:      protocol.MessageTerminalClose,
		RequestID: requestID,
		SessionID: sessionID,
		ChannelID: channelID,
	})
}

func terminalAgentControlMessage(payload []byte, sessionID, channelID string) (any, string, string, error) {
	var envelope struct {
		Type      protocol.MessageType `json:"type"`
		RequestID string               `json:"request_id"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return nil, "", "", err
	}

	agentRequestID := terminalAgentRequestID(sessionID)
	switch envelope.Type {
	case protocol.MessageTerminalWrite:
		var msg protocol.TerminalWrite
		if err := json.Unmarshal(payload, &msg); err != nil {
			return nil, envelope.RequestID, "", err
		}
		msg.RequestID = agentRequestID
		msg.SessionID = sessionID
		msg.ChannelID = channelID
		return msg, envelope.RequestID, agentRequestID, nil
	case protocol.MessageSerialSetConfig:
		var msg protocol.SerialSetConfig
		if err := json.Unmarshal(payload, &msg); err != nil {
			return nil, envelope.RequestID, "", err
		}
		msg.RequestID = agentRequestID
		msg.SessionID = sessionID
		msg.ChannelID = channelID
		return msg, envelope.RequestID, agentRequestID, nil
	case protocol.MessageSerialSetDTR:
		var msg protocol.SerialSetDTR
		if err := json.Unmarshal(payload, &msg); err != nil {
			return nil, envelope.RequestID, "", err
		}
		msg.RequestID = agentRequestID
		msg.SessionID = sessionID
		msg.ChannelID = channelID
		return msg, envelope.RequestID, agentRequestID, nil
	case protocol.MessageSerialSetRTS:
		var msg protocol.SerialSetRTS
		if err := json.Unmarshal(payload, &msg); err != nil {
			return nil, envelope.RequestID, "", err
		}
		msg.RequestID = agentRequestID
		msg.SessionID = sessionID
		msg.ChannelID = channelID
		return msg, envelope.RequestID, agentRequestID, nil
	case protocol.MessageSerialSendBreak:
		var msg protocol.SerialSendBreak
		if err := json.Unmarshal(payload, &msg); err != nil {
			return nil, envelope.RequestID, "", err
		}
		msg.RequestID = agentRequestID
		msg.SessionID = sessionID
		msg.ChannelID = channelID
		return msg, envelope.RequestID, agentRequestID, nil
	default:
		return nil, envelope.RequestID, "", fmt.Errorf("unsupported terminal message type %q", envelope.Type)
	}
}

func terminalBrowserResult(requestID string, agentResult protocol.OperationResult, err error) protocol.OperationResult {
	if err != nil {
		return terminalResult(requestID, err)
	}
	return protocol.OperationResult{
		Type:      protocol.MessageOperationResult,
		RequestID: requestID,
		OK:        agentResult.OK,
		Error:     agentResult.Error,
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
