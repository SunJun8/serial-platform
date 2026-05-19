package serial

import (
	"context"
	"testing"
	"time"
)

func TestWorkerAllowsSingleControlSession(t *testing.T) {
	backend := NewFakeBackend()
	worker := NewWorker("channel-1", DefaultConfig(), backend)

	session, err := worker.OpenControlSession(context.Background(), "first")
	if err != nil {
		t.Fatalf("OpenControlSession first returned error: %v", err)
	}
	_, err = worker.OpenControlSession(context.Background(), "second")
	if err == nil {
		t.Fatal("OpenControlSession second returned nil error")
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	_, err = worker.OpenControlSession(context.Background(), "third")
	if err != nil {
		t.Fatalf("OpenControlSession third returned error: %v", err)
	}
}

func TestWorkerRestoresDefaultConfigOnClose(t *testing.T) {
	backend := NewFakeBackend()
	def := DefaultConfig()
	def.Baud = 115200
	worker := NewWorker("channel-1", def, backend)

	session, err := worker.OpenControlSession(context.Background(), "owner")
	if err != nil {
		t.Fatalf("OpenControlSession returned error: %v", err)
	}
	if err := session.SetConfig(Config{Baud: 2000000, DataBits: 8, Parity: "N", StopBits: 1}); err != nil {
		t.Fatalf("SetConfig returned error: %v", err)
	}
	if backend.Config().Baud != 2000000 {
		t.Fatalf("backend baud = %d", backend.Config().Baud)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if backend.Config().Baud != 115200 {
		t.Fatalf("backend baud after close = %d", backend.Config().Baud)
	}
}

func TestWorkerRecordsTXWrites(t *testing.T) {
	backend := NewFakeBackend()
	worker := NewWorker("channel-1", DefaultConfig(), backend)
	session, err := worker.OpenControlSession(context.Background(), "owner")
	if err != nil {
		t.Fatalf("OpenControlSession returned error: %v", err)
	}
	if err := session.Write([]byte("AT\r\n")); err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
	select {
	case event := <-worker.Events():
		if event.Direction != DirectionTX {
			t.Fatalf("Direction = %v", event.Direction)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for TX event")
	}
}

func TestClosedSessionCannotWrite(t *testing.T) {
	backend := NewFakeBackend()
	worker := NewWorker("channel-1", DefaultConfig(), backend)
	session, err := worker.OpenControlSession(context.Background(), "owner")
	if err != nil {
		t.Fatalf("OpenControlSession returned error: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if err := session.Write([]byte("AT\r\n")); err == nil {
		t.Fatal("Write on closed session returned nil error")
	}

	select {
	case event := <-worker.Events():
		t.Fatalf("unexpected TX event after closed session write: %+v", event)
	default:
	}
}

func TestOldSessionCannotWriteAfterNewSessionOpens(t *testing.T) {
	backend := NewFakeBackend()
	worker := NewWorker("channel-1", DefaultConfig(), backend)
	first, err := worker.OpenControlSession(context.Background(), "first")
	if err != nil {
		t.Fatalf("OpenControlSession first returned error: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close first returned error: %v", err)
	}
	second, err := worker.OpenControlSession(context.Background(), "second")
	if err != nil {
		t.Fatalf("OpenControlSession second returned error: %v", err)
	}

	if err := first.Write([]byte("stale\r\n")); err == nil {
		t.Fatal("Write on old session returned nil error")
	}
	if err := second.Write([]byte("AT\r\n")); err != nil {
		t.Fatalf("Write on current session returned error: %v", err)
	}

	select {
	case event := <-worker.Events():
		if event.Direction != DirectionTX {
			t.Fatalf("Direction = %v", event.Direction)
		}
		if string(event.Data) != "AT\r\n" {
			t.Fatalf("Data = %q", string(event.Data))
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for TX event")
	}
	select {
	case event := <-worker.Events():
		t.Fatalf("unexpected extra TX event: %+v", event)
	default:
	}
}

func TestWorkerEmitsRXFromBackend(t *testing.T) {
	backend := NewFakeBackend()
	worker := NewWorker("channel-1", DefaultConfig(), backend)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go worker.Run(ctx)

	backend.InjectRX([]byte("boot\n"))
	select {
	case event := <-worker.Events():
		if event.Direction != DirectionRX {
			t.Fatalf("Direction = %v", event.Direction)
		}
		if string(event.Data) != "boot\n" {
			t.Fatalf("Data = %q", string(event.Data))
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for RX event")
	}
}
