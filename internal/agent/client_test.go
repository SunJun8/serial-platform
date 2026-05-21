package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"serial-platform/internal/agent"
	"serial-platform/internal/protocol"
	"serial-platform/internal/serial"
)

func TestClientConnectKeepsConnectionOpenUntilClose(t *testing.T) {
	helloReceived := make(chan protocol.AgentHello, 1)
	connClosed := make(chan struct{})

	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ws/agent" {
			http.NotFound(w, r)
			return
		}

		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		var hello protocol.AgentHello
		if err := protocol.ReadJSON(r.Context(), conn, &hello); err != nil {
			return
		}
		helloReceived <- hello

		if err := protocol.WriteJSON(r.Context(), conn, protocol.AgentAccepted{
			Type:   protocol.MessageAgentAccepted,
			Status: "pending",
		}); err != nil {
			return
		}

		_, _, _ = conn.Read(r.Context())
		close(connClosed)
	}))
	t.Cleanup(httpSrv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	client := &agent.Client{Config: agent.Config{
		ServerURL: httpSrv.URL,
		AgentID:   "agent-1",
	}}
	status, err := client.Connect(ctx)
	if err != nil {
		t.Fatalf("Connect returned error: %v", err)
	}
	if status != "pending" {
		t.Fatalf("status = %q, want pending", status)
	}

	select {
	case <-connClosed:
		t.Fatal("connection closed before explicit Close")
	case <-time.After(100 * time.Millisecond):
	}

	if err := client.Close(ctx); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	select {
	case <-connClosed:
	case <-ctx.Done():
		t.Fatal("server did not observe client close")
	}

	hello := <-helloReceived
	if hello.Type != protocol.MessageAgentHello {
		t.Fatalf("hello.Type = %q, want %q", hello.Type, protocol.MessageAgentHello)
	}
	if hello.AgentID != "agent-1" {
		t.Fatalf("hello.AgentID = %q, want agent-1", hello.AgentID)
	}
	if hello.Hostname == "" {
		t.Fatal("hello.Hostname is empty")
	}
	if hello.OS != runtime.GOOS {
		t.Fatalf("hello.OS = %q, want %q", hello.OS, runtime.GOOS)
	}
	if hello.Arch != runtime.GOARCH {
		t.Fatalf("hello.Arch = %q, want %q", hello.Arch, runtime.GOARCH)
	}
}

