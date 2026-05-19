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

type RFC2217Listener struct {
	listener  net.Listener
	channelID string
	resolver  RFC2217Resolver
}

func NewRFC2217Listener(listener net.Listener, channelID string, control serial.SerialControl, config serial.Config) *RFC2217Listener {
	return NewRFC2217ListenerWithResolver(listener, channelID, func(context.Context) (serial.SerialControl, serial.Config, error) {
		return control, config, nil
	})
}

func NewRFC2217ListenerWithResolver(listener net.Listener, channelID string, resolver RFC2217Resolver) *RFC2217Listener {
	return &RFC2217Listener{
		listener:  listener,
		channelID: channelID,
		resolver:  resolver,
	}
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
