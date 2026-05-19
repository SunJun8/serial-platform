package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"serial-platform/internal/storage"
)

func (srv *Server) handleListAgents(w http.ResponseWriter, r *http.Request) {
	agents, err := srv.db.ListAgents()
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, agents)
}

func (srv *Server) handleListChannels(w http.ResponseWriter, r *http.Request) {
	channels, err := srv.db.ListChannels()
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, channels)
}

func (srv *Server) handleApproveAgent(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agentID")
	if agentID == "" {
		writeBadRequest(w, "agent id is required")
		return
	}

	agent, err := srv.db.UpdateAgentStatus(agentID, storage.AgentStatusActive, time.Now().UTC())
	if errors.Is(err, storage.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent not found"})
		return
	}
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, agent)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, err error) {
	writeJSON(w, http.StatusInternalServerError, map[string]string{
		"error": err.Error(),
	})
}