func TestClientSendAndReadControlMessages(t *testing.T) {
	receivedSnapshot := make(chan protocol.DeviceSnapshot, 1)

	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ws/agent" {
			http.NotFound(w, r)
			return
		}

		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		var hello protocol.AgentHello
		if err := protocol.ReadJSON(r.Context(), conn, &hello); err != nil {
			return
		}
		if err := protocol.WriteJSON(r.Context(), conn, protocol.AgentAccepted{
			Type:   protocol.MessageAgentAccepted,
			Status: "active",
		}); err != nil {
			return
		}

		var snapshot protocol.DeviceSnapshot
		if err := protocol.ReadJSON(r.Context(), conn, &snapshot); err != nil {
			return
		}
		receivedSnapshot <- snapshot

		_ = protocol.WriteJSON(r.Context(), conn, protocol.ChannelSync{
			Type: protocol.MessageChannelSync,
			Channels: []protocol.ChannelConfigMessage{
				{ID: "channel-1", AgentID: hello.AgentID, IDPath: "id-path", DefaultBaud: 115200},
			},
		})
	}))
	t.Cleanup(httpSrv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	client := &agent.Client{Config: agent.Config{
		ServerURL: httpSrv.URL,
		AgentID:   "agent-1",
	}}
	status, err := client.Connect(ctx)
	if err != nil {
		t.Fatalf("Connect returned error: %v", err)
	}
	if status != "active" {
		t.Fatalf("status = %q, want active", status)
	}
	t.Cleanup(func() { _ = client.Close(context.Background()) })

	err = client.SendControl(ctx, protocol.DeviceSnapshot{
		Type:    protocol.MessageDeviceSnapshot,
		AgentID: "agent-1",
		Devices: []protocol.DeviceIdentity{
			{DevName: "/dev/ttyUSB0", IDPath: "id-path"},
		},
	})
	if err != nil {
		t.Fatalf("SendControl returned error: %v", err)
	}

	select {
	case snapshot := <-receivedSnapshot:
		if snapshot.Type != protocol.MessageDeviceSnapshot || len(snapshot.Devices) != 1 || snapshot.Devices[0].IDPath != "id-path" {
			t.Fatalf("snapshot = %+v", snapshot)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for snapshot")
	}

	messageType, raw, err := client.ReadControl(ctx)
	if err != nil {
		t.Fatalf("ReadControl returned error: %v", err)
	}
	if messageType != protocol.MessageChannelSync {
		t.Fatalf("messageType = %q, want %q", messageType, protocol.MessageChannelSync)
	}
	var syncMessage protocol.ChannelSync
	if err := json.Unmarshal(raw, &syncMessage); err != nil {
		t.Fatalf("Unmarshal raw returned error: %v", err)
	}
	if len(syncMessage.Channels) != 1 || syncMessage.Channels[0].ID != "channel-1" {
		t.Fatalf("syncMessage = %+v", syncMessage)
	}
}

func TestClientHandlesRFC2217OpenTunnelWithResolvedControl(t *testing.T) {
	control := newAgentRFC2217FakeControl()
	controlMessages := make(chan protocol.MessageType, 4)

	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ws/agent":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "")

			var hello protocol.AgentHello
			if err := protocol.ReadJSON(r.Context(), conn, &hello); err != nil {
				return
			}
			if err := protocol.WriteJSON(r.Context(), conn, protocol.AgentAccepted{
				Type:   protocol.MessageAgentAccepted,
				Status: "active",
			}); err != nil {
				return
			}
			if err := protocol.WriteJSON(r.Context(), conn, protocol.OpenTunnel{
				Type:      protocol.MessageOpenTunnel,
				TunnelID:  "tunnel-1",
				ChannelID: "channel-1",
				Mode:      protocol.TunnelModeRFC2217,
			}); err != nil {
				return
			}
			for {
				messageType, data, err := conn.Read(r.Context())
				if err != nil {
					return
				}
				if messageType == websocket.MessageText {
					var envelope struct {
						Type protocol.MessageType `json:"type"`
					}
					if err := json.Unmarshal(data, &envelope); err == nil {
						controlMessages <- envelope.Type
					}
				}
			}
		case "/ws/tunnel/tunnel-1":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "")
			_ = conn.Write(r.Context(), websocket.MessageBinary, []byte("AT\r"))
			control.session.waitForWrite(t, []byte("AT\r"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(httpSrv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	client := &agent.Client{Config: agent.Config{
		ServerURL: httpSrv.URL,
		AgentID:   "agent-1",
	}}
	if _, err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect returned error: %v", err)
	}
	t.Cleanup(func() { _ = client.Close(context.Background()) })

	done := make(chan error, 1)
	go func() {
		done <- client.HandleControlMessages(ctx, agentRFC2217Resolver{control: control}, agent.TunnelDialer{ServerURL: httpSrv.URL})
	}()

	control.session.waitForWrite(t, []byte("AT\r"))
	select {
	case messageType := <-controlMessages:
		if messageType != protocol.MessageTunnelOpened {
			t.Fatalf("control message type = %q, want tunnel_opened", messageType)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for tunnel_opened")
	}

	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("HandleControlMessages returned %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("HandleControlMessages did not return after context cancellation")
	}
}

func TestClientHandlesTerminalOpenWriteAndClose(t *testing.T) {
	control := newAgentRFC2217FakeControl()
	results := make(chan protocol.OperationResult, 3)

	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ws/agent" {
			http.NotFound(w, r)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		var hello protocol.AgentHello
		if err := protocol.ReadJSON(r.Context(), conn, &hello); err != nil {
			return
		}
		if err := protocol.WriteJSON(r.Context(), conn, protocol.AgentAccepted{
			Type:   protocol.MessageAgentAccepted,
			Status: "active",
		}); err != nil {
			return
		}
		if err := protocol.WriteJSON(r.Context(), conn, protocol.TerminalOpen{
			Type:      protocol.MessageTerminalOpen,
			RequestID: "request-open",
			SessionID: "session-1",
			ChannelID: "channel-1",
		}); err != nil {
			return
		}
		results <- readAgentOperationResult(t, r.Context(), conn)
		if err := protocol.WriteJSON(r.Context(), conn, protocol.TerminalWrite{
			Type:      protocol.MessageTerminalWrite,
			RequestID: "request-write",
			SessionID: "session-1",
			ChannelID: "channel-1",
			Data:      []byte("AT\r"),
		}); err != nil {
			return
		}
		results <- readAgentOperationResult(t, r.Context(), conn)
		if err := protocol.WriteJSON(r.Context(), conn, protocol.TerminalClose{
			Type:      protocol.MessageTerminalClose,
			RequestID: "request-close",
			SessionID: "session-1",
			ChannelID: "channel-1",
		}); err != nil {
			return
		}
		results <- readAgentOperationResult(t, r.Context(), conn)
		<-r.Context().Done()
	}))
	t.Cleanup(httpSrv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	client := &agent.Client{Config: agent.Config{
		ServerURL: httpSrv.URL,
		AgentID:   "agent-1",
	}}
	if _, err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect returned error: %v", err)
	}
	t.Cleanup(func() { _ = client.Close(context.Background()) })

	done := make(chan error, 1)
	go func() {
		done <- client.HandleControlMessages(ctx, agentRFC2217Resolver{control: control}, agent.TunnelDialer{ServerURL: httpSrv.URL})
	}()

	openResult := receiveAgentOperationResult(t, ctx, results)
	if !openResult.OK || openResult.RequestID != "request-open" {
		t.Fatalf("open result = %+v, want OK for request-open", openResult)
	}
	control.session.waitForOpen(t)
	if owner := control.session.owner(); owner != "web" {
		t.Fatalf("control session owner = %q, want web", owner)
	}

	control.session.waitForWrite(t, []byte("AT\r"))
	writeResult := receiveAgentOperationResult(t, ctx, results)
	if !writeResult.OK || writeResult.RequestID != "request-write" {
		t.Fatalf("write result = %+v, want OK for request-write", writeResult)
	}

	closeResult := receiveAgentOperationResult(t, ctx, results)
	if !closeResult.OK || closeResult.RequestID != "request-close" {
		t.Fatalf("close result = %+v, want OK for request-close", closeResult)
	}
	control.session.waitForClose(t)

	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("HandleControlMessages returned %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("HandleControlMessages did not return after context cancellation")
	}
}

