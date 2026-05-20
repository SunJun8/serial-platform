package agent_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"serial-platform/internal/agent"
	"serial-platform/internal/protocol"
	"serial-platform/internal/serial"
)

func TestLogUploaderConvertsSerialEventsToFrames(t *testing.T) {
	events := make(chan serial.Event, 2)
	frames := make(chan protocol.LogFrame, 2)
	uploader := agent.NewLogUploader(agent.LogUploaderConfig{Out: frames})

	txPayload := []byte("AT\r")
	events <- serial.Event{ChannelID: "channel-1", Direction: serial.DirectionTX, Timestamp: time.Unix(1, 0), Data: txPayload}
	events <- serial.Event{ChannelID: "channel-1", Direction: serial.DirectionRX, Timestamp: time.Unix(2, 0), Data: []byte("OK\r\n")}
	close(events)

	if err := uploader.Forward(context.Background(), events); err != nil {
		t.Fatalf("Forward returned error: %v", err)
	}

	first := <-frames
	second := <-frames
	if first.Seq != 1 {
		t.Fatalf("first.Seq = %d, want 1", first.Seq)
	}
	if first.Direction != protocol.DirectionTX {
		t.Fatalf("first.Direction = %v, want TX", first.Direction)
	}
	if first.TimestampNS != time.Unix(1, 0).UnixNano() {
		t.Fatalf("first.TimestampNS = %d, want %d", first.TimestampNS, time.Unix(1, 0).UnixNano())
	}
	if !bytes.Equal(first.Payload, []byte("AT\r")) {
		t.Fatalf("first.Payload = %q, want AT carriage return", first.Payload)
	}

	if second.Seq != 2 {
		t.Fatalf("second.Seq = %d, want 2", second.Seq)
	}
	if second.Direction != protocol.DirectionRX {
		t.Fatalf("second.Direction = %v, want RX", second.Direction)
	}
	if !bytes.Equal(second.Payload, []byte("OK\r\n")) {
		t.Fatalf("second.Payload = %q, want OK line", second.Payload)
	}

	txPayload[0] = 'B'
	if !bytes.Equal(first.Payload, []byte("AT\r")) {
		t.Fatalf("first.Payload changed after source mutation: %q", first.Payload)
	}
}

func TestLogUploaderReturnsContextErrorWhenCanceledBeforeSend(t *testing.T) {
	events := make(chan serial.Event, 1)
	frames := make(chan protocol.LogFrame)
	uploader := agent.NewLogUploader(agent.LogUploaderConfig{Out: frames})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	events <- serial.Event{ChannelID: "channel-1", Direction: serial.DirectionRX, Timestamp: time.Unix(1, 0), Data: []byte("boot\n")}

	err := uploader.Forward(ctx, events)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Forward error = %v, want context.Canceled", err)
	}
}
