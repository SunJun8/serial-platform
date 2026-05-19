# Serial Platform Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the first version of the internal serial platform: central-server, host-agent, serial control abstraction, log capture/export, RFC2217 entrypoint, Web UI shell, CLI, and install scripts.

**Architecture:** Implement a Go monorepo with focused packages under `internal/`. The central-server owns metadata, log storage, Web/API/RFC2217 entrypoints; host-agent owns local serial devices, udev mapping, and the only physical serial file descriptors. Browser controls use platform WebSocket messages; external serial tools use RFC2217 through central-server.

**Tech Stack:** Go 1.22+, SQLite via `modernc.org/sqlite`, WebSocket via `nhooyr.io/websocket`, serial access via `go.bug.st/serial`, React/Vite/TypeScript for the frontend, shell scripts for install packaging.

---

## Scope Notes

This plan implements the first version described by `docs/superpowers/specs/2026-05-19-serial-platform-design.md`.

The first implementation keeps business behavior narrow:

- No login, authorization, audit, user accounts, DUT object, flash recipe, server-side full text search, Windows helper, PostgreSQL, Docker, agent disk spool, or batch calibration.
- The first agent can be developed and tested with fake serial devices before real USB hardware is attached.
- RFC2217 support must cover pass-through bytes plus baudrate, data bits, parity, stop bits, DTR, RTS, and break. Verify command constants against RFC 2217 while implementing `internal/rfc2217/constants.go`: https://www.rfc-editor.org/rfc/rfc2217

## File Structure

Create this structure as the implementation proceeds:

```text
cmd/
  central-server/
    main.go
  host-agent/
    main.go
  serialctl/
    main.go
internal/
  agent/
    client.go
    config.go
    supervisor.go
  buildinfo/
    buildinfo.go
  logstore/
    export.go
    segment_writer.go
  protocol/
    logframe.go
    messages.go
    wsjson.go
  rfc2217/
    constants.go
    parser.go
    session.go
  serial/
    control.go
    fake.go
    real.go
    worker.go
  server/
    agent_registry.go
    api.go
    rfc2217_listener.go
    server.go
    web_terminal.go
  storage/
    db.go
    migrations.go
    models.go
  topology/
    identity.go
    udev_rules.go
    usb_tree.go
web/
  index.html
  package.json
  tsconfig.json
  vite.config.ts
  src/
    App.tsx
    api.ts
    main.tsx
    styles.css
scripts/
  install-agent.sh
  install-central.sh
  build-release.sh
```

Package responsibilities:

- `internal/protocol`: stable wire formats shared by server and agent.
- `internal/storage`: SQLite metadata only, never raw log bytes.
- `internal/logstore`: append-only log segment files and text/raw export.
- `internal/serial`: serial ownership, control session locking, default config restore, fake and real backends.
- `internal/topology`: Linux USB identity extraction and udev rule rendering.
- `internal/agent`: host-agent config, reconnect loop, serial worker supervision.
- `internal/server`: HTTP API, WebSocket endpoints, agent registry, RFC2217 listener/proxy, Web terminal control.
- `internal/rfc2217`: Telnet/RFC2217 parsing and translation into `serial.SerialControl`.
- `web`: static frontend, built into `central-server` by Task 14.
- `scripts`: install and release scripts.

## Task 1: Repository Scaffold

**Files:**
- Create: `go.mod`
- Create: `.gitignore`
- Create: `Makefile`
- Create: `cmd/central-server/main.go`
- Create: `cmd/host-agent/main.go`
- Create: `cmd/serialctl/main.go`
- Create: `internal/buildinfo/buildinfo.go`

- [ ] **Step 1: Create the Go module**

Create `go.mod`:

```go
module serial-platform

go 1.22
```

- [ ] **Step 2: Add repository ignore rules**

Create `.gitignore`:

```gitignore
/bin/
/dist/
/data/
/.agent-data/
/.server-data/
*.log
*.rlog
node_modules/
web/dist/
coverage.out
```

- [ ] **Step 3: Add top-level build commands**

Create `Makefile`:

```makefile
.PHONY: test build fmt

test:
	go test ./...

fmt:
	gofmt -w cmd internal

build:
	mkdir -p bin
	go build -o bin/central-server ./cmd/central-server
	go build -o bin/host-agent ./cmd/host-agent
	go build -o bin/serialctl ./cmd/serialctl
```

- [ ] **Step 4: Add build info package**

Create `internal/buildinfo/buildinfo.go`:

```go
package buildinfo

var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)
```

- [ ] **Step 5: Add command entrypoints**

Create `cmd/central-server/main.go`:

```go
package main

import (
	"fmt"

	"serial-platform/internal/buildinfo"
)

func main() {
	fmt.Printf("central-server %s %s %s\n", buildinfo.Version, buildinfo.Commit, buildinfo.Date)
}
```

Create `cmd/host-agent/main.go`:

```go
package main

import (
	"fmt"

	"serial-platform/internal/buildinfo"
)

func main() {
	fmt.Printf("host-agent %s %s %s\n", buildinfo.Version, buildinfo.Commit, buildinfo.Date)
}
```

Create `cmd/serialctl/main.go`:

```go
package main

import "fmt"

func main() {
	fmt.Println("serialctl")
}
```

- [ ] **Step 6: Verify scaffold builds**

Run:

```bash
make test
make build
```

Expected:

```text
go test ./...
mkdir -p bin
go build -o bin/central-server ./cmd/central-server
go build -o bin/host-agent ./cmd/host-agent
go build -o bin/serialctl ./cmd/serialctl
```

- [ ] **Step 7: Commit**

```bash
git add go.mod .gitignore Makefile cmd internal/buildinfo
git commit -m "chore: scaffold go project"
```

## Task 2: Protocol Log Frames and JSON Messages

**Files:**
- Create: `internal/protocol/logframe.go`
- Create: `internal/protocol/logframe_test.go`
- Create: `internal/protocol/messages.go`
- Create: `internal/protocol/messages_test.go`

- [ ] **Step 1: Write failing log frame tests**

Create `internal/protocol/logframe_test.go`:

```go
package protocol

import (
	"bytes"
	"testing"
	"time"
)

func TestLogFrameRoundTrip(t *testing.T) {
	frame := LogFrame{
		ChannelID:   "channel-1",
		Seq:         42,
		TimestampNS: time.Unix(1700000000, 123).UnixNano(),
		Direction:   DirectionTX,
		Flags:       FlagRaw,
		Payload:     []byte{0x41, 0xff, 0x00},
	}

	encoded, err := EncodeLogFrame(frame)
	if err != nil {
		t.Fatalf("EncodeLogFrame returned error: %v", err)
	}

	decoded, err := DecodeLogFrame(encoded)
	if err != nil {
		t.Fatalf("DecodeLogFrame returned error: %v", err)
	}

	if decoded.ChannelID != frame.ChannelID {
		t.Fatalf("ChannelID = %q, want %q", decoded.ChannelID, frame.ChannelID)
	}
	if decoded.Seq != frame.Seq {
		t.Fatalf("Seq = %d, want %d", decoded.Seq, frame.Seq)
	}
	if decoded.TimestampNS != frame.TimestampNS {
		t.Fatalf("TimestampNS = %d, want %d", decoded.TimestampNS, frame.TimestampNS)
	}
	if decoded.Direction != frame.Direction {
		t.Fatalf("Direction = %d, want %d", decoded.Direction, frame.Direction)
	}
	if decoded.Flags != frame.Flags {
		t.Fatalf("Flags = %d, want %d", decoded.Flags, frame.Flags)
	}
	if !bytes.Equal(decoded.Payload, frame.Payload) {
		t.Fatalf("Payload = %x, want %x", decoded.Payload, frame.Payload)
	}
}

func TestDecodeLogFrameRejectsBadMagic(t *testing.T) {
	_, err := DecodeLogFrame([]byte("bad"))
	if err == nil {
		t.Fatal("DecodeLogFrame returned nil error for bad magic")
	}
}
```

- [ ] **Step 2: Run tests to verify failure**

Run:

```bash
go test ./internal/protocol -run TestLogFrame -v
```

Expected: fail because `LogFrame`, `EncodeLogFrame`, and `DecodeLogFrame` are not defined.

- [ ] **Step 3: Implement log frame encoding**

Create `internal/protocol/logframe.go` with:

