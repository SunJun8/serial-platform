package serial

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"
)

var ErrControlSessionBusy = errors.New("serial control session already open")
var ErrControlSessionClosed = errors.New("serial control session is closed")
var ErrControlSessionStale = errors.New("serial control session is not current")

type Worker struct {
	channelID     string
	defaultConfig Config
	backend       Backend
	events        chan Event

	mu          sync.Mutex
	sessionOpen bool
	owner       string
	sessionID   uint64
	nextID      uint64
	logGap      bool
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
	w.nextID++
	w.sessionOpen = true
	w.owner = owner
	w.sessionID = w.nextID
	return &workerSession{worker: w, owner: owner, id: w.sessionID}, nil
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
			w.tryEmit(DirectionRX, buf[:n])
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

func (w *Worker) tryEmit(direction Direction, data []byte) {
	logGap := w.consumeLogGap()
	event := Event{
		ChannelID: w.channelID,
		Direction: direction,
		Timestamp: time.Now(),
		LogGap:    logGap,
		Data:      append([]byte(nil), data...),
	}
	select {
	case w.events <- event:
	default:
		w.markLogGap()
	}
}

func (w *Worker) markLogGap() {
	w.mu.Lock()
	w.logGap = true
	w.mu.Unlock()
}

func (w *Worker) consumeLogGap() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	logGap := w.logGap
	w.logGap = false
	return logGap
}

type workerSession struct {
	worker *Worker
	owner  string
	id     uint64
	closed bool
}

func (s *workerSession) Write(data []byte) error {
	if err := s.checkValid(); err != nil {
		return err
	}
	n, err := s.worker.backend.Write(data)
	if err != nil {
		return err
	}
	if n != len(data) {
		return io.ErrShortWrite
	}
	s.worker.tryEmit(DirectionTX, data)
	return nil
}

func (s *workerSession) SetConfig(config Config) error {
	if err := s.checkValid(); err != nil {
		return err
	}
	return s.worker.backend.ApplyConfig(config)
}

func (s *workerSession) SetDTR(value bool) error {
	if err := s.checkValid(); err != nil {
		return err
	}
	return s.worker.backend.SetDTR(value)
}

func (s *workerSession) SetRTS(value bool) error {
	if err := s.checkValid(); err != nil {
		return err
	}
	return s.worker.backend.SetRTS(value)
}

func (s *workerSession) SendBreak(duration time.Duration) error {
	if err := s.checkValid(); err != nil {
		return err
	}
	return s.worker.backend.SendBreak(duration)
}

func (s *workerSession) Close() error {
	s.worker.mu.Lock()
	if s.closed {
		s.worker.mu.Unlock()
		return nil
	}
	s.closed = true
	if s.worker.sessionOpen && s.worker.owner == s.owner && s.worker.sessionID == s.id {
		s.worker.sessionOpen = false
		s.worker.owner = ""
		s.worker.sessionID = 0
	}
	s.worker.mu.Unlock()
	return s.worker.backend.ApplyConfig(s.worker.defaultConfig)
}

func (s *workerSession) checkValid() error {
	s.worker.mu.Lock()
	defer s.worker.mu.Unlock()
	if s.closed {
		return ErrControlSessionClosed
	}
	if !s.worker.sessionOpen || s.worker.owner != s.owner || s.worker.sessionID != s.id {
		return ErrControlSessionStale
	}
	return nil
}
