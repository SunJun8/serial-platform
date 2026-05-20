package agent_test

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

	"serial-platform/internal/agent"
)

func TestAgentTunnelDialsServerAndBridgesBytes(t *testing.T) {
	serverConn := make(chan *websocket.Conn, 1)
	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ws/tunnel/tunnel-1" {
			http.NotFound(w, r)
			return
		}

		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		serverConn <- conn
	}))
	t.Cleanup(httpSrv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	dialer := agent.TunnelDialer{ServerURL: httpSrv.URL}
	clientConn, err := dialer.Dial(ctx, "tunnel-1")
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}
	t.Cleanup(func() { _ = clientConn.Close(websocket.StatusNormalClosure, "") })

	var serverWS *websocket.Conn
	select {
	case serverWS = <-serverConn:
	case <-ctx.Done():
		t.Fatal("timeout waiting for tunnel websocket")
	}
	t.Cleanup(func() { _ = serverWS.Close(websocket.StatusNormalClosure, "") })

	bridgeLocal, testLocal := net.Pipe()
	t.Cleanup(func() { _ = bridgeLocal.Close() })
	t.Cleanup(func() { _ = testLocal.Close() })

	bridgeDone := make(chan error, 1)
	go func() {
		bridgeDone <- agent.Bridge(ctx, bridgeLocal, websocket.NetConn(ctx, clientConn, websocket.MessageBinary))
	}()

	if err := writeString(testLocal, "local-to-server"); err != nil {
		t.Fatalf("local write returned error: %v", err)
	}
	messageType, payload, err := serverWS.Read(ctx)
	if err != nil {
		t.Fatalf("server Read returned error: %v", err)
	}
	if messageType != websocket.MessageBinary {
		t.Fatalf("server message type = %v, want binary", messageType)
	}
	if string(payload) != "local-to-server" {
		t.Fatalf("server payload = %q, want local-to-server", payload)
	}

	if err := serverWS.Write(ctx, websocket.MessageBinary, []byte("server-to-local")); err != nil {
		t.Fatalf("server Write returned error: %v", err)
	}
	got, err := readString(testLocal, len("server-to-local"))
	if err != nil {
		t.Fatalf("local read returned error: %v", err)
	}
	if got != "server-to-local" {
		t.Fatalf("local read %q, want server-to-local", got)
	}

	if err := testLocal.Close(); err != nil {
		t.Fatalf("local close returned error: %v", err)
	}
	select {
	case err := <-bridgeDone:
		if err != nil && !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("Bridge returned error: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for bridge shutdown")
	}
}

func TestBridgeCopiesBothDirectionsAndClosesBothSides(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	leftBridge, leftTest := net.Pipe()
	rightBridge, rightTest := net.Pipe()
	t.Cleanup(func() { _ = leftBridge.Close() })
	t.Cleanup(func() { _ = leftTest.Close() })
	t.Cleanup(func() { _ = rightBridge.Close() })
	t.Cleanup(func() { _ = rightTest.Close() })

	done := make(chan error, 1)
	go func() {
		done <- agent.Bridge(ctx, leftBridge, rightBridge)
	}()

	if err := writeString(leftTest, "left-to-right"); err != nil {
		t.Fatalf("left write returned error: %v", err)
	}
	got, err := readString(rightTest, len("left-to-right"))
	if err != nil {
		t.Fatalf("right read returned error: %v", err)
	}
	if got != "left-to-right" {
		t.Fatalf("right read %q, want left-to-right", got)
	}

	if err := writeString(rightTest, "right-to-left"); err != nil {
		t.Fatalf("right write returned error: %v", err)
	}
	got, err = readString(leftTest, len("right-to-left"))
	if err != nil {
		t.Fatalf("left read returned error: %v", err)
	}
	if got != "right-to-left" {
		t.Fatalf("left read %q, want right-to-left", got)
	}

	if err := leftTest.Close(); err != nil {
		t.Fatalf("left close returned error: %v", err)
	}
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("Bridge returned error: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for bridge shutdown")
	}
	waitForReadClosed(t, rightTest)
}

func writeString(writer io.Writer, value string) error {
	_, err := writer.Write([]byte(value))
	return err
}

func readString(reader io.Reader, length int) (string, error) {
	buf := make([]byte, length)
	_, err := io.ReadFull(reader, buf)
	return string(buf), err
}

func waitForReadClosed(t *testing.T, conn net.Conn) {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	buf := make([]byte, 1)
	for {
		if err := conn.SetReadDeadline(time.Now().Add(10 * time.Millisecond)); err != nil {
			if errors.Is(err, io.ErrClosedPipe) || strings.Contains(err.Error(), "closed") {
				return
			}
			t.Fatalf("SetReadDeadline returned error: %v", err)
		}
		_, err := conn.Read(buf)
		if err == nil {
			t.Fatal("Read returned nil error while waiting for closed pipe")
		}
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() && time.Now().Before(deadline) {
			continue
		}
		if errors.As(err, &netErr) && netErr.Timeout() {
			t.Fatal("timeout waiting for bridged peer to close")
		}
		return
	}
}
