package agent_test

import (
	"bytes"
	"testing"
	"time"

	"serial-platform/internal/agent"
	"serial-platform/internal/protocol"
	"serial-platform/internal/serial"
)

func TestSupervisorAddsChannelWorker(t *testing.T) {
	supervisor := agent.NewSupervisor()
	worker := serial.NewWorker("channel-1", serial.DefaultConfig(), serial.NewFakeBackend())

	if err := supervisor.AddChannel("channel-1", worker); err != nil {
		t.Fatalf("AddChannel returned error: %v", err)
	}

	control, ok := supervisor.Channel("channel-1")
	if !ok {
		t.Fatal("Channel did not find channel-1")
	}
	if control != worker {
		t.Fatal("Channel returned a different worker")
	}

	if err := supervisor.AddChannel("channel-1", worker); err == nil {
		t.Fatal("duplicate AddChannel returned nil error")
	}
}

func TestSerialEventToLogFrameMapsDirections(t *testing.T) {
	now := time.Unix(1700000000, 123).UTC()
	tests := []struct {
		name      string
		direction serial.Direction
		want      protocol.Direction
	}{
		{name: "rx", direction: serial.DirectionRX, want: protocol.DirectionRX},
		{name: "tx", direction: serial.DirectionTX, want: protocol.DirectionTX},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := serial.Event{
				ChannelID: "channel-1",
				Direction: tt.direction,
				Timestamp: now,
				Data:      []byte("boot\n"),
			}

			frame := agent.SerialEventToLogFrame(42, event)

			if frame.ChannelID != event.ChannelID {
				t.Fatalf("ChannelID = %q, want %q", frame.ChannelID, event.ChannelID)
			}
			if frame.Seq != 42 {
				t.Fatalf("Seq = %d, want 42", frame.Seq)
			}
			if frame.TimestampNS != now.UnixNano() {
				t.Fatalf("TimestampNS = %d, want %d", frame.TimestampNS, now.UnixNano())
			}
			if frame.Direction != tt.want {
				t.Fatalf("Direction = %v, want %v", frame.Direction, tt.want)
			}
			if frame.Flags != protocol.FlagRaw {
				t.Fatalf("Flags = %v, want %v", frame.Flags, protocol.FlagRaw)
			}
			if !bytes.Equal(frame.Payload, event.Data) {
				t.Fatalf("Payload = %q, want %q", frame.Payload, event.Data)
			}

			event.Data[0] = 'B'
			if string(frame.Payload) != "boot\n" {
				t.Fatalf("Payload changed after event mutation: %q", frame.Payload)
			}
		})
	}
}
