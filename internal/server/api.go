package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"serial-platform/internal/storage"
)

type channelCreateRequest struct {
	AgentID         string `json:"agent_id"`
	Alias           string `json:"alias"`
	Role            string `json:"role"`
	DevName         string `json:"dev_name"`
	IDPath          string `json:"id_path"`
	IDPathTag       string `json:"id_path_tag"`
	SysfsDevpath    string `json:"sysfs_devpath"`
	Interface       string `json:"interface"`
	RFC2217Port     int    `json:"rfc2217_port"`
	DefaultBaud     *int   `json:"default_baud"`
	DefaultDataBits *int   `json:"default_data_bits"`
	DefaultParity   string `json:"default_parity"`
	DefaultStopBits *int   `json:"default_stop_bits"`
	DefaultFlow     string `json:"default_flow"`
}

type channelPatchRequest struct {
	AgentID         *string `json:"agent_id"`
	Alias           *string `json:"alias"`
	Role            *string `json:"role"`
	DevName         *string `json:"dev_name"`
	IDPath          *string `json:"id_path"`
	IDPathTag       *string `json:"id_path_tag"`
	SysfsDevpath    *string `json:"sysfs_devpath"`
	Interface       *string `json:"interface"`
	RFC2217Port     *int    `json:"rfc2217_port"`
	DefaultBaud     *int    `json:"default_baud"`
	DefaultDataBits *int    `json:"default_data_bits"`
	DefaultParity   *string `json:"default_parity"`
	DefaultStopBits *int    `json:"default_stop_bits"`
	DefaultFlow     *string `json:"default_flow"`
}

type candidateConfirmRequest struct {
	Alias           string `json:"alias"`
	Role            string `json:"role"`
	RFC2217Port     int    `json:"rfc2217_port"`
	DefaultBaud     *int   `json:"default_baud"`
	DefaultDataBits *int   `json:"default_data_bits"`
	DefaultParity   string `json:"default_parity"`
	DefaultStopBits *int   `json:"default_stop_bits"`
	DefaultFlow     string `json:"default_flow"`
}

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

func (srv *Server) handleCreateChannel(w http.ResponseWriter, r *http.Request) {
	var req channelCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeBadRequest(w, err.Error())
		return
	}
	if req.AgentID == "" {
		writeBadRequest(w, "agent_id is required")
		return
	}
	if req.RFC2217Port <= 0 {
		writeBadRequest(w, "rfc2217_port is required")
		return
	}

	baud, dataBits, parity, stopBits, flow, err := validateSerialConfig(
		req.RFC2217Port,
		req.DefaultBaud,
		req.DefaultDataBits,
		req.DefaultParity,
		req.DefaultStopBits,
		req.DefaultFlow,
	)
	if err != nil {
		writeBadRequest(w, err.Error())
		return
	}
	channel := storage.Channel{
		ID:              uuid.NewString(),
		AgentID:         req.AgentID,
		AutoName:        channelAutoName(req.AgentID, req.Interface),
		Alias:           req.Alias,
		Role:            defaultString(req.Role, "console"),
		DevName:         req.DevName,
		IDPath:          req.IDPath,
		IDPathTag:       req.IDPathTag,
		SysfsDevpath:    req.SysfsDevpath,
		RFC2217Port:     req.RFC2217Port,
		Status:          storage.ChannelStatusOffline,
		DefaultBaud:     baud,
		DefaultDataBits: dataBits,
		DefaultParity:   parity,
		DefaultStopBits: stopBits,
		DefaultFlow:     flow,
		UpdatedAt:       time.Now().UTC(),
	}
	if err := srv.db.UpsertChannel(channel); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, channel)
}

