# Web UX Terminal I18n Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 改善 serial-platform Web 第一版的真实使用体验：导航重命名、Terminal 页面切换不断开、日志换行正确、channel 可删除且同步清理日志、刷新有反馈、Web 支持中英文切换。

**Architecture:** 保持 B2 轻量模块化 SPA，不引入 React Router。后端只新增 channel 删除和文本导出换行语义；前端把 i18n、Terminal session、live log buffer、页面组件拆到独立模块，`App.tsx` 只保留 shell、全局刷新和 provider 组合。

**Tech Stack:** Go 1.25, `modernc.org/sqlite`, `nhooyr.io/websocket`, React 19, Vite, TypeScript, lucide-react, SQLite + filesystem log segments, agent-browser browser smoke.

---

## Execution Rules

- 对话沟通使用中文；代码注释使用英文；文档使用中文。
- 实现子代理必须使用 `gpt-5.5` 且 `reasoning_effort: xhigh`。
- 每个任务按 TDD 顺序执行：先写失败测试，再实现，再跑目标测试和相关验证，再提交。
- 后端保持低耦合：
  - `internal/storage` 只管理 SQLite 元数据。
  - `internal/server` 只编排 HTTP API、ControlOwner、日志文件路径校验和文件删除。
  - `internal/logstore` 只处理 raw framed traffic 的读取和导出。
- 前端保持低耦合：
  - `App.tsx` 不再持有 Terminal WebSocket 细节。
  - WebSocket 实例和 pending request map 放在 provider 的 refs 里。
  - 页面组件只消费 props/context，不直接知道底层 API 之外的实现细节。
- 不引入 React Router、i18n 依赖、测试框架、登录权限、数据库服务或 Docker。
- 不占用真实串口做无关测试；真实设备验证使用：
  - `/dev/ttyUSB0`: loopback。
  - `/dev/ttyUSB1`: 真实持续输出日志设备，波特率 `2000000`。

## File Map

### Create

- `internal/server/log_delete.go` - 根据 channel log segment 元数据安全删除日志分片文件。
- `web/src/i18n.ts` - 静态中英文字典、语言类型、默认语言选择。
- `web/src/i18n-context.tsx` - `I18nProvider`、`useI18n()`、语言持久化。
- `web/src/terminal-session.tsx` - Terminal 控制 WebSocket provider，跨页面保持连接。
- `web/src/live-log-buffer.ts` - live log frame 到显示行的纯函数 buffer。
- `web/src/components/Badge.tsx` - 状态 badge。
- `web/src/components/EmptyRow.tsx` - 表格空状态行。
- `web/src/components/Metric.tsx` - 顶部统计块。
- `web/src/components/ViewTitle.tsx` - 页面标题。
- `web/src/components/FormFeedback.tsx` - 表单错误/成功反馈。
- `web/src/components/Quota.tsx` - Logs 页面配额占位展示。
- `web/src/pages/AgentsPage.tsx` - 原 Hosts 页面，命名为 Agents。
- `web/src/pages/DevicesPage.tsx` - 原 Calibration 页面，展示 candidates 并创建 channel。
- `web/src/pages/ChannelsPage.tsx` - channel 列表、手工创建、enable/disable/delete。
- `web/src/pages/TerminalPage.tsx` - Terminal UI、live log 显示、串口控制表单。
- `web/src/pages/LogsPage.tsx` - 日志下载页面。

### Modify

- `internal/storage/db.go` - 新增 `DeleteChannelWithLogSegments`，在事务中删除 channel 和该 channel 的 log segment 元数据。
- `internal/storage/db_test.go` - 覆盖 channel + log segment 元数据事务删除。
- `internal/server/api.go` - 新增 `handleDeleteChannel`。
- `internal/server/server.go` - 注册 `DELETE /api/channels/{channelID}`。
- `internal/server/channel_api_test.go` - 覆盖 delete API 正常、404、busy、日志缺失、文件删除失败。
- `internal/server/web_terminal.go` - `ControlOwner` 增加只读 busy 查询。
- `internal/logstore/export.go` - 文本导出保留 payload 自身换行语义。
- `internal/logstore/logstore_test.go` - 覆盖多 frame 拼接、payload 内换行、timestamp/direction 前缀。
- `web/src/api.ts` - 增加 `deleteJSON` 或 `deleteNoContent` helper。
- `web/src/types.ts` - 增加页面/语言/Terminal session/live log 类型。
- `web/src/App.tsx` - 简化为 shell、全局刷新、导航、provider 组合。
- `web/src/styles.css` - 修复 Terminal 初始高度、删除确认、刷新反馈、语言选择和页面拆分后的样式。

## Task 1: Backend Channel Delete API and Log Cleanup

**Files:**
- Create: `internal/server/log_delete.go`
- Modify: `internal/storage/db.go`
- Modify: `internal/storage/db_test.go`
- Modify: `internal/server/web_terminal.go`
- Modify: `internal/server/api.go`
- Modify: `internal/server/server.go`
- Modify: `internal/server/channel_api_test.go`

- [ ] **Step 1: Add storage failing test for metadata transaction delete**

Append to `internal/storage/db_test.go`:

```go
func TestDBDeletesChannelWithLogSegments(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "meta.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	channel := testChannel("channel-1")
	if err := db.UpsertChannel(channel); err != nil {
		t.Fatalf("UpsertChannel returned error: %v", err)
	}
	other := testChannel("channel-2")
	other.RFC2217Port = 7002
	if err := db.UpsertChannel(other); err != nil {
		t.Fatalf("UpsertChannel other returned error: %v", err)
	}
	now := time.Unix(1700000000, 0).UTC()
	for _, segment := range []LogSegment{
		{ChannelID: "channel-1", Path: "channel-1/a.rlog", StartTime: now, EndTime: now, SizeBytes: 12, FrameCount: 1, Status: LogSegmentStatusClosed},
		{ChannelID: "channel-1", Path: "channel-1/b.rlog", StartTime: now, EndTime: now, SizeBytes: 24, FrameCount: 2, Status: LogSegmentStatusClosed},
		{ChannelID: "channel-2", Path: "channel-2/c.rlog", StartTime: now, EndTime: now, SizeBytes: 36, FrameCount: 3, Status: LogSegmentStatusClosed},
	} {
		if err := db.InsertLogSegment(segment); err != nil {
			t.Fatalf("InsertLogSegment returned error: %v", err)
		}
	}

	if err := db.DeleteChannelWithLogSegments("channel-1"); err != nil {
		t.Fatalf("DeleteChannelWithLogSegments returned error: %v", err)
	}
	if _, err := db.GetChannel("channel-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetChannel channel-1 error = %v, want ErrNotFound", err)
	}
	deletedSegments, err := db.ListLogSegments("channel-1", now.Add(-time.Second), now.Add(time.Second))
	if err != nil {
		t.Fatalf("ListLogSegments deleted returned error: %v", err)
	}
	if len(deletedSegments) != 0 {
		t.Fatalf("deleted channel segments = %+v, want empty", deletedSegments)
	}
	remainingSegments, err := db.ListLogSegments("channel-2", now.Add(-time.Second), now.Add(time.Second))
	if err != nil {
		t.Fatalf("ListLogSegments remaining returned error: %v", err)
	}
	if len(remainingSegments) != 1 || remainingSegments[0].Path != "channel-2/c.rlog" {
		t.Fatalf("remaining segments = %+v, want channel-2 segment", remainingSegments)
	}
}

func TestDBDeleteChannelWithLogSegmentsRejectsMissingChannel(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "meta.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := db.DeleteChannelWithLogSegments("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("DeleteChannelWithLogSegments error = %v, want ErrNotFound", err)
	}
}
```

- [ ] **Step 2: Run storage test and verify failure**

Run:

```bash
rtk go test ./internal/storage -run 'TestDBDelete' -count=1
```

Expected: fail with `db.DeleteChannelWithLogSegments undefined`.

- [ ] **Step 3: Implement storage transaction delete**

Add to `internal/storage/db.go` near `DeleteChannel`:

```go
func (db *DB) DeleteChannelWithLogSegments(id string) error {
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	result, err := tx.Exec(`DELETE FROM channels WHERE id = ?`, id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	if _, err := tx.Exec(`DELETE FROM log_segments WHERE channel_id = ?`, id); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}
```

- [ ] **Step 4: Run storage tests**

Run:

```bash
rtk go test ./internal/storage -run 'TestDBDelete|TestDBDeletesChannelWithLogSegments' -count=1
```

Expected: PASS.

- [ ] **Step 5: Add server API failing tests**

