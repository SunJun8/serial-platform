package server

import (
	"sync"
	"time"

	"nhooyr.io/websocket"
)

type AgentConnection struct {
	AgentID string
	Conn    *websocket.Conn
	SeenAt  time.Time
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

	registry.connections[conn.AgentID] = conn
}

func (registry *agentRegistry) remove(agentID string, conn *websocket.Conn) {
	registry.mu.Lock()
	defer registry.mu.Unlock()

	current, ok := registry.connections[agentID]
	if ok && current.Conn == conn {
		delete(registry.connections, agentID)
	}
}
