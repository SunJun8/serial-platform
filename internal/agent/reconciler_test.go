package agent_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"serial-platform/internal/agent"
	"serial-platform/internal/serial"
)

func TestReconcilerStartsWorkerForMatchingChannel(t *testing.T) {
	backendFactory := newFakeBackendFactory()
	reconciler := agent.NewReconciler(agent.ReconcilerConfig{BackendFactory: backendFactory})

	channels := []agent.ChannelConfig{{
		ID:            "channel-1",
		IDPath:        "id-path-1",
		Status:        "offline",
		DefaultConfig: serial.DefaultConfig(),
	}}
	devices := []agent.DiscoveredDevice{{
		DevName:      "/dev/ttyUSB0",
		IDPath:       "id-path-1",
		PermissionOK: true,
	}}

	result := reconciler.Reconcile(context.Background(), channels, devices)
	if len(result.Statuses) != 1 {
		t.Fatalf("len(Statuses) = %d, want 1", len(result.Statuses))
	}
	if result.Statuses[0].ChannelID != "channel-1" {
		t.Fatalf("ChannelID = %q, want channel-1", result.Statuses[0].ChannelID)
	}
	if result.Statuses[0].Status != "online" {
		t.Fatalf("Status = %q, want online", result.Statuses[0].Status)
	}
	if result.Statuses[0].DevName != "/dev/ttyUSB0" {
		t.Fatalf("DevName = %q, want /dev/ttyUSB0", result.Statuses[0].DevName)
	}
	if backendFactory.openedCount("/dev/ttyUSB0") != 1 {
		t.Fatalf("opened /dev/ttyUSB0 = %d, want 1", backendFactory.openedCount("/dev/ttyUSB0"))
	}
	if len(result.Events) != 1 {
		t.Fatalf("len(Events) = %d, want 1", len(result.Events))
	}

	result = reconciler.Reconcile(context.Background(), channels, devices)
	if result.Statuses[0].Status != "online" {
		t.Fatalf("Status after second reconcile = %q, want online", result.Statuses[0].Status)
	}
	if backendFactory.openedCount("/dev/ttyUSB0") != 1 {
		t.Fatalf("opened /dev/ttyUSB0 after second reconcile = %d, want 1", backendFactory.openedCount("/dev/ttyUSB0"))
	}
}

func TestReconcilerResolvesRFC2217ControlFromExistingWorker(t *testing.T) {
	backendFactory := newFakeBackendFactory()
	reconciler := agent.NewReconciler(agent.ReconcilerConfig{BackendFactory: backendFactory})
	config := serial.DefaultConfig()
	config.Baud = 57600
	channels := []agent.ChannelConfig{{
		ID:            "channel-1",
		IDPath:        "id-path-1",
		Status:        "offline",
		DefaultConfig: config,
	}}
	devices := []agent.DiscoveredDevice{{
		DevName:      "/dev/ttyUSB0",
		IDPath:       "id-path-1",
		PermissionOK: true,
	}}

	result := reconciler.Reconcile(context.Background(), channels, devices)
	if len(result.Statuses) != 1 || result.Statuses[0].Status != "online" {
		t.Fatalf("statuses = %+v, want one online status", result.Statuses)
	}

	control, gotConfig, err := reconciler.RFC2217Control(context.Background(), "channel-1")
	if err != nil {
		t.Fatalf("RFC2217Control returned error: %v", err)
	}
	if control == nil {
		t.Fatal("RFC2217Control returned nil control")
	}
	if gotConfig != config {
		t.Fatalf("RFC2217Control config = %+v, want %+v", gotConfig, config)
	}

	session, err := control.OpenControlSession(context.Background(), "test")
	if err != nil {
		t.Fatalf("OpenControlSession returned error: %v", err)
	}
	defer session.Close()
	if err := session.Write([]byte("AT\r")); err != nil {
		t.Fatalf("session.Write returned error: %v", err)
	}
	backend := backendFactory.backend("/dev/ttyUSB0")
	if backend == nil {
		t.Fatal("backend for /dev/ttyUSB0 was not opened")
	}
	backend.waitForWrite(t, []byte("AT\r"))
}

