package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"serial-platform/internal/logstore"
	"serial-platform/internal/protocol"
	"serial-platform/internal/server"
	"serial-platform/internal/storage"
)

func TestLogDownloadRequiresChannel(t *testing.T) {
	root := t.TempDir()
	db, err := storage.Open(filepath.Join(root, "meta.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	srv := server.New(server.ServerConfig{DB: db, LogDir: filepath.Join(root, "logs")})
	req := httptest.NewRequest(http.MethodGet, "/api/logs/download", nil)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "channel_id") {
		t.Fatalf("body = %q, want mention of channel_id", rec.Body.String())
	}
}

func TestLogDownloadTextFiltersDirectionAndLabels(t *testing.T) {
	root := t.TempDir()
	db, logDir := openLogDownloadDB(t, root)
	start := time.Date(2026, 5, 19, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Minute)
	insertLogSegment(t, db, logDir, "channel-1",
		protocol.LogFrame{
			ChannelID:   "channel-1",
			Seq:         1,
			TimestampNS: start.UnixNano(),
			Direction:   protocol.DirectionRX,
			Flags:       protocol.FlagRaw,
			Payload:     []byte("\x1b[31mrx payload\x1b[0m\n"),
		},
		protocol.LogFrame{
			ChannelID:   "channel-1",
			Seq:         2,
			TimestampNS: end.UnixNano(),
			Direction:   protocol.DirectionTX,
			Flags:       protocol.FlagRaw,
			Payload:     []byte("tx payload\n"),
		},
	)

	srv := server.New(server.ServerConfig{DB: db, LogDir: logDir})
	query := url.Values{
		"channel_id":      {"channel-1"},
		"from":            {start.Add(-time.Second).Format(time.RFC3339Nano)},
		"to":              {end.Add(time.Second).Format(time.RFC3339Nano)},
		"direction":       {"rx"},
		"timestamp":       {"true"},
		"direction_label": {"true"},
		"strip_ansi":      {"true"},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/logs/download?"+query.Encode(), nil)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want text/plain; charset=utf-8", got)
	}
	text := rec.Body.String()
	if !strings.Contains(text, start.Format(time.RFC3339Nano)+" RX rx payload") {
		t.Fatalf("body = %q, want timestamped RX payload", text)
	}
	if strings.Contains(text, "tx payload") {
		t.Fatalf("body = %q, want TX payload filtered out", text)
	}
	if strings.Contains(text, "\x1b[31m") {
		t.Fatalf("body = %q, want ANSI removed", text)
	}
}

