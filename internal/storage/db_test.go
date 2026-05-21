package storage

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestDBCreatesAgentAndChannel(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "meta.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	agent := Agent{
		ID:        "agent-1",
		Name:      "node-1",
		Status:    AgentStatusPending,
		Hostname:  "node-1",
		OS:        "linux",
		Arch:      "arm64",
		MachineID: "machine-1",
		UpdatedAt: time.Unix(100, 0).UTC(),
	}
	if err := db.UpsertAgent(agent); err != nil {
		t.Fatalf("UpsertAgent returned error: %v", err)
	}

	agents, err := db.ListAgents()
	if err != nil {
		t.Fatalf("ListAgents returned error: %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("len(agents) = %d, want 1", len(agents))
	}
	if agents[0].Status != AgentStatusPending {
		t.Fatalf("Status = %q", agents[0].Status)
	}

	channel := Channel{
		ID:              "channel-1",
		AgentID:         "agent-1",
		AutoName:        "host01.hub01.port01.if00",
		Alias:           "rack1.port01.console",
		Role:            "console",
		IDPath:          "pci-0000:00:14.0-usb-0:1:1.0",
		IDPathTag:       "pci-0000_00_14_0-usb-0_1_1_0",
		RFC2217Port:     7001,
		Status:          ChannelStatusDisabled,
		DefaultBaud:     115200,
		DefaultDataBits: 8,
		DefaultParity:   "N",
		DefaultStopBits: 1,
		UpdatedAt:       time.Unix(101, 0).UTC(),
	}
	if err := db.UpsertChannel(channel); err != nil {
		t.Fatalf("UpsertChannel returned error: %v", err)
	}
	channels, err := db.ListChannels()
	if err != nil {
		t.Fatalf("ListChannels returned error: %v", err)
	}
	if len(channels) != 1 {
		t.Fatalf("len(channels) = %d, want 1", len(channels))
	}
	if channels[0].IDPath != channel.IDPath {
		t.Fatalf("IDPath = %q, want %q", channels[0].IDPath, channel.IDPath)
	}
}

func TestDBListsOverlappingLogSegments(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "meta.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	base := time.Unix(1700000000, 0).UTC()
	segments := []LogSegment{
		{
			ChannelID:  "channel-1",
			Path:       "channel-1/old.rlog",
			StartTime:  base,
			EndTime:    base.Add(10 * time.Minute),
			SizeBytes:  100,
			FrameCount: 1,
			Status:     LogSegmentStatusClosed,
		},
		{
			ChannelID:  "channel-1",
			Path:       "channel-1/second.rlog",
			StartTime:  base.Add(45 * time.Minute),
			EndTime:    base.Add(60 * time.Minute),
			SizeBytes:  200,
			FrameCount: 2,
			Status:     LogSegmentStatusClosed,
		},
		{
			ChannelID:  "channel-1",
			Path:       "channel-1/first.rlog",
			StartTime:  base.Add(30 * time.Minute),
			EndTime:    base.Add(40 * time.Minute),
			SizeBytes:  300,
			FrameCount: 3,
			Status:     LogSegmentStatusActive,
		},
		{
			ChannelID:  "channel-2",
			Path:       "channel-2/matching-time.rlog",
			StartTime:  base.Add(35 * time.Minute),
			EndTime:    base.Add(50 * time.Minute),
			SizeBytes:  400,
			FrameCount: 4,
			Status:     LogSegmentStatusClosed,
		},
	}
	for _, segment := range segments {
		if err := db.InsertLogSegment(segment); err != nil {
			t.Fatalf("InsertLogSegment returned error: %v", err)
		}
	}

	got, err := db.ListLogSegments("channel-1", base.Add(35*time.Minute), base.Add(50*time.Minute))
	if err != nil {
		t.Fatalf("ListLogSegments returned error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	if got[0].Path != "channel-1/first.rlog" || got[1].Path != "channel-1/second.rlog" {
		t.Fatalf("paths = [%q, %q], want start_time order", got[0].Path, got[1].Path)
	}
	if got[0].Status != LogSegmentStatusActive {
		t.Fatalf("Status = %q, want %q", got[0].Status, LogSegmentStatusActive)
	}
}

func TestDBRejectsDuplicateLogSegmentPath(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "meta.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	now := time.Unix(1700000000, 0).UTC()
	segment := LogSegment{
		ChannelID:  "channel-1",
		Path:       "channel-1/segment.rlog",
		StartTime:  now,
		EndTime:    now,
		SizeBytes:  12,
		FrameCount: 1,
		Status:     LogSegmentStatusActive,
	}
	if err := db.InsertLogSegment(segment); err != nil {
		t.Fatalf("first InsertLogSegment returned error: %v", err)
	}
	segment.ChannelID = "channel-2"
	if err := db.InsertLogSegment(segment); err == nil {
		t.Fatal("second InsertLogSegment returned nil error, want duplicate path rejected")
	}
}

func TestDBOpenAddsUniqueLogSegmentPathIndexToExistingDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meta.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open returned error: %v", err)
	}
	if _, err := legacy.Exec(`
CREATE TABLE log_segments (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  channel_id TEXT NOT NULL,
  path TEXT NOT NULL,
  start_time TEXT NOT NULL,
  end_time TEXT NOT NULL,
  size_bytes INTEGER NOT NULL,
  frame_count INTEGER NOT NULL,
  status TEXT NOT NULL
)`); err != nil {
		t.Fatalf("legacy schema setup returned error: %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("legacy Close returned error: %v", err)
	}

	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	now := time.Unix(1700000000, 0).UTC()
	segment := LogSegment{
		ChannelID:  "channel-1",
		Path:       "channel-1/segment.rlog",
		StartTime:  now,
		EndTime:    now,
		SizeBytes:  12,
		FrameCount: 1,
		Status:     LogSegmentStatusActive,
	}
	if err := db.InsertLogSegment(segment); err != nil {
		t.Fatalf("first InsertLogSegment returned error: %v", err)
	}
	segment.SizeBytes = 24
	if err := db.InsertLogSegment(segment); err == nil {
		t.Fatal("second InsertLogSegment returned nil error, want unique index to reject duplicate path")
	}
}