```go
package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"
)

var logFrameMagic = [4]byte{'S', 'P', 'L', '1'}

type Direction uint8

const (
	DirectionRX Direction = 1
	DirectionTX Direction = 2
)

type LogFlags uint16

const (
	FlagRaw    LogFlags = 1 << 0
	FlagDrop   LogFlags = 1 << 1
	FlagLogGap LogFlags = 1 << 2
)

type LogFrame struct {
	ChannelID   string
	Seq         uint64
	TimestampNS int64
	Direction   Direction
	Flags       LogFlags
	Payload     []byte
}

func EncodeLogFrame(frame LogFrame) ([]byte, error) {
	if frame.ChannelID == "" {
		return nil, errors.New("channel id is required")
	}
	if frame.Direction != DirectionRX && frame.Direction != DirectionTX {
		return nil, fmt.Errorf("invalid direction %d", frame.Direction)
	}
	channel := []byte(frame.ChannelID)
	if len(channel) > 65535 {
		return nil, errors.New("channel id is too long")
	}
	headerLen := 4 + 2 + 2 + 8 + 8 + 1 + 1 + 2 + 4
	total := headerLen + len(channel) + len(frame.Payload)
	out := make([]byte, total)
	copy(out[0:4], logFrameMagic[:])
	binary.BigEndian.PutUint16(out[4:6], uint16(headerLen))
	binary.BigEndian.PutUint16(out[6:8], uint16(len(channel)))
	binary.BigEndian.PutUint64(out[8:16], frame.Seq)
	binary.BigEndian.PutUint64(out[16:24], uint64(frame.TimestampNS))
	out[24] = byte(frame.Direction)
	out[25] = 0
	binary.BigEndian.PutUint16(out[26:28], uint16(frame.Flags))
	binary.BigEndian.PutUint32(out[28:32], uint32(len(frame.Payload)))
	copy(out[32:32+len(channel)], channel)
	copy(out[32+len(channel):], frame.Payload)
	return out, nil
}

func DecodeLogFrame(in []byte) (LogFrame, error) {
	if len(in) < 32 {
		return LogFrame{}, errors.New("log frame is too short")
	}
	if string(in[0:4]) != string(logFrameMagic[:]) {
		return LogFrame{}, errors.New("invalid log frame magic")
	}
	headerLen := int(binary.BigEndian.Uint16(in[4:6]))
	if headerLen != 32 {
		return LogFrame{}, fmt.Errorf("unsupported header length %d", headerLen)
	}
	channelLen := int(binary.BigEndian.Uint16(in[6:8]))
	payloadLen := int(binary.BigEndian.Uint32(in[28:32]))
	if len(in) != headerLen+channelLen+payloadLen {
		return LogFrame{}, errors.New("log frame length mismatch")
	}
	direction := Direction(in[24])
	if direction != DirectionRX && direction != DirectionTX {
		return LogFrame{}, fmt.Errorf("invalid direction %d", direction)
	}
	return LogFrame{
		ChannelID:   string(in[32 : 32+channelLen]),
		Seq:         binary.BigEndian.Uint64(in[8:16]),
		TimestampNS: int64(binary.BigEndian.Uint64(in[16:24])),
		Direction:   direction,
		Flags:       LogFlags(binary.BigEndian.Uint16(in[26:28])),
		Payload:     append([]byte(nil), in[32+channelLen:]...),
	}, nil
}
```

- [ ] **Step 4: Add management message tests**

Create `internal/protocol/messages_test.go`:

```go
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
```

- [ ] **Step 5: Implement management message types**

Create `internal/protocol/messages.go`:

```go
package protocol

type MessageType string

const (
	MessageAgentHello       MessageType = "agent_hello"
	MessageAgentAccepted    MessageType = "agent_accepted"
	MessageAgentPending     MessageType = "agent_pending"
	MessageHeartbeat        MessageType = "heartbeat"
	MessageChannelSnapshot  MessageType = "channel_snapshot"
	MessageOpenTunnel       MessageType = "open_tunnel"
	MessageTerminalOpen     MessageType = "terminal_open"
	MessageTerminalClose    MessageType = "terminal_close"
	MessageTerminalWrite    MessageType = "terminal_write"
	MessageSerialSetConfig  MessageType = "serial_set_config"
	MessageSerialSetDTR     MessageType = "serial_set_dtr"
	MessageSerialSetRTS     MessageType = "serial_set_rts"
	MessageSerialSendBreak  MessageType = "serial_send_break"
	MessageOperationResult  MessageType = "operation_result"
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

type OperationResult struct {
	Type      MessageType `json:"type"`
	RequestID string      `json:"request_id"`
	OK        bool        `json:"ok"`
	Error     string      `json:"error,omitempty"`
}
```

- [ ] **Step 6: Verify protocol tests**

Run:

```bash
go test ./internal/protocol -v
```

Expected: all protocol tests pass.

- [ ] **Step 7: Commit**

```bash
git add internal/protocol
git commit -m "feat: add protocol frame formats"
```

## Task 3: SQLite Metadata Store

**Files:**
- Create: `internal/storage/models.go`
- Create: `internal/storage/migrations.go`
- Create: `internal/storage/db.go`
- Create: `internal/storage/db_test.go`
- Modify: `go.mod`
- Modify: `go.sum`

- [ ] **Step 1: Add SQLite dependency**

Run:

```bash
go get modernc.org/sqlite
```

Expected: `go.mod` and `go.sum` are updated.

- [ ] **Step 2: Write metadata store tests**

Create `internal/storage/db_test.go`:

```go
package storage

import (
	"path/filepath"
	"testing"
	"time"
)

func TestDBCreatesAgentAndChannel(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "meta.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	agent := Agent{
		ID:        "agent-1",
		Name:      "node-1",
		Status:    AgentStatusPending,
		Hostname:  "node-1",
		OS:        "linux",
		Arch:      "arm64",
		MachineID: "machine-1",
		UpdatedAt: time.Unix(100, 0).UTC(),
	}
	if err := db.UpsertAgent(agent); err != nil {
		t.Fatalf("UpsertAgent returned error: %v", err)
	}

	agents, err := db.ListAgents()
	if err != nil {
		t.Fatalf("ListAgents returned error: %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("len(agents) = %d, want 1", len(agents))
	}
	if agents[0].Status != AgentStatusPending {
		t.Fatalf("Status = %q", agents[0].Status)
	}

	channel := Channel{
		ID:             "channel-1",
		AgentID:        "agent-1",
		AutoName:       "host01.hub01.port01.if00",
		Alias:          "rack1.port01.console",
		Role:           "console",
		IDPath:         "pci-0000:00:14.0-usb-0:1:1.0",
		IDPathTag:      "pci-0000_00_14_0-usb-0_1_1_0",
		RFC2217Port:    7001,
		Status:         ChannelStatusDisabled,
		DefaultBaud:    115200,
		DefaultDataBits: 8,
		DefaultParity:   "N",
		DefaultStopBits: 1,
		UpdatedAt:      time.Unix(101, 0).UTC(),
	}
	if err := db.UpsertChannel(channel); err != nil {
		t.Fatalf("UpsertChannel returned error: %v", err)
	}
	channels, err := db.ListChannels()
	if err != nil {
		t.Fatalf("ListChannels returned error: %v", err)
	}
	if len(channels) != 1 {
		t.Fatalf("len(channels) = %d, want 1", len(channels))
	}
	if channels[0].IDPath != channel.IDPath {
		t.Fatalf("IDPath = %q, want %q", channels[0].IDPath, channel.IDPath)
	}
}
```

- [ ] **Step 3: Run tests to verify failure**

Run:

```bash
go test ./internal/storage -run TestDBCreatesAgentAndChannel -v
```

Expected: fail because storage types and `Open` are not defined.

- [ ] **Step 4: Implement storage models**

Create `internal/storage/models.go`:

```go
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
```

- [ ] **Step 5: Implement migrations**

Create `internal/storage/migrations.go`:

