package server

import (
	"net/http"

	"serial-platform/internal/serial"
	"serial-platform/internal/storage"
)

type ServerConfig struct {
	DB             *storage.DB
	LogDir         string
	SerialResolver func(channelID string) (serial.SerialControl, bool)
}

type Server struct {
	db             *storage.DB
	logDir         string
	mux            *http.ServeMux
	agentRegistry  *agentRegistry
	controlOwner   *ControlOwner
	liveLog        *LiveLogHub
	serialResolver func(channelID string) (serial.SerialControl, bool)
}

func New(config ServerConfig) *Server {
	srv := &Server{
		db:             config.DB,
		logDir:         config.LogDir,
		mux:            http.NewServeMux(),
		agentRegistry:  newAgentRegistry(),
		controlOwner:   NewControlOwner(),
		liveLog:        NewLiveLogHub(),
		serialResolver: config.SerialResolver,
	}
	srv.routes()
	return srv
}

func (srv *Server) Handler() http.Handler {
	return srv
}

func (srv *Server) LiveLog() *LiveLogHub {
	return srv.liveLog
}

func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	srv.mux.ServeHTTP(w, r)
}

func (srv *Server) routes() {
	srv.mux.HandleFunc("GET /api/agents", srv.handleListAgents)
	srv.mux.HandleFunc("GET /api/channels", srv.handleListChannels)
	srv.mux.HandleFunc("GET /api/logs/download", srv.handleLogDownload)
	srv.mux.HandleFunc("GET /ws/agent", srv.handleAgentWebSocket)
	srv.mux.HandleFunc("GET /ws/logs", srv.handleLogWebSocket)
	srv.mux.HandleFunc("GET /ws/terminal/{channelID}", srv.handleTerminalWebSocket)
	srv.mux.HandleFunc("GET /ws/live-log/{channelID}", srv.handleLiveLogWebSocket)
	srv.mountStatic()
}
