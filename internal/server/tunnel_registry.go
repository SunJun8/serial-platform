package server

import (
	"context"
	"errors"
	"fmt"
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
	errTunnelCanceled     = errors.New("tunnel wait canceled")
)

type TunnelRegistry struct {
	mu      sync.Mutex
	timeout time.Duration
	waiters map[string]chan tunnelResult
}

type tunnelResult struct {
	conn net.Conn
	err  error
}

func NewTunnelRegistry(timeout time.Duration) *TunnelRegistry {
	return &TunnelRegistry{
		timeout: timeout,
		waiters: make(map[string]chan tunnelResult),
	}
}

func (r *TunnelRegistry) Wait(ctx context.Context, tunnelID string) (net.Conn, error) {
	if tunnelID == "" {
		return nil, errTunnelIDEmpty
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	waiter, err := r.registerWaiter(tunnelID)
	if err != nil {
		return nil, err
	}

	return r.waitForConn(ctx, tunnelID, waiter)
}

func (r *TunnelRegistry) WaitAfterRegister(ctx context.Context, tunnelID string, afterRegister func() error) (net.Conn, error) {
	if tunnelID == "" {
		return nil, errTunnelIDEmpty
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	waiter, err := r.registerWaiter(tunnelID)
	if err != nil {
		return nil, err
	}
	if afterRegister != nil {
		if err := afterRegister(); err != nil {
			r.cancelWaiterAndCloseAttached(tunnelID, waiter)
			return nil, err
		}
	}

	return r.waitForConn(ctx, tunnelID, waiter)
}

func (r *TunnelRegistry) registerWaiter(tunnelID string) (chan tunnelResult, error) {
	waiter := make(chan tunnelResult, 1)
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.waiters[tunnelID]; exists {
		return nil, errTunnelWaiterExists
	}
	r.waiters[tunnelID] = waiter
	return waiter, nil
}

func (r *TunnelRegistry) waitForConn(ctx context.Context, tunnelID string, waiter chan tunnelResult) (net.Conn, error) {
	waitCtx := ctx
	cancel := func() {}
	if r.timeout > 0 {
		waitCtx, cancel = context.WithTimeout(ctx, r.timeout)
	}
	defer cancel()

	select {
	case result, ok := <-waiter:
		if !ok {
			return nil, context.Canceled
		}
		if result.err != nil {
			return nil, result.err
		}
		return result.conn, nil
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

		result, ok := <-waiter
		if !ok {
			return nil, context.Canceled
		}
		if result.err != nil {
			return nil, result.err
		}
		return result.conn, nil
	}
}

func (r *TunnelRegistry) cancelWaiter(tunnelID string, waiter chan tunnelResult) {
	r.mu.Lock()
	current, exists := r.waiters[tunnelID]
	if exists && current == waiter {
		delete(r.waiters, tunnelID)
		close(waiter)
	}
	r.mu.Unlock()
}

func (r *TunnelRegistry) cancelWaiterAndCloseAttached(tunnelID string, waiter chan tunnelResult) {
	r.cancelWaiter(tunnelID, waiter)

	select {
	case result, ok := <-waiter:
		if ok && result.conn != nil {
			_ = result.conn.Close()
		}
	default:
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
		waiter <- tunnelResult{conn: conn}
		delete(r.waiters, tunnelID)
	}
	r.mu.Unlock()

	if !exists {
		return errTunnelNoWaiter
	}
	return nil
}

func (r *TunnelRegistry) Cancel(tunnelID string) {
	r.CancelWithError(tunnelID, errTunnelCanceled)
}

func (r *TunnelRegistry) CancelWithError(tunnelID string, err error) {
	if err == nil {
		err = errTunnelCanceled
	} else if !errors.Is(err, errTunnelCanceled) {
		err = fmt.Errorf("%w: %w", errTunnelCanceled, err)
	}

	r.mu.Lock()
	waiter, exists := r.waiters[tunnelID]
	if exists {
		delete(r.waiters, tunnelID)
	}
	r.mu.Unlock()

	if exists {
		waiter <- tunnelResult{err: err}
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
