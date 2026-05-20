# Serial Platform Real Device Workflow Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Complete the real serial-device workflow so `/dev/ttyUSB0` loopback can be discovered, confirmed, opened by host-agent, logged by central-server, controlled from Web terminal, and accessed through central-server RFC2217.

**Architecture:** Keep host-agent as the only physical serial owner. central-server owns metadata, Web/API, control ownership, tunnel pairing, log storage, and public RFC2217 TCP listeners; host-agent owns Linux device discovery, serial workers, TX/RX events, and RFC2217 parsing against local serial sessions. All agent-server communication remains outbound from host-agent through WebSocket channels.

**Tech Stack:** Go 1.25 module, `nhooyr.io/websocket`, `go.bug.st/serial`, `modernc.org/sqlite`, React/Vite/TypeScript, lucide-react, shell install scripts, non-blocking real loopback test on `/dev/ttyUSB0`.

---

## Execution Rules

- Use LSP first for code navigation when available. In this repository Go LSP may return no symbols; if so, fall back to `rg`, `sed`, and direct source reads.
- Use TDD for implementation tasks: write the failing test, run it, implement the minimum code, rerun targeted tests, then run broader verification.
- Implementation subagents must be spawned with `model: gpt-5.5` and `reasoning_effort: xhigh`.
- Do not run host-agent with `sudo` as the normal path. If permissions fail, diagnostics must recommend adding the user to `dialout`.
- Keep responsibilities narrow:
  - `internal/serial` owns serial sessions and event generation only.
  - `internal/agent` owns discovery, reconciliation, worker lifecycle, log upload, tunnel client, and local RFC2217 application.
  - `internal/server` owns HTTP API, metadata orchestration, control owner, tunnel registry, Web terminal, RFC2217 public listeners, log storage, and shutdown.
  - `internal/protocol` owns stable wire message structs.
  - `web` owns UI state and API/WS client code only.

## File Map

### Create

- `internal/agent/discovery.go` - scan `/dev/ttyUSB*` and `/dev/ttyACM*`, run `udevadm`, normalize discovered devices, and diagnose permissions.
- `internal/agent/discovery_test.go` - fake scanner/udevadm/permission tests.
- `internal/agent/reconciler.go` - match server channel config to discovered devices and manage serial workers.
- `internal/agent/reconciler_test.go` - worker lifecycle, offline/candidate/status behavior.
- `internal/agent/log_uploader.go` - convert worker events to `protocol.LogFrame` and send them to `/ws/logs`.
- `internal/agent/log_uploader_test.go` - sequence numbers, RX/TX conversion, reconnect-safe channel behavior.
- `internal/agent/tunnel.go` - agent-side tunnel WebSocket client and local serial/RFC2217 bridge.
- `internal/agent/tunnel_test.go` - terminal and RFC2217 tunnel behavior against fake serial control.
- `internal/server/tunnel_registry.go` - pair server-created tunnel IDs with agent WebSocket connections.
- `internal/server/tunnel_registry_test.go` - pairing, timeout, close, and no-leak behavior.
- `internal/server/channel_api_test.go` - channel create/update/enable/disable/candidate confirmation API tests.
- `internal/server/shutdown.go` - reusable HTTP graceful shutdown helper.
- `cmd/central-server/main_test.go` - process-level shutdown smoke for SIGINT.
- `internal/e2e/doc.go` - package stub so `make test` can reference `./internal/e2e` before the real test lands.
- `internal/e2e/real_serial_test.go` - non-blocking and strict `/dev/ttyUSB0` loopback tests.

### Modify

- `Makefile` - after Task 10, add non-blocking real serial test to `make test`, plus strict `make test-real-serial`.
- `cmd/central-server/main.go` - use root context, `http.Server`, graceful shutdown, and coordinated RFC2217 shutdown.
- `cmd/host-agent/main.go` - add scan interval, permission diagnostics, and start the agent runtime loop after registration.
- `internal/protocol/messages.go` - add device, candidate, channel sync, tunnel, and terminal session messages.
- `internal/storage/models.go` - add candidate model, channel error/dev fields, flow, and status `error`.
- `internal/storage/migrations.go` - add candidate table and additive migrations for existing channel columns.
- `internal/storage/db.go` - add CRUD methods for candidates and writable channel operations.
- `internal/storage/db_test.go` - cover migrations, candidates, and channel writes.
- `internal/server/api.go` - implement writable channel and candidate endpoints.
- `internal/server/agent_registry.go` - make active agent connection usable for sending control messages safely.
- `internal/server/agent_ws.go` - keep the connection as a bidirectional control protocol, process device snapshots and statuses.
- `internal/server/rfc2217_manager.go` - start public listeners from channel metadata, using tunnel resolver.
- `internal/server/rfc2217_listener.go` - bridge public TCP to tunnel instead of local `SerialControl` in production path while preserving local test helper.
- `internal/server/server.go` - route new APIs and `/ws/tunnel/{tunnelID}`.
- `internal/server/web_terminal.go` - route Web terminal commands through an agent terminal tunnel/session, preserving server-side ownership.
- `internal/server/live_log.go` - leave read-only live log path independent from control owner.
- `internal/topology/identity.go` - parse `DEVNAME`, interface number, serial, and stable diagnostic fields.
- `scripts/install-agent.sh` - support `--user`, configure `dialout`, and write a non-root systemd service.
- `web/src/api.ts` - typed GET/POST/PATCH helpers, download helper, WS URL helper.
- `web/src/App.tsx` - real candidates/channels/forms/terminal/logs flows.
- `web/src/styles.css` - fix Logs layout overlap and improve internal-tool usability.
- `README.md` - document real-device manual test and permissions.

## Task 1: Graceful Shutdown and Test Entry Points

**Files:**
- Create: `internal/server/shutdown.go`
- Create: `cmd/central-server/main_test.go`
- Modify: `cmd/central-server/main.go`
- Modify: `Makefile`

- [ ] **Step 1: Write the failing shutdown helper test**

Add `internal/server/shutdown_test.go`:

```go
package server

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestServeHTTPShutsDownWhenContextCancels(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen returned error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	httpServer := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})}

	done := make(chan error, 1)
	go func() {
		done <- ServeHTTPWithShutdown(ctx, httpServer, listener, 100*time.Millisecond)
	}()

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ServeHTTPWithShutdown returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ServeHTTPWithShutdown did not return after context cancellation")
	}
}
```

- [ ] **Step 2: Run the failing test**

Run:

```bash
go test ./internal/server -run TestServeHTTPShutsDownWhenContextCancels -count=1
```

Expected: fail with `undefined: ServeHTTPWithShutdown`.

- [ ] **Step 3: Implement the shutdown helper**

Create `internal/server/shutdown.go`:

```go
package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"
)

func ServeHTTPWithShutdown(ctx context.Context, srv *http.Server, listener net.Listener, timeout time.Duration) error {
	errCh := make(chan error, 1)
	go func() {
		err := srv.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errCh <- err
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			_ = srv.Close()
		}
		err := <-errCh
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
```

- [ ] **Step 4: Refactor `cmd/central-server/main.go`**

Change `main` to:

1. create `signal.NotifyContext`.
2. create `net.Listener` for `--listen`.
3. create `http.Server{Handler: handler}`.
4. start `handler.ServeRFC2217(ctx, *rfc2217Bind)` in a goroutine.
5. call `server.ServeHTTPWithShutdown(ctx, httpServer, listener, 5*time.Second)`.

The important shape:

```go
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer stop()

listener, err := net.Listen("tcp", *listen)
if err != nil {
	log.Fatalf("listen: %v", err)
}

httpServer := &http.Server{Handler: handler}
go func() {
	if err := handler.ServeRFC2217(ctx, *rfc2217Bind); err != nil {
		log.Printf("rfc2217 listener stopped: %v", err)
	}
}()

log.Printf("central-server %s %s %s listening on %s", buildinfo.Version, buildinfo.Commit, buildinfo.Date, listener.Addr())
if err := server.ServeHTTPWithShutdown(ctx, httpServer, listener, 5*time.Second); err != nil {
	log.Fatalf("listen and serve: %v", err)
}
```

- [ ] **Step 5: Add a process-level SIGINT test**

Add `cmd/central-server/main_test.go` with a short subprocess test that starts `central-server` on `127.0.0.1:0`, sends `os.Interrupt`, and expects exit within 2 seconds. Use `exec.CommandContext` and skip on platforms where interrupt delivery is not available.

Use this pattern:

```go
func TestCentralServerExitsOnInterrupt(t *testing.T) {
	if os.Getenv("SERIAL_PLATFORM_CENTRAL_HELPER") == "1" {
		main()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestCentralServerExitsOnInterrupt")
	cmd.Env = append(os.Environ(), "SERIAL_PLATFORM_CENTRAL_HELPER=1")
	cmd.Dir = t.TempDir()
	if err := cmd.Start(); err != nil {
		t.Fatalf("cmd.Start returned error: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("Signal returned error: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("central-server did not exit cleanly: %v", err)
	}
}
```

Factor command execution into `run(args []string) error`, reset each `flag.FlagSet` inside `run`, and have `main` call `run(os.Args[1:])`. The subprocess test must call the real `main` path only in the helper process.

- [ ] **Step 6: Keep test target stable before real-serial tests exist**

Modify `Makefile`:

```makefile
.PHONY: test test-unit build fmt web

test: test-unit

test-unit:
	go test ./...
```