func TestClientHandlesTerminalSerialControls(t *testing.T) {
	control := newAgentRFC2217FakeControl()
	results := make(chan protocol.OperationResult, 5)

	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ws/agent" {
			http.NotFound(w, r)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		var hello protocol.AgentHello
		if err := protocol.ReadJSON(r.Context(), conn, &hello); err != nil {
			return
		}
		if err := protocol.WriteJSON(r.Context(), conn, protocol.AgentAccepted{
			Type:   protocol.MessageAgentAccepted,
			Status: "active",
		}); err != nil {
			return
		}
		messages := []any{
			protocol.TerminalOpen{
				Type:      protocol.MessageTerminalOpen,
				RequestID: "request-open",
				SessionID: "session-1",
				ChannelID: "channel-1",
			},
			protocol.SerialSetConfig{
				Type:      protocol.MessageSerialSetConfig,
				RequestID: "request-config",
				SessionID: "session-1",
				ChannelID: "channel-1",
				Baud:      921600,
				DataBits:  7,
				Parity:    "E",
				StopBits:  2,
				Flow:      "rtscts",
			},
			protocol.SerialSetDTR{
				Type:      protocol.MessageSerialSetDTR,
				RequestID: "request-dtr",
				SessionID: "session-1",
				ChannelID: "channel-1",
				Value:     true,
			},
			protocol.SerialSetRTS{
				Type:      protocol.MessageSerialSetRTS,
				RequestID: "request-rts",
				SessionID: "session-1",
				ChannelID: "channel-1",
				Value:     false,
			},
			protocol.SerialSendBreak{
				Type:       protocol.MessageSerialSendBreak,
				RequestID:  "request-break",
				SessionID:  "session-1",
				ChannelID:  "channel-1",
				DurationMS: 25,
			},
		}
		for _, message := range messages {
			if err := protocol.WriteJSON(r.Context(), conn, message); err != nil {
				return
			}
			results <- readAgentOperationResult(t, r.Context(), conn)
		}
		<-r.Context().Done()
	}))
	t.Cleanup(httpSrv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	client := &agent.Client{Config: agent.Config{
		ServerURL: httpSrv.URL,
		AgentID:   "agent-1",
	}}
	if _, err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect returned error: %v", err)
	}
	t.Cleanup(func() { _ = client.Close(context.Background()) })

	done := make(chan error, 1)
	go func() {
		done <- client.HandleControlMessages(ctx, agentRFC2217Resolver{control: control}, agent.TunnelDialer{ServerURL: httpSrv.URL})
	}()

	for _, requestID := range []string{"request-open", "request-config", "request-dtr", "request-rts", "request-break"} {
		result := receiveAgentOperationResult(t, ctx, results)
		if !result.OK || result.RequestID != requestID {
			t.Fatalf("operation result = %+v, want OK for %q", result, requestID)
		}
	}
	control.session.waitForConfig(t, serial.Config{Baud: 921600, DataBits: 7, Parity: "E", StopBits: 2, Flow: "rtscts"})
	control.session.waitForDTR(t, true)
	control.session.waitForRTS(t, false)
	control.session.waitForBreak(t, 25*time.Millisecond)

	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("HandleControlMessages returned %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("HandleControlMessages did not return after context cancellation")
	}
}

func TestClientClosesTerminalSessionOnControlConnectionLoss(t *testing.T) {
	control := newAgentRFC2217FakeControl()
	opened := make(chan struct{})

	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ws/agent" {
			http.NotFound(w, r)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()

		var hello protocol.AgentHello
		if err := protocol.ReadJSON(r.Context(), conn, &hello); err != nil {
			return
		}
		if err := protocol.WriteJSON(r.Context(), conn, protocol.AgentAccepted{
			Type:   protocol.MessageAgentAccepted,
			Status: "active",
		}); err != nil {
			return
		}
		if err := protocol.WriteJSON(r.Context(), conn, protocol.TerminalOpen{
			Type:      protocol.MessageTerminalOpen,
			SessionID: "session-1",
			ChannelID: "channel-1",
		}); err != nil {
			return
		}
		result := readAgentOperationResult(t, r.Context(), conn)
		if !result.OK {
			t.Errorf("open result = %+v, want OK", result)
			return
		}
		close(opened)
	}))
	t.Cleanup(httpSrv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	client := &agent.Client{Config: agent.Config{
		ServerURL: httpSrv.URL,
		AgentID:   "agent-1",
	}}
	if _, err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect returned error: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- client.HandleControlMessages(ctx, agentRFC2217Resolver{control: control}, agent.TunnelDialer{ServerURL: httpSrv.URL})
	}()

	select {
	case <-opened:
	case <-ctx.Done():
		t.Fatal("timeout waiting for terminal session open")
	}
	control.session.waitForClose(t)
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("HandleControlMessages returned nil error, want closed connection error")
		}
	case <-ctx.Done():
		t.Fatal("HandleControlMessages did not return after control connection closed")
	}
}

