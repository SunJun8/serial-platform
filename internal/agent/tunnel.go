package agent

import (
	"context"
	"errors"
	"io"
	"net/url"
	"sync"

	"nhooyr.io/websocket"

	"serial-platform/internal/rfc2217"
	"serial-platform/internal/serial"
)

type TunnelDialer struct {
	ServerURL string
}

func (d TunnelDialer) Dial(ctx context.Context, tunnelID string) (*websocket.Conn, error) {
	if tunnelID == "" {
		return nil, errors.New("tunnel id is empty")
	}

	wsURL, err := webSocketURL(d.ServerURL, "/ws/tunnel/"+url.PathEscape(tunnelID))
	if err != nil {
		return nil, err
	}

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

func Bridge(ctx context.Context, left io.ReadWriteCloser, right io.ReadWriteCloser) error {
	errs := make(chan error, 2)
	var closeOnce sync.Once
	closeBoth := func() {
		closeOnce.Do(func() {
			go func() { _ = left.Close() }()
			go func() { _ = right.Close() }()
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
		return bridgeError(ctx, err)
	case <-ctx.Done():
		closeBoth()
		return ctx.Err()
	}
}

func HandleRFC2217Tunnel(ctx context.Context, conn io.ReadWriteCloser, channelID string, control serial.SerialControl, config serial.Config) error {
	session, err := control.OpenControlSession(ctx, "rfc2217")
	if err != nil {
		_ = conn.Close()
		return err
	}
	defer session.Close()

	tunnelCtx, cancel := context.WithCancel(ctx)
	eventsDone := make(chan struct{})
	go pipeRFC2217Events(tunnelCtx, conn, channelID, control.Events(), eventsDone)
	defer func() {
		cancel()
		_ = conn.Close()
	}()

	current := config
	parser := rfc2217.NewParser()
	buf := make([]byte, 4096)
	for {
		n, readErr := conn.Read(buf)
		if n > 0 {
			ops, parseErr := parser.Feed(buf[:n])
			if parseErr != nil {
				return parseErr
			}
			response := []byte(nil)
			current, response, parseErr = rfc2217.ApplyOperations(session, current, ops)
			if parseErr != nil {
				return parseErr
			}
			if len(response) > 0 {
				if _, err := conn.Write(response); err != nil {
					return err
				}
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return readErr
		}
	}
}

func pipeRFC2217Events(ctx context.Context, conn io.Writer, channelID string, events <-chan serial.Event, done chan<- struct{}) {
	defer close(done)
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			if event.ChannelID != channelID || event.Direction != serial.DirectionRX || len(event.Data) == 0 {
				continue
			}
			if _, err := conn.Write(escapeRFC2217Data(event.Data)); err != nil {
				return
			}
		}
	}
}

func escapeRFC2217Data(data []byte) []byte {
	out := make([]byte, 0, len(data))
	for _, value := range data {
		out = append(out, value)
		if value == rfc2217.IAC {
			out = append(out, rfc2217.IAC)
		}
	}
	return out
}

func bridgeError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}
