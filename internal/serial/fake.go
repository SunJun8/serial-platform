package serial

import (
	"io"
	"sync"
	"time"
)

type FakeBackend struct {
	mu     sync.Mutex
	config Config
	writes [][]byte
	dtr    bool
	rts    bool
	rx     chan []byte
	closed bool
}

func NewFakeBackend() *FakeBackend {
	return &FakeBackend{config: DefaultConfig(), rx: make(chan []byte, 16)}
}

func (b *FakeBackend) ApplyConfig(config Config) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.config = config
	return nil
}

func (b *FakeBackend) Config() Config {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.config
}

func (b *FakeBackend) SetDTR(value bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.dtr = value
	return nil
}

func (b *FakeBackend) SetRTS(value bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rts = value
	return nil
}

func (b *FakeBackend) SendBreak(time.Duration) error {
	return nil
}

func (b *FakeBackend) Read(buf []byte) (int, error) {
	data, ok := <-b.rx
	if !ok {
		return 0, io.EOF
	}
	return copy(buf, data), nil
}

func (b *FakeBackend) InjectRX(data []byte) {
	b.rx <- append([]byte(nil), data...)
}

func (b *FakeBackend) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.writes = append(b.writes, append([]byte(nil), data...))
	return len(data), nil
}

func (b *FakeBackend) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.closed {
		close(b.rx)
		b.closed = true
	}
	return nil
}
