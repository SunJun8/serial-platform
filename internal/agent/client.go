package agent

import (
	"context"
	"errors"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"

	"nhooyr.io/websocket"

	"serial-platform/internal/protocol"
)

type Client struct {
	Config Config

	mu   sync.Mutex
	conn *websocket.Conn
}

func (client *Client) Connect(ctx context.Context) (string, error) {
	client.mu.Lock()
	defer client.mu.Unlock()

	if client.conn != nil {
		return "", errors.New("agent client already connected")
	}

	wsURL, err := agentWebSocketURL(client.Config.ServerURL)
	if err != nil {
		return "", err
	}

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		return "", err
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = conn.Close(websocket.StatusNormalClosure, "")
		}
	}()

	hostname, _ := os.Hostname()

	hello := protocol.AgentHello{
		Type:     protocol.MessageAgentHello,
		AgentID:  client.Config.AgentID,
		Hostname: hostname,
		OS:       runtime.GOOS,
		Arch:     runtime.GOARCH,
	}
	if err := protocol.WriteJSON(ctx, conn, hello); err != nil {
		return "", err
	}

	var accepted protocol.AgentAccepted
	if err := protocol.ReadJSON(ctx, conn, &accepted); err != nil {
		return "", err
	}
	client.conn = conn
	closeOnError = false
	return accepted.Status, nil
}

func (client *Client) Close(_ context.Context) error {
	client.mu.Lock()
	conn := client.conn
	client.conn = nil
	client.mu.Unlock()

	if conn == nil {
		return nil
	}
	return conn.Close(websocket.StatusNormalClosure, "")
}

func (client *Client) SendLogFrames(ctx context.Context, frames <-chan protocol.LogFrame) error {
	wsURL, err := webSocketURL(client.Config.ServerURL, "/ws/logs")
	if err != nil {
		return err
	}

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		return err
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	for frame := range frames {
		encoded, err := protocol.EncodeLogFrame(frame)
		if err != nil {
			return err
		}
		if err := conn.Write(ctx, websocket.MessageBinary, encoded); err != nil {
			return err
		}
	}
	return nil
}

func agentWebSocketURL(serverURL string) (string, error) {
	return webSocketURL(serverURL, "/ws/agent")
}

func webSocketURL(serverURL, path string) (string, error) {
	parsed, err := url.Parse(serverURL)
	if err != nil {
		return "", err
	}
	switch parsed.Scheme {
	case "http":
		parsed.Scheme = "ws"
	case "https":
		parsed.Scheme = "wss"
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + path
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}
