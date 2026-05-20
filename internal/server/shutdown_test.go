package server

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestServeHTTPShutsDownWhenContextCancels(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen returned error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	httpServer := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})}

	done := make(chan error, 1)
	go func() {
		done <- ServeHTTPWithShutdown(ctx, httpServer, listener, 100*time.Millisecond)
	}()

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ServeHTTPWithShutdown returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ServeHTTPWithShutdown did not return after context cancellation")
	}
}