func TestClientFetchChannelConfigsFiltersAgentAndConvertsSerialDefaults(t *testing.T) {
	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/channels" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode([]map[string]any{
			{
				"ID":              "channel-1",
				"AgentID":         "agent-1",
				"DevName":         "/dev/ttyUSB0",
				"IDPath":          "id-path-1",
				"IDPathTag":       "id-path-tag-1",
				"Status":          "offline",
				"DefaultBaud":     921600,
				"DefaultDataBits": 7,
				"DefaultParity":   "E",
				"DefaultStopBits": 2,
				"DefaultFlow":     "none",
			},
			{
				"ID":              "channel-2",
				"AgentID":         "agent-2",
				"DevName":         "/dev/ttyUSB1",
				"IDPath":          "id-path-2",
				"Status":          "offline",
				"DefaultBaud":     115200,
				"DefaultDataBits": 8,
				"DefaultParity":   "N",
				"DefaultStopBits": 1,
				"DefaultFlow":     "none",
			},
		}); err != nil {
			t.Errorf("encode channels: %v", err)
		}
	}))
	t.Cleanup(httpSrv.Close)

	client := &agent.Client{Config: agent.Config{
		ServerURL: httpSrv.URL,
		AgentID:   "agent-1",
	}}

	channels, err := client.FetchChannelConfigs(context.Background())
	if err != nil {
		t.Fatalf("FetchChannelConfigs returned error: %v", err)
	}
	if len(channels) != 1 {
		t.Fatalf("len(channels) = %d, want 1: %+v", len(channels), channels)
	}
	got := channels[0]
	if got.ID != "channel-1" || got.AgentID != "agent-1" || got.DevName != "/dev/ttyUSB0" || got.IDPath != "id-path-1" || got.IDPathTag != "id-path-tag-1" || got.Status != "offline" {
		t.Fatalf("channel = %+v, want channel-1 fields for agent-1", got)
	}
	wantConfig := serial.Config{Baud: 921600, DataBits: 7, Parity: "E", StopBits: 2, Flow: "none"}
	if got.DefaultConfig != wantConfig {
		t.Fatalf("DefaultConfig = %+v, want %+v", got.DefaultConfig, wantConfig)
	}
}

func TestClientSendLogFramesWritesBinaryFrames(t *testing.T) {
	received := make(chan protocol.LogFrame, 1)

	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ws/logs" {
			http.NotFound(w, r)
			return
		}

		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		messageType, payload, err := conn.Read(r.Context())
		if err != nil {
			return
		}
		if messageType != websocket.MessageBinary {
			return
		}
		frame, err := protocol.DecodeLogFrame(payload)
		if err != nil {
			return
		}
		received <- frame
	}))
	t.Cleanup(httpSrv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	frames := make(chan protocol.LogFrame, 1)
	frames <- protocol.LogFrame{
		ChannelID:   "channel-1",
		Seq:         1,
		TimestampNS: time.Unix(1700000000, 0).UnixNano(),
		Direction:   protocol.DirectionRX,
		Flags:       protocol.FlagRaw,
		Payload:     []byte("boot\n"),
	}
	close(frames)

	client := &agent.Client{Config: agent.Config{ServerURL: httpSrv.URL}}
	if err := client.SendLogFrames(ctx, frames); err != nil {
		t.Fatalf("SendLogFrames returned error: %v", err)
	}

	select {
	case frame := <-received:
		if frame.ChannelID != "channel-1" {
			t.Fatalf("ChannelID = %q, want channel-1", frame.ChannelID)
		}
		if string(frame.Payload) != "boot\n" {
			t.Fatalf("Payload = %q, want boot newline", frame.Payload)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for log frame")
	}
}

func TestClientSendLogFramesReturnsCloseErrorWhenNoFramesSent(t *testing.T) {
	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ws/logs" {
			http.NotFound(w, r)
			return
		}

		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		conn.CloseNow()
	}))
	t.Cleanup(httpSrv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	frames := make(chan protocol.LogFrame)
	close(frames)

	client := &agent.Client{Config: agent.Config{ServerURL: httpSrv.URL}}
	if err := client.SendLogFrames(ctx, frames); err == nil {
		t.Fatal("SendLogFrames returned nil error, want close error")
	}
}

func TestClientSendLogFramesLoopReconnectsAfterDisconnect(t *testing.T) {
	received := make(chan protocol.LogFrame, 2)
	firstDisconnected := make(chan struct{})

	var mu sync.Mutex
	connections := 0
	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ws/logs" {
			http.NotFound(w, r)
			return
		}

		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		mu.Lock()
		connections++
		connection := connections
		mu.Unlock()

		messageType, payload, err := conn.Read(r.Context())
		if err != nil {
			return
		}
		if messageType != websocket.MessageBinary {
			return
		}
		frame, err := protocol.DecodeLogFrame(payload)
		if err != nil {
			return
		}
		received <- frame
		if connection == 1 {
			close(firstDisconnected)
			conn.CloseNow()
			return
		}
	}))
	t.Cleanup(httpSrv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	frames := make(chan protocol.LogFrame, 2)
	client := &agent.Client{Config: agent.Config{ServerURL: httpSrv.URL}}
	done := make(chan error, 1)
	go func() {
		done <- client.SendLogFramesLoop(ctx, frames, time.Millisecond)
	}()

	frames <- testLogFrame(1, "first\n")
	select {
	case <-firstDisconnected:
	case <-ctx.Done():
		t.Fatal("timeout waiting for first log connection to disconnect")
	}
	frames <- testLogFrame(2, "second\n")
	close(frames)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("SendLogFramesLoop returned error: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for SendLogFramesLoop to finish")
	}

	gotFirst := receiveLogFrame(t, ctx, received)
	gotSecond := receiveLogFrame(t, ctx, received)
	if gotFirst.Seq != 1 || string(gotFirst.Payload) != "first\n" {
		t.Fatalf("first received frame = %+v, want seq 1 payload first", gotFirst)
	}
	if gotSecond.Seq != 2 || string(gotSecond.Payload) != "second\n" {
		t.Fatalf("second received frame = %+v, want seq 2 payload second", gotSecond)
	}
}

