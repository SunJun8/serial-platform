package protocol

type MessageType string

const (
	MessageAgentHello      MessageType = "agent_hello"
	MessageAgentAccepted   MessageType = "agent_accepted"
	MessageAgentPending    MessageType = "agent_pending"
	MessageHeartbeat       MessageType = "heartbeat"
	MessageChannelSnapshot MessageType = "channel_snapshot"
	MessageOpenTunnel      MessageType = "open_tunnel"
	MessageTerminalOpen    MessageType = "terminal_open"
	MessageTerminalClose   MessageType = "terminal_close"
	MessageTerminalWrite   MessageType = "terminal_write"
	MessageSerialSetConfig MessageType = "serial_set_config"
	MessageSerialSetDTR    MessageType = "serial_set_dtr"
	MessageSerialSetRTS    MessageType = "serial_set_rts"
	MessageSerialSendBreak MessageType = "serial_send_break"
	MessageOperationResult MessageType = "operation_result"
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
