package server_test

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"serial-platform/internal/protocol"
	"serial-platform/internal/server"
	"serial-platform/internal/storage"
)

func TestLogWebSocketAcceptsBinaryFrame(t *testing.T) {
	root := t.TempDir()
	db, err := storage.Open(filepath.Join(root, "meta.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	logDir := filepath.Join(root, "logs")
	srv := server.New(server.ServerConfig{DB: db, LogDir: logDir})
	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	wsURL := "ws" + strings.TrimPrefix(httpSrv.URL, "http") + "/ws/logs"
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}

	now := time.Now().UTC()
	encoded, err := protocol.EncodeLogFrame(protocol.LogFrame{
		ChannelID:   "channel-1",
		Seq:         1,
		TimestampNS: now.UnixNano(),
		Direction:   protocol.DirectionRX,
		Flags:       protocol.FlagRaw,
		Payload:     []byte("boot\n"),
	})
	if err != nil {
		t.Fatalf("EncodeLogFrame returned error: %v", err)
	}

	if err := conn.Write(ctx, websocket.MessageBinary, encoded); err != nil {
		t.Fatalf("conn.Write returned error: %v", err)
	}
	if err := conn.Close(websocket.StatusNormalClosure, ""); err != nil {
		t.Fatalf("conn.Close returned error: %v", err)
	}

	segments := waitForLogSegments(t, db, "channel-1", now.Add(-time.Second), now.Add(time.Second))
	if len(segments) != 1 {
		t.Fatalf("len(segments) = %d, want 1", len(segments))
	}
	if segments[0].FrameCount != 1 {
		t.Fatalf("FrameCount = %d, want 1", segments[0].FrameCount)
	}
	if segments[0].Status != storage.LogSegmentStatusClosed {
		t.Fatalf("Status = %q, want %q", segments[0].Status, storage.LogSegmentStatusClosed)
	}
	if _, err := os.Stat(filepath.Join(logDir, segments[0].Path)); err != nil {
		t.Fatalf("log segment file does not exist: %v", err)
	}
}

func waitForLogSegments(t *testing.T, db *storage.DB, channelID string, start, end time.Time) []storage.LogSegment {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		segments, err := db.ListLogSegments(channelID, start, end)
		if err == nil && len(segments) > 0 {
			return segments
		}
		lastErr = err
		time.Sleep(10 * time.Millisecond)
	}
	if lastErr != nil {
		t.Fatalf("ListLogSegments returned error: %v", lastErr)
	}
	t.Fatal("timeout waiting for log segment metadata")
	return nil
}