```go
package storage

const schemaSQL = `
CREATE TABLE IF NOT EXISTS agents (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  status TEXT NOT NULL,
  hostname TEXT NOT NULL,
  os TEXT NOT NULL,
  arch TEXT NOT NULL,
  machine_id TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS channels (
  id TEXT PRIMARY KEY,
  agent_id TEXT NOT NULL,
  auto_name TEXT NOT NULL,
  alias TEXT NOT NULL,
  role TEXT NOT NULL,
  id_path TEXT NOT NULL,
  id_path_tag TEXT NOT NULL,
  sysfs_devpath TEXT NOT NULL,
  rfc2217_port INTEGER NOT NULL UNIQUE,
  status TEXT NOT NULL,
  default_baud INTEGER NOT NULL,
  default_data_bits INTEGER NOT NULL,
  default_parity TEXT NOT NULL,
  default_stop_bits INTEGER NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS log_segments (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  channel_id TEXT NOT NULL,
  path TEXT NOT NULL,
  start_time TEXT NOT NULL,
  end_time TEXT NOT NULL,
  size_bytes INTEGER NOT NULL,
  frame_count INTEGER NOT NULL,
  status TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS quota_config (
  scope TEXT PRIMARY KEY,
  global_max_storage_bytes INTEGER NOT NULL,
  default_retention_days INTEGER NOT NULL,
  default_channel_max_storage_bytes INTEGER NOT NULL,
  warning_threshold_percent INTEGER NOT NULL,
  critical_threshold_percent INTEGER NOT NULL,
  cleanup_interval_seconds INTEGER NOT NULL
);
`
```

- [ ] **Step 6: Implement database methods**

Create `internal/storage/db.go`:

```go
package storage

import (
	"context"
	"database/sql"
	"time"

	_ "modernc.org/sqlite"
)

type DB struct {
	sql *sql.DB
}

func Open(path string) (*DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if _, err := db.ExecContext(context.Background(), schemaSQL); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &DB{sql: db}, nil
}

func (db *DB) Close() error {
	return db.sql.Close()
}

func (db *DB) UpsertAgent(agent Agent) error {
	_, err := db.sql.Exec(`
INSERT INTO agents (id, name, status, hostname, os, arch, machine_id, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  name=excluded.name,
  status=excluded.status,
  hostname=excluded.hostname,
  os=excluded.os,
  arch=excluded.arch,
  machine_id=excluded.machine_id,
  updated_at=excluded.updated_at
`, agent.ID, agent.Name, string(agent.Status), agent.Hostname, agent.OS, agent.Arch, agent.MachineID, agent.UpdatedAt.Format(time.RFC3339Nano))
	return err
}

func (db *DB) ListAgents() ([]Agent, error) {
	rows, err := db.sql.Query(`SELECT id, name, status, hostname, os, arch, machine_id, updated_at FROM agents ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Agent
	for rows.Next() {
		var agent Agent
		var status string
		var updated string
		if err := rows.Scan(&agent.ID, &agent.Name, &status, &agent.Hostname, &agent.OS, &agent.Arch, &agent.MachineID, &updated); err != nil {
			return nil, err
		}
		agent.Status = AgentStatus(status)
		agent.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
		if err != nil {
			return nil, err
		}
		out = append(out, agent)
	}
	return out, rows.Err()
}

func (db *DB) UpsertChannel(channel Channel) error {
	_, err := db.sql.Exec(`
INSERT INTO channels (
  id, agent_id, auto_name, alias, role, id_path, id_path_tag, sysfs_devpath,
  rfc2217_port, status, default_baud, default_data_bits, default_parity,
  default_stop_bits, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  agent_id=excluded.agent_id,
  auto_name=excluded.auto_name,
  alias=excluded.alias,
  role=excluded.role,
  id_path=excluded.id_path,
  id_path_tag=excluded.id_path_tag,
  sysfs_devpath=excluded.sysfs_devpath,
  rfc2217_port=excluded.rfc2217_port,
  status=excluded.status,
  default_baud=excluded.default_baud,
  default_data_bits=excluded.default_data_bits,
  default_parity=excluded.default_parity,
  default_stop_bits=excluded.default_stop_bits,
  updated_at=excluded.updated_at
`, channel.ID, channel.AgentID, channel.AutoName, channel.Alias, channel.Role,
		channel.IDPath, channel.IDPathTag, channel.SysfsDevpath, channel.RFC2217Port,
		string(channel.Status), channel.DefaultBaud, channel.DefaultDataBits,
		channel.DefaultParity, channel.DefaultStopBits, channel.UpdatedAt.Format(time.RFC3339Nano))
	return err
}

func (db *DB) ListChannels() ([]Channel, error) {
	rows, err := db.sql.Query(`SELECT id, agent_id, auto_name, alias, role, id_path, id_path_tag, sysfs_devpath, rfc2217_port, status, default_baud, default_data_bits, default_parity, default_stop_bits, updated_at FROM channels ORDER BY alias`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Channel
	for rows.Next() {
		var channel Channel
		var status string
		var updated string
		if err := rows.Scan(&channel.ID, &channel.AgentID, &channel.AutoName, &channel.Alias, &channel.Role,
			&channel.IDPath, &channel.IDPathTag, &channel.SysfsDevpath, &channel.RFC2217Port,
			&status, &channel.DefaultBaud, &channel.DefaultDataBits, &channel.DefaultParity,
			&channel.DefaultStopBits, &updated); err != nil {
			return nil, err
		}
		channel.Status = ChannelStatus(status)
		channel.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
		if err != nil {
			return nil, err
		}
		out = append(out, channel)
	}
	return out, rows.Err()
}
```

- [ ] **Step 7: Verify storage tests**

Run:

```bash
go test ./internal/storage -v
```

Expected: storage tests pass.

- [ ] **Step 8: Commit**

```bash
git add go.mod go.sum internal/storage
git commit -m "feat: add sqlite metadata store"
```

## Task 4: Log Segment Writer and Exporter

**Files:**
- Create: `internal/logstore/segment_writer.go`
- Create: `internal/logstore/export.go`
- Create: `internal/logstore/logstore_test.go`
- Modify: `internal/storage/models.go`
- Modify: `internal/storage/db.go`

- [ ] **Step 1: Write logstore tests**

Create `internal/logstore/logstore_test.go`:

```go
package logstore

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"serial-platform/internal/protocol"
)

func TestSegmentWriterWritesAndExportsText(t *testing.T) {
	dir := t.TempDir()
	writer, err := NewSegmentWriter(dir, "channel-1", 1024*1024)
	if err != nil {
		t.Fatalf("NewSegmentWriter returned error: %v", err)
	}

	frame := protocol.LogFrame{
		ChannelID:   "channel-1",
		Seq:         1,
		TimestampNS: time.Unix(1700000000, 0).UnixNano(),
		Direction:   protocol.DirectionRX,
		Flags:       protocol.FlagRaw,
		Payload:     []byte{'H', 'i', 0xff, '\n'},
	}
	if err := writer.WriteFrame(frame); err != nil {
		t.Fatalf("WriteFrame returned error: %v", err)
	}
	segment, err := writer.Close()
	if err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	var out bytes.Buffer
	err = ExportText([]string{filepath.Join(dir, segment.RelativePath)}, ExportOptions{
		IncludeRX:         true,
		IncludeTX:         true,
		IncludeTimestamp:  true,
		IncludeDirection:  true,
		StripANSI:         false,
	}, &out)
	if err != nil {
		t.Fatalf("ExportText returned error: %v", err)
	}
	text := out.String()
	if !strings.Contains(text, "RX") {
		t.Fatalf("export text %q does not contain direction", text)
	}
	if !strings.Contains(text, `Hi\xff`) {
		t.Fatalf("export text %q does not escape invalid UTF-8", text)
	}
}
```

- [ ] **Step 2: Run tests to verify failure**

Run:

```bash
go test ./internal/logstore -run TestSegmentWriterWritesAndExportsText -v
```

Expected: fail because `NewSegmentWriter`, `ExportText`, and `ExportOptions` are not defined.

- [ ] **Step 3: Implement segment writer**

Create `internal/logstore/segment_writer.go`:

```go
package logstore

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"time"

	"serial-platform/internal/protocol"
)

type SegmentInfo struct {
	RelativePath string
	SizeBytes    int64
	FrameCount   int64
	StartTime     time.Time
	EndTime       time.Time
}

type SegmentWriter struct {
	root       string
	channelID  string
	file       *os.File
	relPath    string
	sizeBytes  int64
	frameCount int64
	startTime  time.Time
	endTime    time.Time
}

func NewSegmentWriter(root, channelID string, maxBytes int64) (*SegmentWriter, error) {
	now := time.Now().UTC()
	rel := filepath.Join(channelID, now.Format("2006"), now.Format("01"), now.Format("02"), now.Format("15"), "segment-000001.rlog")
	abs := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(abs, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &SegmentWriter{root: root, channelID: channelID, file: file, relPath: rel}, nil
}

func (w *SegmentWriter) WriteFrame(frame protocol.LogFrame) error {
	encoded, err := protocol.EncodeLogFrame(frame)
	if err != nil {
		return err
	}
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(encoded)))
	if _, err := w.file.Write(lenBuf[:]); err != nil {
		return err
	}
	if _, err := w.file.Write(encoded); err != nil {
		return err
	}
	w.sizeBytes += int64(4 + len(encoded))
	w.frameCount++
	ts := time.Unix(0, frame.TimestampNS).UTC()
	if w.startTime.IsZero() {
		w.startTime = ts
	}
	w.endTime = ts
	return nil
}

func (w *SegmentWriter) Close() (SegmentInfo, error) {
	err := w.file.Close()
	return SegmentInfo{
		RelativePath: w.relPath,
		SizeBytes:    w.sizeBytes,
		FrameCount:   w.frameCount,
		StartTime:     w.startTime,
		EndTime:       w.endTime,
	}, err
}
```

- [ ] **Step 4: Implement text exporter**

Create `internal/logstore/export.go`:

```go
package logstore

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"regexp"
	"time"
	"unicode/utf8"

	"serial-platform/internal/protocol"
)

type ExportOptions struct {
	IncludeRX        bool
	IncludeTX        bool
	IncludeTimestamp bool
	IncludeDirection bool
	StripANSI        bool
}

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

func ExportText(paths []string, opts ExportOptions, out io.Writer) error {
	writer := bufio.NewWriter(out)
	defer writer.Flush()
	for _, path := range paths {
		if err := exportTextFile(path, opts, writer); err != nil {
			return err
		}
	}
	return nil
}

func exportTextFile(path string, opts ExportOptions, out *bufio.Writer) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	for {
		var lenBuf [4]byte
		_, err := io.ReadFull(file, lenBuf[:])
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		payload := make([]byte, binary.BigEndian.Uint32(lenBuf[:]))
		if _, err := io.ReadFull(file, payload); err != nil {
			return err
		}
		frame, err := protocol.DecodeLogFrame(payload)
		if err != nil {
			return err
		}
		if frame.Direction == protocol.DirectionRX && !opts.IncludeRX {
			continue
		}
		if frame.Direction == protocol.DirectionTX && !opts.IncludeTX {
			continue
		}
		if opts.IncludeTimestamp {
			fmt.Fprintf(out, "%s ", time.Unix(0, frame.TimestampNS).UTC().Format(time.RFC3339Nano))
		}
		if opts.IncludeDirection {
			if frame.Direction == protocol.DirectionRX {
				fmt.Fprint(out, "RX ")
			} else {
				fmt.Fprint(out, "TX ")
			}
		}
		text := escapedUTF8(frame.Payload)
		if opts.StripANSI {
			text = ansiRE.ReplaceAllString(text, "")
		}
		fmt.Fprint(out, text)
	}
}

