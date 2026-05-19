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

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

func ExportText(paths []string, opts ExportOptions, out io.Writer) error {
	writer := bufio.NewWriter(out)

	if err := exportFrames(paths, opts, func(_ []byte, frame protocol.LogFrame) error {
		return writeTextFrame(writer, frame, opts)
	}); err != nil {
		return err
	}
	return writer.Flush()
}

func ExportRaw(paths []string, opts ExportOptions, out io.Writer) error {
	return exportFrames(paths, opts, func(payload []byte, _ protocol.LogFrame) error {
		var lenBuf [4]byte
		binary.BigEndian.PutUint32(lenBuf[:], uint32(len(payload)))
		if _, err := out.Write(lenBuf[:]); err != nil {
			return err
		}
		_, err := out.Write(payload)
		return err
	})
}

func exportFrames(paths []string, opts ExportOptions, handle func([]byte, protocol.LogFrame) error) error {
	for _, path := range paths {
		if err := exportFramesFile(path, opts, handle); err != nil {
			return err
		}
	}
	return nil
}

func exportFramesFile(path string, opts ExportOptions, handle func([]byte, protocol.LogFrame) error) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	for {
		var lenBuf [4]byte
		_, err := io.ReadFull(file, lenBuf[:])
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		payload := make([]byte, binary.BigEndian.Uint32(lenBuf[:]))
		if _, err := io.ReadFull(file, payload); err != nil {
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
