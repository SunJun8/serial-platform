package server

import (
	"net/http"

	"serial-platform/internal/storage"
)

type ServerConfig struct {
	DB *storage.DB
}

type Server struct {
	db            *storage.DB
	mux           *http.ServeMux
	agentRegistry *agentRegistry
}

func New(config ServerConfig) *Server {
	srv := &Server{
		db:            config.DB,
		mux:           http.NewServeMux(),
		agentRegistry: newAgentRegistry(),
	}
	srv.routes()
	return srv
}

func (srv *Server) Handler() http.Handler {
	return srv
}

func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	srv.mux.ServeHTTP(w, r)
}

func (srv *Server) routes() {
	srv.mux.HandleFunc("GET /api/agents", srv.handleListAgents)
	srv.mux.HandleFunc("GET /api/channels", srv.handleListChannels)
	srv.mux.HandleFunc("GET /ws/agent", srv.handleAgentWebSocket)
}
