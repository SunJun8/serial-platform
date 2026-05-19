package serial

import (
	"fmt"
	"strings"
	"sync"
	"time"

	bugserial "go.bug.st/serial"
)

type RealBackend struct {
	mu   sync.Mutex
	port bugserial.Port
}

func NewRealBackend(path string, config Config) (*RealBackend, error) {
	mode, err := toSerialMode(config)
	if err != nil {
		return nil, err
	}
	port, err := bugserial.Open(path, mode)
	if err != nil {
		return nil, err
	}
	return &RealBackend{port: port}, nil
}

func (b *RealBackend) ApplyConfig(config Config) error {
	mode, err := toSerialMode(config)
	if err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.port.SetMode(mode)
}

func (b *RealBackend) SetDTR(value bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.port.SetDTR(value)
}

func (b *RealBackend) SetRTS(value bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.port.SetRTS(value)
}

func (b *RealBackend) SendBreak(duration time.Duration) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.port.Break(duration)
}

func (b *RealBackend) Read(buf []byte) (int, error) {
	return b.port.Read(buf)
}

func (b *RealBackend) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.port.Write(data)
}

func (b *RealBackend) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.port.Close()
}

func toSerialMode(config Config) (*bugserial.Mode, error) {
	parity, err := toSerialParity(config.Parity)
	if err != nil {
		return nil, err
	}
	stopBits, err := toSerialStopBits(config.StopBits)
	if err != nil {
		return nil, err
	}
	if flow := strings.ToLower(config.Flow); flow != "" && flow != "none" {
		return nil, fmt.Errorf("unsupported serial flow control %q", config.Flow)
	}
	return &bugserial.Mode{
		BaudRate: config.Baud,
		DataBits: config.DataBits,
		Parity:   parity,
		StopBits: stopBits,
	}, nil
}

func toSerialParity(value string) (bugserial.Parity, error) {
	switch strings.ToUpper(value) {
	case "", "N", "NONE":
		return bugserial.NoParity, nil
	case "O", "ODD":
		return bugserial.OddParity, nil
	case "E", "EVEN":
		return bugserial.EvenParity, nil
	case "M", "MARK":
		return bugserial.MarkParity, nil
	case "S", "SPACE":
		return bugserial.SpaceParity, nil
	default:
		return bugserial.NoParity, fmt.Errorf("unsupported serial parity %q", value)
	}
}

func toSerialStopBits(value int) (bugserial.StopBits, error) {
	switch value {
	case 1:
		return bugserial.OneStopBit, nil
	case 2:
		return bugserial.TwoStopBits, nil
	default:
		return bugserial.OneStopBit, fmt.Errorf("unsupported serial stop bits %d", value)
	}
}
