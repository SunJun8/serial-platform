package server_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
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
	got, err := db.GetChannel(created.ID)
	if err != nil {
		t.Fatalf("GetChannel returned error: %v", err)
	}
	if got.Alias != "renamed" || got.DefaultBaud != 921600 {
		t.Fatalf("channel = %+v, want alias renamed and default baud 921600", got)
	}
}

func TestChannelAPIPatchAgentIDKeepsAutoNameInterfaceSuffix(t *testing.T) {
	db := newAPITestDB(t)
	channel := apiTestChannel("channel-1", 7001)
	channel.AgentID = "agent-1"
	channel.AutoName = "agent-1.if02"
	if err := db.UpsertChannel(channel); err != nil {
		t.Fatalf("UpsertChannel returned error: %v", err)
	}
	srv := server.New(server.ServerConfig{DB: db})
	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)

	resp, respBody := patchJSON(t, httpSrv.URL+"/api/channels/channel-1", `{"agent_id":"agent-2"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH /api/channels status = %s, body = %s", resp.Status, respBody)
	}
	got, err := db.GetChannel("channel-1")
	if err != nil {
		t.Fatalf("GetChannel returned error: %v", err)
	}
	if got.AutoName != "agent-2.if02" {
		t.Fatalf("AutoName = %q, want %q", got.AutoName, "agent-2.if02")
	}
}

func TestChannelAPIPatchRejectsInvalidSerialConfig(t *testing.T) {
	db := newAPITestDB(t)
	if err := db.UpsertChannel(apiTestChannel("channel-1", 7001)); err != nil {
		t.Fatalf("UpsertChannel returned error: %v", err)
	}
	srv := server.New(server.ServerConfig{DB: db})
	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)

	tests := []struct {
		name string
		body string
	}{
		{name: "port below range", body: `{"rfc2217_port":0}`},
		{name: "port above range", body: `{"rfc2217_port":65536}`},
		{name: "baud below range", body: `{"default_baud":0}`},
		{name: "data bits below range", body: `{"default_data_bits":4}`},
		{name: "unknown parity", body: `{"default_parity":"X"}`},
		{name: "unknown stop bits", body: `{"default_stop_bits":3}`},
		{name: "unknown flow", body: `{"default_flow":"xonxoff"}`},
		{name: "unsupported rtscts flow", body: `{"default_flow":"rtscts"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, respBody := patchJSON(t, httpSrv.URL+"/api/channels/channel-1", tt.body)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("PATCH /api/channels status = %s, body = %s", resp.Status, respBody)
			}
		})
	}
}

