package agent_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"serial-platform/internal/agent"
	"serial-platform/internal/serial"
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

func TestBridgeReturnsAfterFirstSideEndsEvenIfOtherReadStaysBlocked(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	left := newBlockingReadWriteCloser()
	right := newEOFReadWriteCloser()
	t.Cleanup(func() { left.unblockRead(io.ErrClosedPipe) })

	done := make(chan error, 1)
	go func() {
		done <- agent.Bridge(ctx, left, right)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Bridge returned error: %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Bridge did not return promptly after first copy ended")
	}

	waitForCloseCount(t, left, 1)
	waitForCloseCount(t, right, 1)
	left.unblockRead(io.ErrClosedPipe)
	waitForReadUnblocked(t, left)
}

func TestAgentHandlesRFC2217TunnelWithLocalSerialControl(t *testing.T) {
	control := newAgentRFC2217FakeControl()
	agentConn, clientConn := net.Pipe()
	t.Cleanup(func() { _ = agentConn.Close() })
	t.Cleanup(func() { _ = clientConn.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- agent.HandleRFC2217Tunnel(ctx, agentConn, "channel-1", control, serial.DefaultConfig())
	}()

	control.session.waitForOpen(t)
	if control.session.owner() != "rfc2217" {
		t.Fatalf("control session owner = %q, want rfc2217", control.session.owner())
	}

	if _, err := clientConn.Write([]byte("AT\r")); err != nil {
		t.Fatalf("client tunnel Write returned error: %v", err)
	}
	control.session.waitForWrite(t, []byte("AT\r"))

	control.events <- serial.Event{
		ChannelID: "channel-1",
		Direction: serial.DirectionRX,
		Timestamp: time.Now(),
		Data:      []byte{'O', 'K', 0xff, '\r', '\n'},
	}
	if err := clientConn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("client tunnel SetReadDeadline returned error: %v", err)
	}
	response := make([]byte, 6)
	if _, err := io.ReadFull(clientConn, response); err != nil {
		t.Fatalf("client tunnel ReadFull returned error: %v", err)
	}
	want := []byte{'O', 'K', 0xff, 0xff, '\r', '\n'}
	if string(response) != string(want) {
		t.Fatalf("client tunnel read = %x, want %x", response, want)
	}

	if err := clientConn.Close(); err != nil {
		t.Fatalf("client tunnel Close returned error: %v", err)
	}
	control.session.waitForClose(t)
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("HandleRFC2217Tunnel returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("HandleRFC2217Tunnel did not return after tunnel close")
	}
}

func TestAgentHandlesRFC2217TunnelReturnsWhenRXWriterIsBlocked(t *testing.T) {
	control := newAgentRFC2217FakeControl()
	conn := newBlockingWriteEOFConn()
	t.Cleanup(conn.releaseWrite)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- agent.HandleRFC2217Tunnel(ctx, conn, "channel-1", control, serial.DefaultConfig())
	}()

	control.session.waitForOpen(t)
	control.events <- serial.Event{
		ChannelID: "channel-1",
		Direction: serial.DirectionRX,
		Timestamp: time.Now(),
		Data:      []byte("blocked-rx"),
	}
	conn.waitForWriteBlocked(t)
	conn.finishRead()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("HandleRFC2217Tunnel returned error: %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("HandleRFC2217Tunnel did not return promptly while RX writer was blocked")
	}
	control.session.waitForClose(t)
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

type eofReadWriteCloser struct {
	mu     sync.Mutex
	closes int
}

func newEOFReadWriteCloser() *eofReadWriteCloser {
	return &eofReadWriteCloser{}
}

func (c *eofReadWriteCloser) Read([]byte) (int, error) {
	return 0, io.EOF
}

func (c *eofReadWriteCloser) Write(p []byte) (int, error) {
	return len(p), nil
}

func (c *eofReadWriteCloser) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closes++
	return nil
}

func (c *eofReadWriteCloser) closeCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closes
}

type blockingReadWriteCloser struct {
	mu     sync.Mutex
	closes int
	read   chan error
	done   chan struct{}
}

func newBlockingReadWriteCloser() *blockingReadWriteCloser {
	return &blockingReadWriteCloser{
		read: make(chan error, 1),
		done: make(chan struct{}),
	}
}

func (c *blockingReadWriteCloser) Read([]byte) (int, error) {
	err := <-c.read
	close(c.done)
	return 0, err
}

func (c *blockingReadWriteCloser) Write(p []byte) (int, error) {
	return len(p), nil
}

func (c *blockingReadWriteCloser) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closes++
	return nil
}

func (c *blockingReadWriteCloser) closeCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closes
}

func (c *blockingReadWriteCloser) unblockRead(err error) {
	select {
	case c.read <- err:
	default:
	}
}

type closeCounter interface {
	closeCount() int
}

