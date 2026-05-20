package server

import (
	"context"
	"errors"
	"sync"
	"time"

	"nhooyr.io/websocket"

	"serial-platform/internal/protocol"
)

var errAgentNotConnected = errors.New("agent not connected")

type AgentConnection struct {
	AgentID string
	Conn    *websocket.Conn
	SeenAt  time.Time
	sendMu  *sync.Mutex
}

func newAgentConnection(agentID string, conn *websocket.Conn, seenAt time.Time) AgentConnection {
	return AgentConnection{
		AgentID: agentID,
		Conn:    conn,
		SeenAt:  seenAt,
		sendMu:  &sync.Mutex{},
	}
}

type agentRegistry struct {
	mu          sync.Mutex
	connections map[string]AgentConnection
}

func newAgentRegistry() *agentRegistry {
	return &agentRegistry{
		connections: make(map[string]AgentConnection),
	}
}

func (registry *agentRegistry) upsert(conn AgentConnection) {
	registry.mu.Lock()
	defer registry.mu.Unlock()

	if conn.sendMu == nil {
		conn.sendMu = &sync.Mutex{}
	}
	registry.connections[conn.AgentID] = conn
}

func (registry *agentRegistry) get(agentID string) (AgentConnection, bool) {
	registry.mu.Lock()
	defer registry.mu.Unlock()

	conn, ok := registry.connections[agentID]
	return conn, ok
}

func (registry *agentRegistry) send(ctx context.Context, agentID string, value any) error {
	conn, ok := registry.get(agentID)
	if !ok {
		return errAgentNotConnected
	}
	return conn.send(ctx, value)
}

func (conn AgentConnection) send(ctx context.Context, value any) error {
	conn.sendMu.Lock()
	defer conn.sendMu.Unlock()
	return protocol.WriteJSON(ctx, conn.Conn, value)
}

func (registry *agentRegistry) remove(agentID string, conn *websocket.Conn) {
	registry.mu.Lock()
	defer registry.mu.Unlock()

	current, ok := registry.connections[agentID]
	if ok && current.Conn == conn {
		delete(registry.connections, agentID)
	}
}
