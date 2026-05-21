package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"serial-platform/internal/protocol"
	"serial-platform/internal/storage"
)

func TestAgentWebSocketInitialChannelSyncUsesLocalConnection(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "meta.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := db.UpsertChannel(storage.Channel{
		ID:              "channel-1",
		AgentID:         "agent-1",
		AutoName:        "agent-1.if00",
		Alias:           "loopback",
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
	}); err != nil {
		t.Fatalf("UpsertChannel returned error: %v", err)
	}

	srv := New(ServerConfig{DB: db})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	serverConns := make(chan *websocket.Conn, 2)
	done := make(chan struct{})
	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		serverConns <- conn
		<-done
		_ = conn.CloseNow()
	}))
	t.Cleanup(func() {
		close(done)
		httpSrv.Close()
	})

	dial := func() *websocket.Conn {
		t.Helper()
		wsURL := "ws" + strings.TrimPrefix(httpSrv.URL, "http")
		conn, _, err := websocket.Dial(ctx, wsURL, nil)
		if err != nil {
			t.Fatalf("websocket.Dial returned error: %v", err)
		}
		return conn
	}
	nextServerConn := func() *websocket.Conn {
		t.Helper()
		select {
		case conn := <-serverConns:
			return conn
		case <-ctx.Done():
			t.Fatalf("timed out waiting for accepted server websocket: %v", ctx.Err())
			return nil
		}
	}

	firstClient := dial()
	defer firstClient.CloseNow()
	firstServer := nextServerConn()
	secondClient := dial()
	defer secondClient.CloseNow()
	secondServer := nextServerConn()

	firstAgentConn := newAgentConnection("agent-1", firstServer, time.Unix(100, 0).UTC())
	secondAgentConn := newAgentConnection("agent-1", secondServer, time.Unix(101, 0).UTC())
	srv.agentRegistry.upsert(firstAgentConn)
	srv.agentRegistry.upsert(secondAgentConn)

	if err := firstAgentConn.send(ctx, protocol.AgentAccepted{
		Type:   protocol.MessageAgentAccepted,
		Status: string(storage.AgentStatusPending),
	}); err != nil {
		t.Fatalf("firstAgentConn.send returned error: %v", err)
	}
	if err := srv.sendInitialChannelSync(ctx, firstAgentConn); err != nil {
		t.Fatalf("sendInitialChannelSync returned error: %v", err)
	}

	var accepted protocol.AgentAccepted
	if err := protocol.ReadJSON(ctx, firstClient, &accepted); err != nil {
		t.Fatalf("accepted ReadJSON returned error: %v", err)
	}
	if accepted.Type != protocol.MessageAgentAccepted {
		t.Fatalf("accepted.Type = %q, want %q", accepted.Type, protocol.MessageAgentAccepted)
	}

	var syncMessage protocol.ChannelSync
	if err := protocol.ReadJSON(ctx, firstClient, &syncMessage); err != nil {
		t.Fatalf("sync ReadJSON returned error: %v", err)
	}
	if syncMessage.Type != protocol.MessageChannelSync ||
		len(syncMessage.Channels) != 1 ||
		syncMessage.Channels[0].ID != "channel-1" {
		t.Fatalf("syncMessage = %+v", syncMessage)
	}
}

func TestAgentWebSocketTunnelErrorCancelsPendingTunnelWait(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "meta.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	srv := New(ServerConfig{DB: db})
	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	wsURL := "ws" + strings.TrimPrefix(httpSrv.URL, "http") + "/ws/agent"
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("websocket.Dial returned error: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	if err := protocol.WriteJSON(ctx, conn, protocol.AgentHello{
		Type:    protocol.MessageAgentHello,
		AgentID: "agent-1",
	}); err != nil {
		t.Fatalf("write agent hello returned error: %v", err)
	}
	var accepted protocol.AgentAccepted
	if err := protocol.ReadJSON(ctx, conn, &accepted); err != nil {
		t.Fatalf("read agent accepted returned error: %v", err)
	}
	var syncMessage protocol.ChannelSync
	if err := protocol.ReadJSON(ctx, conn, &syncMessage); err != nil {
		t.Fatalf("read channel sync returned error: %v", err)
	}

	waited := make(chan error, 1)
	go func() {
		_, err := srv.tunnels.WaitAfterRegister(ctx, "tunnel-1", nil)
		waited <- err
	}()
	waitForTunnelWaiter(t, ctx, srv.tunnels, "tunnel-1")

	if err := protocol.WriteJSON(ctx, conn, protocol.TunnelError{
		Type:     protocol.MessageTunnelError,
		TunnelID: "tunnel-1",
		Error:    "open failed",
	}); err != nil {
		t.Fatalf("write tunnel_error returned error: %v", err)
	}

	select {
	case err := <-waited:
		if err == nil {
			t.Fatal("WaitAfterRegister returned nil error, want tunnel error")
		}
		if errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("WaitAfterRegister error = %v, want immediate tunnel error", err)
		}
		if !errors.Is(err, errTunnelCanceled) {
			t.Fatalf("WaitAfterRegister error = %v, want tunnel cancellation", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("WaitAfterRegister did not return promptly after tunnel_error")
	}
}
