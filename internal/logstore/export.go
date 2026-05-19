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
}

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

func ExportText(paths []string, opts ExportOptions, out io.Writer) error {
	writer := bufio.NewWriter(out)
	defer writer.Flush()

	for _, path := range paths {
		if err := exportTextFile(path, opts, writer); err != nil {
			return err
		}
	}
	return nil
}

func exportTextFile(path string, opts ExportOptions, out *bufio.Writer) error {
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
		if frame.Direction == protocol.DirectionRX && !opts.IncludeRX {
			continue
		}
		if frame.Direction == protocol.DirectionTX && !opts.IncludeTX {
			continue
		}

		if opts.IncludeTimestamp {
			fmt.Fprintf(out, "%s ", time.Unix(0, frame.TimestampNS).UTC().Format(time.RFC3339Nano))
		}
		if opts.IncludeDirection {
			if frame.Direction == protocol.DirectionRX {
				fmt.Fprint(out, "RX ")
			} else {
				fmt.Fprint(out, "TX ")
			}
		}

		text := escapedUTF8(frame.Payload)
		if opts.StripANSI {
			text = ansiRE.ReplaceAllString(text, "")
		}
		fmt.Fprint(out, text)
	}
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