func TestReconcilerRefreshesRFC2217ControlConfigWhenDefaultConfigChanges(t *testing.T) {
	backendFactory := newFakeBackendFactory()
	reconciler := agent.NewReconciler(agent.ReconcilerConfig{BackendFactory: backendFactory})
	config := serial.DefaultConfig()
	config.Baud = 115200
	channels := []agent.ChannelConfig{{
		ID:            "channel-1",
		IDPath:        "id-path-1",
		Status:        "offline",
		DefaultConfig: config,
	}}
	devices := []agent.DiscoveredDevice{{
		DevName:      "/dev/ttyUSB0",
		IDPath:       "id-path-1",
		PermissionOK: true,
	}}

	result := reconciler.Reconcile(context.Background(), channels, devices)
	if len(result.Statuses) != 1 || result.Statuses[0].Status != "online" {
		t.Fatalf("first reconcile statuses = %+v, want one online status", result.Statuses)
	}
	_, gotConfig, err := reconciler.RFC2217Control(context.Background(), "channel-1")
	if err != nil {
		t.Fatalf("RFC2217Control after first reconcile returned error: %v", err)
	}
	if gotConfig.Baud != 115200 {
		t.Fatalf("RFC2217Control baud after first reconcile = %d, want 115200", gotConfig.Baud)
	}
	firstBackend := backendFactory.backend("/dev/ttyUSB0")
	if firstBackend == nil {
		t.Fatal("backend for /dev/ttyUSB0 was not opened")
	}

	channels[0].DefaultConfig.Baud = 57600
	result = reconciler.Reconcile(context.Background(), channels, devices)
	if len(result.Statuses) != 1 || result.Statuses[0].Status != "online" {
		t.Fatalf("second reconcile statuses = %+v, want one online status", result.Statuses)
	}
	_, gotConfig, err = reconciler.RFC2217Control(context.Background(), "channel-1")
	if err != nil {
		t.Fatalf("RFC2217Control after config change returned error: %v", err)
	}
	if gotConfig.Baud != 57600 {
		t.Fatalf("RFC2217Control baud after config change = %d, want 57600", gotConfig.Baud)
	}
	if backendFactory.openedCount("/dev/ttyUSB0") != 2 {
		t.Fatalf("opened /dev/ttyUSB0 after config change = %d, want 2", backendFactory.openedCount("/dev/ttyUSB0"))
	}
	if !firstBackend.waitClosed(time.Second) {
		t.Fatal("old backend was not closed after default config changed")
	}
}

func TestReconcilerRFC2217ControlReceivesSameRXEventsAsLogForwarding(t *testing.T) {
	backendFactory := newFakeBackendFactory()
	reconciler := agent.NewReconciler(agent.ReconcilerConfig{BackendFactory: backendFactory})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	channels := []agent.ChannelConfig{{
		ID:            "channel-1",
		IDPath:        "id-path-1",
		Status:        "offline",
		DefaultConfig: serial.DefaultConfig(),
	}}
	devices := []agent.DiscoveredDevice{{
		DevName:      "/dev/ttyUSB0",
		IDPath:       "id-path-1",
		PermissionOK: true,
	}}

	result := reconciler.Reconcile(ctx, channels, devices)
	if len(result.Statuses) != 1 || result.Statuses[0].Status != "online" {
		t.Fatalf("statuses = %+v, want one online status", result.Statuses)
	}
	if len(result.Events) != 1 {
		t.Fatalf("len(Events) = %d, want 1", len(result.Events))
	}
	logEvents := result.Events[0].Events

	control, _, err := reconciler.RFC2217Control(ctx, "channel-1")
	if err != nil {
		t.Fatalf("RFC2217Control returned error: %v", err)
	}
	session, err := control.OpenControlSession(ctx, "rfc2217")
	if err != nil {
		t.Fatalf("OpenControlSession returned error: %v", err)
	}
	defer session.Close()
	tunnelEvents := control.Events()

	backend := backendFactory.backend("/dev/ttyUSB0")
	if backend == nil {
		t.Fatal("backend for /dev/ttyUSB0 was not opened")
	}
	backend.injectRX(t, []byte("rx-data"))

	logEvent := readReconcilerEvent(t, logEvents)
	assertSerialEvent(t, logEvent, "rx-data")
	tunnelEvent := readReconcilerEvent(t, tunnelEvents)
	assertSerialEvent(t, tunnelEvent, "rx-data")
}

