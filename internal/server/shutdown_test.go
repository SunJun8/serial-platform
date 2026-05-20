package server

import (
	"context"
	"errors"
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

	resp, err := http.Get("http://" + listener.Addr().String())
	if err != nil {
		t.Fatalf("http.Get returned error: %v", err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("response body close returned error: %v", err)
	}

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

func TestServeHTTPWithShutdownReturnsShutdownTimeout(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen returned error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	handlerStarted := make(chan struct{})
	releaseHandler := make(chan struct{})
	t.Cleanup(func() {
		close(releaseHandler)
	})
	httpServer := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(handlerStarted)
		<-releaseHandler
	})}

	done := make(chan error, 1)
	go func() {
		done <- ServeHTTPWithShutdown(ctx, httpServer, listener, time.Nanosecond)
	}()

	requestDone := make(chan error, 1)
	go func() {
		resp, err := http.Get("http://" + listener.Addr().String())
		if resp != nil {
			_ = resp.Body.Close()
		}
		requestDone <- err
	}()

	select {
	case <-handlerStarted:
	case <-time.After(time.Second):
		t.Fatal("handler did not receive request")
	}

	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("ServeHTTPWithShutdown returned nil error, want shutdown timeout")
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("ServeHTTPWithShutdown returned error %v, want context deadline exceeded", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ServeHTTPWithShutdown did not return after shutdown timeout")
	}

	select {
	case <-requestDone:
	case <-time.After(time.Second):
		t.Fatal("blocked request did not finish after server close")
	}
}