Append to `internal/server/channel_api_test.go`:

```go
func TestChannelAPIDeleteRemovesChannelAndLogs(t *testing.T) {
	root := t.TempDir()
	db := newAPITestDB(t)
	logDir := filepath.Join(root, "logs")
	channel := apiTestChannel("channel-1", 7001)
	if err := db.UpsertChannel(channel); err != nil {
		t.Fatalf("UpsertChannel returned error: %v", err)
	}
	now := time.Unix(1700000000, 0).UTC()
	segmentPath := filepath.Join("channel-1", "segment.rlog")
	fullSegmentPath := filepath.Join(logDir, segmentPath)
	if err := os.MkdirAll(filepath.Dir(fullSegmentPath), 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.WriteFile(fullSegmentPath, []byte("raw"), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	if err := db.InsertLogSegment(storage.LogSegment{
		ChannelID: "channel-1",
		Path: segmentPath,
		StartTime: now,
		EndTime: now,
		SizeBytes: 3,
		FrameCount: 1,
		Status: storage.LogSegmentStatusClosed,
	}); err != nil {
		t.Fatalf("InsertLogSegment returned error: %v", err)
	}
	srv := server.New(server.ServerConfig{DB: db, LogDir: logDir})
	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)

	req, err := http.NewRequest(http.MethodDelete, httpSrv.URL+"/api/channels/channel-1", nil)
	if err != nil {
		t.Fatalf("NewRequest returned error: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do returned error: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE status = %s, body = %s", resp.Status, body)
	}
	if _, err := db.GetChannel("channel-1"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("GetChannel error = %v, want ErrNotFound", err)
	}
	if _, err := os.Stat(fullSegmentPath); !os.IsNotExist(err) {
		t.Fatalf("log segment stat error = %v, want not exist", err)
	}
	segments, err := db.ListLogSegments("channel-1", now.Add(-time.Second), now.Add(time.Second))
	if err != nil {
		t.Fatalf("ListLogSegments returned error: %v", err)
	}
	if len(segments) != 0 {
		t.Fatalf("segments = %+v, want empty", segments)
	}
}

func TestChannelAPIDeleteMissingChannelReturnsNotFound(t *testing.T) {
	db := newAPITestDB(t)
	srv := server.New(server.ServerConfig{DB: db, LogDir: t.TempDir()})
	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)

	req, err := http.NewRequest(http.MethodDelete, httpSrv.URL+"/api/channels/missing", nil)
	if err != nil {
		t.Fatalf("NewRequest returned error: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do returned error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("DELETE missing status = %s, body = %s", resp.Status, body)
	}
}

func TestChannelAPIDeleteBusyChannelReturnsConflict(t *testing.T) {
	db := newAPITestDB(t)
	if err := db.UpsertChannel(apiTestChannel("channel-1", 7001)); err != nil {
		t.Fatalf("UpsertChannel returned error: %v", err)
	}
	srv := server.New(server.ServerConfig{DB: db, LogDir: t.TempDir()})
	owners := srv.ControlOwnerForTest()
	if err := owners.Acquire("channel-1", "web"); err != nil {
		t.Fatalf("Acquire returned error: %v", err)
	}
	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)

	req, err := http.NewRequest(http.MethodDelete, httpSrv.URL+"/api/channels/channel-1", nil)
	if err != nil {
		t.Fatalf("NewRequest returned error: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do returned error: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("DELETE busy status = %s, body = %s", resp.Status, body)
	}
	if _, err := db.GetChannel("channel-1"); err != nil {
		t.Fatalf("GetChannel after busy delete returned error: %v", err)
	}
}
```

Also add imports used by the new tests: `os` if absent.

- [ ] **Step 6: Run server delete tests and verify failure**

Run:

```bash
rtk go test ./internal/server -run 'TestChannelAPIDelete' -count=1
```

Expected: fail with missing route/methods, including `ControlOwnerForTest` undefined.

- [ ] **Step 7: Add ControlOwner busy query and test accessor**

Modify `internal/server/web_terminal.go`:

```go
func (o *ControlOwner) Busy(channelID string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.owners[channelID] != ""
}
```

Add to `internal/server/server.go`:

```go
func (srv *Server) ControlOwnerForTest() *ControlOwner {
	return srv.controlOwner
}
```

`ControlOwnerForTest` is exported only so black-box package tests can acquire ownership without opening WebSocket/RFC2217 sessions.

- [ ] **Step 8: Implement log file delete helper**

Create `internal/server/log_delete.go`:

```go
package server

import (
	"errors"
	"os"

	"serial-platform/internal/storage"
)

func (srv *Server) deleteChannelLogFiles(segments []storage.LogSegment) error {
	for _, segment := range segments {
		path, err := srv.logSegmentPath(segment.Path)
		if err != nil {
			return err
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}
```

- [ ] **Step 9: Implement DELETE route**

Modify `internal/server/server.go`:

```go
srv.mux.HandleFunc("DELETE /api/channels/{channelID}", srv.handleDeleteChannel)
```

Add to `internal/server/api.go` after `handleDisableChannel`:

```go
func (srv *Server) handleDeleteChannel(w http.ResponseWriter, r *http.Request) {
	channelID := r.PathValue("channelID")
	if channelID == "" {
		writeBadRequest(w, "channel id is required")
		return
	}
	if _, err := srv.db.GetChannel(channelID); errors.Is(err, storage.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "channel not found"})
		return
	} else if err != nil {
		writeError(w, err)
		return
	}
	if srv.controlOwner.Busy(channelID) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "channel is busy"})
		return
	}
	segments, err := srv.db.ListLogSegments(channelID, time.Time{}, time.Now().UTC())
	if err != nil {
		writeError(w, err)
		return
	}
	if err := srv.deleteChannelLogFiles(segments); err != nil {
		writeError(w, err)
		return
	}
	if err := srv.db.DeleteChannelWithLogSegments(channelID); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "channel not found"})
			return
		}
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

Use the existing `time` and `errors` imports already present in `api.go`.

- [ ] **Step 10: Add missing-log success and delete-failure preservation tests**

Append to `internal/server/channel_api_test.go`:

```go
func TestChannelAPIDeleteIgnoresMissingLogFiles(t *testing.T) {
	db := newAPITestDB(t)
	if err := db.UpsertChannel(apiTestChannel("channel-1", 7001)); err != nil {
		t.Fatalf("UpsertChannel returned error: %v", err)
	}
	now := time.Unix(1700000000, 0).UTC()
	if err := db.InsertLogSegment(storage.LogSegment{
		ChannelID: "channel-1",
		Path: filepath.Join("channel-1", "missing.rlog"),
		StartTime: now,
		EndTime: now,
		SizeBytes: 12,
		FrameCount: 1,
		Status: storage.LogSegmentStatusClosed,
	}); err != nil {
		t.Fatalf("InsertLogSegment returned error: %v", err)
	}
	srv := server.New(server.ServerConfig{DB: db, LogDir: t.TempDir()})
	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)

	req, err := http.NewRequest(http.MethodDelete, httpSrv.URL+"/api/channels/channel-1", nil)
	if err != nil {
		t.Fatalf("NewRequest returned error: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do returned error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("DELETE status = %s, body = %s", resp.Status, body)
	}
}