func TestReconcilerRFC2217ControlCloseUnsubscribesEventStream(t *testing.T) {
	backendFactory := newFakeBackendFactory()
	reconciler := agent.NewReconciler(agent.ReconcilerConfig{BackendFactory: backendFactory})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	channels := []agent.ChannelConfig{{
		ID:            "channel-1",
		IDPath:        "id-path-1",
		Status:        "offline",
		DefaultConfig: serial.DefaultConfig(),
	}}
	devices := []agent.DiscoveredDevice{{
		DevName:      "/dev/ttyUSB0",
		IDPath:       "id-path-1",
		PermissionOK: true,
	}}

	result := reconciler.Reconcile(ctx, channels, devices)
	if len(result.Statuses) != 1 || result.Statuses[0].Status != "online" {
		t.Fatalf("statuses = %+v, want one online status", result.Statuses)
	}
	if len(result.Events) != 1 {
		t.Fatalf("len(Events) = %d, want 1", len(result.Events))
	}
	logEvents := result.Events[0].Events

	control, _, err := reconciler.RFC2217Control(ctx, "channel-1")
	if err != nil {
		t.Fatalf("RFC2217Control returned error: %v", err)
	}
	session, err := control.OpenControlSession(ctx, "rfc2217")
	if err != nil {
		t.Fatalf("OpenControlSession returned error: %v", err)
	}
	_ = control.Events()
	if err := session.Close(); err != nil {
		t.Fatalf("session.Close returned error: %v", err)
	}

	backend := backendFactory.backend("/dev/ttyUSB0")
	if backend == nil {
		t.Fatal("backend for /dev/ttyUSB0 was not opened")
	}
	for i := 0; i < 70; i++ {
		payload := []byte{byte(i)}
		backend.injectRX(t, payload)
		event := readReconcilerEvent(t, logEvents)
		if string(event.Data) != string(payload) {
			t.Fatalf("event %d Data = %x, want %x", i, event.Data, payload)
		}
	}
}

func TestReconcilerRestartsExitedWorkerForMatchingChannel(t *testing.T) {
	backendFactory := newFakeBackendFactory()
	reconciler := agent.NewReconciler(agent.ReconcilerConfig{BackendFactory: backendFactory})
	channels := []agent.ChannelConfig{{
		ID:            "channel-1",
		IDPath:        "id-path-1",
		Status:        "offline",
		DefaultConfig: serial.DefaultConfig(),
	}}
	devices := []agent.DiscoveredDevice{{
		DevName:      "/dev/ttyUSB0",
		IDPath:       "id-path-1",
		PermissionOK: true,
	}}

	result := reconciler.Reconcile(context.Background(), channels, devices)
	if len(result.Events) != 1 {
		t.Fatalf("len(Events) = %d, want 1", len(result.Events))
	}
	firstBackend := backendFactory.backend("/dev/ttyUSB0")
	if firstBackend == nil {
		t.Fatal("backend for /dev/ttyUSB0 was not opened")
	}
	firstBackend.finishReads()
	if !firstBackend.waitReadDone(time.Second) {
		t.Fatal("worker did not exit after backend read EOF")
	}

	result = reconciler.Reconcile(context.Background(), channels, devices)
	if len(result.Statuses) != 1 || result.Statuses[0].Status != "online" {
		t.Fatalf("second reconcile statuses = %+v, want one online status", result.Statuses)
	}
	if backendFactory.openedCount("/dev/ttyUSB0") != 2 {
		t.Fatalf("opened /dev/ttyUSB0 after worker exit = %d, want 2", backendFactory.openedCount("/dev/ttyUSB0"))
	}
	if len(result.Events) != 1 {
		t.Fatalf("len(Events) after worker restart = %d, want 1", len(result.Events))
	}
	if backendFactory.backend("/dev/ttyUSB0") == firstBackend {
		t.Fatal("reconciler reused exited backend, want a newly opened backend")
	}
}

