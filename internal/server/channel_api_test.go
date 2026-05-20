package server_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"serial-platform/internal/server"
	"serial-platform/internal/storage"
)

func TestChannelAPICreatesAndUpdatesChannel(t *testing.T) {
	db := newAPITestDB(t)
	srv := server.New(server.ServerConfig{DB: db})
	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)

	body := `{"agent_id":"agent-1","alias":"loopback","role":"console","id_path":"pci-path","id_path_tag":"pci-tag","rfc2217_port":7001,"default_baud":115200,"default_data_bits":8,"default_parity":"N","default_stop_bits":1,"default_flow":"none"}`
	resp, respBody := postJSON(t, httpSrv.URL+"/api/channels", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /api/channels status = %s, body = %s", resp.Status, respBody)
	}

	var created storage.Channel
	if err := json.Unmarshal(respBody, &created); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if created.ID == "" {
		t.Fatalf("created channel ID is empty")
	}

	resp, respBody = patchJSON(t, httpSrv.URL+"/api/channels/"+created.ID, `{"alias":"renamed","default_baud":921600}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH /api/channels status = %s, body = %s", resp.Status, respBody)
	}
}

func TestCandidateConfirmCreatesChannelAndDeletesCandidate(t *testing.T) {
	db := newAPITestDB(t)
	now := time.Unix(10, 0).UTC()
	if err := db.UpsertCandidate(storage.Candidate{ID: "cand-1", AgentID: "agent-1", DevName: "/dev/ttyUSB0", IDPath: "id-path", IDPathTag: "id-tag", FirstSeen: now, LastSeen: now}); err != nil {
		t.Fatalf("UpsertCandidate returned error: %v", err)
	}
	srv := server.New(server.ServerConfig{DB: db})
	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)

	resp, respBody := postJSON(t, httpSrv.URL+"/api/candidates/cand-1/confirm", `{"alias":"loopback","role":"console","rfc2217_port":7001,"default_baud":115200}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("confirm status = %s, body = %s", resp.Status, respBody)
	}
	candidates, err := db.ListCandidates()
	if err != nil {
		t.Fatalf("ListCandidates returned error: %v", err)
	}
	if len(candidates) != 0 {
		t.Fatalf("candidate was not deleted: %+v", candidates)
	}
}

func newAPITestDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "meta.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func postJSON(t *testing.T, url string, body string) (*http.Response, []byte) {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("http.Post returned error: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll returned error: %v", err)
	}
	return resp, respBody
}

func patchJSON(t *testing.T, url string, body string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPatch, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest returned error: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do returned error: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll returned error: %v", err)
	}
	return resp, respBody
}
