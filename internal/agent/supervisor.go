package agent

import (
	"errors"
	"sync"

	"serial-platform/internal/serial"
)

type Supervisor struct {
	mu       sync.Mutex
	channels map[string]serial.SerialControl
}

func NewSupervisor() *Supervisor {
	return &Supervisor{channels: make(map[string]serial.SerialControl)}
}

func (s *Supervisor) AddChannel(channelID string, control serial.SerialControl) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.channels[channelID]; exists {
		return errors.New("agent supervisor channel already exists")
	}
	s.channels[channelID] = control
	return nil
}

func (s *Supervisor) Channel(channelID string) (serial.SerialControl, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	control, ok := s.channels[channelID]
	return control, ok
}
