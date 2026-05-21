package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	bugserial "go.bug.st/serial"

	"serial-platform/internal/agent"
	"serial-platform/internal/protocol"
	"serial-platform/internal/serial"
	"serial-platform/internal/server"
	"serial-platform/internal/storage"
)

func TestRealSerialLoopback(t *testing.T) {
	config := realSerialTestConfig(t)

	if _, err := os.Stat(config.Dev); err != nil {
		message := fmt.Sprintf("real serial: %s not found", config.Dev)
		if isPermissionError(err) {
			message = fmt.Sprintf("real serial: permission denied for %s, add current user to dialout", config.Dev)
		}
		failOrSkip(t, config.Required, message)
	}

	port, err := bugserial.Open(config.Dev, &bugserial.Mode{
		BaudRate: config.Baud,
		DataBits: 8,
		Parity:   bugserial.NoParity,
		StopBits: bugserial.OneStopBit,
	})
	if err != nil {
		message := fmt.Sprintf("real serial: open %s failed: %v", config.Dev, err)
		if isPermissionError(err) {
			message = fmt.Sprintf("real serial: permission denied for %s, add current user to dialout", config.Dev)
		}
		failOrSkip(t, config.Required, message)
	}
	defer port.Close()
	if err := port.SetReadTimeout(100 * time.Millisecond); err != nil {
		failOrSkip(t, config.Required, fmt.Sprintf("real serial: set read timeout for %s failed: %v", config.Dev, err))
	}

	payload := []byte(fmt.Sprintf("serial-platform-loopback-%d\r\n", time.Now().UnixNano()))
	n, err := port.Write(payload)
	if err != nil {
		failOrSkip(t, config.Required, fmt.Sprintf("real serial: write %s failed: %v", config.Dev, err))
	}
	if n != len(payload) {
		failOrSkip(t, config.Required, fmt.Sprintf("real serial: short write on %s: wrote %d of %d bytes", config.Dev, n, len(payload)))
	}

	got, err := readSerialPortUntil(port, payload, 2*time.Second)
	if err != nil {
		failOrSkip(t, config.Required, fmt.Sprintf("real serial: read %s failed: %v", config.Dev, err))
	}
	if bytes.Contains(got, payload) {
		t.Logf("real serial: loopback passed %s at %d baud", config.Dev, config.Baud)
		return
	}
	if config.ExpectLoopback {
		failOrSkip(t, config.Required, fmt.Sprintf("real serial: loopback payload not observed within 2s on %s, got %q want %q", config.Dev, got, payload))
	}
	if len(got) == 0 {
		failOrSkip(t, config.Required, fmt.Sprintf("real serial: no loopback payload or RX activity observed within 2s on %s", config.Dev))
	}
	t.Logf("real serial: RX activity observed on %s at %d baud without loopback echo (%d bytes)", config.Dev, config.Baud, len(got))
}

