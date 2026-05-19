package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

const logFrameHeaderLen = 32

var logFrameMagic = [4]byte{'S', 'P', 'L', '1'}

type Direction uint8

const (
	DirectionRX Direction = 1
	DirectionTX Direction = 2
)

type LogFlags uint16

const (
	FlagRaw    LogFlags = 1 << 0
	FlagDrop   LogFlags = 1 << 1
	FlagLogGap LogFlags = 1 << 2
)

type LogFrame struct {
	ChannelID   string
	Seq         uint64
	TimestampNS int64
	Direction   Direction
	Flags       LogFlags
	Payload     []byte
}

func EncodeLogFrame(frame LogFrame) ([]byte, error) {
	return encodeLogFrameWithPayloadLen(frame, uint64(len(frame.Payload)))
}

func encodeLogFrameWithPayloadLen(frame LogFrame, payloadLen uint64) ([]byte, error) {
	if frame.ChannelID == "" {
		return nil, errors.New("channel id is required")
	}
	if frame.Direction != DirectionRX && frame.Direction != DirectionTX {
		return nil, fmt.Errorf("invalid direction %d", frame.Direction)
	}
	if payloadLen > math.MaxUint32 {
		return nil, errors.New("payload is too long")
	}
	channel := []byte(frame.ChannelID)
	if len(channel) > 65535 {
		return nil, errors.New("channel id is too long")
	}

	out := make([]byte, logFrameHeaderLen+len(channel)+len(frame.Payload))
	copy(out[0:4], logFrameMagic[:])
	binary.BigEndian.PutUint16(out[4:6], logFrameHeaderLen)
	binary.BigEndian.PutUint16(out[6:8], uint16(len(channel)))
	binary.BigEndian.PutUint64(out[8:16], frame.Seq)
	binary.BigEndian.PutUint64(out[16:24], uint64(frame.TimestampNS))
	out[24] = byte(frame.Direction)
	out[25] = 0
	binary.BigEndian.PutUint16(out[26:28], uint16(frame.Flags))
	binary.BigEndian.PutUint32(out[28:32], uint32(payloadLen))
	copy(out[32:32+len(channel)], channel)
	copy(out[32+len(channel):], frame.Payload)
	return out, nil
}

func DecodeLogFrame(in []byte) (LogFrame, error) {
	if len(in) < logFrameHeaderLen {
		return LogFrame{}, errors.New("log frame is too short")
	}
	if string(in[0:4]) != string(logFrameMagic[:]) {
		return LogFrame{}, errors.New("invalid log frame magic")
	}
	headerLen := int(binary.BigEndian.Uint16(in[4:6]))
	if headerLen != logFrameHeaderLen {
		return LogFrame{}, fmt.Errorf("unsupported header length %d", headerLen)
	}

	channelLen := int(binary.BigEndian.Uint16(in[6:8]))
	payloadLen := int(binary.BigEndian.Uint32(in[28:32]))
	if len(in) != headerLen+channelLen+payloadLen {
		return LogFrame{}, errors.New("log frame length mismatch")
	}

	direction := Direction(in[24])
	if direction != DirectionRX && direction != DirectionTX {
		return LogFrame{}, fmt.Errorf("invalid direction %d", direction)
	}

	payloadStart := logFrameHeaderLen + channelLen
	return LogFrame{
		ChannelID:   string(in[logFrameHeaderLen:payloadStart]),
		Seq:         binary.BigEndian.Uint64(in[8:16]),
		TimestampNS: int64(binary.BigEndian.Uint64(in[16:24])),
		Direction:   direction,
		Flags:       LogFlags(binary.BigEndian.Uint16(in[26:28])),
		Payload:     append([]byte(nil), in[payloadStart:]...),
	}, nil
}