func TestLogDownloadRawConcatenatesSegments(t *testing.T) {
	root := t.TempDir()
	db, logDir := openLogDownloadDB(t, root)
	start := time.Date(2026, 5, 19, 0, 0, 0, 0, time.UTC)
	first := insertLogSegment(t, db, logDir, "channel-1", protocol.LogFrame{
		ChannelID:   "channel-1",
		Seq:         1,
		TimestampNS: start.UnixNano(),
		Direction:   protocol.DirectionRX,
		Flags:       protocol.FlagRaw,
		Payload:     []byte("first\n"),
	})
	second := insertLogSegment(t, db, logDir, "channel-1", protocol.LogFrame{
		ChannelID:   "channel-1",
		Seq:         2,
		TimestampNS: start.Add(time.Second).UnixNano(),
		Direction:   protocol.DirectionTX,
		Flags:       protocol.FlagRaw,
		Payload:     []byte("second\n"),
	})
	firstBytes, err := os.ReadFile(filepath.Join(logDir, first.RelativePath))
	if err != nil {
		t.Fatalf("ReadFile first returned error: %v", err)
	}
	secondBytes, err := os.ReadFile(filepath.Join(logDir, second.RelativePath))
	if err != nil {
		t.Fatalf("ReadFile second returned error: %v", err)
	}

	srv := server.New(server.ServerConfig{DB: db, LogDir: logDir})
	query := url.Values{
		"channel_id": {"channel-1"},
		"from":       {start.Add(-time.Second).Format(time.RFC3339Nano)},
		"to":         {start.Add(time.Minute).Format(time.RFC3339Nano)},
		"format":     {"raw"},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/logs/download?"+query.Encode(), nil)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	want := append(append([]byte(nil), firstBytes...), secondBytes...)
	if got := rec.Body.Bytes(); string(got) != string(want) {
		t.Fatalf("raw bytes = %v, want %v", got, want)
	}
}

func TestLogDownloadRejectsInvalidQuery(t *testing.T) {
	root := t.TempDir()
	db, logDir := openLogDownloadDB(t, root)
	srv := server.New(server.ServerConfig{DB: db, LogDir: logDir})

	tests := []struct {
		name    string
		query   string
		wantErr string
	}{
		{name: "format", query: "channel_id=channel-1&format=json", wantErr: "format"},
		{name: "direction", query: "channel_id=channel-1&direction=bad", wantErr: "direction"},
		{name: "from", query: "channel_id=channel-1&from=bad", wantErr: "invalid time"},
		{name: "to", query: "channel_id=channel-1&to=bad", wantErr: "invalid time"},
		{name: "timestamp", query: "channel_id=channel-1&timestamp=bad", wantErr: "timestamp"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/logs/download?"+tt.query, nil)
			rec := httptest.NewRecorder()

			srv.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tt.wantErr) {
				t.Fatalf("body = %q, want mention of %q", rec.Body.String(), tt.wantErr)
			}
		})
	}
}

func TestLogDownloadRejectsUnsafeSegmentPath(t *testing.T) {
	root := t.TempDir()
	db, logDir := openLogDownloadDB(t, root)
	start := time.Date(2026, 5, 19, 0, 0, 0, 0, time.UTC)
	if err := db.InsertLogSegment(storage.LogSegment{
		ChannelID:  "channel-1",
		Path:       "../outside.rlog",
		StartTime:  start,
		EndTime:    start.Add(time.Second),
		SizeBytes:  1,
		FrameCount: 1,
		Status:     storage.LogSegmentStatusClosed,
	}); err != nil {
		t.Fatalf("InsertLogSegment returned error: %v", err)
	}

	srv := server.New(server.ServerConfig{DB: db, LogDir: logDir})
	query := url.Values{
		"channel_id": {"channel-1"},
		"from":       {start.Add(-time.Second).Format(time.RFC3339Nano)},
		"to":         {start.Add(time.Minute).Format(time.RFC3339Nano)},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/logs/download?"+query.Encode(), nil)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if !strings.Contains(body["error"], "invalid log segment path") {
		t.Fatalf("error = %q, want invalid log segment path", body["error"])
	}
}

func openLogDownloadDB(t *testing.T, root string) (*storage.DB, string) {
	t.Helper()

	db, err := storage.Open(filepath.Join(root, "meta.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, filepath.Join(root, "logs")
}

func insertLogSegment(t *testing.T, db *storage.DB, logDir string, channelID string, frames ...protocol.LogFrame) logstore.SegmentInfo {
	t.Helper()

	writer, err := logstore.NewSegmentWriter(logDir, channelID, 1024*1024)
	if err != nil {
		t.Fatalf("NewSegmentWriter returned error: %v", err)
	}
	for _, frame := range frames {
		if err := writer.WriteFrame(frame); err != nil {
			t.Fatalf("WriteFrame returned error: %v", err)
		}
	}
	segment, err := writer.Close()
	if err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if err := db.InsertLogSegment(storage.LogSegment{
		ChannelID:  channelID,
		Path:       segment.RelativePath,
		StartTime:  segment.StartTime,
		EndTime:    segment.EndTime,
		SizeBytes:  segment.SizeBytes,
		FrameCount: segment.FrameCount,
		Status:     storage.LogSegmentStatusClosed,
	}); err != nil {
		t.Fatalf("InsertLogSegment returned error: %v", err)
	}
	return segment
}
