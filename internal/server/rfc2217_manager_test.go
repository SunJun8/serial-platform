package server_test

import (
	"bytes"
	"context"
	"net"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"serial-platform/internal/protocol"
	"serial-platform/internal/server"
	"serial-platform/internal/storage"
)

func TestServeRFC2217StartsListenerForConfiguredChannel(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "meta.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	netListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen returned error: %v", err)
	}
	port := netListener.Addr().(*net.TCPAddr).Port
	if err := netListener.Close(); err != nil {
		t.Fatalf("listener.Close returned error: %v", err)
	}

	channel := storage.Channel{
		ID:              "channel-1",
		AgentID:         "agent-1",
		AutoName:        "host01.hub01.port01.if00",
		Alias:           "rack1.port01.console",
		Role:            "console",
		IDPath:          "pci-0000:00:14.0-usb-0:1:1.0",
		IDPathTag:       "pci-0000_00_14_0-usb-0_1_1_0",
		RFC2217Port:     port,
		Status:          storage.ChannelStatusOnline,
		DefaultBaud:     115200,
		DefaultDataBits: 8,
		DefaultParity:   "N",
		DefaultStopBits: 1,
		DefaultFlow:     "rtscts",
		UpdatedAt:       time.Now().UTC(),
	}
	if err := db.UpsertChannel(channel); err != nil {
		t.Fatalf("UpsertChannel returned error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv := server.New(server.ServerConfig{
		DB: db,
	})
	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)
	agentWS := connectRFC2217ManagerAgent(t, ctx, httpSrv.URL, channel.AgentID)
	defer agentWS.CloseNow()

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- srv.ServeRFC2217(ctx, "127.0.0.1")
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-serveErr:
			if err != nil {
				t.Fatalf("ServeRFC2217 returned error: %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("ServeRFC2217 did not return after context cancellation")
		}
	})

	conn := dialEventually(t, net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	defer conn.Close()

	open := readRFC2217OpenTunnel(t, ctx, agentWS)
	tunnelWS, _, err := websocket.Dial(ctx, rfc2217ManagerWSURL(httpSrv.URL, "/ws/tunnel/"+open.TunnelID), nil)
	if err != nil {
		t.Fatalf("tunnel websocket Dial returned error: %v", err)
	}
	defer tunnelWS.CloseNow()
	tunnelConn := websocket.NetConn(ctx, tunnelWS, websocket.MessageBinary)
	defer tunnelConn.Close()

	if _, err := conn.Write([]byte("AT\r")); err != nil {
		t.Fatalf("conn.Write returned error: %v", err)
	}
	if err := tunnelConn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("tunnelConn.SetReadDeadline returned error: %v", err)
	}
	buf := make([]byte, 3)
	if _, err := tunnelConn.Read(buf); err != nil {
		t.Fatalf("tunnelConn.Read returned error: %v", err)
	}
	if !bytes.Equal(buf, []byte("AT\r")) {
		t.Fatalf("agent tunnel read = %q, want AT\\r", buf)
	}
}

func connectRFC2217ManagerAgent(t *testing.T, ctx context.Context, serverURL, agentID string) *websocket.Conn {
	t.Helper()

	conn, _, err := websocket.Dial(ctx, rfc2217ManagerWSURL(serverURL, "/ws/agent"), nil)
	if err != nil {
		t.Fatalf("agent websocket Dial returned error: %v", err)
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
		t.Fatalf("read channel sync returned error: %v", err)
	}
	return conn
}

func readRFC2217OpenTunnel(t *testing.T, ctx context.Context, conn *websocket.Conn) protocol.OpenTunnel {
	t.Helper()

	var open protocol.OpenTunnel
	if err := protocol.ReadJSON(ctx, conn, &open); err != nil {
		t.Fatalf("read open_tunnel returned error: %v", err)
	}
	if open.Type != protocol.MessageOpenTunnel ||
		open.ChannelID != "channel-1" ||
		open.Mode != protocol.TunnelModeRFC2217 ||
		open.TunnelID == "" {
		t.Fatalf("open_tunnel = %+v", open)
	}
	return open
}

func rfc2217ManagerWSURL(serverURL, path string) string {
	return "ws" + strings.TrimPrefix(serverURL, "http") + path
}

func dialEventually(t *testing.T, addr string) net.Conn {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		conn, err := net.Dial("tcp", addr)
		if err == nil {
			return conn
		}
		lastErr = err
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("net.Dial(%q) failed: %v", addr, lastErr)
	return nil
}