func TestDBOpenDeduplicatesLogSegmentsBeforeUniqueIndex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meta.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open returned error: %v", err)
	}
	if _, err := legacy.Exec(`
CREATE TABLE log_segments (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  channel_id TEXT NOT NULL,
  path TEXT NOT NULL,
  start_time TEXT NOT NULL,
  end_time TEXT NOT NULL,
  size_bytes INTEGER NOT NULL,
  frame_count INTEGER NOT NULL,
  status TEXT NOT NULL
)`); err != nil {
		t.Fatalf("legacy schema setup returned error: %v", err)
	}
	base := time.Unix(1700000000, 0).UTC()
	if _, err := legacy.Exec(`
INSERT INTO log_segments (
  id, channel_id, path, start_time, end_time, size_bytes, frame_count, status
) VALUES
  (?, ?, ?, ?, ?, ?, ?, ?),
  (?, ?, ?, ?, ?, ?, ?, ?)
`,
		1, "channel-1", "channel-1/segment.rlog", base.Format(time.RFC3339Nano), base.Format(time.RFC3339Nano), 12, 1, string(LogSegmentStatusActive),
		2, "channel-1", "channel-1/segment.rlog", base.Format(time.RFC3339Nano), base.Add(time.Minute).Format(time.RFC3339Nano), 24, 2, string(LogSegmentStatusClosed),
	); err != nil {
		t.Fatalf("legacy data setup returned error: %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("legacy Close returned error: %v", err)
	}

	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	var id int64
	var sizeBytes int64
	var frameCount int64
	var status string
	var count int
	if err := db.sql.QueryRow(`
SELECT COUNT(*), id, size_bytes, frame_count, status
FROM log_segments
WHERE path = ?
`, "channel-1/segment.rlog").Scan(&count, &id, &sizeBytes, &frameCount, &status); err != nil {
		t.Fatalf("deduplicated row query returned error: %v", err)
	}
	if count != 1 {
		t.Fatalf("row count for path = %d, want 1", count)
	}
	if id != 2 || sizeBytes != 24 || frameCount != 2 || status != string(LogSegmentStatusClosed) {
		t.Fatalf("deduplicated row = id %d size %d frames %d status %q, want id 2 size 24 frames 2 status %q",
			id, sizeBytes, frameCount, status, LogSegmentStatusClosed)
	}

	duplicate := LogSegment{
		ChannelID:  "channel-2",
		Path:       "channel-1/segment.rlog",
		StartTime:  base,
		EndTime:    base,
		SizeBytes:  36,
		FrameCount: 3,
		Status:     LogSegmentStatusActive,
	}
	if err := db.InsertLogSegment(duplicate); err == nil {
		t.Fatal("InsertLogSegment returned nil error, want unique index to reject duplicate path")
	}
}

