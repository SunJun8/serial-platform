# 串口平台真实设备工作流设计

日期: 2026-05-20
范围: 打通真实串口设备、host-agent、central-server、Web UI、CLI/RFC2217 用户工具之间的完整链路

## 背景

当前第一版已经实现 central-server 基础 API、日志保存、日志下载、RFC2217 解析、Web UI 壳和 host-agent 注册。但 host-agent 主程序还没有把真实 `/dev/ttyUSB*`、`/dev/ttyACM*` 的自动发现、打开串口、读取 RX、上传日志、Web/RFC2217 控制串起来。

本设计在原有总体设计基础上补齐真实设备工作流，重点解决:

1. 真实串口设备接入后可以被发现、确认、管理。
2. host-agent 是唯一物理串口 owner，避免资源抢占。
3. central-server 统一对外暴露 Web、日志下载和 RFC2217 入口。
4. Web terminal 和外部 RFC2217 工具都能经 central-server 访问远端真实串口。
5. 中心端保存完整 TX+RX raw framed traffic。
6. 开发机可用 `/dev/ttyUSB0` 单路 loopback 做默认真实设备测试。
7. 修复 central-server `Ctrl+C` 无法退出和 Web Logs 页面按钮重叠问题。

## 目标

1. 打通真实链路:

   ```text
   真实串口设备
     <-> host-agent serial worker
     <-> host-agent log uploader / tunnel client
     <-> central-server
     <-> Web UI / CLI / RFC2217 user tool
   ```

2. 保持原设计方向:
   - host-agent 是唯一物理串口 owner。
   - central-server 是统一入口。
   - host-agent 只主动连接 central-server。
   - channel 绑定 `ID_PATH` 物理路径，不绑定 USB 转串口芯片型号。

3. 第一版 Web UI 成为可用操作台:
   - Hosts 审批。
   - Channels 管理和手动添加。
   - Calibration 候选确认。
   - Terminal 实时收发。
   - Logs 下载。

4. 默认测试覆盖真实设备路径:
   - `make test` 尝试 `/dev/ttyUSB0` loopback 测试。
   - 没有设备或权限不足时不阻塞其它测试，但必须明确报告 skip 原因。
   - `make test-real-serial` 作为强制真实设备测试入口。

## 非目标

本轮不做:

1. 登录、权限、审计、多用户账户体系。
2. DUT/test slot 上层对象。
3. 烧录 recipe 或平台内置烧录任务。
4. 服务端全文搜索。
5. Windows 虚拟 COM 客户端。
6. PostgreSQL、Docker 或外部数据库服务。
7. 复杂批量标定。
8. 配额策略完整产品化。

## 架构原则

本轮实现必须遵守低耦合、高内聚，并显式避免资源抢占和死锁:

1. 控制 owner 只在 server 侧统一判定，Web terminal、RFC2217、未来烧录任务共用同一个 `ControlOwner`。
2. agent 侧 serial worker 只暴露 `SerialControl`，不关心调用来源。
3. tunnel registry 只负责 `tunnel_id -> WebSocket/TCP pairing`，不直接操作串口。
4. 日志采集只订阅 serial event，不持有控制锁。
5. 所有 Acquire 都必须有对应 Release，连接异常、超时、agent offline 都走统一释放路径。
6. channel worker 生命周期由 agent reconciler 单点管理，避免多个模块同时 open 同一个 `/dev/ttyUSBx`。
7. WS/TCP bridge 每个方向独立 goroutine，关闭通过 context 和 once 收敛，避免半关闭后泄漏或卡死。

## host-agent 设备管理

### 设备发现

host-agent 扫描:

```text
/dev/ttyUSB*
/dev/ttyACM*
```

并通过 `udevadm info -q property -n <device>` 读取:

```text
DEVNAME
ID_PATH
ID_PATH_TAG
DEVPATH
ID_VENDOR_ID
ID_MODEL_ID
ID_SERIAL_SHORT
ID_USB_INTERFACE_NUM
ID_VENDOR
ID_MODEL
```

绑定规则:

1. `ID_PATH` 是主绑定字段。
2. `ID_PATH_TAG` 用于安全命名和 udev symlink。
3. VID/PID、USB serial、driver、manufacturer、product 只用于展示、诊断和告警，不参与默认匹配。

