package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"serial-platform/internal/buildinfo"
	"serial-platform/internal/server"
	"serial-platform/internal/storage"
)

func main() {
	listen := flag.String("listen", ":8080", "HTTP listen address")
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
	log.Printf("central-server %s %s %s listening on %s", buildinfo.Version, buildinfo.Commit, buildinfo.Date, *listen)
	if err := http.ListenAndServe(*listen, handler); err != nil {
		log.Fatalf("listen and serve: %v", err)
	}
}
