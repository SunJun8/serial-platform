package logstore

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"serial-platform/internal/protocol"
)

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