func TestClientSendLogFramesLoopStopsWhenFramesCloseBeforeReconnect(t *testing.T) {
	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ws/logs" {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "temporary outage", http.StatusServiceUnavailable)
	}))
	t.Cleanup(httpSrv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	frames := make(chan protocol.LogFrame)
	close(frames)

	client := &agent.Client{Config: agent.Config{ServerURL: httpSrv.URL}}
	if err := client.SendLogFramesLoop(ctx, frames, time.Millisecond); err != nil {
		t.Fatalf("SendLogFramesLoop returned error: %v", err)
	}
}

func TestClientSendLogFramesLoopDrainsFramesDuringOutage(t *testing.T) {
	received := make(chan protocol.LogFrame, 1)

	var mu sync.Mutex
	acceptLogs := false
	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ws/logs" {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		accept := acceptLogs
		mu.Unlock()
		if accept {
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "")
			messageType, payload, err := conn.Read(r.Context())
			if err != nil || messageType != websocket.MessageBinary {
				return
			}
			frame, err := protocol.DecodeLogFrame(payload)
			if err == nil {
				received <- frame
			}
			return
		}
		http.Error(w, "temporary outage", http.StatusServiceUnavailable)
	}))
	t.Cleanup(httpSrv.Close)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	frames := make(chan protocol.LogFrame)
	client := &agent.Client{Config: agent.Config{ServerURL: httpSrv.URL}}
	done := make(chan error, 1)
	go func() {
		done <- client.SendLogFramesLoop(ctx, frames, time.Hour)
	}()

	accepted := make(chan struct{})
	go func() {
		for seq := uint64(1); seq <= 3; seq++ {
			frames <- testLogFrame(seq, "queued\n")
		}
		close(frames)
		close(accepted)
	}()

	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Fatal("SendLogFramesLoop did not drain incoming frames during outage")
	}

	mu.Lock()
	acceptLogs = true
	mu.Unlock()
	gapFrame := receiveLogFrame(t, ctx, received)
	if gapFrame.Seq != 3 {
		t.Fatalf("gap frame Seq = %d, want last dropped sequence number", gapFrame.Seq)
	}
	if gapFrame.Flags&protocol.FlagLogGap == 0 {
		t.Fatalf("gap frame Flags = %v, want FlagLogGap", gapFrame.Flags)
	}
	if len(gapFrame.Payload) != 0 {
		t.Fatalf("gap frame Payload = %q, want empty payload", gapFrame.Payload)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("SendLogFramesLoop returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for SendLogFramesLoop to stop")
	}
}

func TestRuntimeScansAndForwardsReconciledEvents(t *testing.T) {
	events := make(chan serial.Event)
	forwarded := make(chan serial.Event, 1)
	scanned := make(chan struct{}, 1)
	reconciled := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runtime := agent.NewRuntime(agent.RuntimeConfig{
		ScanInterval: time.Hour,
		Discover: func(agent.DiscoveryConfig) ([]agent.DiscoveredDevice, error) {
			select {
			case scanned <- struct{}{}:
			default:
			}
			return []agent.DiscoveredDevice{{DevName: "/dev/ttyUSB0", IDPath: "id-path-1", PermissionOK: true}}, nil
		},
		Reconciler: &runtimeFakeReconciler{
			result: agent.ReconcileResult{Events: []agent.EventStream{{Events: events}}},
			done:   reconciled,
		},
		ForwardEvents: func(ctx context.Context, in <-chan serial.Event) error {
			select {
			case event := <-in:
				forwarded <- event
			case <-ctx.Done():
				return ctx.Err()
			}
			return nil
		},
	})

	done := make(chan error, 1)
	go func() {
		done <- runtime.Run(ctx)
	}()

	select {
	case <-scanned:
	case <-time.After(time.Second):
		t.Fatal("runtime did not scan devices")
	}
	select {
	case <-reconciled:
	case <-time.After(time.Second):
		t.Fatal("runtime did not reconcile scan result")
	}

	events <- serial.Event{ChannelID: "channel-1", Direction: serial.DirectionRX, Timestamp: time.Unix(1, 0), Data: []byte("boot\n")}
	select {
	case event := <-forwarded:
		if event.ChannelID != "channel-1" {
			t.Fatalf("forwarded ChannelID = %q, want channel-1", event.ChannelID)
		}
	case <-time.After(time.Second):
		t.Fatal("runtime did not forward reconciled event stream")
	}

	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("Run error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runtime did not stop after context cancellation")
	}
}

