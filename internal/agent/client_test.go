package agent_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"serial-platform/internal/agent"
	"serial-platform/internal/protocol"
	"serial-platform/internal/serial"
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

func TestClientSendLogFramesWritesBinaryFrames(t *testing.T) {
	received := make(chan protocol.LogFrame, 1)

	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ws/logs" {
			http.NotFound(w, r)
			return
		}

		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		messageType, payload, err := conn.Read(r.Context())
		if err != nil {
			return
		}
		if messageType != websocket.MessageBinary {
			return
		}
		frame, err := protocol.DecodeLogFrame(payload)
		if err != nil {
			return
		}
		received <- frame
	}))
	t.Cleanup(httpSrv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	frames := make(chan protocol.LogFrame, 1)
	frames <- protocol.LogFrame{
		ChannelID:   "channel-1",
		Seq:         1,
		TimestampNS: time.Unix(1700000000, 0).UnixNano(),
		Direction:   protocol.DirectionRX,
		Flags:       protocol.FlagRaw,
		Payload:     []byte("boot\n"),
	}
	close(frames)

	client := &agent.Client{Config: agent.Config{ServerURL: httpSrv.URL}}
	if err := client.SendLogFrames(ctx, frames); err != nil {
		t.Fatalf("SendLogFrames returned error: %v", err)
	}

	select {
	case frame := <-received:
		if frame.ChannelID != "channel-1" {
			t.Fatalf("ChannelID = %q, want channel-1", frame.ChannelID)
		}
		if string(frame.Payload) != "boot\n" {
			t.Fatalf("Payload = %q, want boot newline", frame.Payload)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for log frame")
	}
}

func TestClientSendLogFramesReturnsCloseErrorWhenNoFramesSent(t *testing.T) {
	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ws/logs" {
			http.NotFound(w, r)
			return
		}

		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		conn.CloseNow()
	}))
	t.Cleanup(httpSrv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	frames := make(chan protocol.LogFrame)
	close(frames)

	client := &agent.Client{Config: agent.Config{ServerURL: httpSrv.URL}}
	if err := client.SendLogFrames(ctx, frames); err == nil {
		t.Fatal("SendLogFrames returned nil error, want close error")
	}
}

func TestRuntimeScansAndForwardsReconciledEvents(t *testing.T) {
	events := make(chan serial.Event)
	forwarded := make(chan serial.Event, 1)
	scanned := make(chan struct{}, 1)
	reconciled := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runtime := agent.NewRuntime(agent.RuntimeConfig{
		ScanInterval: time.Hour,
		Discover: func(agent.DiscoveryConfig) ([]agent.DiscoveredDevice, error) {
			select {
			case scanned <- struct{}{}:
			default:
			}
			return []agent.DiscoveredDevice{{DevName: "/dev/ttyUSB0", IDPath: "id-path-1", PermissionOK: true}}, nil
		},
		Reconciler: &runtimeFakeReconciler{
			result: agent.ReconcileResult{Events: []<-chan serial.Event{events}},
			done:   reconciled,
		},
		ForwardEvents: func(ctx context.Context, in <-chan serial.Event) error {
			select {
			case event := <-in:
				forwarded <- event
			case <-ctx.Done():
				return ctx.Err()
			}
			return nil
		},
	})

	done := make(chan error, 1)
	go func() {
		done <- runtime.Run(ctx)
	}()

	select {
	case <-scanned:
	case <-time.After(time.Second):
		t.Fatal("runtime did not scan devices")
	}
	select {
	case <-reconciled:
	case <-time.After(time.Second):
		t.Fatal("runtime did not reconcile scan result")
	}

	events <- serial.Event{ChannelID: "channel-1", Direction: serial.DirectionRX, Timestamp: time.Unix(1, 0), Data: []byte("boot\n")}
	select {
	case event := <-forwarded:
		if event.ChannelID != "channel-1" {
			t.Fatalf("forwarded ChannelID = %q, want channel-1", event.ChannelID)
		}
	case <-time.After(time.Second):
		t.Fatal("runtime did not forward reconciled event stream")
	}

	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("Run error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runtime did not stop after context cancellation")
	}
}

type runtimeFakeReconciler struct {
	mu       sync.Mutex
	channels []agent.ChannelConfig
	devices  []agent.DiscoveredDevice
	result   agent.ReconcileResult
	done     chan<- struct{}
}

func (r *runtimeFakeReconciler) Reconcile(_ context.Context, channels []agent.ChannelConfig, devices []agent.DiscoveredDevice) agent.ReconcileResult {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.channels = append([]agent.ChannelConfig(nil), channels...)
	r.devices = append([]agent.DiscoveredDevice(nil), devices...)
	select {
	case r.done <- struct{}{}:
	default:
	}
	return r.result
}
