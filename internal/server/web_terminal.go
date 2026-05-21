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
	pending map[string]terminalPendingOperation
}

type terminalPendingOperation struct {
	agentID string
	result  chan protocol.OperationResult
}

func newTerminalOperationRegistry() *terminalOperationRegistry {
	return &terminalOperationRegistry{pending: make(map[string]terminalPendingOperation)}
}

func (r *terminalOperationRegistry) register(agentID, requestID string) (<-chan protocol.OperationResult, error) {
	result := make(chan protocol.OperationResult, 1)
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.pending[requestID]; exists {
		return nil, fmt.Errorf("terminal operation request %s is already pending", requestID)
	}
	r.pending[requestID] = terminalPendingOperation{agentID: agentID, result: result}
	return result, nil
}

func (r *terminalOperationRegistry) cancel(agentID, requestID string) {
	r.mu.Lock()
	if pending, ok := r.pending[requestID]; ok && pending.agentID == agentID {
		delete(r.pending, requestID)
	}
	r.mu.Unlock()
}

func (r *terminalOperationRegistry) complete(agentID string, result protocol.OperationResult) bool {
	r.mu.Lock()
	pending, ok := r.pending[result.RequestID]
	if ok && pending.agentID == agentID {
		delete(r.pending, result.RequestID)
	} else {
		ok = false
	}
	r.mu.Unlock()
	if ok {
		pending.result <- result
	}
	return ok
}

func (r *terminalOperationRegistry) failAgent(agentID string, err error) {
	r.mu.Lock()
	type failedOperation struct {
		requestID string
		result    chan protocol.OperationResult
	}
	results := make([]failedOperation, 0)
	for requestID, pending := range r.pending {
		if pending.agentID != agentID {
			continue
		}
		delete(r.pending, requestID)
		results = append(results, failedOperation{requestID: requestID, result: pending.result})
	}
	r.mu.Unlock()
	for _, failed := range results {
		failed.result <- protocol.OperationResult{
			Type:      protocol.MessageOperationResult,
			RequestID: failed.requestID,
			OK:        false,
			Error:     err.Error(),
		}
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
	releaseOwner := true
	defer func() {
		if releaseOwner {
			srv.controlOwner.Release(channelID, "web")
		}
	}()

	sessionID := uuid.NewString()
	openRequestID := terminalAgentRequestID(sessionID)
	openCtx, stopOpenCloseWatch := srv.terminalCloseWatchContext(r.Context(), conn)
	openResult, sent, err := srv.sendTerminalOperation(openCtx, channel.AgentID, openRequestID, protocol.TerminalOpen{
		Type:      protocol.MessageTerminalOpen,
		RequestID: openRequestID,
		SessionID: sessionID,
		ChannelID: channel.ID,
	})
	stopOpenCloseWatch()
	if sent && err != nil {
		srv.sendTerminalClose(channel.AgentID, channel.ID, sessionID)
	}
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

func (srv *Server) terminalCloseWatchContext(parent context.Context, conn *websocket.Conn) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pingCtx, pingCancel := context.WithTimeout(ctx, 100*time.Millisecond)
				err := conn.Ping(pingCtx)
				pingCancel()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()
	return ctx, func() {
		cancel()
		<-done
	}
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
			agentResult, _, err = srv.sendTerminalOperation(ctx, channel.AgentID, agentRequestID, message)
			result = terminalBrowserResult(browserRequestID, agentResult, err)
			if err == nil && !agentResult.OK && agentResult.Error == errAgentNotConnected.Error() {
				_ = protocol.WriteJSON(ctx, conn, result)
				return
			}
		}
		if err := protocol.WriteJSON(ctx, conn, result); err != nil {
			return
		}
	}
}

func terminalAgentRequestID(sessionID string) string {
	return "terminal-" + sessionID + "-" + uuid.NewString()
}

func (srv *Server) sendTerminalOperation(ctx context.Context, agentID, requestID string, message any) (protocol.OperationResult, bool, error) {
	resultCh, err := srv.terminalOps.register(agentID, requestID)
	if err != nil {
		return protocol.OperationResult{}, false, err
	}
	defer srv.terminalOps.cancel(agentID, requestID)
	if err := srv.agentRegistry.send(ctx, agentID, message); err != nil {
		return protocol.OperationResult{}, false, err
	}
	select {
	case result := <-resultCh:
		return result, true, nil
	case <-ctx.Done():
		return protocol.OperationResult{}, true, ctx.Err()
	}
}

func (srv *Server) sendTerminalClose(agentID, channelID, sessionID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	requestID := terminalAgentRequestID(sessionID)
	_, _, _ = srv.sendTerminalOperation(ctx, agentID, requestID, protocol.TerminalClose{
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
