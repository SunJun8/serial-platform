package server

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"serial-platform/internal/rfc2217"
	"serial-platform/internal/storage"
)

func TestServeRFC2217ReloadsListenerWhenPortChanges(t *testing.T) {
	db := openRFC2217ManagerTestDB(t)
	oldPort := freeTCPPort(t)
	newPort := freeTCPPort(t)
	channel := rfc2217ManagerTestChannel(oldPort)
	upsertRFC2217ManagerTestChannel(t, db, channel)

	manager, ctx, cancel := newRFC2217ManagerForTest(db, channel.ID)
	defer cancel()
	defer manager.close()

	if err := manager.sync(ctx); err != nil {
		t.Fatalf("sync returned error: %v", err)
	}
	oldConn := dialRFC2217Eventually(t, net.JoinHostPort("127.0.0.1", strconv.Itoa(oldPort)))
	if err := oldConn.Close(); err != nil {
		t.Fatalf("oldConn.Close returned error: %v", err)
	}

	channel.RFC2217Port = newPort
	upsertRFC2217ManagerTestChannel(t, db, channel)
	if err := manager.sync(ctx); err != nil {
		t.Fatalf("sync after port update returned error: %v", err)
	}

	newConn := dialRFC2217Eventually(t, net.JoinHostPort("127.0.0.1", strconv.Itoa(newPort)))
	if err := newConn.Close(); err != nil {
		t.Fatalf("newConn.Close returned error: %v", err)
	}
	dialRFC2217FailsEventually(t, net.JoinHostPort("127.0.0.1", strconv.Itoa(oldPort)))
}

func TestServeRFC2217ReloadsResolverWhenDefaultBaudChanges(t *testing.T) {
	db := openRFC2217ManagerTestDB(t)
	port := freeTCPPort(t)
	channel := rfc2217ManagerTestChannel(port)
	upsertRFC2217ManagerTestChannel(t, db, channel)

	manager, ctx, cancel := newRFC2217ManagerForTest(db, channel.ID)
	defer cancel()
	defer manager.close()

	if err := manager.sync(ctx); err != nil {
		t.Fatalf("sync returned error: %v", err)
	}
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	firstConn := dialRFC2217Eventually(t, addr)
	if err := firstConn.Close(); err != nil {
		t.Fatalf("firstConn.Close returned error: %v", err)
	}

	channel.DefaultBaud = 57600
	upsertRFC2217ManagerTestChannel(t, db, channel)
	if err := manager.sync(ctx); err != nil {
		t.Fatalf("sync after baud update returned error: %v", err)
	}

	secondConn := dialRFC2217Eventually(t, addr)
	if err := secondConn.Close(); err != nil {
		t.Fatalf("secondConn.Close returned error: %v", err)
	}
}

func openRFC2217ManagerTestDB(t *testing.T) *storage.DB {
	t.Helper()

	db, err := storage.Open(filepath.Join(t.TempDir(), "meta.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func rfc2217ManagerTestChannel(port int) storage.Channel {
	return storage.Channel{
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
		DefaultFlow:     "none",
		UpdatedAt:       time.Now().UTC(),
	}
}

func upsertRFC2217ManagerTestChannel(t *testing.T, db *storage.DB, channel storage.Channel) {
	t.Helper()

	if err := db.UpsertChannel(channel); err != nil {
		t.Fatalf("UpsertChannel returned error: %v", err)
	}
}

func newRFC2217ManagerForTest(db *storage.DB, channelID string) (*rfc2217Manager, context.Context, context.CancelFunc) {
	srv := New(ServerConfig{
		DB: db,
	})
	ctx, cancel := context.WithCancel(context.Background())
	return &rfc2217Manager{
		srv:      srv,
		bindHost: "127.0.0.1",
		active:   make(map[string]*rfc2217ActiveEntry),
	}, ctx, cancel
}

func freeTCPPort(t *testing.T) int {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen returned error: %v", err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func dialRFC2217Eventually(t *testing.T, addr string) net.Conn {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			return conn
		}
		lastErr = err
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("net.Dial(%q) failed: %v", addr, lastErr)
	return nil
}

func dialRFC2217FailsEventually(t *testing.T, addr string) {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err != nil {
			return
		}
		_ = conn.Close()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("net.Dial(%q) kept succeeding, want closed listener", addr)
}

func assertRFC2217BaudQuery(t *testing.T, addr string, want int) {
	t.Helper()

	conn := dialRFC2217Eventually(t, addr)
	defer conn.Close()

	query := []byte{
		rfc2217.IAC, rfc2217.SB, rfc2217.COMPortOption, rfc2217.SetBaudrate,
		0, 0, 0, 0,
		rfc2217.IAC, rfc2217.SE,
	}
	if _, err := conn.Write(query); err != nil {
		t.Fatalf("conn.Write returned error: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline returned error: %v", err)
	}

	response := make([]byte, 10)
	if _, err := io.ReadFull(conn, response); err != nil {
		t.Fatalf("io.ReadFull returned error: %v", err)
	}
	wantResponse := []byte{
		rfc2217.IAC, rfc2217.SB, rfc2217.COMPortOption, rfc2217.SetBaudrate + 100,
		0, 0, 0, 0,
		rfc2217.IAC, rfc2217.SE,
	}
	binary.BigEndian.PutUint32(wantResponse[4:8], uint32(want))
	if !bytes.Equal(response, wantResponse) {
		t.Fatalf("baud query response = %x, want %x", response, wantResponse)
	}
}
