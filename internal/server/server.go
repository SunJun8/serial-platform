package server

import (
	"net"
	"net/http"
	"time"

	"serial-platform/internal/serial"
	"serial-platform/internal/storage"
)

type ServerConfig struct {
	DB             *storage.DB
	LogDir         string
	LogSegmentSize int64
	SerialResolver func(channelID string) (serial.SerialControl, bool)
	RFC2217Listen  func(network, address string) (net.Listener, error)
}

type Server struct {
	db             *storage.DB
	logDir         string
	logSegmentSize int64
	mux            *http.ServeMux
	agentRegistry  *agentRegistry
	tunnels        *TunnelRegistry
	controlOwner   *ControlOwner
	terminalOps    *terminalOperationRegistry
	liveLog        *LiveLogHub
	serialResolver func(channelID string) (serial.SerialControl, bool)
	rfc2217Listen  func(network, address string) (net.Listener, error)
}

func New(config ServerConfig) *Server {
	rfc2217Listen := config.RFC2217Listen
	if rfc2217Listen == nil {
		rfc2217Listen = net.Listen
	}
	srv := &Server{
		db:             config.DB,
		logDir:         config.LogDir,
		logSegmentSize: config.LogSegmentSize,
		mux:            http.NewServeMux(),
		agentRegistry:  newAgentRegistry(),
		tunnels:        NewTunnelRegistry(5 * time.Second),
		controlOwner:   NewControlOwner(),
		terminalOps:    newTerminalOperationRegistry(),
		liveLog:        NewLiveLogHub(),
		serialResolver: config.SerialResolver,
		rfc2217Listen:  rfc2217Listen,
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
	srv.mux.HandleFunc("GET /ws/tunnel/{tunnelID}", srv.handleTunnelWebSocket)
	srv.mux.HandleFunc("GET /ws/terminal/{channelID}", srv.handleTerminalWebSocket)
	srv.mux.HandleFunc("GET /ws/live-log/{channelID}", srv.handleLiveLogWebSocket)
	srv.mountStatic()
}
