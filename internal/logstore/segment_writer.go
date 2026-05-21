package logstore

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"serial-platform/internal/protocol"
)

type SegmentInfo struct {
	RelativePath string
	SizeBytes    int64
	FrameCount   int64
	StartTime    time.Time
	EndTime      time.Time
}

type SegmentWriter struct {
	root      string
	channelID string
	maxBytes  int64

	file       *os.File
	relPath    string
	sizeBytes  int64
	frameCount int64
	startTime  time.Time
	endTime    time.Time
	closed     []SegmentInfo
}

func NewSegmentWriter(root, channelID string, maxBytes int64) (*SegmentWriter, error) {
	if err := validateChannelID(channelID); err != nil {
		return nil, err
	}

	writer := &SegmentWriter{
		root:      root,
		channelID: channelID,
		maxBytes:  maxBytes,
	}
	if err := writer.openNewSegment(time.Now().UTC()); err != nil {
		return nil, err
	}
	return writer, nil
}

func (w *SegmentWriter) openNewSegment(now time.Time) error {
	dirRel := filepath.Join(w.channelID, now.Format("2006"), now.Format("01"), now.Format("02"), now.Format("15"))
	dirAbs := filepath.Join(w.root, dirRel)
	if err := os.MkdirAll(dirAbs, 0o755); err != nil {
		return err
	}
	rel, file, err := createSegmentFile(dirRel, dirAbs, now)
	if err != nil {
		return err
	}
	w.file = file
	w.relPath = rel
	w.sizeBytes = 0
	w.frameCount = 0
	w.startTime = time.Time{}
	w.endTime = time.Time{}
	return nil
}

func validateChannelID(channelID string) error {
	if channelID == "" {
		return errors.New("channel id is required")
	}
	if filepath.IsAbs(channelID) || strings.Contains(channelID, "..") {
		return fmt.Errorf("invalid channel id %q", channelID)
	}
	for _, ch := range channelID {
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '.' || ch == '_' || ch == '-' {
			continue
		}
		return fmt.Errorf("invalid channel id %q", channelID)
	}
	return nil
}

func createSegmentFile(dirRel, dirAbs string, now time.Time) (string, *os.File, error) {
	for attempt := 0; attempt < 1000; attempt++ {
		name := fmt.Sprintf("segment-%d-%03d.rlog", now.UnixNano(), attempt)
		rel := filepath.Join(dirRel, name)
		abs := filepath.Join(dirAbs, name)
		file, err := os.OpenFile(abs, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			return rel, file, nil
		}
		if errors.Is(err, os.ErrExist) {
			continue
		}
		return "", nil, err
	}
	return "", nil, errors.New("could not create unique segment file")
}

func (w *SegmentWriter) WriteFrame(frame protocol.LogFrame) error {
	if w.file == nil {
		return errors.New("segment writer is closed")
	}

	encoded, err := protocol.EncodeLogFrame(frame)
	if err != nil {
		return err
	}
	if len(encoded) > math.MaxUint32 {
		return errors.New("encoded log frame is too long")
	}
	frameBytes := int64(4 + len(encoded))
	if w.maxBytes > 0 && w.frameCount > 0 && w.sizeBytes+frameBytes > w.maxBytes {
		if err := w.rotate(time.Unix(0, frame.TimestampNS).UTC()); err != nil {
			return err
		}
	}

	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(encoded)))
	if _, err := w.file.Write(lenBuf[:]); err != nil {
		return err
	}
	if _, err := w.file.Write(encoded); err != nil {
		return err
	}

	w.sizeBytes += frameBytes
	w.frameCount++
	ts := time.Unix(0, frame.TimestampNS).UTC()
	if w.startTime.IsZero() {
		w.startTime = ts
	}
	w.endTime = ts
	return nil
}

func (w *SegmentWriter) rotate(now time.Time) error {
	info := w.Info()
	if err := w.file.Close(); err != nil {
		w.file = nil
		return err
	}
	w.file = nil
	if info.FrameCount > 0 {
		w.closed = append(w.closed, info)
	}
	return w.openNewSegment(now)
}

func (w *SegmentWriter) ClosedSegments() []SegmentInfo {
	closed := append([]SegmentInfo(nil), w.closed...)
	w.closed = nil
	return closed
}

func (w *SegmentWriter) Info() SegmentInfo {
	return SegmentInfo{
		RelativePath: w.relPath,
		SizeBytes:    w.sizeBytes,
		FrameCount:   w.frameCount,
		StartTime:    w.startTime,
		EndTime:      w.endTime,
	}
}

func (w *SegmentWriter) Close() (SegmentInfo, error) {
	info := w.Info()
	if w.file == nil {
		return info, nil
	}
	err := w.file.Close()
	w.file = nil
	return info, err
}
