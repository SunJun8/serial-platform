package protocol

import (
	"bytes"
	"math"
	"testing"
	"time"
)

func TestLogFrameRoundTrip(t *testing.T) {
	frame := LogFrame{
		ChannelID:   "channel-1",
		Seq:         42,
		TimestampNS: time.Unix(1700000000, 123).UnixNano(),
		Direction:   DirectionTX,
		Flags:       FlagRaw,
		Payload:     []byte{0x41, 0xff, 0x00},
	}

	encoded, err := EncodeLogFrame(frame)
	if err != nil {
		t.Fatalf("EncodeLogFrame returned error: %v", err)
	}

	decoded, err := DecodeLogFrame(encoded)
	if err != nil {
		t.Fatalf("DecodeLogFrame returned error: %v", err)
	}

	if decoded.ChannelID != frame.ChannelID {
		t.Fatalf("ChannelID = %q, want %q", decoded.ChannelID, frame.ChannelID)
	}
	if decoded.Seq != frame.Seq {
		t.Fatalf("Seq = %d, want %d", decoded.Seq, frame.Seq)
	}
	if decoded.TimestampNS != frame.TimestampNS {
		t.Fatalf("TimestampNS = %d, want %d", decoded.TimestampNS, frame.TimestampNS)
	}
	if decoded.Direction != frame.Direction {
		t.Fatalf("Direction = %d, want %d", decoded.Direction, frame.Direction)
	}
	if decoded.Flags != frame.Flags {
		t.Fatalf("Flags = %d, want %d", decoded.Flags, frame.Flags)
	}
	if !bytes.Equal(decoded.Payload, frame.Payload) {
		t.Fatalf("Payload = %x, want %x", decoded.Payload, frame.Payload)
	}
}

func TestDecodeLogFrameRejectsShortFrame(t *testing.T) {
	_, err := DecodeLogFrame([]byte("bad"))
	if err == nil {
		t.Fatal("DecodeLogFrame returned nil error for short frame")
	}
}

func TestDecodeLogFrameRejectsBadMagic(t *testing.T) {
	frame := validEncodedLogFrame(t)
	copy(frame[0:4], []byte("BAD!"))

	_, err := DecodeLogFrame(frame)
	if err == nil {
		t.Fatal("DecodeLogFrame returned nil error for bad magic")
	}
}

func TestDecodeLogFrameRejectsHeaderLengthMismatch(t *testing.T) {
	frame := validEncodedLogFrame(t)
	frame[5] = 31

	_, err := DecodeLogFrame(frame)
	if err == nil {
		t.Fatal("DecodeLogFrame returned nil error for header length mismatch")
	}
}

func TestDecodeLogFrameRejectsInvalidDirection(t *testing.T) {
	frame := validEncodedLogFrame(t)
	frame[24] = 3

	_, err := DecodeLogFrame(frame)
	if err == nil {
		t.Fatal("DecodeLogFrame returned nil error for invalid direction")
	}
}

func TestDecodeLogFrameRejectsLengthMismatch(t *testing.T) {
	frame := validEncodedLogFrame(t)
	frame = frame[:len(frame)-1]

	_, err := DecodeLogFrame(frame)
	if err == nil {
		t.Fatal("DecodeLogFrame returned nil error for length mismatch")
	}
}

func TestEncodeLogFrameRejectsInvalidDirection(t *testing.T) {
	_, err := EncodeLogFrame(LogFrame{
		ChannelID: "channel-1",
		Direction: Direction(3),
	})
	if err == nil {
		t.Fatal("EncodeLogFrame returned nil error for invalid direction")
	}
}

func TestEncodeLogFrameRejectsOversizedPayloadLength(t *testing.T) {
	_, err := encodeLogFrameWithPayloadLen(LogFrame{
		ChannelID: "channel-1",
		Direction: DirectionRX,
	}, math.MaxUint32+1)
	if err == nil {
		t.Fatal("encodeLogFrameWithPayloadLen returned nil error for oversized payload length")
	}
}

func validEncodedLogFrame(t *testing.T) []byte {
	t.Helper()

	encoded, err := EncodeLogFrame(LogFrame{
		ChannelID:   "channel-1",
		Seq:         42,
		TimestampNS: time.Unix(1700000000, 123).UnixNano(),
		Direction:   DirectionRX,
		Flags:       FlagRaw | FlagLogGap,
		Payload:     []byte("boot\n"),
	})
	if err != nil {
		t.Fatalf("EncodeLogFrame returned error: %v", err)
	}
	return encoded
}
