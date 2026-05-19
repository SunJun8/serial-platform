package server

import (
	"net/http"

	"serial-platform/internal/storage"
)

type ServerConfig struct {
	DB     *storage.DB
	LogDir string
}

type Server struct {
	db            *storage.DB
	logDir        string
	mux           *http.ServeMux
	agentRegistry *agentRegistry
}

func New(config ServerConfig) *Server {
	srv := &Server{
		db:            config.DB,
		logDir:        config.LogDir,
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
	srv.mux.HandleFunc("GET /ws/logs", srv.handleLogWebSocket)
}
