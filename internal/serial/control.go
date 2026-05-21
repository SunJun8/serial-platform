package serial

import (
	"context"
	"time"
)

type Direction int

const (
	DirectionRX Direction = 1
	DirectionTX Direction = 2
)

type Config struct {
	Baud     int
	DataBits int
	Parity   string
	StopBits int
	Flow     string
}

func DefaultConfig() Config {
	return Config{Baud: 115200, DataBits: 8, Parity: "N", StopBits: 1, Flow: "none"}
}

type Event struct {
	ChannelID string
	Direction Direction
	Timestamp time.Time
	LogGap    bool
	Data      []byte
}

type Backend interface {
	ApplyConfig(Config) error
	SetDTR(bool) error
	SetRTS(bool) error
	SendBreak(time.Duration) error
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	Close() error
}

type ControlSession interface {
	Write([]byte) error
	SetConfig(Config) error
	SetDTR(bool) error
	SetRTS(bool) error
	SendBreak(time.Duration) error
	Close() error
}

type SerialControl interface {
	OpenControlSession(context.Context, string) (ControlSession, error)
	Events() <-chan Event
}
