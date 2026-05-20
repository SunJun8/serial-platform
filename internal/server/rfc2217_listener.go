package server

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"

	"serial-platform/internal/rfc2217"
	"serial-platform/internal/serial"
)

type RFC2217Resolver func(context.Context) (serial.SerialControl, serial.Config, error)

type RFC2217TunnelResolver interface {
	OpenRFC2217Tunnel(ctx context.Context, channelID string) (net.Conn, error)
}

type RFC2217ListenerOption func(*RFC2217Listener)

type RFC2217Listener struct {
	listener       net.Listener
	channelID      string
	resolver       RFC2217Resolver
	tunnelResolver RFC2217TunnelResolver
	owners         *ControlOwner
}

func WithRFC2217ControlOwner(owners *ControlOwner) RFC2217ListenerOption {
	return func(l *RFC2217Listener) {
		l.owners = owners
	}
}

func NewRFC2217Listener(listener net.Listener, channelID string, control serial.SerialControl, config serial.Config, options ...RFC2217ListenerOption) *RFC2217Listener {
	return NewRFC2217ListenerWithResolver(listener, channelID, func(context.Context) (serial.SerialControl, serial.Config, error) {
		return control, config, nil
	}, options...)
}

func NewRFC2217ListenerWithResolver(listener net.Listener, channelID string, resolver RFC2217Resolver, options ...RFC2217ListenerOption) *RFC2217Listener {
	l := &RFC2217Listener{
		listener:  listener,
		channelID: channelID,
		resolver:  resolver,
	}
	for _, option := range options {
		option(l)
	}
	return l
}

func NewRFC2217TunnelListener(listener net.Listener, channelID string, resolver RFC2217TunnelResolver, options ...RFC2217ListenerOption) *RFC2217Listener {
	l := &RFC2217Listener{
		listener:       listener,
		channelID:      channelID,
		tunnelResolver: resolver,
	}
	for _, option := range options {
		option(l)
	}
	return l
}

func (l *RFC2217Listener) Serve(ctx context.Context) error {
	var closeOnce sync.Once
	go func() {
		<-ctx.Done()
		closeOnce.Do(func() {
			_ = l.listener.Close()
		})
	}()

	for {
		conn, err := l.listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go l.handleConn(ctx, conn)
	}
}

func (l *RFC2217Listener) handleConn(parent context.Context, conn net.Conn) {
	defer conn.Close()

	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()

	if l.owners != nil {
		if err := l.owners.Acquire(l.channelID, "rfc2217"); err != nil {
			return
		}
		defer l.owners.Release(l.channelID, "rfc2217")
	}

	if l.tunnelResolver != nil {
		l.handleTunnelConn(ctx, conn)
		return
	}

	control, config, err := l.resolver(ctx)
	if err != nil {
		return
	}
	session, err := control.OpenControlSession(ctx, "rfc2217")
	if err != nil {
		return
	}
	defer session.Close()

	done := make(chan struct{})
	go l.pipeSerialRX(ctx, conn, control.Events(), done)
	defer func() {
		cancel()
		<-done
	}()

	current := config
	parser := rfc2217.NewParser()
	buf := make([]byte, 4096)
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			ops, parseErr := parser.Feed(buf[:n])
			if parseErr != nil {
				return
			}
			response := []byte(nil)
			current, response, parseErr = rfc2217.ApplyOperations(session, current, ops)
			if parseErr != nil {
				return
			}
			if len(response) > 0 {
				if _, writeErr := conn.Write(response); writeErr != nil {
					return
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			return
		}
	}
}

func (l *RFC2217Listener) handleTunnelConn(ctx context.Context, conn net.Conn) {
	tunnel, err := l.tunnelResolver.OpenRFC2217Tunnel(ctx, l.channelID)
	if err != nil {
		return
	}
	defer tunnel.Close()

	_ = bridgeConns(ctx, conn, tunnel)
}

func (l *RFC2217Listener) pipeSerialRX(ctx context.Context, conn net.Conn, events <-chan serial.Event, done chan<- struct{}) {
	defer close(done)
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			if event.ChannelID != l.channelID || event.Direction != serial.DirectionRX || len(event.Data) == 0 {
				continue
			}
			if _, err := conn.Write(escapeTelnetData(event.Data)); err != nil {
				return
			}
		}
	}
}

func bridgeConns(ctx context.Context, left, right net.Conn) error {
	errs := make(chan error, 2)
	var closeOnce sync.Once
	closeBoth := func() {
		closeOnce.Do(func() {
			_ = left.Close()
			_ = right.Close()
		})
	}

	copySide := func(dst io.Writer, src io.Reader) {
		_, err := io.Copy(dst, src)
		errs <- err
		closeBoth()
	}

	go copySide(left, right)
	go copySide(right, left)

	select {
	case err := <-errs:
		closeBoth()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	case <-ctx.Done():
		closeBoth()
		return ctx.Err()
	}
}

func escapeTelnetData(data []byte) []byte {
	out := make([]byte, 0, len(data))
	for _, value := range data {
		out = append(out, value)
		if value == rfc2217.IAC {
			out = append(out, rfc2217.IAC)
		}
	}
	return out
}