func TestReconcilerReportsCandidateForUnconfiguredDevice(t *testing.T) {
	reconciler := agent.NewReconciler(agent.ReconcilerConfig{BackendFactory: newFakeBackendFactory()})
	devices := []agent.DiscoveredDevice{{
		DevName:      "/dev/ttyUSB0",
		IDPath:       "id-path-1",
		PermissionOK: true,
	}}

	result := reconciler.Reconcile(context.Background(), nil, devices)
	if len(result.Statuses) != 0 {
		t.Fatalf("len(Statuses) = %d, want 0", len(result.Statuses))
	}
	if len(result.Candidates) != 1 {
		t.Fatalf("len(Candidates) = %d, want 1", len(result.Candidates))
	}
	if result.Candidates[0].DevName != "/dev/ttyUSB0" {
		t.Fatalf("candidate DevName = %q, want /dev/ttyUSB0", result.Candidates[0].DevName)
	}
}

func TestReconcilerReportsCandidateWhenOnlyIDPathTagIsConfigured(t *testing.T) {
	reconciler := agent.NewReconciler(agent.ReconcilerConfig{BackendFactory: newFakeBackendFactory()})
	channels := []agent.ChannelConfig{{
		ID:            "channel-1",
		IDPath:        "configured-id-path",
		IDPathTag:     "shared-id-path-tag",
		Status:        "disabled",
		DefaultConfig: serial.DefaultConfig(),
	}}
	devices := []agent.DiscoveredDevice{{
		DevName:      "/dev/ttyUSB0",
		IDPath:       "candidate-id-path",
		IDPathTag:    "shared-id-path-tag",
		PermissionOK: true,
	}}

	result := reconciler.Reconcile(context.Background(), channels, devices)
	if len(result.Candidates) != 1 {
		t.Fatalf("len(Candidates) = %d, want 1", len(result.Candidates))
	}
	if result.Candidates[0].IDPath != "candidate-id-path" {
		t.Fatalf("candidate IDPath = %q, want candidate-id-path", result.Candidates[0].IDPath)
	}
}

func TestReconcilerDoesNotStartWorkerWhenOnlyIDPathTagMatches(t *testing.T) {
	backendFactory := newFakeBackendFactory()
	reconciler := agent.NewReconciler(agent.ReconcilerConfig{BackendFactory: backendFactory})
	channels := []agent.ChannelConfig{{
		ID:            "channel-1",
		IDPath:        "configured-id-path",
		IDPathTag:     "shared-id-path-tag",
		Status:        "offline",
		DefaultConfig: serial.DefaultConfig(),
	}}
	devices := []agent.DiscoveredDevice{{
		DevName:      "/dev/ttyUSB0",
		IDPath:       "candidate-id-path",
		IDPathTag:    "shared-id-path-tag",
		PermissionOK: true,
	}}

	result := reconciler.Reconcile(context.Background(), channels, devices)
	if len(result.Statuses) != 1 {
		t.Fatalf("len(Statuses) = %d, want 1", len(result.Statuses))
	}
	if result.Statuses[0].Status != "offline" {
		t.Fatalf("Status = %q, want offline", result.Statuses[0].Status)
	}
	if backendFactory.openedCount("/dev/ttyUSB0") != 0 {
		t.Fatalf("opened /dev/ttyUSB0 = %d, want 0", backendFactory.openedCount("/dev/ttyUSB0"))
	}
	if len(result.Candidates) != 1 {
		t.Fatalf("len(Candidates) = %d, want 1", len(result.Candidates))
	}
	if result.Candidates[0].IDPath != "candidate-id-path" {
		t.Fatalf("candidate IDPath = %q, want candidate-id-path", result.Candidates[0].IDPath)
	}
}

