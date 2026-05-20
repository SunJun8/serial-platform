package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"serial-platform/internal/buildinfo"
	"serial-platform/internal/server"
	"serial-platform/internal/storage"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func run(args []string) error {
	return runWithDeps(args, centralServerDeps{
		notifyContext: func(parent context.Context) (context.Context, context.CancelFunc) {
			return signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
		},
		openDB: storage.Open,
		closeDB: func(db *storage.DB) error {
			return db.Close()
		},
		newServer: func(db *storage.DB, logDir string) centralServer {
			return server.New(server.ServerConfig{
				DB:     db,
				LogDir: logDir,
			})
		},
	})
}

type centralServer interface {
	http.Handler
	ServeRFC2217(context.Context, string) error
}

type centralServerDeps struct {
	notifyContext func(context.Context) (context.Context, context.CancelFunc)
	openDB        func(string) (*storage.DB, error)
	closeDB       func(*storage.DB) error
	newServer     func(*storage.DB, string) centralServer
}

func runWithDeps(args []string, deps centralServerDeps) error {
	flags := flag.NewFlagSet("central-server", flag.ContinueOnError)
	listen := flags.String("listen", ":8080", "HTTP listen address")
	rfc2217Bind := flags.String("rfc2217-bind", "0.0.0.0", "RFC2217 listen host")
	dataDir := flags.String("data-dir", "data", "central server data directory")
	if err := flags.Parse(args); err != nil {
		return err
	}

	if err := os.MkdirAll(*dataDir, 0o755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}

	db, err := deps.openDB(filepath.Join(*dataDir, "meta.db"))
	if err != nil {
		return fmt.Errorf("open metadata db: %w", err)
	}
	defer func() {
		if err := deps.closeDB(db); err != nil {
			log.Printf("close metadata db: %v", err)
		}
	}()

	handler := deps.newServer(db, filepath.Join(*dataDir, "logs"))

	ctx, stop := deps.notifyContext(context.Background())
	defer stop()

	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	if readyFile := os.Getenv("SERIAL_PLATFORM_CENTRAL_READY_FILE"); readyFile != "" {
		if err := os.WriteFile(readyFile, []byte(listener.Addr().String()+"\n"), 0o600); err != nil {
			_ = listener.Close()
			return fmt.Errorf("write ready file: %w", err)
		}
	}

	httpServer := &http.Server{Handler: handler}
	rfc2217Done := make(chan error, 1)
	go func() {
		err := handler.ServeRFC2217(ctx, *rfc2217Bind)
		if err != nil {
			log.Printf("rfc2217 listener stopped: %v", err)
		}
		rfc2217Done <- err
	}()

	log.Printf("central-server %s %s %s listening on %s", buildinfo.Version, buildinfo.Commit, buildinfo.Date, listener.Addr())
	serveErr := server.ServeHTTPWithShutdown(ctx, httpServer, listener, 5*time.Second)
	stop()
	<-rfc2217Done
	if serveErr != nil {
		return fmt.Errorf("listen and serve: %w", serveErr)
	}
	return nil
}