func TestRuntimeForwardsSnapshotAndStatusAfterReconcile(t *testing.T) {
	snapshots := make(chan []agent.DiscoveredDevice, 1)
	statuses := make(chan []agent.ChannelStatus, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runtime := agent.NewRuntime(agent.RuntimeConfig{
		ScanInterval: time.Hour,
		Discover: func(agent.DiscoveryConfig) ([]agent.DiscoveredDevice, error) {
			return []agent.DiscoveredDevice{{DevName: "/dev/ttyUSB0", IDPath: "id-path-1", IDPathTag: "id-path-tag-1", PermissionOK: true}}, nil
		},
		Reconciler: &runtimeFakeReconciler{
			result: agent.ReconcileResult{
				Candidates: []agent.DiscoveredDevice{{
					DevName:      "/dev/ttyUSB1",
					IDPath:       "candidate-id-path",
					IDPathTag:    "candidate-id-path-tag",
					PermissionOK: true,
				}},
				Statuses: []agent.ChannelStatus{{
					ChannelID: "channel-1",
					Status:    "online",
					DevName:   "/dev/ttyUSB0",
				}},
			},
		},
		ForwardSnapshot: func(_ context.Context, devices []agent.DiscoveredDevice) error {
			snapshots <- append([]agent.DiscoveredDevice(nil), devices...)
			return nil
		},
		ForwardStatuses: func(_ context.Context, in []agent.ChannelStatus) error {
			statuses <- append([]agent.ChannelStatus(nil), in...)
			return nil
		},
	})

	done := make(chan error, 1)
	go func() {
		done <- runtime.Run(ctx)
	}()

	select {
	case got := <-snapshots:
		if len(got) != 1 || got[0].DevName != "/dev/ttyUSB1" || got[0].IDPath != "candidate-id-path" {
			t.Fatalf("forwarded snapshot = %+v, want candidate /dev/ttyUSB1", got)
		}
	case <-time.After(time.Second):
		t.Fatal("runtime did not forward reconciled candidates")
	}
	select {
	case got := <-statuses:
		if len(got) != 1 || got[0].ChannelID != "channel-1" || got[0].Status != "online" || got[0].DevName != "/dev/ttyUSB0" {
			t.Fatalf("forwarded statuses = %+v, want channel-1 online on /dev/ttyUSB0", got)
		}
	case <-time.After(time.Second):
		t.Fatal("runtime did not forward reconciled statuses")
	}

	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("Run error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runtime did not stop after context cancellation")
	}
}

func TestRuntimeForwardsStatusesWhenSnapshotForwardFails(t *testing.T) {
	statuses := make(chan []agent.ChannelStatus, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runtime := agent.NewRuntime(agent.RuntimeConfig{
		ScanInterval: time.Hour,
		Discover: func(agent.DiscoveryConfig) ([]agent.DiscoveredDevice, error) {
			return []agent.DiscoveredDevice{{DevName: "/dev/ttyUSB0", IDPath: "id-path-1", PermissionOK: true}}, nil
		},
		Reconciler: &runtimeFakeReconciler{
			result: agent.ReconcileResult{
				Candidates: []agent.DiscoveredDevice{{
					DevName:      "/dev/ttyUSB1",
					IDPath:       "candidate-id-path",
					PermissionOK: true,
				}},
				Statuses: []agent.ChannelStatus{{
					ChannelID: "channel-1",
					Status:    "online",
					DevName:   "/dev/ttyUSB0",
				}},
			},
		},
		ForwardSnapshot: func(context.Context, []agent.DiscoveredDevice) error {
			return errors.New("snapshot forward failed")
		},
		ForwardStatuses: func(_ context.Context, in []agent.ChannelStatus) error {
			statuses <- append([]agent.ChannelStatus(nil), in...)
			return nil
		},
	})

	done := make(chan error, 1)
	go func() {
		done <- runtime.Run(ctx)
	}()

	select {
	case got := <-statuses:
		if len(got) != 1 || got[0].ChannelID != "channel-1" || got[0].Status != "online" || got[0].DevName != "/dev/ttyUSB0" {
			t.Fatalf("forwarded statuses = %+v, want channel-1 online on /dev/ttyUSB0", got)
		}
	case err := <-done:
		t.Fatalf("Run returned before forwarding status after snapshot error: %v", err)
	case <-time.After(time.Second):
		t.Fatal("runtime did not forward statuses after snapshot forward error")
	}

	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("Run error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runtime did not stop after context cancellation")
	}
}

func TestRuntimeForwardsEmptySnapshotToClearStaleCandidates(t *testing.T) {
	snapshots := make(chan []agent.DiscoveredDevice, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runtime := agent.NewRuntime(agent.RuntimeConfig{
		ScanInterval: time.Hour,
		Discover: func(agent.DiscoveryConfig) ([]agent.DiscoveredDevice, error) {
			return nil, nil
		},
		Reconciler: &runtimeFakeReconciler{
			result: agent.ReconcileResult{},
		},
		ForwardSnapshot: func(_ context.Context, devices []agent.DiscoveredDevice) error {
			snapshots <- append([]agent.DiscoveredDevice(nil), devices...)
			return nil
		},
	})

	done := make(chan error, 1)
	go func() {
		done <- runtime.Run(ctx)
	}()

	select {
	case got := <-snapshots:
		if len(got) != 0 {
			t.Fatalf("forwarded snapshot length = %d, want 0", len(got))
		}
	case err := <-done:
		t.Fatalf("Run returned before forwarding empty snapshot: %v", err)
	case <-time.After(time.Second):
		t.Fatal("runtime did not forward empty candidate snapshot")
	}

	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("Run error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runtime did not stop after context cancellation")
	}
}

