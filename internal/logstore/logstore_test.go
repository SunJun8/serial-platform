package logstore

import (
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"serial-platform/internal/protocol"
)

func TestNewSegmentWriterRejectsInvalidChannelIDs(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "logs")
	absoluteChannel := filepath.Join(parent, "absolute-channel")

	tests := []struct {
		name        string
		channelID   string
		outsidePath string
	}{
		{name: "empty", channelID: ""},
		{name: "parent traversal", channelID: "../x", outsidePath: filepath.Join(parent, "x")},
		{name: "path separator", channelID: "a/b"},
		{name: "dotdot", channelID: "..", outsidePath: parent},
		{name: "absolute", channelID: absoluteChannel, outsidePath: absoluteChannel},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writer, err := NewSegmentWriter(root, tt.channelID, 1024*1024)
			if err == nil {
				if writer != nil {
					_, _ = writer.Close()
				}
				t.Errorf("NewSegmentWriter accepted invalid channel ID %q", tt.channelID)
			}
			if tt.outsidePath != "" && tt.outsidePath != parent {
				if _, statErr := os.Stat(tt.outsidePath); !os.IsNotExist(statErr) {
					t.Errorf("outside path %q exists after rejecting channel ID %q", tt.outsidePath, tt.channelID)
				}
			}
		})
	}
}

func TestNewSegmentWriterCreatesUniqueRelativePaths(t *testing.T) {
	dir := t.TempDir()

	first, err := NewSegmentWriter(dir, "channel-1", 1024*1024)
	if err != nil {
		t.Fatalf("first NewSegmentWriter returned error: %v", err)
	}
	firstInfo, err := first.Close()
	if err != nil {
		t.Fatalf("first Close returned error: %v", err)
	}

	second, err := NewSegmentWriter(dir, "channel-1", 1024*1024)
	if err != nil {
		t.Fatalf("second NewSegmentWriter returned error: %v", err)
	}
	secondInfo, err := second.Close()
	if err != nil {
		t.Fatalf("second Close returned error: %v", err)
	}

	if firstInfo.RelativePath == secondInfo.RelativePath {
		t.Fatalf("RelativePath collision: both writers used %q", firstInfo.RelativePath)
	}
}

func TestSegmentWriterWritesAndExportsText(t *testing.T) {
	dir := t.TempDir()
	writer, err := NewSegmentWriter(dir, "channel-1", 1024*1024)
	if err != nil {
		t.Fatalf("NewSegmentWriter returned error: %v", err)
	}

	frame := protocol.LogFrame{
		ChannelID:   "channel-1",
		Seq:         1,
		TimestampNS: time.Unix(1700000000, 0).UnixNano(),
		Direction:   protocol.DirectionRX,
		Flags:       protocol.FlagRaw,
		Payload:     []byte{'H', 'i', 0xff, '\n'},
	}
	if err := writer.WriteFrame(frame); err != nil {
		t.Fatalf("WriteFrame returned error: %v", err)
	}
	segment, err := writer.Close()
	if err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	var out bytes.Buffer
	err = ExportText([]string{filepath.Join(dir, segment.RelativePath)}, ExportOptions{
		IncludeRX:        true,
		IncludeTX:        true,
		IncludeTimestamp: true,
		IncludeDirection: true,
		StripANSI:        false,
	}, &out)
	if err != nil {
		t.Fatalf("ExportText returned error: %v", err)
	}
	text := out.String()
	if !strings.Contains(text, "RX") {
		t.Fatalf("export text %q does not contain direction", text)
	}
	if !strings.Contains(text, `Hi\xff`) {
		t.Fatalf("export text %q does not escape invalid UTF-8", text)
	}
}

func TestExportTextFiltersDirections(t *testing.T) {
	dir := t.TempDir()
	segmentPath := writeTestSegment(t, dir,
		protocol.LogFrame{
			ChannelID:   "channel-1",
			Seq:         1,
			TimestampNS: time.Unix(1700000000, 0).UnixNano(),
			Direction:   protocol.DirectionRX,
			Flags:       protocol.FlagRaw,
			Payload:     []byte("rx payload\n"),
		},
		protocol.LogFrame{
			ChannelID:   "channel-1",
			Seq:         2,
			TimestampNS: time.Unix(1700000001, 0).UnixNano(),
			Direction:   protocol.DirectionTX,
			Flags:       protocol.FlagRaw,
			Payload:     []byte("tx payload\n"),
		},
	)

	var rxOnly bytes.Buffer
	if err := ExportText([]string{segmentPath}, ExportOptions{
		IncludeRX: true,
		IncludeTX: false,
	}, &rxOnly); err != nil {
		t.Fatalf("ExportText RX-only returned error: %v", err)
	}
	rxText := rxOnly.String()
	if !strings.Contains(rxText, "rx payload") {
		t.Fatalf("RX-only export %q does not contain RX payload", rxText)
	}
	if strings.Contains(rxText, "tx payload") {
		t.Fatalf("RX-only export %q contains TX payload", rxText)
	}

	var txOnly bytes.Buffer
	if err := ExportText([]string{segmentPath}, ExportOptions{
		IncludeRX: false,
		IncludeTX: true,
	}, &txOnly); err != nil {
		t.Fatalf("ExportText TX-only returned error: %v", err)
	}
	txText := txOnly.String()
	if !strings.Contains(txText, "tx payload") {
		t.Fatalf("TX-only export %q does not contain TX payload", txText)
	}
	if strings.Contains(txText, "rx payload") {
		t.Fatalf("TX-only export %q contains RX payload", txText)
	}
}