func TestRealSerialPlatformWorkflow(t *testing.T) {
	config := realSerialTestConfig(t)
	if _, err := os.Stat(config.Dev); err != nil {
		message := fmt.Sprintf("real serial platform: %s not found", config.Dev)
		if isPermissionError(err) {
			message = fmt.Sprintf("real serial platform: permission denied for %s, add current user to dialout", config.Dev)
		}
		failOrSkip(t, config.Required, message)
	}

	root := t.TempDir()
	db, err := storage.Open(filepath.Join(root, "meta.db"))
	if err != nil {
		t.Fatalf("Open metadata DB returned error: %v", err)
	}
	defer db.Close()

	rfc2217Listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen RFC2217 returned error: %v", err)
	}
	rfc2217Port := rfc2217Listener.Addr().(*net.TCPAddr).Port
	var listenerOnce sync.Once
	srv := server.New(server.ServerConfig{
		DB:     db,
		LogDir: filepath.Join(root, "logs"),
		RFC2217Listen: func(network, address string) (net.Listener, error) {
			wantAddress := net.JoinHostPort("127.0.0.1", strconv.Itoa(rfc2217Port))
			if network != "tcp" || address != wantAddress {
				return nil, fmt.Errorf("unexpected RFC2217 listen %s %s, want tcp %s", network, address, wantAddress)
			}
			var out net.Listener
			listenerOnce.Do(func() {
				out = rfc2217Listener
			})
			if out == nil {
				return nil, fmt.Errorf("RFC2217 listener for %s already consumed", address)
			}
			return out, nil
		},
	})
	httpSrv := httptest.NewServer(srv)
	defer httpSrv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	bgErrs := make(chan error, 4)
	var wg sync.WaitGroup
	defer func() {
		cancel()
		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatalf("background real serial workflow goroutines did not stop")
		}
		waitForSerialReusable(t, config)
	}()

	startBackground := func(run func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			run()
		}()
	}

	startBackground(func() {
		if err := srv.ServeRFC2217(ctx, "127.0.0.1"); err != nil && ctx.Err() == nil {
			bgErrs <- fmt.Errorf("ServeRFC2217: %w", err)
		}
	})

	agentID := "e2e-agent"
	agentClient := &agent.Client{Config: agent.Config{
		ServerURL: httpSrv.URL,
		DataDir:   filepath.Join(root, "agent"),
		AgentID:   agentID,
	}}
	if _, err := agentClient.Connect(ctx); err != nil {
		failOrSkip(t, config.Required, fmt.Sprintf("real serial platform: connect agent failed: %v", err))
	}
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), time.Second)
		defer closeCancel()
		_ = agentClient.Close(closeCtx)
	}()

	frames := make(chan protocol.LogFrame, 256)
	uploader := agent.NewLogUploader(agent.LogUploaderConfig{Out: frames})
	startBackground(func() {
		if err := agentClient.SendLogFramesLoop(ctx, frames, 50*time.Millisecond); err != nil && ctx.Err() == nil {
			bgErrs <- fmt.Errorf("SendLogFramesLoop: %w", err)
		}
	})

	reconciler := agent.NewReconciler(agent.ReconcilerConfig{})
	runtime := agent.NewRuntime(agent.RuntimeConfig{
		ScanInterval:  200 * time.Millisecond,
		ChannelSource: agentClient.FetchChannelConfigs,
		Reconciler:    reconciler,
		ForwardEvents: func(ctx context.Context, events <-chan serial.Event) error {
			return uploader.Forward(ctx, events)
		},
		ForwardSnapshot: func(ctx context.Context, devices []agent.DiscoveredDevice) error {
			return agentClient.SendControl(ctx, agent.NewDeviceSnapshotMessage(agentID, devices))
		},
		ForwardStatuses: func(ctx context.Context, statuses []agent.ChannelStatus) error {
			return agentClient.SendControl(ctx, agent.NewChannelStatusUpdateMessage(agentID, statuses))
		},
	})
	startBackground(func() {
		if err := runtime.Run(ctx); err != nil && !errors.Is(err, context.Canceled) && ctx.Err() == nil {
			bgErrs <- fmt.Errorf("agent runtime: %w", err)
		}
	})
	startBackground(func() {
		if err := agentClient.HandleControlMessages(ctx, reconciler, agent.TunnelDialer{ServerURL: httpSrv.URL}); err != nil && !errors.Is(err, context.Canceled) && ctx.Err() == nil {
			bgErrs <- fmt.Errorf("agent control: %w", err)
		}
	})

	postNoBody(t, httpSrv.Client(), httpSrv.URL+"/api/agents/"+url.PathEscape(agentID)+"/approve")

	candidate := waitForCandidate(t, ctx, httpSrv.Client(), httpSrv.URL, config.Dev, bgErrs)
	channel := confirmCandidate(t, httpSrv.Client(), httpSrv.URL, candidate.ID, rfc2217Port, config.Baud)
	channel = waitForChannelOnline(t, ctx, httpSrv.Client(), httpSrv.URL, channel.ID, config.Dev, bgErrs)

	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(rfc2217Port))
	conn := dialTCPUntil(t, ctx, addr, bgErrs)
	defer conn.Close()

	payload := []byte(fmt.Sprintf("serial-platform-rfc2217-%d\r\n", time.Now().UnixNano()))
	if err := conn.SetDeadline(time.Now().Add(15 * time.Second)); err != nil {
		t.Fatalf("SetDeadline returned error: %v", err)
	}
	writeRFC2217Baud(t, conn, config.Baud)
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("RFC2217 write returned error: %v", err)
	}

	rx, err := readNetConnUntil(conn, payload, 12*time.Second)
	if err != nil {
		t.Fatalf("RFC2217 read returned error: %v", err)
	}
	loopbackObserved := bytes.Contains(rx, payload)
	if !loopbackObserved && config.ExpectLoopback {
		failOrSkip(t, config.Required, fmt.Sprintf("real serial platform: loopback payload not observed through RFC2217, got %q want %q", rx, payload))
	}
	if !loopbackObserved && len(rx) == 0 {
		failOrSkip(t, config.Required, "real serial platform: no RX data observed through RFC2217")
	}

	txLog := waitForDownloadedLog(t, ctx, httpSrv.Client(), httpSrv.URL, channel.ID, "tx", "text", payload, bgErrs)
	if !strings.Contains(txLog, string(payload)) {
		t.Fatalf("TX log does not contain payload %q: %q", payload, txLog)
	}

	if loopbackObserved {
		rxLog := waitForDownloadedLog(t, ctx, httpSrv.Client(), httpSrv.URL, channel.ID, "rx", "text", payload, bgErrs)
		if !strings.Contains(rxLog, string(payload)) {
			t.Fatalf("RX log does not contain loopback payload %q: %q", payload, rxLog)
		}
	} else {
		rxLog := waitForAnyDownloadedLog(t, ctx, httpSrv.Client(), httpSrv.URL, channel.ID, "rx", bgErrs)
		if strings.TrimSpace(rxLog) == "" {
			t.Fatal("RX log is empty after RFC2217 session observed RX data")
		}
		t.Logf("real serial platform: RX activity observed without loopback echo (%d RFC2217 bytes, %d downloaded log bytes)", len(rx), len(rxLog))
	}

	waitForRawDirections(t, ctx, httpSrv.Client(), httpSrv.URL, channel.ID, payload, bgErrs)
	t.Logf("real serial platform: passed %s at %d baud via RFC2217 port %d channel %s", config.Dev, config.Baud, rfc2217Port, channel.ID)
}