func escapedUTF8(data []byte) string {
	out := make([]byte, 0, len(data))
	for len(data) > 0 {
		r, size := utf8.DecodeRune(data)
		if r == utf8.RuneError && size == 1 {
			out = append(out, fmt.Sprintf(`\x%02x`, data[0])...)
			data = data[1:]
			continue
		}
		out = append(out, data[:size]...)
		data = data[size:]
	}
	return string(out)
}
```

- [ ] **Step 5: Add segment metadata model methods**

Modify `internal/storage/models.go` to add:

```go
type LogSegmentStatus string

const (
	LogSegmentStatusActive LogSegmentStatus = "active"
	LogSegmentStatusClosed LogSegmentStatus = "closed"
)

type LogSegment struct {
	ID         int64
	ChannelID  string
	Path       string
	StartTime  time.Time
	EndTime    time.Time
	SizeBytes  int64
	FrameCount int64
	Status     LogSegmentStatus
}
```

Modify `internal/storage/db.go` to add `InsertLogSegment(segment LogSegment) error` and `ListLogSegments(channelID string, start, end time.Time) ([]LogSegment, error)` using the existing `log_segments` table.

- [ ] **Step 6: Verify logstore tests**

Run:

```bash
go test ./internal/logstore ./internal/storage -v
```

Expected: all logstore and storage tests pass.

- [ ] **Step 7: Commit**

```bash
git add internal/logstore internal/storage
git commit -m "feat: add log segment storage"
```

## Task 5: Serial Control Abstraction

**Files:**
- Create: `internal/serial/control.go`
- Create: `internal/serial/fake.go`
- Create: `internal/serial/worker.go`
- Create: `internal/serial/worker_test.go`
- Modify: `go.mod`
- Modify: `go.sum`

- [ ] **Step 1: Write serial worker tests**

Create `internal/serial/worker_test.go`:

```go
package serial

import (
	"context"
	"testing"
	"time"
)

func TestWorkerAllowsSingleControlSession(t *testing.T) {
	backend := NewFakeBackend()
	worker := NewWorker("channel-1", DefaultConfig(), backend)

	session, err := worker.OpenControlSession(context.Background(), "first")
	if err != nil {
		t.Fatalf("OpenControlSession first returned error: %v", err)
	}
	_, err = worker.OpenControlSession(context.Background(), "second")
	if err == nil {
		t.Fatal("OpenControlSession second returned nil error")
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	_, err = worker.OpenControlSession(context.Background(), "third")
	if err != nil {
		t.Fatalf("OpenControlSession third returned error: %v", err)
	}
}

func TestWorkerRestoresDefaultConfigOnClose(t *testing.T) {
	backend := NewFakeBackend()
	def := DefaultConfig()
	def.Baud = 115200
	worker := NewWorker("channel-1", def, backend)

	session, err := worker.OpenControlSession(context.Background(), "owner")
	if err != nil {
		t.Fatalf("OpenControlSession returned error: %v", err)
	}
	if err := session.SetConfig(Config{Baud: 2000000, DataBits: 8, Parity: "N", StopBits: 1}); err != nil {
		t.Fatalf("SetConfig returned error: %v", err)
	}
	if backend.Config().Baud != 2000000 {
		t.Fatalf("backend baud = %d", backend.Config().Baud)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if backend.Config().Baud != 115200 {
		t.Fatalf("backend baud after close = %d", backend.Config().Baud)
	}
}

func TestWorkerRecordsTXWrites(t *testing.T) {
	backend := NewFakeBackend()
	worker := NewWorker("channel-1", DefaultConfig(), backend)
	session, err := worker.OpenControlSession(context.Background(), "owner")
	if err != nil {
		t.Fatalf("OpenControlSession returned error: %v", err)
	}
	if err := session.Write([]byte("AT\r\n")); err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
	select {
	case event := <-worker.Events():
		if event.Direction != DirectionTX {
			t.Fatalf("Direction = %v", event.Direction)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for TX event")
	}
}

func TestWorkerEmitsRXFromBackend(t *testing.T) {
	backend := NewFakeBackend()
	worker := NewWorker("channel-1", DefaultConfig(), backend)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go worker.Run(ctx)

	backend.InjectRX([]byte("boot\n"))
	select {
	case event := <-worker.Events():
		if event.Direction != DirectionRX {
			t.Fatalf("Direction = %v", event.Direction)
		}
		if string(event.Data) != "boot\n" {
			t.Fatalf("Data = %q", string(event.Data))
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for RX event")
	}
}
```

- [ ] **Step 2: Run tests to verify failure**

Run:

```bash
go test ./internal/serial -run TestWorker -v
```

Expected: fail because `NewWorker`, `NewFakeBackend`, and related types are not defined.

- [ ] **Step 3: Implement control interfaces**

Create `internal/serial/control.go`:

```go
package serial

import (
	"context"
	"time"
)

type Direction int

const (
	DirectionRX Direction = 1
	DirectionTX Direction = 2
)

type Config struct {
	Baud     int
	DataBits int
	Parity   string
	StopBits int
	Flow     string
}

func DefaultConfig() Config {
	return Config{Baud: 115200, DataBits: 8, Parity: "N", StopBits: 1, Flow: "none"}
}

type Event struct {
	ChannelID string
	Direction Direction
	Timestamp time.Time
	Data      []byte
}

type Backend interface {
	ApplyConfig(Config) error
	SetDTR(bool) error
	SetRTS(bool) error
	SendBreak(time.Duration) error
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	Close() error
}

type ControlSession interface {
	Write([]byte) error
	SetConfig(Config) error
	SetDTR(bool) error
	SetRTS(bool) error
	SendBreak(time.Duration) error
	Close() error
}

type SerialControl interface {
	OpenControlSession(context.Context, string) (ControlSession, error)
	Events() <-chan Event
}
```

- [ ] **Step 4: Implement fake backend**

Create `internal/serial/fake.go`:

```go
package serial

import (
	"sync"
	"time"
)

type FakeBackend struct {
	mu     sync.Mutex
	config Config
	writes [][]byte
	dtr    bool
	rts    bool
	rx     chan []byte
}

func NewFakeBackend() *FakeBackend {
	return &FakeBackend{config: DefaultConfig(), rx: make(chan []byte, 16)}
}

func (b *FakeBackend) ApplyConfig(config Config) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.config = config
	return nil
}

func (b *FakeBackend) Config() Config {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.config
}

func (b *FakeBackend) SetDTR(value bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.dtr = value
	return nil
}

func (b *FakeBackend) SetRTS(value bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rts = value
	return nil
}

func (b *FakeBackend) SendBreak(time.Duration) error {
	return nil
}

func (b *FakeBackend) Read(buf []byte) (int, error) {
	data := <-b.rx
	return copy(buf, data), nil
}

func (b *FakeBackend) InjectRX(data []byte) {
	b.rx <- append([]byte(nil), data...)
}

func (b *FakeBackend) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.writes = append(b.writes, append([]byte(nil), data...))
	return len(data), nil
}

func (b *FakeBackend) Close() error {
	return nil
}
```

- [ ] **Step 5: Implement worker, RX loop, and control session**

Create `internal/serial/worker.go` with a mutex-protected single owner. The implementation must:

1. Store `channelID`, `defaultConfig`, `backend`, `events`.
2. Reject a second `OpenControlSession` while `owner` is non-empty.
3. On `Close`, clear owner and call `backend.ApplyConfig(defaultConfig)`.
4. On `Write`, call backend and emit a `DirectionTX` event.
5. Provide `Run(ctx context.Context)` that reads from `backend.Read`, emits `DirectionRX` events, and returns when the context is canceled or the backend returns a terminal error.

Use this public constructor:

```go
func NewWorker(channelID string, defaultConfig Config, backend Backend) *Worker
```

- [ ] **Step 6: Verify serial tests**

Run:

```bash
go test ./internal/serial -v
```

Expected: serial tests pass.

- [ ] **Step 7: Add real backend dependency**

Run:

```bash
go get go.bug.st/serial
```

Create `internal/serial/real.go` with `RealBackend` that opens a serial port path and implements the complete `Backend` interface. Map read, write, baud, data bits, parity, stop bits, DTR, RTS, break, and close directly to `go.bug.st/serial` APIs.

- [ ] **Step 8: Verify all tests**

Run:

```bash
go test ./...
```

Expected: all tests pass.

- [ ] **Step 9: Commit**

```bash
git add go.mod go.sum internal/serial
git commit -m "feat: add serial control abstraction"
```

## Task 6: Topology Identity and udev Rule Rendering

**Files:**
- Create: `internal/topology/identity.go`
- Create: `internal/topology/udev_rules.go`
- Create: `internal/topology/topology_test.go`

- [ ] **Step 1: Write topology tests**

Create `internal/topology/topology_test.go`:

```go
package topology

import (
	"strings"
	"testing"
)

func TestParseUdevProperties(t *testing.T) {
	props := ParseUdevProperties(`ID_PATH=pci-0000:00:14.0-usb-0:1.2:1.0
ID_PATH_TAG=pci-0000_00_14_0-usb-0_1_2_1_0
ID_VENDOR_ID=1a86
ID_MODEL_ID=7523
DEVPATH=/devices/pci0000:00/0000:00:14.0/usb1/1-1/1-1.2/ttyUSB0
`)
	if props.IDPath != "pci-0000:00:14.0-usb-0:1.2:1.0" {
		t.Fatalf("IDPath = %q", props.IDPath)
	}
	if props.IDPathTag != "pci-0000_00_14_0-usb-0_1_2_1_0" {
		t.Fatalf("IDPathTag = %q", props.IDPathTag)
	}
	if props.VID != "1a86" || props.PID != "7523" {
		t.Fatalf("VID/PID = %q/%q", props.VID, props.PID)
	}
}

func TestRenderUdevRuleUsesIDPathTag(t *testing.T) {
	rule := RenderUdevRule(ChannelRule{
		IDPathTag: "pci-0000_00_14_0-usb-0_1_2_1_0",
		Symlink:   "lab/host01/hub01/port02/console",
	})
	if !strings.Contains(rule, `ENV{ID_PATH_TAG}=="pci-0000_00_14_0-usb-0_1_2_1_0"`) {
		t.Fatalf("rule missing ID_PATH_TAG: %s", rule)
	}
	if !strings.Contains(rule, `SYMLINK+="lab/host01/hub01/port02/console"`) {
		t.Fatalf("rule missing symlink: %s", rule)
	}
}
```

- [ ] **Step 2: Run tests to verify failure**

Run:

```bash
go test ./internal/topology -v
```

Expected: fail because topology package is not implemented.

- [ ] **Step 3: Implement topology identity parsing**

Create `internal/topology/identity.go`:

```go
package topology

import "strings"

type USBIdentity struct {
	IDPath      string
	IDPathTag   string
	SysfsDevpath string
	VID         string
	PID         string
	Driver      string
	Manufacturer string
	Product     string
}

func ParseUdevProperties(text string) USBIdentity {
	var out USBIdentity
	for _, line := range strings.Split(text, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch key {
		case "ID_PATH":
			out.IDPath = value
		case "ID_PATH_TAG":
			out.IDPathTag = value
		case "DEVPATH":
			out.SysfsDevpath = value
		case "ID_VENDOR_ID":
			out.VID = value
		case "ID_MODEL_ID":
			out.PID = value
		case "ID_USB_DRIVER":
			out.Driver = value
		case "ID_VENDOR":
			out.Manufacturer = value
		case "ID_MODEL":
			out.Product = value
		}
	}
	return out
}
```

- [ ] **Step 4: Implement udev rule rendering**

Create `internal/topology/udev_rules.go`:

```go
package topology

import "fmt"

type ChannelRule struct {
	IDPathTag string
	Symlink   string
}

func RenderUdevRule(rule ChannelRule) string {
	return fmt.Sprintf(`SUBSYSTEM=="tty", ENV{ID_PATH_TAG}=="%s", SYMLINK+="%s"`+"\n", rule.IDPathTag, rule.Symlink)
}
```

- [ ] **Step 5: Verify topology tests**

Run:

```bash
go test ./internal/topology -v
```

Expected: topology tests pass.

- [ ] **Step 6: Commit**

```bash
git add internal/topology
git commit -m "feat: add usb topology helpers"
```

## Task 7: Central Server HTTP API and Metadata Views

**Files:**
- Create: `internal/server/server.go`
- Create: `internal/server/api.go`
- Create: `internal/server/server_test.go`
- Modify: `cmd/central-server/main.go`

- [ ] **Step 1: Write HTTP API tests**

Create `internal/server/server_test.go`:

```go
package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"serial-platform/internal/storage"
)

func TestListAgentsAPI(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "meta.db"))
	if err != nil {
		t.Fatalf("storage.Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.UpsertAgent(storage.Agent{
		ID: "agent-1", Name: "node-1", Status: storage.AgentStatusPending,
		Hostname: "node-1", OS: "linux", Arch: "arm64", MachineID: "machine-1",
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("UpsertAgent returned error: %v", err)
	}

	srv := New(ServerConfig{DB: db})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/agents", nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var agents []storage.Agent
	if err := json.Unmarshal(rec.Body.Bytes(), &agents); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("len(agents) = %d", len(agents))
	}
}
```

- [ ] **Step 2: Run tests to verify failure**

Run:

```bash
go test ./internal/server -run TestListAgentsAPI -v
```

Expected: fail because `server.New` is not implemented.

- [ ] **Step 3: Implement server shell**

Create `internal/server/server.go`:

```go
package server

