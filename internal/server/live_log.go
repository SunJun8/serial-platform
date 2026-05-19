package server

import (
	"encoding/base64"
	"net/http"
	"sync"

	"nhooyr.io/websocket"

	"serial-platform/internal/protocol"
)

const LiveLogSubscriberBuffer = 64

type LiveLogHub struct {
	mu          sync.Mutex
	subscribers map[string]map[chan protocol.LogFrame]struct{}
}

func NewLiveLogHub() *LiveLogHub {
	return &LiveLogHub{subscribers: make(map[string]map[chan protocol.LogFrame]struct{})}
}

func (h *LiveLogHub) Publish(frame protocol.LogFrame) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for subscriber := range h.subscribers[frame.ChannelID] {
		select {
		case subscriber <- frame:
		default:
			select {
			case <-subscriber:
			default:
			}
			subscriber <- frame
		}
	}
}

func (h *LiveLogHub) Subscribe(channelID string) (<-chan protocol.LogFrame, func()) {
	ch := make(chan protocol.LogFrame, LiveLogSubscriberBuffer)

	h.mu.Lock()
	if h.subscribers[channelID] == nil {
		h.subscribers[channelID] = make(map[chan protocol.LogFrame]struct{})
	}
	h.subscribers[channelID][ch] = struct{}{}
	h.mu.Unlock()

	unsubscribe := func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.subscribers[channelID] == nil {
			return
		}
		delete(h.subscribers[channelID], ch)
		if len(h.subscribers[channelID]) == 0 {
			delete(h.subscribers, channelID)
		}
		close(ch)
	}
	return ch, unsubscribe
}

func (srv *Server) handleLiveLogWebSocket(w http.ResponseWriter, r *http.Request) {
	channelID := r.PathValue("channelID")
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	frames, unsubscribe := srv.liveLog.Subscribe(channelID)
	defer unsubscribe()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case frame, ok := <-frames:
			if !ok {
				return
			}
			if err := protocol.WriteJSON(ctx, conn, liveLogFrameJSON(frame)); err != nil {
				return
			}
		}
	}
}

func liveLogFrameJSON(frame protocol.LogFrame) protocol.LiveLogFrame {
	return protocol.LiveLogFrame{
		ChannelID:   frame.ChannelID,
		Seq:         frame.Seq,
		TimestampNS: frame.TimestampNS,
		Direction:   frame.Direction,
		Flags:       frame.Flags,
		Payload:     base64.StdEncoding.EncodeToString(frame.Payload),
	}
}