func TestReconcilerFallsBackToDevNameWhenIDPathMissing(t *testing.T) {
	backendFactory := newFakeBackendFactory()
	reconciler := agent.NewReconciler(agent.ReconcilerConfig{BackendFactory: backendFactory})
	channels := []agent.ChannelConfig{{
		ID:            "channel-1",
		DevName:       "/dev/ttyUSB0",
		IDPathTag:     "configured-id-path-tag",
		Status:        "offline",
		DefaultConfig: serial.DefaultConfig(),
	}}
	devices := []agent.DiscoveredDevice{{
		DevName:      "/dev/ttyUSB0",
		IDPath:       "candidate-id-path",
		IDPathTag:    "candidate-id-path-tag",
		PermissionOK: true,
	}}

	result := reconciler.Reconcile(context.Background(), channels, devices)
	if len(result.Statuses) != 1 {
		t.Fatalf("len(Statuses) = %d, want 1", len(result.Statuses))
	}
	if result.Statuses[0].Status != "online" {
		t.Fatalf("Status = %q, want online", result.Statuses[0].Status)
	}
	if result.Statuses[0].DevName != "/dev/ttyUSB0" {
		t.Fatalf("DevName = %q, want /dev/ttyUSB0", result.Statuses[0].DevName)
	}
	if backendFactory.openedCount("/dev/ttyUSB0") != 1 {
		t.Fatalf("opened /dev/ttyUSB0 = %d, want 1", backendFactory.openedCount("/dev/ttyUSB0"))
	}
}

func TestReconcilerDoesNotMatchStaleDevNameWhenIDPathConfigured(t *testing.T) {
	backendFactory := newFakeBackendFactory()
	reconciler := agent.NewReconciler(agent.ReconcilerConfig{BackendFactory: backendFactory})
	channels := []agent.ChannelConfig{{
		ID:            "channel-1",
		DevName:       "/dev/ttyUSB0",
		IDPath:        "id-path-1",
		Status:        "offline",
		DefaultConfig: serial.DefaultConfig(),
	}}
	devices := []agent.DiscoveredDevice{{
		DevName:      "/dev/ttyUSB0",
		IDPath:       "id-path-2",
		PermissionOK: true,
	}}

	result := reconciler.Reconcile(context.Background(), channels, devices)
	if len(result.Statuses) != 1 {
		t.Fatalf("len(Statuses) = %d, want 1", len(result.Statuses))
	}
	if result.Statuses[0].Status != "offline" {
		t.Fatalf("Status = %q, want offline", result.Statuses[0].Status)
	}
	if backendFactory.openedCount("/dev/ttyUSB0") != 0 {
		t.Fatalf("opened /dev/ttyUSB0 = %d, want 0", backendFactory.openedCount("/dev/ttyUSB0"))
	}
}

