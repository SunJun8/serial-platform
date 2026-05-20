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

func TestAgentHelloPreservesActiveStatus(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "meta.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.UpsertAgent(storage.Agent{
		ID:        "agent-1",
		Name:      "node-1",
		Status:    storage.AgentStatusActive,
		Hostname:  "node-1",
		OS:        "linux",
		Arch:      "arm64",
		MachineID: "machine-1",
		UpdatedAt: time.Unix(100, 0).UTC(),
	}); err != nil {
		t.Fatalf("UpsertAgent returned error: %v", err)
	}

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

	if err := protocol.WriteJSON(ctx, conn, protocol.AgentHello{
		Type:      protocol.MessageAgentHello,
		AgentID:   "agent-1",
		Hostname:  "node-1",
		OS:        "linux",
		Arch:      "arm64",
		MachineID: "machine-1",
	}); err != nil {
		t.Fatalf("protocol.WriteJSON returned error: %v", err)
	}

	var accepted protocol.AgentAccepted
	if err := protocol.ReadJSON(ctx, conn, &accepted); err != nil {
		t.Fatalf("protocol.ReadJSON returned error: %v", err)
	}
	if accepted.Status != string(storage.AgentStatusActive) {
		t.Fatalf("accepted.Status = %q, want %q", accepted.Status, storage.AgentStatusActive)
	}

	agents, err := db.ListAgents()
	if err != nil {
		t.Fatalf("ListAgents returned error: %v", err)
	}
	if agents[0].Status != storage.AgentStatusActive {
		t.Fatalf("agent status = %q, want %q", agents[0].Status, storage.AgentStatusActive)
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

func TestAgentHelloRejectsBinaryHello(t *testing.T) {
	db := newAgentWSTestDB(t)
	srv := server.New(server.ServerConfig{DB: db})
	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn := dialAgentWS(t, ctx, httpSrv.URL)
	defer conn.Close(websocket.StatusNormalClosure, "")

	payload, err := json.Marshal(protocol.AgentHello{
		Type:      protocol.MessageAgentHello,
		AgentID:   "agent-1",
		Hostname:  "node-1",
		Version:   "dev",
		OS:        "linux",
		Arch:      "arm64",
		MachineID: "machine-1",
	})
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}
	if err := conn.Write(ctx, websocket.MessageBinary, payload); err != nil {
		t.Fatalf("conn.Write returned error: %v", err)
	}

	_, _, err = conn.Read(ctx)
	if err == nil {
		t.Fatal("conn.Read returned nil error, want rejected websocket close")
	}
	if got := websocket.CloseStatus(err); got != websocket.StatusPolicyViolation {
		t.Fatalf("conn.Read close status = %v, want %v", got, websocket.StatusPolicyViolation)
	}

	agents, err := db.ListAgents()
	if err != nil {
		t.Fatalf("ListAgents returned error: %v", err)
	}
	if len(agents) != 0 {
		t.Fatalf("len(agents) = %d, want 0", len(agents))
	}
}

func TestAgentWebSocketStoresCandidatesFromDeviceSnapshot(t *testing.T) {
	db := newAgentWSTestDB(t)
	srv := server.New(server.ServerConfig{DB: db})
	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn := dialAgentWS(t, ctx, httpSrv.URL)
	defer conn.Close(websocket.StatusNormalClosure, "")

	writeAgentHelloAndReadAccepted(t, ctx, conn, "agent-1")

	err := protocol.WriteJSON(ctx, conn, protocol.DeviceSnapshot{
		Type:    protocol.MessageDeviceSnapshot,
		AgentID: "agent-1",
		Devices: []protocol.DeviceIdentity{
			{
				DevName:      "/dev/ttyUSB0",
				IDPath:       "id-path",
				IDPathTag:    "id-tag",
				PermissionOK: true,
			},
		},
	})
	if err != nil {
		t.Fatalf("protocol.WriteJSON returned error: %v", err)
	}

	requireEventually(t, func() bool {
		candidates, err := db.ListCandidates()
		if err != nil {
			t.Fatalf("ListCandidates returned error: %v", err)
		}
		return len(candidates) == 1 &&
			candidates[0].AgentID == "agent-1" &&
			candidates[0].DevName == "/dev/ttyUSB0" &&
			candidates[0].IDPath == "id-path" &&
			candidates[0].IDPathTag == "id-tag"
	})
}

