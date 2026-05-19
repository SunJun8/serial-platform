package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"serial-platform/internal/buildinfo"
	"serial-platform/internal/server"
	"serial-platform/internal/storage"
)

func main() {
	listen := flag.String("listen", ":8080", "HTTP listen address")
	rfc2217Bind := flag.String("rfc2217-bind", "0.0.0.0", "RFC2217 listen host")
	dataDir := flag.String("data-dir", "data", "central server data directory")
	flag.Parse()

	if err := os.MkdirAll(*dataDir, 0o755); err != nil {
		log.Fatalf("create data dir: %v", err)
	}

	db, err := storage.Open(filepath.Join(*dataDir, "meta.db"))
	if err != nil {
		log.Fatalf("open metadata db: %v", err)
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
	go func() {
		if err := handler.ServeRFC2217(ctx, *rfc2217Bind); err != nil {
			log.Printf("rfc2217 listener stopped: %v", err)
		}
	}()

	log.Printf("central-server %s %s %s listening on %s", buildinfo.Version, buildinfo.Commit, buildinfo.Date, *listen)
	if err := http.ListenAndServe(*listen, handler); err != nil {
		log.Fatalf("listen and serve: %v", err)
	}
}
