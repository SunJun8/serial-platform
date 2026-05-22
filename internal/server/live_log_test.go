package server_test

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"serial-platform/internal/protocol"
	"serial-platform/internal/server"
	"serial-platform/internal/storage"
)

func TestLiveLogSubscribeReceivesPublishedFrame(t *testing.T) {
	hub := server.NewLiveLogHub()
	frames, unsubscribe := hub.Subscribe("channel-1")
	defer unsubscribe()

	want := protocol.LogFrame{
		ChannelID:   "channel-1",
		Seq:         7,
		TimestampNS: time.Unix(1, 2).UnixNano(),
		Direction:   protocol.DirectionRX,
		Flags:       protocol.FlagRaw,
		Payload:     []byte("boot\n"),
	}
	hub.Publish(want)

	select {
	case got := <-frames:
		if got.ChannelID != want.ChannelID || got.Seq != want.Seq || string(got.Payload) != string(want.Payload) {
			t.Fatalf("frame = %+v, want %+v", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for published live log frame")
	}
}

func TestLiveLogSubscribeDropsOldestWhenFull(t *testing.T) {
	hub := server.NewLiveLogHub()
	frames, unsubscribe := hub.Subscribe("channel-1")
	defer unsubscribe()

	for i := 0; i < server.LiveLogSubscriberBuffer+1; i++ {
		hub.Publish(protocol.LogFrame{
			ChannelID: "channel-1",
			Seq:       uint64(i),
			Direction: protocol.DirectionRX,
			Payload:   []byte{byte(i)},
		})
	}

	got := <-frames
	if got.Seq != 1 {
		t.Fatalf("first frame seq = %d, want oldest dropped seq 1", got.Seq)
	}
}

func TestLiveLogUnsubscribeIsIdempotentAndKeepsOtherSubscribers(t *testing.T) {
	hub := server.NewLiveLogHub()
	frames1, unsubscribe1 := hub.Subscribe("channel-1")
	frames2, unsubscribe2 := hub.Subscribe("channel-1")
	defer unsubscribe2()

	unsubscribe1()
	unsubscribe1()

	if _, ok := <-frames1; ok {
		t.Fatal("frames1 is open after unsubscribe, want closed")
	}

	want := protocol.LogFrame{
		ChannelID: "channel-1",
		Seq:       42,
		Direction: protocol.DirectionRX,
		Payload:   []byte("still subscribed"),
	}
	hub.Publish(want)

	select {
	case got := <-frames2:
		if got.Seq != want.Seq || string(got.Payload) != string(want.Payload) {
			t.Fatalf("frame = %+v, want %+v", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for second subscriber frame")
	}
}

func TestLiveLogWebSocketStreamsBase64Payload(t *testing.T) {
	srv := server.New(server.ServerConfig{})
	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn := dialLiveLogWebSocket(t, ctx, httpSrv.URL, "channel-1")
	defer conn.CloseNow()

	publishDone := make(chan struct{})
	go func() {
		defer close(publishDone)
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				srv.LiveLog().Publish(protocol.LogFrame{
					ChannelID:   "channel-1",
					Seq:         3,
					TimestampNS: time.Unix(4, 5).UnixNano(),
					Direction:   protocol.DirectionTX,
					Flags:       protocol.FlagRaw,
					Payload:     []byte("hello"),
				})
			}
		}
	}()

	var got protocol.LiveLogFrame
	if err := protocol.ReadJSON(ctx, conn, &got); err != nil {
		t.Fatalf("protocol.ReadJSON returned error: %v", err)
	}
	cancel()
	<-publishDone
	if got.ChannelID != "channel-1" || got.Seq != 3 || got.Payload != "aGVsbG8=" {
		t.Fatalf("live log frame = %+v, want channel-1 seq 3 base64 hello", got)
	}
}

func TestLogWebSocketPublishesToLiveLogSubscribers(t *testing.T) {
	root := t.TempDir()
	db, err := storage.Open(filepath.Join(root, "meta.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	upsertLogTestChannel(t, db, "channel-1")

	srv := server.New(server.ServerConfig{DB: db, LogDir: filepath.Join(root, "logs")})
	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	liveConn := dialLiveLogWebSocket(t, ctx, httpSrv.URL, "channel-1")
	defer liveConn.CloseNow()

	logConn := dialLogWebSocket(t, ctx, httpSrv.URL)
	defer logConn.Close(websocket.StatusNormalClosure, "")

	encoded, err := protocol.EncodeLogFrame(protocol.LogFrame{
		ChannelID:   "channel-1",
		Seq:         9,
		TimestampNS: time.Unix(10, 11).UnixNano(),
		Direction:   protocol.DirectionRX,
		Flags:       protocol.FlagRaw,
		Payload:     []byte("from uploader"),
	})
	if err != nil {
		t.Fatalf("EncodeLogFrame returned error: %v", err)
	}

	for {
		if err := logConn.Write(ctx, websocket.MessageBinary, encoded); err != nil {
			t.Fatalf("logConn.Write returned error: %v", err)
		}

		readCtx, readCancel := context.WithTimeout(ctx, 25*time.Millisecond)
		var got protocol.LiveLogFrame
		err := protocol.ReadJSON(readCtx, liveConn, &got)
		readCancel()
		if err != nil {
			if ctx.Err() != nil {
				t.Fatalf("protocol.ReadJSON timed out waiting for live log publish: %v", err)
			}
			continue
		}
		if got.ChannelID != "channel-1" || got.Seq != 9 || got.Payload != "ZnJvbSB1cGxvYWRlcg==" {
			t.Fatalf("live log frame = %+v, want uploaded channel-1 seq 9", got)
		}
		return
	}
}

func dialLiveLogWebSocket(t *testing.T, ctx context.Context, serverURL, channelID string) *websocket.Conn {
	t.Helper()

	wsURL := "ws" + strings.TrimPrefix(serverURL, "http") + "/ws/live-log/" + channelID
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}
	return conn
}
