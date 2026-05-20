package main

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

func TestCentralServerExitsOnInterrupt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Interrupt delivery to subprocesses is not supported on windows")
	}

	if os.Getenv("SERIAL_PLATFORM_CENTRAL_HELPER") == "1" {
		if err := run([]string{
			"--listen", "127.0.0.1:0",
			"--data-dir", os.Getenv("SERIAL_PLATFORM_CENTRAL_DATA_DIR"),
			"--rfc2217-bind", "127.0.0.1",
		}); err != nil {
			os.Exit(1)
		}
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestCentralServerExitsOnInterrupt")
	cmd.Env = append(os.Environ(),
		"SERIAL_PLATFORM_CENTRAL_HELPER=1",
		"SERIAL_PLATFORM_CENTRAL_DATA_DIR="+t.TempDir(),
	)
	cmd.Dir = t.TempDir()
	if err := cmd.Start(); err != nil {
		t.Fatalf("cmd.Start returned error: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("Signal returned error: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("central-server did not exit cleanly: %v", err)
	}
}
