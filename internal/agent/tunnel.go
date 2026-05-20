package agent

import (
	"context"
	"errors"
	"io"
	"net/url"
	"sync"

	"nhooyr.io/websocket"
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

func bridgeError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}