func (srv *Server) handleUpdateChannel(w http.ResponseWriter, r *http.Request) {
	channelID := r.PathValue("channelID")
	if channelID == "" {
		writeBadRequest(w, "channel id is required")
		return
	}

	var req channelPatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeBadRequest(w, err.Error())
		return
	}

	channel, err := srv.db.GetChannel(channelID)
	if errors.Is(err, storage.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "channel not found"})
		return
	}
	if err != nil {
		writeError(w, err)
		return
	}

	interfaceName := existingAutoNameInterface(channel.AutoName)
	if req.AgentID != nil {
		channel.AgentID = *req.AgentID
	}
	if req.Interface != nil {
		interfaceName = *req.Interface
	}
	if req.AgentID != nil || req.Interface != nil {
		channel.AutoName = channelAutoName(channel.AgentID, interfaceName)
	}
	if req.Alias != nil {
		channel.Alias = *req.Alias
	}
	if req.Role != nil {
		channel.Role = *req.Role
	}
	if req.DevName != nil {
		channel.DevName = *req.DevName
	}
	if req.IDPath != nil {
		channel.IDPath = *req.IDPath
	}
	if req.IDPathTag != nil {
		channel.IDPathTag = *req.IDPathTag
	}
	if req.SysfsDevpath != nil {
		channel.SysfsDevpath = *req.SysfsDevpath
	}
	if req.RFC2217Port != nil {
		channel.RFC2217Port = *req.RFC2217Port
	}
	if req.DefaultBaud != nil {
		channel.DefaultBaud = *req.DefaultBaud
	}
	if req.DefaultDataBits != nil {
		channel.DefaultDataBits = *req.DefaultDataBits
	}
	if req.DefaultParity != nil {
		channel.DefaultParity = *req.DefaultParity
	}
	if req.DefaultStopBits != nil {
		channel.DefaultStopBits = *req.DefaultStopBits
	}
	if req.DefaultFlow != nil {
		channel.DefaultFlow = *req.DefaultFlow
	}
	if err := validateChannelConfig(channel); err != nil {
		writeBadRequest(w, err.Error())
		return
	}
	channel.UpdatedAt = time.Now().UTC()

	if err := srv.db.UpsertChannel(channel); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, channel)
}

func (srv *Server) handleDeleteChannel(w http.ResponseWriter, r *http.Request) {
	channelID := r.PathValue("channelID")
	if channelID == "" {
		writeBadRequest(w, "channel id is required")
		return
	}
	if err := srv.controlOwner.Acquire(channelID, "delete"); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "channel is busy"})
		return
	}
	defer srv.controlOwner.Release(channelID, "delete")

	if _, err := srv.db.GetChannel(channelID); errors.Is(err, storage.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "channel not found"})
		return
	} else if err != nil {
		writeError(w, err)
		return
	}
	segments, err := srv.db.ListLogSegmentsForChannel(channelID)
	if err != nil {
		writeError(w, err)
		return
	}
	if err := srv.deleteChannelLogFiles(segments); err != nil {
		writeError(w, err)
		return
	}
	if err := srv.db.DeleteChannelWithLogSegments(channelID); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "channel not found"})
			return
		}
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (srv *Server) handleEnableChannel(w http.ResponseWriter, r *http.Request) {
	srv.updateChannelStatus(w, r, storage.ChannelStatusOffline)
}

func (srv *Server) handleDisableChannel(w http.ResponseWriter, r *http.Request) {
	srv.updateChannelStatus(w, r, storage.ChannelStatusDisabled)
}

func (srv *Server) updateChannelStatus(w http.ResponseWriter, r *http.Request, status storage.ChannelStatus) {
	channelID := r.PathValue("channelID")
	if channelID == "" {
		writeBadRequest(w, "channel id is required")
		return
	}
	channel, err := srv.db.GetChannel(channelID)
	if errors.Is(err, storage.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "channel not found"})
		return
	}
	if err != nil {
		writeError(w, err)
		return
	}
	if err := srv.db.UpdateChannelStatus(channelID, status, channel.DevName, "", time.Now().UTC()); err != nil {
		writeError(w, err)
		return
	}
	channel, err = srv.db.GetChannel(channelID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, channel)
}

func (srv *Server) handleListCandidates(w http.ResponseWriter, r *http.Request) {
	candidates, err := srv.db.ListCandidates()
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, candidates)
}

