package server

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

func TestTunnelRegistryPairsServerAndAgent(t *testing.T) {
	registry := NewTunnelRegistry(time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	type waitResult struct {
		conn net.Conn
		err  error
	}
	waited := make(chan waitResult, 1)
	go func() {
		conn, err := registry.Wait(ctx, "tunnel-1")
		waited <- waitResult{conn: conn, err: err}
	}()

	peer, attached := net.Pipe()
	t.Cleanup(func() { _ = peer.Close() })
	t.Cleanup(func() { _ = attached.Close() })

	attachTunnel(t, ctx, registry, "tunnel-1", attached)

	var result waitResult
	select {
	case result = <-waited:
	case <-ctx.Done():
		t.Fatal("timeout waiting for tunnel registry")
	}
	if result.err != nil {
		t.Fatalf("Wait returned error: %v", result.err)
	}

	roundTripConnBytes(t, peer, result.conn, "server-to-agent", "agent-to-server")
}

func TestTunnelRegistryWaitTimesOut(t *testing.T) {
	registry := NewTunnelRegistry(10 * time.Millisecond)

	start := time.Now()
	conn, err := registry.Wait(context.Background(), "missing-tunnel")
	if err == nil {
		t.Fatal("Wait returned nil error, want timeout")
	}
	if conn != nil {
		t.Fatalf("Wait conn = %v, want nil", conn)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Wait error = %v, want context deadline exceeded", err)
	}
	if elapsed := time.Since(start); elapsed < 10*time.Millisecond {
		t.Fatalf("Wait returned too early after %v", elapsed)
	}
}

func TestTunnelWebSocketConnCloseUsesNetConnClose(t *testing.T) {
	accepted := make(chan *WSByteConn, 1)
	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}

		byteConn := NewWSByteConn(r.Context(), conn)
		accepted <- byteConn
		<-byteConn.Done()
	}))
	t.Cleanup(httpSrv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	client, _, err := websocket.Dial(ctx, tunnelWebSocketURL(httpSrv.URL, ""), nil)
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}
	t.Cleanup(func() { _ = client.CloseNow() })

	var byteConn *WSByteConn
	select {
	case byteConn = <-accepted:
	case <-ctx.Done():
		t.Fatal("timeout waiting for accepted websocket")
	}

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- byteConn.Close()
	}()

	_, _, err = client.Read(ctx)
	if websocket.CloseStatus(err) != websocket.StatusNormalClosure {
		t.Fatalf("client Read error = %v, want normal websocket close", err)
	}

	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("WSByteConn.Close returned error: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for WSByteConn.Close")
	}

	select {
	case <-byteConn.Done():
	default:
		t.Fatal("WSByteConn.Done is not closed after Close")
	}
}

func TestTunnelWebSocketRouteAttachesWaiterAndBridgesBytes(t *testing.T) {
	srv := New(ServerConfig{})
	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	type waitResult struct {
		conn net.Conn
		err  error
	}
	waited := make(chan waitResult, 1)
	go func() {
		conn, err := srv.tunnels.Wait(ctx, "tunnel-1")
		waited <- waitResult{conn: conn, err: err}
	}()
	waitForTunnelWaiter(t, ctx, srv.tunnels, "tunnel-1")

	clientWS, _, err := websocket.Dial(ctx, tunnelWebSocketURL(httpSrv.URL, "tunnel-1"), nil)
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}
	t.Cleanup(func() { _ = clientWS.CloseNow() })

	var result waitResult
	select {
	case result = <-waited:
	case <-ctx.Done():
		t.Fatal("timeout waiting for tunnel attach")
	}
	if result.err != nil {
		t.Fatalf("Wait returned error: %v", result.err)
	}

	clientConn := websocket.NetConn(ctx, clientWS, websocket.MessageBinary)
	t.Cleanup(func() { _ = clientConn.Close() })
	t.Cleanup(func() { _ = result.conn.Close() })

	roundTripConnBytes(t, clientConn, result.conn, "client-to-server", "server-to-client")
}

func attachTunnel(t *testing.T, ctx context.Context, registry *TunnelRegistry, tunnelID string, conn net.Conn) {
	t.Helper()

	var lastErr error
	for {
		if err := registry.Attach(tunnelID, conn); err != nil {
			lastErr = err
		} else {
			return
		}

		select {
		case <-ctx.Done():
			t.Fatalf("Attach did not pair before timeout, last error: %v", lastErr)
		case <-time.After(time.Millisecond):
		}
	}
}

func waitForTunnelWaiter(t *testing.T, ctx context.Context, registry *TunnelRegistry, tunnelID string) {
	t.Helper()

	for {
		registry.mu.Lock()
		_, exists := registry.waiters[tunnelID]
		registry.mu.Unlock()
		if exists {
			return
		}

		select {
		case <-ctx.Done():
			t.Fatalf("waiter for %q was not registered before timeout", tunnelID)
		case <-time.After(time.Millisecond):
		}
	}
}

func tunnelWebSocketURL(serverURL, tunnelID string) string {
	wsURL := "ws" + strings.TrimPrefix(serverURL, "http")
	if tunnelID != "" {
		wsURL += "/ws/tunnel/" + tunnelID
	}
	return wsURL
}

func roundTripConnBytes(t *testing.T, left, right net.Conn, leftToRight, rightToLeft string) {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	if err := left.SetDeadline(deadline); err != nil {
		t.Fatalf("left SetDeadline returned error: %v", err)
	}
	if err := right.SetDeadline(deadline); err != nil {
		t.Fatalf("right SetDeadline returned error: %v", err)
	}

	writeDone := make(chan error, 1)
	go func() {
		_, err := left.Write([]byte(leftToRight))
		writeDone <- err
	}()
	got := make([]byte, len(leftToRight))
	if _, err := io.ReadFull(right, got); err != nil {
		t.Fatalf("right ReadFull returned error: %v", err)
	}
	if string(got) != leftToRight {
		t.Fatalf("right read %q, want %q", got, leftToRight)
	}
	if err := <-writeDone; err != nil {
		t.Fatalf("left Write returned error: %v", err)
	}

	go func() {
		_, err := right.Write([]byte(rightToLeft))
		writeDone <- err
	}()
	got = make([]byte, len(rightToLeft))
	if _, err := io.ReadFull(left, got); err != nil {
		t.Fatalf("left ReadFull returned error: %v", err)
	}
	if string(got) != rightToLeft {
		t.Fatalf("left read %q, want %q", got, rightToLeft)
	}
	if err := <-writeDone; err != nil {
		t.Fatalf("right Write returned error: %v", err)
	}
}