type realSerialConfig struct {
	Dev            string
	Baud           int
	Required       bool
	ExpectLoopback bool
}

func realSerialTestConfig(t *testing.T) realSerialConfig {
	t.Helper()
	dev := os.Getenv("REAL_SERIAL_DEV")
	if dev == "" {
		dev = "/dev/ttyUSB0"
	}
	baud := 2000000
	if value := strings.TrimSpace(os.Getenv("REAL_SERIAL_BAUD")); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed <= 0 {
			t.Fatalf("REAL_SERIAL_BAUD must be a positive integer, got %q", value)
		}
		baud = parsed
	}
	return realSerialConfig{
		Dev:            dev,
		Baud:           baud,
		Required:       isRealSerialRequired(),
		ExpectLoopback: os.Getenv("REAL_SERIAL_EXPECT_LOOPBACK") == "1",
	}
}

func readSerialPortUntil(port bugserial.Port, want []byte, timeout time.Duration) ([]byte, error) {
	deadline := time.Now().Add(timeout)
	got := make([]byte, 0, len(want))
	buf := make([]byte, 512)
	for time.Now().Before(deadline) && !bytes.Contains(got, want) {
		n, err := port.Read(buf)
		if err != nil {
			return got, err
		}
		got = append(got, buf[:n]...)
	}
	return got, nil
}

func readNetConnUntil(conn net.Conn, want []byte, timeout time.Duration) ([]byte, error) {
	deadline := time.Now().Add(timeout)
	got := make([]byte, 0, len(want))
	buf := make([]byte, 512)
	for time.Now().Before(deadline) && !bytes.Contains(got, want) {
		if err := conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
			return got, err
		}
		n, err := conn.Read(buf)
		if n > 0 {
			got = append(got, buf[:n]...)
		}
		if err == nil {
			continue
		}
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			continue
		}
		return got, err
	}
	return got, nil
}

func writeRFC2217Baud(t *testing.T, conn net.Conn, baud int) {
	t.Helper()
	command := []byte{
		255, 250, 44, 1,
		byte(baud >> 24), byte(baud >> 16), byte(baud >> 8), byte(baud),
		255, 240,
	}
	if _, err := conn.Write(command); err != nil {
		t.Fatalf("RFC2217 SET-BAUDRATE write returned error: %v", err)
	}
	confirmation := []byte{
		255, 250, 44, 101,
		byte(baud >> 24), byte(baud >> 16), byte(baud >> 8), byte(baud),
		255, 240,
	}
	got, err := readNetConnUntil(conn, confirmation, 10*time.Second)
	if err != nil {
		t.Fatalf("RFC2217 SET-BAUDRATE confirmation read returned error: %v", err)
	}
	if !bytes.Contains(got, confirmation) {
		t.Fatalf("RFC2217 SET-BAUDRATE confirmation not observed, got %q want %q", got, confirmation)
	}
}