func TestChannelAPICreateRejectsInvalidSerialConfig(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "explicit zero baud", body: `{"agent_id":"agent-1","rfc2217_port":7001,"default_baud":0}`},
		{name: "explicit zero data bits", body: `{"agent_id":"agent-1","rfc2217_port":7001,"default_data_bits":0}`},
		{name: "explicit zero stop bits", body: `{"agent_id":"agent-1","rfc2217_port":7001,"default_stop_bits":0}`},
		{name: "unsupported rtscts flow", body: `{"agent_id":"agent-1","rfc2217_port":7001,"default_flow":"rtscts"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := newAPITestDB(t)
			srv := server.New(server.ServerConfig{DB: db})
			httpSrv := httptest.NewServer(srv)
			t.Cleanup(httpSrv.Close)

			resp, respBody := postJSON(t, httpSrv.URL+"/api/channels", tt.body)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("POST /api/channels status = %s, body = %s", resp.Status, respBody)
			}
		})
	}
}

func TestChannelAPIPortConflictsReturnConflict(t *testing.T) {
	db := newAPITestDB(t)
	if err := db.UpsertChannel(apiTestChannel("channel-1", 7001)); err != nil {
		t.Fatalf("UpsertChannel channel-1 returned error: %v", err)
	}
	if err := db.UpsertChannel(apiTestChannel("channel-2", 7002)); err != nil {
		t.Fatalf("UpsertChannel channel-2 returned error: %v", err)
	}
	candidate := apiTestCandidate("cand-1")
	if err := db.UpsertCandidate(candidate); err != nil {
		t.Fatalf("UpsertCandidate returned error: %v", err)
	}
	srv := server.New(server.ServerConfig{DB: db})
	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)

	tests := []struct {
		name    string
		request func(t *testing.T) (*http.Response, []byte)
	}{
		{
			name: "create",
			request: func(t *testing.T) (*http.Response, []byte) {
				return postJSON(t, httpSrv.URL+"/api/channels", `{"agent_id":"agent-3","alias":"duplicate","rfc2217_port":7001}`)
			},
		},
		{
			name: "patch",
			request: func(t *testing.T) (*http.Response, []byte) {
				return patchJSON(t, httpSrv.URL+"/api/channels/channel-2", `{"rfc2217_port":7001}`)
			},
		},
		{
			name: "confirm",
			request: func(t *testing.T) (*http.Response, []byte) {
				return postJSON(t, httpSrv.URL+"/api/candidates/cand-1/confirm", `{"alias":"duplicate","role":"console","rfc2217_port":7001}`)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, respBody := tt.request(t)
			if resp.StatusCode != http.StatusConflict {
				t.Fatalf("status = %s, body = %s", resp.Status, respBody)
			}
			if !strings.Contains(string(respBody), "rfc2217_port already exists") {
				t.Fatalf("body = %s, want clear rfc2217_port conflict", respBody)
			}
		})
	}
	if _, err := db.GetCandidate("cand-1"); err != nil {
		t.Fatalf("GetCandidate after failed confirm returned error: %v", err)
	}
	if _, err := db.GetChannel("cand-1"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("GetChannel cand-1 error = %v, want ErrNotFound", err)
	}
}

func TestCandidateConfirmCreatesChannelAndDeletesCandidate(t *testing.T) {
	db := newAPITestDB(t)
	now := time.Unix(10, 0).UTC()
	candidate := storage.Candidate{
		ID:           "cand-1",
		AgentID:      "agent-1",
		DevName:      "/dev/ttyUSB0",
		IDPath:       "id-path",
		IDPathTag:    "id-tag",
		SysfsDevpath: "/devices/pci/ttyUSB0",
		Interface:    "02",
		VID:          "1a86",
		PID:          "7523",
		Serial:       "serial-a",
		Driver:       "ch341",
		FirstSeen:    now,
		LastSeen:     now,
	}
	if err := db.UpsertCandidate(candidate); err != nil {
		t.Fatalf("UpsertCandidate returned error: %v", err)
	}
	srv := server.New(server.ServerConfig{DB: db})
	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)

	resp, respBody := postJSON(t, httpSrv.URL+"/api/candidates/cand-1/confirm", `{"alias":"loopback","role":"console","rfc2217_port":7001,"default_baud":115200}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("confirm status = %s, body = %s", resp.Status, respBody)
	}
	var created storage.Channel
	if err := json.Unmarshal(respBody, &created); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if created.ID == "" {
		t.Fatalf("created channel ID is empty")
	}
	channel, err := db.GetChannel(created.ID)
	if err != nil {
		t.Fatalf("GetChannel returned error: %v", err)
	}
	if channel.AgentID != candidate.AgentID ||
		channel.DevName != candidate.DevName ||
		channel.IDPath != candidate.IDPath ||
		channel.IDPathTag != candidate.IDPathTag ||
		channel.SysfsDevpath != candidate.SysfsDevpath ||
		channel.AutoName != "agent-1.if02" {
		t.Fatalf("channel = %+v, candidate = %+v", channel, candidate)
	}
	candidates, err := db.ListCandidates()
	if err != nil {
		t.Fatalf("ListCandidates returned error: %v", err)
	}
	if len(candidates) != 0 {
		t.Fatalf("candidate was not deleted: %+v", candidates)
	}
}

