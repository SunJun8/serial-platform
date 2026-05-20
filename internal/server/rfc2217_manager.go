package server

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"serial-platform/internal/serial"
	"serial-platform/internal/storage"
)

const rfc2217ScanInterval = 5 * time.Second

func (srv *Server) ServeRFC2217(ctx context.Context, bindHost string) error {
	manager := &rfc2217Manager{
		srv:      srv,
		bindHost: bindHost,
		active:   make(map[string]context.CancelFunc),
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
	active map[string]context.CancelFunc
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

	m.mu.Lock()
	for channelID, cancel := range m.active {
		if _, ok := want[channelID]; !ok {
			cancel()
			delete(m.active, channelID)
		}
	}
	m.mu.Unlock()

	for _, channel := range want {
		if err := m.start(ctx, channel); err != nil {
			return err
		}
	}
	return nil
}

func (m *rfc2217Manager) start(ctx context.Context, channel storage.Channel) error {
	m.mu.Lock()
	if _, ok := m.active[channel.ID]; ok {
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
	listener := NewRFC2217ListenerWithResolver(
		netListener,
		channel.ID,
		m.srv.rfc2217Resolver(channel),
		WithRFC2217ControlOwner(m.srv.controlOwner),
	)

	m.mu.Lock()
	m.active[channel.ID] = cancel
	m.mu.Unlock()

	go func() {
		_ = listener.Serve(listenerCtx)
		m.mu.Lock()
		delete(m.active, channel.ID)
		m.mu.Unlock()
	}()
	return nil
}

func (m *rfc2217Manager) close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for channelID, cancel := range m.active {
		cancel()
		delete(m.active, channelID)
	}
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