func waitForSerialReusable(t *testing.T, config realSerialConfig) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		port, err := bugserial.Open(config.Dev, &bugserial.Mode{
			BaudRate: config.Baud,
			DataBits: 8,
			Parity:   bugserial.NoParity,
			StopBits: bugserial.OneStopBit,
		})
		if err == nil {
			_ = port.Close()
			return
		}
		lastErr = err
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("serial device %s was not reusable after workflow cleanup: %v", config.Dev, lastErr)
}

func waitForCandidate(t *testing.T, ctx context.Context, client *http.Client, baseURL, dev string, bgErrs <-chan error) storage.Candidate {
	t.Helper()
	var found storage.Candidate
	waitFor(t, ctx, bgErrs, func() (bool, error) {
		var candidates []storage.Candidate
		if err := getJSON(ctx, client, baseURL+"/api/candidates", &candidates); err != nil {
			return false, err
		}
		for _, candidate := range candidates {
			if candidate.DevName == dev {
				found = candidate
				return true, nil
			}
		}
		return false, nil
	})
	return found
}

func confirmCandidate(t *testing.T, client *http.Client, baseURL, candidateID string, rfc2217Port, baud int) storage.Channel {
	t.Helper()
	body := fmt.Sprintf(`{"alias":"e2e-%d","role":"console","rfc2217_port":%d,"default_baud":%d,"default_data_bits":8,"default_parity":"N","default_stop_bits":1,"default_flow":"none"}`, time.Now().UnixNano(), rfc2217Port, baud)
	var channel storage.Channel
	postJSON(t, client, baseURL+"/api/candidates/"+url.PathEscape(candidateID)+"/confirm", body, http.StatusCreated, &channel)
	return channel
}

func waitForChannelOnline(t *testing.T, ctx context.Context, client *http.Client, baseURL, channelID, dev string, bgErrs <-chan error) storage.Channel {
	t.Helper()
	var found storage.Channel
	waitFor(t, ctx, bgErrs, func() (bool, error) {
		var channels []storage.Channel
		if err := getJSON(ctx, client, baseURL+"/api/channels", &channels); err != nil {
			return false, err
		}
		for _, channel := range channels {
			if channel.ID != channelID {
				continue
			}
			found = channel
			if channel.Status == storage.ChannelStatusError {
				return false, fmt.Errorf("channel entered error state: %s", channel.ErrorMessage)
			}
			return channel.Status == storage.ChannelStatusOnline && channel.DevName == dev, nil
		}
		return false, nil
	})
	return found
}

func dialTCPUntil(t *testing.T, ctx context.Context, addr string, bgErrs <-chan error) net.Conn {
	t.Helper()
	var conn net.Conn
	waitFor(t, ctx, bgErrs, func() (bool, error) {
		dialed, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err != nil {
			return false, nil
		}
		conn = dialed
		return true, nil
	})
	return conn
}

func waitForDownloadedLog(t *testing.T, ctx context.Context, client *http.Client, baseURL, channelID, direction, format string, want []byte, bgErrs <-chan error) string {
	t.Helper()
	data := waitForDownloadedLogBytes(t, ctx, client, baseURL, channelID, direction, format, want, bgErrs)
	return string(data)
}

func waitForAnyDownloadedLog(t *testing.T, ctx context.Context, client *http.Client, baseURL, channelID, direction string, bgErrs <-chan error) string {
	t.Helper()
	var data []byte
	waitFor(t, ctx, bgErrs, func() (bool, error) {
		var err error
		data, err = downloadLog(ctx, client, baseURL, channelID, direction, "text")
		if err != nil {
			return false, err
		}
		return strings.TrimSpace(string(data)) != "", nil
	})
	return string(data)
}

