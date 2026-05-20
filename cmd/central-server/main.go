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

	db, err := storage.Open(filepath.Join(*dataDir, "meta.db"))
	if err != nil {
		return fmt.Errorf("open metadata db: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.Printf("close metadata db: %v", err)
		}
	}()

	handler := server.New(server.ServerConfig{
		DB:     db,
		LogDir: filepath.Join(*dataDir, "logs"),
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	httpServer := &http.Server{Handler: handler}
	go func() {
		if err := handler.ServeRFC2217(ctx, *rfc2217Bind); err != nil {
			log.Printf("rfc2217 listener stopped: %v", err)
		}
	}()

	log.Printf("central-server %s %s %s listening on %s", buildinfo.Version, buildinfo.Commit, buildinfo.Date, listener.Addr())
	if err := server.ServeHTTPWithShutdown(ctx, httpServer, listener, 5*time.Second); err != nil {
		return fmt.Errorf("listen and serve: %w", err)
	}
	return nil
}
