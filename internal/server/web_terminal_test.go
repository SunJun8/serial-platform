package server_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"serial-platform/internal/protocol"
	"serial-platform/internal/serial"
	"serial-platform/internal/server"
	"serial-platform/internal/storage"
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

func TestTerminalWebSocketSendsWriteThroughAgentTunnel(t *testing.T) {
	db := newTerminalTestDB(t)
	if err := db.UpsertChannel(terminalTestChannel("channel-1", "agent-1")); err != nil {
		t.Fatalf("UpsertChannel returned error: %v", err)
	}
	srv := server.New(server.ServerConfig{
		DB: db,
		SerialResolver: func(string) (serial.SerialControl, bool) {
			t.Fatal("web terminal must not resolve local serial control on server")
			return nil, false
		},
	})
	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	agentConn := connectTerminalTestAgent(t, ctx, httpSrv.URL, "agent-1")
	defer agentConn.Close(websocket.StatusNormalClosure, "")

	conn := dialTerminalWebSocket(t, ctx, httpSrv.URL, "channel-1")
	defer conn.Close(websocket.StatusNormalClosure, "")

	open := readTerminalAgentMessage(t, ctx, agentConn)
	if open.Type != protocol.MessageTerminalOpen ||
		open.SessionID == "" ||
		open.ChannelID != "channel-1" {
		t.Fatalf("terminal open = %+v, want session for channel-1", open)
	}

	err := protocol.WriteJSON(ctx, conn, protocol.TerminalWrite{
		Type:      protocol.MessageTerminalWrite,
		RequestID: "request-1",
		Data:      []byte("show version\n"),
	})
	if err != nil {
		t.Fatalf("protocol.WriteJSON returned error: %v", err)
	}

	write := readTerminalAgentMessage(t, ctx, agentConn)
	if write.Type != protocol.MessageTerminalWrite ||
		write.RequestID != "request-1" ||
		write.SessionID != open.SessionID ||
		write.ChannelID != "channel-1" ||
		string(write.Data) != "show version\n" {
		t.Fatalf("terminal write = %+v, want request through agent session %s", write, open.SessionID)
	}

	var result protocol.OperationResult
	if err := protocol.ReadJSON(ctx, conn, &result); err != nil {
		t.Fatalf("protocol.ReadJSON returned error: %v", err)
	}
	if !result.OK || result.RequestID != "request-1" {
		t.Fatalf("result = %+v, want OK result for request-1", result)
	}
}

type terminalAgentMessage struct {
	Type      protocol.MessageType `json:"type"`
	RequestID string               `json:"request_id"`
	SessionID string               `json:"session_id"`
	ChannelID string               `json:"channel_id"`
	Data      []byte               `json:"data"`
}

