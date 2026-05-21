# Web UX, Terminal Session, Log Rendering, Channel Delete, and I18n Design

日期: 2026-05-21

范围: serial-platform Web 第一版的可用性修正和前端结构整理。

## 背景

当前真实串口链路已经打通，但 Web 使用流程暴露出几个问题:

1. `Live Log / Terminal` 命名重复，页面职责不够清晰。
2. Terminal 日志窗口初始高度不稳定，有日志刷新后才变为正常高度。
3. 从 Terminal 切到 Logs 再切回 Terminal 时，已连接设备会断开。
4. Channel 只能 enable / disable，不能删除。
5. `Calibration` 命名不准确，使用流程应是先连接 agent，再识别设备，再创建 channel。
6. 顶部刷新按钮反馈不足，用户不知道是否生效。
7. Live Log 按 frame 强制换行，和真实串口文本换行语义不一致。
8. Web 需要支持中文，并在右上角提供语言切换。

当前 `web/src/App.tsx` 已经包含 shell、页面、表单、Terminal WebSocket、live log 渲染和下载逻辑。继续在单文件内修补会让状态耦合更重，尤其 Terminal 连接状态不能再绑定到 `TerminalView` 的 mount/unmount 生命周期。

## 目标

本轮目标是改善第一版 Web 的真实使用体验，并保持低耦合、高内聚:

- 导航按实际使用流程命名和排序。
- Terminal 控制会话在页面切换时保持连接。
- Terminal 日志窗口初始高度稳定。
- Live Log 和文本导出遵循 payload 中真实换行符。
- Channel 支持危险删除，并同步删除该 channel 的日志。
- 顶部刷新按钮有明确 loading/成功/失败反馈。
- Web UI 支持 English / 中文切换。
- 拆分前端模块，避免继续扩大 `App.tsx`。

## 非目标

本轮不做:

- 登录、权限、审计。
- React Router 引入。
- 服务端全文搜索。
- Windows 虚拟 COM 工具。
- 复杂主题系统。
- 多语言动态加载或后端 i18n。
- Terminal 历史回放。
- 批量删除 channel。

## 推荐方案

采用轻量模块化 SPA 方案，不引入 React Router，不重做整体 UI 框架。

核心做法:

- `App` 只负责 shell、导航、全局数据刷新和 provider 组合。
- 页面拆为 `AgentsPage`、`DevicesPage`、`ChannelsPage`、`TerminalPage`、`LogsPage`。
- `TerminalSessionProvider` 挂在 `App` 下，持有控制 WebSocket、连接状态、当前 channel、DTR/RTS/baud、pending 操作。页面切换不会卸载 provider，因此不会断开。
- Live log 渲染逻辑拆为独立 buffer/hook，按 payload 中真实换行符生成显示行。
- `I18nProvider` 管理语言、字典和 `localStorage` 持久化。
- 后端新增 channel 删除 API，负责删除元数据和日志文件。

该方案对应 React 设计原则:

- Terminal WebSocket 这类外部连接放入 provider/hook，避免和页面生命周期耦合。
- 高频/外部对象如 WebSocket 实例、pending request map 使用 `useRef` 保存。
- 页面组件只消费稳定接口，不直接管理底层连接。
- i18n 字典和语言状态集中管理，避免文案分散。

## 信息架构

导航调整为:

```text
Agents
Devices
Channels
Terminal
Logs
```

页面职责:

- `Agents`: host-agent 注册、approve、状态查看。
- `Devices`: 展示当前 agent 扫描到的未确认设备，并从设备创建 channel。
- `Channels`: 管理已创建 channel，包括 enable、disable、delete。
- `Terminal`: 已创建 channel 的串口控制、实时日志显示、TX 输入、DTR/RTS/baud/break。
- `Logs`: 按 channel、方向、时间范围、格式下载日志。

命名规则:

- `Calibration` 改为 `Devices`。
- `Live Log / Terminal` 改为 `Terminal`。
- 页面内标题避免重复，例如 Terminal 页面主标题只显示 `Terminal`，日志区可显示 `Live log`。

## Terminal 会话保持

### 当前问题

当前 Terminal WebSocket 状态在 `TerminalView` 内部。React 条件渲染切换页面时会卸载 `TerminalView`，`useEffect` cleanup 会关闭 WebSocket，导致返回 Terminal 时必须重新 `Connect`。

### 新行为

在同一个浏览器页面内:

- `Terminal -> Logs -> Terminal` 不断开。
- `Terminal -> Channels -> Terminal` 不断开。
- 顶部全局 refresh 不断开。
- 用户点击 `Disconnect` 时断开。
- 用户在 Terminal 内切换 channel 时断开当前控制会话，再连接新 channel。
- 浏览器刷新、关闭 tab、网络中断或 server/agent 断开时断开。

### 组件边界

