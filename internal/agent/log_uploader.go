package agent

import (
	"context"
	"sync"

	"serial-platform/internal/protocol"
	"serial-platform/internal/serial"
)

type LogUploaderConfig struct {
	Out chan<- protocol.LogFrame
}

type LogUploader struct {
	mu  sync.Mutex
	seq uint64
	out chan<- protocol.LogFrame
}

func NewLogUploader(config LogUploaderConfig) *LogUploader {
	return &LogUploader{out: config.Out}
}

func (u *LogUploader) Forward(ctx context.Context, events <-chan serial.Event) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-events:
			if !ok {
				return nil
			}
			frame := u.NextFrame(event)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case u.out <- frame:
			}
		}
	}
}

func (u *LogUploader) NextFrame(event serial.Event) protocol.LogFrame {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.seq++
	return SerialEventToLogFrame(u.seq, event)
}
