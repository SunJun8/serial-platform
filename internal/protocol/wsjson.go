package protocol

import (
	"context"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

func WriteJSON(ctx context.Context, conn *websocket.Conn, value any) error {
	return wsjson.Write(ctx, conn, value)
}

func ReadJSON(ctx context.Context, conn *websocket.Conn, target any) error {
	return wsjson.Read(ctx, conn, target)
}
