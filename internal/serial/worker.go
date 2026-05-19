package serial

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"
)

var ErrControlSessionBusy = errors.New("serial control session already open")

type Worker struct {
	channelID     string
	defaultConfig Config
	backend       Backend
	events        chan Event

	mu          sync.Mutex
	sessionOpen bool
	owner       string
}

func NewWorker(channelID string, defaultConfig Config, backend Backend) *Worker {
	return &Worker{
		channelID:     channelID,
		defaultConfig: defaultConfig,
		backend:       backend,
		events:        make(chan Event, 64),
	}
}

func (w *Worker) OpenControlSession(ctx context.Context, owner string) (ControlSession, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.sessionOpen {
		return nil, ErrControlSessionBusy
	}
	w.sessionOpen = true
	w.owner = owner
	return &workerSession{worker: w, owner: owner}, nil
}

func (w *Worker) Events() <-chan Event {
	return w.events
}

func (w *Worker) Run(ctx context.Context) {
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = w.backend.Close()
		case <-done:
		}
	}()

	buf := make([]byte, 4096)
	for {
		n, err := w.backend.Read(buf)
		if n > 0 {
			w.emit(DirectionRX, buf[:n])
		}
		if err != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		default:
		}
	}
}

func (w *Worker) emit(direction Direction, data []byte) {
	event := Event{
		ChannelID: w.channelID,
		Direction: direction,
		Timestamp: time.Now(),
		Data:      append([]byte(nil), data...),
	}
	w.events <- event
}

type workerSession struct {
	worker *Worker
	owner  string
	closed bool
}

func (s *workerSession) Write(data []byte) error {
	n, err := s.worker.backend.Write(data)
	if err != nil {
		return err
	}
	if n != len(data) {
		return io.ErrShortWrite
	}
	s.worker.emit(DirectionTX, data)
	return nil
}

func (s *workerSession) SetConfig(config Config) error {
	return s.worker.backend.ApplyConfig(config)
}

func (s *workerSession) SetDTR(value bool) error {
	return s.worker.backend.SetDTR(value)
}

func (s *workerSession) SetRTS(value bool) error {
	return s.worker.backend.SetRTS(value)
}

func (s *workerSession) SendBreak(duration time.Duration) error {
	return s.worker.backend.SendBreak(duration)
}

func (s *workerSession) Close() error {
	s.worker.mu.Lock()
	if s.closed {
		s.worker.mu.Unlock()
		return nil
	}
	s.closed = true
	s.worker.sessionOpen = false
	s.worker.owner = ""
	s.worker.mu.Unlock()
	return s.worker.backend.ApplyConfig(s.worker.defaultConfig)
}
