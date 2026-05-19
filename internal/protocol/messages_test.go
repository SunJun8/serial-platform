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
			},
			want: `{"type":"open_tunnel","tunnel_id":"tunnel-1","channel_id":"channel-1"}`,
		},
		{
			name: "terminal write data",
			msg: TerminalWrite{
				Type:      MessageTerminalWrite,
				RequestID: "request-1",
				Data:      []byte("AT\r\n"),
			},
			want: `{"type":"terminal_write","request_id":"request-1","data":"QVQNCg=="}`,
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
