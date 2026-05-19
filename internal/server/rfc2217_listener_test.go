package server_test

import (
	"bytes"
	"context"
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
	if _, err := conn.Read(buf); err != nil {
		t.Fatalf("conn.Read returned error: %v", err)
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

type rfc2217FakeControl struct {
	session *rfc2217FakeSession
	events  chan serial.Event
}

func newRFC2217FakeControl() *rfc2217FakeControl {
	return &rfc2217FakeControl{
		session: &rfc2217FakeSession{},
		events:  make(chan serial.Event, 4),
	}
}

func (c *rfc2217FakeControl) OpenControlSession(context.Context, string) (serial.ControlSession, error) {
	return c.session, nil
}

func (c *rfc2217FakeControl) Events() <-chan serial.Event {
	return c.events
}

type rfc2217FakeSession struct {
	mu     sync.Mutex
	writes [][]byte
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
	return nil
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