func TestReconcilerClosesWorkerWhenChannelDisabled(t *testing.T) {
	backendFactory := newFakeBackendFactory()
	reconciler := agent.NewReconciler(agent.ReconcilerConfig{BackendFactory: backendFactory})
	channels := []agent.ChannelConfig{{
		ID:            "channel-1",
		IDPath:        "id-path-1",
		Status:        "offline",
		DefaultConfig: serial.DefaultConfig(),
	}}
	devices := []agent.DiscoveredDevice{{
		DevName:      "/dev/ttyUSB0",
		IDPath:       "id-path-1",
		PermissionOK: true,
	}}

	result := reconciler.Reconcile(context.Background(), channels, devices)
	if len(result.Statuses) != 1 || result.Statuses[0].Status != "online" {
		t.Fatalf("first reconcile statuses = %+v, want one online status", result.Statuses)
	}
	if len(result.Events) != 1 {
		t.Fatalf("len(Events) = %d, want 1", len(result.Events))
	}
	events := result.Events[0].Events
	backend := backendFactory.backend("/dev/ttyUSB0")
	if backend == nil {
		t.Fatal("backend for /dev/ttyUSB0 was not opened")
	}

	channels[0].Status = "disabled"
	result = reconciler.Reconcile(context.Background(), channels, devices)
	if len(result.Statuses) != 1 {
		t.Fatalf("len(Statuses) = %d, want 1", len(result.Statuses))
	}
	if result.Statuses[0].Status != "disabled" {
		t.Fatalf("Status = %q, want disabled", result.Statuses[0].Status)
	}
	if !backend.waitClosed(time.Second) {
		t.Fatal("worker backend was not closed after channel disabled")
	}
	select {
	case _, ok := <-events:
		if ok {
			t.Fatal("worker event stream stayed open after channel disabled")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for worker event stream to close")
	}
}

func TestReconcilerClosesExitedWorkerWhenChannelDisabled(t *testing.T) {
	backendFactory := newFakeBackendFactory()
	reconciler := agent.NewReconciler(agent.ReconcilerConfig{BackendFactory: backendFactory})
	channels := []agent.ChannelConfig{{
		ID:            "channel-1",
		IDPath:        "id-path-1",
		Status:        "offline",
		DefaultConfig: serial.DefaultConfig(),
	}}
	devices := []agent.DiscoveredDevice{{
		DevName:      "/dev/ttyUSB0",
		IDPath:       "id-path-1",
		PermissionOK: true,
	}}

	result := reconciler.Reconcile(context.Background(), channels, devices)
	if len(result.Statuses) != 1 || result.Statuses[0].Status != "online" {
		t.Fatalf("first reconcile statuses = %+v, want one online status", result.Statuses)
	}
	backend := backendFactory.backend("/dev/ttyUSB0")
	if backend == nil {
		t.Fatal("backend for /dev/ttyUSB0 was not opened")
	}
	backend.finishReads()
	if !backend.waitReadDone(time.Second) {
		t.Fatal("worker did not exit after backend read EOF")
	}

	channels[0].Status = "disabled"
	result = reconciler.Reconcile(context.Background(), channels, devices)
	if len(result.Statuses) != 1 || result.Statuses[0].Status != "disabled" {
		t.Fatalf("disabled reconcile statuses = %+v, want one disabled status", result.Statuses)
	}
	if !backend.waitClosed(time.Second) {
		t.Fatal("exited worker backend was not closed after channel disabled")
	}
}

func TestReconcilerReportsErrorForPermissionDeniedDevice(t *testing.T) {
	backendFactory := newFakeBackendFactory()
	reconciler := agent.NewReconciler(agent.ReconcilerConfig{BackendFactory: backendFactory})
	channels := []agent.ChannelConfig{{
		ID:            "channel-1",
		IDPath:        "id-path-1",
		Status:        "offline",
		DefaultConfig: serial.DefaultConfig(),
	}}
	devices := []agent.DiscoveredDevice{{
		DevName:      "/dev/ttyUSB0",
		IDPath:       "id-path-1",
		PermissionOK: false,
		ErrorMessage: "permission denied",
	}}

	result := reconciler.Reconcile(context.Background(), channels, devices)
	if len(result.Statuses) != 1 {
		t.Fatalf("len(Statuses) = %d, want 1", len(result.Statuses))
	}
	if result.Statuses[0].Status != "error" {
		t.Fatalf("Status = %q, want error", result.Statuses[0].Status)
	}
	if !strings.Contains(result.Statuses[0].ErrorMessage, "permission denied") {
		t.Fatalf("ErrorMessage = %q, want permission denied", result.Statuses[0].ErrorMessage)
	}
	if backendFactory.openedCount("/dev/ttyUSB0") != 0 {
		t.Fatalf("opened /dev/ttyUSB0 = %d, want 0", backendFactory.openedCount("/dev/ttyUSB0"))
	}
}

func TestReconcilerReportsErrorWhenBackendOpenFails(t *testing.T) {
	backendFactory := newFakeBackendFactory()
	backendFactory.openErr = errors.New("open failed")
	reconciler := agent.NewReconciler(agent.ReconcilerConfig{BackendFactory: backendFactory})
	channels := []agent.ChannelConfig{{
		ID:            "channel-1",
		IDPath:        "id-path-1",
		Status:        "offline",
		DefaultConfig: serial.DefaultConfig(),
	}}
	devices := []agent.DiscoveredDevice{{
		DevName:      "/dev/ttyUSB0",
		IDPath:       "id-path-1",
		PermissionOK: true,
	}}

	result := reconciler.Reconcile(context.Background(), channels, devices)
	if len(result.Statuses) != 1 {
		t.Fatalf("len(Statuses) = %d, want 1", len(result.Statuses))
	}
	if result.Statuses[0].Status != "error" {
		t.Fatalf("Status = %q, want error", result.Statuses[0].Status)
	}
	if !strings.Contains(result.Statuses[0].ErrorMessage, "open failed") {
		t.Fatalf("ErrorMessage = %q, want open failed", result.Statuses[0].ErrorMessage)
	}
}

type fakeBackendFactory struct {
	mu       sync.Mutex
	opened   map[string]int
	backends map[string]*reconcilerFakeBackend
	openErr  error
}

func newFakeBackendFactory() *fakeBackendFactory {
	return &fakeBackendFactory{
		opened:   make(map[string]int),
		backends: make(map[string]*reconcilerFakeBackend),
	}
}

func (f *fakeBackendFactory) Open(devName string, _ serial.Config) (serial.Backend, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opened[devName]++
	if f.openErr != nil {
		return nil, f.openErr
	}
	backend := newReconcilerFakeBackend()
	f.backends[devName] = backend
	return backend, nil
}

func (f *fakeBackendFactory) openedCount(devName string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.opened[devName]
}

func (f *fakeBackendFactory) backend(devName string) *reconcilerFakeBackend {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.backends[devName]
}

type reconcilerFakeBackend struct {
	rx        chan []byte
	closed    chan struct{}
	readDone  chan struct{}
	mu        sync.Mutex
	writes    [][]byte
	readEOF   bool
	closeEOF  bool
	closeOnce sync.Once
	readOnce  sync.Once
}

func newReconcilerFakeBackend() *reconcilerFakeBackend {
	return &reconcilerFakeBackend{
		rx:       make(chan []byte),
		closed:   make(chan struct{}),
		readDone: make(chan struct{}),
	}
}

func (b *reconcilerFakeBackend) ApplyConfig(serial.Config) error { return nil }

func (b *reconcilerFakeBackend) SetDTR(bool) error { return nil }

func (b *reconcilerFakeBackend) SetRTS(bool) error { return nil }

func (b *reconcilerFakeBackend) SendBreak(time.Duration) error { return nil }

func (b *reconcilerFakeBackend) Read(buf []byte) (int, error) {
	defer b.readOnce.Do(func() { close(b.readDone) })
	select {
	case data, ok := <-b.rx:
		if !ok {
			return 0, io.EOF
		}
		return copy(buf, data), nil
	case <-b.closed:
		return 0, io.EOF
	}
}

func (b *reconcilerFakeBackend) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.writes = append(b.writes, append([]byte(nil), data...))
	return len(data), nil
}

