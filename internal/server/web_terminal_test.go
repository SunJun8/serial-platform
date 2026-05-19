package server_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"serial-platform/internal/protocol"
	"serial-platform/internal/serial"
	"serial-platform/internal/server"
)

func TestControlOwnerRejectsSecondSession(t *testing.T) {
	owners := server.NewControlOwner()

	if err := owners.Acquire("channel-1", "web"); err != nil {
		t.Fatalf("Acquire web returned error: %v", err)
	}
	if err := owners.Acquire("channel-1", "rfc2217"); err == nil {
		t.Fatal("Acquire rfc2217 returned nil error, want busy channel")
	}

	owners.Release("channel-1", "rfc2217")
	if err := owners.Acquire("channel-1", "rfc2217"); err == nil {
		t.Fatal("Acquire rfc2217 after wrong-owner release returned nil error, want busy channel")
	}

	owners.Release("channel-1", "web")

	if err := owners.Acquire("channel-1", "rfc2217"); err != nil {
		t.Fatalf("Acquire rfc2217 after release returned error: %v", err)
	}
}

func TestTerminalWebSocketWriteCallsSerialSession(t *testing.T) {
	control := newTerminalFakeControl()
	srv := server.New(server.ServerConfig{
		SerialResolver: func(channelID string) (serial.SerialControl, bool) {
			if channelID != "channel-1" {
				return nil, false
			}
			return control, true
		},
	})
	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn := dialTerminalWebSocket(t, ctx, httpSrv.URL, "channel-1")
	defer conn.Close(websocket.StatusNormalClosure, "")

	err := protocol.WriteJSON(ctx, conn, protocol.TerminalWrite{
		Type:      protocol.MessageTerminalWrite,
		RequestID: "request-1",
		Data:      []byte("show version\n"),
	})
	if err != nil {
		t.Fatalf("protocol.WriteJSON returned error: %v", err)
	}

	var result protocol.OperationResult
	if err := protocol.ReadJSON(ctx, conn, &result); err != nil {
		t.Fatalf("protocol.ReadJSON returned error: %v", err)
	}
	if !result.OK || result.RequestID != "request-1" {
		t.Fatalf("result = %+v, want OK result for request-1", result)
	}

	if got := string(control.session.writeData()); got != "show version\n" {
		t.Fatalf("serial write = %q, want %q", got, "show version\n")
	}
}

func TestTerminalWebSocketRejectsSecondSession(t *testing.T) {
	control := newTerminalFakeControl()
	srv := server.New(server.ServerConfig{
		SerialResolver: func(channelID string) (serial.SerialControl, bool) {
			return control, channelID == "channel-1"
		},
	})
	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	first := dialTerminalWebSocket(t, ctx, httpSrv.URL, "channel-1")
	defer first.Close(websocket.StatusNormalClosure, "")
	writeTerminalAndExpectOK(t, ctx, first, "first-session-ready", []byte("ready\n"))

	second := dialTerminalWebSocket(t, ctx, httpSrv.URL, "channel-1")
	defer second.Close(websocket.StatusNormalClosure, "")

	_, _, err := second.Read(ctx)
	if err == nil {
		t.Fatal("second.Read returned nil error, want busy close")
	}
	if got := websocket.CloseStatus(err); got != websocket.StatusTryAgainLater {
		t.Fatalf("second close status = %v, want %v", got, websocket.StatusTryAgainLater)
	}
}

func TestTerminalWebSocketUnsupportedMessageReturnsError(t *testing.T) {
	control := newTerminalFakeControl()
	srv := server.New(server.ServerConfig{
		SerialResolver: func(channelID string) (serial.SerialControl, bool) {
			return control, channelID == "channel-1"
		},
	})
	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn := dialTerminalWebSocket(t, ctx, httpSrv.URL, "channel-1")
	defer conn.Close(websocket.StatusNormalClosure, "")

	if err := protocol.WriteJSON(ctx, conn, struct {
		Type      protocol.MessageType `json:"type"`
		RequestID string               `json:"request_id"`
	}{
		Type:      protocol.MessageType("terminal_unsupported"),
		RequestID: "request-unsupported",
	}); err != nil {
		t.Fatalf("protocol.WriteJSON returned error: %v", err)
	}

	var result protocol.OperationResult
	if err := protocol.ReadJSON(ctx, conn, &result); err != nil {
		t.Fatalf("protocol.ReadJSON returned error: %v", err)
	}
	if result.Type != protocol.MessageOperationResult || result.RequestID != "request-unsupported" || result.OK || result.Error == "" {
		t.Fatalf("result = %+v, want operation error for unsupported message", result)
	}
}

func dialTerminalWebSocket(t *testing.T, ctx context.Context, serverURL, channelID string) *websocket.Conn {
	t.Helper()

	wsURL := "ws" + strings.TrimPrefix(serverURL, "http") + "/ws/terminal/" + channelID
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}
	return conn
}

func writeTerminalAndExpectOK(t *testing.T, ctx context.Context, conn *websocket.Conn, requestID string, data []byte) {
	t.Helper()

	if err := protocol.WriteJSON(ctx, conn, protocol.TerminalWrite{
		Type:      protocol.MessageTerminalWrite,
		RequestID: requestID,
		Data:      data,
	}); err != nil {
		t.Fatalf("protocol.WriteJSON returned error: %v", err)
	}

	var result protocol.OperationResult
	if err := protocol.ReadJSON(ctx, conn, &result); err != nil {
		t.Fatalf("protocol.ReadJSON returned error: %v", err)
	}
	if !result.OK || result.RequestID != requestID {
		t.Fatalf("result = %+v, want OK result for %s", result, requestID)
	}
}

type terminalFakeControl struct {
	session *terminalFakeSession
}

func newTerminalFakeControl() *terminalFakeControl {
	return &terminalFakeControl{session: &terminalFakeSession{}}
}

func (c *terminalFakeControl) OpenControlSession(context.Context, string) (serial.ControlSession, error) {
	return c.session, nil
}

func (c *terminalFakeControl) Events() <-chan serial.Event {
	return nil
}

type terminalFakeSession struct {
	mu     sync.Mutex
	writes []byte
	closed bool
}

func (s *terminalFakeSession) Write(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes = append(s.writes, data...)
	return nil
}

func (s *terminalFakeSession) SetConfig(serial.Config) error {
	return nil
}

func (s *terminalFakeSession) SetDTR(bool) error {
	return nil
}

func (s *terminalFakeSession) SetRTS(bool) error {
	return nil
}

func (s *terminalFakeSession) SendBreak(time.Duration) error {
	return nil
}

func (s *terminalFakeSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

func (s *terminalFakeSession) writeData() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.writes...)
}