func TestAgentWebSocketRejectsBinaryControlMessage(t *testing.T) {
	db := newAgentWSTestDB(t)
	srv := server.New(server.ServerConfig{DB: db})
	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn := dialAgentWS(t, ctx, httpSrv.URL)
	defer conn.Close(websocket.StatusNormalClosure, "")

	writeAgentHelloAndReadAccepted(t, ctx, conn, "agent-1")
	var initialSync protocol.ChannelSync
	if err := protocol.ReadJSON(ctx, conn, &initialSync); err != nil {
		t.Fatalf("initial sync ReadJSON returned error: %v", err)
	}

	payload, err := json.Marshal(protocol.DeviceSnapshot{
		Type:    protocol.MessageDeviceSnapshot,
		AgentID: "agent-1",
		Devices: []protocol.DeviceIdentity{
			{DevName: "/dev/ttyUSB0", IDPath: "binary-id-path"},
		},
	})
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}
	if err := conn.Write(ctx, websocket.MessageBinary, payload); err != nil {
		t.Fatalf("conn.Write returned error: %v", err)
	}

	readCtx, readCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer readCancel()
	_, _, err = conn.Read(readCtx)
	if err == nil {
		t.Fatal("conn.Read returned nil error, want server to reject binary control message")
	}
	if got := websocket.CloseStatus(err); got != websocket.StatusPolicyViolation {
		t.Fatalf("conn.Read close status = %v, want %v", got, websocket.StatusPolicyViolation)
	}

	candidates, err := db.ListCandidates()
	if err != nil {
		t.Fatalf("ListCandidates returned error: %v", err)
	}
	if len(candidates) != 0 {
		t.Fatalf("len(candidates) = %d, want 0", len(candidates))
	}
}