func TestRuntimeReleasesReconcilerEventStreamWhenForwardingStops(t *testing.T) {
	backendFactory := newFakeBackendFactory()
	reconciler := agent.NewReconciler(agent.ReconcilerConfig{BackendFactory: backendFactory})
	forwarded := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runtime := agent.NewRuntime(agent.RuntimeConfig{
		ScanInterval: time.Hour,
		Channels: []agent.ChannelConfig{{
			ID:            "channel-1",
			IDPath:        "id-path-1",
			DefaultConfig: serial.DefaultConfig(),
		}},
		Discover: func(agent.DiscoveryConfig) ([]agent.DiscoveredDevice, error) {
			return []agent.DiscoveredDevice{{DevName: "/dev/ttyUSB0", IDPath: "id-path-1", PermissionOK: true}}, nil
		},
		Reconciler: reconciler,
		ForwardEvents: func(context.Context, <-chan serial.Event) error {
			forwarded <- struct{}{}
			return nil
		},
	})

	done := make(chan error, 1)
	go func() {
		done <- runtime.Run(ctx)
	}()

	select {
	case <-forwarded:
	case <-time.After(time.Second):
		t.Fatal("runtime did not start event forwarding")
	}

	backend := backendFactory.backend("/dev/ttyUSB0")
	if backend == nil {
		t.Fatal("backend for /dev/ttyUSB0 was not opened")
	}
	for i := 0; i < 140; i++ {
		backend.injectRX(t, []byte{byte(i)})
	}

	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("Run error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runtime did not stop after context cancellation")
	}
}

func TestRuntimeContinuesAfterTransientScanErrors(t *testing.T) {
	reconciled := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	discoverCalls := 0
	sourceCalls := 0
	runtime := agent.NewRuntime(agent.RuntimeConfig{
		ScanInterval: 10 * time.Millisecond,
		Discover: func(agent.DiscoveryConfig) ([]agent.DiscoveredDevice, error) {
			discoverCalls++
			if discoverCalls == 1 {
				return nil, errors.New("temporary discover failure")
			}
			return []agent.DiscoveredDevice{{DevName: "/dev/ttyUSB0", IDPath: "id-path-1", PermissionOK: true}}, nil
		},
		ChannelSource: func(context.Context) ([]agent.ChannelConfig, error) {
			sourceCalls++
			if sourceCalls == 1 {
				return nil, errors.New("temporary channel source failure")
			}
			return []agent.ChannelConfig{{
				ID:            "channel-1",
				IDPath:        "id-path-1",
				DefaultConfig: serial.DefaultConfig(),
			}}, nil
		},
		Reconciler: &runtimeFakeReconciler{done: reconciled},
	})

	done := make(chan error, 1)
	go func() {
		done <- runtime.Run(ctx)
	}()

	select {
	case <-reconciled:
	case err := <-done:
		t.Fatalf("Run returned before recovering from transient scan errors: %v", err)
	case <-time.After(time.Second):
		t.Fatal("runtime did not reconcile after transient scan errors")
	}

	if discoverCalls < 3 {
		t.Fatalf("discover calls = %d, want at least 3", discoverCalls)
	}
	if sourceCalls < 2 {
		t.Fatalf("channel source calls = %d, want at least 2", sourceCalls)
	}

	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("Run error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runtime did not stop after context cancellation")
	}
}

func TestRuntimeContinuesAfterTransientRuntimeStateForwardError(t *testing.T) {
	forwarded := make(chan []agent.DiscoveredDevice, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	forwardCalls := 0
	runtime := agent.NewRuntime(agent.RuntimeConfig{
		ScanInterval: time.Millisecond,
		Discover: func(agent.DiscoveryConfig) ([]agent.DiscoveredDevice, error) {
			return []agent.DiscoveredDevice{{DevName: "/dev/ttyUSB0", IDPath: "id-path-1", PermissionOK: true}}, nil
		},
		Reconciler: &runtimeFakeReconciler{
			result: agent.ReconcileResult{
				Candidates: []agent.DiscoveredDevice{{DevName: "/dev/ttyUSB0", IDPath: "id-path-1", PermissionOK: true}},
			},
		},
		ForwardSnapshot: func(_ context.Context, devices []agent.DiscoveredDevice) error {
			forwardCalls++
			if forwardCalls == 1 {
				return errors.New("temporary snapshot forward failure")
			}
			select {
			case forwarded <- append([]agent.DiscoveredDevice(nil), devices...):
			default:
			}
			return nil
		},
	})

	done := make(chan error, 1)
	go func() {
		done <- runtime.Run(ctx)
	}()

	select {
	case got := <-forwarded:
		if len(got) != 1 || got[0].IDPath != "id-path-1" {
			t.Fatalf("forwarded snapshot after retry = %+v, want id-path-1", got)
		}
	case err := <-done:
		t.Fatalf("Run returned before retrying after forward error: %v", err)
	case <-time.After(time.Second):
		t.Fatal("runtime did not retry after snapshot forward error")
	}

	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("Run error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runtime did not stop after context cancellation")
	}
}