func (srv *Server) handleConfirmCandidate(w http.ResponseWriter, r *http.Request) {
	candidateID := r.PathValue("candidateID")
	if candidateID == "" {
		writeBadRequest(w, "candidate id is required")
		return
	}
	var req candidateConfirmRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeBadRequest(w, err.Error())
		return
	}
	if req.RFC2217Port <= 0 {
		writeBadRequest(w, "rfc2217_port is required")
		return
	}

	baud, dataBits, parity, stopBits, flow, err := validateSerialConfig(
		req.RFC2217Port,
		req.DefaultBaud,
		req.DefaultDataBits,
		req.DefaultParity,
		req.DefaultStopBits,
		req.DefaultFlow,
	)
	if err != nil {
		writeBadRequest(w, err.Error())
		return
	}

	channel, err := srv.db.ConfirmCandidate(candidateID, func(candidate storage.Candidate) storage.Channel {
		return storage.Channel{
			ID:              uuid.NewString(),
			AgentID:         candidate.AgentID,
			AutoName:        channelAutoName(candidate.AgentID, candidate.Interface),
			Alias:           req.Alias,
			Role:            defaultString(req.Role, "console"),
			DevName:         candidate.DevName,
			IDPath:          candidate.IDPath,
			IDPathTag:       candidate.IDPathTag,
			SysfsDevpath:    candidate.SysfsDevpath,
			RFC2217Port:     req.RFC2217Port,
			Status:          storage.ChannelStatusOffline,
			DefaultBaud:     baud,
			DefaultDataBits: dataBits,
			DefaultParity:   parity,
			DefaultStopBits: stopBits,
			DefaultFlow:     flow,
			UpdatedAt:       time.Now().UTC(),
		}
	})
	if errors.Is(err, storage.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "candidate not found"})
		return
	}
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, channel)
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

func channelAutoName(agentID string, interfaceName string) string {
	return agentID + "." + normalizeInterface(interfaceName)
}

func existingAutoNameInterface(autoName string) string {
	if idx := strings.LastIndex(autoName, "."); idx >= 0 && idx+1 < len(autoName) {
		return autoName[idx+1:]
	}
	return "if00"
}

func normalizeInterface(interfaceName string) string {
	if interfaceName == "" {
		return "if00"
	}
	if len(interfaceName) >= 2 && interfaceName[:2] == "if" {
		return interfaceName
	}
	return "if" + interfaceName
}

func validateSerialConfig(rfc2217Port int, baud, dataBits *int, parity string, stopBits *int, flow string) (int, int, string, int, string, error) {
	defaultBaud, defaultDataBits, defaultParity, defaultStopBits, defaultFlow := serialConfigDefaults(baud, dataBits, parity, stopBits, flow)
	if err := validateChannelConfig(storage.Channel{
		RFC2217Port:     rfc2217Port,
		DefaultBaud:     defaultBaud,
		DefaultDataBits: defaultDataBits,
		DefaultParity:   defaultParity,
		DefaultStopBits: defaultStopBits,
		DefaultFlow:     defaultFlow,
	}); err != nil {
		return 0, 0, "", 0, "", err
	}
	return defaultBaud, defaultDataBits, defaultParity, defaultStopBits, defaultFlow, nil
}

func validateChannelConfig(channel storage.Channel) error {
	if channel.RFC2217Port < 1 || channel.RFC2217Port > 65535 {
		return fmt.Errorf("rfc2217_port must be between 1 and 65535")
	}
	if channel.DefaultBaud < 1 {
		return fmt.Errorf("default_baud must be greater than 0")
	}
	if channel.DefaultDataBits < 5 || channel.DefaultDataBits > 8 {
		return fmt.Errorf("default_data_bits must be between 5 and 8")
	}
	switch channel.DefaultParity {
	case "N", "E", "O":
	default:
		return fmt.Errorf("default_parity must be one of N, E, O")
	}
	if channel.DefaultStopBits != 1 && channel.DefaultStopBits != 2 {
		return fmt.Errorf("default_stop_bits must be 1 or 2")
	}
	if channel.DefaultFlow != "none" {
		return fmt.Errorf("default_flow must be none")
	}
	return nil
}

func serialConfigDefaults(baud, dataBits *int, parity string, stopBits *int, flow string) (int, int, string, int, string) {
	return defaultOptionalInt(baud, 115200),
		defaultOptionalInt(dataBits, 8),
		defaultString(parity, "N"),
		defaultOptionalInt(stopBits, 1),
		defaultString(flow, "none")
}

func defaultOptionalInt(value *int, fallback int) int {
	if value == nil {
		return fallback
	}
	return *value
}

func defaultString(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, err error) {
	if errors.Is(err, storage.ErrConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]string{
		"error": err.Error(),
	})
}