func TestChannelAPIDeleteRejectsInvalidLogSegmentPathAndKeepsMetadata(t *testing.T) {
	db := newAPITestDB(t)
	if err := db.UpsertChannel(apiTestChannel("channel-1", 7001)); err != nil {
		t.Fatalf("UpsertChannel returned error: %v", err)
	}
	now := time.Unix(1700000000, 0).UTC()
	if err := db.InsertLogSegment(storage.LogSegment{
		ChannelID: "channel-1",
		Path: "../outside.rlog",
		StartTime: now,
		EndTime: now,
		SizeBytes: 12,
		FrameCount: 1,
		Status: storage.LogSegmentStatusClosed,
	}); err != nil {
		t.Fatalf("InsertLogSegment returned error: %v", err)
	}
	srv := server.New(server.ServerConfig{DB: db, LogDir: t.TempDir()})
	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)

	req, err := http.NewRequest(http.MethodDelete, httpSrv.URL+"/api/channels/channel-1", nil)
	if err != nil {
		t.Fatalf("NewRequest returned error: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do returned error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("DELETE invalid path status = %s, body = %s", resp.Status, body)
	}
	if _, err := db.GetChannel("channel-1"); err != nil {
		t.Fatalf("channel metadata was removed after delete failure: %v", err)
	}
	segments, err := db.ListLogSegments("channel-1", now.Add(-time.Second), now.Add(time.Second))
	if err != nil {
		t.Fatalf("ListLogSegments returned error: %v", err)
	}
	if len(segments) != 1 {
		t.Fatalf("segments after failed delete = %+v, want original segment", segments)
	}
}
```

- [ ] **Step 11: Run backend tests for this task**

Run:

```bash
rtk go test ./internal/storage ./internal/server -run 'TestDBDelete|TestChannelAPIDelete|TestControlOwner' -count=1
```

Expected: PASS.

- [ ] **Step 12: Format and commit**

Run:

```bash
rtk gofmt -w internal/storage/db.go internal/storage/db_test.go internal/server/api.go internal/server/server.go internal/server/web_terminal.go internal/server/channel_api_test.go internal/server/log_delete.go
rtk go test ./internal/storage ./internal/server -run 'TestDBDelete|TestChannelAPIDelete|TestControlOwner' -count=1
rtk git diff --check
rtk git status --short
rtk git add internal/storage/db.go internal/storage/db_test.go internal/server/api.go internal/server/server.go internal/server/web_terminal.go internal/server/channel_api_test.go internal/server/log_delete.go
rtk git commit -m "feat(server): delete channels with logs"
```

Expected: tests pass; commit created.

## Task 2: Text Log Export Newline Semantics

**Files:**
- Modify: `internal/logstore/export.go`
- Modify: `internal/logstore/logstore_test.go`
- Modify: `internal/server/log_download_test.go`

- [ ] **Step 1: Add failing unit test for split frames and payload newlines**

Append to `internal/logstore/logstore_test.go`:

```go
func TestExportTextPreservesPayloadNewlinesAcrossFrames(t *testing.T) {
	dir := t.TempDir()
	start := time.Unix(1700000000, 0).UTC()
	segmentPath := writeTestSegment(t, dir,
		protocol.LogFrame{ChannelID: "channel-1", Seq: 1, TimestampNS: start.UnixNano(), Direction: protocol.DirectionRX, Flags: protocol.FlagRaw, Payload: []byte("hello ")},
		protocol.LogFrame{ChannelID: "channel-1", Seq: 2, TimestampNS: start.Add(time.Second).UnixNano(), Direction: protocol.DirectionRX, Flags: protocol.FlagRaw, Payload: []byte("world\nnext")},
		protocol.LogFrame{ChannelID: "channel-1", Seq: 3, TimestampNS: start.Add(2 * time.Second).UnixNano(), Direction: protocol.DirectionRX, Flags: protocol.FlagRaw, Payload: []byte(" line\r\nlast")},
	)

	var out bytes.Buffer
	if err := ExportText([]string{segmentPath}, ExportOptions{IncludeRX: true, IncludeTX: true}, &out); err != nil {
		t.Fatalf("ExportText returned error: %v", err)
	}
	want := "hello world\nnext line\r\nlast"
	if out.String() != want {
		t.Fatalf("export text = %q, want %q", out.String(), want)
	}
}

func TestExportTextPrefixesOnlyAtFrameStart(t *testing.T) {
	dir := t.TempDir()
	start := time.Unix(1700000000, 0).UTC()
	segmentPath := writeTestSegment(t, dir,
		protocol.LogFrame{ChannelID: "channel-1", Seq: 1, TimestampNS: start.UnixNano(), Direction: protocol.DirectionRX, Flags: protocol.FlagRaw, Payload: []byte("hello")},
		protocol.LogFrame{ChannelID: "channel-1", Seq: 2, TimestampNS: start.Add(time.Second).UnixNano(), Direction: protocol.DirectionTX, Flags: protocol.FlagRaw, Payload: []byte("tx\nline2")},
	)

	var out bytes.Buffer
	if err := ExportText([]string{segmentPath}, ExportOptions{
		IncludeRX: true,
		IncludeTX: true,
		IncludeTimestamp: true,
		IncludeDirection: true,
	}, &out); err != nil {
		t.Fatalf("ExportText returned error: %v", err)
	}
	want := start.Format(time.RFC3339Nano) + " RX hello" +
		start.Add(time.Second).Format(time.RFC3339Nano) + " TX tx\nline2"
	if out.String() != want {
		t.Fatalf("export text = %q, want %q", out.String(), want)
	}
}
```

- [ ] **Step 2: Run unit tests and verify failure**

Run:

```bash
rtk go test ./internal/logstore -run 'TestExportTextPreservesPayloadNewlinesAcrossFrames|TestExportTextPrefixesOnlyAtFrameStart' -count=1
```

Expected: first test currently passes only if no frame separator exists; second exposes the exact prefix behavior. If the tests already pass, continue to Step 3 to add server-level coverage and keep the implementation minimal.

- [ ] **Step 3: Add server download coverage for text export newline behavior**

Append to `internal/server/log_download_test.go`:

```go
func TestLogDownloadTextPreservesPayloadNewlinesAcrossFrames(t *testing.T) {
	root := t.TempDir()
	db, logDir := openLogDownloadDB(t, root)
	start := time.Date(2026, 5, 19, 0, 0, 0, 0, time.UTC)
	insertLogSegment(t, db, logDir, "channel-1",
		protocol.LogFrame{ChannelID: "channel-1", Seq: 1, TimestampNS: start.UnixNano(), Direction: protocol.DirectionRX, Flags: protocol.FlagRaw, Payload: []byte("hello ")},
		protocol.LogFrame{ChannelID: "channel-1", Seq: 2, TimestampNS: start.Add(time.Second).UnixNano(), Direction: protocol.DirectionRX, Flags: protocol.FlagRaw, Payload: []byte("world\nnext")},
	)

	srv := server.New(server.ServerConfig{DB: db, LogDir: logDir})
	req := httptest.NewRequest(http.MethodGet, "/api/logs/download?channel_id=channel-1&direction=rx", nil)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	want := "hello world\nnext"
	if rec.Body.String() != want {
		t.Fatalf("body = %q, want %q", rec.Body.String(), want)
	}
}
```

- [ ] **Step 4: Make text export behavior explicit**

Update `internal/logstore/export.go` by replacing `writeTextFrame` with explicit prefix + payload write:

```go
func writeTextFrame(out *bufio.Writer, frame protocol.LogFrame, opts ExportOptions) error {
	if err := writeTextFramePrefix(out, frame, opts); err != nil {
		return err
	}
	text := escapedUTF8(frame.Payload)
	if opts.StripANSI {
		text = ansiRE.ReplaceAllString(text, "")
	}
	_, err := fmt.Fprint(out, text)
	return err
}