func TestCandidateConfirmRejectsInvalidSerialConfig(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "explicit zero baud", body: `{"alias":"loopback","role":"console","rfc2217_port":7001,"default_baud":0}`},
		{name: "explicit zero data bits", body: `{"alias":"loopback","role":"console","rfc2217_port":7001,"default_data_bits":0}`},
		{name: "explicit zero stop bits", body: `{"alias":"loopback","role":"console","rfc2217_port":7001,"default_stop_bits":0}`},
		{name: "unsupported rtscts flow", body: `{"alias":"loopback","role":"console","rfc2217_port":7001,"default_flow":"rtscts"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := newAPITestDB(t)
			candidate := apiTestCandidate("cand-1")
			if err := db.UpsertCandidate(candidate); err != nil {
				t.Fatalf("UpsertCandidate returned error: %v", err)
			}
			srv := server.New(server.ServerConfig{DB: db})
			httpSrv := httptest.NewServer(srv)
			t.Cleanup(httpSrv.Close)

			resp, respBody := postJSON(t, httpSrv.URL+"/api/candidates/cand-1/confirm", tt.body)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("confirm status = %s, body = %s", resp.Status, respBody)
			}
			if _, err := db.GetCandidate("cand-1"); err != nil {
				t.Fatalf("GetCandidate after failed confirm returned error: %v", err)
			}
		})
	}
}

func TestChannelAPIDeleteRemovesChannelAndLogs(t *testing.T) {
	root := t.TempDir()
	db := newAPITestDB(t)
	logDir := filepath.Join(root, "logs")
	channel := apiTestChannel("channel-1", 7001)
	if err := db.UpsertChannel(channel); err != nil {
		t.Fatalf("UpsertChannel returned error: %v", err)
	}
	now := time.Unix(1700000000, 0).UTC()
	segmentPath := filepath.Join("channel-1", "segment.rlog")
	fullSegmentPath := filepath.Join(logDir, segmentPath)
	if err := os.MkdirAll(filepath.Dir(fullSegmentPath), 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.WriteFile(fullSegmentPath, []byte("raw"), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	if err := db.InsertLogSegment(storage.LogSegment{ChannelID: "channel-1", Path: segmentPath, StartTime: now, EndTime: now, SizeBytes: 3, FrameCount: 1, Status: storage.LogSegmentStatusClosed}); err != nil {
		t.Fatalf("InsertLogSegment returned error: %v", err)
	}
	srv := server.New(server.ServerConfig{DB: db, LogDir: logDir})
	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)

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
	if _, err := db.GetChannel("channel-1"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("GetChannel error = %v, want ErrNotFound", err)
	}
	if _, err := os.Stat(fullSegmentPath); !os.IsNotExist(err) {
		t.Fatalf("log segment stat error = %v, want not exist", err)
	}
	segments, err := db.ListLogSegments("channel-1", now.Add(-time.Second), now.Add(time.Second))
	if err != nil {
		t.Fatalf("ListLogSegments returned error: %v", err)
	}
	if len(segments) != 0 {
		t.Fatalf("segments = %+v, want empty", segments)
	}
}

func TestChannelAPIDeleteMissingChannelReturnsNotFound(t *testing.T) {
	db := newAPITestDB(t)
	srv := server.New(server.ServerConfig{DB: db, LogDir: t.TempDir()})
	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)
	req, err := http.NewRequest(http.MethodDelete, httpSrv.URL+"/api/channels/missing", nil)
	if err != nil {
		t.Fatalf("NewRequest returned error: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do returned error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("DELETE missing status = %s, body = %s", resp.Status, body)
	}
}

func TestChannelAPIDeleteBusyChannelReturnsConflict(t *testing.T) {
	db := newAPITestDB(t)
	if err := db.UpsertChannel(apiTestChannel("channel-1", 7001)); err != nil {
		t.Fatalf("UpsertChannel returned error: %v", err)
	}
	srv := server.New(server.ServerConfig{DB: db, LogDir: t.TempDir()})
	owners := srv.ControlOwnerForTest()
	if err := owners.Acquire("channel-1", "web"); err != nil {
		t.Fatalf("Acquire returned error: %v", err)
	}
	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)
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
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("DELETE busy status = %s, body = %s", resp.Status, body)
	}
	if _, err := db.GetChannel("channel-1"); err != nil {
		t.Fatalf("GetChannel after busy delete returned error: %v", err)
	}
}

func TestChannelAPIDeleteIgnoresMissingLogFiles(t *testing.T) {
	db := newAPITestDB(t)
	if err := db.UpsertChannel(apiTestChannel("channel-1", 7001)); err != nil {
		t.Fatalf("UpsertChannel returned error: %v", err)
	}
	now := time.Unix(1700000000, 0).UTC()
	if err := db.InsertLogSegment(storage.LogSegment{
		ChannelID:  "channel-1",
		Path:       filepath.Join("channel-1", "missing.rlog"),
		StartTime:  now,
		EndTime:    now,
		SizeBytes:  3,
		FrameCount: 1,
		Status:     storage.LogSegmentStatusClosed,
	}); err != nil {
		t.Fatalf("InsertLogSegment returned error: %v", err)
	}
	srv := server.New(server.ServerConfig{DB: db, LogDir: t.TempDir()})
	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)

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
	if _, err := db.GetChannel("channel-1"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("GetChannel error = %v, want ErrNotFound", err)
	}
	segments, err := db.ListLogSegments("channel-1", now.Add(-time.Second), now.Add(time.Second))
	if err != nil {
		t.Fatalf("ListLogSegments returned error: %v", err)
	}
	if len(segments) != 0 {
		t.Fatalf("segments = %+v, want empty", segments)
	}
}

func TestChannelAPIDeleteRejectsInvalidLogSegmentPathAndKeepsMetadata(t *testing.T) {
	db := newAPITestDB(t)
	if err := db.UpsertChannel(apiTestChannel("channel-1", 7001)); err != nil {
		t.Fatalf("UpsertChannel returned error: %v", err)
	}
	now := time.Unix(1700000000, 0).UTC()
	if err := db.InsertLogSegment(storage.LogSegment{
		ChannelID:  "channel-1",
		Path:       filepath.Join("..", "segment.rlog"),
		StartTime:  now,
		EndTime:    now,
		SizeBytes:  3,
		FrameCount: 1,
		Status:     storage.LogSegmentStatusClosed,
	}); err != nil {
		t.Fatalf("InsertLogSegment returned error: %v", err)
	}
	srv := server.New(server.ServerConfig{DB: db, LogDir: t.TempDir()})
	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)

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
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("DELETE invalid segment status = %s, body = %s", resp.Status, body)
	}
	if _, err := db.GetChannel("channel-1"); err != nil {
		t.Fatalf("GetChannel after failed delete returned error: %v", err)
	}
	segments, err := db.ListLogSegments("channel-1", now.Add(-time.Second), now.Add(time.Second))
	if err != nil {
		t.Fatalf("ListLogSegments returned error: %v", err)
	}
	if len(segments) != 1 || segments[0].Path != filepath.Join("..", "segment.rlog") {
		t.Fatalf("segments = %+v, want original invalid segment metadata", segments)
	}
}

func apiTestChannel(id string, port int) storage.Channel {
	return storage.Channel{
		ID:              id,
		AgentID:         "agent-1",
		AutoName:        "agent-1.if00",
		Alias:           id,
		Role:            "console",
		DevName:         "/dev/ttyUSB0",
		IDPath:          "id-path-" + id,
		IDPathTag:       "id-tag-" + id,
		SysfsDevpath:    "/devices/" + id,
		RFC2217Port:     port,
		Status:          storage.ChannelStatusOffline,
		DefaultBaud:     115200,
		DefaultDataBits: 8,
		DefaultParity:   "N",
		DefaultStopBits: 1,
		DefaultFlow:     "none",
		UpdatedAt:       time.Unix(1, 0).UTC(),
	}
}

func apiTestCandidate(id string) storage.Candidate {
	now := time.Unix(10, 0).UTC()
	return storage.Candidate{
		ID:           id,
		AgentID:      "agent-1",
		DevName:      "/dev/ttyUSB0",
		IDPath:       "id-path-" + id,
		IDPathTag:    "id-tag-" + id,
		SysfsDevpath: "/devices/" + id,
		Interface:    "00",
		VID:          "1a86",
		PID:          "7523",
		Serial:       "serial-a",
		Driver:       "ch341",
		FirstSeen:    now,
		LastSeen:     now,
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
