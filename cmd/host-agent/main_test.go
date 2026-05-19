package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadOrCreateAgentIDReusesExistingTrimmedID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent_id")
	want := "existing-agent"
	if err := os.WriteFile(path, []byte(" \n\t"+want+"\n "), 0o600); err != nil {
		t.Fatalf("write existing agent_id: %v", err)
	}

	got, err := loadOrCreateAgentID(dir)
	if err != nil {
		t.Fatalf("load agent_id: %v", err)
	}

	if got != want {
		t.Fatalf("expected trimmed existing agent_id %q, got %q", want, got)
	}
}

func TestLoadOrCreateAgentIDCreatesMissingID(t *testing.T) {
	dir := t.TempDir()

	got, err := loadOrCreateAgentID(dir)
	if err != nil {
		t.Fatalf("load missing agent_id: %v", err)
	}

	assertNonEmptyAgentID(t, got)
	data, err := os.ReadFile(filepath.Join(dir, "agent_id"))
	if err != nil {
		t.Fatalf("read created agent_id: %v", err)
	}
	if strings.TrimSpace(string(data)) != got {
		t.Fatalf("expected created file to contain %q, got %q", got, string(data))
	}
}

func TestLoadOrCreateAgentIDReplacesEmptyExistingID(t *testing.T) {
	for _, tc := range []struct {
		name string
		data string
	}{
		{name: "empty", data: ""},
		{name: "whitespace", data: " \n\t\r"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "agent_id")
			if err := os.WriteFile(path, []byte(tc.data), 0o600); err != nil {
				t.Fatalf("write stale agent_id: %v", err)
			}

			got, err := loadOrCreateAgentID(dir)
			if err != nil {
				t.Fatalf("load stale agent_id: %v", err)
			}

			assertNonEmptyAgentID(t, got)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read replaced agent_id: %v", err)
			}
			if strings.TrimSpace(string(data)) != got {
				t.Fatalf("expected replaced file to contain %q, got %q", got, string(data))
			}
		})
	}
}

func assertNonEmptyAgentID(t *testing.T, got string) {
	t.Helper()
	if got == "" {
		t.Fatalf("expected non-empty agent_id")
	}
	if strings.TrimSpace(got) != got {
		t.Fatalf("expected trimmed agent_id, got %q", got)
	}
}
