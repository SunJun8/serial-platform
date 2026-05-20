package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"nhooyr.io/websocket"

	"serial-platform/internal/protocol"
	"serial-platform/internal/serial"
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

	for {
		var frame protocol.LogFrame
		select {
		case <-ctx.Done():
			_ = conn.Close(websocket.StatusNormalClosure, "")
			return ctx.Err()
		case next, ok := <-frames:
			if !ok {
				return conn.Close(websocket.StatusNormalClosure, "")
			}
			frame = next
		}
		encoded, err := protocol.EncodeLogFrame(frame)
		if err != nil {
			_ = conn.Close(websocket.StatusNormalClosure, "")
			return err
		}
		if err := conn.Write(ctx, websocket.MessageBinary, encoded); err != nil {
			_ = conn.Close(websocket.StatusNormalClosure, "")
			return err
		}
	}
}

type channelConfigResponse struct {
	ID              string
	AgentID         string
	DevName         string
	IDPath          string
	IDPathTag       string
	Status          string
	DefaultBaud     int
	DefaultDataBits int
	DefaultParity   string
	DefaultStopBits int
	DefaultFlow     string
}

func (client *Client) FetchChannelConfigs(ctx context.Context) ([]ChannelConfig, error) {
	reqURL, err := serverHTTPURL(client.Config.ServerURL, "/api/channels")
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET /api/channels returned %s", resp.Status)
	}

	var channels []channelConfigResponse
	if err := json.NewDecoder(resp.Body).Decode(&channels); err != nil {
		return nil, err
	}

	configs := make([]ChannelConfig, 0, len(channels))
	for _, channel := range channels {
		if channel.AgentID != client.Config.AgentID {
			continue
		}
		configs = append(configs, channelConfigFromResponse(channel))
	}
	return configs, nil
}

func channelConfigFromResponse(channel channelConfigResponse) ChannelConfig {
	config := serial.DefaultConfig()
	if channel.DefaultBaud > 0 {
		config.Baud = channel.DefaultBaud
	}
	if channel.DefaultDataBits > 0 {
		config.DataBits = channel.DefaultDataBits
	}
	if channel.DefaultParity != "" {
		config.Parity = channel.DefaultParity
	}
	if channel.DefaultStopBits > 0 {
		config.StopBits = channel.DefaultStopBits
	}
	if channel.DefaultFlow != "" {
		config.Flow = channel.DefaultFlow
	}
	return ChannelConfig{
		ID:            channel.ID,
		AgentID:       channel.AgentID,
		DevName:       channel.DevName,
		IDPath:        channel.IDPath,
		IDPathTag:     channel.IDPathTag,
		Status:        channel.Status,
		DefaultConfig: config,
	}
}

type RuntimeReconciler interface {
	Reconcile(context.Context, []ChannelConfig, []DiscoveredDevice) ReconcileResult
}

type ChannelSourceFunc func(context.Context) ([]ChannelConfig, error)

type DiscoverFunc func(DiscoveryConfig) ([]DiscoveredDevice, error)

type ForwardEventsFunc func(context.Context, <-chan serial.Event) error

type RuntimeConfig struct {
	ScanInterval  time.Duration
	Discovery     DiscoveryConfig
	Discover      DiscoverFunc
	Reconciler    RuntimeReconciler
	Channels      []ChannelConfig
	ChannelSource ChannelSourceFunc
	ForwardEvents ForwardEventsFunc
}

type Runtime struct {
	scanInterval  time.Duration
	discovery     DiscoveryConfig
	discover      DiscoverFunc
	reconciler    RuntimeReconciler
	channels      []ChannelConfig
	channelSource ChannelSourceFunc
	forwardEvents ForwardEventsFunc

	mu         sync.Mutex
	forwarding map[<-chan serial.Event]struct{}
}

func NewRuntime(config RuntimeConfig) *Runtime {
	scanInterval := config.ScanInterval
	if scanInterval <= 0 {
		scanInterval = 3 * time.Second
	}
	discover := config.Discover
	if discover == nil {
		discover = DiscoverDevices
	}
	reconciler := config.Reconciler
	if reconciler == nil {
		reconciler = NewReconciler(ReconcilerConfig{})
	}
	return &Runtime{
		scanInterval:  scanInterval,
		discovery:     config.Discovery,
		discover:      discover,
		reconciler:    reconciler,
		channels:      append([]ChannelConfig(nil), config.Channels...),
		channelSource: config.ChannelSource,
		forwardEvents: config.ForwardEvents,
		forwarding:    make(map[<-chan serial.Event]struct{}),
	}
}

func (runtime *Runtime) Run(ctx context.Context) error {
	if err := runtime.scan(ctx); err != nil {
		return err
	}

	ticker := time.NewTicker(runtime.scanInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := runtime.scan(ctx); err != nil {
				return err
			}
		}
	}
}

func (runtime *Runtime) scan(ctx context.Context) error {
	devices, err := runtime.discover(runtime.discovery)
	if err != nil {
		return err
	}
	channels, err := runtime.currentChannels(ctx)
	if err != nil {
		return err
	}
	result := runtime.reconciler.Reconcile(ctx, channels, devices)
	for _, events := range result.Events {
		runtime.startForwarding(ctx, events)
	}
	return nil
}

func (runtime *Runtime) currentChannels(ctx context.Context) ([]ChannelConfig, error) {
	if runtime.channelSource == nil {
		return append([]ChannelConfig(nil), runtime.channels...), nil
	}
	channels, err := runtime.channelSource(ctx)
	if err != nil {
		return nil, err
	}
	return append([]ChannelConfig(nil), channels...), nil
}

func (runtime *Runtime) startForwarding(ctx context.Context, events <-chan serial.Event) {
	if runtime.forwardEvents == nil {
		return
	}

	runtime.mu.Lock()
	if _, exists := runtime.forwarding[events]; exists {
		runtime.mu.Unlock()
		return
	}
	runtime.forwarding[events] = struct{}{}
	runtime.mu.Unlock()

	go func() {
		defer func() {
			runtime.mu.Lock()
			delete(runtime.forwarding, events)
			runtime.mu.Unlock()
		}()
		if err := runtime.forwardEvents(ctx, events); err != nil && ctx.Err() == nil {
			log.Printf("forward serial events: %v", err)
		}
	}()
}

func agentWebSocketURL(serverURL string) (string, error) {
	return webSocketURL(serverURL, "/ws/agent")
}

func serverHTTPURL(serverURL, path string) (string, error) {
	parsed, err := url.Parse(serverURL)
	if err != nil {
		return "", err
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + path
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
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