func TestAgentRegistryCanSendChannelSync(t *testing.T) {
	db := newAgentWSTestDB(t)
	if err := db.UpsertChannel(storage.Channel{
		ID:              "channel-1",
		AgentID:         "agent-1",
		AutoName:        "agent-1.if00",
		Alias:           "loopback",
		Role:            "console",
		DevName:         "/dev/ttyUSB0",
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
	srv := server.New(server.ServerConfig{DB: db})
	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn := dialAgentWS(t, ctx, httpSrv.URL)
	defer conn.Close(websocket.StatusNormalClosure, "")

	writeAgentHelloAndReadAccepted(t, ctx, conn, "agent-1")

	var syncMessage protocol.ChannelSync
	if err := protocol.ReadJSON(ctx, conn, &syncMessage); err != nil {
		t.Fatalf("protocol.ReadJSON returned error: %v", err)
	}
	if syncMessage.Type != protocol.MessageChannelSync ||
		len(syncMessage.Channels) != 1 ||
		syncMessage.Channels[0].ID != "channel-1" ||
		syncMessage.Channels[0].DefaultBaud != 115200 {
		t.Fatalf("syncMessage = %+v", syncMessage)
	}
}

func TestAgentWebSocketUpdatesChannelStatus(t *testing.T) {
	db := newAgentWSTestDB(t)
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
	srv := server.New(server.ServerConfig{DB: db})
	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn := dialAgentWS(t, ctx, httpSrv.URL)
	defer conn.Close(websocket.StatusNormalClosure, "")

	writeAgentHelloAndReadAccepted(t, ctx, conn, "agent-1")
	var initialSync protocol.ChannelSync
	if err := protocol.ReadJSON(ctx, conn, &initialSync); err != nil {
		t.Fatalf("initial sync ReadJSON returned error: %v", err)
	}

	if err := protocol.WriteJSON(ctx, conn, protocol.ChannelStatusUpdate{
		Type:    protocol.MessageChannelStatus,
		AgentID: "agent-1",
		Statuses: []protocol.ChannelRuntimeStatus{
			{
				ChannelID:    "channel-1",
				Status:       "error",
				DevName:      "/dev/ttyUSB0",
				ErrorMessage: "permission denied",
			},
		},
	}); err != nil {
		t.Fatalf("protocol.WriteJSON returned error: %v", err)
	}

	requireEventually(t, func() bool {
		channel, err := db.GetChannel("channel-1")
		if err != nil {
			t.Fatalf("GetChannel returned error: %v", err)
		}
		return channel.Status == storage.ChannelStatusError &&
			channel.DevName == "/dev/ttyUSB0" &&
			channel.ErrorMessage == "permission denied"
	})
}

func TestAgentWebSocketIgnoresChannelStatusForAnotherAgent(t *testing.T) {
	db := newAgentWSTestDB(t)
	if err := db.UpsertChannel(agentWSTestChannel("channel-owned", "agent-1", "owned", 7001)); err != nil {
		t.Fatalf("UpsertChannel owned returned error: %v", err)
	}
	otherChannel := agentWSTestChannel("channel-other", "agent-2", "other", 7002)
	otherChannel.DevName = "/dev/ttyUSB9"
	if err := db.UpsertChannel(otherChannel); err != nil {
		t.Fatalf("UpsertChannel other returned error: %v", err)
	}
	srv := server.New(server.ServerConfig{DB: db})
	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn := dialAgentWS(t, ctx, httpSrv.URL)
	defer conn.Close(websocket.StatusNormalClosure, "")

	writeAgentHelloAndReadAccepted(t, ctx, conn, "agent-1")
	var initialSync protocol.ChannelSync
	if err := protocol.ReadJSON(ctx, conn, &initialSync); err != nil {
		t.Fatalf("initial sync ReadJSON returned error: %v", err)
	}

	if err := protocol.WriteJSON(ctx, conn, protocol.ChannelStatusUpdate{
		Type:    protocol.MessageChannelStatus,
		AgentID: "agent-1",
		Statuses: []protocol.ChannelRuntimeStatus{
			{
				ChannelID: "channel-other",
				Status:    "error",
				DevName:   "/dev/ttyUSB0",
			},
			{
				ChannelID: "channel-owned",
				Status:    "busy",
				DevName:   "/dev/ttyUSB1",
			},
		},
	}); err != nil {
		t.Fatalf("protocol.WriteJSON returned error: %v", err)
	}

	requireEventually(t, func() bool {
		channel, err := db.GetChannel("channel-owned")
		if err != nil {
			t.Fatalf("GetChannel owned returned error: %v", err)
		}
		return channel.Status == storage.ChannelStatusBusy &&
			channel.DevName == "/dev/ttyUSB1"
	})
	other, err := db.GetChannel("channel-other")
	if err != nil {
		t.Fatalf("GetChannel other returned error: %v", err)
	}
	if other.Status != storage.ChannelStatusOffline ||
		other.DevName != "/dev/ttyUSB9" ||
		other.ErrorMessage != "" {
		t.Fatalf("other channel = %+v, want original status/dev/error", other)
	}
}

func TestAgentWebSocketIgnoresInvalidChannelStatus(t *testing.T) {
	db := newAgentWSTestDB(t)
	invalidChannel := agentWSTestChannel("channel-invalid", "agent-1", "invalid", 7001)
	invalidChannel.DevName = "/dev/ttyUSB0"
	if err := db.UpsertChannel(invalidChannel); err != nil {
		t.Fatalf("UpsertChannel invalid returned error: %v", err)
	}
	if err := db.UpsertChannel(agentWSTestChannel("channel-barrier", "agent-1", "barrier", 7002)); err != nil {
		t.Fatalf("UpsertChannel barrier returned error: %v", err)
	}
	srv := server.New(server.ServerConfig{DB: db})
	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn := dialAgentWS(t, ctx, httpSrv.URL)
	defer conn.Close(websocket.StatusNormalClosure, "")

	writeAgentHelloAndReadAccepted(t, ctx, conn, "agent-1")
	var initialSync protocol.ChannelSync
	if err := protocol.ReadJSON(ctx, conn, &initialSync); err != nil {
		t.Fatalf("initial sync ReadJSON returned error: %v", err)
	}

	if err := protocol.WriteJSON(ctx, conn, protocol.ChannelStatusUpdate{
		Type:    protocol.MessageChannelStatus,
		AgentID: "agent-1",
		Statuses: []protocol.ChannelRuntimeStatus{
			{
				ChannelID: "channel-invalid",
				Status:    "not-a-status",
				DevName:   "/dev/ttyUSB1",
			},
			{
				ChannelID: "channel-invalid",
				Status:    "",
				DevName:   "/dev/ttyUSB2",
			},
			{
				ChannelID: "channel-barrier",
				Status:    "online",
				DevName:   "/dev/ttyUSB3",
			},
		},
	}); err != nil {
		t.Fatalf("protocol.WriteJSON returned error: %v", err)
	}

	requireEventually(t, func() bool {
		channel, err := db.GetChannel("channel-barrier")
		if err != nil {
			t.Fatalf("GetChannel barrier returned error: %v", err)
		}
		return channel.Status == storage.ChannelStatusOnline &&
			channel.DevName == "/dev/ttyUSB3"
	})
	invalid, err := db.GetChannel("channel-invalid")
	if err != nil {
		t.Fatalf("GetChannel invalid returned error: %v", err)
	}
	if invalid.Status != storage.ChannelStatusOffline ||
		invalid.DevName != "/dev/ttyUSB0" ||
		invalid.ErrorMessage != "" {
		t.Fatalf("invalid channel = %+v, want original status/dev/error", invalid)
	}
}

func agentWSTestChannel(id, agentID, alias string, port int) storage.Channel {
	return storage.Channel{
		ID:              id,
		AgentID:         agentID,
		AutoName:        agentID + "." + alias,
		Alias:           alias,
		Role:            "console",
		IDPath:          alias + "-id-path",
		IDPathTag:       alias + "-id-tag",
		RFC2217Port:     port,
		Status:          storage.ChannelStatusOffline,
		DefaultBaud:     115200,
		DefaultDataBits: 8,
		DefaultParity:   "N",
		DefaultStopBits: 1,
		DefaultFlow:     "none",
		UpdatedAt:       time.Unix(1700000000, 0).UTC(),
	}
}

func newAgentWSTestDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "meta.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func dialAgentWS(t *testing.T, ctx context.Context, serverURL string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(serverURL, "http") + "/ws/agent"
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("websocket.Dial returned error: %v", err)
	}
	return conn
}

func writeAgentHelloAndReadAccepted(t *testing.T, ctx context.Context, conn *websocket.Conn, agentID string) protocol.AgentAccepted {
	t.Helper()
	if err := protocol.WriteJSON(ctx, conn, protocol.AgentHello{
		Type:      protocol.MessageAgentHello,
		AgentID:   agentID,
		Hostname:  "node-1",
		Version:   "dev",
		OS:        "linux",
		Arch:      "arm64",
		MachineID: "machine-1",
	}); err != nil {
		t.Fatalf("protocol.WriteJSON returned error: %v", err)
	}

	var accepted protocol.AgentAccepted
	if err := protocol.ReadJSON(ctx, conn, &accepted); err != nil {
		t.Fatalf("protocol.ReadJSON returned error: %v", err)
	}
	if accepted.Type != protocol.MessageAgentAccepted {
		t.Fatalf("accepted.Type = %q, want %q", accepted.Type, protocol.MessageAgentAccepted)
	}
	return accepted
}

func requireEventually(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if condition() {
			return
		}
		select {
		case <-deadline:
			t.Fatal("condition was not met before timeout")
		case <-ticker.C:
		}
	}
}