func TestTerminalWebSocketRejectsSecondSession(t *testing.T) {
	db := newTerminalTestDB(t)
	if err := db.UpsertChannel(terminalTestChannel("channel-1", "agent-1")); err != nil {
		t.Fatalf("UpsertChannel returned error: %v", err)
	}
	srv := server.New(server.ServerConfig{DB: db})
	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	agentConn := connectTerminalTestAgent(t, ctx, httpSrv.URL, "agent-1")
	defer agentConn.Close(websocket.StatusNormalClosure, "")

	first := dialTerminalWebSocket(t, ctx, httpSrv.URL, "channel-1")
	defer first.Close(websocket.StatusNormalClosure, "")
	_ = readTerminalAgentMessage(t, ctx, agentConn)
	writeTerminalAndExpectOK(t, ctx, first, "first-session-ready", []byte("ready\n"))
	_ = readTerminalAgentMessage(t, ctx, agentConn)

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
	db := newTerminalTestDB(t)
	if err := db.UpsertChannel(terminalTestChannel("channel-1", "agent-1")); err != nil {
		t.Fatalf("UpsertChannel returned error: %v", err)
	}
	srv := server.New(server.ServerConfig{DB: db})
	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	agentConn := connectTerminalTestAgent(t, ctx, httpSrv.URL, "agent-1")
	defer agentConn.Close(websocket.StatusNormalClosure, "")

	conn := dialTerminalWebSocket(t, ctx, httpSrv.URL, "channel-1")
	defer conn.Close(websocket.StatusNormalClosure, "")
	_ = readTerminalAgentMessage(t, ctx, agentConn)

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

func TestTerminalWebSocketDisconnectSendsCloseAndReleasesOwner(t *testing.T) {
	db := newTerminalTestDB(t)
	if err := db.UpsertChannel(terminalTestChannel("channel-1", "agent-1")); err != nil {
		t.Fatalf("UpsertChannel returned error: %v", err)
	}
	srv := server.New(server.ServerConfig{DB: db})
	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	agentConn := connectTerminalTestAgent(t, ctx, httpSrv.URL, "agent-1")
	defer agentConn.Close(websocket.StatusNormalClosure, "")

	first := dialTerminalWebSocket(t, ctx, httpSrv.URL, "channel-1")
	open := readTerminalAgentMessage(t, ctx, agentConn)
	if err := first.Close(websocket.StatusNormalClosure, ""); err != nil {
		t.Fatalf("first.Close returned error: %v", err)
	}

	closeMsg := readTerminalAgentMessage(t, ctx, agentConn)
	if closeMsg.Type != protocol.MessageTerminalClose ||
		closeMsg.SessionID != open.SessionID ||
		closeMsg.ChannelID != "channel-1" {
		t.Fatalf("terminal close = %+v, want close for session %s", closeMsg, open.SessionID)
	}

	second := dialTerminalWebSocket(t, ctx, httpSrv.URL, "channel-1")
	defer second.Close(websocket.StatusNormalClosure, "")
	secondOpen := readTerminalAgentMessage(t, ctx, agentConn)
	if secondOpen.Type != protocol.MessageTerminalOpen || secondOpen.SessionID == open.SessionID {
		t.Fatalf("second terminal open = %+v, want a new session", secondOpen)
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

func connectTerminalTestAgent(t *testing.T, ctx context.Context, serverURL, agentID string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(serverURL, "http") + "/ws/agent"
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("agent Dial returned error: %v", err)
	}
	if err := protocol.WriteJSON(ctx, conn, protocol.AgentHello{
		Type:    protocol.MessageAgentHello,
		AgentID: agentID,
	}); err != nil {
		t.Fatalf("write agent hello returned error: %v", err)
	}
	var accepted protocol.AgentAccepted
	if err := protocol.ReadJSON(ctx, conn, &accepted); err != nil {
		t.Fatalf("read agent accepted returned error: %v", err)
	}
	var syncMessage protocol.ChannelSync
	if err := protocol.ReadJSON(ctx, conn, &syncMessage); err != nil {
		t.Fatalf("read initial channel sync returned error: %v", err)
	}
	return conn
}

func readTerminalAgentMessage(t *testing.T, ctx context.Context, conn *websocket.Conn) terminalAgentMessage {
	t.Helper()
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read agent control message returned error: %v", err)
	}
	var msg terminalAgentMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("unmarshal agent control message returned error: %v", err)
	}
	return msg
}

func terminalTestChannel(id, agentID string) storage.Channel {
	return storage.Channel{
		ID:              id,
		AgentID:         agentID,
		AutoName:        agentID + ".if00",
		Alias:           "console",
		Role:            "console",
		IDPath:          "id-path",
		IDPathTag:       "id-tag",
		RFC2217Port:     7001,
		Status:          storage.ChannelStatusOffline,
		DefaultBaud:     115200,
		DefaultDataBits: 8,
		DefaultParity:   "N",
		DefaultStopBits: 1,
		DefaultFlow:     "none",
		UpdatedAt:       time.Unix(1700000000, 0).UTC(),
	}
}

func newTerminalTestDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "meta.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
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