新增 `TerminalSessionProvider`:

```text
TerminalSessionProvider
  state:
    selectedChannelID
    status: idle | connecting | connected | error
    pendingCount
    baud
    dtr
    rts
    error
  refs:
    controlWS
    pendingRequests
  actions:
    selectChannel(channelID)
    connect(channelID)
    disconnect()
    writeText(text)
    setConfig(config)
    setDTR(value)
    setRTS(value)
    sendBreak(duration)
```

`TerminalPage` 只调用 provider 暴露的状态和 actions，不直接创建 WebSocket。

### Busy 状态

Web Terminal 和 RFC2217 仍共用后端 `ControlOwner`。如果 channel 已被占用，连接失败并显示错误，不抢占。

## Live Log 显示和换行

### 当前问题

Live Log 当前把每个 log frame 渲染为一行。串口数据常常一行被拆成多个 frame，或者一个 frame 包含多行，因此按 frame 换行会造成显示和保存内容不一致。

### 新规则

Live Log 按 payload 内容渲染:

- payload 没有 `\n` 时，追加到当前显示行后面。
- payload 包含 `\n` 或 `\r\n` 时，按真实换行拆分。
- payload 以换行结束时，下一段从新空行开始。
- 方向从 RX 切到 TX 或 TX 切到 RX 时，强制开启新行并显示新方向，避免同一显示行混入不同方向。
- 时间戳显示在每一条实际产生的新显示行前。
- 非 UTF-8 字节仍按现有策略用替代或转义方式显示，不影响 raw 保存。

建议抽象:

```text
appendLogFrameToBuffer(buffer, frame) -> nextBuffer
```

buffer 保存:

- 已完成显示行。
- 当前未结束行。
- 当前行方向。
- 当前行首时间戳。

Terminal UI 只渲染 buffer 输出，不直接按 frame map。

### 文本下载

中心端文本导出也要遵循 payload 自身换行:

- 不再将每个 frame 视为天然一行。
- 原始 payload 中的 `\n` / `\r\n` 应保留。
- 如果用户选择 `timestamp` / `direction_label`，只在每个 frame 的起始处添加前缀。
- `direction=rx|tx|both`、`format=text|raw` 行为保持不变。
- raw framed log 不变，仍是权威记录。

## Terminal 窗口高度

### 当前问题

Terminal 日志窗口初始高度不稳定，有日志刷新后才变为正常高度。

### 新规则

Terminal 页面首次渲染时高度必须稳定:

- 主日志区域使用明确的 flex/grid 约束。
- `.terminal-panel` 和 `.terminal-output` 不能依赖日志内容撑开。
- 空状态 `Waiting for live frames` 使用同样的输出区域高度。
- 页面在桌面和窄屏下都不能出现 Terminal 输入区、控制区和日志区重叠。

验收时用浏览器检查:

- 无日志时 Terminal 页面高度正常。
- 有日志后高度不跳变。
- 切换页面再回来，布局不跳变。

## Channel 删除

### 新能力

Channels 页面新增 `Delete` 操作。

删除是危险操作，前端必须二次确认，文案明确说明:

```text
Deleting this channel will also delete all logs for this channel. This cannot be undone.
```

中文:

```text
删除该 channel 会同步删除它的所有日志，且无法恢复。
```

### 后端 API

新增:

```text
DELETE /api/channels/{channelID}
```

行为:

1. 如果 channel 不存在，返回 404。
2. 如果 channel 当前 busy，返回 409，要求用户先断开 Terminal/RFC2217。
3. 删除该 channel 的日志分片文件。
4. 删除该 channel 的 log segment 元数据。
5. 删除 channel 元数据。
6. 返回 204。

如果删除日志文件时部分文件不存在，应继续清理元数据并返回成功。原因是日志分片可能已被人工清理，channel delete 不应因此卡死。

如果删除日志文件遇到权限或 I/O 错误，应返回 500，并尽量不删除 channel 元数据，避免出现“channel 不见了但日志文件还在且不可追踪”的状态。

### 数据一致性

SQLite 元数据删除应尽量在事务内完成。文件系统删除和 SQLite 事务无法原子化，因此采用顺序:

1. 读取待删 segment 列表。
2. 删除文件。
3. 在事务内删除 segment 元数据和 channel。

如果第 2 步失败，不进入第 3 步。

## 顶部刷新反馈

当前右上角 refresh 图标没有明显反馈。

新行为:

- 点击后图标旋转或按钮进入 loading 状态。
- 刷新期间按钮 disabled，避免重复请求。
- 刷新成功后显示短暂状态，例如 `Updated just now` / `刚刚更新`。
- 刷新失败时顶部 error strip 显示错误。

刷新仍只负责全局数据:

- agents
- channels

Devices 页面自己的 candidate refresh 可以保留页面内按钮。

## 中文支持

### 范围