Create `internal/e2e/doc.go` in this task so `go test ./...` can resolve the package before Task 10 adds the real loopback test:

```go
package e2e
```

Do not wire `test-real-serial-soft` into the default `test` target until Task 10 adds `TestRealSerialLoopback`. In this task only define `test-unit`; Task 10 will make `test` depend on the non-blocking real-device target.

- [ ] **Step 7: Verify and commit**

Run:

```bash
go test ./internal/server ./cmd/central-server -run 'TestServeHTTPShutsDownWhenContextCancels|TestCentralServerExitsOnInterrupt' -count=1
go test ./...
```

Expected: targeted tests pass; full `go test ./...` passes.

Commit:

```bash
git add Makefile cmd/central-server/main.go cmd/central-server/main_test.go internal/server/shutdown.go internal/server/shutdown_test.go internal/e2e/doc.go
git commit -m "fix: add graceful server shutdown"
```

## Task 2: Storage Models and Writable Channel APIs

**Files:**
- Modify: `internal/storage/models.go`
- Modify: `internal/storage/migrations.go`
- Modify: `internal/storage/db.go`
- Modify: `internal/storage/db_test.go`
- Create: `internal/server/channel_api_test.go`
- Modify: `internal/server/api.go`
- Modify: `internal/server/server.go`

- [ ] **Step 1: Write storage tests for candidates and channel writes**

Extend `internal/storage/db_test.go`:

```go
func TestDBUpsertsCandidatesAndConfirmsChannel(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "meta.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	now := time.Unix(1700000000, 0).UTC()
	candidate := Candidate{
		ID:             "candidate-1",
		AgentID:        "agent-1",
		DevName:        "/dev/ttyUSB0",
		IDPath:         "pci-0000:00:14.0-usb-0:1.2:1.0",
		IDPathTag:      "pci-0000_00_14_0-usb-0_1_2_1_0",
		SysfsDevpath:   "/devices/pci/ttyUSB0",
		Interface:      "00",
		VID:            "1a86",
		PID:            "7523",
		Serial:         "serial-a",
		Driver:         "ch341",
		FirstSeen:      now,
		LastSeen:       now,
	}
	if err := db.UpsertCandidate(candidate); err != nil {
		t.Fatalf("UpsertCandidate returned error: %v", err)
	}
	candidates, err := db.ListCandidates()
	if err != nil {
		t.Fatalf("ListCandidates returned error: %v", err)
	}
	if len(candidates) != 1 || candidates[0].IDPath != candidate.IDPath {
		t.Fatalf("candidates = %+v", candidates)
	}

	channel := Channel{
		ID:              "channel-1",
		AgentID:         "agent-1",
		AutoName:        "agent-1.if00",
		Alias:           "loopback",
		Role:            "console",
		DevName:         "/dev/ttyUSB0",
		IDPath:          candidate.IDPath,
		IDPathTag:       candidate.IDPathTag,
		SysfsDevpath:    candidate.SysfsDevpath,
		RFC2217Port:     7001,
		Status:          ChannelStatusOffline,
		DefaultBaud:     115200,
		DefaultDataBits: 8,
		DefaultParity:   "N",
		DefaultStopBits: 1,
		DefaultFlow:     "none",
		UpdatedAt:       now,
	}
	if err := db.UpsertChannel(channel); err != nil {
		t.Fatalf("UpsertChannel returned error: %v", err)
	}
	if err := db.DeleteCandidate(candidate.ID); err != nil {
		t.Fatalf("DeleteCandidate returned error: %v", err)
	}
	candidates, err = db.ListCandidates()
	if err != nil {
		t.Fatalf("ListCandidates returned error: %v", err)
	}
	if len(candidates) != 0 {
		t.Fatalf("len(candidates) = %d, want 0", len(candidates))
	}
}

func TestDBUpdatesChannelStatusAndConfig(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "meta.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	channel := testChannel("channel-1")
	if err := db.UpsertChannel(channel); err != nil {
		t.Fatalf("UpsertChannel returned error: %v", err)
	}
	if err := db.UpdateChannelStatus("channel-1", ChannelStatusError, "/dev/ttyUSB0", "permission denied", time.Unix(2, 0).UTC()); err != nil {
		t.Fatalf("UpdateChannelStatus returned error: %v", err)
	}
	got, err := db.GetChannel("channel-1")
	if err != nil {
		t.Fatalf("GetChannel returned error: %v", err)
	}
	if got.Status != ChannelStatusError || got.ErrorMessage != "permission denied" || got.DevName != "/dev/ttyUSB0" {
		t.Fatalf("channel = %+v", got)
	}
}

func testChannel(id string) Channel {
	return Channel{
		ID:              id,
		AgentID:         "agent-1",
		AutoName:        "agent-1.if00",
		Alias:           "loopback",
		Role:            "console",
		IDPath:          "id-path",
		IDPathTag:       "id-tag",
		RFC2217Port:     7001,
		Status:          ChannelStatusOffline,
		DefaultBaud:     115200,
		DefaultDataBits: 8,
		DefaultParity:   "N",
		DefaultStopBits: 1,
		DefaultFlow:     "none",
		UpdatedAt:       time.Unix(1, 0).UTC(),
	}
}
```

- [ ] **Step 2: Run storage tests and confirm failure**

Run:

```bash
go test ./internal/storage -run 'TestDBUpsertsCandidatesAndConfirmsChannel|TestDBUpdatesChannelStatusAndConfig' -count=1
```

Expected: fail with undefined `Candidate`, new fields, and new DB methods.

- [ ] **Step 3: Extend models**

Modify `internal/storage/models.go`:

```go
const (
	ChannelStatusOnline   ChannelStatus = "online"
	ChannelStatusOffline  ChannelStatus = "offline"
	ChannelStatusBusy     ChannelStatus = "busy"
	ChannelStatusDisabled ChannelStatus = "disabled"
	ChannelStatusError    ChannelStatus = "error"
)

type Candidate struct {
	ID             string
	AgentID        string
	DevName        string
	IDPath         string
	IDPathTag      string
	SysfsDevpath   string
	Interface      string
	VID            string
	PID            string
	Serial         string
	Driver         string
	Manufacturer   string
	Product        string
	FirstSeen      time.Time
	LastSeen       time.Time
}
```

Add these fields to `Channel`:

```go
DevName      string
DefaultFlow  string
ErrorMessage string
```

- [ ] **Step 4: Add additive schema migration**

Modify `internal/storage/migrations.go`:

1. Add `dev_name`, `default_flow`, and `error_message` to the `channels` create table statement.
2. Add `candidates` table.
3. After `schemaSQL`, run an `ensureSchema` function from `Open` that performs:

```sql
ALTER TABLE channels ADD COLUMN dev_name TEXT NOT NULL DEFAULT '';
ALTER TABLE channels ADD COLUMN default_flow TEXT NOT NULL DEFAULT 'none';
ALTER TABLE channels ADD COLUMN error_message TEXT NOT NULL DEFAULT '';
```

Each `ALTER TABLE` must ignore duplicate-column errors.

The candidate table:

```sql
CREATE TABLE IF NOT EXISTS candidates (
  id TEXT PRIMARY KEY,
  agent_id TEXT NOT NULL,
  dev_name TEXT NOT NULL,
  id_path TEXT NOT NULL,
  id_path_tag TEXT NOT NULL,
  sysfs_devpath TEXT NOT NULL,
  interface TEXT NOT NULL,
  vid TEXT NOT NULL,
  pid TEXT NOT NULL,
  serial TEXT NOT NULL,
  driver TEXT NOT NULL,
  manufacturer TEXT NOT NULL,
  product TEXT NOT NULL,
  first_seen TEXT NOT NULL,
  last_seen TEXT NOT NULL
);
```

- [ ] **Step 5: Add DB methods**

Add to `internal/storage/db.go`:

```go
func (db *DB) GetChannel(id string) (Channel, error)
func (db *DB) UpdateChannelStatus(id string, status ChannelStatus, devName, errorMessage string, updatedAt time.Time) error
func (db *DB) DeleteChannel(id string) error
func (db *DB) UpsertCandidate(candidate Candidate) error
func (db *DB) ListCandidates() ([]Candidate, error)
func (db *DB) GetCandidate(id string) (Candidate, error)
func (db *DB) DeleteCandidate(id string) error
func (db *DB) DeleteCandidatesByAgent(agentID string) error
```

Update `UpsertChannel` and `ListChannels` to include `dev_name`, `default_flow`, and `error_message`.

- [ ] **Step 6: Write server API tests**

Create `internal/server/channel_api_test.go`:

```go
func TestChannelAPICreatesAndUpdatesChannel(t *testing.T) {
	db := newAPITestDB(t)
	srv := server.New(server.ServerConfig{DB: db})
	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)

	body := `{"agent_id":"agent-1","alias":"loopback","role":"console","id_path":"pci-path","id_path_tag":"pci-tag","rfc2217_port":7001,"default_baud":115200,"default_data_bits":8,"default_parity":"N","default_stop_bits":1,"default_flow":"none"}`
	resp := postJSON(t, httpSrv.URL+"/api/channels", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /api/channels status = %s", resp.Status)
	}

	resp = patchJSON(t, httpSrv.URL+"/api/channels/channel-1", `{"alias":"renamed","default_baud":921600}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH /api/channels status = %s", resp.Status)
	}
}

