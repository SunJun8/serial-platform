package server

import (
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"nhooyr.io/websocket"

	"serial-platform/internal/logstore"
	"serial-platform/internal/protocol"
	"serial-platform/internal/storage"
)

const defaultLogSegmentMaxBytes = 64 * 1024 * 1024

func (srv *Server) handleLogWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	if srv.logDir == "" {
		_ = conn.Close(websocket.StatusInternalError, "log directory is not configured")
		return
	}

	writers := make(map[string]*logstore.SegmentWriter)
	defer srv.closeLogWriters(conn, writers)

	ctx := r.Context()
	for {
		messageType, payload, err := conn.Read(ctx)
		if err != nil {
			return
		}
		if messageType != websocket.MessageBinary {
			_ = conn.Close(websocket.StatusUnsupportedData, "log frames must be binary")
			return
		}

		frame, err := protocol.DecodeLogFrame(payload)
		if err != nil {
			_ = conn.Close(websocket.StatusInvalidFramePayloadData, "invalid log frame")
			return
		}
		if err := validateLogChannelID(frame.ChannelID); err != nil {
			_ = conn.Close(websocket.StatusInternalError, err.Error())
			return
		}
		if !srv.channelExists(frame.ChannelID) {
			srv.closeAndRemoveLogWriter(writers, frame.ChannelID)
			continue
		}

		writer, err := srv.logWriterForChannel(writers, frame.ChannelID)
		if err != nil {
			_ = conn.Close(websocket.StatusInternalError, err.Error())
			return
		}
		if err := writer.WriteFrame(frame); err != nil {
			_ = conn.Close(websocket.StatusInternalError, err.Error())
			return
		}
		for _, info := range writer.ClosedSegments() {
			if err := srv.upsertLogSegment(frame.ChannelID, info, storage.LogSegmentStatusClosed); err != nil {
				_ = conn.Close(websocket.StatusInternalError, err.Error())
				return
			}
		}
		if err := srv.upsertLogSegment(frame.ChannelID, writer.Info(), storage.LogSegmentStatusActive); err != nil {
			_ = conn.Close(websocket.StatusInternalError, err.Error())
			return
		}
		srv.liveLog.Publish(frame)
	}
}

func (srv *Server) logWriterForChannel(writers map[string]*logstore.SegmentWriter, channelID string) (*logstore.SegmentWriter, error) {
	if writer, ok := writers[channelID]; ok {
		return writer, nil
	}
	maxBytes := srv.logSegmentSize
	if maxBytes <= 0 {
		maxBytes = defaultLogSegmentMaxBytes
	}
	writer, err := logstore.NewSegmentWriter(srv.logDir, channelID, maxBytes)
	if err != nil {
		return nil, err
	}
	writers[channelID] = writer
	return writer, nil
}

func (srv *Server) closeLogWriters(conn *websocket.Conn, writers map[string]*logstore.SegmentWriter) {
	closeStatus := websocket.StatusNormalClosure
	var closeReason string

	for channelID, writer := range writers {
		info, err := writer.Close()
		if err != nil {
			closeStatus = websocket.StatusInternalError
			closeReason = err.Error()
			continue
		}
		if info.FrameCount == 0 {
			continue
		}
		if !srv.channelExists(channelID) {
			continue
		}
		if err := srv.upsertLogSegment(channelID, info, storage.LogSegmentStatusClosed); err != nil {
			closeStatus = websocket.StatusInternalError
			closeReason = err.Error()
		}
	}

	_ = conn.Close(closeStatus, closeReason)
}

func (srv *Server) closeAndRemoveLogWriter(writers map[string]*logstore.SegmentWriter, channelID string) {
	writer, ok := writers[channelID]
	if !ok {
		return
	}
	_, _ = writer.Close()
	delete(writers, channelID)
}

func validateLogChannelID(channelID string) error {
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

func (srv *Server) channelExists(channelID string) bool {
	if channelID == "" {
		return false
	}
	_, err := srv.db.GetChannel(channelID)
	return err == nil || !errors.Is(err, storage.ErrNotFound)
}

func (srv *Server) upsertLogSegment(channelID string, info logstore.SegmentInfo, status storage.LogSegmentStatus) error {
	_, err := srv.db.UpsertLogSegmentIfChannelExists(storage.LogSegment{
		ChannelID:  channelID,
		Path:       info.RelativePath,
		StartTime:  info.StartTime,
		EndTime:    info.EndTime,
		SizeBytes:  info.SizeBytes,
		FrameCount: info.FrameCount,
		Status:     status,
	})
	return err
}
