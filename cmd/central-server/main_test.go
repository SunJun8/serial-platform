package main

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"serial-platform/internal/server"
	"serial-platform/internal/storage"
)

func TestCentralServerExitsOnInterrupt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Interrupt delivery to subprocesses is not supported on windows")
	}

	if os.Getenv("SERIAL_PLATFORM_CENTRAL_HELPER") == "1" {
		os.Args = []string{
			os.Args[0],
			"--listen", "127.0.0.1:0",
			"--data-dir", os.Getenv("SERIAL_PLATFORM_CENTRAL_DATA_DIR"),
			"--rfc2217-bind", "127.0.0.1",
		}
		main()
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	readyPath := filepath.Join(t.TempDir(), "central-server.ready")
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestCentralServerExitsOnInterrupt")
	cmd.Env = append(os.Environ(),
		"SERIAL_PLATFORM_CENTRAL_HELPER=1",
		"SERIAL_PLATFORM_CENTRAL_DATA_DIR="+t.TempDir(),
		"SERIAL_PLATFORM_CENTRAL_READY_FILE="+readyPath,
	)
	cmd.Dir = t.TempDir()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("cmd.Start returned error: %v", err)
	}
	waitForFile(t, readyPath, time.Second)
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("Signal returned error: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("central-server did not exit cleanly: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
}

func TestRunWaitsForRFC2217ShutdownBeforeClosingDB(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dataDir := t.TempDir()
	readyPath := filepath.Join(t.TempDir(), "central-server.ready")
	var dbClosedBeforeRFC2217 int32
	var rfc2217ObservedDBClosed int32
	releaseRFC2217 := make(chan struct{})
	t.Setenv("SERIAL_PLATFORM_CENTRAL_READY_FILE", readyPath)
	deps := centralServerDeps{
		notifyContext: func(context.Context) (context.Context, context.CancelFunc) {
			return ctx, cancel
		},
		openDB: storage.Open,
		newServer: func(db *storage.DB, logDir string) centralServer {
			return &blockingRFC2217Server{
				Server: server.New(server.ServerConfig{
					DB:     db,
					LogDir: logDir,
				}),
				dbClosedBeforeRFC2217: &dbClosedBeforeRFC2217,
				observedDBClosed:      &rfc2217ObservedDBClosed,
				release:               releaseRFC2217,
			}
		},
		closeDB: func(db *storage.DB) error {
			atomic.StoreInt32(&dbClosedBeforeRFC2217, 1)
			return db.Close()
		},
	}

	done := make(chan error, 1)
	go func() {
		done <- runWithDeps([]string{
			"--listen", "127.0.0.1:0",
			"--data-dir", dataDir,
			"--rfc2217-bind", "127.0.0.1",
		}, deps)
	}()

	waitForFile(t, readyPath, time.Second)
	cancel()

	select {
	case err := <-done:
		t.Fatalf("runWithDeps returned before RFC2217 shutdown completed: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(releaseRFC2217)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runWithDeps returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runWithDeps did not return after RFC2217 shutdown completed")
	}

	if atomic.LoadInt32(&dbClosedBeforeRFC2217) != 1 {
		t.Fatal("test closeDB hook did not run")
	}
	if atomic.LoadInt32(&rfc2217ObservedDBClosed) != 0 {
		t.Fatal("DB was closed before RFC2217 shutdown completed")
	}
}

func TestRunLogsRFC2217ErrorBeforeHTTPShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logs := newCapturingLogWriter()
	previousLogOutput := log.Writer()
	log.SetOutput(logs)
	t.Cleanup(func() {
		log.SetOutput(previousLogOutput)
	})

	dataDir := t.TempDir()
	readyPath := filepath.Join(t.TempDir(), "central-server.ready")
	t.Setenv("SERIAL_PLATFORM_CENTRAL_READY_FILE", readyPath)
	rfc2217Err := errors.New("rfc2217 startup failed")
	deps := centralServerDeps{
		notifyContext: func(context.Context) (context.Context, context.CancelFunc) {
			return ctx, cancel
		},
		openDB: storage.Open,
		newServer: func(db *storage.DB, logDir string) centralServer {
			return &immediateRFC2217ErrorServer{err: rfc2217Err}
		},
		closeDB: func(db *storage.DB) error {
			return db.Close()
		},
	}

	done := make(chan error, 1)
	go func() {
		done <- runWithDeps([]string{
			"--listen", "127.0.0.1:0",
			"--data-dir", dataDir,
			"--rfc2217-bind", "127.0.0.1",
		}, deps)
	}()

	waitForFile(t, readyPath, time.Second)
	if !logs.waitContains("rfc2217 listener stopped: rfc2217 startup failed", 200*time.Millisecond) {
		cancel()
		waitForRunWithDeps(t, done)
		t.Fatal("RFC2217 error was not logged before HTTP shutdown")
	}

	cancel()
	if err := waitForRunWithDeps(t, done); err != nil {
		t.Fatalf("runWithDeps returned error: %v", err)
	}
}

func TestRunReturnsReadyFileError(t *testing.T) {
	dataDir := t.TempDir()
	missingParent := filepath.Join(t.TempDir(), "missing", "central-server.ready")
	t.Setenv("SERIAL_PLATFORM_CENTRAL_READY_FILE", missingParent)

	err := run([]string{
		"--listen", "127.0.0.1:0",
		"--data-dir", dataDir,
		"--rfc2217-bind", "127.0.0.1",
	})
	if err == nil {
		t.Fatal("run returned nil error, want ready file error")
	}
	if !strings.Contains(err.Error(), "write ready file") {
		t.Fatalf("error = %q, want ready file context", err.Error())
	}
}

type immediateRFC2217ErrorServer struct {
	err error
}

func (srv *immediateRFC2217ErrorServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

func (srv *immediateRFC2217ErrorServer) ServeRFC2217(ctx context.Context, bindHost string) error {
	return srv.err
}

type blockingRFC2217Server struct {
	*server.Server
	dbClosedBeforeRFC2217 *int32
	observedDBClosed      *int32
	release               <-chan struct{}
}

func (srv *blockingRFC2217Server) ServeRFC2217(ctx context.Context, bindHost string) error {
	<-ctx.Done()
	<-srv.release
	if atomic.LoadInt32(srv.dbClosedBeforeRFC2217) != 0 {
		atomic.StoreInt32(srv.observedDBClosed, 1)
	}
	return nil
}

func waitForFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("file %s was not created within %s", path, timeout)
}

func waitForRunWithDeps(t *testing.T, done <-chan error) error {
	t.Helper()

	select {
	case err := <-done:
		return err
	case <-time.After(time.Second):
		t.Fatal("runWithDeps did not return")
		return nil
	}
}

type capturingLogWriter struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	notify chan struct{}
}

func newCapturingLogWriter() *capturingLogWriter {
	return &capturingLogWriter{notify: make(chan struct{}, 1)}
}

func (w *capturingLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	n, err := w.buf.Write(p)
	select {
	case w.notify <- struct{}{}:
	default:
	}
	return n, err
}

func (w *capturingLogWriter) contains(needle string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	return strings.Contains(w.buf.String(), needle)
}

func (w *capturingLogWriter) waitContains(needle string, timeout time.Duration) bool {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	for {
		if w.contains(needle) {
			return true
		}
		select {
		case <-w.notify:
		case <-deadline.C:
			return w.contains(needle)
		}
	}
}