import (
	"net/http"

	"serial-platform/internal/storage"
)

type ServerConfig struct {
	DB *storage.DB
}

type Server struct {
	db  *storage.DB
	mux *http.ServeMux
}

func New(config ServerConfig) *Server {
	s := &Server{db: config.DB, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler {
	return s.mux
}
```

Create `internal/server/api.go`:

```go
package server

import (
	"encoding/json"
	"net/http"
)

func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/agents", s.handleListAgents)
	s.mux.HandleFunc("GET /api/channels", s.handleListChannels)
}

func (s *Server) handleListAgents(w http.ResponseWriter, r *http.Request) {
	agents, err := s.db.ListAgents()
	writeJSON(w, agents, err)
}

func (s *Server) handleListChannels(w http.ResponseWriter, r *http.Request) {
	channels, err := s.db.ListChannels()
	writeJSON(w, channels, err)
}

func writeJSON(w http.ResponseWriter, value any, err error) {
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(value)
}
```

- [ ] **Step 4: Wire central-server command**

Modify `cmd/central-server/main.go` to parse:

```text
--listen
--data-dir
```

The command runs these steps:

1. Create the data dir.
2. Open `<data-dir>/meta.db`.
3. Construct `server.New`.
4. Run `http.ListenAndServe`.

- [ ] **Step 5: Verify server tests and command build**

Run:

```bash
go test ./internal/server -v
go build ./cmd/central-server
```

Expected: tests pass and command builds.

- [ ] **Step 6: Commit**

```bash
git add cmd/central-server internal/server
git commit -m "feat: add central server api shell"
```

## Task 8: Agent Registration WebSocket

**Files:**
- Create: `internal/protocol/wsjson.go`
- Create: `internal/server/agent_registry.go`
- Create: `internal/server/agent_ws.go`
- Create: `internal/server/agent_ws_test.go`
- Create: `internal/agent/config.go`
- Create: `internal/agent/client.go`
- Modify: `go.mod`
- Modify: `go.sum`

- [ ] **Step 1: Add WebSocket dependency**

Run:

```bash
go get nhooyr.io/websocket
```

- [ ] **Step 2: Write WebSocket JSON helper**

Create `internal/protocol/wsjson.go` with `WriteJSON(ctx, conn, value)` and `ReadJSON(ctx, conn, target)` wrappers around `nhooyr.io/websocket/wsjson`.

- [ ] **Step 3: Write agent registry tests**

Create `internal/server/agent_ws_test.go`:

```go
package server

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
	"serial-platform/internal/protocol"
	"serial-platform/internal/storage"
)