func waitForCloseCount(t *testing.T, closer closeCounter, want int) {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for {
		if got := closer.closeCount(); got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("Close count = %d, want %d", closer.closeCount(), want)
		}
		time.Sleep(time.Millisecond)
	}
}

func waitForReadUnblocked(t *testing.T, closer *blockingReadWriteCloser) {
	t.Helper()

	select {
	case <-closer.done:
	case <-time.After(time.Second):
		t.Fatal("blocked read did not exit before test cleanup")
	}
}

type blockingWriteEOFConn struct {
	readDone     chan struct{}
	writeStarted chan struct{}
	writeRelease chan struct{}
	closes       int
	mu           sync.Mutex
}

func newBlockingWriteEOFConn() *blockingWriteEOFConn {
	return &blockingWriteEOFConn{
		readDone:     make(chan struct{}),
		writeStarted: make(chan struct{}),
		writeRelease: make(chan struct{}),
	}
}

func (c *blockingWriteEOFConn) Read([]byte) (int, error) {
	<-c.readDone
	return 0, io.EOF
}

func (c *blockingWriteEOFConn) Write(p []byte) (int, error) {
	select {
	case c.writeStarted <- struct{}{}:
	default:
	}
	select {
	case <-c.writeRelease:
		return len(p), nil
	}
}

func (c *blockingWriteEOFConn) Close() error {
	c.mu.Lock()
	c.closes++
	c.mu.Unlock()
	return nil
}

func (c *blockingWriteEOFConn) finishRead() {
	select {
	case <-c.readDone:
	default:
		close(c.readDone)
	}
}

func (c *blockingWriteEOFConn) releaseWrite() {
	select {
	case <-c.writeRelease:
	default:
		close(c.writeRelease)
	}
}

func (c *blockingWriteEOFConn) waitForWriteBlocked(t *testing.T) {
	t.Helper()
	select {
	case <-c.writeStarted:
	case <-time.After(time.Second):
		t.Fatal("RX writer did not enter blocked Write")
	}
}

type agentRFC2217FakeControl struct {
	session *agentRFC2217FakeSession
	events  chan serial.Event
}

func newAgentRFC2217FakeControl() *agentRFC2217FakeControl {
	return &agentRFC2217FakeControl{
		session: newAgentRFC2217FakeSession(),
		events:  make(chan serial.Event, 4),
	}
}

func (c *agentRFC2217FakeControl) OpenControlSession(_ context.Context, owner string) (serial.ControlSession, error) {
	c.session.markOpen(owner)
	return c.session, nil
}

func (c *agentRFC2217FakeControl) Events() <-chan serial.Event {
	return c.events
}

type agentRFC2217FakeSession struct {
	mu        sync.Mutex
	ownerName string
	writes    [][]byte
	opened    chan struct{}
	closed    chan struct{}
	onceOpen  sync.Once
	onceClose sync.Once
}

func newAgentRFC2217FakeSession() *agentRFC2217FakeSession {
	return &agentRFC2217FakeSession{
		opened: make(chan struct{}),
		closed: make(chan struct{}),
	}
}

func (s *agentRFC2217FakeSession) markOpen(owner string) {
	s.mu.Lock()
	s.ownerName = owner
	s.mu.Unlock()
	s.onceOpen.Do(func() {
		close(s.opened)
	})
}

func (s *agentRFC2217FakeSession) owner() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ownerName
}

func (s *agentRFC2217FakeSession) Write(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes = append(s.writes, append([]byte(nil), data...))
	return nil
}

func (s *agentRFC2217FakeSession) SetConfig(serial.Config) error { return nil }

func (s *agentRFC2217FakeSession) SetDTR(bool) error { return nil }

func (s *agentRFC2217FakeSession) SetRTS(bool) error { return nil }

func (s *agentRFC2217FakeSession) SendBreak(time.Duration) error { return nil }

func (s *agentRFC2217FakeSession) Close() error {
	s.onceClose.Do(func() {
		close(s.closed)
	})
	return nil
}

func (s *agentRFC2217FakeSession) waitForOpen(t *testing.T) {
	t.Helper()
	select {
	case <-s.opened:
	case <-time.After(time.Second):
		t.Fatal("control session was not opened")
	}
}

func (s *agentRFC2217FakeSession) waitForClose(t *testing.T) {
	t.Helper()
	select {
	case <-s.closed:
	case <-time.After(time.Second):
		t.Fatal("control session was not closed")
	}
}

func (s *agentRFC2217FakeSession) waitForWrite(t *testing.T, want []byte) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		for _, got := range s.writes {
			if string(got) == string(want) {
				s.mu.Unlock()
				return
			}
		}
		s.mu.Unlock()
		time.Sleep(time.Millisecond)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t.Fatalf("writes = %q, want %q", s.writes, want)
}
