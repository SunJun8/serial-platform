package logstore

import (
	"encoding/binary"
	"errors"
	"math"
	"os"
	"path/filepath"
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
}

func NewSegmentWriter(root, channelID string, maxBytes int64) (*SegmentWriter, error) {
	now := time.Now().UTC()
	rel := filepath.Join(channelID, now.Format("2006"), now.Format("01"), now.Format("02"), now.Format("15"), "segment-000001.rlog")
	abs := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(abs, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &SegmentWriter{
		root:      root,
		channelID: channelID,
		maxBytes:  maxBytes,
		file:      file,
		relPath:   rel,
	}, nil
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

	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(encoded)))
	if _, err := w.file.Write(lenBuf[:]); err != nil {
		return err
	}
	if _, err := w.file.Write(encoded); err != nil {
		return err
	}

	w.sizeBytes += int64(4 + len(encoded))
	w.frameCount++
	ts := time.Unix(0, frame.TimestampNS).UTC()
	if w.startTime.IsZero() {
		w.startTime = ts
	}
	w.endTime = ts
	return nil
}

func (w *SegmentWriter) Close() (SegmentInfo, error) {
	info := SegmentInfo{
		RelativePath: w.relPath,
		SizeBytes:    w.sizeBytes,
		FrameCount:   w.frameCount,
		StartTime:    w.startTime,
		EndTime:      w.endTime,
	}
	if w.file == nil {
		return info, nil
	}
	err := w.file.Close()
	w.file = nil
	return info, err
}
