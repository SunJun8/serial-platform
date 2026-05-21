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

	mu            sync.Mutex
	controlSendMu sync.Mutex
	conn          *websocket.Conn
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

func (client *Client) SendControl(ctx context.Context, value any) error {
	conn, err := client.controlConn()
	if err != nil {
		return err
	}
	client.controlSendMu.Lock()
	defer client.controlSendMu.Unlock()
	return protocol.WriteJSON(ctx, conn, value)
}

func (client *Client) ReadControl(ctx context.Context) (protocol.MessageType, []byte, error) {
	conn, err := client.controlConn()
	if err != nil {
		return "", nil, err
	}
	messageType, data, err := conn.Read(ctx)
	if err != nil {
		return "", nil, err
	}
	if messageType != websocket.MessageText {
		return "", nil, fmt.Errorf("control websocket received %v message", messageType)
	}
	var envelope struct {
		Type protocol.MessageType `json:"type"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return "", nil, err
	}
	return envelope.Type, data, nil
}

func (client *Client) HandleControlMessages(ctx context.Context, resolver RFC2217ControlResolver, dialer TunnelDialer) error {
	terminalSessions := make(map[string]terminalSession)
	defer closeTerminalSessions(terminalSessions)

	for {
		messageType, data, err := client.ReadControl(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		switch messageType {
		case protocol.MessageOpenTunnel:
			var open protocol.OpenTunnel
			if err := json.Unmarshal(data, &open); err != nil {
				return err
			}
			go client.handleOpenTunnel(ctx, open, resolver, dialer)
		case protocol.MessageTerminalOpen:
			var open protocol.TerminalOpen
			if err := json.Unmarshal(data, &open); err != nil {
				return err
			}
			client.handleTerminalOpen(ctx, open, resolver, terminalSessions)
		case protocol.MessageTerminalClose:
			var closeMessage protocol.TerminalClose
			if err := json.Unmarshal(data, &closeMessage); err != nil {
				return err
			}
			client.handleTerminalClose(ctx, closeMessage, terminalSessions)
		case protocol.MessageTerminalWrite,
			protocol.MessageSerialSetConfig,
			protocol.MessageSerialSetDTR,
			protocol.MessageSerialSetRTS,
			protocol.MessageSerialSendBreak:
			client.handleTerminalOperation(ctx, messageType, data, terminalSessions)
		case protocol.MessageChannelSync:
		default:
			log.Printf("agent received unsupported control message type %q", messageType)
		}
	}
}

type terminalSession struct {
	channelID string
	session   serial.ControlSession
}

func closeTerminalSessions(sessions map[string]terminalSession) {
	for sessionID, entry := range sessions {
		_ = entry.session.Close()
		delete(sessions, sessionID)
	}
}

func (client *Client) handleTerminalOpen(ctx context.Context, open protocol.TerminalOpen, resolver RFC2217ControlResolver, sessions map[string]terminalSession) {
	var err error
	if open.SessionID == "" {
		err = errors.New("terminal session_id is required")
	} else if open.ChannelID == "" {
		err = errors.New("terminal channel_id is required")
	} else if _, exists := sessions[open.SessionID]; exists {
		err = fmt.Errorf("terminal session %s already exists", open.SessionID)
	} else if resolver == nil {
		err = errors.New("terminal control resolver is not configured")
	} else {
		var control serial.SerialControl
		control, _, err = resolver.RFC2217Control(ctx, open.ChannelID)
		if err == nil {
			var session serial.ControlSession
			session, err = control.OpenControlSession(ctx, "web")
			if err == nil {
				sessions[open.SessionID] = terminalSession{channelID: open.ChannelID, session: session}
			}
		}
	}
	client.sendOperationResult(ctx, "", err)
}

func (client *Client) handleTerminalClose(ctx context.Context, closeMessage protocol.TerminalClose, sessions map[string]terminalSession) {
	entry, err := terminalSessionFor(sessions, closeMessage.SessionID, closeMessage.ChannelID)
	if err == nil {
		err = entry.session.Close()
		delete(sessions, closeMessage.SessionID)
	}
	client.sendOperationResult(ctx, "", err)
}

func (client *Client) handleTerminalOperation(ctx context.Context, messageType protocol.MessageType, data []byte, sessions map[string]terminalSession) {
	requestID, err := client.applyTerminalOperation(ctx, messageType, data, sessions)
	client.sendOperationResult(ctx, requestID, err)
}

func (client *Client) applyTerminalOperation(_ context.Context, messageType protocol.MessageType, data []byte, sessions map[string]terminalSession) (string, error) {
	switch messageType {
	case protocol.MessageTerminalWrite:
		var msg protocol.TerminalWrite
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", err
		}
		entry, err := terminalSessionFor(sessions, msg.SessionID, msg.ChannelID)
		if err != nil {
			return msg.RequestID, err
		}
		return msg.RequestID, entry.session.Write(msg.Data)
	case protocol.MessageSerialSetConfig:
		var msg protocol.SerialSetConfig
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", err
		}
		entry, err := terminalSessionFor(sessions, msg.SessionID, msg.ChannelID)
		if err != nil {
			return msg.RequestID, err
		}
		return msg.RequestID, entry.session.SetConfig(serial.Config{
			Baud:     msg.Baud,
			DataBits: msg.DataBits,
			Parity:   msg.Parity,
			StopBits: msg.StopBits,
			Flow:     msg.Flow,
		})
	case protocol.MessageSerialSetDTR:
		var msg protocol.SerialSetDTR
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", err
		}
		entry, err := terminalSessionFor(sessions, msg.SessionID, msg.ChannelID)
		if err != nil {
			return msg.RequestID, err
		}
		return msg.RequestID, entry.session.SetDTR(msg.Value)
	case protocol.MessageSerialSetRTS:
		var msg protocol.SerialSetRTS
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", err
		}
		entry, err := terminalSessionFor(sessions, msg.SessionID, msg.ChannelID)
		if err != nil {
			return msg.RequestID, err
		}
		return msg.RequestID, entry.session.SetRTS(msg.Value)
	case protocol.MessageSerialSendBreak:
		var msg protocol.SerialSendBreak
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", err
		}
		entry, err := terminalSessionFor(sessions, msg.SessionID, msg.ChannelID)
		if err != nil {
			return msg.RequestID, err
		}
		return msg.RequestID, entry.session.SendBreak(time.Duration(msg.DurationMS) * time.Millisecond)
	default:
		return "", fmt.Errorf("unsupported terminal operation %q", messageType)
	}
}

func terminalSessionFor(sessions map[string]terminalSession, sessionID, channelID string) (terminalSession, error) {
	entry, ok := sessions[sessionID]
	if !ok {
		return terminalSession{}, fmt.Errorf("terminal session %s not found", sessionID)
	}
	if channelID != "" && entry.channelID != channelID {
		return terminalSession{}, fmt.Errorf("terminal session %s belongs to channel %s", sessionID, entry.channelID)
	}
	return entry, nil
}

func (client *Client) sendOperationResult(ctx context.Context, requestID string, err error) {
	result := protocol.OperationResult{
		Type:      protocol.MessageOperationResult,
		RequestID: requestID,
		OK:        err == nil,
	}
	if err != nil {
		result.Error = err.Error()
	}
	if sendErr := client.SendControl(ctx, result); sendErr != nil && ctx.Err() == nil {
		log.Printf("send operation result: %v", sendErr)
	}
}

func (client *Client) handleOpenTunnel(ctx context.Context, open protocol.OpenTunnel, resolver RFC2217ControlResolver, dialer TunnelDialer) {
	if open.Mode != protocol.TunnelModeRFC2217 {
		client.sendTunnelError(ctx, open.TunnelID, fmt.Errorf("unsupported tunnel mode %q", open.Mode))
		return
	}
	if resolver == nil {
		client.sendTunnelError(ctx, open.TunnelID, errors.New("rfc2217 control resolver is not configured"))
		return
	}

	control, config, err := resolver.RFC2217Control(ctx, open.ChannelID)
	if err != nil {
		client.sendTunnelError(ctx, open.TunnelID, err)
		return
	}
	wsConn, err := dialer.Dial(ctx, open.TunnelID)
	if err != nil {
		client.sendTunnelError(ctx, open.TunnelID, err)
		return
	}

	conn := websocket.NetConn(ctx, wsConn, websocket.MessageBinary)
	if err := client.SendControl(ctx, protocol.TunnelOpened{
		Type:     protocol.MessageTunnelOpened,
		TunnelID: open.TunnelID,
		Mode:     open.Mode,
	}); err != nil {
		_ = conn.Close()
		return
	}

	if err := HandleRFC2217Tunnel(ctx, conn, open.ChannelID, control, config); err != nil && ctx.Err() == nil {
		client.sendTunnelError(ctx, open.TunnelID, err)
	}
}

func (client *Client) sendTunnelError(ctx context.Context, tunnelID string, err error) {
	if err == nil {
		return
	}
	if sendErr := client.SendControl(ctx, protocol.TunnelError{
		Type:     protocol.MessageTunnelError,
		TunnelID: tunnelID,
		Error:    err.Error(),
	}); sendErr != nil && ctx.Err() == nil {
		log.Printf("send tunnel error: %v", sendErr)
	}
}

func (client *Client) controlConn() (*websocket.Conn, error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.conn == nil {
		return nil, errors.New("agent client not connected")
	}
	return client.conn, nil
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

func (client *Client) SendLogFramesLoop(ctx context.Context, frames <-chan protocol.LogFrame, backoff time.Duration) error {
	if backoff <= 0 {
		backoff = time.Second
	}

	wsURL, err := webSocketURL(client.Config.ServerURL, "/ws/logs")
	if err != nil {
		return err
	}

	var pending protocol.LogFrame
	hasPending := false
	for {
		if !hasPending {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case frame, ok := <-frames:
				if !ok {
					return nil
				}
				pending = frame
				hasPending = true
			}
		}

		conn, _, err := websocket.Dial(ctx, wsURL, nil)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			log.Printf("connect log stream: %v", err)
			if err := sleepContext(ctx, backoff); err != nil {
				return err
			}
			continue
		}
		connCtx := conn.CloseRead(ctx)

		reconnect := false
		for {
			encoded, err := protocol.EncodeLogFrame(pending)
			if err != nil {
				_ = conn.Close(websocket.StatusNormalClosure, "")
				return err
			}
			if err := conn.Ping(ctx); err != nil {
				_ = conn.Close(websocket.StatusNormalClosure, "")
				if ctx.Err() != nil {
					return ctx.Err()
				}
				log.Printf("ping log stream: %v", err)
				reconnect = true
				break
			}
			if err := conn.Write(ctx, websocket.MessageBinary, encoded); err != nil {
				_ = conn.Close(websocket.StatusNormalClosure, "")
				if ctx.Err() != nil {
					return ctx.Err()
				}
				log.Printf("send log frame: %v", err)
				reconnect = true
				break
			}
			hasPending = false

			select {
			case <-ctx.Done():
				_ = conn.Close(websocket.StatusNormalClosure, "")
				return ctx.Err()
			case <-connCtx.Done():
				reconnect = true
			default:
			}
			if reconnect {
				break
			}
			select {
			case <-ctx.Done():
				_ = conn.Close(websocket.StatusNormalClosure, "")
				return ctx.Err()
			case <-connCtx.Done():
				reconnect = true
			case frame, ok := <-frames:
				if !ok {
					_ = conn.Close(websocket.StatusNormalClosure, "")
					return nil
				}
				pending = frame
				hasPending = true
			}
			if reconnect {
				break
			}
		}

		_ = conn.Close(websocket.StatusNormalClosure, "")
		if err := sleepContext(ctx, backoff); err != nil {
			return err
		}
	}
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
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

type RFC2217ControlResolver interface {
	RFC2217Control(ctx context.Context, channelID string) (serial.SerialControl, serial.Config, error)
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
		if ctx.Err() != nil {
			return ctx.Err()
		}
		log.Printf("agent runtime scan: %v", err)
	}

	ticker := time.NewTicker(runtime.scanInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := runtime.scan(ctx); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				log.Printf("agent runtime scan: %v", err)
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
	for _, stream := range result.Events {
		runtime.startForwarding(ctx, stream)
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

func (runtime *Runtime) startForwarding(ctx context.Context, stream EventStream) {
	if runtime.forwardEvents == nil {
		if stream.Cancel != nil {
			stream.Cancel()
		}
		return
	}
	if stream.Events == nil {
		if stream.Cancel != nil {
			stream.Cancel()
		}
		return
	}

	runtime.mu.Lock()
	if _, exists := runtime.forwarding[stream.Events]; exists {
		runtime.mu.Unlock()
		return
	}
	runtime.forwarding[stream.Events] = struct{}{}
	runtime.mu.Unlock()

	go func() {
		defer func() {
			if stream.Cancel != nil {
				stream.Cancel()
			}
			runtime.mu.Lock()
			delete(runtime.forwarding, stream.Events)
			runtime.mu.Unlock()
		}()
		if err := runtime.forwardEvents(ctx, stream.Events); err != nil && ctx.Err() == nil {
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
