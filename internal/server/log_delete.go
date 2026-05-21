package server

import (
	"errors"
	"os"

	"serial-platform/internal/storage"
)

func (srv *Server) deleteChannelLogFiles(segments []storage.LogSegment) error {
	paths := make([]string, 0, len(segments))
	for _, segment := range segments {
		path, err := srv.logSegmentPath(segment.Path)
		if err != nil {
			return err
		}
		paths = append(paths, path)
	}
	for _, path := range paths {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}