func waitForDownloadedLogBytes(t *testing.T, ctx context.Context, client *http.Client, baseURL, channelID, direction, format string, want []byte, bgErrs <-chan error) []byte {
	t.Helper()
	var data []byte
	waitFor(t, ctx, bgErrs, func() (bool, error) {
		var err error
		data, err = downloadLog(ctx, client, baseURL, channelID, direction, format)
		if err != nil {
			return false, err
		}
		return bytes.Contains(data, want), nil
	})
	return data
}

func waitForRawDirections(t *testing.T, ctx context.Context, client *http.Client, baseURL, channelID string, payload []byte, bgErrs <-chan error) {
	t.Helper()
	waitFor(t, ctx, bgErrs, func() (bool, error) {
		data, err := downloadLog(ctx, client, baseURL, channelID, "both", "raw")
		if err != nil {
			return false, err
		}
		frames, err := decodeRawDownload(data)
		if err != nil {
			return false, err
		}
		var tx bytes.Buffer
		var rx bytes.Buffer
		for _, frame := range frames {
			if frame.Direction == protocol.DirectionTX {
				tx.Write(frame.Payload)
			}
			if frame.Direction == protocol.DirectionRX {
				rx.Write(frame.Payload)
			}
		}
		return bytes.Contains(tx.Bytes(), payload) && bytes.Contains(rx.Bytes(), payload), nil
	})
}

func decodeRawDownload(data []byte) ([]protocol.LogFrame, error) {
	frames := make([]protocol.LogFrame, 0)
	for len(data) > 0 {
		if len(data) < 4 {
			return nil, fmt.Errorf("raw download has truncated length header")
		}
		size := int(data[0])<<24 | int(data[1])<<16 | int(data[2])<<8 | int(data[3])
		data = data[4:]
		if len(data) < size {
			return nil, fmt.Errorf("raw download frame length %d exceeds remaining %d", size, len(data))
		}
		frame, err := protocol.DecodeLogFrame(data[:size])
		if err != nil {
			return nil, err
		}
		frames = append(frames, frame)
		data = data[size:]
	}
	return frames, nil
}

func downloadLog(ctx context.Context, client *http.Client, baseURL, channelID, direction, format string) ([]byte, error) {
	query := url.Values{}
	query.Set("channel_id", channelID)
	query.Set("direction", direction)
	query.Set("format", format)
	query.Set("direction_label", "false")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/logs/download?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET /api/logs/download returned %s: %s", resp.Status, data)
	}
	return data, nil
}

func waitFor(t *testing.T, ctx context.Context, bgErrs <-chan error, probe func() (bool, error)) {
	t.Helper()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-bgErrs:
			t.Fatal(err)
		case <-ctx.Done():
			t.Fatalf("timed out waiting for condition: %v", ctx.Err())
		default:
		}
		ok, err := probe()
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			return
		}
		select {
		case err := <-bgErrs:
			t.Fatal(err)
		case <-ctx.Done():
			t.Fatalf("timed out waiting for condition: %v", ctx.Err())
		case <-ticker.C:
		}
	}
}

func getJSON(ctx context.Context, client *http.Client, requestURL string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("GET %s returned %s: %s", requestURL, resp.Status, data)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func postNoBody(t *testing.T, client *http.Client, requestURL string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, requestURL, nil)
	if err != nil {
		t.Fatalf("NewRequest returned error: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST %s returned error: %v", requestURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST %s returned %s: %s", requestURL, resp.Status, data)
	}
}

func postJSON(t *testing.T, client *http.Client, requestURL, body string, wantStatus int, out any) {
	t.Helper()
	resp, err := client.Post(requestURL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s returned error: %v", requestURL, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll returned error: %v", err)
	}
	if resp.StatusCode != wantStatus {
		t.Fatalf("POST %s returned %s: %s", requestURL, resp.Status, data)
	}
	if err := json.Unmarshal(data, out); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
}

func isRealSerialRequired() bool {
	return os.Getenv("REAL_SERIAL_REQUIRED") == "1"
}

func failOrSkip(t *testing.T, required bool, message string) {
	t.Helper()
	if required {
		t.Fatal(message)
	}
	t.Skip(skipMessage(message))
}

func isPermissionError(err error) bool {
	if errors.Is(err, os.ErrPermission) {
		return true
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "permission") || strings.Contains(text, "denied")
}

func skipMessage(message string) string {
	return strings.Replace(message, "real serial:", "real serial: skipped,", 1)
}
