package server

import (
	"bytes"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"serial-platform/internal/logstore"
	"serial-platform/internal/storage"
)

func (srv *Server) handleLogDownload(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	channelID := query.Get("channel_id")
	if channelID == "" {
		writeBadRequest(w, "channel_id is required")
		return
	}

	from, err := parseLogDownloadTime(query.Get("from"), time.Time{})
	if err != nil {
		writeBadRequest(w, err.Error())
		return
	}
	to, err := parseLogDownloadTime(query.Get("to"), time.Now().UTC())
	if err != nil {
		writeBadRequest(w, err.Error())
		return
	}
	if !from.IsZero() && to.Before(from) {
		writeBadRequest(w, "to must be at or after from")
		return
	}

	format := query.Get("format")
	if format == "" {
		format = "text"
	}
	if format != "text" && format != "raw" {
		writeBadRequest(w, "format must be text or raw")
		return
	}

	direction := query.Get("direction")
	includeRX, includeTX, err := parseLogDownloadDirection(direction)
	if err != nil {
		writeBadRequest(w, err.Error())
		return
	}

	includeTimestamp, err := parseLogDownloadBool(query.Get("timestamp"), "timestamp")
	if err != nil {
		writeBadRequest(w, err.Error())
		return
	}
	includeDirection, err := parseLogDownloadBool(query.Get("direction_label"), "direction_label")
	if err != nil {
		writeBadRequest(w, err.Error())
		return
	}
	stripANSI, err := parseLogDownloadBool(query.Get("strip_ansi"), "strip_ansi")
	if err != nil {
		writeBadRequest(w, err.Error())
		return
	}

	segments, err := srv.db.ListLogSegments(channelID, from, to)
	if err != nil {
		writeError(w, err)
		return
	}
	sources, err := srv.logSegmentSources(segments)
	if err != nil {
		writeError(w, err)
		return
	}

	if format == "raw" {
		var out bytes.Buffer
		if err := logstore.ExportRawSegments(sources, logstore.ExportOptions{
			IncludeRX: includeRX,
			IncludeTX: includeTX,
			From:      from,
			To:        to,
		}, &out); err != nil {
			writeError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(out.Bytes())
		return
	}

	var out bytes.Buffer
	if err := logstore.ExportTextSegments(sources, logstore.ExportOptions{
		IncludeRX:        includeRX,
		IncludeTX:        includeTX,
		IncludeTimestamp: includeTimestamp,
		IncludeDirection: includeDirection,
		StripANSI:        stripANSI,
		From:             from,
		To:               to,
	}, &out); err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out.Bytes())
}

func parseLogDownloadTime(value string, fallback time.Time) (time.Time, error) {
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid time %q", value)
	}
	return parsed, nil
}

func parseLogDownloadDirection(value string) (bool, bool, error) {
	switch value {
	case "", "both":
		return true, true, nil
	case "rx":
		return true, false, nil
	case "tx":
		return false, true, nil
	default:
		return false, false, fmt.Errorf("direction must be rx, tx, or both")
	}
}

func parseLogDownloadBool(value string, name string) (bool, error) {
	if value == "" {
		return false, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s must be a bool", name)
	}
	return parsed, nil
}

func (srv *Server) logSegmentSources(segments []storage.LogSegment) ([]logstore.SegmentSource, error) {
	sources := make([]logstore.SegmentSource, 0, len(segments))
	for _, segment := range segments {
		path, err := srv.logSegmentPath(segment.Path)
		if err != nil {
			return nil, err
		}
		sources = append(sources, logstore.SegmentSource{
			Path:     path,
			MaxBytes: segment.SizeBytes,
		})
	}
	return sources, nil
}

func (srv *Server) logSegmentPath(segmentPath string) (string, error) {
	if srv.logDir == "" {
		return "", fmt.Errorf("log directory is not configured")
	}
	if filepath.IsAbs(segmentPath) {
		return "", fmt.Errorf("invalid log segment path %q", segmentPath)
	}

	root, err := filepath.Abs(srv.logDir)
	if err != nil {
		return "", err
	}
	full := filepath.Join(root, filepath.Clean(segmentPath))
	rel, err := filepath.Rel(root, full)
	if err != nil {
		return "", err
	}
	if rel == "." || rel == ".." || len(rel) >= 3 && rel[:3] == ".."+string(filepath.Separator) {
		return "", fmt.Errorf("invalid log segment path %q", segmentPath)
	}
	return full, nil
}

func writeBadRequest(w http.ResponseWriter, message string) {
	writeJSON(w, http.StatusBadRequest, map[string]string{
		"error": message,
	})
}