第一版发现机制:

```text
启动时全量 scan
运行时定时 rescan
```

定时 rescan 默认间隔为 3s，并通过 agent 配置覆盖。netlink/udev monitor 不在本轮范围内，避免把真实设备链路打通依赖到事件监听实现。

### channel reconciler

agent 从 server 获取或接收 channel 配置后，按当前设备快照做 reconcile:

```text
configured channel + matching device -> start/reuse serial worker
configured channel + no matching device -> mark offline
unconfigured device -> report candidate
disabled channel -> close worker
```

重插后即使 Linux 设备名从 `/dev/ttyUSB0` 变成 `/dev/ttyUSB3`，只要 `ID_PATH` 不变，就绑定回同一个 channel。

### serial worker

每个 online channel 一个 worker:

```text
path: 当前 DEVNAME
config: channel default serial config
owner: host-agent only
```

worker 职责:

1. 打开真实串口。
2. 持续读取 RX。
3. Web terminal、RFC2217 tunnel 写入时产生 TX event。
4. RX/TX event 转成 `protocol.LogFrame`。
5. 通过 log uploader 上传到 central-server。

serial worker 不直接调用 server API，不处理 UI/RFC2217 业务语义。

### 权限处理

不把 `sudo` 作为常态运行方式。

install-agent:

1. 支持 `--user USER`，默认取 `SUDO_USER` 或当前用户。
2. 尽量把运行用户加入 `dialout` group。
3. systemd unit 使用指定用户运行。
4. 提示用户重新登录或重启服务使 group 生效。

host-agent 运行时:

1. 检测目标串口是否可读写。
2. 如果权限不足，打印明确提示:

   ```text
   serial device /dev/ttyUSB0 is not accessible by current user.
   Recommended:
     sudo usermod -aG dialout <user>
     newgrp dialout
   or log out and log in again.
   ```

3. 如果用户用 root 或 sudo 启动，允许运行，但提示不建议长期 root/sudo 运行。

### udev rules

本轮可以生成稳定 symlink，但不把 symlink 作为打开设备的唯一依据。agent 仍以 `ID_PATH -> 当前 DEVNAME` 运行时匹配为准。

推荐 symlink:

```text
/dev/lab/<agent>/<channel-alias-or-auto-name>
```

udev rules 由 host-agent 运行时根据 server 下发配置生成和刷新，不由安装脚本生成。

## server、tunnel 和 RFC2217

### agent control protocol

`/ws/agent` 是长连接管理通道，只传 JSON 管理消息，不承载大流量串口数据。

server -> agent:

```text
sync_channels
open_tunnel
close_tunnel
set_channel_config
```

agent -> server:

```text
device_snapshot
channel_status
tunnel_opened
tunnel_error
```

日志上传继续使用独立 `/ws/logs` binary framed message，避免管理消息和日志互相阻塞。

### RFC2217 按需 tunnel

外部工具连接 central-server 的 channel RFC2217 端口时，server 按需建立 tunnel:

```text
user tool
  -> tcp connect central-server:<rfc2217_port>
  -> server Acquire channel control owner
  -> server sends open_tunnel(channel_id, tunnel_id, mode=rfc2217)
  -> agent dials /ws/tunnel/{tunnel_id}
  -> server bridges TCP <-> tunnel WS bytes
  -> agent bridges tunnel WS bytes <-> local RFC2217 parser <-> SerialControl
```

规则:

1. 每个 channel 同时只有一个写控制 owner。
2. Web terminal 和 RFC2217 共用 `ControlOwner`。
3. 日志采集不占 owner，始终保存 RX/TX。
4. RFC2217 字节流在 server 侧透明转发，不在 server 侧解析为串口操作。
5. agent 侧复用 RFC2217 parser，把 baud、data bits、parity、stop bits、DTR、RTS、break 转成 `SerialControl` 操作。
6. 如果 agent 不在线、channel offline、权限不足、串口已被占用，RFC2217 TCP 连接快速关闭，并记录状态原因。
7. server 发出 `open_tunnel` 后，默认 5 秒内 agent 没有连接 `/ws/tunnel/{id}`，关闭外部 TCP 连接并释放 owner。

### Web terminal tunnel

Web terminal 也通过 server 统一入口控制串口:

