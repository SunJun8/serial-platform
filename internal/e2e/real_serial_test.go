package e2e

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	bugserial "go.bug.st/serial"
)

func TestRealSerialLoopback(t *testing.T) {
	dev := os.Getenv("REAL_SERIAL_DEV")
	if dev == "" {
		dev = "/dev/ttyUSB0"
	}
	required := isRealSerialRequired()

	if _, err := os.Stat(dev); err != nil {
		message := fmt.Sprintf("real serial: %s not found", dev)
		if isPermissionError(err) {
			message = fmt.Sprintf("real serial: permission denied for %s, add current user to dialout", dev)
		}
		failOrSkip(t, required, message)
	}

	port, err := bugserial.Open(dev, &bugserial.Mode{
		BaudRate: 115200,
		DataBits: 8,
		Parity:   bugserial.NoParity,
		StopBits: bugserial.OneStopBit,
	})
	if err != nil {
		message := fmt.Sprintf("real serial: open %s failed: %v", dev, err)
		if isPermissionError(err) {
			message = fmt.Sprintf("real serial: permission denied for %s, add current user to dialout", dev)
		}
		failOrSkip(t, required, message)
	}
	defer port.Close()
	if err := port.SetReadTimeout(100 * time.Millisecond); err != nil {
		failOrSkip(t, required, fmt.Sprintf("real serial: set read timeout for %s failed: %v", dev, err))
	}

	payload := []byte(fmt.Sprintf("serial-platform-loopback-%d\r\n", time.Now().UnixNano()))
	n, err := port.Write(payload)
	if err != nil {
		failOrSkip(t, required, fmt.Sprintf("real serial: write %s failed: %v", dev, err))
	}
	if n != len(payload) {
		failOrSkip(t, required, fmt.Sprintf("real serial: short write on %s: wrote %d of %d bytes", dev, n, len(payload)))
	}

	deadline := time.Now().Add(2 * time.Second)
	got := make([]byte, 0, len(payload))
	buf := make([]byte, 128)
	for time.Now().Before(deadline) && !bytes.Contains(got, payload) {
		n, err := port.Read(buf)
		if err != nil {
			failOrSkip(t, required, fmt.Sprintf("real serial: read %s failed: %v", dev, err))
		}
		got = append(got, buf[:n]...)
	}
	if !bytes.Contains(got, payload) {
		failOrSkip(t, required, fmt.Sprintf("real serial: loopback payload not observed within 2s on %s, got %q want %q", dev, got, payload))
	}
	t.Logf("real serial: passed %s", dev)
}

func isRealSerialRequired() bool {
	return os.Getenv("REAL_SERIAL_REQUIRED") == "1"
}

func failOrSkip(t *testing.T, required bool, message string) {
	t.Helper()
	if required {
		t.Fatal(message)
	}
	t.Skip(skipMessage(message))
}

func isPermissionError(err error) bool {
	if errors.Is(err, os.ErrPermission) {
		return true
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "permission") || strings.Contains(text, "denied")
}

func skipMessage(message string) string {
	return strings.Replace(message, "real serial:", "real serial: skipped,", 1)
}