func TestRuntimeReadsChannelSourceOnEachScan(t *testing.T) {
	reconciled := make(chan struct{}, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reconciler := &runtimeFakeReconciler{done: reconciled}
	sourceCalls := 0
	runtime := agent.NewRuntime(agent.RuntimeConfig{
		ScanInterval: 10 * time.Millisecond,
		Channels: []agent.ChannelConfig{{
			ID:            "stale-channel",
			IDPath:        "stale-id-path",
			DefaultConfig: serial.DefaultConfig(),
		}},
		ChannelSource: func(context.Context) ([]agent.ChannelConfig, error) {
			sourceCalls++
			if sourceCalls == 1 {
				return []agent.ChannelConfig{{
					ID:            "channel-1",
					IDPath:        "id-path-1",
					DefaultConfig: serial.DefaultConfig(),
				}}, nil
			}
			config := serial.DefaultConfig()
			config.Baud = 57600
			return []agent.ChannelConfig{{
				ID:            "channel-2",
				IDPath:        "id-path-1",
				DefaultConfig: config,
			}}, nil
		},
		Discover: func(agent.DiscoveryConfig) ([]agent.DiscoveredDevice, error) {
			return []agent.DiscoveredDevice{{DevName: "/dev/ttyUSB0", IDPath: "id-path-1", PermissionOK: true}}, nil
		},
		Reconciler: reconciler,
	})

	done := make(chan error, 1)
	go func() {
		done <- runtime.Run(ctx)
	}()

	waitForReconciles(t, reconciled, 2)
	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("Run error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runtime did not stop after context cancellation")
	}

	calls := reconciler.reconciledChannelCalls()
	if len(calls) < 2 {
		t.Fatalf("reconcile calls = %d, want at least 2", len(calls))
	}
	if got := calls[0]; len(got) != 1 || got[0].ID != "channel-1" || got[0].DefaultConfig.Baud != 115200 {
		t.Fatalf("first reconcile channels = %+v, want channel-1 at 115200", got)
	}
	if got := calls[1]; len(got) != 1 || got[0].ID != "channel-2" || got[0].DefaultConfig.Baud != 57600 {
		t.Fatalf("second reconcile channels = %+v, want channel-2 at 57600", got)
	}
}

func testLogFrame(seq uint64, payload string) protocol.LogFrame {
	return protocol.LogFrame{
		ChannelID:   "channel-1",
		Seq:         seq,
		TimestampNS: time.Unix(1700000000, int64(seq)).UnixNano(),
		Direction:   protocol.DirectionRX,
		Flags:       protocol.FlagRaw,
		Payload:     []byte(payload),
	}
}

func receiveLogFrame(t *testing.T, ctx context.Context, frames <-chan protocol.LogFrame) protocol.LogFrame {
	t.Helper()
	select {
	case frame := <-frames:
		return frame
	case <-ctx.Done():
		t.Fatal("timeout waiting for log frame")
		return protocol.LogFrame{}
	}
}

func readAgentOperationResult(t *testing.T, ctx context.Context, conn *websocket.Conn) protocol.OperationResult {
	t.Helper()
	var result protocol.OperationResult
	if err := protocol.ReadJSON(ctx, conn, &result); err != nil {
		t.Fatalf("read operation result returned error: %v", err)
	}
	return result
}

func receiveAgentOperationResult(t *testing.T, ctx context.Context, results <-chan protocol.OperationResult) protocol.OperationResult {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-ctx.Done():
		t.Fatal("timeout waiting for operation result")
		return protocol.OperationResult{}
	}
}

type runtimeFakeReconciler struct {
	mu       sync.Mutex
	channels []agent.ChannelConfig
	calls    [][]agent.ChannelConfig
	devices  []agent.DiscoveredDevice
	result   agent.ReconcileResult
	done     chan<- struct{}
}

type agentRFC2217Resolver struct {
	control serial.SerialControl
	config  serial.Config
}

func (r agentRFC2217Resolver) RFC2217Control(_ context.Context, channelID string) (serial.SerialControl, serial.Config, error) {
	if channelID != "channel-1" {
		return nil, serial.Config{}, errors.New("channel not found")
	}
	config := r.config
	if config == (serial.Config{}) {
		config = serial.DefaultConfig()
	}
	return r.control, config, nil
}

func (r *runtimeFakeReconciler) Reconcile(_ context.Context, channels []agent.ChannelConfig, devices []agent.DiscoveredDevice) agent.ReconcileResult {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.channels = append([]agent.ChannelConfig(nil), channels...)
	r.calls = append(r.calls, append([]agent.ChannelConfig(nil), channels...))
	r.devices = append([]agent.DiscoveredDevice(nil), devices...)
	select {
	case r.done <- struct{}{}:
	default:
	}
	return r.result
}

func (r *runtimeFakeReconciler) reconciledChannelCalls() [][]agent.ChannelConfig {
	r.mu.Lock()
	defer r.mu.Unlock()
	calls := make([][]agent.ChannelConfig, 0, len(r.calls))
	for _, channels := range r.calls {
		calls = append(calls, append([]agent.ChannelConfig(nil), channels...))
	}
	return calls
}

func waitForReconciles(t *testing.T, reconciled <-chan struct{}, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		select {
		case <-reconciled:
		case <-time.After(time.Second):
			t.Fatalf("runtime reconciled %d times, want %d", i, count)
		}
	}
}