```text
browser /ws/terminal/{channel_id}
  -> server Acquire channel owner
  -> server sends open_tunnel(channel_id, tunnel_id, mode=terminal)
  -> agent local serial session
```

Terminal tunnel payload 第一版使用二进制字节流。少量控制消息通过 `/ws/agent` 管理通道传输，不在 tunnel 内混杂复杂 framing。

### graceful shutdown

central-server 需要修复 `Ctrl+C` 无法退出:

1. 用 `http.Server` 替代直接 `http.ListenAndServe`。
2. SIGINT/SIGTERM 触发 root context cancel。
3. HTTP server 执行 graceful shutdown。
4. RFC2217 manager、tunnel registry、WS、TCP listener 挂在同一个 context 下。
5. 关闭时释放 control owner、关闭 tunnel、关闭数据库。

## Web UI 操作台

Web UI 保留现有页面结构，改为真实 API/WS 驱动。

### Hosts

功能:

1. 显示 agent 列表、状态、hostname、OS/arch、更新时间。
2. pending agent 可一键 approve。
3. active agent 显示在线/离线状态。
4. agent 重命名不在本轮范围内，第一版保持只读。

### Channels

功能:

1. 显示已确认 channel。
2. 字段包括 alias、auto_name、status、agent、当前设备路径、ID_PATH、RFC2217 端口、默认串口参数。
3. 支持手动添加 channel:
   - agent
   - device path 或 ID_PATH
   - alias
   - baud/data/parity/stop
   - rfc2217 port
4. 支持 enable/disable。
5. 支持编辑 alias 和默认串口参数。

### Calibration

功能:

1. 显示 agent 上报的候选 tty 设备。
2. 候选项展示 `/dev/ttyUSBx`、`ID_PATH`、interface、VID/PID、driver。
3. 用户确认候选后生成 channel。
4. 确认时可编辑 alias、role、rfc2217 port。
5. 保存后 server 下发配置，agent reconciler 启动 worker。

### Terminal

功能:

1. 选择 channel。
2. 连接后显示实时 RX/TX。
3. 输入命令发送 TX。
4. 支持修改 baud。
5. 支持 DTR、RTS、send break。
6. 如果 channel 被 RFC2217 占用，页面显示 busy，不抢占。

### Logs

功能:

1. 选择 channel、方向、时间范围、format。
2. 支持 timestamp、direction label、strip ANSI。
3. 点击下载直接触发文件下载。
4. 默认下载 UTF-8 文本，raw 作为原始数据选项。
5. 修复当前 prepare 按钮重叠问题。

布局要求:

1. 使用表单式布局，按钮跟随正常文档流。
2. 不把按钮压进窄列导致重叠。
3. 内部工具风格，密度适中、扫描友好、少装饰。
4. 不引入大型 UI 框架。
5. 图标继续使用 lucide。

## API 和状态模型

### 新增 API

```text
GET    /api/agents
POST   /api/agents/{agentID}/approve

GET    /api/channels
POST   /api/channels
PATCH  /api/channels/{channelID}
POST   /api/channels/{channelID}/enable
POST   /api/channels/{channelID}/disable

GET    /api/candidates
POST   /api/candidates/{candidateID}/confirm

GET    /api/logs/download
```

现有 API 可以保留，但新增接口应保持职责清晰，不把 channel、candidate、agent 状态混在一个大接口里。

### channel 状态

```text
online     agent 已打开真实串口 worker
offline    已配置但当前未匹配到设备
busy       有 Web terminal/RFC2217 控制 owner
disabled   用户禁用
error      打开失败，例如权限不足或参数错误
```

### candidate 状态

候选设备不等同于 channel。candidate 是当前 agent 发现但尚未确认的 tty 设备，至少包含:

```text
candidate_id
agent_id
dev_name
id_path
id_path_tag
sysfs_devpath
interface
vid
pid
serial
driver
first_seen
last_seen
```

## 日志保存和下载

agent 将 serial event 转为 `protocol.LogFrame`:

```text
channel_id
seq
timestamp_ns
direction: rx | tx
flags
payload
```

central-server 始终保存完整 raw framed traffic。

下载默认导出 UTF-8 文本:

