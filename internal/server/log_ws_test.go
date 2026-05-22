package server_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	upsertLogTestChannel(t, db, "channel-1")

	logDir := filepath.Join(root, "logs")
	srv := server.New(server.ServerConfig{DB: db, LogDir: logDir})
	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn := dialLogWebSocket(t, ctx, httpSrv.URL)

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

	segments := waitForLogSegmentsWithStatus(t, db, "channel-1", now.Add(-time.Second), now.Add(time.Second), storage.LogSegmentStatusClosed)
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

func TestLogWebSocketRegistersActiveSegmentBeforeClose(t *testing.T) {
	root := t.TempDir()
	db, err := storage.Open(filepath.Join(root, "meta.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	upsertLogTestChannel(t, db, "channel-1")

	logDir := filepath.Join(root, "logs")
	srv := server.New(server.ServerConfig{DB: db, LogDir: logDir})
	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn := dialLogWebSocket(t, ctx, httpSrv.URL)

	now := time.Now().UTC()
	encoded, err := protocol.EncodeLogFrame(protocol.LogFrame{
		ChannelID:   "channel-1",
		Seq:         1,
		TimestampNS: now.UnixNano(),
		Direction:   protocol.DirectionRX,
		Flags:       protocol.FlagRaw,
		Payload:     []byte("active boot\n"),
	})
	if err != nil {
		t.Fatalf("EncodeLogFrame returned error: %v", err)
	}

	if err := conn.Write(ctx, websocket.MessageBinary, encoded); err != nil {
		t.Fatalf("conn.Write returned error: %v", err)
	}

	segments := waitForLogSegments(t, db, "channel-1", now.Add(-time.Second), now.Add(time.Second))
	if len(segments) != 1 {
		t.Fatalf("len(segments) = %d, want 1", len(segments))
	}
	segment := segments[0]
	if segment.Status != storage.LogSegmentStatusActive {
		t.Fatalf("Status = %q, want %q", segment.Status, storage.LogSegmentStatusActive)
	}
	if segment.FrameCount != 1 {
		t.Fatalf("FrameCount = %d, want 1", segment.FrameCount)
	}
	if segment.SizeBytes <= 0 {
		t.Fatalf("SizeBytes = %d, want > 0", segment.SizeBytes)
	}
	if _, err := os.Stat(filepath.Join(logDir, segment.Path)); err != nil {
		t.Fatalf("log segment file does not exist: %v", err)
	}

	query := url.Values{
		"channel_id": {"channel-1"},
		"from":       {now.Add(-time.Second).Format(time.RFC3339Nano)},
		"to":         {now.Add(time.Second).Format(time.RFC3339Nano)},
		"format":     {"raw"},
	}
	resp, err := httpSrv.Client().Get(httpSrv.URL + "/api/logs/download?" + query.Encode())
	if err != nil {
		t.Fatalf("GET /api/logs/download returned error: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll returned error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200, body = %s", resp.StatusCode, string(body))
	}
	if len(body) == 0 {
		t.Fatal("raw download is empty, want active segment bytes")
	}
	frames := decodeRawDownload(t, body)
	if len(frames) != 1 {
		t.Fatalf("downloaded %d frames, want 1", len(frames))
	}
	if frames[0].Seq != 1 || string(frames[0].Payload) != "active boot\n" {
		t.Fatalf("downloaded frame = %+v, want active boot payload", frames[0])
	}
}

func TestLogWebSocketClosesRolledSegments(t *testing.T) {
	root := t.TempDir()
	db, err := storage.Open(filepath.Join(root, "meta.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	upsertLogTestChannel(t, db, "channel-1")

	logDir := filepath.Join(root, "logs")
	srv := server.New(server.ServerConfig{DB: db, LogDir: logDir, LogSegmentSize: 128})
	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn := dialLogWebSocket(t, ctx, httpSrv.URL)
	defer conn.Close(websocket.StatusNormalClosure, "")

	now := time.Now().UTC()
	for i, payload := range []string{strings.Repeat("a", 80), strings.Repeat("b", 80)} {
		encoded, err := protocol.EncodeLogFrame(protocol.LogFrame{
			ChannelID:   "channel-1",
			Seq:         uint64(i + 1),
			TimestampNS: now.Add(time.Duration(i) * time.Second).UnixNano(),
			Direction:   protocol.DirectionRX,
			Flags:       protocol.FlagRaw,
			Payload:     []byte(payload),
		})
		if err != nil {
			t.Fatalf("EncodeLogFrame returned error: %v", err)
		}
		if err := conn.Write(ctx, websocket.MessageBinary, encoded); err != nil {
			t.Fatalf("conn.Write returned error: %v", err)
		}
	}

	var segments []storage.LogSegment
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var err error
		segments, err = db.ListLogSegments("channel-1", now.Add(-time.Second), now.Add(3*time.Second))
		if err != nil {
			t.Fatalf("ListLogSegments returned error: %v", err)
		}
		if len(segments) == 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(segments) != 2 {
		t.Fatalf("len(segments) = %d, want 2", len(segments))
	}
	if segments[0].Status != storage.LogSegmentStatusClosed {
		t.Fatalf("first segment status = %q, want closed", segments[0].Status)
	}
	if segments[1].Status != storage.LogSegmentStatusActive {
		t.Fatalf("second segment status = %q, want active", segments[1].Status)
	}
	if segments[0].Path == segments[1].Path {
		t.Fatalf("rolled segments used same path %q", segments[0].Path)
	}
	if err := conn.Close(websocket.StatusNormalClosure, ""); err != nil {
		t.Fatalf("conn.Close returned error: %v", err)
	}
	_ = waitForLogSegmentsWithStatus(t, db, "channel-1", now.Add(-time.Second), now.Add(3*time.Second), storage.LogSegmentStatusClosed)
}

func TestLogWebSocketIgnoresFramesAfterChannelDelete(t *testing.T) {
	root := t.TempDir()
	db, err := storage.Open(filepath.Join(root, "meta.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	channel := apiTestChannel("channel-1", 7001)
	if err := db.UpsertChannel(channel); err != nil {
		t.Fatalf("UpsertChannel returned error: %v", err)
	}
	logDir := filepath.Join(root, "logs")
	srv := server.New(server.ServerConfig{DB: db, LogDir: logDir})
	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn := dialLogWebSocket(t, ctx, httpSrv.URL)

	first := time.Now().UTC()
	if err := conn.Write(ctx, websocket.MessageBinary, encodedLogFrame(t, "channel-1", 1, first, "before delete\n")); err != nil {
		t.Fatalf("first conn.Write returned error: %v", err)
	}
	segments := waitForLogSegments(t, db, "channel-1", first.Add(-time.Second), first.Add(time.Second))
	if len(segments) != 1 {
		t.Fatalf("len(segments) before delete = %d, want 1", len(segments))
	}

	req, err := http.NewRequest(http.MethodDelete, httpSrv.URL+"/api/channels/channel-1", nil)
	if err != nil {
		t.Fatalf("NewRequest returned error: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do returned error: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE status = %s, body = %s", resp.Status, body)
	}

	second := first.Add(time.Second)
	if err := conn.Write(ctx, websocket.MessageBinary, encodedLogFrame(t, "channel-1", 2, second, "after delete\n")); err != nil {
		t.Fatalf("second conn.Write returned error: %v", err)
	}
	if err := conn.Close(websocket.StatusNormalClosure, ""); err != nil {
		t.Fatalf("conn.Close returned error: %v", err)
	}

	if _, err := db.GetChannel("channel-1"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("GetChannel error = %v, want ErrNotFound", err)
	}
	segments, err = db.ListLogSegments("channel-1", first.Add(-time.Second), second.Add(time.Second))
	if err != nil {
		t.Fatalf("ListLogSegments returned error: %v", err)
	}
	if len(segments) != 0 {
		t.Fatalf("segments after delete = %+v, want empty", segments)
	}
}

func TestLogWebSocketRejectsInvalidChannelIDWithoutSegment(t *testing.T) {
	tests := []struct {
		name      string
		channelID string
		frame     func(t *testing.T, channelID string) []byte
		wantClose websocket.StatusCode
	}{
		{
			name:      "empty",
			channelID: "",
			wantClose: websocket.StatusInvalidFramePayloadData,
			frame: func(t *testing.T, _ string) []byte {
				t.Helper()
				return encodedLogFrameWithEmptyChannelID(t)
			},
		},
		{
			name:      "path traversal",
			channelID: "../x",
			wantClose: websocket.StatusInternalError,
			frame: func(t *testing.T, channelID string) []byte {
				t.Helper()
				return encodedLogFrameForChannel(t, channelID)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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
			conn := dialLogWebSocket(t, ctx, httpSrv.URL)
			defer conn.Close(websocket.StatusNormalClosure, "")

			if err := conn.Write(ctx, websocket.MessageBinary, tt.frame(t, tt.channelID)); err != nil {
				t.Fatalf("conn.Write returned error: %v", err)
			}

			readCtx, readCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer readCancel()
			_, _, err = conn.Read(readCtx)
			if err == nil {
				t.Fatal("conn.Read returned nil error, want server to reject invalid channel ID")
			}
			if got := websocket.CloseStatus(err); got != tt.wantClose {
				t.Fatalf("conn.Read close status = %v, want %v", got, tt.wantClose)
			}

			segments, err := db.ListLogSegments(tt.channelID, time.Unix(0, 0), time.Now().Add(time.Hour))
			if err != nil {
				t.Fatalf("ListLogSegments returned error: %v", err)
			}
			if len(segments) != 0 {
				t.Fatalf("len(segments) = %d, want 0", len(segments))
			}
		})
	}
}

func TestLogWebSocketRejectsEmptyLogDir(t *testing.T) {
	root := t.TempDir()
	db, err := storage.Open(filepath.Join(root, "meta.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	channelID := "empty-logdir-test-channel"
	t.Cleanup(func() { _ = os.RemoveAll(channelID) })

	srv := server.New(server.ServerConfig{DB: db})
	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn := dialLogWebSocket(t, ctx, httpSrv.URL)
	defer conn.Close(websocket.StatusNormalClosure, "")

	if err := conn.Write(ctx, websocket.MessageBinary, encodedLogFrameForChannel(t, channelID)); err != nil {
		t.Fatalf("conn.Write returned error: %v", err)
	}

	readCtx, readCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer readCancel()
	_, _, err = conn.Read(readCtx)
	if err == nil {
		t.Fatal("conn.Read returned nil error, want server to reject empty LogDir")
	}
	if got := websocket.CloseStatus(err); got != websocket.StatusInternalError {
		t.Fatalf("conn.Read close status = %v, want %v", got, websocket.StatusInternalError)
	}

	segments, err := db.ListLogSegments(channelID, time.Unix(0, 0), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("ListLogSegments returned error: %v", err)
	}
	if len(segments) != 0 {
		t.Fatalf("len(segments) = %d, want 0", len(segments))
	}
	if _, err := os.Stat(channelID); !os.IsNotExist(err) {
		t.Fatalf("relative log directory %q exists after empty LogDir rejection", channelID)
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

func waitForLogSegmentsWithStatus(t *testing.T, db *storage.DB, channelID string, start, end time.Time, status storage.LogSegmentStatus) []storage.LogSegment {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	var lastSegments []storage.LogSegment
	var lastErr error
	for time.Now().Before(deadline) {
		segments, err := db.ListLogSegments(channelID, start, end)
		if err != nil {
			lastErr = err
			time.Sleep(10 * time.Millisecond)
			continue
		}
		lastSegments = segments
		if len(segments) > 0 && allLogSegmentsHaveStatus(segments, status) {
			return segments
		}
		time.Sleep(10 * time.Millisecond)
	}
	if lastErr != nil {
		t.Fatalf("ListLogSegments returned error: %v", lastErr)
	}
	t.Fatalf("timeout waiting for log segment status %q, last segments = %+v", status, lastSegments)
	return nil
}

func allLogSegmentsHaveStatus(segments []storage.LogSegment, status storage.LogSegmentStatus) bool {
	for _, segment := range segments {
		if segment.Status != status {
			return false
		}
	}
	return true
}

func dialLogWebSocket(t *testing.T, ctx context.Context, serverURL string) *websocket.Conn {
	t.Helper()

	wsURL := "ws" + strings.TrimPrefix(serverURL, "http") + "/ws/logs"
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}
	return conn
}

func upsertLogTestChannel(t *testing.T, db *storage.DB, channelID string) {
	t.Helper()
	channel := apiTestChannel(channelID, 7001)
	if err := db.UpsertChannel(channel); err != nil {
		t.Fatalf("UpsertChannel returned error: %v", err)
	}
}

func encodedLogFrameForChannel(t *testing.T, channelID string) []byte {
	t.Helper()

	encoded, err := protocol.EncodeLogFrame(protocol.LogFrame{
		ChannelID:   channelID,
		Seq:         1,
		TimestampNS: time.Now().UTC().UnixNano(),
		Direction:   protocol.DirectionRX,
		Flags:       protocol.FlagRaw,
		Payload:     []byte("boot\n"),
	})
	if err != nil {
		t.Fatalf("EncodeLogFrame returned error: %v", err)
	}
	return encoded
}

func encodedLogFrame(t *testing.T, channelID string, seq uint64, timestamp time.Time, payload string) []byte {
	t.Helper()

	encoded, err := protocol.EncodeLogFrame(protocol.LogFrame{
		ChannelID:   channelID,
		Seq:         seq,
		TimestampNS: timestamp.UnixNano(),
		Direction:   protocol.DirectionRX,
		Flags:       protocol.FlagRaw,
		Payload:     []byte(payload),
	})
	if err != nil {
		t.Fatalf("EncodeLogFrame returned error: %v", err)
	}
	return encoded
}

func encodedLogFrameWithEmptyChannelID(t *testing.T) []byte {
	t.Helper()

	encoded := encodedLogFrameForChannel(t, "channel-1")
	channelLen := len("channel-1")
	copy(encoded[32:], encoded[32+channelLen:])
	encoded = encoded[:len(encoded)-channelLen]
	encoded[6] = 0
	encoded[7] = 0
	return encoded
}
