package server

import (
	"net/http"

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

		writer, err := srv.logWriterForChannel(writers, frame.ChannelID)
		if err != nil {
			_ = conn.Close(websocket.StatusInternalError, err.Error())
			return
		}
		if err := writer.WriteFrame(frame); err != nil {
			_ = conn.Close(websocket.StatusInternalError, err.Error())
			return
		}
	}
}

func (srv *Server) logWriterForChannel(writers map[string]*logstore.SegmentWriter, channelID string) (*logstore.SegmentWriter, error) {
	if writer, ok := writers[channelID]; ok {
		return writer, nil
	}
	writer, err := logstore.NewSegmentWriter(srv.logDir, channelID, defaultLogSegmentMaxBytes)
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
		err = srv.db.InsertLogSegment(storage.LogSegment{
			ChannelID:  channelID,
			Path:       info.RelativePath,
			StartTime:  info.StartTime,
			EndTime:    info.EndTime,
			SizeBytes:  info.SizeBytes,
			FrameCount: info.FrameCount,
			Status:     storage.LogSegmentStatusClosed,
		})
		if err != nil {
			closeStatus = websocket.StatusInternalError
			closeReason = err.Error()
		}
	}

	_ = conn.Close(closeStatus, closeReason)
}
