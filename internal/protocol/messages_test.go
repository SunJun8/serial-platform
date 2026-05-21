package protocol

import (
	"encoding/json"
	"testing"
)

func TestAgentHelloMessageJSON(t *testing.T) {
	msg := AgentHello{
		Type:      MessageAgentHello,
		AgentID:   "agent-1",
		Hostname:  "node-1",
		Version:   "dev",
		OS:        "linux",
		Arch:      "arm64",
		MachineID: "machine-1",
	}
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	var decoded AgentHello
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	if decoded.Type != MessageAgentHello {
		t.Fatalf("Type = %q, want %q", decoded.Type, MessageAgentHello)
	}
	if decoded.AgentID != "agent-1" {
		t.Fatalf("AgentID = %q", decoded.AgentID)
	}
}

func TestProtocolMessageStructsJSON(t *testing.T) {
	tests := []struct {
		name string
		msg  any
		want string
	}{
		{
			name: "agent accepted",
			msg: AgentAccepted{
				Type:   MessageAgentAccepted,
				Status: "pending",
			},
			want: `{"type":"agent_accepted","status":"pending"}`,
		},
		{
			name: "open tunnel",
			msg: OpenTunnel{
				Type:      MessageOpenTunnel,
				TunnelID:  "tunnel-1",
				ChannelID: "channel-1",
				Mode:      TunnelModeRFC2217,
			},
			want: `{"type":"open_tunnel","tunnel_id":"tunnel-1","channel_id":"channel-1","mode":"rfc2217"}`,
		},
		{
			name: "terminal write data",
			msg: TerminalWrite{
				Type:      MessageTerminalWrite,
				RequestID: "request-1",
				SessionID: "session-1",
				ChannelID: "channel-1",
				Data:      []byte("AT\r\n"),
			},
			want: `{"type":"terminal_write","request_id":"request-1","session_id":"session-1","channel_id":"channel-1","data":"QVQNCg=="}`,
		},
		{
			name: "terminal open",
			msg: TerminalOpen{
				Type:      MessageTerminalOpen,
				RequestID: "request-open",
				SessionID: "session-1",
				ChannelID: "channel-1",
			},
			want: `{"type":"terminal_open","request_id":"request-open","session_id":"session-1","channel_id":"channel-1"}`,
		},
		{
			name: "terminal close",
			msg: TerminalClose{
				Type:      MessageTerminalClose,
				RequestID: "request-close",
				SessionID: "session-1",
				ChannelID: "channel-1",
			},
			want: `{"type":"terminal_close","request_id":"request-close","session_id":"session-1","channel_id":"channel-1"}`,
		},
		{
			name: "serial config with session",
			msg: SerialSetConfig{
				Type:      MessageSerialSetConfig,
				RequestID: "request-2",
				SessionID: "session-1",
				ChannelID: "channel-1",
				Baud:      921600,
				DataBits:  8,
				Parity:    "N",
				StopBits:  1,
				Flow:      "none",
			},
			want: `{"type":"serial_set_config","request_id":"request-2","session_id":"session-1","channel_id":"channel-1","baud":921600,"data_bits":8,"parity":"N","stop_bits":1,"flow":"none"}`,
		},
		{
			name: "operation result",
			msg: OperationResult{
				Type:      MessageOperationResult,
				RequestID: "request-1",
				OK:        false,
				Error:     "denied",
			},
			want: `{"type":"operation_result","request_id":"request-1","ok":false,"error":"denied"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.msg)
			if err != nil {
				t.Fatalf("Marshal returned error: %v", err)
			}
			if string(data) != tt.want {
				t.Fatalf("JSON = %s, want %s", data, tt.want)
			}
		})
	}
}

func TestAgentControlMessagesRoundTrip(t *testing.T) {
	tests := []struct {
		name   string
		msg    any
		assert func(t *testing.T, data []byte)
	}{
		{
			name: "device snapshot",
			msg: DeviceSnapshot{
				Type:    MessageDeviceSnapshot,
				AgentID: "agent-1",
				Devices: []DeviceIdentity{
					{DevName: "/dev/ttyUSB0", IDPath: "id-path", PermissionOK: true},
				},
			},
			assert: func(t *testing.T, data []byte) {
				t.Helper()
				var decoded DeviceSnapshot
				if err := json.Unmarshal(data, &decoded); err != nil {
					t.Fatalf("Unmarshal(DeviceSnapshot) returned error: %v", err)
				}
				if decoded.Type != MessageDeviceSnapshot ||
					decoded.AgentID != "agent-1" ||
					len(decoded.Devices) != 1 ||
					decoded.Devices[0].DevName != "/dev/ttyUSB0" ||
					decoded.Devices[0].IDPath != "id-path" ||
					!decoded.Devices[0].PermissionOK {
					t.Fatalf("decoded DeviceSnapshot = %+v", decoded)
				}
			},
		},
		{
			name: "channel status",
			msg: ChannelStatusUpdate{
				Type:    MessageChannelStatus,
				AgentID: "agent-1",
				Statuses: []ChannelRuntimeStatus{
					{
						ChannelID:    "channel-1",
						Status:       "error",
						DevName:      "/dev/ttyUSB0",
						ErrorMessage: "permission denied",
					},
				},
			},
			assert: func(t *testing.T, data []byte) {
				t.Helper()
				var decoded ChannelStatusUpdate
				if err := json.Unmarshal(data, &decoded); err != nil {
					t.Fatalf("Unmarshal(ChannelStatusUpdate) returned error: %v", err)
				}
				if decoded.Type != MessageChannelStatus ||
					decoded.AgentID != "agent-1" ||
					len(decoded.Statuses) != 1 ||
					decoded.Statuses[0].ChannelID != "channel-1" ||
					decoded.Statuses[0].Status != "error" ||
					decoded.Statuses[0].DevName != "/dev/ttyUSB0" ||
					decoded.Statuses[0].ErrorMessage != "permission denied" {
					t.Fatalf("decoded ChannelStatusUpdate = %+v", decoded)
				}
			},
		},
		{
			name: "channel sync",
			msg: ChannelSync{
				Type: MessageChannelSync,
				Channels: []ChannelConfigMessage{
					{
						ID:              "channel-1",
						AgentID:         "agent-1",
						DevName:         "/dev/ttyUSB0",
						IDPath:          "id-path",
						Status:          "online",
						DefaultBaud:     115200,
						DefaultDataBits: 8,
						DefaultParity:   "N",
						DefaultStopBits: 1,
						DefaultFlow:     "none",
					},
				},
			},
			assert: func(t *testing.T, data []byte) {
				t.Helper()
				var decoded ChannelSync
				if err := json.Unmarshal(data, &decoded); err != nil {
					t.Fatalf("Unmarshal(ChannelSync) returned error: %v", err)
				}
				if decoded.Type != MessageChannelSync ||
					len(decoded.Channels) != 1 ||
					decoded.Channels[0].ID != "channel-1" ||
					decoded.Channels[0].AgentID != "agent-1" ||
					decoded.Channels[0].DevName != "/dev/ttyUSB0" ||
					decoded.Channels[0].IDPath != "id-path" ||
					decoded.Channels[0].Status != "online" ||
					decoded.Channels[0].DefaultBaud != 115200 ||
					decoded.Channels[0].DefaultDataBits != 8 ||
					decoded.Channels[0].DefaultParity != "N" ||
					decoded.Channels[0].DefaultStopBits != 1 ||
					decoded.Channels[0].DefaultFlow != "none" {
					t.Fatalf("decoded ChannelSync = %+v", decoded)
				}
			},
		},
		{
			name: "open tunnel",
			msg: OpenTunnel{
				Type:      MessageOpenTunnel,
				TunnelID:  "tunnel-1",
				ChannelID: "channel-1",
				Mode:      TunnelModeTerminal,
			},
			assert: func(t *testing.T, data []byte) {
				t.Helper()
				var decoded OpenTunnel
				if err := json.Unmarshal(data, &decoded); err != nil {
					t.Fatalf("Unmarshal(OpenTunnel) returned error: %v", err)
				}
				if decoded.Type != MessageOpenTunnel ||
					decoded.TunnelID != "tunnel-1" ||
					decoded.ChannelID != "channel-1" ||
					decoded.Mode != TunnelModeTerminal {
					t.Fatalf("decoded OpenTunnel = %+v", decoded)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.msg)
			if err != nil {
				t.Fatalf("Marshal(%T) returned error: %v", tt.msg, err)
			}
			tt.assert(t, data)
		})
	}
}