1. 非 UTF-8 字节按 `\xNN` 转义。
2. 可选 timestamp。
3. 可选 direction label。
4. 可选方向过滤: `rx`、`tx`、`both`。
5. 可选时间范围过滤。
6. `format=raw` 导出原始 framed traffic。

## 测试设计

### 普通自动测试

`make test` 继续跑所有不依赖真实硬件的测试:

```text
protocol
storage
logstore
serial fake backend
agent discovery/reconciler
server API
tunnel registry
RFC2217 parser/listener
web terminal
log download
```

这些测试必须稳定通过。

### 默认真实串口 loopback 测试

`make test` 默认尝试真实设备测试，默认设备:

```text
/dev/ttyUSB0
```

行为:

```text
如果 /dev/ttyUSB0 存在且当前用户可读写:
  执行真实串口 loopback 测试
  输出 real serial: passed /dev/ttyUSB0

如果 /dev/ttyUSB0 不存在:
  不让 make test 失败
  输出 real serial: skipped, /dev/ttyUSB0 not found

如果 /dev/ttyUSB0 存在但权限不足:
  不让 make test 失败
  输出 real serial: skipped, permission denied, add current user to dialout
```

可配置变量:

```bash
REAL_SERIAL_DEV=/dev/ttyUSB0 make test
```

真实 loopback 测试内容:

1. 打开真实串口。
2. 设置默认串口参数。
3. 写入唯一测试 payload。
4. 从同一串口读回 payload。
5. 验证 TX event 和 RX event 都产生。
6. 验证 log uploader 把 framed log 发到 server。
7. 验证 server 下载日志包含该 payload。
8. 验证 RFC2217 连接 central-server 后写入 payload，也能从 loopback 读回。

### 强制真实设备测试

提供:

```bash
make test-real-serial
```

该命令严格要求真实设备可用。如果设备不存在、权限不足或 loopback 不通，应失败。

### 端到端手动验收

手动验收流程:

1. 启动 central-server。
2. 启动 host-agent。
3. Web approve agent。
4. agent 发现 `/dev/ttyUSB0` candidate。
5. Web Calibration 确认 candidate，生成 channel。
6. Web Terminal 打开 channel，发送字符串，loopback 回显。
7. Logs 页面下载 `rx`、`tx`、`both`、`raw`。
8. RFC2217 client 连 central-server 端口，发送字符串，日志中能看到 TX/RX。
9. Ctrl+C 关闭 central-server，进程退出，不残留监听端口。

浏览器测试允许使用 agent-browser 控制 Edge，验收点:

1. Hosts 页面 approve 可用。
2. Channels 页面能看到 channel 状态。
3. Calibration 候选确认可用。
4. Terminal 页面收发可用。
5. Logs 下载按钮不重叠，能下载文件。
6. 窄屏下 Logs 页面无明显文本或按钮重叠。

## 实现顺序建议

1. 修复 central-server graceful shutdown。
2. 扩展 storage/API，加入 channel 写操作和 candidate 模型。
3. 实现 agent device discovery 和权限诊断。
4. 实现 agent reconciler 和真实 serial worker 生命周期。
5. 实现 log uploader 与 server 日志保存端到端。
6. 实现 agent control protocol 和 tunnel registry。
7. 改造 RFC2217 为 server 透明代理、agent 侧解析控制。
8. 改造 Web terminal 为 tunnel 控制真实串口。
9. 接通 Web UI 各页面真实 API/WS，并修复 Logs 布局。
10. 增加真实 loopback 默认测试和强制测试命令。
11. 用 `/dev/ttyUSB0` 完成手动验收。

## 验收标准

1. `make test` 通过，并明确报告真实串口测试 passed 或 skipped 原因。
2. `make test-real-serial REAL_SERIAL_DEV=/dev/ttyUSB0` 在 loopback 设备可用时通过。
3. central-server Ctrl+C 能退出，不残留监听端口。
4. host-agent 不用 sudo 常态运行；权限不足时给出 dialout 建议。
5. Web approve agent 可用。
6. Web Calibration 能确认 `/dev/ttyUSB0` candidate。
7. Web Terminal 能对 loopback channel 收发。
8. Logs 页面布局无重叠，能下载 RX/TX/both/raw。
9. RFC2217 client 连接 central-server channel 端口后能通过 loopback 收发。
10. 中心端日志分片和 SQLite 元数据都能看到对应 channel 的记录。