第一版 i18n 只覆盖 Web UI 文案:

- 导航
- 页面标题
- 表格标题
- 表单 label
- 按钮
- 空状态
- 可控错误提示
- 删除确认文案
- 顶部刷新状态

不翻译:

- API JSON 字段名。
- alias。
- `ID_PATH` / `ID_PATH_TAG`。
- 日志 payload。
- 原始设备状态值在 API 中的表示。

### 语言选择

右上角 toolbar 加语言下拉:

```text
English
中文
```

默认语言:

1. 如果 `localStorage` 中有用户选择，使用该选择。
2. 否则根据 `navigator.language` 判断中文环境。
3. 其它情况默认 English。

持久化:

```text
localStorage key: serial-platform.language
value: en | zh-CN
```

### 实现边界

新增:

```text
web/src/i18n.ts
web/src/i18n-context.tsx
```

字典使用静态对象，不引入外部 i18n 依赖。

## 前端模块结构

本轮最低拆分要求:

```text
web/src/
  App.tsx
  api.ts
  types.ts
  i18n.ts
  i18n-context.tsx
  terminal-session.tsx
  live-log-buffer.ts
  pages/
    AgentsPage.tsx
    DevicesPage.tsx
    ChannelsPage.tsx
    TerminalPage.tsx
    LogsPage.tsx
  components/
    Badge.tsx
    EmptyRow.tsx
    Metric.tsx
    ViewTitle.tsx
```

其中 `i18n.ts`、`i18n-context.tsx`、`terminal-session.tsx`、`live-log-buffer.ts`、`pages/*` 是本轮必须建立的边界。`components/*` 可按实现需要拆分，但新增复杂逻辑不得继续塞进 `App.tsx`。

## 后端改动

需要新增或扩展:

- storage: 删除 channel 和相关 log segment 元数据的方法。
- server API: `DELETE /api/channels/{channelID}`。
- logstore/server: 删除 channel 日志文件的 helper。
- tests: 覆盖正常删除、channel 不存在、busy 冲突、日志文件缺失、文件删除失败。

现有 API 保持兼容:

- `GET /api/channels`
- `POST /api/channels`
- `POST /api/candidates/{candidateID}/confirm`
- `POST /api/channels/{channelID}/enable`
- `POST /api/channels/{channelID}/disable`
- `GET /api/logs/download`

## 测试要求

### Go

新增/更新测试:

- channel delete API 删除 channel 和 log segment 元数据。
- channel delete 删除日志文件。
- channel delete 对 busy channel 返回 409。
- channel delete 对不存在 channel 返回 404。
- 文本日志导出保留 payload 内部换行。
- 文本日志导出 timestamp/direction 只在 frame 起始处添加前缀。

### TypeScript

当前项目只有 `tsc --noEmit`，本轮至少保证:

```bash
cd web && npm run lint
```

可选新增纯函数测试能力不在本轮强制范围内。如果不引入测试框架，则 live log buffer 逻辑应保持纯函数并通过 Go/e2e 或手动浏览器 smoke 验证。

### Browser smoke

使用浏览器手动或 agent-browser 验证:

1. 导航显示为 `Agents / Devices / Channels / Terminal / Logs`。
2. `Terminal` 标题不再出现 `Live Log / Terminal`。
3. Terminal 无日志时高度正常。
4. Terminal connect 后切到 Logs 再切回 Terminal，连接仍保持。
5. Terminal payload 按真实换行显示。
6. Channels 页面 delete 弹窗说明会删除日志。
7. 删除 channel 后，该 channel 不再出现在 Channels / Terminal / Logs 下拉中，日志下载不可用。
8. 右上角 refresh 有 loading 或成功反馈。
9. 语言下拉可切换 English / 中文，并持久化。

### 全量验证

实现完成后运行:

```bash
go test -count=1 ./...
REAL_SERIAL_DEV=/dev/ttyUSB0 make test-real-serial
REAL_SERIAL_DEV=/dev/ttyUSB0 make test
make build
bash scripts/install_scripts_test.sh
bash scripts/build-release.sh
git diff --check
```

真实串口测试可根据设备占用情况执行；如果无法执行，必须明确说明原因。

## 验收标准

- Web 导航按流程命名和排序。
- `Calibration` 不再出现在 UI。
- `Live Log / Terminal` 不再出现在 UI。
- Terminal 页面切换不导致已连接会话断开。
- Terminal 日志显示遵循 payload 实际换行。
- 文本日志下载保留 payload 换行语义。
- Channel 可以删除，且删除前明确提示同步删除日志。
- 删除 channel 后，对应日志文件和元数据被清理。
- 顶部 refresh 有可见反馈。
- Web UI 支持 English / 中文切换并持久化。
- `App.tsx` 不继续承载新增复杂逻辑，Terminal session 和 i18n 有独立模块。