func TestCandidateConfirmCreatesChannelAndDeletesCandidate(t *testing.T) {
	db := newAPITestDB(t)
	now := time.Unix(10, 0).UTC()
	if err := db.UpsertCandidate(storage.Candidate{ID: "cand-1", AgentID: "agent-1", DevName: "/dev/ttyUSB0", IDPath: "id-path", IDPathTag: "id-tag", FirstSeen: now, LastSeen: now}); err != nil {
		t.Fatalf("UpsertCandidate returned error: %v", err)
	}
	srv := server.New(server.ServerConfig{DB: db})
	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)

	resp := postJSON(t, httpSrv.URL+"/api/candidates/cand-1/confirm", `{"alias":"loopback","role":"console","rfc2217_port":7001,"default_baud":115200}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("confirm status = %s", resp.Status)
	}
	candidates, err := db.ListCandidates()
	if err != nil {
		t.Fatalf("ListCandidates returned error: %v", err)
	}
	if len(candidates) != 0 {
		t.Fatalf("candidate was not deleted: %+v", candidates)
	}
}

func newAPITestDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "meta.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func postJSON(t *testing.T, url string, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("http.Post returned error: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func patchJSON(t *testing.T, url string, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPatch, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest returned error: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do returned error: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}
```

Use helper functions that read and close response bodies.

- [ ] **Step 7: Implement API handlers**

Add handlers:

```text
POST /api/channels
PATCH /api/channels/{channelID}
POST /api/channels/{channelID}/enable
POST /api/channels/{channelID}/disable
GET /api/candidates
POST /api/candidates/{candidateID}/confirm
```

Implementation details:

- Generate channel IDs with `uuid.NewString()`.
- Generate `AutoName` as `<agentID>.<interface-or-if00>`.
- Default serial config: 115200 8N1 flow none.
- `enable` sets status to `offline`; `disable` sets status to `disabled`.
- Confirming candidate copies candidate identity fields to the channel, then deletes the candidate.

- [ ] **Step 8: Verify and commit**

Run:

```bash
go test ./internal/storage ./internal/server -run 'TestDBUpsertsCandidates|TestDBUpdatesChannel|TestChannelAPI|TestCandidateConfirm' -count=1
go test ./...
```

Expected: all tests pass.

Commit:

```bash
git add internal/storage internal/server
git commit -m "feat: add channel and candidate APIs"
```

## Task 3: Device Discovery and Permission Diagnostics

**Files:**
- Create: `internal/agent/discovery.go`
- Create: `internal/agent/discovery_test.go`
- Modify: `internal/topology/identity.go`
- Modify: `internal/topology/topology_test.go`

- [ ] **Step 1: Write discovery tests**

Create `internal/agent/discovery_test.go`:

```go
func TestDiscoverDevicesParsesTTYUSBAndTTYACM(t *testing.T) {
	devDir := t.TempDir()
	ttyUSB := filepath.Join(devDir, "ttyUSB0")
	ttyACM := filepath.Join(devDir, "ttyACM0")
	if err := os.WriteFile(ttyUSB, []byte{}, 0o666); err != nil {
		t.Fatalf("WriteFile ttyUSB returned error: %v", err)
	}
	if err := os.WriteFile(ttyACM, []byte{}, 0o666); err != nil {
		t.Fatalf("WriteFile ttyACM returned error: %v", err)
	}

	runner := fakeUdevRunner{props: map[string]string{
		ttyUSB: "DEVNAME=" + ttyUSB + "\nID_PATH=pci-usb-0:1.1:1.0\nID_PATH_TAG=pci-usb-0_1_1_1_0\nID_USB_INTERFACE_NUM=00\nID_VENDOR_ID=1a86\nID_MODEL_ID=7523\n",
		ttyACM: "DEVNAME=" + ttyACM + "\nID_PATH=pci-usb-0:1.2:1.0\nID_PATH_TAG=pci-usb-0_1_2_1_0\nID_USB_INTERFACE_NUM=01\n",
	}}

	devices, err := DiscoverDevices(DiscoveryConfig{DevDir: devDir, Udev: runner})
	if err != nil {
		t.Fatalf("DiscoverDevices returned error: %v", err)
	}
	if len(devices) != 2 {
		t.Fatalf("len(devices) = %d, want 2", len(devices))
	}
	if devices[0].IDPath == "" || devices[0].DevName == "" {
		t.Fatalf("device missing identity: %+v", devices[0])
	}
}

func TestPermissionAdviceForUnreadableDevice(t *testing.T) {
	advice := PermissionAdvice("/dev/ttyUSB0", "miot")
	if !strings.Contains(advice, "usermod -aG dialout miot") {
		t.Fatalf("advice missing dialout command: %s", advice)
	}
}

type fakeUdevRunner struct {
	props map[string]string
}

func (r fakeUdevRunner) Info(devName string) (string, error) {
	props, ok := r.props[devName]
	if !ok {
		return "", fmt.Errorf("missing fake udev props for %s", devName)
	}
	return props, nil
}
```

- [ ] **Step 2: Run the failing tests**

Run:

```bash
go test ./internal/agent ./internal/topology -run 'TestDiscoverDevices|TestPermissionAdvice|TestParseUdevProperties' -count=1
```

Expected: fail with undefined discovery types and missing extended topology fields.

- [ ] **Step 3: Extend topology identity**

Modify `internal/topology/identity.go`:

```go
type USBIdentity struct {
	DevName       string
	IDPath        string
	IDPathTag     string
	SysfsDevpath  string
	Interface     string
	VID           string
	PID           string
	Serial        string
	Driver        string
	Manufacturer  string
	Product       string
}
```

Handle keys:

```text
DEVNAME
ID_USB_INTERFACE_NUM
ID_SERIAL_SHORT
ID_USB_DRIVER
```

- [ ] **Step 4: Implement discovery**

Create `internal/agent/discovery.go`:

```go
package agent

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"serial-platform/internal/topology"
)

type DiscoveredDevice struct {
	DevName       string
	IDPath        string
	IDPathTag     string
	SysfsDevpath  string
	Interface     string
	VID           string
	PID           string
	Serial        string
	Driver        string
	Manufacturer  string
	Product       string
	PermissionOK  bool
	ErrorMessage  string
}

type UdevRunner interface {
	Info(devName string) (string, error)
}

type ExecUdevRunner struct{}

func (ExecUdevRunner) Info(devName string) (string, error) {
	out, err := exec.Command("udevadm", "info", "-q", "property", "-n", devName).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("udevadm info %s: %w: %s", devName, err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

type DiscoveryConfig struct {
	DevDir string
	Udev   UdevRunner
	User   string
}

func DiscoverDevices(config DiscoveryConfig) ([]DiscoveredDevice, error) {
	devDir := config.DevDir
	if devDir == "" {
		devDir = "/dev"
	}
	udev := config.Udev
	if udev == nil {
		udev = ExecUdevRunner{}
	}

	paths := make([]string, 0)
	for _, pattern := range []string{"ttyUSB*", "ttyACM*"} {
		matches, err := filepath.Glob(filepath.Join(devDir, pattern))
		if err != nil {
			return nil, err
		}
		paths = append(paths, matches...)
	}
	sort.Strings(paths)

	devices := make([]DiscoveredDevice, 0, len(paths))
	for _, path := range paths {
		props, err := udev.Info(path)
		if err != nil {
			devices = append(devices, DiscoveredDevice{DevName: path, PermissionOK: canReadWrite(path), ErrorMessage: err.Error()})
			continue
		}
		identity := topology.ParseUdevProperties(props)
		devName := identity.DevName
		if devName == "" {
			devName = path
		}
		device := DiscoveredDevice{
			DevName:      devName,
			IDPath:       identity.IDPath,
			IDPathTag:    identity.IDPathTag,
			SysfsDevpath: identity.SysfsDevpath,
			Interface:    identity.Interface,
			VID:          identity.VID,
			PID:          identity.PID,
			Serial:       identity.Serial,
			Driver:       identity.Driver,
			Manufacturer: identity.Manufacturer,
			Product:      identity.Product,
			PermissionOK: canReadWrite(path),
		}
		if !device.PermissionOK {
			device.ErrorMessage = PermissionAdvice(path, config.User)
		}
		devices = append(devices, device)
	}
	return devices, nil
}

func canReadWrite(path string) bool {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_NONBLOCK, 0)
	if err != nil {
		return false
	}
	_ = file.Close()
	return true
}

func PermissionAdvice(devName, user string) string {
	if strings.TrimSpace(user) == "" {
		user = "$USER"
	}
	return fmt.Sprintf("serial device %s is not accessible by current user. Recommended: sudo usermod -aG dialout %s; newgrp dialout; or log out and log in again.", devName, user)
}

var ErrDevicePermission = errors.New("serial device permission denied")
```

- [ ] **Step 5: Verify and commit**

Run:

```bash
go test ./internal/agent ./internal/topology -run 'TestDiscoverDevices|TestPermissionAdvice|TestParseUdevProperties' -count=1
go test ./...
```

Commit:

```bash
git add internal/agent/discovery.go internal/agent/discovery_test.go internal/topology
git commit -m "feat: discover local serial devices"
```

## Task 4: Agent Reconciler, Serial Workers, and Log Upload

**Files:**
- Create: `internal/agent/reconciler.go`
- Create: `internal/agent/reconciler_test.go`
- Create: `internal/agent/log_uploader.go`
- Create: `internal/agent/log_uploader_test.go`
- Modify: `internal/agent/client.go`
- Modify: `cmd/host-agent/main.go`

- [ ] **Step 1: Write reconciler lifecycle tests**

Create `internal/agent/reconciler_test.go`:

```go
func TestReconcilerStartsWorkerForMatchingChannel(t *testing.T) {
	backendFactory := &fakeBackendFactory{}
	reconciler := NewReconciler(ReconcilerConfig{BackendFactory: backendFactory})

	channels := []ChannelConfig{{
		ID: "channel-1", IDPath: "id-path-1", Status: "offline",
		DefaultConfig: serial.DefaultConfig(),
	}}
	devices := []DiscoveredDevice{{DevName: "/dev/ttyUSB0", IDPath: "id-path-1", PermissionOK: true}}

	result := reconciler.Reconcile(context.Background(), channels, devices)
	if len(result.Statuses) != 1 || result.Statuses[0].Status != "online" {
		t.Fatalf("statuses = %+v", result.Statuses)
	}
	if backendFactory.opened["/dev/ttyUSB0"] != 1 {
		t.Fatalf("opened = %+v", backendFactory.opened)
	}
}

func TestReconcilerReportsCandidateForUnconfiguredDevice(t *testing.T) {
	reconciler := NewReconciler(ReconcilerConfig{BackendFactory: &fakeBackendFactory{}})
	result := reconciler.Reconcile(context.Background(), nil, []DiscoveredDevice{{
		DevName: "/dev/ttyUSB0", IDPath: "candidate-path", IDPathTag: "candidate-tag", PermissionOK: true,
	}})
	if len(result.Candidates) != 1 || result.Candidates[0].IDPath != "candidate-path" {
		t.Fatalf("candidates = %+v", result.Candidates)
	}
}

func TestReconcilerClosesWorkerWhenChannelDisabled(t *testing.T) {
	factory := &fakeBackendFactory{}
	reconciler := NewReconciler(ReconcilerConfig{BackendFactory: factory})
	reconciler.Reconcile(context.Background(), []ChannelConfig{{ID: "channel-1", IDPath: "id-path-1", DefaultConfig: serial.DefaultConfig()}}, []DiscoveredDevice{{DevName: "/dev/ttyUSB0", IDPath: "id-path-1", PermissionOK: true}})
	reconciler.Reconcile(context.Background(), []ChannelConfig{{ID: "channel-1", IDPath: "id-path-1", Status: "disabled", DefaultConfig: serial.DefaultConfig()}}, []DiscoveredDevice{{DevName: "/dev/ttyUSB0", IDPath: "id-path-1", PermissionOK: true}})
	if factory.closed["/dev/ttyUSB0"] != 1 {
		t.Fatalf("closed = %+v", factory.closed)
	}
}

type fakeBackendFactory struct {
	opened map[string]int
	closed map[string]int
}

func (f *fakeBackendFactory) Open(devName string, config serial.Config) (serial.Backend, error) {
	if f.opened == nil {
		f.opened = make(map[string]int)
	}
	if f.closed == nil {
		f.closed = make(map[string]int)
	}
	f.opened[devName]++
	return &fakeManagedBackend{devName: devName, closed: f.closed}, nil
}

type fakeManagedBackend struct {
	devName string
	closed  map[string]int
}

func (b *fakeManagedBackend) ApplyConfig(serial.Config) error { return nil }
func (b *fakeManagedBackend) SetDTR(bool) error { return nil }
func (b *fakeManagedBackend) SetRTS(bool) error { return nil }
func (b *fakeManagedBackend) SendBreak(time.Duration) error { return nil }
func (b *fakeManagedBackend) Read([]byte) (int, error) { return 0, io.EOF }
func (b *fakeManagedBackend) Write(data []byte) (int, error) { return len(data), nil }
func (b *fakeManagedBackend) Close() error {
	b.closed[b.devName]++
	return nil
}
```

- [ ] **Step 2: Write log uploader tests**

Create `internal/agent/log_uploader_test.go`:

```go
func TestLogUploaderConvertsSerialEventsToFrames(t *testing.T) {
	events := make(chan serial.Event, 2)
	frames := make(chan protocol.LogFrame, 2)
	uploader := NewLogUploader(LogUploaderConfig{Out: frames})

	events <- serial.Event{ChannelID: "channel-1", Direction: serial.DirectionTX, Timestamp: time.Unix(1, 0), Data: []byte("AT\r")}
	events <- serial.Event{ChannelID: "channel-1", Direction: serial.DirectionRX, Timestamp: time.Unix(2, 0), Data: []byte("OK\r\n")}
	close(events)

	if err := uploader.Forward(context.Background(), events); err != nil {
		t.Fatalf("Forward returned error: %v", err)
	}
	first := <-frames
	second := <-frames
	if first.Seq != 1 || first.Direction != protocol.DirectionTX || string(first.Payload) != "AT\r" {
		t.Fatalf("first frame = %+v", first)
	}
	if second.Seq != 2 || second.Direction != protocol.DirectionRX || string(second.Payload) != "OK\r\n" {
		t.Fatalf("second frame = %+v", second)
	}
}
```

- [ ] **Step 3: Run failing tests**

Run:

```bash
go test ./internal/agent -run 'TestReconciler|TestLogUploader' -count=1
```

Expected: fail with undefined reconciler and uploader types.

- [ ] **Step 4: Implement backend factory and reconciler**

Create `internal/agent/reconciler.go` with:

```go
type BackendFactory interface {
	Open(devName string, config serial.Config) (serial.Backend, error)
}

type RealBackendFactory struct{}

func (RealBackendFactory) Open(devName string, config serial.Config) (serial.Backend, error) {
	return serial.NewRealBackend(devName, config)
}

type ChannelConfig struct {
	ID            string
	AgentID       string
	DevName       string
	IDPath        string
	IDPathTag     string
	Status        string
	DefaultConfig serial.Config
}

type ChannelStatus struct {
	ChannelID    string
	Status       string
	DevName      string
	ErrorMessage string
}

type ReconcileResult struct {
	Statuses   []ChannelStatus
	Candidates []DiscoveredDevice
	Events     []<-chan serial.Event
}
```

`Reconciler.Reconcile` must:

- maintain `map[channelID]*serial.Worker`.
- start one worker for a matching channel if not already running.
- close worker on disabled/offline mismatch.
- report candidate for devices whose `IDPath` is not in configured channels.
- report `error` status when permission or backend open fails.

- [ ] **Step 5: Implement log uploader**

Create `internal/agent/log_uploader.go`:

```go
type LogUploaderConfig struct {
	Out chan<- protocol.LogFrame
}

type LogUploader struct {
	mu  sync.Mutex
	seq uint64
	out chan<- protocol.LogFrame
}

func NewLogUploader(config LogUploaderConfig) *LogUploader
func (u *LogUploader) Forward(ctx context.Context, events <-chan serial.Event) error
func (u *LogUploader) NextFrame(event serial.Event) protocol.LogFrame
```

`NextFrame` must call `SerialEventToLogFrame`, increment sequence, and copy payload.

- [ ] **Step 6: Wire host-agent runtime loop**

Modify `cmd/host-agent/main.go` and `internal/agent/client.go` enough to start a runtime object after `Connect`.

Runtime loop responsibilities:

```text
every scan interval:
  DiscoverDevices
  reconcile current channel config with devices
  send candidates/statuses to server over control WS
  ensure worker events are forwarded into /ws/logs
```

Before Task 5 adds server-driven channel sync, the runtime loop must still run discovery and send candidates when the channel config list is empty.

- [ ] **Step 7: Verify and commit**

Run:

```bash
go test ./internal/agent -run 'TestReconciler|TestLogUploader' -count=1
go test ./...
```

Commit:

```bash
git add cmd/host-agent internal/agent
git commit -m "feat: run agent serial reconciliation"
```

## Task 5: Bidirectional Agent Control Protocol

**Files:**
- Modify: `internal/protocol/messages.go`
- Modify: `internal/protocol/messages_test.go`
- Modify: `internal/server/agent_registry.go`
- Modify: `internal/server/agent_ws.go`
- Modify: `internal/server/agent_ws_test.go`
- Modify: `internal/agent/client.go`

- [ ] **Step 1: Write protocol tests**

Add to `internal/protocol/messages_test.go`:

```go
func TestAgentControlMessagesRoundTrip(t *testing.T) {
	messages := []any{
		DeviceSnapshot{Type: MessageDeviceSnapshot, AgentID: "agent-1", Devices: []DeviceIdentity{{DevName: "/dev/ttyUSB0", IDPath: "id-path"}}},
		ChannelStatusUpdate{Type: MessageChannelStatus, AgentID: "agent-1", Statuses: []ChannelRuntimeStatus{{ChannelID: "channel-1", Status: "online", DevName: "/dev/ttyUSB0"}}},
		ChannelSync{Type: MessageChannelSync, Channels: []ChannelConfigMessage{{ID: "channel-1", IDPath: "id-path", DefaultBaud: 115200}}},
		OpenTunnel{Type: MessageOpenTunnel, TunnelID: "tunnel-1", ChannelID: "channel-1", Mode: TunnelModeRFC2217},
	}
	for _, msg := range messages {
		data, err := json.Marshal(msg)
		if err != nil {
			t.Fatalf("Marshal(%T) returned error: %v", msg, err)
		}
		if !bytes.Contains(data, []byte(`"type"`)) {
			t.Fatalf("message missing type: %s", data)
		}
	}
}
```

- [ ] **Step 2: Run failing protocol test**

Run:

```bash
go test ./internal/protocol -run TestAgentControlMessagesRoundTrip -count=1
```

Expected: fail with undefined message types.

- [ ] **Step 3: Add protocol types**

Modify `internal/protocol/messages.go`:

```go
const (
	MessageDeviceSnapshot MessageType = "device_snapshot"
	MessageChannelStatus  MessageType = "channel_status"
	MessageChannelSync    MessageType = "channel_sync"
	MessageTunnelOpened   MessageType = "tunnel_opened"
	MessageTunnelError    MessageType = "tunnel_error"
)

type TunnelMode string

const (
	TunnelModeRFC2217  TunnelMode = "rfc2217"
	TunnelModeTerminal TunnelMode = "terminal"
)

type DeviceIdentity struct {
	DevName       string `json:"dev_name"`
	IDPath        string `json:"id_path"`
	IDPathTag     string `json:"id_path_tag"`
	SysfsDevpath  string `json:"sysfs_devpath"`
	Interface     string `json:"interface"`
	VID           string `json:"vid"`
	PID           string `json:"pid"`
	Serial        string `json:"serial"`
	Driver        string `json:"driver"`
	Manufacturer  string `json:"manufacturer"`
	Product       string `json:"product"`
	PermissionOK  bool   `json:"permission_ok"`
	ErrorMessage  string `json:"error_message,omitempty"`
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
```

Extend `OpenTunnel` with:

```go
Mode TunnelMode `json:"mode"`
```

- [ ] **Step 4: Write agent WS server tests**

Add tests in `internal/server/agent_ws_test.go`:

```go
func TestAgentWebSocketStoresCandidatesFromDeviceSnapshot(t *testing.T) {
	db := newAgentWSTestDB(t)
	srv := server.New(server.ServerConfig{DB: db})
	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn := dialAgentWS(t, ctx, httpSrv.URL)
	defer conn.Close(websocket.StatusNormalClosure, "")

	writeAgentHelloAndReadAccepted(t, ctx, conn, "agent-1")
	err := protocol.WriteJSON(ctx, conn, protocol.DeviceSnapshot{Type: protocol.MessageDeviceSnapshot, AgentID: "agent-1", Devices: []protocol.DeviceIdentity{{DevName: "/dev/ttyUSB0", IDPath: "id-path", IDPathTag: "id-tag", PermissionOK: true}}})
	if err != nil {
		t.Fatalf("WriteJSON returned error: %v", err)
	}
	requireEventually(t, func() bool {
		candidates, _ := db.ListCandidates()
		return len(candidates) == 1 && candidates[0].IDPath == "id-path"
	})
}

func TestAgentRegistryCanSendChannelSync(t *testing.T) {
	db := newAgentWSTestDB(t)
	srv := server.New(server.ServerConfig{DB: db})
	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn := dialAgentWS(t, ctx, httpSrv.URL)
	defer conn.Close(websocket.StatusNormalClosure, "")

	writeAgentHelloAndReadAccepted(t, ctx, conn, "agent-1")

	if err := srv.SendChannelSyncForTest(ctx, "agent-1", []protocol.ChannelConfigMessage{{ID: "channel-1", AgentID: "agent-1", IDPath: "id-path", DefaultBaud: 115200}}); err != nil {
		t.Fatalf("SendChannelSyncForTest returned error: %v", err)
	}

	var syncMessage protocol.ChannelSync
	if err := protocol.ReadJSON(ctx, conn, &syncMessage); err != nil {
		t.Fatalf("ReadJSON returned error: %v", err)
	}
	if syncMessage.Type != protocol.MessageChannelSync || len(syncMessage.Channels) != 1 || syncMessage.Channels[0].ID != "channel-1" {
		t.Fatalf("syncMessage = %+v", syncMessage)
	}
}

func newAgentWSTestDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "meta.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func dialAgentWS(t *testing.T, ctx context.Context, serverURL string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(serverURL, "http") + "/ws/agent"
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}
	return conn
}

func writeAgentHelloAndReadAccepted(t *testing.T, ctx context.Context, conn *websocket.Conn, agentID string) {
	t.Helper()
	if err := protocol.WriteJSON(ctx, conn, protocol.AgentHello{Type: protocol.MessageAgentHello, AgentID: agentID, Hostname: "node-1", OS: "linux", Arch: "amd64"}); err != nil {
		t.Fatalf("WriteJSON hello returned error: %v", err)
	}
	var accepted protocol.AgentAccepted
	if err := protocol.ReadJSON(ctx, conn, &accepted); err != nil {
		t.Fatalf("ReadJSON accepted returned error: %v", err)
	}
	if accepted.Type != protocol.MessageAgentAccepted {
		t.Fatalf("accepted = %+v", accepted)
	}
}

func requireEventually(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition was not met before deadline")
}
```

- [ ] **Step 5: Implement registry send path**

Modify `internal/server/agent_registry.go`:

- store an `AgentConnection` with a send mutex.
- expose:

```go
func (r *agentRegistry) send(ctx context.Context, agentID string, value any) error
func (r *agentRegistry) get(agentID string) (AgentConnection, bool)
```

Never write to a WebSocket from two goroutines without a per-connection mutex.

- [ ] **Step 6: Implement control message handling**

Modify `internal/server/agent_ws.go` after `AgentAccepted`:

```text
send initial ChannelSync for that agent
read JSON envelopes in a loop
DeviceSnapshot -> upsert candidates for unknown devices
ChannelStatusUpdate -> update channel status/dev_name/error
TunnelOpened/TunnelError -> forward to tunnel registry in Task 6
```

`TunnelOpened` and `TunnelError` messages must be parsed and logged in this task; Task 6 will connect them to the tunnel registry. Do not leave the read loop unable to decode these message types.

- [ ] **Step 7: Implement agent client receive loop hooks**

Modify `internal/agent/client.go`:

- expose `SendControl(ctx, value any) error`.
- expose `ReadControl(ctx) (protocol.MessageType, []byte, error)`.
- protect writes with a mutex.
- keep `SendLogFrames` on separate `/ws/logs`.

- [ ] **Step 8: Verify and commit**

Run:

```bash
go test ./internal/protocol ./internal/server ./internal/agent -run 'TestAgentControl|TestAgentWebSocket|TestAgentRegistry' -count=1
go test ./...
```

Commit:

```bash
git add internal/protocol internal/server internal/agent
git commit -m "feat: add agent control protocol"
```

## Task 6: Tunnel Registry and Byte Bridges

**Files:**
- Create: `internal/server/tunnel_registry.go`
- Create: `internal/server/tunnel_registry_test.go`
- Modify: `internal/server/server.go`
- Modify: `internal/server/agent_ws.go`
- Create: `internal/agent/tunnel.go`
- Create: `internal/agent/tunnel_test.go`

- [ ] **Step 1: Write tunnel registry tests**

Create `internal/server/tunnel_registry_test.go`:

```go
func TestTunnelRegistryPairsServerAndAgent(t *testing.T) {
	registry := server.NewTunnelRegistry(100 * time.Millisecond)
	waiter := make(chan net.Conn, 1)
	go func() {
		conn, err := registry.Wait(context.Background(), "tunnel-1")
		if err != nil {
			t.Errorf("Wait returned error: %v", err)
			return
		}
		waiter <- conn
	}()

	serverSide, agentSide := net.Pipe()
	defer serverSide.Close()
	defer agentSide.Close()

	if err := registry.Attach("tunnel-1", agentSide); err != nil {
		t.Fatalf("Attach returned error: %v", err)
	}
	select {
	case got := <-waiter:
		if got == nil {
			t.Fatal("Wait returned nil conn")
		}
	case <-time.After(time.Second):
		t.Fatal("Wait did not receive attached tunnel")
	}
}

func TestTunnelRegistryWaitTimesOut(t *testing.T) {
	registry := server.NewTunnelRegistry(10 * time.Millisecond)
	_, err := registry.Wait(context.Background(), "missing")
	if err == nil {
		t.Fatal("Wait returned nil error for missing tunnel")
	}
}
```

- [ ] **Step 2: Run failing tests**

Run:

```bash
go test ./internal/server -run TestTunnelRegistry -count=1
```

Expected: fail with undefined `TunnelRegistry`.

- [ ] **Step 3: Implement tunnel registry**

Create `internal/server/tunnel_registry.go`:

```go
type TunnelRegistry struct {
	mu      sync.Mutex
	timeout time.Duration
	waiters map[string]chan net.Conn
}

func NewTunnelRegistry(timeout time.Duration) *TunnelRegistry
func (r *TunnelRegistry) Wait(ctx context.Context, tunnelID string) (net.Conn, error)
func (r *TunnelRegistry) Attach(tunnelID string, conn net.Conn) error
func (r *TunnelRegistry) Cancel(tunnelID string)
```

Use buffered one-shot channels and delete waiters on success, timeout, or context cancellation.

- [ ] **Step 4: Add WebSocket net.Conn adapter**

In `internal/server/tunnel_registry.go` or a small `ws_conn.go`, add an adapter:

```go
type WSByteConn struct {
	Conn *websocket.Conn
	Ctx  context.Context
}

func (c *WSByteConn) Read(p []byte) (int, error)
func (c *WSByteConn) Write(p []byte) (int, error)
func (c *WSByteConn) Close() error
```

It must use binary WebSocket messages and copy payloads through an internal read buffer.

- [ ] **Step 5: Add server route `/ws/tunnel/{tunnelID}`**

Modify `internal/server/server.go`:

```go
srv.mux.HandleFunc("GET /ws/tunnel/{tunnelID}", srv.handleTunnelWebSocket)
```

`handleTunnelWebSocket` accepts a WebSocket, wraps it in `WSByteConn`, and calls `srv.tunnels.Attach(tunnelID, wsConn)`.

- [ ] **Step 6: Write agent tunnel tests**

Create `internal/agent/tunnel_test.go`:

```go
func TestAgentTunnelDialsServerAndBridgesBytes(t *testing.T) {
	var serverConn *websocket.Conn
	ready := make(chan struct{})
	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ws/tunnel/tunnel-1" {
			http.NotFound(w, r)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("Accept returned error: %v", err)
			return
		}
		serverConn = conn
		close(ready)
	}))
	t.Cleanup(httpSrv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	dialer := TunnelDialer{ServerURL: httpSrv.URL}
	clientConn, err := dialer.Dial(ctx, "tunnel-1")
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}
	defer clientConn.Close(websocket.StatusNormalClosure, "")

	<-ready
	if err := clientConn.Write(ctx, websocket.MessageBinary, []byte("ping")); err != nil {
		t.Fatalf("client Write returned error: %v", err)
	}
	_, payload, err := serverConn.Read(ctx)
	if err != nil {
		t.Fatalf("server Read returned error: %v", err)
	}
	if string(payload) != "ping" {
		t.Fatalf("payload = %q, want ping", payload)
	}
}
```

- [ ] **Step 7: Implement agent tunnel dialer**

Create `internal/agent/tunnel.go`:

```go
type TunnelDialer struct {
	ServerURL string
}

func (d TunnelDialer) Dial(ctx context.Context, tunnelID string) (*websocket.Conn, error)
func Bridge(ctx context.Context, left io.ReadWriteCloser, right io.ReadWriteCloser) error
```

`Bridge` must use two goroutines, `sync.Once`, and close both sides on either copy returning.

- [ ] **Step 8: Verify and commit**

Run:

```bash
go test ./internal/server ./internal/agent -run 'TestTunnelRegistry|TestAgentTunnel|TestBridge' -count=1
go test ./...
```

Commit:

```bash
git add internal/server internal/agent
git commit -m "feat: add agent tunnel registry"
```

## Task 7: RFC2217 Through Agent Tunnel

**Files:**
- Modify: `internal/server/rfc2217_listener.go`
- Modify: `internal/server/rfc2217_manager.go`
- Modify: `internal/server/rfc2217_listener_test.go`
- Modify: `internal/agent/tunnel.go`
- Modify: `internal/agent/tunnel_test.go`

- [ ] **Step 1: Write server-side RFC2217 tunnel test**

Add to `internal/server/rfc2217_listener_test.go`:

```go
func TestRFC2217ListenerRequestsAgentTunnelAndBridgesTCP(t *testing.T) {
	agentConn, serverTunnelConn := net.Pipe()
	defer agentConn.Close()
	defer serverTunnelConn.Close()

	requests := make(chan protocol.OpenTunnel, 1)
	resolver := server.RFC2217TunnelResolverFunc(func(ctx context.Context, channelID string) (net.Conn, error) {
		requests <- protocol.OpenTunnel{Type: protocol.MessageOpenTunnel, ChannelID: channelID, TunnelID: "tunnel-1", Mode: protocol.TunnelModeRFC2217}
		return serverTunnelConn, nil
	})

	netListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen returned error: %v", err)
	}
	listener := server.NewRFC2217TunnelListener(netListener, "channel-1", resolver, server.WithRFC2217ControlOwner(server.NewControlOwner()))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = listener.Serve(ctx) }()

	client, err := net.Dial("tcp", netListener.Addr().String())
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}
	defer client.Close()

	if _, err := client.Write([]byte("AT\r")); err != nil {
		t.Fatalf("client.Write returned error: %v", err)
	}
	buf := make([]byte, 3)
	if _, err := io.ReadFull(agentConn, buf); err != nil {
		t.Fatalf("ReadFull agentConn returned error: %v", err)
	}
	if string(buf) != "AT\r" {
		t.Fatalf("agent read = %q", buf)
	}
}
```

- [ ] **Step 2: Write agent-side RFC2217 apply test**

Add to `internal/agent/tunnel_test.go`:

```go
func TestAgentHandlesRFC2217TunnelWithLocalSerialControl(t *testing.T) {
	control := newFakeTunnelSerialControl()
	clientSide, agentSide := net.Pipe()
	defer clientSide.Close()
	defer agentSide.Close()

	go func() {
		_ = HandleRFC2217Tunnel(context.Background(), agentSide, "channel-1", control, serial.DefaultConfig())
	}()

	if _, err := clientSide.Write([]byte("AT\r")); err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
	control.session.waitForWrite(t, []byte("AT\r"))
}

type fakeTunnelSerialControl struct {
	session *fakeTunnelSession
	events  chan serial.Event
}

func newFakeTunnelSerialControl() *fakeTunnelSerialControl {
	return &fakeTunnelSerialControl{session: &fakeTunnelSession{writes: make(chan []byte, 1)}, events: make(chan serial.Event, 8)}
}

func (c *fakeTunnelSerialControl) OpenControlSession(context.Context, string) (serial.ControlSession, error) {
	return c.session, nil
}

func (c *fakeTunnelSerialControl) Events() <-chan serial.Event {
	return c.events
}

type fakeTunnelSession struct {
	writes chan []byte
}

func (s *fakeTunnelSession) Write(data []byte) error {
	s.writes <- append([]byte(nil), data...)
	return nil
}

func (s *fakeTunnelSession) SetConfig(serial.Config) error { return nil }
func (s *fakeTunnelSession) SetDTR(bool) error { return nil }
func (s *fakeTunnelSession) SetRTS(bool) error { return nil }
func (s *fakeTunnelSession) SendBreak(time.Duration) error { return nil }
func (s *fakeTunnelSession) Close() error { return nil }

func (s *fakeTunnelSession) waitForWrite(t *testing.T, want []byte) {
	t.Helper()
	select {
	case got := <-s.writes:
		if !bytes.Equal(got, want) {
			t.Fatalf("write = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for write %q", want)
	}
}
```

- [ ] **Step 3: Run failing tests**

Run:

```bash
go test ./internal/server ./internal/agent -run 'TestRFC2217ListenerRequestsAgentTunnel|TestAgentHandlesRFC2217Tunnel' -count=1
```

Expected: fail with undefined tunnel listener and agent handler.

- [ ] **Step 4: Implement server tunnel listener path**

Keep existing local `NewRFC2217Listener` for tests, but add production constructor:

```go
type RFC2217TunnelResolver interface {
	OpenRFC2217Tunnel(ctx context.Context, channelID string) (net.Conn, error)
}

func NewRFC2217TunnelListener(listener net.Listener, channelID string, resolver RFC2217TunnelResolver, options ...RFC2217ListenerOption) *RFC2217Listener
```

When a public TCP client connects:

1. acquire `ControlOwner`.
2. call `resolver.OpenRFC2217Tunnel`.
3. bridge raw TCP bytes to the tunnel conn.
4. release owner on any close path.

Do not parse RFC2217 on server in this path.

- [ ] **Step 5: Implement manager resolver**

Modify `internal/server/rfc2217_manager.go`:

```go
func (srv *Server) OpenRFC2217Tunnel(ctx context.Context, channel storage.Channel) (net.Conn, error) {
	tunnelID := uuid.NewString()
	if err := srv.agentRegistry.send(ctx, channel.AgentID, protocol.OpenTunnel{Type: protocol.MessageOpenTunnel, TunnelID: tunnelID, ChannelID: channel.ID, Mode: protocol.TunnelModeRFC2217}); err != nil {
		return nil, err
	}
	return srv.tunnels.Wait(ctx, tunnelID)
}
```

Use the channel's `AgentID`. If the agent is not connected, return a clear error.

- [ ] **Step 6: Implement agent RFC2217 tunnel handler**

In `internal/agent/tunnel.go`:

```go
func HandleRFC2217Tunnel(ctx context.Context, conn io.ReadWriteCloser, channelID string, control serial.SerialControl, config serial.Config) error
```

Behavior:

1. Open a local `ControlSession` with owner `rfc2217`.
2. Start goroutine copying `control.Events()` RX events to tunnel, escaping IAC.
3. Read tunnel bytes, feed `rfc2217.Parser`.
4. Apply operations to local session via `rfc2217.ApplyOperations`.
5. Write RFC2217 responses back to tunnel.
6. Close session and conn on exit.

- [ ] **Step 7: Wire `open_tunnel` handling in agent client**

When agent receives `OpenTunnel{Mode: "rfc2217"}`:

1. find channel control from reconciler/supervisor.
2. dial `/ws/tunnel/{tunnelID}`.
3. call `HandleRFC2217Tunnel`.
4. send `tunnel_error` if channel is unavailable or dial fails.

- [ ] **Step 8: Verify and commit**

Run:

```bash
go test ./internal/server ./internal/agent ./internal/rfc2217 -run 'TestRFC2217|TestAgentHandlesRFC2217Tunnel' -count=1
go test ./...
```

Commit:

```bash
git add internal/server internal/agent
git commit -m "feat: proxy rfc2217 through agent tunnel"
```

## Task 8: Web Terminal Through Agent Control

**Files:**
- Modify: `internal/server/web_terminal.go`
- Modify: `internal/server/web_terminal_test.go`
- Modify: `internal/agent/tunnel.go`
- Modify: `internal/agent/tunnel_test.go`
- Modify: `internal/protocol/messages.go`

- [ ] **Step 1: Write server terminal tunnel test**

Modify `internal/server/web_terminal_test.go` so the main write test verifies server sends terminal write to agent instead of opening local `SerialControl`:

```go
func TestTerminalWebSocketSendsWriteThroughAgentTunnel(t *testing.T) {
	db := newTerminalTestDBWithChannel(t, "channel-1", "agent-1")
	agent := newFakeAgentConnection()
	srv := server.New(server.ServerConfig{DB: db, AgentRegistryForTest: agent.Registry})
	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn := dialTerminalWebSocket(t, ctx, httpSrv.URL, "channel-1")
	defer conn.Close(websocket.StatusNormalClosure, "")

	writeTerminalAndExpectOK(t, ctx, conn, "request-1", []byte("AT\r"))

	msg := agent.lastMessageOfType(protocol.MessageTerminalWrite)
	if string(msg.Data) != "AT\r" {
		t.Fatalf("terminal write = %q", msg.Data)
	}
}
```

Use existing `dialTerminalWebSocket` and `writeTerminalAndExpectOK` helpers from `internal/server/web_terminal_test.go`. Add these helpers in the same test file:

```go
func newTerminalTestDBWithChannel(t *testing.T, channelID, agentID string) *storage.DB {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "meta.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.UpsertChannel(storage.Channel{
		ID:              channelID,
		AgentID:         agentID,
		AutoName:        "agent-1.if00",
		Alias:           "loopback",
		Role:            "console",
		IDPath:          "id-path",
		IDPathTag:       "id-tag",
		RFC2217Port:     7001,
		Status:          storage.ChannelStatusOnline,
		DefaultBaud:     115200,
		DefaultDataBits: 8,
		DefaultParity:   "N",
		DefaultStopBits: 1,
		DefaultFlow:     "none",
		UpdatedAt:       time.Now().UTC(),
	}); err != nil {
		t.Fatalf("UpsertChannel returned error: %v", err)
	}
	return db
}

type fakeAgentConnection struct {
	Registry *server.AgentRegistryForTest
	messages []any
}

func newFakeAgentConnection() *fakeAgentConnection {
	fake := &fakeAgentConnection{}
	fake.Registry = server.NewAgentRegistryForTest(func(ctx context.Context, agentID string, value any) error {
		fake.messages = append(fake.messages, value)
		return nil
	})
	return fake
}

func (f *fakeAgentConnection) lastMessageOfType(messageType protocol.MessageType) protocol.TerminalWrite {
	for i := len(f.messages) - 1; i >= 0; i-- {
		msg, ok := f.messages[i].(protocol.TerminalWrite)
		if ok && msg.Type == messageType {
			return msg
		}
	}
	return protocol.TerminalWrite{}
}
```

- [ ] **Step 2: Run failing terminal tests**

Run:

```bash
go test ./internal/server -run TestTerminalWebSocketSendsWriteThroughAgentTunnel -count=1
```

Expected: fail because current terminal path uses `SerialResolver`.

- [ ] **Step 3: Define terminal session protocol**

Add or extend protocol messages:

```go
type TerminalOpen struct {
	Type      MessageType `json:"type"`
	SessionID string      `json:"session_id"`
	ChannelID string      `json:"channel_id"`
}

type TerminalClose struct {
	Type      MessageType `json:"type"`
	SessionID string      `json:"session_id"`
	ChannelID string      `json:"channel_id"`
}
```

Add `SessionID` to terminal write/config/DTR/RTS/break messages.

- [ ] **Step 4: Refactor server terminal handler**

`handleTerminalWebSocket` must:

1. lookup channel metadata by ID.
2. acquire server `ControlOwner` as `web`.
3. create `sessionID`.
4. send `TerminalOpen` to channel's agent.
5. translate browser JSON commands to agent control messages with `sessionID`.
6. return `OperationResult` to browser when agent result arrives, or fail fast if agent send fails.
7. send `TerminalClose` and release owner on exit.

At this stage, RX display is still provided by `/ws/live-log/{channelID}`.

- [ ] **Step 5: Implement agent terminal sessions**

Agent runtime keeps:

```go
map[sessionID]serial.ControlSession
```

On `TerminalOpen`, open local control session. On terminal write/config/DTR/RTS/break, apply to that session and return `OperationResult`. On close or connection loss, close all sessions.

- [ ] **Step 6: Preserve busy behavior**

Update tests to ensure:

- second Web terminal is rejected while first holds owner.
- RFC2217 cannot acquire owner while Web terminal is active.
- owner is released after browser disconnect.

- [ ] **Step 7: Verify and commit**

Run:

```bash
go test ./internal/server ./internal/agent -run 'TestTerminal|TestControlOwner' -count=1
go test ./...
```

Commit:

```bash
git add internal/server internal/agent internal/protocol
git commit -m "feat: route web terminal through agent"
```

## Task 9: Web UI Real API and Layout Upgrade

**Files:**
- Modify: `web/src/api.ts`
- Modify: `web/src/App.tsx`
- Modify: `web/src/styles.css`
- Create: `web/src/types.ts`

- [ ] **Step 1: Split API helpers**

Modify `web/src/api.ts`:

```ts
export async function getJSON<T>(path: string): Promise<T> {
  const res = await fetch(path);
  if (!res.ok) {
    throw new Error(await res.text());
  }
  return res.json() as Promise<T>;
}

export async function postJSON<T, B = unknown>(path: string, body?: B): Promise<T> {
  const init: RequestInit = { method: 'POST' };
  if (body !== undefined) {
    init.headers = { 'Content-Type': 'application/json' };
    init.body = JSON.stringify(body);
  }
  const res = await fetch(path, init);
  if (!res.ok) {
    throw new Error(await res.text());
  }
  return res.json() as Promise<T>;
}

export async function patchJSON<T, B = unknown>(path: string, body: B): Promise<T> {
  const res = await fetch(path, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body)
  });
  if (!res.ok) {
    throw new Error(await res.text());
  }
  return res.json() as Promise<T>;
}

export function wsURL(path: string): string {
  const url = new URL(path, window.location.href);
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:';
  return url.toString();
}

export function downloadURL(path: string, params: Record<string, string | boolean | undefined>): string {
  const url = new URL(path, window.location.href);
  for (const [key, value] of Object.entries(params)) {
    if (value !== undefined && value !== '') {
      url.searchParams.set(key, String(value));
    }
  }
  return url.pathname + url.search;
}
```

`postJSON` and `patchJSON` must set `Content-Type: application/json` when a body is present.

- [ ] **Step 2: Add UI data types**

Either keep types in `App.tsx` or create `web/src/types.ts`:

```ts
export type Candidate = {
  ID: string;
  AgentID: string;
  DevName: string;
  IDPath: string;
  IDPathTag: string;
  Interface: string;
  VID: string;
  PID: string;
  Driver: string;
  LastSeen: string;
};
```

Extend `Channel` with `DevName`, `DefaultFlow`, and `ErrorMessage`.

- [ ] **Step 3: Replace mock Calibration with real data**

Implement Calibration view behavior:

1. fetch `/api/candidates`.
2. render candidates in a table.
3. confirm form fields: alias, role, RFC2217 port, baud.
4. call `POST /api/candidates/{id}/confirm`.
5. refresh candidates and channels.

Candidate confirm payload:

```ts
{
  alias,
  role,
  rfc2217_port: Number(port),
  default_baud: Number(baud),
  default_data_bits: 8,
  default_parity: 'N',
  default_stop_bits: 1,
  default_flow: 'none'
}
```

- [ ] **Step 4: Add manual channel form**

In Channels view, add a compact form that supports manual add from known identity:

```ts
{
  agent_id,
  alias,
  role: 'console',
  id_path,
  id_path_tag,
  rfc2217_port,
  default_baud,
  default_data_bits: 8,
  default_parity: 'N',
  default_stop_bits: 1,
  default_flow: 'none'
}
```

Manual add should be clearly secondary to Calibration.

- [ ] **Step 5: Implement terminal connection**

Terminal view:

1. select channel.
2. open `/ws/live-log/{channelID}` to display RX/TX.
3. open `/ws/terminal/{channelID}` only when user clicks Connect.
4. send `terminal_write`, `serial_set_config`, `serial_set_dtr`, `serial_set_rts`, `serial_send_break` JSON messages.
5. show busy/error states without retry loops.

Use `useEffect` cleanup to close WebSockets when channel changes or component unmounts.

- [ ] **Step 6: Implement logs download form and fix overlap**

Logs view:

1. use fields for channel, direction, from, to, format, timestamp, direction label, strip ANSI.
2. build `downloadURL('/api/logs/download', params)`.
3. use an `<a download href={url}>Download</a>` or button that sets `window.location.href`.
4. remove the mock "Prepare download" action.

CSS requirements:

```css
.log-export-form {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
  gap: 10px;
  padding: 12px;
}

.form-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  padding: 12px;
  border-top: 1px solid #e2e5e1;
}
```

Do not position the button absolutely.

- [ ] **Step 7: Apply React/UI guidelines**

When editing React:

- keep data fetching parallel where independent.
- avoid defining components inside other components.
- avoid expensive filter/map chains for repeated channel lookup; use `useMemo` with primitive dependencies.
- keep UI dense and operational, not marketing-like.
- do not use nested cards.

- [ ] **Step 8: Verify Web build**

Run:

```bash
cd web && npm run lint && npm run build
cd .. && make build
```

Expected: TypeScript passes; Vite build passes; Go binaries rebuild.

Commit:

```bash
git add web internal/server/webdist
git commit -m "feat: wire web serial console UI"
```

## Task 10: Real Serial Loopback Tests

**Files:**
- Create: `internal/e2e/real_serial_test.go`
- Modify: `Makefile`
- Modify: `README.md`

- [ ] **Step 1: Create real serial e2e package**

Create `internal/e2e/real_serial_test.go`:

```go
package e2e

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	bugserial "go.bug.st/serial"
)

func TestRealSerialLoopback(t *testing.T) {
	dev := os.Getenv("REAL_SERIAL_DEV")
	if dev == "" {
		dev = "/dev/ttyUSB0"
	}
	required := os.Getenv("REAL_SERIAL_REQUIRED") == "1"

	if _, err := os.Stat(dev); err != nil {
		message := fmt.Sprintf("real serial: skipped, %s not found", dev)
		if required {
			t.Fatal(message)
		}
		t.Skip(message)
	}

	port, err := bugserial.Open(dev, &bugserial.Mode{BaudRate: 115200, DataBits: 8, Parity: bugserial.NoParity, StopBits: bugserial.OneStopBit})
	if err != nil {
		message := fmt.Sprintf("real serial: skipped, open %s failed: %v", dev, err)
		if isPermissionError(err) {
			message = fmt.Sprintf("real serial: skipped, permission denied for %s, add current user to dialout", dev)
		}
		if required {
			t.Fatal(message)
		}
		t.Skip(message)
	}
	defer port.Close()

	payload := []byte(fmt.Sprintf("serial-platform-loopback-%d\r\n", time.Now().UnixNano()))
	if _, err := port.Write(payload); err != nil {
		t.Fatalf("real serial: write %s failed: %v", dev, err)
	}

	deadline := time.Now().Add(2 * time.Second)
	got := make([]byte, 0, len(payload))
	buf := make([]byte, 128)
	for time.Now().Before(deadline) && !bytes.Contains(got, payload) {
		n, err := port.Read(buf)
		if err != nil {
			t.Fatalf("real serial: read %s failed: %v", dev, err)
		}
		got = append(got, buf[:n]...)
	}
	if !bytes.Contains(got, payload) {
		t.Fatalf("real serial: loopback payload not observed on %s, got %q want %q", dev, got, payload)
	}
	t.Logf("real serial: passed %s", dev)
}

func isPermissionError(err error) bool {
	if errors.Is(err, os.ErrPermission) {
		return true
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "permission") || strings.Contains(text, "denied")
}
```

- [ ] **Step 2: Run non-blocking test without assuming hardware**

Run:

```bash
REAL_SERIAL_DEV=/dev/ttyUSB0 REAL_SERIAL_SOFT=1 go test -v ./internal/e2e -run TestRealSerialLoopback -count=1
```

Expected:

- with loopback device and permission: `real serial: passed /dev/ttyUSB0`.
- without device: `real serial: skipped, /dev/ttyUSB0 not found` and exit 0.
- without permission: `real serial: skipped, permission denied for /dev/ttyUSB0, add current user to dialout` and exit 0.

- [ ] **Step 3: Run strict test when hardware is present**

Run:

```bash
REAL_SERIAL_DEV=/dev/ttyUSB0 REAL_SERIAL_REQUIRED=1 go test -v ./internal/e2e -run TestRealSerialLoopback -count=1
```

Expected with real loopback hardware: pass.

- [ ] **Step 4: Ensure Makefile matches the behavior**

Update Makefile in this task so:

```bash
make test
```

runs normal tests and the non-blocking real serial test.

```bash
make test-real-serial REAL_SERIAL_DEV=/dev/ttyUSB0
```

runs the strict real serial test.

- [ ] **Step 5: Document manual hardware setup**

Add to `README.md`:

```markdown
## 真实串口 loopback 测试

默认设备是 `/dev/ttyUSB0`。请将该串口的 TX 和 RX 短接。

```bash
make test
make test-real-serial REAL_SERIAL_DEV=/dev/ttyUSB0
```

`make test` 会尝试真实设备测试；如果设备不存在或权限不足，会报告 skipped 并继续。`make test-real-serial` 是强制测试，设备不可用时失败。

如果权限不足:

```bash
sudo usermod -aG dialout "$USER"
newgrp dialout
```
```

- [ ] **Step 6: Verify and commit**

Run:

```bash
make test
```

Expected: all normal tests pass; real serial test either passes or reports skipped reason.

Commit:

```bash
git add Makefile README.md internal/e2e
git commit -m "test: add real serial loopback check"
```

## Task 11: Install Script, Full Manual Smoke, and Browser Verification

**Files:**
- Modify: `scripts/install-agent.sh`
- Modify: `scripts/install_scripts_test.sh`
- Modify: `README.md`
- Modify: `docs/superpowers/plans/2026-05-19-serial-platform-smoke-test.md`

- [ ] **Step 1: Update install script tests**

Modify `scripts/install_scripts_test.sh` to assert:

```bash
assert_contains "${install_agent}" "--user"
assert_contains "${install_agent}" "usermod -aG dialout"
assert_contains "${install_agent}" "User="
```

Run:

```bash
bash scripts/install_scripts_test.sh
```

Expected: fail until install script is updated.

- [ ] **Step 2: Implement non-root agent service install**

Modify `scripts/install-agent.sh`:

1. add `--user USER`.
2. default `RUN_USER="${SUDO_USER:-$(id -un)}"`.
3. validate `id "$RUN_USER"`.
4. run `usermod -aG dialout "$RUN_USER"` if group exists.
5. write systemd unit with:

```ini
User=<RUN_USER>
SupplementaryGroups=dialout
```

6. print:

```text
If this is the first time the user was added to dialout, log out and log in again or restart the service after group membership is active.
```

- [ ] **Step 3: Verify install script smoke**

Run:

```bash
bash scripts/install_scripts_test.sh
```

Expected: pass.

- [ ] **Step 4: Run full local verification**

Run:

```bash
make test
make build
bash scripts/build-release.sh
tar -tzf serial-platform-linux.tar.gz | sort
```

Expected:

- Go tests pass.
- Web lint/build pass.
- release tar contains central-server, host-agent amd64/arm64/armv7, serialctl, install scripts.

- [ ] **Step 5: Run real device manual smoke**

With `/dev/ttyUSB0` loopback connected:

```bash
rm -rf .server-data .agent-data
./bin/central-server --data-dir .server-data --listen 127.0.0.1:8080 --rfc2217-bind 127.0.0.1
```

In another terminal:

```bash
./bin/host-agent --server http://127.0.0.1:8080 --data-dir .agent-data
```

Then:

```bash
AGENT_ID=$(./bin/serialctl --server http://127.0.0.1:8080 hosts list | jq -r '.[0].ID')
curl -fsS -X POST "http://127.0.0.1:8080/api/agents/${AGENT_ID}/approve"
./bin/serialctl --server http://127.0.0.1:8080 channels list
```

Expected:

- candidate appears for `/dev/ttyUSB0`.
- confirming candidate creates channel.
- channel status becomes `online`.
- logs can be downloaded after terminal/RFC2217 traffic.

- [ ] **Step 6: Verify Ctrl+C**

While central-server is running, press `Ctrl+C`.

Expected:

```bash
pgrep -af '[c]entral-server.*127.0.0.1:8080' || true
```

returns no process.

- [ ] **Step 7: Browser verification with Edge**

Use `agent-browser` skill during execution to drive Edge against:

```text
http://127.0.0.1:8080/
```

Verify:

1. Hosts approve works.
2. Calibration candidate confirmation works.
3. Channels shows online channel and RFC2217 port.
4. Terminal connect/send shows loopback RX through live log.
5. Logs form has no overlapping controls at desktop and narrow widths.
6. Logs download produces a file for `rx`, `tx`, `both`, and `raw`.

- [ ] **Step 8: Cleanup and final commit**

Remove transient artifacts:

```bash
rm -rf .server-data .agent-data serial-platform-linux.tar.gz dist .release-build
```

Check:

```bash
git status --short --ignored=matching
git diff --check
```

Expected: only ignored build caches such as `bin/`, `web/dist/`, `web/node_modules/`.

Commit docs/script final changes:

```bash
git add scripts README.md docs/superpowers/plans/2026-05-19-serial-platform-smoke-test.md
git commit -m "docs: update real serial smoke workflow"
```

## Final Verification Checklist

Before claiming completion, run:

```bash
make test
make build
bash scripts/install_scripts_test.sh
bash scripts/build-release.sh
git diff --check
```

With `/dev/ttyUSB0` loopback connected and accessible:

```bash
make test-real-serial REAL_SERIAL_DEV=/dev/ttyUSB0
```

Manual runtime evidence must include:

1. central-server starts and exits on Ctrl+C.
2. host-agent connects without sudo as a dialout user.
3. Web approves host and confirms `/dev/ttyUSB0` candidate.
4. Web terminal sends and receives loopback data.
5. RFC2217 client connects through central-server and loopback data is logged.
6. downloaded text log includes expected payload and raw log is non-empty.