func (b *reconcilerFakeBackend) Close() error {
	b.closeOnce.Do(func() {
		close(b.closed)
		b.mu.Lock()
		defer b.mu.Unlock()
		if !b.readEOF {
			b.closeEOF = true
			close(b.rx)
		}
	})
	return nil
}

func (b *reconcilerFakeBackend) finishReads() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.readEOF || b.closeEOF {
		return
	}
	b.readEOF = true
	close(b.rx)
}

func (b *reconcilerFakeBackend) waitReadDone(timeout time.Duration) bool {
	select {
	case <-b.readDone:
		return true
	case <-time.After(timeout):
		return false
	}
}

func (b *reconcilerFakeBackend) waitClosed(timeout time.Duration) bool {
	select {
	case <-b.closed:
		return true
	case <-time.After(timeout):
		return false
	}
}

func (b *reconcilerFakeBackend) injectRX(t *testing.T, data []byte) {
	t.Helper()
	select {
	case b.rx <- append([]byte(nil), data...):
	case <-time.After(time.Second):
		t.Fatalf("timeout injecting RX %q", data)
	}
}

func (b *reconcilerFakeBackend) waitForWrite(t *testing.T, want []byte) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		b.mu.Lock()
		for _, got := range b.writes {
			if string(got) == string(want) {
				b.mu.Unlock()
				return
			}
		}
		b.mu.Unlock()
		time.Sleep(time.Millisecond)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	t.Fatalf("writes = %q, want %q", b.writes, want)
}

func readReconcilerEvent(t *testing.T, events <-chan serial.Event) serial.Event {
	t.Helper()
	select {
	case event, ok := <-events:
		if !ok {
			t.Fatal("event stream closed before RX event")
		}
		return event
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for RX event")
		return serial.Event{}
	}
}

func assertSerialEvent(t *testing.T, event serial.Event, wantPayload string) {
	t.Helper()
	if event.ChannelID != "channel-1" {
		t.Fatalf("event ChannelID = %q, want channel-1", event.ChannelID)
	}
	if event.Direction != serial.DirectionRX {
		t.Fatalf("event Direction = %v, want RX", event.Direction)
	}
	if string(event.Data) != wantPayload {
		t.Fatalf("event Data = %q, want %q", event.Data, wantPayload)
	}
}
