package server

import (
	"context"
	"net/http"
	"time"

	"nhooyr.io/websocket"

	"serial-platform/internal/protocol"
	"serial-platform/internal/storage"
)

func (srv *Server) handleAgentWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	ctx := r.Context()
	var hello protocol.AgentHello
	if err := protocol.ReadJSON(ctx, conn, &hello); err != nil {
		return
	}
	if hello.Type != protocol.MessageAgentHello || hello.AgentID == "" {
		conn.Close(websocket.StatusPolicyViolation, "malformed agent hello")
		return
	}

	now := time.Now().UTC()
	if err := srv.db.UpsertAgent(storage.Agent{
		ID:        hello.AgentID,
		Name:      hello.Hostname,
		Status:    storage.AgentStatusPending,
		Hostname:  hello.Hostname,
		OS:        hello.OS,
		Arch:      hello.Arch,
		MachineID: hello.MachineID,
		UpdatedAt: now,
	}); err != nil {
		conn.Close(websocket.StatusInternalError, err.Error())
		return
	}

	srv.agentRegistry.upsert(AgentConnection{
		AgentID: hello.AgentID,
		Conn:    conn,
		SeenAt:  now,
	})
	defer srv.agentRegistry.remove(hello.AgentID, conn)

	accepted := protocol.AgentAccepted{
		Type:   protocol.MessageAgentAccepted,
		Status: string(storage.AgentStatusPending),
	}
	if err := protocol.WriteJSON(ctx, conn, accepted); err != nil {
		return
	}

	keepAgentConnectionOpen(ctx, conn)
}

func keepAgentConnectionOpen(ctx context.Context, conn *websocket.Conn) {
	for {
		_, _, err := conn.Read(ctx)
		if err != nil {
			return
		}
	}
}
