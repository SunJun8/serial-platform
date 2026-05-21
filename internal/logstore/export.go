package logstore

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"regexp"
	"time"
	"unicode/utf8"

	"serial-platform/internal/protocol"
)

type ExportOptions struct {
	IncludeRX        bool
	IncludeTX        bool
	IncludeTimestamp bool
	IncludeDirection bool
	StripANSI        bool
	From             time.Time
	To               time.Time
}

type SegmentSource struct {
	Path     string
	MaxBytes int64
}

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

func ExportText(paths []string, opts ExportOptions, out io.Writer) error {
	return ExportTextSegments(segmentSourcesFromPaths(paths), opts, out)
}

func ExportTextSegments(sources []SegmentSource, opts ExportOptions, out io.Writer) error {
	writer := bufio.NewWriter(out)

	if err := exportFrames(sources, opts, func(_ []byte, frame protocol.LogFrame) error {
		return writeTextFrame(writer, frame, opts)
	}); err != nil {
		return err
	}
	return writer.Flush()
}

func ExportRaw(paths []string, opts ExportOptions, out io.Writer) error {
	return ExportRawSegments(segmentSourcesFromPaths(paths), opts, out)
}

func ExportRawSegments(sources []SegmentSource, opts ExportOptions, out io.Writer) error {
	return exportFrames(sources, opts, func(payload []byte, _ protocol.LogFrame) error {
		var lenBuf [4]byte
		binary.BigEndian.PutUint32(lenBuf[:], uint32(len(payload)))
		if _, err := out.Write(lenBuf[:]); err != nil {
			return err
		}
		_, err := out.Write(payload)
		return err
	})
}

func segmentSourcesFromPaths(paths []string) []SegmentSource {
	sources := make([]SegmentSource, 0, len(paths))
	for _, path := range paths {
		sources = append(sources, SegmentSource{Path: path, MaxBytes: -1})
	}
	return sources
}

func exportFrames(sources []SegmentSource, opts ExportOptions, handle func([]byte, protocol.LogFrame) error) error {
	for _, source := range sources {
		if err := exportFramesFile(source, opts, handle); err != nil {
			return err
		}
	}
	return nil
}

func exportFramesFile(source SegmentSource, opts ExportOptions, handle func([]byte, protocol.LogFrame) error) error {
	if source.MaxBytes == 0 {
		return nil
	}

	file, err := os.Open(source.Path)
	if err != nil {
		return err
	}
	defer file.Close()

	var reader io.Reader = file
	if source.MaxBytes > 0 {
		reader = io.LimitReader(file, source.MaxBytes)
	}
	for {
		var lenBuf [4]byte
		_, err := io.ReadFull(reader, lenBuf[:])
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		payload := make([]byte, binary.BigEndian.Uint32(lenBuf[:]))
		if _, err := io.ReadFull(reader, payload); err != nil {
			return err
		}
		frame, err := protocol.DecodeLogFrame(payload)
		if err != nil {
			return err
		}
		if !includeFrame(frame, opts) {
			continue
		}
		if err := handle(payload, frame); err != nil {
			return err
		}
	}
}

func includeFrame(frame protocol.LogFrame, opts ExportOptions) bool {
	if frame.Direction == protocol.DirectionRX && !opts.IncludeRX {
		return false
	}
	if frame.Direction == protocol.DirectionTX && !opts.IncludeTX {
		return false
	}

	ts := time.Unix(0, frame.TimestampNS).UTC()
	if !opts.From.IsZero() && ts.Before(opts.From) {
		return false
	}
	if !opts.To.IsZero() && ts.After(opts.To) {
		return false
	}
	return true
}

func writeTextFrame(out *bufio.Writer, frame protocol.LogFrame, opts ExportOptions) error {
	if opts.IncludeTimestamp {
		if _, err := fmt.Fprintf(out, "%s ", time.Unix(0, frame.TimestampNS).UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	if opts.IncludeDirection {
		label := "TX"
		if frame.Direction == protocol.DirectionRX {
			label = "RX"
		}
		if _, err := fmt.Fprintf(out, "%s ", label); err != nil {
			return err
		}
	}

	text := escapedUTF8(frame.Payload)
	if opts.StripANSI {
		text = ansiRE.ReplaceAllString(text, "")
	}
	_, err := fmt.Fprint(out, text)
	return err
}

func escapedUTF8(data []byte) string {
	out := make([]byte, 0, len(data))
	for len(data) > 0 {
		r, size := utf8.DecodeRune(data)
		if r == utf8.RuneError && size == 1 {
			out = append(out, fmt.Sprintf(`\x%02x`, data[0])...)
			data = data[1:]
			continue
		}
		out = append(out, data[:size]...)
		data = data[size:]
	}
	return string(out)
}