func TestAgentHelloCreatesPendingAgent(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "meta.db"))
	if err != nil {
		t.Fatalf("storage.Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	srv := New(ServerConfig{DB: db})
	httpSrv := httptest.NewServer(srv.Handler())
	t.Cleanup(httpSrv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	wsURL := "ws" + httpSrv.URL[len("http"):] + "/ws/agent"
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	err = wsjson.Write(ctx, conn, protocol.AgentHello{
		Type: protocol.MessageAgentHello, AgentID: "agent-1", Hostname: "node-1",
		Version: "dev", OS: "linux", Arch: "arm64", MachineID: "machine-1",
	})
	if err != nil {
		t.Fatalf("wsjson.Write returned error: %v", err)
	}

	var accepted protocol.AgentAccepted
	if err := wsjson.Read(ctx, conn, &accepted); err != nil {
		t.Fatalf("wsjson.Read returned error: %v", err)
	}
	if accepted.Status != "pending" {
		t.Fatalf("Status = %q", accepted.Status)
	}
}
```

- [ ] **Step 4: Run tests to verify failure**

Run:

```bash
go test ./internal/server -run TestAgentHelloCreatesPendingAgent -v
```

Expected: fail because `/ws/agent` is not implemented.

- [ ] **Step 5: Implement agent WebSocket endpoint**

Add route `GET /ws/agent` in `internal/server/api.go`.

Create `internal/server/agent_registry.go` with an in-memory registry:

```go
type AgentConnection struct {
	AgentID string
	Conn    *websocket.Conn
	SeenAt  time.Time
}
```

Create `internal/server/agent_ws.go` that:

1. Accepts WebSocket.
2. Reads one `protocol.AgentHello`.
3. Upserts a pending `storage.Agent`.
4. Writes `protocol.AgentAccepted{Type: protocol.MessageAgentAccepted, Status: "pending"}`.
5. Keeps the connection open until context cancellation or read failure.

- [ ] **Step 6: Implement host-agent config and client**

Create `internal/agent/config.go`:

```go
package agent

type Config struct {
	ServerURL string
	DataDir   string
	AgentID   string
}
```

Create `internal/agent/client.go` with:

```go
type Client struct {
	Config Config
}
```

Add a `Connect(ctx)` method that dials `/ws/agent`, sends `protocol.AgentHello`, and returns the accepted status.

- [ ] **Step 7: Verify WebSocket tests**

Run:

```bash
go test ./internal/server ./internal/agent -v
```

Expected: tests pass.

- [ ] **Step 8: Commit**

```bash
git add go.mod go.sum internal/protocol internal/server internal/agent
git commit -m "feat: add agent websocket registration"
```

## Task 9: Agent Serial Worker Supervisor

**Files:**
- Create: `internal/agent/supervisor.go`
- Create: `internal/agent/supervisor_test.go`
- Modify: `cmd/host-agent/main.go`

- [ ] **Step 1: Write supervisor tests**

Create `internal/agent/supervisor_test.go`:

```go
package agent

import (
	"testing"

	"serial-platform/internal/serial"
)

func TestSupervisorAddsChannelWorker(t *testing.T) {
	supervisor := NewSupervisor()
	worker := serial.NewWorker("channel-1", serial.DefaultConfig(), serial.NewFakeBackend())
	if err := supervisor.AddChannel("channel-1", worker); err != nil {
		t.Fatalf("AddChannel returned error: %v", err)
	}
	if _, ok := supervisor.Channel("channel-1"); !ok {
		t.Fatal("Channel channel-1 not found")
	}
	if err := supervisor.AddChannel("channel-1", worker); err == nil {
		t.Fatal("AddChannel duplicate returned nil error")
	}
}
```

- [ ] **Step 2: Run tests to verify failure**

Run:

```bash
go test ./internal/agent -run TestSupervisorAddsChannelWorker -v
```

Expected: fail because `NewSupervisor` is not defined.

- [ ] **Step 3: Implement supervisor**

Create `internal/agent/supervisor.go`:

```go
package agent

import (
	"errors"
	"sync"

	"serial-platform/internal/serial"
)

type Supervisor struct {
	mu       sync.Mutex
	channels map[string]serial.SerialControl
}

func NewSupervisor() *Supervisor {
	return &Supervisor{channels: make(map[string]serial.SerialControl)}
}

func (s *Supervisor) AddChannel(id string, control serial.SerialControl) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.channels[id]; exists {
		return errors.New("channel already exists")
	}
	s.channels[id] = control
	return nil
}

func (s *Supervisor) Channel(id string) (serial.SerialControl, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	control, ok := s.channels[id]
	return control, ok
}
```

- [ ] **Step 4: Wire host-agent command**

Modify `cmd/host-agent/main.go` to parse:

```text
--server
--data-dir
--agent-id
```

The command must:

1. Create data dir.
2. Create or read `<data-dir>/agent_id` when `--agent-id` is empty.
3. Connect to central-server through `internal/agent.Client`.
4. Print accepted status.

- [ ] **Step 5: Verify agent tests and command build**

Run:

```bash
go test ./internal/agent -v
go build ./cmd/host-agent
```

Expected: tests pass and command builds.

- [ ] **Step 6: Commit**

```bash
git add cmd/host-agent internal/agent
git commit -m "feat: add agent supervisor"
```

## Task 10: Log Upload Pipeline

**Files:**
- Create: `internal/server/log_ws.go`
- Create: `internal/server/log_ws_test.go`
- Modify: `internal/agent/client.go`
- Modify: `internal/serial/worker.go`
- Modify: `internal/protocol/messages.go`

- [ ] **Step 1: Write server log upload test**

Create `internal/server/log_ws_test.go`:

```go
package server

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"nhooyr.io/websocket"
	"serial-platform/internal/protocol"
	"serial-platform/internal/storage"
)

