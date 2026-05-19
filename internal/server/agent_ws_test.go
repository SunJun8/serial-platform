package server_test

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"serial-platform/internal/protocol"
	"serial-platform/internal/server"
	"serial-platform/internal/storage"
)

func TestAgentHelloCreatesPendingAgent(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "meta.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	srv := server.New(server.ServerConfig{DB: db})
	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	wsURL := "ws" + strings.TrimPrefix(httpSrv.URL, "http") + "/ws/agent"
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	hello := protocol.AgentHello{
		Type:      protocol.MessageAgentHello,
		AgentID:   "agent-1",
		Hostname:  "node-1",
		Version:   "dev",
		OS:        "linux",
		Arch:      "arm64",
		MachineID: "machine-1",
	}
	if err := protocol.WriteJSON(ctx, conn, hello); err != nil {
		t.Fatalf("protocol.WriteJSON returned error: %v", err)
	}

	var accepted protocol.AgentAccepted
	if err := protocol.ReadJSON(ctx, conn, &accepted); err != nil {
		t.Fatalf("protocol.ReadJSON returned error: %v", err)
	}
	if accepted.Type != protocol.MessageAgentAccepted {
		t.Fatalf("accepted.Type = %q, want %q", accepted.Type, protocol.MessageAgentAccepted)
	}
	if accepted.Status != string(storage.AgentStatusPending) {
		t.Fatalf("accepted.Status = %q, want %q", accepted.Status, storage.AgentStatusPending)
	}

	agents, err := db.ListAgents()
	if err != nil {
		t.Fatalf("ListAgents returned error: %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("len(agents) = %d, want 1", len(agents))
	}
	if agents[0].ID != hello.AgentID || agents[0].Status != storage.AgentStatusPending {
		t.Fatalf("agent = %+v, want ID %q Status %q", agents[0], hello.AgentID, storage.AgentStatusPending)
	}
}

func TestAgentHelloRejectsMalformedHello(t *testing.T) {
	tests := []struct {
		name  string
		hello protocol.AgentHello
	}{
		{
			name: "wrong type",
			hello: protocol.AgentHello{
				Type:    protocol.MessageHeartbeat,
				AgentID: "agent-1",
			},
		},
		{
			name: "empty agent id",
			hello: protocol.AgentHello{
				Type: protocol.MessageAgentHello,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, err := storage.Open(filepath.Join(t.TempDir(), "meta.db"))
			if err != nil {
				t.Fatalf("Open returned error: %v", err)
			}
			t.Cleanup(func() { _ = db.Close() })

			srv := server.New(server.ServerConfig{DB: db})
			httpSrv := httptest.NewServer(srv)
			t.Cleanup(httpSrv.Close)

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()

			wsURL := "ws" + strings.TrimPrefix(httpSrv.URL, "http") + "/ws/agent"
			conn, _, err := websocket.Dial(ctx, wsURL, nil)
			if err != nil {
				t.Fatalf("Dial returned error: %v", err)
			}
			defer conn.Close(websocket.StatusNormalClosure, "")

			if err := protocol.WriteJSON(ctx, conn, tt.hello); err != nil {
				t.Fatalf("protocol.WriteJSON returned error: %v", err)
			}

			_, _, err = conn.Read(ctx)
			if err == nil {
				t.Fatal("conn.Read returned nil error, want rejected websocket close")
			}

			agents, err := db.ListAgents()
			if err != nil {
				t.Fatalf("ListAgents returned error: %v", err)
			}
			if len(agents) != 0 {
				t.Fatalf("len(agents) = %d, want 0", len(agents))
			}
		})
	}
}
