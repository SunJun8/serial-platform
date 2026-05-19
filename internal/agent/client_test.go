package agent_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"serial-platform/internal/agent"
	"serial-platform/internal/protocol"
)

func TestClientConnectKeepsConnectionOpenUntilClose(t *testing.T) {
	helloReceived := make(chan protocol.AgentHello, 1)
	connClosed := make(chan struct{})

	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ws/agent" {
			http.NotFound(w, r)
			return
		}

		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		var hello protocol.AgentHello
		if err := protocol.ReadJSON(r.Context(), conn, &hello); err != nil {
			return
		}
		helloReceived <- hello

		if err := protocol.WriteJSON(r.Context(), conn, protocol.AgentAccepted{
			Type:   protocol.MessageAgentAccepted,
			Status: "pending",
		}); err != nil {
			return
		}

		_, _, _ = conn.Read(r.Context())
		close(connClosed)
	}))
	t.Cleanup(httpSrv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	client := &agent.Client{Config: agent.Config{
		ServerURL: httpSrv.URL,
		AgentID:   "agent-1",
	}}
	status, err := client.Connect(ctx)
	if err != nil {
		t.Fatalf("Connect returned error: %v", err)
	}
	if status != "pending" {
		t.Fatalf("status = %q, want pending", status)
	}

	select {
	case <-connClosed:
		t.Fatal("connection closed before explicit Close")
	case <-time.After(100 * time.Millisecond):
	}

	if err := client.Close(ctx); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	select {
	case <-connClosed:
	case <-ctx.Done():
		t.Fatal("server did not observe client close")
	}

	hello := <-helloReceived
	if hello.Type != protocol.MessageAgentHello {
		t.Fatalf("hello.Type = %q, want %q", hello.Type, protocol.MessageAgentHello)
	}
	if hello.AgentID != "agent-1" {
		t.Fatalf("hello.AgentID = %q, want agent-1", hello.AgentID)
	}
	if hello.Hostname == "" {
		t.Fatal("hello.Hostname is empty")
	}
	if hello.OS != runtime.GOOS {
		t.Fatalf("hello.OS = %q, want %q", hello.OS, runtime.GOOS)
	}
	if hello.Arch != runtime.GOARCH {
		t.Fatalf("hello.Arch = %q, want %q", hello.Arch, runtime.GOARCH)
	}
}
