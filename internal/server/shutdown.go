package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"
)

func ServeHTTPWithShutdown(ctx context.Context, srv *http.Server, listener net.Listener, timeout time.Duration) error {
	errCh := make(chan error, 1)
	go func() {
		err := srv.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errCh <- err
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		shutdownErr := srv.Shutdown(shutdownCtx)
		if shutdownErr != nil {
			_ = srv.Close()
		}
		err := <-errCh
		if shutdownErr != nil {
			if err != nil {
				return errors.Join(shutdownErr, err)
			}
			return shutdownErr
		}
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
