package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunListsHostsAndChannels(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantPath string
		body     string
		wantOut  string
	}{
		{
			name:     "hosts",
			args:     []string{"hosts", "list"},
			wantPath: "/api/agents",
			body:     `[{"id":"agent-1","status":"pending"}]`,
			wantOut:  "[\n  {\n    \"id\": \"agent-1\",\n    \"status\": \"pending\"\n  }\n]\n",
		},
		{
			name:     "channels",
			args:     []string{"channels", "list"},
			wantPath: "/api/channels",
			body:     `[{"id":"channel-1","auto_name":"host01.hub01.port01.if00"}]`,
			wantOut:  "[\n  {\n    \"auto_name\": \"host01.hub01.port01.if00\",\n    \"id\": \"channel-1\"\n  }\n]\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tt.wantPath {
					t.Fatalf("path = %q, want %q", r.URL.Path, tt.wantPath)
				}
				_, _ = w.Write([]byte(tt.body))
			}))
			t.Cleanup(server.Close)

			var out bytes.Buffer
			args := append([]string{"--server", server.URL}, tt.args...)
			if err := run(args, &out); err != nil {
				t.Fatalf("run returned error: %v", err)
			}
			if out.String() != tt.wantOut {
				t.Fatalf("stdout = %q, want %q", out.String(), tt.wantOut)
			}
		})
	}
}

func TestRunDownloadsLogsToStdoutWithQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/logs/download" {
			t.Fatalf("path = %q, want /api/logs/download", r.URL.Path)
		}
		query := r.URL.Query()
		wants := map[string]string{
			"channel_id":      "channel-1",
			"from":            "2026-05-19T00:00:00Z",
			"to":              "2026-05-19T01:00:00Z",
			"format":          "text",
			"direction":       "rx",
			"timestamp":       "true",
			"direction_label": "true",
			"strip_ansi":      "true",
		}
		for key, want := range wants {
			if got := query.Get(key); got != want {
				t.Fatalf("query %s = %q, want %q", key, got, want)
			}
		}
		_, _ = w.Write([]byte("downloaded log"))
	}))
	t.Cleanup(server.Close)

	var out bytes.Buffer
	err := run([]string{
		"--server", server.URL,
		"logs", "download",
		"--channel-id", "channel-1",
		"--from", "2026-05-19T00:00:00Z",
		"--to", "2026-05-19T01:00:00Z",
		"--format", "text",
		"--direction", "rx",
		"--timestamp",
		"--direction-label",
		"--strip-ansi",
	}, &out)
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if out.String() != "downloaded log" {
		t.Fatalf("stdout = %q, want downloaded log", out.String())
	}
}

func TestRunDownloadsLogsToOutputFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("file log"))
	}))
	t.Cleanup(server.Close)
	outputPath := filepath.Join(t.TempDir(), "channel-1.log")

	var out bytes.Buffer
	err := run([]string{
		"--server", server.URL,
		"logs", "download",
		"--channel-id", "channel-1",
		"--output", outputPath,
	}, &out)
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want empty when writing output file", out.String())
	}
	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if string(content) != "file log" {
		t.Fatalf("output file = %q, want file log", string(content))
	}
}

func TestRunReportsHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad query", http.StatusBadRequest)
	}))
	t.Cleanup(server.Close)

	var out bytes.Buffer
	err := run([]string{"--server", server.URL, "hosts", "list"}, &out)
	if err == nil {
		t.Fatal("run returned nil error, want HTTP error")
	}
	if !strings.Contains(err.Error(), "400 Bad Request") || !strings.Contains(err.Error(), "bad query") {
		t.Fatalf("error = %q, want status and body", err.Error())
	}
}

func TestRunRequiresChannelIDForLogDownload(t *testing.T) {
	var out bytes.Buffer
	err := run([]string{"logs", "download"}, &out)
	if err == nil {
		t.Fatal("run returned nil error, want missing channel ID error")
	}
	if !strings.Contains(err.Error(), "--channel-id") {
		t.Fatalf("error = %q, want mention of --channel-id", err.Error())
	}
}