func writeTextFramePrefix(out *bufio.Writer, frame protocol.LogFrame, opts ExportOptions) error {
	if opts.IncludeTimestamp {
		if _, err := fmt.Fprintf(out, "%s ", time.Unix(0, frame.TimestampNS).UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	if opts.IncludeDirection {
		label := "TX"
		if frame.Direction == protocol.DirectionRX {
			label = "RX"
		}
		if _, err := fmt.Fprintf(out, "%s ", label); err != nil {
			return err
		}
	}
	return nil
}
```

This keeps the intended behavior visible in code: prefix is frame-level metadata, payload bytes decide text line breaks.

- [ ] **Step 5: Run logstore and server log tests**

Run:

```bash
rtk gofmt -w internal/logstore/export.go internal/logstore/logstore_test.go internal/server/log_download_test.go
rtk go test ./internal/logstore ./internal/server -run 'TestExportText|TestLogDownloadText' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

Run:

```bash
rtk git diff --check
rtk git status --short
rtk git add internal/logstore/export.go internal/logstore/logstore_test.go internal/server/log_download_test.go
rtk git commit -m "fix(logs): preserve text newline semantics"
```

Expected: commit created.

## Task 3: Frontend I18n Foundation, API Helper, and Shell Refresh Feedback

**Files:**
- Create: `web/src/i18n.ts`
- Create: `web/src/i18n-context.tsx`
- Create: `web/src/components/Metric.tsx`
- Create: `web/src/components/ViewTitle.tsx`
- Create: `web/src/components/Badge.tsx`
- Create: `web/src/components/EmptyRow.tsx`
- Create: `web/src/components/FormFeedback.tsx`
- Create: `web/src/components/Quota.tsx`
- Modify: `web/src/api.ts`
- Modify: `web/src/types.ts`
- Modify: `web/src/App.tsx`
- Modify: `web/src/styles.css`

- [ ] **Step 1: Add frontend types**

Modify `web/src/types.ts`:

```ts
export type ViewKey = 'agents' | 'devices' | 'channels' | 'terminal' | 'logs';

export type Language = 'en' | 'zh-CN';

export type RequestState = {
  busy: boolean;
  error: string | null;
  message: string | null;
};

export type RefreshState = 'idle' | 'loading' | 'success' | 'error';
```

- [ ] **Step 2: Add delete API helper**

Modify `web/src/api.ts`:

```ts
export async function deleteNoContent(path: string): Promise<void> {
  const res = await fetch(path, { method: 'DELETE' });
  if (!res.ok) {
    throw new Error(await res.text());
  }
}
```

- [ ] **Step 3: Create static i18n dictionary**

Create `web/src/i18n.ts`:

```ts
import type { Language } from './types';

export const LANGUAGE_STORAGE_KEY = 'serial-platform.language';

export const languages: { value: Language; label: string }[] = [
  { value: 'en', label: 'English' },
  { value: 'zh-CN', label: '中文' }
];

export const messages = {
  en: {
    appName: 'Serial Platform',
    centralServer: 'central-server',
    navAgents: 'Agents',
    navDevices: 'Devices',
    navChannels: 'Channels',
    navTerminal: 'Terminal',
    navLogs: 'Logs',
    metricAgents: 'Agents',
    metricPending: 'Pending',
    metricOnlineChannels: 'Online channels',
    metricBusy: 'Busy',
    filterChannels: 'Filter channels',
    refresh: 'Refresh',
    refreshing: 'Refreshing',
    updatedJustNow: 'Updated just now',
    apiUnavailable: 'API unavailable',
    loadingAPI: 'Loading API',
    apiConnected: 'API connected',
    apiError: 'API error',
    language: 'Language'
  },
  'zh-CN': {
    appName: '串口平台',
    centralServer: 'central-server',
    navAgents: 'Agents',
    navDevices: 'Devices',
    navChannels: 'Channels',
    navTerminal: 'Terminal',
    navLogs: 'Logs',
    metricAgents: 'Agent',
    metricPending: '待确认',
    metricOnlineChannels: '在线 channel',
    metricBusy: '占用',
    filterChannels: '筛选 channel',
    refresh: '刷新',
    refreshing: '刷新中',
    updatedJustNow: '刚刚更新',
    apiUnavailable: 'API 不可用',
    loadingAPI: '正在加载 API',
    apiConnected: 'API 已连接',
    apiError: 'API 错误',
    language: '语言'
  }
} satisfies Record<Language, Record<string, string>>;

export type MessageKey = keyof typeof messages.en;

export function detectDefaultLanguage(): Language {
  const stored = window.localStorage.getItem(LANGUAGE_STORAGE_KEY);
  if (stored === 'en' || stored === 'zh-CN') {
    return stored;
  }
  return navigator.language.toLowerCase().startsWith('zh') ? 'zh-CN' : 'en';
}
```

Keep navigation labels `Agents / Devices / Channels / Terminal / Logs` stable in both languages because they are product-level nouns already used in the spec.

- [ ] **Step 4: Create i18n provider**

Create `web/src/i18n-context.tsx`:

```tsx
import { createContext, use, useCallback, useMemo, useState, type ReactNode } from 'react';
import { LANGUAGE_STORAGE_KEY, detectDefaultLanguage, messages, type MessageKey } from './i18n';
import type { Language } from './types';

type I18nContextValue = {
  language: Language;
  setLanguage: (language: Language) => void;
  t: (key: MessageKey) => string;
};

const I18nContext = createContext<I18nContextValue | null>(null);

export function I18nProvider({ children }: { children: ReactNode }) {
  const [language, setLanguageState] = useState<Language>(() => detectDefaultLanguage());

  const setLanguage = useCallback((nextLanguage: Language) => {
    setLanguageState(nextLanguage);
    window.localStorage.setItem(LANGUAGE_STORAGE_KEY, nextLanguage);
  }, []);

  const value = useMemo<I18nContextValue>(
    () => ({
      language,
      setLanguage,
      t: (key) => messages[language][key] ?? messages.en[key]
    }),
    [language, setLanguage]
  );

  return <I18nContext value={value}>{children}</I18nContext>;
}

export function useI18n() {
  const value = use(I18nContext);
  if (!value) {
    throw new Error('useI18n must be used within I18nProvider');
  }
  return value;
}
```

- [ ] **Step 5: Extract shared components**

Create `web/src/components/Metric.tsx`:

```tsx
export function Metric({ label, value, tone }: { label: string; value: number; tone: 'neutral' | 'good' | 'warn' }) {
  return (
    <div className={`metric ${tone}`}>
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
  );
}
```

Create `web/src/components/ViewTitle.tsx`:

```tsx
import type { LucideIcon } from 'lucide-react';
import { Settings2 } from 'lucide-react';

export function ViewTitle({ icon: Icon, title, action }: { icon: LucideIcon; title: string; action: string }) {
  return (
    <div className="view-title">
      <div>
        <Icon size={20} aria-hidden="true" />
        <h1>{title}</h1>
      </div>
      <span className="view-action">
        <Settings2 size={15} aria-hidden="true" />
        {action}
      </span>
    </div>
  );
}
```

Create `web/src/components/Badge.tsx`:

```tsx
export function Badge({ value }: { value: string }) {
  const normalized = value ? value.toLowerCase() : 'unknown';
  return <span className={`badge ${normalized}`}>{value || 'unknown'}</span>;
}
```

Create `web/src/components/EmptyRow.tsx`:

```tsx
import { ListFilter } from 'lucide-react';

export function EmptyRow({ colSpan, label }: { colSpan: number; label: string }) {
  return (
    <tr>
      <td colSpan={colSpan} className="empty-row">
        <ListFilter size={15} aria-hidden="true" />
        {label}
      </td>
    </tr>
  );
}
```

Create `web/src/components/FormFeedback.tsx`:

```tsx
import type { RequestState } from '../types';

export function FormFeedback({ state }: { state: RequestState }) {
  if (state.error) {
    return <div className="inline-error">{state.error}</div>;
  }
  if (state.message) {
    return <div className="inline-success">{state.message}</div>;
  }
  return null;
}
```

Create `web/src/components/Quota.tsx`:

```tsx
import { Activity } from 'lucide-react';

export function Quota({ label, value, limit }: { label: string; value: string; limit: string }) {
  return (
    <div className="quota">
      <Activity size={16} aria-hidden="true" />
      <span>{label}</span>
      <strong>{value}</strong>
      <small>{limit}</small>
    </div>
  );
}
```

- [ ] **Step 6: Replace App shell only**

Modify `web/src/App.tsx` so the shell uses:

```tsx
const navItems: NavItem[] = [
  { key: 'agents', labelKey: 'navAgents', icon: Monitor },
  { key: 'devices', labelKey: 'navDevices', icon: PlugZap },
  { key: 'channels', labelKey: 'navChannels', icon: Cable },
  { key: 'terminal', labelKey: 'navTerminal', icon: TerminalSquare },
  { key: 'logs', labelKey: 'navLogs', icon: HardDrive }
];
```

Add refresh feedback state:

```tsx
const [refreshState, setRefreshState] = useState<RefreshState>('idle');
const [lastUpdatedAt, setLastUpdatedAt] = useState<Date | null>(null);
```

Update `refresh`:

```tsx
const refresh = useCallback(async () => {
  setLoading(true);
  setRefreshState('loading');
  setError(null);
  try {
    const [nextAgents, nextChannels] = await Promise.all([
      getJSON<Agent[]>('/api/agents'),
      getJSON<Channel[]>('/api/channels')
    ]);
    setAgents(nextAgents);
    setChannels(nextChannels);
    setLastUpdatedAt(new Date());
    setRefreshState('success');
  } catch (err) {
    setError(errorMessage(err));
    setRefreshState('error');
  } finally {
    setLoading(false);
  }
}, []);
```

Add a cleanup effect:

```tsx
useEffect(() => {
  if (refreshState !== 'success') {
    return undefined;
  }
  const timer = window.setTimeout(() => setRefreshState('idle'), 1800);
  return () => window.clearTimeout(timer);
}, [refreshState]);
```

Add language selector in the topbar:

```tsx
<label className="language-select">
  <span>{t('language')}</span>
  <select value={language} onChange={(event) => setLanguage(event.target.value as Language)}>
    {languages.map((item) => (
      <option key={item.value} value={item.value}>
        {item.label}
      </option>
    ))}
  </select>
</label>
```

Refresh button should be disabled while loading and show a spinner class:

```tsx
<button
  type="button"
  className={refreshState === 'loading' ? 'icon-button spinning' : 'icon-button'}
  onClick={() => void refresh()}
  title={t('refresh')}
  disabled={refreshState === 'loading'}
>
  <RefreshCw size={16} aria-hidden="true" />
</button>
```

Keep existing page components inside `App.tsx` for this task; only shell labels, extracted shared components, i18n provider, and refresh feedback change here.

- [ ] **Step 7: Wrap app with I18nProvider**

Modify `web/src/main.tsx`:

```tsx
import { I18nProvider } from './i18n-context';

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <I18nProvider>
      <App />
    </I18nProvider>
  </StrictMode>
);
```

- [ ] **Step 8: Add CSS for refresh feedback and language selector**

Append to `web/src/styles.css`:

```css
.language-select {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  color: #5f6a61;
}

.language-select select {
  min-height: 34px;
  border: 1px solid #cfd5cf;
  border-radius: 6px;
  background: #fff;
  color: #1f2a22;
}

.refresh-status {
  min-width: 112px;
  font-size: 13px;
  color: #5f6a61;
}

.icon-button.spinning svg {
  animation: spin 0.8s linear infinite;
}

@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}
```

- [ ] **Step 9: Run frontend type check**

Run:

```bash
rtk cd web && npm run lint
```

Expected: PASS.

- [ ] **Step 10: Commit**

Run:

```bash
rtk git diff --check
rtk git status --short
rtk git add web/src/App.tsx web/src/main.tsx web/src/api.ts web/src/types.ts web/src/i18n.ts web/src/i18n-context.tsx web/src/components web/src/styles.css
rtk git commit -m "feat(web): add shell i18n refresh feedback"
```

Expected: commit created.

## Task 4: Frontend Page Split and Navigation Rename

**Files:**
- Create: `web/src/pages/AgentsPage.tsx`
- Create: `web/src/pages/DevicesPage.tsx`
- Create: `web/src/pages/ChannelsPage.tsx`
- Create: `web/src/pages/LogsPage.tsx`
- Modify: `web/src/App.tsx`
- Modify: `web/src/types.ts`

- [ ] **Step 1: Move app utility functions into page-owned files**

Each extracted page should keep only the helpers it uses:

- `AgentsPage.tsx`: `formatTime`.
- `DevicesPage.tsx`: `defaultConfirmForm`, `candidateAlias`, `formatTime`.
- `ChannelsPage.tsx`: `defaultManualForm`, `nextRFC2217Port`.
- `LogsPage.tsx`: no channel mutation helpers.

Do not create a shared utility module unless the same helper is required by at least two extracted pages after the move.

- [ ] **Step 2: Create AgentsPage**

Move current `HostsView` into `web/src/pages/AgentsPage.tsx` and rename it:

The exported function signature must be:

```tsx
export function AgentsPage({
  agents,
  channels,
  loading,
  busyAgentID,
  onApproveAgent
}: {
  agents: Agent[];
  channels: Channel[];
  loading: boolean;
  busyAgentID: string | null;
  onApproveAgent: (agentID: string) => void;
}) {
```

Copy the full JSX body from the current `HostsView` into this component, then change only these visible labels:

- `ViewTitle` title: `Hosts` -> `Agents`
- top-level inventory wording that says `Hosts` -> `Agents`

Do not change table columns, approve behavior, agent/channel counting logic, API field values, or raw status values.

- [ ] **Step 3: Create DevicesPage**

Move current `CalibrationView` and `CandidateDetail` into `web/src/pages/DevicesPage.tsx`, rename exported component:

The exported function signature must be:

```tsx
export function DevicesPage({
  agents,
  channels,
  onRefresh
}: {
  agents: Agent[];
  channels: Channel[];
  onRefresh: () => Promise<void>;
}) {
```

Replace visible text:

- `Calibration` -> `Devices`
- `Confirm mapping` -> `Create channel`
- `Discovered candidates` can remain because it describes the table.

Do not change candidate refresh, candidate selection, confirm form, or `POST /api/candidates/{candidateID}/confirm` behavior.

- [ ] **Step 4: Create ChannelsPage**

Move current `ChannelsView` into `web/src/pages/ChannelsPage.tsx`:

The exported function signature must be:

```tsx
export function ChannelsPage({
  agents,
  channels,
  allChannels,
  loading,
  query,
  onRefresh
}: {
  agents: Agent[];
  channels: Channel[];
  allChannels: Channel[];
  loading: boolean;
  query: string;
  onRefresh: () => Promise<void>;
}) {
```

Do not add delete UI in this task; Task 6 owns delete UI.
Do not change enable/disable behavior, manual channel creation behavior, filtering, or form defaults.

- [ ] **Step 5: Create LogsPage**

Move current `LogsView` into `web/src/pages/LogsPage.tsx`:

The exported function signature must be:

```tsx
export function LogsPage({ channels }: { channels: Channel[] }) {
```

Preserve the current download defaults exactly: `format = "text"`, `timestamp = true`, `directionLabel = true`, `direction = "both"`, `stripANSI = false`.

- [ ] **Step 6: Simplify App view switch**

Modify `web/src/App.tsx`:

```tsx
{activeView === 'agents' ? (
  <AgentsPage
    agents={agents}
    channels={channels}
    loading={loading}
    busyAgentID={busyAgentID}
    onApproveAgent={(agentID) => void approveAgent(agentID)}
  />
) : null}
{activeView === 'devices' ? <DevicesPage agents={agents} channels={channels} onRefresh={refresh} /> : null}
{activeView === 'channels' ? (
  <ChannelsPage
    agents={agents}
    channels={visibleChannels}
    allChannels={channels}
    loading={loading}
    query={query}
    onRefresh={refresh}
  />
) : null}
{activeView === 'terminal' ? <TerminalPage channels={channels} /> : null}
{activeView === 'logs' ? <LogsPage channels={channels} /> : null}
```

For this task, `TerminalPage` can still be the old inline `TerminalView` temporarily renamed and left in `App.tsx`. Task 5 moves it.

- [ ] **Step 7: Remove old UI strings**

Run:

```bash
rtk rg -n 'Calibration|Live Log / Terminal|Hosts' web/src
```

Expected after this task:

- no `Calibration`
- no `Live Log / Terminal`
- `Hosts` absent from visible UI strings

If `Hosts` appears only in historical comments or unavailable generated build files, remove or regenerate those files during final build instead of keeping stale UI text.

- [ ] **Step 8: Type check and commit**

Run:

```bash
rtk cd web && npm run lint
rtk git diff --check
rtk git status --short
rtk git add web/src/App.tsx web/src/types.ts web/src/pages web/src/components web/src/styles.css
rtk git commit -m "refactor(web): split pages and rename navigation"
```

Expected: type check passes; commit created.

## Task 5: Terminal Session Provider and Live Log Buffer

**Files:**
- Create: `web/src/live-log-buffer.ts`
- Create: `web/src/terminal-session.tsx`
- Create: `web/src/pages/TerminalPage.tsx`
- Modify: `web/src/App.tsx`
- Modify: `web/src/types.ts`
- Modify: `web/src/styles.css`

- [ ] **Step 1: Add live log buffer types**

Append to `web/src/types.ts`:

```ts
export type TerminalStatus = 'idle' | 'connecting' | 'connected' | 'error';

export type LogDisplayLine = {
  id: string;
  ts: string;
  dir: string;
  text: string;
};
```

- [ ] **Step 2: Create live log buffer pure function**

Create `web/src/live-log-buffer.ts`:

```ts
import type { LiveLogFrame, LogDisplayLine } from './types';

const textDecoder = new TextDecoder();

export type LiveLogBuffer = {
  lines: LogDisplayLine[];
  current: LogDisplayLine | null;
  lastDirection: string | null;
};

export function emptyLiveLogBuffer(): LiveLogBuffer {
  return { lines: [], current: null, lastDirection: null };
}

export function appendLiveLogFrame(buffer: LiveLogBuffer, frame: LiveLogFrame, limit = 500): LiveLogBuffer {
  const dir = formatDirection(frame.direction);
  const text = decodePayload(frame.payload);
  let lines = buffer.lines;
  let current = buffer.current;

  if (current && buffer.lastDirection !== dir) {
    lines = pushLine(lines, current, limit);
    current = null;
  }

  const parts = splitKeepingNewlineBoundaries(text);
  for (const part of parts) {
    if (!current) {
      current = newLine(frame, dir);
    }
    current = { ...current, text: current.text + part.text };
    if (part.endsLine) {
      lines = pushLine(lines, current, limit);
      current = null;
    }
  }

  if (parts.length === 0 && !current) {
    current = newLine(frame, dir);
  }

  return { lines, current, lastDirection: dir };
}

export function liveLogLines(buffer: LiveLogBuffer): LogDisplayLine[] {
  return buffer.current ? [...buffer.lines, buffer.current] : buffer.lines;
}

function splitKeepingNewlineBoundaries(text: string): { text: string; endsLine: boolean }[] {
  const parts: { text: string; endsLine: boolean }[] = [];
  let start = 0;
  for (let i = 0; i < text.length; i += 1) {
    if (text[i] !== '\n') {
      continue;
    }
    const end = i > 0 && text[i - 1] === '\r' ? i - 1 : i;
    parts.push({ text: text.slice(start, end), endsLine: true });
    start = i + 1;
  }
  if (start < text.length) {
    parts.push({ text: text.slice(start), endsLine: false });
  }
  return parts;
}

function newLine(frame: LiveLogFrame, dir: string): LogDisplayLine {
  return {
    id: `${frame.seq}-${frame.timestamp_ns}-${dir}`,
    ts: frame.timestamp_ns ? new Date(Math.floor(Number(frame.timestamp_ns) / 1000000)).toLocaleTimeString() : String(frame.seq),
    dir,
    text: ''
  };
}

function pushLine(lines: LogDisplayLine[], line: LogDisplayLine, limit: number) {
  return [...lines, line].slice(-limit);
}

export function decodePayload(payload: string) {
  try {
    const bytes = Uint8Array.from(atob(payload), (char) => char.charCodeAt(0));
    return textDecoder.decode(bytes);
  } catch {
    return payload;
  }
}

export function formatDirection(direction: LiveLogFrame['direction']) {
  if (direction === 1 || direction === '1') {
    return 'RX';
  }
  if (direction === 2 || direction === '2') {
    return 'TX';
  }
  return String(direction).toUpperCase();
}
```

- [ ] **Step 3: Create TerminalSessionProvider**

Create `web/src/terminal-session.tsx`:

```tsx
import { createContext, use, useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { wsURL } from './api';
import type { Channel, OperationResult, TerminalMessage, TerminalStatus } from './types';

const textEncoder = new TextEncoder();

type TerminalSessionContextValue = {
  selectedChannelID: string;
  status: TerminalStatus;
  error: string | null;
  pendingCount: number;
  baud: string;
  dtr: boolean;
  rts: boolean;
  connected: boolean;
  selectChannel: (channelID: string) => void;
  connect: () => void;
  disconnect: () => void;
  setBaud: (baud: string) => void;
  applySerialConfig: () => void;
  setDTRValue: (value: boolean) => void;
  setRTSValue: (value: boolean) => void;
  writeText: (text: string) => boolean;
  sendBreak: (durationMS: number) => void;
};

const TerminalSessionContext = createContext<TerminalSessionContextValue | null>(null);

export function TerminalSessionProvider({ channels, children }: { channels: Channel[]; children: ReactNode }) {
  const [selectedChannelID, setSelectedChannelID] = useState(channels[0]?.ID ?? '');
  const [status, setStatus] = useState<TerminalStatus>('idle');
  const [error, setError] = useState<string | null>(null);
  const [pendingCount, setPendingCount] = useState(0);
  const [baud, setBaud] = useState('115200');
  const [dtr, setDTR] = useState(true);
  const [rts, setRTS] = useState(true);
  const socketRef = useRef<WebSocket | null>(null);
  const selectedRef = useRef(selectedChannelID);
  const connected = status === 'connected';

  const channelByID = useMemo(() => new Map(channels.map((channel) => [channel.ID, channel])), [channels]);
  const selectedChannel = channelByID.get(selectedChannelID);

  useEffect(() => {
    selectedRef.current = selectedChannelID;
  }, [selectedChannelID]);

  useEffect(() => {
    if (!selectedChannelID && channels[0]) {
      setSelectedChannelID(channels[0].ID);
      return;
    }
    if (selectedChannelID && !channelByID.has(selectedChannelID)) {
      socketRef.current?.close();
      socketRef.current = null;
      setSelectedChannelID(channels[0]?.ID ?? '');
      setStatus('idle');
      setPendingCount(0);
    }
  }, [channelByID, channels, selectedChannelID]);

  useEffect(() => {
    if (selectedChannel && !connected) {
      setBaud(String(selectedChannel.DefaultBaud || 115200));
    }
  }, [connected, selectedChannel]);

  useEffect(() => () => socketRef.current?.close(), []);

  const disconnect = useCallback(() => {
    socketRef.current?.close();
    socketRef.current = null;
    setStatus('idle');
    setError(null);
    setPendingCount(0);
  }, []);

  const selectChannel = useCallback(
    (channelID: string) => {
      if (channelID === selectedRef.current) {
        return;
      }
      disconnect();
      setSelectedChannelID(channelID);
    },
    [disconnect]
  );

  const connect = useCallback(() => {
    const channelID = selectedRef.current;
    if (!channelID || socketRef.current || status === 'connecting') {
      return;
    }
    setStatus('connecting');
    setError(null);
    const socket = new WebSocket(wsURL(`/ws/terminal/${encodeURIComponent(channelID)}`));
    socketRef.current = socket;
    socket.onopen = () => {
      if (socketRef.current === socket) {
        setStatus('connected');
      }
    };
    socket.onmessage = (event) => {
      if (socketRef.current !== socket) {
        return;
      }
      try {
        const result = JSON.parse(String(event.data)) as OperationResult;
        setPendingCount((count) => Math.max(0, count - 1));
        if (!result.ok) {
          setError(result.error || 'operation failed');
        }
      } catch (err) {
        setError(errorMessage(err));
      }
    };
    socket.onerror = () => {
      if (socketRef.current === socket) {
        setStatus('error');
        setError('terminal websocket error');
      }
    };
    socket.onclose = (event) => {
      if (socketRef.current !== socket) {
        return;
      }
      socketRef.current = null;
      setPendingCount(0);
      setStatus(event.code === 1000 ? 'idle' : 'error');
      setError(event.code === 1000 ? null : event.reason || 'terminal closed');
    };
  }, [status]);

  const sendTerminalMessage = useCallback((message: TerminalMessage) => {
    if (!socketRef.current || socketRef.current.readyState !== WebSocket.OPEN) {
      setError('terminal is not connected');
      return false;
    }
    socketRef.current.send(JSON.stringify(message));
    setPendingCount((count) => count + 1);
    return true;
  }, []);

  const writeText = useCallback(
    (text: string) =>
      sendTerminalMessage({
        type: 'terminal_write',
        request_id: requestID(),
        data: base64Encode(text)
      }),
    [sendTerminalMessage]
  );

  const applySerialConfig = useCallback(() => {
    sendTerminalMessage({
      type: 'serial_set_config',
      request_id: requestID(),
      baud: Number(baud),
      data_bits: 8,
      parity: 'N',
      stop_bits: 1,
      flow: 'none'
    });
  }, [baud, sendTerminalMessage]);

  const setDTRValue = useCallback(
    (value: boolean) => {
      setDTR(value);
      sendTerminalMessage({ type: 'serial_set_dtr', request_id: requestID(), value });
    },
    [sendTerminalMessage]
  );

  const setRTSValue = useCallback(
    (value: boolean) => {
      setRTS(value);
      sendTerminalMessage({ type: 'serial_set_rts', request_id: requestID(), value });
    },
    [sendTerminalMessage]
  );

  const sendBreak = useCallback(
    (durationMS: number) => {
      sendTerminalMessage({ type: 'serial_send_break', request_id: requestID(), duration_ms: durationMS });
    },
    [sendTerminalMessage]
  );

  const value = useMemo<TerminalSessionContextValue>(
    () => ({
      selectedChannelID,
      status,
      error,
      pendingCount,
      baud,
      dtr,
      rts,
      connected,
      selectChannel,
      connect,
      disconnect,
      setBaud,
      applySerialConfig,
      setDTRValue,
      setRTSValue,
      writeText,
      sendBreak
    }),
    [selectedChannelID, status, error, pendingCount, baud, dtr, rts, connected, selectChannel, connect, disconnect, applySerialConfig, setDTRValue, setRTSValue, writeText, sendBreak]
  );

  return <TerminalSessionContext value={value}>{children}</TerminalSessionContext>;
}

export function useTerminalSession() {
  const value = use(TerminalSessionContext);
  if (!value) {
    throw new Error('useTerminalSession must be used within TerminalSessionProvider');
  }
  return value;
}

function requestID() {
  if (window.crypto?.randomUUID) {
    return window.crypto.randomUUID();
  }
  return `web-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

function base64Encode(value: string) {
  const bytes = textEncoder.encode(value);
  let binary = '';
  bytes.forEach((byte) => {
    binary += String.fromCharCode(byte);
  });
  return btoa(binary);
}

function errorMessage(err: unknown) {
  return err instanceof Error ? err.message : 'Request failed';
}
```

- [ ] **Step 4: Create TerminalPage**

Move old inline Terminal UI into `web/src/pages/TerminalPage.tsx`, replacing control state with `useTerminalSession()` and replacing frame rendering with `appendLiveLogFrame`:

```tsx
import { type FormEvent, useEffect, useMemo, useRef, useState } from 'react';
import { Power, Send, TerminalSquare, Unplug } from 'lucide-react';
import { wsURL } from '../api';
import { ViewTitle } from '../components/ViewTitle';
import { appendLiveLogFrame, emptyLiveLogBuffer, liveLogLines } from '../live-log-buffer';
import { useTerminalSession } from '../terminal-session';
import type { Channel, LiveLogFrame } from '../types';

export function TerminalPage({ channels }: { channels: Channel[] }) {
  const session = useTerminalSession();
  const [logBuffer, setLogBuffer] = useState(() => emptyLiveLogBuffer());
  const [input, setInput] = useState('');
  const outputRef = useRef<HTMLDivElement | null>(null);
  const channelByID = useMemo(() => new Map(channels.map((channel) => [channel.ID, channel])), [channels]);
  const selectedChannel = channelByID.get(session.selectedChannelID);
  const logLines = liveLogLines(logBuffer);

  useEffect(() => {
    setLogBuffer(emptyLiveLogBuffer());
    if (!session.selectedChannelID) {
      return undefined;
    }
    let closedByCleanup = false;
    const socket = new WebSocket(wsURL(`/ws/live-log/${encodeURIComponent(session.selectedChannelID)}`));
    socket.onmessage = (event) => {
      if (closedByCleanup) {
        return;
      }
      try {
        const frame = JSON.parse(String(event.data)) as LiveLogFrame;
        setLogBuffer((current) => appendLiveLogFrame(current, frame));
      } catch (err) {
        setLogBuffer((current) =>
          appendLiveLogFrame(current, {
            channel_id: session.selectedChannelID,
            seq: Date.now(),
            timestamp_ns: Date.now() * 1000000,
            direction: 'err',
            flags: 0,
            payload: btoa(errorMessage(err))
          })
        );
      }
    };
    socket.onerror = () => {
      if (!closedByCleanup) {
        setLogBuffer((current) =>
          appendLiveLogFrame(current, {
            channel_id: session.selectedChannelID,
            seq: Date.now(),
            timestamp_ns: Date.now() * 1000000,
            direction: 'err',
            flags: 0,
            payload: btoa('live log websocket error')
          })
        );
      }
    };
    return () => {
      closedByCleanup = true;
      socket.close();
    };
  }, [session.selectedChannelID]);

  useEffect(() => {
    outputRef.current?.scrollTo({ top: outputRef.current.scrollHeight });
  }, [logLines]);

  function sendInput(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (input && session.writeText(input)) {
      setInput('');
    }
  }

  return (
    <section className="view terminal-view">
      <ViewTitle icon={TerminalSquare} title="Terminal" action="Connect to control" />
    </section>
  );
}

function errorMessage(err: unknown) {
  return err instanceof Error ? err.message : 'Request failed';
}
```

Replace the skeleton `<section>` return above with the old `TerminalView` JSX body and apply these exact substitutions:

- `selectedChannelID` -> `session.selectedChannelID`
- `connected` -> `session.connected`
- `terminalStatus` -> `session.status`
- `terminalError` -> `session.error`
- `pendingCount` -> `session.pendingCount`
- `baud` -> `session.baud`
- `dtr` -> `session.dtr`
- `rts` -> `session.rts`
- `setSelectedID(event.target.value)` -> `session.selectChannel(event.target.value)`
- `connectTerminal` -> `session.connect`
- `disconnectTerminal` -> `session.disconnect`
- `setBaud(event.target.value)` -> `session.setBaud(event.target.value)`
- `applySerialConfig` -> `session.applySerialConfig`
- `updateDTR(event.target.checked)` -> `session.setDTRValue(event.target.checked)`
- `updateRTS(event.target.checked)` -> `session.setRTSValue(event.target.checked)`
- the old `sendTerminalMessage` call for `serial_send_break` -> `session.sendBreak(250)`
- old live-log row rendering must render `liveLogLines(logBuffer)` output, not one row per frame.

The final JSX must preserve all visible controls: channel selector, Connect, Disconnect, DTR, RTS, baud, Apply, Break, TX input, live log panel.

- [ ] **Step 5: Mount provider above page switching**

Modify `web/src/App.tsx` so provider is not unmounted when `activeView` changes:

```tsx
<TerminalSessionProvider channels={channels}>
  {activeView === 'agents' ? (
    <AgentsPage
      agents={agents}
      channels={channels}
      loading={loading}
      busyAgentID={busyAgentID}
      onApproveAgent={(agentID) => void approveAgent(agentID)}
    />
  ) : null}
  {activeView === 'devices' ? <DevicesPage agents={agents} channels={channels} onRefresh={refresh} /> : null}
  {activeView === 'channels' ? (
    <ChannelsPage
      agents={agents}
      channels={visibleChannels}
      allChannels={channels}
      loading={loading}
      query={query}
      onRefresh={refresh}
    />
  ) : null}
  {activeView === 'terminal' ? <TerminalPage channels={channels} /> : null}
  {activeView === 'logs' ? <LogsPage channels={channels} /> : null}
</TerminalSessionProvider>
```

- [ ] **Step 6: Stabilize terminal height**

Modify `web/src/styles.css`:

```css
.terminal-layout {
  align-items: stretch;
}

.terminal-panel {
  display: grid;
  grid-template-rows: auto minmax(360px, 1fr) auto;
  min-height: min(720px, calc(100vh - 190px));
}

.terminal-output {
  min-height: 360px;
  height: 100%;
  max-height: none;
  overflow: auto;
  white-space: pre-wrap;
}

.log-line code {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
```

Ensure mobile media rules do not reduce `.terminal-output` below `280px`.

- [ ] **Step 7: Run string and type checks**

Run:

```bash
rtk rg -n 'Live Log / Terminal|Calibration' web/src
rtk cd web && npm run lint
```

Expected: `rg` returns no matches; type check passes.

- [ ] **Step 8: Commit**

Run:

```bash
rtk git diff --check
rtk git status --short
rtk git add web/src/App.tsx web/src/types.ts web/src/terminal-session.tsx web/src/live-log-buffer.ts web/src/pages/TerminalPage.tsx web/src/styles.css
rtk git commit -m "feat(web): persist terminal sessions"
```

Expected: commit created.

## Task 6: Channel Delete UI, i18n Coverage, and Final CSS Polish

**Files:**
- Modify: `web/src/pages/ChannelsPage.tsx`
- Modify: `web/src/pages/AgentsPage.tsx`
- Modify: `web/src/pages/DevicesPage.tsx`
- Modify: `web/src/pages/TerminalPage.tsx`
- Modify: `web/src/pages/LogsPage.tsx`
- Modify: `web/src/i18n.ts`
- Modify: `web/src/api.ts`
- Modify: `web/src/App.tsx`
- Modify: `web/src/styles.css`

- [ ] **Step 1: Expand i18n dictionary for page text**

Add concrete keys in `web/src/i18n.ts` for visible UI text used in extracted pages. Required keys include:

```ts
agentsTitle: 'Agents',
agentsAction: 'Approve / Rename',
devicesTitle: 'Devices',
devicesAction: 'Create channel',
channelsTitle: 'Channels',
channelsAction: 'Manual fallback',
terminalTitle: 'Terminal',
terminalAction: 'Connect to control',
logsTitle: 'Logs',
logsAction: 'Download range',
deleteChannel: 'Delete',
deleteChannelConfirm: 'Deleting this channel will also delete all logs for this channel. This cannot be undone.',
deleteChannelConfirmZh: '删除该 channel 会同步删除它的所有日志，且无法恢复。',
confirmDelete: 'Confirm delete',
cancel: 'Cancel',
deleting: 'Deleting',
channelDeleted: 'Channel deleted'
```

Use proper `en` and `zh-CN` values. Do not translate API field names like `ID_PATH` or raw status values.

- [ ] **Step 2: Wire i18n into page titles and buttons**

Update each page to use:

```tsx
const { t } = useI18n();
```

Replace page title/action and common button/empty-state strings with dictionary values. Leave user data, alias, `ID_PATH`, `ID_PATH_TAG`, status values, and log payload untouched.

- [ ] **Step 3: Add delete UI state to ChannelsPage**

In `web/src/pages/ChannelsPage.tsx`, add:

```tsx
const [deleteTarget, setDeleteTarget] = useState<Channel | null>(null);
const [deleteState, setDeleteState] = useState<RequestState>({ busy: false, error: null, message: null });
```

Add a delete button beside enable/disable:

```tsx
<button type="button" className="danger subtle" onClick={() => setDeleteTarget(channel)}>
  <Trash2 size={14} aria-hidden="true" />
  {t('deleteChannel')}
</button>
```

Add confirmation panel near the table or as an inline modal:

```tsx
{deleteTarget ? (
  <div className="danger-confirm" role="alertdialog" aria-modal="true" aria-labelledby="delete-channel-title">
    <h3 id="delete-channel-title">{deleteTarget.Alias || deleteTarget.AutoName}</h3>
    <p>{t('deleteChannelConfirm')}</p>
    <FormFeedback state={deleteState} />
    <div className="connect-row">
      <button type="button" onClick={() => setDeleteTarget(null)} disabled={deleteState.busy}>
        {t('cancel')}
      </button>
      <button type="button" className="danger" onClick={() => void deleteChannel(deleteTarget)} disabled={deleteState.busy}>
        {deleteState.busy ? t('deleting') : t('confirmDelete')}
      </button>
    </div>
  </div>
) : null}
```

- [ ] **Step 4: Implement delete action**

Use `deleteNoContent` from `web/src/api.ts`:

```tsx
async function deleteChannel(channel: Channel) {
  setDeleteState({ busy: true, error: null, message: null });
  try {
    await deleteNoContent(`/api/channels/${encodeURIComponent(channel.ID)}`);
    setDeleteTarget(null);
    setDeleteState({ busy: false, error: null, message: t('channelDeleted') });
    await onRefresh();
  } catch (err) {
    setDeleteState({ busy: false, error: errorMessage(err), message: null });
  }
}
```

If the server returns 409, the error text from the response should be visible in `FormFeedback`.

- [ ] **Step 5: Ensure deleted channel disappears from dependent views**

`TerminalSessionProvider` from Task 5 already clears or moves selection when `channels` no longer contains the selected channel. Verify `LogsPage` also updates its selected channel with the existing `activeChannelID` effect. Do not add cross-page direct calls between `ChannelsPage` and `TerminalPage`; keep coupling through refreshed `channels` props.

- [ ] **Step 6: Add CSS for delete confirmation and refresh/layout polish**

Append or adjust `web/src/styles.css`:

```css
.danger-confirm {
  display: grid;
  gap: 10px;
  margin-top: 12px;
  padding: 12px;
  border: 1px solid #e5a39a;
  border-radius: 8px;
  background: #fff4f2;
  color: #54251f;
}

.danger-confirm h3,
.danger-confirm p {
  margin: 0;
}

button.subtle {
  background: transparent;
  border-color: #d8c1bd;
  color: #9f3d32;
}

.toolbar {
  flex-wrap: wrap;
}
```

Also inspect `.log-export-form` and `.form-actions` so the Logs download button no longer overlaps any field at desktop or mobile widths.

- [ ] **Step 7: Type check and UI string scan**

Run:

```bash
rtk cd web && npm run lint
rtk rg -n 'Calibration|Live Log / Terminal' web/src
```

Expected: type check passes; `rg` returns no matches.

- [ ] **Step 8: Commit**

Run:

```bash
rtk git diff --check
rtk git status --short
rtk git add web/src/pages web/src/i18n.ts web/src/api.ts web/src/App.tsx web/src/styles.css
rtk git commit -m "feat(web): add channel delete and i18n"
```

Expected: commit created.

## Task 7: Full Verification, Real Devices, Browser Smoke, Cleanup, and Final Commit

**Files:**
- Modify only files required by verification fixes discovered in this task.

- [ ] **Step 1: Run full automated verification**

Run:

```bash
rtk go test -count=1 ./...
rtk REAL_SERIAL_DEV=/dev/ttyUSB0 REAL_SERIAL_BAUD=115200 REAL_SERIAL_EXPECT_LOOPBACK=1 make test-real-serial
rtk REAL_SERIAL_DEV=/dev/ttyUSB0 REAL_SERIAL_BAUD=115200 REAL_SERIAL_EXPECT_LOOPBACK=1 make test
rtk make build
rtk bash scripts/install_scripts_test.sh
rtk bash scripts/build-release.sh
rtk git diff --check
```

Expected:

- Go unit tests pass.
- `/dev/ttyUSB0` loopback test passes when the device is present and free.
- Build and install script tests pass.
- If `/dev/ttyUSB0` is missing or busy, record the exact error and continue with the remaining automated tests.

- [ ] **Step 2: Run `/dev/ttyUSB1` log-device smoke**

Use the existing binaries from `make build`.

Start central-server:

```bash
rtk ./bin/central-server --data-dir .server-data --listen 127.0.0.1:8080 --rfc2217-bind 127.0.0.1
```

Start host-agent in another process:

```bash
rtk ./bin/host-agent --server http://127.0.0.1:8080 --agent-id dev-host --scan-interval 2s --baud 2000000
```

If host-agent has no global `--baud` flag, create the channel through Web/API with `default_baud=2000000`; do not add a new CLI flag in this task.

Use Web or API to:

1. approve `dev-host`;
2. confirm `/dev/ttyUSB1` as a channel;
3. set baud `2000000`;
4. open Terminal and verify RX log continues to show data;
5. download text logs for that channel and verify payload content exists.

Stop both processes after smoke. No residual process may keep `/dev/ttyUSB0` or `/dev/ttyUSB1` open.

- [ ] **Step 3: Browser smoke with agent-browser**

Use Edge-compatible browser automation through the installed `agent-browser` skill. Verify:

1. navigation shows `Agents / Devices / Channels / Terminal / Logs`;
2. top-right language selector switches English and 中文 and persists after refresh;
3. refresh button spins or visibly enters loading state, then shows updated status;
4. Terminal empty output has stable height before frames arrive;
5. connect Terminal, switch to Logs, switch back, connection still shows connected;
6. live log display follows payload newline semantics;
7. Channels delete confirmation says logs will also be deleted;
8. after delete, channel disappears from Channels, Terminal channel selector, and Logs channel selector;
9. Logs download form has no prepare/download overlap at desktop and mobile widths.

If browser automation cannot connect to the local app, record the browser/tool error and complete manual API/browser checks where possible.

- [ ] **Step 4: Check for leftover test processes and serial owners**

Run:

```bash
rtk ps -ef | rg 'central-server|host-agent|rfc2217|serial-platform' || true
rtk lsof /dev/ttyUSB0 /dev/ttyUSB1 || true
```

Stop only processes started by this implementation session. Do not kill unrelated user processes without confirming they belong to this test run.

- [ ] **Step 5: Final repository cleanup**

Run:

```bash
rtk git status --short
rtk git diff --check
rtk cd web && npm run lint
rtk go test -count=1 ./...
```

Expected: only intended source/doc changes are present before final commit; lint and tests pass.

- [ ] **Step 6: Final commit for verification fixes**

If Step 1-5 required code changes after the last feature commit, run:

```bash
rtk git add <changed-files>
rtk git commit -m "fix(web): polish terminal workflow"
```

If no code changes were required, do not create an empty commit.

## Self-Review Checklist

- Spec coverage:
  - Navigation rename and order covered by Task 3 and Task 4.
  - Terminal session persistence covered by Task 5.
  - Terminal initial height covered by Task 5 and Task 7 browser smoke.
  - Channel delete and log cleanup covered by Task 1 and Task 6.
  - Live log payload newline rendering covered by Task 5.
  - Text export payload newline semantics covered by Task 2.
  - Refresh feedback covered by Task 3.
  - English/中文 language selector covered by Task 3 and Task 6.
  - Browser smoke and real devices covered by Task 7.
- Placeholder scan:
  - No placeholder terms or unspecified implementation steps are used.
  - Where existing JSX is moved, the owning file and exact retained behavior are stated.
- Type consistency:
  - `ViewKey`, `Language`, `RequestState`, `TerminalStatus`, and `LogDisplayLine` are defined before use.
  - `deleteNoContent`, `I18nProvider`, `TerminalSessionProvider`, `appendLiveLogFrame`, and page component names are consistent across tasks.
  - Backend methods `DeleteChannelWithLogSegments`, `ControlOwner.Busy`, and `deleteChannelLogFiles` are introduced before API use.
