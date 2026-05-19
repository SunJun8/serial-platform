package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"serial-platform/internal/agent"
	"serial-platform/internal/buildinfo"
)

func main() {
	serverURL := flag.String("server", "http://localhost:8080", "central server URL")
	dataDir := flag.String("data-dir", "data", "host agent data directory")
	agentID := flag.String("agent-id", "", "agent ID")
	flag.Parse()

	if err := os.MkdirAll(*dataDir, 0o755); err != nil {
		log.Fatalf("create data dir: %v", err)
	}

	id := strings.TrimSpace(*agentID)
	if id == "" {
		var err error
		id, err = loadOrCreateAgentID(*dataDir)
		if err != nil {
			log.Fatalf("load agent id: %v", err)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	client := &agent.Client{Config: agent.Config{
		ServerURL: *serverURL,
		DataDir:   *dataDir,
		AgentID:   id,
	}}

	log.Printf("host-agent %s %s %s connecting to %s as %s", buildinfo.Version, buildinfo.Commit, buildinfo.Date, *serverURL, id)
	status, err := client.Connect(ctx)
	if err != nil {
		log.Fatalf("connect to central server: %v", err)
	}
	log.Printf("agent accepted status: %s", status)

	<-ctx.Done()
	closeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Close(closeCtx); err != nil {
		log.Printf("close agent client: %v", err)
	}
}

func loadOrCreateAgentID(dataDir string) (string, error) {
	path := filepath.Join(dataDir, "agent_id")
	data, err := os.ReadFile(path)
	if err == nil {
		return strings.TrimSpace(string(data)), nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}

	id, err := generateAgentID()
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(id+"\n"), 0o600); err != nil {
		return "", err
	}
	return id, nil
}

func generateAgentID() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}
