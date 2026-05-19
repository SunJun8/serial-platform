package server

import (
	"net/http"

	"serial-platform/internal/storage"
)

type ServerConfig struct {
	DB *storage.DB
}

type Server struct {
	db  *storage.DB
	mux *http.ServeMux
}

func New(config ServerConfig) *Server {
	srv := &Server{
		db:  config.DB,
		mux: http.NewServeMux(),
	}
	srv.routes()
	return srv
}

func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	srv.mux.ServeHTTP(w, r)
}

func (srv *Server) routes() {
	srv.mux.HandleFunc("GET /api/agents", srv.handleListAgents)
	srv.mux.HandleFunc("GET /api/channels", srv.handleListChannels)
}