func TestLogWebSocketAcceptsBinaryFrame(t *testing.T) {
	root := t.TempDir()
	db, err := storage.Open(filepath.Join(root, "meta.db"))
	if err != nil {
		t.Fatalf("storage.Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	srv := New(ServerConfig{DB: db, LogDir: filepath.Join(root, "logs")})
	httpSrv := httptest.NewServer(srv.Handler())
	t.Cleanup(httpSrv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+httpSrv.URL[len("http"):]+"/ws/logs", nil)
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	frame, err := protocol.EncodeLogFrame(protocol.LogFrame{
		ChannelID: "channel-1", Seq: 1, TimestampNS: time.Now().UnixNano(),
		Direction: protocol.DirectionRX, Flags: protocol.FlagRaw, Payload: []byte("boot\n"),
	})
	if err != nil {
		t.Fatalf("EncodeLogFrame returned error: %v", err)
	}
	if err := conn.Write(ctx, websocket.MessageBinary, frame); err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
}
```

- [ ] **Step 2: Run tests to verify failure**

Run:

```bash
go test ./internal/server -run TestLogWebSocketAcceptsBinaryFrame -v
```

Expected: fail because `/ws/logs` and `ServerConfig.LogDir` are not implemented.

- [ ] **Step 3: Implement server log WebSocket**

Add `LogDir string` to `server.ServerConfig`.

Create `internal/server/log_ws.go` that:

1. Accepts WebSocket at `GET /ws/logs`.
2. Reads binary messages.
3. Decodes `protocol.LogFrame`.
4. Writes frames through `logstore.SegmentWriter`.
5. Rejects text messages with close status `StatusUnsupportedData`.

- [ ] **Step 4: Implement agent log sender**

Modify `internal/agent/client.go` to add:

```go
func (c *Client) SendLogFrames(ctx context.Context, frames <-chan protocol.LogFrame) error
```

This method dials `/ws/logs`, encodes each frame, and writes it as a binary WebSocket message.

- [ ] **Step 5: Wire serial events to log frames**

Add an adapter in `internal/agent/supervisor.go`:

```go
func SerialEventToLogFrame(seq uint64, event serial.Event) protocol.LogFrame
```

Map `serial.DirectionRX` to `protocol.DirectionRX` and `serial.DirectionTX` to `protocol.DirectionTX`.

- [ ] **Step 6: Verify log upload tests**

Run:

```bash
go test ./internal/server ./internal/agent ./internal/logstore -v
```

Expected: tests pass.

- [ ] **Step 7: Commit**

```bash
git add internal/server internal/agent internal/serial internal/protocol
git commit -m "feat: add log upload pipeline"
```

## Task 11: RFC2217 Parser and Control Translation

**Files:**
- Create: `internal/rfc2217/constants.go`
- Create: `internal/rfc2217/parser.go`
- Create: `internal/rfc2217/session.go`
- Create: `internal/rfc2217/parser_test.go`
- Create: `internal/server/rfc2217_listener.go`

- [ ] **Step 1: Write RFC2217 parser tests**

Create `internal/rfc2217/parser_test.go`:

```go
package rfc2217

import "testing"

func TestParseSetBaudrate(t *testing.T) {
	cmds, data, err := ParseClientBytes([]byte{IAC, SB, COMPortOption, SetBaudrate, 0x00, 0x1c, 0x20, 0x00, IAC, SE})
	if err != nil {
		t.Fatalf("ParseClientBytes returned error: %v", err)
	}
	if len(data) != 0 {
		t.Fatalf("data len = %d", len(data))
	}
	if len(cmds) != 1 {
		t.Fatalf("cmd count = %d", len(cmds))
	}
	if cmds[0].Kind != CommandSetBaudrate || cmds[0].Baudrate != 1843200 {
		t.Fatalf("command = %+v", cmds[0])
	}
}

func TestParseSetDTRAndRTS(t *testing.T) {
	cmds, _, err := ParseClientBytes([]byte{
		IAC, SB, COMPortOption, SetControl, ControlDTRON, IAC, SE,
		IAC, SB, COMPortOption, SetControl, ControlRTSOFF, IAC, SE,
	})
	if err != nil {
		t.Fatalf("ParseClientBytes returned error: %v", err)
	}
	if len(cmds) != 2 {
		t.Fatalf("cmd count = %d", len(cmds))
	}
	if cmds[0].Kind != CommandSetDTR || !cmds[0].BoolValue {
		t.Fatalf("first command = %+v", cmds[0])
	}
	if cmds[1].Kind != CommandSetRTS || cmds[1].BoolValue {
		t.Fatalf("second command = %+v", cmds[1])
	}
}

func TestParsePassThroughData(t *testing.T) {
	_, data, err := ParseClientBytes([]byte("AT\r\n"))
	if err != nil {
		t.Fatalf("ParseClientBytes returned error: %v", err)
	}
	if string(data) != "AT\r\n" {
		t.Fatalf("data = %q", string(data))
	}
}
```

- [ ] **Step 2: Run tests to verify failure**

Run:

```bash
go test ./internal/rfc2217 -v
```

Expected: fail because RFC2217 package is not implemented.

- [ ] **Step 3: Implement RFC2217 constants**

Create `internal/rfc2217/constants.go`:

```go
package rfc2217

const (
	IAC byte = 255
	DONT byte = 254
	DO byte = 253
	WONT byte = 252
	WILL byte = 251
	SB byte = 250
	SE byte = 240

	COMPortOption byte = 44

	SetBaudrate byte = 1
	SetDataSize byte = 2
	SetParity byte = 3
	SetStopSize byte = 4
	SetControl byte = 5

	ControlBreakON byte = 5
	ControlBreakOFF byte = 6
	ControlDTRON byte = 8
	ControlDTROFF byte = 9
	ControlRTSON byte = 11
	ControlRTSOFF byte = 12
)
```

- [ ] **Step 4: Implement RFC2217 parser**

Create `internal/rfc2217/parser.go` with:

```go
package rfc2217

import (
	"encoding/binary"
	"errors"
)

type CommandKind int

const (
	CommandSetBaudrate CommandKind = 1
	CommandSetDataBits CommandKind = 2
	CommandSetParity CommandKind = 3
	CommandSetStopBits CommandKind = 4
	CommandSetDTR CommandKind = 5
	CommandSetRTS CommandKind = 6
	CommandSetBreak CommandKind = 7
)

type Command struct {
	Kind CommandKind
	Baudrate int
	IntValue int
	BoolValue bool
}

func ParseClientBytes(in []byte) ([]Command, []byte, error) {
	var commands []Command
	data := make([]byte, 0, len(in))
	for i := 0; i < len(in); {
		if in[i] != IAC {
			data = append(data, in[i])
			i++
			continue
		}
		if i+1 >= len(in) {
			return nil, nil, errors.New("truncated telnet command")
		}
		if in[i+1] != SB {
			i += 2
			continue
		}
		end := findSubnegotiationEnd(in, i+2)
		if end < 0 {
			return nil, nil, errors.New("unterminated subnegotiation")
		}
		payload := in[i+2:end]
		cmd, ok, err := parseSubnegotiation(payload)
		if err != nil {
			return nil, nil, err
		}
		if ok {
			commands = append(commands, cmd)
		}
		i = end + 2
	}
	return commands, data, nil
}

func findSubnegotiationEnd(in []byte, start int) int {
	for i := start; i+1 < len(in); i++ {
		if in[i] == IAC && in[i+1] == SE {
			return i
		}
	}
	return -1
}

func parseSubnegotiation(payload []byte) (Command, bool, error) {
	if len(payload) < 2 || payload[0] != COMPortOption {
		return Command{}, false, nil
	}
	switch payload[1] {
	case SetBaudrate:
		if len(payload) != 6 {
			return Command{}, false, errors.New("invalid SET-BAUDRATE length")
		}
		return Command{Kind: CommandSetBaudrate, Baudrate: int(binary.BigEndian.Uint32(payload[2:6]))}, true, nil
	case SetDataSize:
		if len(payload) != 3 {
			return Command{}, false, errors.New("invalid SET-DATASIZE length")
		}
		return Command{Kind: CommandSetDataBits, IntValue: int(payload[2])}, true, nil
	case SetParity:
		if len(payload) != 3 {
			return Command{}, false, errors.New("invalid SET-PARITY length")
		}
		return Command{Kind: CommandSetParity, IntValue: int(payload[2])}, true, nil
	case SetStopSize:
		if len(payload) != 3 {
			return Command{}, false, errors.New("invalid SET-STOPSIZE length")
		}
		return Command{Kind: CommandSetStopBits, IntValue: int(payload[2])}, true, nil
	case SetControl:
		if len(payload) != 3 {
			return Command{}, false, errors.New("invalid SET-CONTROL length")
		}
		switch payload[2] {
		case ControlBreakON:
			return Command{Kind: CommandSetBreak, BoolValue: true}, true, nil
		case ControlBreakOFF:
			return Command{Kind: CommandSetBreak, BoolValue: false}, true, nil
		case ControlDTRON:
			return Command{Kind: CommandSetDTR, BoolValue: true}, true, nil
		case ControlDTROFF:
			return Command{Kind: CommandSetDTR, BoolValue: false}, true, nil
		case ControlRTSON:
			return Command{Kind: CommandSetRTS, BoolValue: true}, true, nil
		case ControlRTSOFF:
			return Command{Kind: CommandSetRTS, BoolValue: false}, true, nil
		}
	}
	return Command{}, false, nil
}
```

- [ ] **Step 5: Implement RFC2217 session translation**

Create `internal/rfc2217/session.go` that accepts parsed commands and a `serial.ControlSession`. It must:

1. Call `SetConfig` for baud/data/parity/stop changes.
2. Call `SetDTR` and `SetRTS` for modem control.
3. Call `SendBreak` for break on and break off using a short duration for break on.
4. Call `Write` for pass-through data.

- [ ] **Step 6: Implement central RFC2217 listener**

Create `internal/server/rfc2217_listener.go` with a `RFC2217Listener` type that:

1. Listens on configured per-channel ports.
2. Rejects offline, disabled, or busy channels.
3. Opens one control session.
4. Pipes TCP client bytes through `rfc2217.ParseClientBytes`.
5. Writes serial RX bytes back to TCP client from the serial event stream.

- [ ] **Step 7: Verify RFC2217 tests**

Run:

```bash
go test ./internal/rfc2217 ./internal/server -v
```

Expected: all RFC2217 and server tests pass.

- [ ] **Step 8: Commit**

```bash
git add internal/rfc2217 internal/server
git commit -m "feat: add rfc2217 control translation"
```

## Task 12: Web Terminal and Live Log API

**Files:**
- Create: `internal/server/web_terminal.go`
- Create: `internal/server/live_log.go`
- Create: `internal/server/web_terminal_test.go`
- Modify: `internal/protocol/messages.go`

- [ ] **Step 1: Write Web terminal tests**

Create `internal/server/web_terminal_test.go`:

```go
package server

import "testing"

func TestControlOwnerRejectsSecondSession(t *testing.T) {
	owner := NewControlOwner()
	if err := owner.Acquire("channel-1", "web"); err != nil {
		t.Fatalf("Acquire first returned error: %v", err)
	}
	if err := owner.Acquire("channel-1", "rfc2217"); err == nil {
		t.Fatal("Acquire second returned nil error")
	}
	owner.Release("channel-1", "web")
	if err := owner.Acquire("channel-1", "rfc2217"); err != nil {
		t.Fatalf("Acquire after release returned error: %v", err)
	}
}
```

- [ ] **Step 2: Run tests to verify failure**

Run:

```bash
go test ./internal/server -run TestControlOwnerRejectsSecondSession -v
```

Expected: fail because `NewControlOwner` is not implemented.

- [ ] **Step 3: Implement control owner**

Create `internal/server/web_terminal.go` with:

```go
package server

import (
	"errors"
	"sync"
)

type ControlOwner struct {
	mu     sync.Mutex
	owners map[string]string
}

func NewControlOwner() *ControlOwner {
	return &ControlOwner{owners: make(map[string]string)}
}

func (o *ControlOwner) Acquire(channelID, owner string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if current := o.owners[channelID]; current != "" {
		return errors.New("channel is busy")
	}
	o.owners[channelID] = owner
	return nil
}

func (o *ControlOwner) Release(channelID, owner string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.owners[channelID] == owner {
		delete(o.owners, channelID)
	}
}
```

In the same file, add a WebSocket handler at `GET /ws/terminal/{channelID}`. The handler reads platform JSON control messages, calls the channel `SerialControl`, and shares the same `ControlOwner` with the RFC2217 listener.

- [ ] **Step 4: Implement live log fanout**

Create `internal/server/live_log.go` with an in-memory `LiveLogHub`:

1. `Publish(frame protocol.LogFrame)`.
2. `Subscribe(channelID string) (<-chan protocol.LogFrame, func())`.
3. Drop oldest frame when a subscriber channel is full.

Route `GET /ws/live-log/{channelID}` streams log frames to browser clients as JSON objects with base64 payload.

- [ ] **Step 5: Verify server tests**

Run:

```bash
go test ./internal/server -v
```

Expected: server tests pass.

- [ ] **Step 6: Commit**

```bash
git add internal/server internal/protocol
git commit -m "feat: add web terminal control paths"
```

## Task 13: Logs Download API and CLI

**Files:**
- Create: `internal/server/log_download.go`
- Create: `internal/server/log_download_test.go`
- Modify: `cmd/serialctl/main.go`

- [ ] **Step 1: Write log download API tests**

Create `internal/server/log_download_test.go`:

```go
package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"serial-platform/internal/storage"
)

func TestLogDownloadRequiresChannel(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "meta.db"))
	if err != nil {
		t.Fatalf("storage.Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	srv := New(ServerConfig{DB: db, LogDir: t.TempDir()})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/logs/download", nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "channel_id") {
		t.Fatalf("body = %q", rec.Body.String())
	}
}
```

- [ ] **Step 2: Run tests to verify failure**

Run:

```bash
go test ./internal/server -run TestLogDownloadRequiresChannel -v
```

Expected: fail because `/api/logs/download` is not implemented.

- [ ] **Step 3: Implement log download endpoint**

Create `internal/server/log_download.go`:

```go
package server

import "net/http"

