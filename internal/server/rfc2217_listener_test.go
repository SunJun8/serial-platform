package server_test

import (
	"bytes"
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"serial-platform/internal/serial"
	"serial-platform/internal/server"
)

func TestRFC2217ListenerTranslatesClientBytesAndSerialRX(t *testing.T) {
	netListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen returned error: %v", err)
	}
	control := newRFC2217FakeControl()
	listener := server.NewRFC2217Listener(netListener, "channel-1", control, serial.DefaultConfig())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- listener.Serve(ctx)
	}()

	conn, err := net.Dial("tcp", netListener.Addr().String())
	if err != nil {
		t.Fatalf("net.Dial returned error: %v", err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("AT\r")); err != nil {
		t.Fatalf("conn.Write returned error: %v", err)
	}
	control.session.waitForWrite(t, []byte("AT\r"))

	control.events <- serial.Event{
		ChannelID: "channel-1",
		Direction: serial.DirectionRX,
		Timestamp: time.Now(),
		Data:      []byte("OK\r\n"),
	}
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline returned error: %v", err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("io.ReadFull returned error: %v", err)
	}
	if !bytes.Equal(buf, []byte("OK\r\n")) {
		t.Fatalf("tcp read = %q, want %q", buf, "OK\r\n")
	}

	cancel()
	if err := netListener.Close(); err != nil {
		t.Fatalf("netListener.Close returned error: %v", err)
	}
	select {
	case err := <-serveErr:
		if err != nil {
			t.Fatalf("Serve returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Serve did not return after listener close")
	}
}

func TestRFC2217ListenerContextCancelClosesActiveConnectionAndSession(t *testing.T) {
	netListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen returned error: %v", err)
	}
	control := newRFC2217FakeControl()
	listener := server.NewRFC2217Listener(netListener, "channel-1", control, serial.DefaultConfig())

	ctx, cancel := context.WithCancel(context.Background())
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- listener.Serve(ctx)
	}()

	conn, err := net.Dial("tcp", netListener.Addr().String())
	if err != nil {
		t.Fatalf("net.Dial returned error: %v", err)
	}
	defer conn.Close()

	control.session.waitForOpen(t)
	cancel()

	control.session.waitForClose(t)
	if err := conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline returned error: %v", err)
	}
	var one [1]byte
	if _, err := conn.Read(one[:]); err == nil {
		t.Fatal("conn.Read returned nil error after context cancellation, want closed connection")
	}

	select {
	case err := <-serveErr:
		if err != nil {
			t.Fatalf("Serve returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Serve did not return after context cancellation")
	}
}

func TestRFC2217ListenerEscapesSerialRXIAC(t *testing.T) {
	netListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen returned error: %v", err)
	}
	control := newRFC2217FakeControl()
	listener := server.NewRFC2217Listener(netListener, "channel-1", control, serial.DefaultConfig())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- listener.Serve(ctx)
	}()

	conn, err := net.Dial("tcp", netListener.Addr().String())
	if err != nil {
		t.Fatalf("net.Dial returned error: %v", err)
	}
	defer conn.Close()

	control.session.waitForOpen(t)
	control.events <- serial.Event{
		ChannelID: "channel-1",
		Direction: serial.DirectionRX,
		Timestamp: time.Now(),
		Data:      []byte{'A', 0xff, 'B'},
	}
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline returned error: %v", err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("io.ReadFull returned error: %v", err)
	}
	want := []byte{'A', 0xff, 0xff, 'B'}
	if !bytes.Equal(buf, want) {
		t.Fatalf("tcp read = %x, want %x", buf, want)
	}

	cancel()
	select {
	case err := <-serveErr:
		if err != nil {
			t.Fatalf("Serve returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Serve did not return after context cancellation")
	}
}

type rfc2217FakeControl struct {
	session *rfc2217FakeSession
	events  chan serial.Event
}

func newRFC2217FakeControl() *rfc2217FakeControl {
	return &rfc2217FakeControl{
		session: newRFC2217FakeSession(),
		events:  make(chan serial.Event, 4),
	}
}

func (c *rfc2217FakeControl) OpenControlSession(context.Context, string) (serial.ControlSession, error) {
	c.session.markOpen()
	return c.session, nil
}

func (c *rfc2217FakeControl) Events() <-chan serial.Event {
	return c.events
}

type rfc2217FakeSession struct {
	mu        sync.Mutex
	writes    [][]byte
	closed    bool
	opened    chan struct{}
	closec    chan struct{}
	onceOpen  sync.Once
	onceClose sync.Once
}

func newRFC2217FakeSession() *rfc2217FakeSession {
	return &rfc2217FakeSession{
		opened: make(chan struct{}),
		closec: make(chan struct{}),
	}
}

func (s *rfc2217FakeSession) markOpen() {
	s.onceOpen.Do(func() {
		close(s.opened)
	})
}

func (s *rfc2217FakeSession) Write(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes = append(s.writes, append([]byte(nil), data...))
	return nil
}

func (s *rfc2217FakeSession) SetConfig(serial.Config) error {
	return nil
}

func (s *rfc2217FakeSession) SetDTR(bool) error {
	return nil
}

func (s *rfc2217FakeSession) SetRTS(bool) error {
	return nil
}

func (s *rfc2217FakeSession) SendBreak(time.Duration) error {
	return nil
}

func (s *rfc2217FakeSession) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.onceClose.Do(func() {
		close(s.closec)
	})
	return nil
}

func (s *rfc2217FakeSession) waitForOpen(t *testing.T) {
	t.Helper()
	select {
	case <-s.opened:
	case <-time.After(time.Second):
		t.Fatal("control session was not opened")
	}
}

func (s *rfc2217FakeSession) waitForClose(t *testing.T) {
	t.Helper()
	select {
	case <-s.closec:
	case <-time.After(time.Second):
		t.Fatal("control session was not closed")
	}
}

func (s *rfc2217FakeSession) waitForWrite(t *testing.T, want []byte) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		for _, got := range s.writes {
			if bytes.Equal(got, want) {
				s.mu.Unlock()
				return
			}
		}
		s.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t.Fatalf("writes = %q, want %q", s.writes, want)
}
