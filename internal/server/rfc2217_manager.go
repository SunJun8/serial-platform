package server

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"

	"serial-platform/internal/protocol"
	"serial-platform/internal/serial"
	"serial-platform/internal/storage"
)

const rfc2217ScanInterval = 5 * time.Second

func (srv *Server) ServeRFC2217(ctx context.Context, bindHost string) error {
	manager := &rfc2217Manager{
		srv:      srv,
		bindHost: bindHost,
		active:   make(map[string]*rfc2217ActiveEntry),
	}
	defer manager.close()

	if err := manager.sync(ctx); err != nil {
		return err
	}

	ticker := time.NewTicker(rfc2217ScanInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := manager.sync(ctx); err != nil {
				return err
			}
		}
	}
}

type rfc2217Manager struct {
	srv      *Server
	bindHost string

	mu     sync.Mutex
	nextID uint64
	active map[string]*rfc2217ActiveEntry
}

type rfc2217ActiveEntry struct {
	signature rfc2217ActiveSignature
	cancel    context.CancelFunc
	done      chan struct{}
	token     uint64
}

type rfc2217ActiveSignature struct {
	agentID string
	port    int
	config  serial.Config
}

func (m *rfc2217Manager) sync(ctx context.Context) error {
	channels, err := m.srv.db.ListChannels()
	if err != nil {
		return err
	}

	want := make(map[string]storage.Channel)
	for _, channel := range channels {
		if channel.RFC2217Port <= 0 || channel.Status == storage.ChannelStatusDisabled {
			continue
		}
		want[channel.ID] = channel
	}

	var waitFor []*rfc2217ActiveEntry
	var start []storage.Channel

	m.mu.Lock()
	for channelID, entry := range m.active {
		channel, ok := want[channelID]
		if !ok {
			entry.cancel()
			delete(m.active, channelID)
			continue
		}
		if entry.signature != rfc2217Signature(channel) {
			entry.cancel()
			delete(m.active, channelID)
			waitFor = append(waitFor, entry)
			start = append(start, channel)
		}
		delete(want, channelID)
	}
	for _, channel := range want {
		start = append(start, channel)
	}
	m.mu.Unlock()

	for _, entry := range waitFor {
		select {
		case <-entry.done:
		case <-ctx.Done():
			return nil
		}
	}

	for _, channel := range start {
		if err := m.start(ctx, channel); err != nil {
			return err
		}
	}
	return nil
}

func (m *rfc2217Manager) start(ctx context.Context, channel storage.Channel) error {
	signature := rfc2217Signature(channel)

	m.mu.Lock()
	if entry, ok := m.active[channel.ID]; ok && entry.signature == signature {
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()

	addr := net.JoinHostPort(m.bindHost, strconv.Itoa(channel.RFC2217Port))
	netListener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}

	listenerCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	listener := NewRFC2217TunnelListener(
		netListener,
		channel.ID,
		rfc2217ServerTunnelResolver{srv: m.srv, channel: channel},
		WithRFC2217ControlOwner(m.srv.controlOwner),
	)

	m.mu.Lock()
	m.nextID++
	token := m.nextID
	m.active[channel.ID] = &rfc2217ActiveEntry{
		signature: signature,
		cancel:    cancel,
		done:      done,
		token:     token,
	}
	m.mu.Unlock()

	go func() {
		defer close(done)
		_ = listener.Serve(listenerCtx)
		m.mu.Lock()
		if entry, ok := m.active[channel.ID]; ok && entry.token == token {
			delete(m.active, channel.ID)
		}
		m.mu.Unlock()
	}()
	return nil
}

func (m *rfc2217Manager) close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for channelID, entry := range m.active {
		entry.cancel()
		delete(m.active, channelID)
	}
}

func rfc2217Signature(channel storage.Channel) rfc2217ActiveSignature {
	return rfc2217ActiveSignature{
		agentID: channel.AgentID,
		port:    channel.RFC2217Port,
		config:  channelDefaultConfig(channel),
	}
}

type rfc2217ServerTunnelResolver struct {
	srv     *Server
	channel storage.Channel
}

func (r rfc2217ServerTunnelResolver) OpenRFC2217Tunnel(ctx context.Context, channelID string) (net.Conn, error) {
	if channelID != r.channel.ID {
		return nil, fmt.Errorf("channel %s does not match listener channel %s", channelID, r.channel.ID)
	}
	return r.srv.OpenRFC2217Tunnel(ctx, r.channel)
}

func (srv *Server) OpenRFC2217Tunnel(ctx context.Context, channel storage.Channel) (net.Conn, error) {
	tunnelID := uuid.NewString()
	message := protocol.OpenTunnel{
		Type:      protocol.MessageOpenTunnel,
		TunnelID:  tunnelID,
		ChannelID: channel.ID,
		Mode:      protocol.TunnelModeRFC2217,
	}
	conn, err := srv.tunnels.WaitAfterRegister(ctx, tunnelID, func() error {
		return srv.agentRegistry.send(ctx, channel.AgentID, message)
	})
	if err != nil {
		return nil, fmt.Errorf("open rfc2217 tunnel %s for channel %s: %w", tunnelID, channel.ID, err)
	}
	return conn, nil
}

func (srv *Server) rfc2217Resolver(channel storage.Channel) RFC2217Resolver {
	return func(context.Context) (serial.SerialControl, serial.Config, error) {
		if srv.serialResolver == nil {
			return nil, serial.Config{}, fmt.Errorf("serial resolver is not configured for channel %s", channel.ID)
		}
		control, ok := srv.serialResolver(channel.ID)
		if !ok {
			return nil, serial.Config{}, fmt.Errorf("serial control is not available for channel %s", channel.ID)
		}
		return control, channelDefaultConfig(channel), nil
	}
}

func channelDefaultConfig(channel storage.Channel) serial.Config {
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
	config.Flow = "none"
	return config
}