func (s *Server) handleLogDownload(w http.ResponseWriter, r *http.Request) {
	channelID := r.URL.Query().Get("channel_id")
	if channelID == "" {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("channel_id is required"))
		return
	}
}
```

Implement the endpoint to:

1. Parse `from`, `to`, `format`, `direction`, `timestamp`, `direction_label`, and `strip_ansi`.
2. Resolve matching log segments from storage.
3. For `format=text`, call `logstore.ExportText`.
4. For `format=raw`, stream raw segment bytes in time order.

- [ ] **Step 4: Implement serialctl minimal commands**

Modify `cmd/serialctl/main.go` to support:

```text
serialctl --server http://central:8080 hosts list
serialctl --server http://central:8080 channels list
serialctl --server http://central:8080 rfc2217 list
serialctl --server http://central:8080 logs download --channel-id channel-1 --from 2026-05-19T00:00:00Z --to 2026-05-19T01:00:00Z --output out.log
```

Use only Go standard library `flag`, `net/http`, and `encoding/json`.

- [ ] **Step 5: Verify API and CLI**

Run:

```bash
go test ./internal/server -v
go build ./cmd/serialctl
```

Expected: tests pass and CLI builds.

- [ ] **Step 6: Commit**

```bash
git add cmd/serialctl internal/server
git commit -m "feat: add log download api and cli"
```

## Task 14: Frontend Web UI Shell

**Files:**
- Create: `web/package.json`
- Create: `web/index.html`
- Create: `web/tsconfig.json`
- Create: `web/vite.config.ts`
- Create: `web/src/main.tsx`
- Create: `web/src/App.tsx`
- Create: `web/src/api.ts`
- Create: `web/src/styles.css`
- Modify: `cmd/central-server/main.go`
- Create: `internal/server/static.go`

- [ ] **Step 1: Create Vite React frontend**

Create `web/package.json`:

```json
{
  "scripts": {
    "dev": "vite",
    "build": "vite build",
    "lint": "tsc --noEmit"
  },
  "dependencies": {
    "@vitejs/plugin-react": "latest",
    "vite": "latest",
    "typescript": "latest",
    "react": "latest",
    "react-dom": "latest",
    "lucide-react": "latest"
  },
  "devDependencies": {}
}
```

Create a five-view UI in `web/src/App.tsx`:

```text
Hosts
Channels
Calibration
Live Log / Terminal
Logs
```

Use compact operational styling, no landing page, no marketing hero.

- [ ] **Step 2: Implement API helpers**

Create `web/src/api.ts` with:

```ts
export async function getJSON<T>(path: string): Promise<T> {
  const res = await fetch(path);
  if (!res.ok) {
    throw new Error(await res.text());
  }
  return res.json() as Promise<T>;
}
```

- [ ] **Step 3: Build frontend**

Run:

```bash
cd web
npm install
npm run build
```

Expected: `web/dist` is created.

- [ ] **Step 4: Embed frontend in central-server**

Create `internal/server/static.go`:

```go
package server

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed all:webdist
var embeddedWeb embed.FS

func (s *Server) mountStatic() {
	sub, err := fs.Sub(embeddedWeb, "webdist")
	if err != nil {
		panic(err)
	}
	s.mux.Handle("/", http.FileServer(http.FS(sub)))
}
```

Task 15 copies `web/dist` to `internal/server/webdist` before `go build`.

- [ ] **Step 5: Verify frontend build**

Run:

```bash
cd web
npm run lint
npm run build
cd ..
```

Expected: TypeScript and Vite builds pass.

- [ ] **Step 6: Commit**

```bash
git add web internal/server/static.go cmd/central-server
git commit -m "feat: add web ui shell"
```

## Task 15: Install and Release Scripts

**Files:**
- Create: `scripts/install-central.sh`
- Create: `scripts/install-agent.sh`
- Create: `scripts/build-release.sh`
- Create: `scripts/install_scripts_test.sh`

- [ ] **Step 1: Write install script smoke test**

Create `scripts/install_scripts_test.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail

bash -n scripts/install-central.sh
bash -n scripts/install-agent.sh
bash -n scripts/build-release.sh

grep -q "systemctl daemon-reload" scripts/install-central.sh
grep -q "systemctl daemon-reload" scripts/install-agent.sh
grep -q "udevadm control --reload-rules" scripts/install-agent.sh && exit 1

echo "install script smoke test passed"
```

- [ ] **Step 2: Create central install script**

Create `scripts/install-central.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail

DATA_DIR="/data/serial-platform"
LISTEN=":8080"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --data-dir) DATA_DIR="$2"; shift 2 ;;
    --listen) LISTEN="$2"; shift 2 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

install -m 0755 central-server-linux-amd64 /usr/local/bin/central-server
install -d -m 0755 "$DATA_DIR"
cat >/etc/systemd/system/serial-platform-central.service <<UNIT
[Unit]
Description=Serial Platform Central Server
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/central-server --data-dir $DATA_DIR --listen $LISTEN
Restart=always
RestartSec=2

[Install]
WantedBy=multi-user.target
UNIT

systemctl daemon-reload
systemctl enable --now serial-platform-central.service
```

- [ ] **Step 3: Create agent install script**

Create `scripts/install-agent.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail

SERVER=""
DATA_DIR="/var/lib/serial-agent"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --server) SERVER="$2"; shift 2 ;;
    --data-dir) DATA_DIR="$2"; shift 2 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

if [[ -z "$SERVER" ]]; then
  echo "--server is required" >&2
  exit 2
fi

command -v systemctl >/dev/null
command -v udevadm >/dev/null

ARCH="$(uname -m)"
case "$ARCH" in
  x86_64) BIN="host-agent-linux-amd64" ;;
  aarch64) BIN="host-agent-linux-arm64" ;;
  armv7l|armv6l) BIN="host-agent-linux-armv7" ;;
  *) echo "unsupported architecture: $ARCH" >&2; exit 1 ;;
esac

install -m 0755 "$BIN" /usr/local/bin/host-agent
install -d -m 0755 "$DATA_DIR"
cat >/etc/systemd/system/serial-platform-agent.service <<UNIT
[Unit]
Description=Serial Platform Host Agent
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/host-agent --server $SERVER --data-dir $DATA_DIR
Restart=always
RestartSec=2

[Install]
WantedBy=multi-user.target
UNIT

systemctl daemon-reload
systemctl enable --now serial-platform-agent.service
```

This script checks that `udevadm` exists but does not generate or reload channel-level udev rules.

- [ ] **Step 4: Create release build script**

Create `scripts/build-release.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail

rm -rf dist
mkdir -p dist

if [[ -d web ]]; then
  (cd web && npm install && npm run build)
  rm -rf internal/server/webdist
  mkdir -p internal/server/webdist
  cp -R web/dist/. internal/server/webdist/
fi

GOOS=linux GOARCH=amd64 go build -o dist/central-server-linux-amd64 ./cmd/central-server
GOOS=linux GOARCH=amd64 go build -o dist/host-agent-linux-amd64 ./cmd/host-agent
GOOS=linux GOARCH=arm64 go build -o dist/host-agent-linux-arm64 ./cmd/host-agent
GOOS=linux GOARCH=arm GOARM=7 go build -o dist/host-agent-linux-armv7 ./cmd/host-agent
GOOS=linux GOARCH=amd64 go build -o dist/serialctl-linux-amd64 ./cmd/serialctl

cp scripts/install-central.sh dist/
cp scripts/install-agent.sh dist/
tar -C dist -czf serial-platform-linux.tar.gz .
```

- [ ] **Step 5: Verify scripts**

Run:

```bash
chmod +x scripts/*.sh
bash scripts/install_scripts_test.sh
```

Expected:

```text
install script smoke test passed
```

- [ ] **Step 6: Commit**

```bash
git add scripts
git commit -m "feat: add install and release scripts"
```

## Task 16: End-to-End Smoke Test Documentation

**Files:**
- Create: `docs/superpowers/plans/2026-05-19-serial-platform-smoke-test.md`
- Create: `README.md`

- [ ] **Step 1: Create README**

Create `README.md`:

```markdown
# Serial Platform

Internal serial test platform for managing USB serial channels, logs, remote COM access, and host agents.

## First Version

- central-server exposes Web/API/RFC2217.
- host-agent owns local serial devices.
- Logs are stored centrally as TX+RX raw framed traffic.
- Browser terminal uses platform WebSocket control.
- External serial tools use RFC2217.

## Design

See `docs/superpowers/specs/2026-05-19-serial-platform-design.md`.

## Build

```bash
make test
make build
```
```

- [ ] **Step 2: Create smoke test plan**

Create `docs/superpowers/plans/2026-05-19-serial-platform-smoke-test.md`:

```markdown
# Serial Platform Smoke Test

1. Build binaries with `make build`.
2. Start central-server with `./bin/central-server --data-dir .server-data --listen :8080`.
3. Start host-agent with `./bin/host-agent --server ws://127.0.0.1:8080 --data-dir .agent-data`.
4. Open `http://127.0.0.1:8080/api/agents`.
5. Confirm the agent appears with `pending` status.
6. Create a fake channel through test fixture or API.
7. Connect Web terminal and verify a second control connection is rejected.
8. Send RX and TX frames through the fake serial worker.
9. Download logs with `serialctl logs download`.
10. Verify UTF-8 export escapes invalid bytes as `\xNN`.
```

- [ ] **Step 3: Run final checks**

Run:

```bash
make test
make build
bash scripts/install_scripts_test.sh
```

Expected: tests pass, binaries build, script smoke test passes.

- [ ] **Step 4: Commit**

```bash
git add README.md docs/superpowers/plans/2026-05-19-serial-platform-smoke-test.md
git commit -m "docs: add serial platform smoke test"
```

## Self-Review Checklist

Before implementation begins, verify:

- Every first-version requirement in `docs/superpowers/specs/2026-05-19-serial-platform-design.md` maps to at least one task above.
- `install-agent.sh` checks udev availability but does not generate or reload channel-level udev rules.
- `host-agent` remains the owner of udev rule generation and serial file descriptors.
- `SerialControl` is the only control abstraction used by Web terminal, RFC2217, and future flash recipes.
- SQLite stores metadata only; raw traffic is stored in log segment files.
- Agent disk spool is not implemented.
- RFC2217 is only the external remote COM protocol.
