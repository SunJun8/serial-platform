package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"nhooyr.io/websocket"
)

var (
	errTunnelIDEmpty      = errors.New("tunnel id is empty")
	errTunnelWaiterExists = errors.New("tunnel waiter already exists")
	errTunnelNoWaiter     = errors.New("tunnel waiter not found")
)

type TunnelRegistry struct {
	mu      sync.Mutex
	timeout time.Duration
	waiters map[string]chan net.Conn
}

func NewTunnelRegistry(timeout time.Duration) *TunnelRegistry {
	return &TunnelRegistry{
		timeout: timeout,
		waiters: make(map[string]chan net.Conn),
	}
}

func (r *TunnelRegistry) Wait(ctx context.Context, tunnelID string) (net.Conn, error) {
	if tunnelID == "" {
		return nil, errTunnelIDEmpty
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	waiter := make(chan net.Conn, 1)
	r.mu.Lock()
	if _, exists := r.waiters[tunnelID]; exists {
		r.mu.Unlock()
		return nil, errTunnelWaiterExists
	}
	r.waiters[tunnelID] = waiter
	r.mu.Unlock()

	waitCtx := ctx
	cancel := func() {}
	if r.timeout > 0 {
		waitCtx, cancel = context.WithTimeout(ctx, r.timeout)
	}
	defer cancel()

	select {
	case conn, ok := <-waiter:
		if !ok {
			return nil, context.Canceled
		}
		return conn, nil
	case <-waitCtx.Done():
		r.mu.Lock()
		current, exists := r.waiters[tunnelID]
		if exists && current == waiter {
			delete(r.waiters, tunnelID)
			close(waiter)
			r.mu.Unlock()
			return nil, waitCtx.Err()
		}
		r.mu.Unlock()

		conn, ok := <-waiter
		if !ok {
			return nil, context.Canceled
		}
		return conn, nil
	}
}

func (r *TunnelRegistry) Attach(tunnelID string, conn net.Conn) error {
	if tunnelID == "" {
		return errTunnelIDEmpty
	}
	if conn == nil {
		return errors.New("tunnel conn is nil")
	}

	r.mu.Lock()
	waiter, exists := r.waiters[tunnelID]
	if exists {
		waiter <- conn
		delete(r.waiters, tunnelID)
	}
	r.mu.Unlock()

	if !exists {
		return errTunnelNoWaiter
	}
	return nil
}

func (r *TunnelRegistry) Cancel(tunnelID string) {
	r.mu.Lock()
	waiter, exists := r.waiters[tunnelID]
	if exists {
		delete(r.waiters, tunnelID)
	}
	r.mu.Unlock()

	if exists {
		close(waiter)
	}
}

type WSByteConn struct {
	ws   *websocket.Conn
	conn net.Conn

	closeOnce sync.Once
	closed    chan struct{}
}

var _ net.Conn = (*WSByteConn)(nil)

func NewWSByteConn(ctx context.Context, conn *websocket.Conn) *WSByteConn {
	return &WSByteConn{
		ws:     conn,
		conn:   websocket.NetConn(ctx, conn, websocket.MessageBinary),
		closed: make(chan struct{}),
	}
}

func (c *WSByteConn) Read(p []byte) (int, error) {
	return c.conn.Read(p)
}

func (c *WSByteConn) Write(p []byte) (int, error) {
	return c.conn.Write(p)
}

func (c *WSByteConn) Close() error {
	var err error
	c.closeOnce.Do(func() {
		err = c.conn.Close()
		close(c.closed)
	})
	return err
}

func (c *WSByteConn) LocalAddr() net.Addr {
	return c.conn.LocalAddr()
}

func (c *WSByteConn) RemoteAddr() net.Addr {
	return c.conn.RemoteAddr()
}

func (c *WSByteConn) SetDeadline(t time.Time) error {
	return c.conn.SetDeadline(t)
}

func (c *WSByteConn) SetReadDeadline(t time.Time) error {
	return c.conn.SetReadDeadline(t)
}

func (c *WSByteConn) SetWriteDeadline(t time.Time) error {
	return c.conn.SetWriteDeadline(t)
}

func (c *WSByteConn) Done() <-chan struct{} {
	return c.closed
}

func (srv *Server) handleTunnelWebSocket(w http.ResponseWriter, r *http.Request) {
	tunnelID := r.PathValue("tunnelID")
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}

	byteConn := NewWSByteConn(r.Context(), conn)
	if err := srv.tunnels.Attach(tunnelID, byteConn); err != nil {
		_ = byteConn.Close()
		return
	}
	defer byteConn.Close()

	select {
	case <-r.Context().Done():
	case <-byteConn.Done():
	}
}
