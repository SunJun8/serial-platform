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
	srv.mux.HandleFunc("POST /api/agents/{agentID}/approve", srv.handleApproveAgent)
	srv.mux.HandleFunc("GET /api/channels", srv.handleListChannels)
	srv.mux.HandleFunc("POST /api/channels", srv.handleCreateChannel)
	srv.mux.HandleFunc("PATCH /api/channels/{channelID}", srv.handleUpdateChannel)
	srv.mux.HandleFunc("POST /api/channels/{channelID}/enable", srv.handleEnableChannel)
	srv.mux.HandleFunc("POST /api/channels/{channelID}/disable", srv.handleDisableChannel)
	srv.mux.HandleFunc("GET /api/candidates", srv.handleListCandidates)
	srv.mux.HandleFunc("POST /api/candidates/{candidateID}/confirm", srv.handleConfirmCandidate)
	srv.mux.HandleFunc("GET /api/logs/download", srv.handleLogDownload)
	srv.mux.HandleFunc("GET /ws/agent", srv.handleAgentWebSocket)
	srv.mux.HandleFunc("GET /ws/logs", srv.handleLogWebSocket)
	srv.mux.HandleFunc("GET /ws/terminal/{channelID}", srv.handleTerminalWebSocket)
	srv.mux.HandleFunc("GET /ws/live-log/{channelID}", srv.handleLiveLogWebSocket)
	srv.mountStatic()
}
