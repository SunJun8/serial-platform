package agent

import (
	"context"
	"net/url"
	"runtime"
	"strings"

	"nhooyr.io/websocket"

	"serial-platform/internal/protocol"
)

type Client struct {
	Config Config
}

func (client *Client) Connect(ctx context.Context) (string, error) {
	wsURL, err := agentWebSocketURL(client.Config.ServerURL)
	if err != nil {
		return "", err
	}

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		return "", err
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	hello := protocol.AgentHello{
		Type:    protocol.MessageAgentHello,
		AgentID: client.Config.AgentID,
		OS:      runtime.GOOS,
		Arch:    runtime.GOARCH,
	}
	if err := protocol.WriteJSON(ctx, conn, hello); err != nil {
		return "", err
	}

	var accepted protocol.AgentAccepted
	if err := protocol.ReadJSON(ctx, conn, &accepted); err != nil {
		return "", err
	}
	return accepted.Status, nil
}

func agentWebSocketURL(serverURL string) (string, error) {
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
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/ws/agent"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}