func TestDBUpsertsLogSegmentByPath(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "meta.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	start := time.Unix(1700000000, 0).UTC()
	active := LogSegment{
		ChannelID:  "channel-1",
		Path:       "channel-1/segment.rlog",
		StartTime:  start,
		EndTime:    start,
		SizeBytes:  12,
		FrameCount: 1,
		Status:     LogSegmentStatusActive,
	}
	if err := db.UpsertLogSegment(active); err != nil {
		t.Fatalf("active UpsertLogSegment returned error: %v", err)
	}
	before, err := db.ListLogSegments("channel-1", start.Add(-time.Second), start.Add(time.Second))
	if err != nil {
		t.Fatalf("ListLogSegments before close returned error: %v", err)
	}
	if len(before) != 1 {
		t.Fatalf("len(before) = %d, want 1", len(before))
	}

	closed := active
	closed.EndTime = start.Add(time.Second)
	closed.SizeBytes = 36
	closed.FrameCount = 3
	closed.Status = LogSegmentStatusClosed
	if err := db.UpsertLogSegment(closed); err != nil {
		t.Fatalf("closed UpsertLogSegment returned error: %v", err)
	}

	after, err := db.ListLogSegments("channel-1", start.Add(-time.Second), start.Add(2*time.Second))
	if err != nil {
		t.Fatalf("ListLogSegments after close returned error: %v", err)
	}
	if len(after) != 1 {
		t.Fatalf("len(after) = %d, want 1", len(after))
	}
	if after[0].ID != before[0].ID {
		t.Fatalf("ID = %d, want same row ID %d", after[0].ID, before[0].ID)
	}
	if after[0].Status != LogSegmentStatusClosed || after[0].SizeBytes != 36 || after[0].FrameCount != 3 {
		t.Fatalf("segment after close = %+v, want closed size 36 frame_count 3", after[0])
	}
	var count int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM log_segments WHERE path = ?`, active.Path).Scan(&count); err != nil {
		t.Fatalf("COUNT query returned error: %v", err)
	}
	if count != 1 {
		t.Fatalf("row count for path = %d, want 1", count)
	}
}

func TestDBUpsertsCandidatesAndConfirmsChannel(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "meta.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	now := time.Unix(1700000000, 0).UTC()
	candidate := Candidate{
		ID:           "candidate-1",
		AgentID:      "agent-1",
		DevName:      "/dev/ttyUSB0",
		IDPath:       "pci-0000:00:14.0-usb-0:1.2:1.0",
		IDPathTag:    "pci-0000_00_14_0-usb-0_1_2_1_0",
		SysfsDevpath: "/devices/pci/ttyUSB0",
		Interface:    "00",
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
	candidates, err := db.ListCandidates()
	if err != nil {
		t.Fatalf("ListCandidates returned error: %v", err)
	}
	if len(candidates) != 1 || candidates[0].IDPath != candidate.IDPath {
		t.Fatalf("candidates = %+v", candidates)
	}

	updatedCandidate := candidate
	updatedCandidate.DevName = "/dev/ttyUSB1"
	updatedCandidate.IDPath = "pci-0000:00:14.0-usb-0:1.3:1.0"
	updatedCandidate.IDPathTag = "pci-0000_00_14_0-usb-0_1_3_1_0"
	updatedCandidate.SysfsDevpath = "/devices/pci/ttyUSB1"
	updatedCandidate.Serial = "serial-b"
	updatedCandidate.LastSeen = now.Add(time.Minute)
	if err := db.UpsertCandidate(updatedCandidate); err != nil {
		t.Fatalf("UpsertCandidate update returned error: %v", err)
	}
	candidates, err = db.ListCandidates()
	if err != nil {
		t.Fatalf("ListCandidates returned error: %v", err)
	}
	if len(candidates) != 1 || candidates[0].DevName != updatedCandidate.DevName || candidates[0].Serial != updatedCandidate.Serial {
		t.Fatalf("updated candidates = %+v", candidates)
	}
	if !candidates[0].FirstSeen.Equal(candidate.FirstSeen) {
		t.Fatalf("FirstSeen = %s, want original %s", candidates[0].FirstSeen, candidate.FirstSeen)
	}

	channel := Channel{
		ID:              "channel-1",
		AgentID:         "agent-1",
		AutoName:        "agent-1.if00",
		Alias:           "loopback",
		Role:            "console",
		DevName:         updatedCandidate.DevName,
		IDPath:          updatedCandidate.IDPath,
		IDPathTag:       updatedCandidate.IDPathTag,
		SysfsDevpath:    updatedCandidate.SysfsDevpath,
		RFC2217Port:     7001,
		Status:          ChannelStatusOffline,
		DefaultBaud:     115200,
		DefaultDataBits: 8,
		DefaultParity:   "N",
		DefaultStopBits: 1,
		DefaultFlow:     "none",
		UpdatedAt:       now,
	}
	if err := db.UpsertChannel(channel); err != nil {
		t.Fatalf("UpsertChannel returned error: %v", err)
	}
	if err := db.DeleteCandidate(updatedCandidate.ID); err != nil {
		t.Fatalf("DeleteCandidate returned error: %v", err)
	}
	candidates, err = db.ListCandidates()
	if err != nil {
		t.Fatalf("ListCandidates returned error: %v", err)
	}
	if len(candidates) != 0 {
		t.Fatalf("len(candidates) = %d, want 0", len(candidates))
	}
}

func TestDBConfirmCandidateCreatesChannelAndDeletesCandidateAtomically(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "meta.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	now := time.Unix(1700000000, 0).UTC()
	candidate := Candidate{
		ID:           "candidate-1",
		AgentID:      "agent-1",
		DevName:      "/dev/ttyUSB0",
		IDPath:       "pci-0000:00:14.0-usb-0:1.2:1.0",
		IDPathTag:    "pci-0000_00_14_0-usb-0_1_2_1_0",
		SysfsDevpath: "/devices/pci/ttyUSB0",
		Interface:    "00",
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
	channel := Channel{
		ID:              "channel-1",
		AgentID:         candidate.AgentID,
		AutoName:        "agent-1.if00",
		Alias:           "loopback",
		Role:            "console",
		DevName:         candidate.DevName,
		IDPath:          candidate.IDPath,
		IDPathTag:       candidate.IDPathTag,
		SysfsDevpath:    candidate.SysfsDevpath,
		RFC2217Port:     7001,
		Status:          ChannelStatusOffline,
		DefaultBaud:     115200,
		DefaultDataBits: 8,
		DefaultParity:   "N",
		DefaultStopBits: 1,
		DefaultFlow:     "none",
		UpdatedAt:       now,
	}
	created, err := db.ConfirmCandidate("candidate-1", func(Candidate) Channel {
		return channel
	})
	if err != nil {
		t.Fatalf("ConfirmCandidate returned error: %v", err)
	}
	if created.ID != channel.ID {
		t.Fatalf("created.ID = %q, want %q", created.ID, channel.ID)
	}
	if _, err := db.GetCandidate("candidate-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetCandidate error = %v, want ErrNotFound", err)
	}
	got, err := db.GetChannel("channel-1")
	if err != nil {
		t.Fatalf("GetChannel returned error: %v", err)
	}
	if got.IDPath != candidate.IDPath || got.RFC2217Port != channel.RFC2217Port {
		t.Fatalf("channel = %+v, candidate = %+v", got, candidate)
	}
}

func TestDBConfirmCandidateRollbackKeepsCandidateOnChannelConflict(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "meta.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	now := time.Unix(1700000000, 0).UTC()
	candidate := Candidate{
		ID:           "candidate-1",
		AgentID:      "agent-1",
		DevName:      "/dev/ttyUSB0",
		IDPath:       "id-path",
		IDPathTag:    "id-tag",
		SysfsDevpath: "/devices/pci/ttyUSB0",
		Interface:    "00",
		FirstSeen:    now,
		LastSeen:     now,
	}
	if err := db.UpsertCandidate(candidate); err != nil {
		t.Fatalf("UpsertCandidate returned error: %v", err)
	}
	existing := testChannel("channel-existing")
	existing.RFC2217Port = 7001
	if err := db.UpsertChannel(existing); err != nil {
		t.Fatalf("UpsertChannel returned error: %v", err)
	}
	conflicting := testChannel("channel-new")
	conflicting.RFC2217Port = existing.RFC2217Port
	_, err = db.ConfirmCandidate("candidate-1", func(Candidate) Channel {
		return conflicting
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("ConfirmCandidate error = %v, want ErrConflict", err)
	}
	if _, err := db.GetCandidate("candidate-1"); err != nil {
		t.Fatalf("GetCandidate returned error after rollback: %v", err)
	}
	if _, err := db.GetChannel("channel-new"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetChannel channel-new error = %v, want ErrNotFound", err)
	}
}

func TestDBUpdatesChannelStatusAndConfig(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "meta.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	channel := testChannel("channel-1")
	if err := db.UpsertChannel(channel); err != nil {
		t.Fatalf("UpsertChannel returned error: %v", err)
	}
	got, err := db.GetChannel("channel-1")
	if err != nil {
		t.Fatalf("GetChannel returned error: %v", err)
	}
	if got.DefaultFlow != channel.DefaultFlow {
		t.Fatalf("DefaultFlow = %q, want %q", got.DefaultFlow, channel.DefaultFlow)
	}
	channels, err := db.ListChannels()
	if err != nil {
		t.Fatalf("ListChannels returned error: %v", err)
	}
	if len(channels) != 1 || channels[0].DefaultFlow != channel.DefaultFlow {
		t.Fatalf("channels = %+v", channels)
	}
	if err := db.UpdateChannelStatus("channel-1", ChannelStatusError, "/dev/ttyUSB0", "permission denied", time.Unix(2, 0).UTC()); err != nil {
		t.Fatalf("UpdateChannelStatus returned error: %v", err)
	}
	got, err = db.GetChannel("channel-1")
	if err != nil {
		t.Fatalf("GetChannel returned error: %v", err)
	}
	if got.Status != ChannelStatusError || got.ErrorMessage != "permission denied" || got.DevName != "/dev/ttyUSB0" || got.DefaultFlow != channel.DefaultFlow {
		t.Fatalf("channel = %+v", got)
	}
}

func TestDBUpdatesChannelStatusForAgentOnly(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "meta.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	channel := testChannel("channel-1")
	channel.AgentID = "agent-1"
	channel.DevName = "/dev/ttyUSB0"
	channel.Status = ChannelStatusOffline
	if err := db.UpsertChannel(channel); err != nil {
		t.Fatalf("UpsertChannel returned error: %v", err)
	}

	if err := db.UpdateChannelStatusForAgent("channel-1", "agent-2", ChannelStatusError, "/dev/ttyUSB9", "wrong agent", time.Unix(2, 0).UTC()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("UpdateChannelStatusForAgent wrong agent error = %v, want ErrNotFound", err)
	}
	got, err := db.GetChannel("channel-1")
	if err != nil {
		t.Fatalf("GetChannel returned error: %v", err)
	}
	if got.Status != ChannelStatusOffline || got.DevName != "/dev/ttyUSB0" || got.ErrorMessage != "" {
		t.Fatalf("channel after wrong agent update = %+v, want original status/dev/error", got)
	}

	if err := db.UpdateChannelStatusForAgent("channel-1", "agent-1", ChannelStatusBusy, "/dev/ttyUSB1", "", time.Unix(3, 0).UTC()); err != nil {
		t.Fatalf("UpdateChannelStatusForAgent owner returned error: %v", err)
	}
	got, err = db.GetChannel("channel-1")
	if err != nil {
		t.Fatalf("GetChannel returned error: %v", err)
	}
	if got.Status != ChannelStatusBusy || got.DevName != "/dev/ttyUSB1" {
		t.Fatalf("channel after owner update = %+v, want busy on /dev/ttyUSB1", got)
	}
}

func testChannel(id string) Channel {
	return Channel{
		ID:              id,
		AgentID:         "agent-1",
		AutoName:        "agent-1.if00",
		Alias:           "loopback",
		Role:            "console",
		IDPath:          "id-path",
		IDPathTag:       "id-tag",
		RFC2217Port:     7001,
		Status:          ChannelStatusOffline,
		DefaultBaud:     115200,
		DefaultDataBits: 8,
		DefaultParity:   "N",
		DefaultStopBits: 1,
		DefaultFlow:     "rtscts",
		UpdatedAt:       time.Unix(1, 0).UTC(),
	}
}
