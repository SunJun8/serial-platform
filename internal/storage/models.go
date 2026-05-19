package storage

import "time"

type AgentStatus string

const (
	AgentStatusPending AgentStatus = "pending"
	AgentStatusActive  AgentStatus = "active"
	AgentStatusOffline AgentStatus = "offline"
)

type ChannelStatus string

const (
	ChannelStatusOnline   ChannelStatus = "online"
	ChannelStatusOffline  ChannelStatus = "offline"
	ChannelStatusBusy     ChannelStatus = "busy"
	ChannelStatusDisabled ChannelStatus = "disabled"
)

type Agent struct {
	ID        string
	Name      string
	Status    AgentStatus
	Hostname  string
	OS        string
	Arch      string
	MachineID string
	UpdatedAt time.Time
}

type Channel struct {
	ID              string
	AgentID         string
	AutoName        string
	Alias           string
	Role            string
	IDPath          string
	IDPathTag       string
	SysfsDevpath    string
	RFC2217Port     int
	Status          ChannelStatus
	DefaultBaud     int
	DefaultDataBits int
	DefaultParity   string
	DefaultStopBits int
	UpdatedAt       time.Time
}
