package storage

import (
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
