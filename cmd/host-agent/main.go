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
	"serial-platform/internal/protocol"
	"serial-platform/internal/serial"
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

	frames := make(chan protocol.LogFrame, 256)
	uploader := agent.NewLogUploader(agent.LogUploaderConfig{Out: frames})
	go func() {
		if err := client.SendLogFramesLoop(ctx, frames, time.Second); err != nil && ctx.Err() == nil {
			log.Printf("send log frames: %v", err)
		}
	}()

	runtime := agent.NewRuntime(agent.RuntimeConfig{
		ScanInterval:  3 * time.Second,
		ChannelSource: client.FetchChannelConfigs,
		ForwardEvents: func(ctx context.Context, events <-chan serial.Event) error {
			return uploader.Forward(ctx, events)
		},
	})
	go func() {
		if err := runtime.Run(ctx); err != nil && ctx.Err() == nil {
			log.Printf("agent runtime: %v", err)
		}
	}()

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
		id := strings.TrimSpace(string(data))
		if id != "" {
			return id, nil
		}
	}
	if err != nil && !os.IsNotExist(err) {
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
