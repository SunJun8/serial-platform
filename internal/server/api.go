package server

import (
	"encoding/json"
	"net/http"
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