func TestExportTextFiltersFrameTimeRange(t *testing.T) {
	dir := t.TempDir()
	start := time.Unix(1700000000, 0).UTC()
	segmentPath := writeTestSegment(t, dir,
		protocol.LogFrame{
			ChannelID:   "channel-1",
			Seq:         1,
			TimestampNS: start.UnixNano(),
			Direction:   protocol.DirectionRX,
			Flags:       protocol.FlagRaw,
			Payload:     []byte("before\n"),
		},
		protocol.LogFrame{
			ChannelID:   "channel-1",
			Seq:         2,
			TimestampNS: start.Add(time.Second).UnixNano(),
			Direction:   protocol.DirectionRX,
			Flags:       protocol.FlagRaw,
			Payload:     []byte("inside\n"),
		},
		protocol.LogFrame{
			ChannelID:   "channel-1",
			Seq:         3,
			TimestampNS: start.Add(2 * time.Second).UnixNano(),
			Direction:   protocol.DirectionRX,
			Flags:       protocol.FlagRaw,
			Payload:     []byte("after\n"),
		},
	)

	var out bytes.Buffer
	if err := ExportText([]string{segmentPath}, ExportOptions{
		IncludeRX: true,
		IncludeTX: true,
		From:      start.Add(time.Second),
		To:        start.Add(time.Second),
	}, &out); err != nil {
		t.Fatalf("ExportText returned error: %v", err)
	}
	text := out.String()
	if !strings.Contains(text, "inside") {
		t.Fatalf("time-filtered export %q does not contain in-range payload", text)
	}
	if strings.Contains(text, "before") || strings.Contains(text, "after") {
		t.Fatalf("time-filtered export %q contains out-of-range payload", text)
	}
}

func TestExportRawFiltersFrameTimeRangeAndDirection(t *testing.T) {
	dir := t.TempDir()
	start := time.Unix(1700000000, 0).UTC()
	segmentPath := writeTestSegment(t, dir,
		protocol.LogFrame{
			ChannelID:   "channel-1",
			Seq:         1,
			TimestampNS: start.UnixNano(),
			Direction:   protocol.DirectionRX,
			Flags:       protocol.FlagRaw,
			Payload:     []byte("before\n"),
		},
		protocol.LogFrame{
			ChannelID:   "channel-1",
			Seq:         2,
			TimestampNS: start.Add(time.Second).UnixNano(),
			Direction:   protocol.DirectionTX,
			Flags:       protocol.FlagRaw,
			Payload:     []byte("inside tx\n"),
		},
		protocol.LogFrame{
			ChannelID:   "channel-1",
			Seq:         3,
			TimestampNS: start.Add(time.Second).UnixNano(),
			Direction:   protocol.DirectionRX,
			Flags:       protocol.FlagRaw,
			Payload:     []byte("inside rx\n"),
		},
	)

	var out bytes.Buffer
	if err := ExportRaw([]string{segmentPath}, ExportOptions{
		IncludeRX: false,
		IncludeTX: true,
		From:      start.Add(time.Second),
		To:        start.Add(time.Second),
	}, &out); err != nil {
		t.Fatalf("ExportRaw returned error: %v", err)
	}
	frames := decodeRawExport(t, out.Bytes())
	if len(frames) != 1 {
		t.Fatalf("exported %d frames, want 1", len(frames))
	}
	if frames[0].Seq != 2 || string(frames[0].Payload) != "inside tx\n" {
		t.Fatalf("exported frame = %+v, want only in-range TX frame", frames[0])
	}
}

func TestExportTextStripsANSI(t *testing.T) {
	dir := t.TempDir()
	segmentPath := writeTestSegment(t, dir, protocol.LogFrame{
		ChannelID:   "channel-1",
		Seq:         1,
		TimestampNS: time.Unix(1700000000, 0).UnixNano(),
		Direction:   protocol.DirectionRX,
		Flags:       protocol.FlagRaw,
		Payload:     []byte("\x1b[31mred text\x1b[0m\n"),
	})

	var out bytes.Buffer
	if err := ExportText([]string{segmentPath}, ExportOptions{
		IncludeRX: true,
		IncludeTX: true,
		StripANSI: true,
	}, &out); err != nil {
		t.Fatalf("ExportText returned error: %v", err)
	}
	text := out.String()
	if strings.Contains(text, "\x1b[31m") || strings.Contains(text, "\x1b[0m") {
		t.Fatalf("export text %q contains ANSI escape sequence", text)
	}
	if !strings.Contains(text, "red text") {
		t.Fatalf("export text %q does not retain text content", text)
	}
}

func writeTestSegment(t *testing.T, dir string, frames ...protocol.LogFrame) string {
	t.Helper()

	writer, err := NewSegmentWriter(dir, "channel-1", 1024*1024)
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
	return filepath.Join(dir, segment.RelativePath)
}

func decodeRawExport(t *testing.T, data []byte) []protocol.LogFrame {
	t.Helper()

	reader := bytes.NewReader(data)
	var frames []protocol.LogFrame
	for reader.Len() > 0 {
		var lenBuf [4]byte
		if _, err := io.ReadFull(reader, lenBuf[:]); err != nil {
			t.Fatalf("ReadFull length returned error: %v", err)
		}
		payload := make([]byte, binary.BigEndian.Uint32(lenBuf[:]))
		if _, err := io.ReadFull(reader, payload); err != nil {
			t.Fatalf("ReadFull payload returned error: %v", err)
		}
		frame, err := protocol.DecodeLogFrame(payload)
		if err != nil {
			t.Fatalf("DecodeLogFrame returned error: %v", err)
		}
		frames = append(frames, frame)
	}
	return frames
}
