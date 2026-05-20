package server_test

import (
	"bytes"
	"context"
	"net"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"serial-platform/internal/serial"
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

	backend := serial.NewFakeBackend()
	worker := serial.NewWorker(channel.ID, serial.DefaultConfig(), backend)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go worker.Run(ctx)

	srv := server.New(server.ServerConfig{
		DB: db,
		SerialResolver: func(channelID string) (serial.SerialControl, bool) {
			if channelID != channel.ID {
				return nil, false
			}
			return worker, true
		},
	})
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

	if _, err := conn.Write([]byte("AT\r")); err != nil {
		t.Fatalf("conn.Write returned error: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("fake backend did not receive RFC2217 client bytes")
		}
		writes := backend.Writes()
		if len(writes) > 0 {
			if !bytes.Equal(writes[0], []byte("AT\r")) {
				t.Fatalf("backend write = %q, want AT\\r", writes[0])
			}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if backend.Config().Flow != "none" {
		t.Fatalf("backend flow = %q, want none", backend.Config().Flow)
	}
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
