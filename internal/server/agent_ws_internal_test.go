package server

import (
	"context"
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
