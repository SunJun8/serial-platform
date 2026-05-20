package protocol

type MessageType string

const (
	MessageAgentHello      MessageType = "agent_hello"
	MessageAgentAccepted   MessageType = "agent_accepted"
	MessageAgentPending    MessageType = "agent_pending"
	MessageHeartbeat       MessageType = "heartbeat"
	MessageChannelSnapshot MessageType = "channel_snapshot"
	MessageDeviceSnapshot  MessageType = "device_snapshot"
	MessageChannelStatus   MessageType = "channel_status"
	MessageChannelSync     MessageType = "channel_sync"
	MessageOpenTunnel      MessageType = "open_tunnel"
	MessageTunnelOpened    MessageType = "tunnel_opened"
	MessageTunnelError     MessageType = "tunnel_error"
	MessageTerminalOpen    MessageType = "terminal_open"
	MessageTerminalClose   MessageType = "terminal_close"
	MessageTerminalWrite   MessageType = "terminal_write"
	MessageSerialSetConfig MessageType = "serial_set_config"
	MessageSerialSetDTR    MessageType = "serial_set_dtr"
	MessageSerialSetRTS    MessageType = "serial_set_rts"
	MessageSerialSendBreak MessageType = "serial_send_break"
	MessageOperationResult MessageType = "operation_result"
)

type TunnelMode string

const (
	TunnelModeRFC2217  TunnelMode = "rfc2217"
	TunnelModeTerminal TunnelMode = "terminal"
)

type AgentHello struct {
	Type      MessageType `json:"type"`
	AgentID   string      `json:"agent_id"`
	Hostname  string      `json:"hostname"`
	Version   string      `json:"version"`
	OS        string      `json:"os"`
	Arch      string      `json:"arch"`
	MachineID string      `json:"machine_id"`
}

type AgentAccepted struct {
	Type   MessageType `json:"type"`
	Status string      `json:"status"`
}

type OpenTunnel struct {
	Type      MessageType `json:"type"`
	TunnelID  string      `json:"tunnel_id"`
	ChannelID string      `json:"channel_id"`
	Mode      TunnelMode  `json:"mode"`
}

type TunnelOpened struct {
	Type     MessageType `json:"type"`
	TunnelID string      `json:"tunnel_id"`
	Mode     TunnelMode  `json:"mode"`
}

type TunnelError struct {
	Type     MessageType `json:"type"`
	TunnelID string      `json:"tunnel_id"`
	Error    string      `json:"error"`
}

type DeviceIdentity struct {
	DevName      string `json:"dev_name"`
	IDPath       string `json:"id_path"`
	IDPathTag    string `json:"id_path_tag"`
	SysfsDevpath string `json:"sysfs_devpath"`
	Interface    string `json:"interface"`
	VID          string `json:"vid"`
	PID          string `json:"pid"`
	Serial       string `json:"serial"`
	Driver       string `json:"driver"`
	Manufacturer string `json:"manufacturer"`
	Product      string `json:"product"`
	PermissionOK bool   `json:"permission_ok"`
	ErrorMessage string `json:"error_message,omitempty"`
}

type DeviceSnapshot struct {
	Type    MessageType      `json:"type"`
	AgentID string           `json:"agent_id"`
	Devices []DeviceIdentity `json:"devices"`
}

type ChannelConfigMessage struct {
	ID              string `json:"id"`
	AgentID         string `json:"agent_id"`
	DevName         string `json:"dev_name"`
	IDPath          string `json:"id_path"`
	IDPathTag       string `json:"id_path_tag"`
	Status          string `json:"status"`
	DefaultBaud     int    `json:"default_baud"`
	DefaultDataBits int    `json:"default_data_bits"`
	DefaultParity   string `json:"default_parity"`
	DefaultStopBits int    `json:"default_stop_bits"`
	DefaultFlow     string `json:"default_flow"`
}

type ChannelSync struct {
	Type     MessageType            `json:"type"`
	Channels []ChannelConfigMessage `json:"channels"`
}

type ChannelRuntimeStatus struct {
	ChannelID    string `json:"channel_id"`
	Status       string `json:"status"`
	DevName      string `json:"dev_name"`
	ErrorMessage string `json:"error_message,omitempty"`
}

type ChannelStatusUpdate struct {
	Type     MessageType            `json:"type"`
	AgentID  string                 `json:"agent_id"`
	Statuses []ChannelRuntimeStatus `json:"statuses"`
}

type TerminalWrite struct {
	Type      MessageType `json:"type"`
	RequestID string      `json:"request_id,omitempty"`
	Data      []byte      `json:"data"`
}

type SerialSetConfig struct {
	Type      MessageType `json:"type"`
	RequestID string      `json:"request_id,omitempty"`
	Baud      int         `json:"baud"`
	DataBits  int         `json:"data_bits"`
	Parity    string      `json:"parity"`
	StopBits  int         `json:"stop_bits"`
	Flow      string      `json:"flow"`
}

type SerialSetDTR struct {
	Type      MessageType `json:"type"`
	RequestID string      `json:"request_id,omitempty"`
	Value     bool        `json:"value"`
}

type SerialSetRTS struct {
	Type      MessageType `json:"type"`
	RequestID string      `json:"request_id,omitempty"`
	Value     bool        `json:"value"`
}

type SerialSendBreak struct {
	Type       MessageType `json:"type"`
	RequestID  string      `json:"request_id,omitempty"`
	DurationMS int         `json:"duration_ms"`
}

type OperationResult struct {
	Type      MessageType `json:"type"`
	RequestID string      `json:"request_id"`
	OK        bool        `json:"ok"`
	Error     string      `json:"error,omitempty"`
}

type LiveLogFrame struct {
	ChannelID   string    `json:"channel_id"`
	Seq         uint64    `json:"seq"`
	TimestampNS int64     `json:"timestamp_ns"`
	Direction   Direction `json:"direction"`
	Flags       LogFlags  `json:"flags"`
	Payload     string    `json:"payload"`
}
