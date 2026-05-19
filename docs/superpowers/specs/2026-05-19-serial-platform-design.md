# 串口测试平台设计

日期: 2026-05-19
范围: 内部局域网串口资产管理、日志采集、远程 COM 访问、Web 管理平台第一版

## 背景

嵌入式模组测试环境会通过 USB Hub 在一台或多台 Linux 主机上接入大量 USB 转串口设备。现有痛点包括:

1. Windows COM 号和 Linux `ttyUSB*` 号难以稳定管理。
2. DUT 频繁上下电、重插后，串口设备号变化，需要人工重新找端口。
3. 串口日志需要人工打开工具采集，历史日志不集中。
4. 远程烧录需要控制 DTR/RTS 或远程打开串口。
5. 级联 Hub、多串口模组、多台 USB 接入主机后，人工维护 udev 规则不可持续。

第一版目标是先建立稳定的串口基础设施: 物理端口映射、远程 COM、实时日志、历史下载、Web 管理和一键部署。

## 设计目标

1. 稳定身份绑定 USB 物理端口，而不是绑定某个 USB 转串口芯片。
2. 所有物理串口只由 host-agent/serial-agent 打开，避免多个进程抢占。
3. central-server 对外提供统一入口，包括 Web UI、API、CLI 和 RFC2217 远程 COM 端口。
4. host-agent 只主动连接 central-server，适配树莓派、NanoPi 等小节点。
5. 中心端保存完整 TX+RX raw traffic，用户默认下载 UTF-8 文本日志。
6. 第一版部署简单: Go 单二进制、SQLite、文件分片、release tarball + install script，不引入 Docker/PostgreSQL。

## 非目标

第一版不做以下能力:

1. 登录、权限、审计、多用户账户体系。
2. DUT/test slot 上层对象。
3. 烧录 recipe 或平台内置烧录任务编排。
4. 服务端日志关键字搜索或全文索引。
5. Windows 虚拟 COM 客户端/驱动。
6. Docker 部署和外部数据库服务。
7. 复杂主动通知，例如邮件、企业微信、飞书告警。

## 总体架构

```text
用户/工具
  -> central-server
     -> host-agent WebSocket
        -> serial-agent
           -> /dev/ttyUSBx
```

### central-server

central-server 是统一入口:

1. Go 后端。
2. React/Vite 前端，构建后嵌入 Go 二进制。
3. SQLite 保存配置、channel、端口和日志分片元数据。
4. 文件系统保存 raw framed traffic 日志分片。
5. 对 Web/API/CLI/RFC2217 暴露统一入口。
6. 对外暴露每个 channel 的固定 RFC2217 TCP 端口。
7. 通过 WebSocket 与 host-agent 通信。

### host-agent

每台接 USB Hub 的 Linux 主机运行一个 host-agent:

1. Go 单二进制，交叉编译到 `linux/amd64`、`linux/arm64`、`linux/armv7`。
2. 以 root + systemd service 运行。
3. 首次连接 central-server 后自动注册，状态为 pending。
4. 管理员在 Web UI 确认并重命名后，agent 进入 active 状态。
5. 生成本机 udev rules 和 `/dev/lab/...` symlink。
6. 监听 udev/sysfs 热插拔事件。
7. 管理本机多个串口 channel，目标规模不超过 50 个 channel/host。
8. serial-agent 永远是唯一物理串口 owner。

host-agent 节点不需要 Go、Node、Python 构建环境。

## 部署方案

第一版采用 release tarball + install script，不使用 Docker。

推荐产物:

```text
central-server-linux-amd64
host-agent-linux-amd64
host-agent-linux-arm64
host-agent-linux-armv7
install-central.sh
install-agent.sh
```

安装命令示例:

```bash
sudo ./install-central.sh --data-dir /data/serial-platform --listen :8080
sudo ./install-agent.sh --server ws://central:8080 --data-dir /var/lib/serial-agent
```

安装脚本负责:

1. 复制二进制到 `/usr/local/bin`。
2. 生成默认配置文件。
3. 创建 data/log 目录。
4. 创建 systemd unit。
5. 执行 `systemctl daemon-reload`。
6. 执行 `systemctl enable --now ...`。
7. 检查 systemd、udev、串口权限等运行前提，并给出明确错误。

安装脚本不生成、不刷新 channel 级 udev rules。运行期 host-agent 负责根据 central-server 下发配置生成和刷新本机 udev rules，并执行 `udevadm control --reload-rules` 和必要的 `udevadm trigger`。

## 拓扑和 channel 模型

稳定身份绑定 USB 物理路径。

### 绑定字段

主绑定字段:

```text
ID_PATH
```

辅助字段:

```text
ID_PATH_TAG       用于 udev/symlink 安全字段
sysfs devpath    用于诊断和 fallback
VID/PID          仅展示或告警
USB serial       仅展示或告警
driver           仅展示或告警
manufacturer     仅展示或告警
product          仅展示或告警
```

VID/PID、USB serial、driver 不默认参与匹配，避免更换 USB 转串口芯片后破坏物理端口绑定。

### channel 字段

```text
channel_id: UUID，不随重命名变化
auto_name: host01.hub02.port07.if00
alias: rack1.port07.console
role: console / at / boot / custom
id_path: USB 物理路径
id_path_tag: udev/symlink 辅助字段
sysfs_devpath: 诊断字段
rfc2217_port: 固定端口
default_serial_config: baud/data/parity/stop/flow
status: online/offline/busy/disabled
```

日志、配置、端口绑定都引用 `channel_id`。用户修改 alias、role 或重新生成 auto_name，不影响历史日志和 RFC2217 端口绑定。

删除 channel 后，`channel_id` retired，RFC2217 端口默认 disabled 保留，不自动复用。

### 多 interface 命名

USB 复合设备或多串口设备按 interface 顺序自动命名:

```text
host01.hub02.port07.if00
host01.hub02.port07.if01
```

管理员可以再配置业务别名:

```text
rack1.port07.console
rack1.port07.at
```

## 标定流程

第一版采用逐个端口插拔向导，不做全自动批量标定。

流程:

1. 管理员在 Web UI 进入 Calibration。
2. 选择 host。
3. 点击新增或校准 channel。
4. 按提示将设备插入目标物理口。
5. host-agent 捕获 udev 新增事件。
6. host-agent 读取 `ID_PATH`、`ID_PATH_TAG`、sysfs devpath、interface、VID/PID 等属性。
7. central-server 自动生成逻辑名。
8. 管理员确认并可编辑 alias/role。
9. central-server 保存 channel 映射。
10. host-agent 刷新 udev rules 和 symlink。

Web UI 还提供 USB 树扫描视图，用于查看当前 Hub 拓扑和所有 tty 属性。

## 串口所有权和控制

host serial-agent 永远是唯一物理串口 owner。

外部工具、Web terminal、未来烧录任务都不会直接打开 `/dev/ttyUSBx`。它们通过 `SerialControl` 接口控制对应 channel。

### SerialControl 接口

```text
OpenControlSession()
CloseControlSession()
Write(bytes)
SetConfig(baud/data/parity/stop/flow)
SetDTR(bool)
SetRTS(bool)
SendBreak(duration)
GetStatus()
```

调用方:

```text
Web terminal -> platform WebSocket control -> SerialControl
RFC2217 proxy -> RFC2217 parser -> SerialControl
future flash recipe -> SerialControl
```

### 控制互斥

每个 channel 同一时间只允许一个控制会话:

```text
Web terminal
外部 RFC2217 client
未来烧录任务
```

三者共用同一互斥。Web 日志视图不占用控制会话，可多人同时查看。

控制会话结束后，serial-agent 恢复 channel 默认串口参数。

### 热插拔

已登记端口断开或重插后:

1. host-agent 根据 udev/sysfs 事件重新识别设备。
2. 按 `ID_PATH` 绑定回原 channel。
3. 恢复默认串口参数。
4. 继续状态上报和日志采集。

## 协议分层

### 管理协议

```text
central-server <-> host-agent
WebSocket JSON
```

管理协议负责:

1. agent 注册和状态。
2. host/USB 树信息。
3. channel 配置下发。
4. channel online/offline/busy 状态。
5. 配置变更和应用结果。
6. tunnel 创建通知。

### 日志上报协议

```text
host-agent -> central-server
WebSocket binary
一条 binary message = 一个自定义 log frame
```

日志上报使用 WebSocket 作为传输通道，使用 framed log stream 作为消息语义。

log frame 字段:

```text
magic/version
header_len
channel_id
seq
timestamp_ns
direction: RX/TX
flags
payload_len
payload raw bytes
```

第一版一条 WebSocket binary message 对应一个 log frame。

### 远程 COM 协议

```text
user tool -> central-server RFC2217 TCP port
central-server -> host-agent tunnel WebSocket
host-agent -> SerialControl -> local serial
```

RFC2217 只作为外部远程 COM 兼容协议，不作为平台内部核心协议。

流程:

```text
user tool -> rfc2217://central:7003
central -> control WS 通知 agent 打开 channel
agent -> 主动连 central 建立 tunnel WS
central <-> agent tunnel WS 转发 RFC2217 字节流
agent RFC2217 parser -> SerialControl
```

agent offline、channel offline 或 channel busy 时，RFC2217 连接快速失败，不排队等待。

## Web terminal

浏览器不实现 RFC2217。Web terminal 使用平台自定义 WebSocket 控制消息。

第一版 Web terminal 功能:

1. 打开/关闭控制会话。
2. 发送文本。
3. 发送十六进制 bytes。
4. 修改 baudrate、data bits、parity、stop bits、flow control。
5. 设置 DTR。
6. 设置 RTS。
7. 发送 break。
8. 清屏。

不做宏命令、脚本执行、自动登录、命令历史同步、终端录制回放。

## 日志设计

central-server 始终保存完整 TX+RX raw framed traffic。raw 原始数据是权威记录，不能丢。

### 保存内容

每个 frame 保存:

```text
channel_id
seq
timestamp_ns
direction RX/TX
flags
raw bytes
```

TX 表示平台/用户/工具写入设备的数据。RX 表示设备输出的数据。

第一版不区分烧录和非烧录区间，不做 session 标记。

### 文件分片

SQLite 只保存元数据，raw 日志存文件分片。

推荐路径:

```text
/data/serial-platform/logs/<channel_id>/YYYY/MM/DD/HH/segment-000123.rlog
```

日志分片按时间或大小滚动，例如:

```text
每 5 分钟滚动一次
或达到 64MB 滚动一次
```

segment 元数据:

```text
channel_id
path
start_time
end_time
size_bytes
frame_count
status: active/closed
```

active segment 不参与删除。

### agent 缓冲

第一版不做磁盘 spool。

host-agent 使用有界内存缓冲，吸收短暂网络波动:

```text
默认每 channel 256KB
默认每 host 16MB
策略: drop oldest
```

server 断开、网络持续不可写或队列满时，agent 丢弃旧 frame，并在恢复后上报 `DROP`/`LOG_GAP` 事件。

串口读取不能被网络发送长期阻塞。

### 日志下载

默认下载 UTF-8 文本文档。

下载选项:

```text
格式:
  UTF-8 text
  raw framed log

方向:
  RX only
  TX only
  RX+TX

文本:
  是否带时间戳
  是否带方向
  是否 strip ANSI

范围:
  channel + 起止时间
```

文本导出规则:

1. UTF-8 可解码部分正常输出。
2. 非 UTF-8 字节用 `\xNN` 转义。
3. ANSI 控制码默认保留，可选 strip。

第一版不做服务端日志关键字搜索。用户下载后自行搜索和处理。

## 配额和清理

第一版支持配置:

```text
global_max_storage_bytes
default_retention_days
default_channel_max_storage_bytes
per_channel_max_storage_bytes override
warning_threshold_percent
critical_threshold_percent
cleanup_interval
```

配置策略:

1. 全局默认配置为主。
2. channel 可选覆盖。
3. 不做 host/role 级复杂策略。

清理策略:

1. 保留时间到期后删除最旧 closed segment。
2. 超过 channel 配额时，优先删除该 channel 最旧 closed segment。
3. 超过全局配额时，删除全局最旧 closed segment。
4. active segment 不删除。

配额状态在 Web UI 和 CLI 中可见:

```text
warning threshold: 80%
critical threshold: 95%
```

第一版不做主动通知。

## 配置变更策略

可热更新:

```text
alias
role
日志导出选项
配额配置
Web 显示配置
```

需要 channel restart:

```text
id_path 映射
默认 baud/data/parity/stop/flow
RFC2217 端口
udev symlink 名称
```

当需要 restart 的配置变化时:

1. 如果 channel 没有控制会话，agent 关闭并重新打开串口，应用新配置。
2. 如果 channel 正在被 Web terminal 或 RFC2217 使用，标记 pending config。
3. pending config 等控制会话结束后应用，或由管理员手动 force apply。

## Web UI

第一版 Web UI 包含 5 个视图。

### Hosts

展示:

1. agent 状态。
2. agent 版本。
3. 架构和 OS 信息。
4. pending/active 状态。
5. USB 树。
6. 当前检测到的 tty 设备。

支持:

1. 确认 pending agent。
2. 重命名 agent。
3. 查看 agent reconnect 状态。

### Channels

展示:

1. channel 列表。
2. online/offline/busy/disabled 状态。
3. auto_name、alias、role。
4. 默认串口参数。
5. 固定 RFC2217 endpoint。
6. 当前配置是否 pending apply。

支持:

1. 修改 alias/role。
2. 修改默认串口参数。
3. 禁用 channel。
4. force apply pending config。

### Calibration

支持逐个端口标定向导:

1. 选择 host。
2. 插入设备。
3. 自动捕获候选 tty。
4. 展示 `ID_PATH`、interface、VID/PID、driver。
5. 自动生成逻辑名。
6. 管理员确认。

### Live Log / Terminal

支持:

1. 实时日志查看。
2. 多人同时查看实时日志。
3. RX only / TX only / RX+TX 显示。
4. timestamp off/relative/absolute。
5. text/hex 显示。
6. 单控制会话 Web terminal。
7. DTR/RTS/break/baudrate 控件。

默认显示:

```text
RX+TX
方向开启
relative timestamp
UTF-8 text
```

### Logs

支持:

1. 按 channel + 时间范围选择日志。
2. 下载 UTF-8 文本日志。
3. 下载 raw framed log。
4. 选择 RX only / TX only / RX+TX。
5. 选择是否带时间戳。
6. 选择是否带方向。
7. 查看配额状态。

## CLI

第一版提供最小 `serialctl`，调用 central-server API。

示例命令:

```bash
serialctl hosts list
serialctl channels list
serialctl channels rename <channel-id> <alias>
serialctl logs download <channel-id> --from ... --to ...
serialctl rfc2217 list
serialctl status
```

CLI 用于排障和脚本化，不替代 Web UI。

## 数据存储

### SQLite

SQLite 保存:

```text
hosts
agents
channels
channel_aliases
rfc2217_ports
serial_default_configs
usb_identities
log_segments
quota_config
pending_configs
```

raw 日志不进入 SQLite。

### 文件系统

文件系统保存:

```text
raw framed traffic segment
可选 text export cache
```

备份建议以 central-server data dir 为单位:

```text
/data/serial-platform/meta.db
/data/serial-platform/logs/
/data/serial-platform/config/
```

## 错误处理

### central-server 重启

1. host-agent 自动重连。
2. server 不在线期间，agent 继续管理本地串口。
3. server 不在线期间，agent 不做磁盘缓存，日志可能丢失。
4. 重连后 agent 上报 `LOG_GAP`。
5. RFC2217 远程入口在 server 不在线期间不可用。
6. 已存在的 Web terminal/RFC2217 控制会话关闭，agent 恢复 channel 默认串口参数。

### host-agent 离线

1. central-server 标记 agent offline。
2. channel 标记 offline。
3. 对应 RFC2217 端口连接快速失败。
4. Web UI 显示 offline。

### channel busy

1. Web terminal 或 RFC2217 已有控制会话时，新的控制连接快速失败。
2. Web 日志查看不受影响。

### 日志写入失败

1. central-server 应标记 log storage error。
2. Web UI/CLI 显示错误状态。
3. active segment 写失败时，后续 frame 不能静默丢失，应记录内部错误计数。

## 后续扩展

设计保留但第一版不实现:

1. DUT/test slot 对象，将多个 channel 聚合为一个测试位。
2. 烧录 recipe，平台调用自有 Python 烧录工具。
3. 登录、权限、审计、租约。
4. 服务端全文搜索。
5. Windows 虚拟 COM helper。
6. PostgreSQL 存储后端。
7. agent 磁盘 spool。
8. 批量 Hub 端口标定。

## 验证标准

第一版完成后应满足:

1. 一个 central-server 能管理至少一台 host-agent。
2. 一台 host-agent 能管理不超过 50 个 channel。
3. 已登记 channel 重插后能按 `ID_PATH` 自动恢复。
4. Web UI 能看到 channel online/offline/busy 状态。
5. Web terminal 和外部 RFC2217 对同一 channel 互斥。
6. 外部工具能通过 `rfc2217://central:<port>` 访问串口。
7. DTR/RTS/break/baudrate 能通过 Web terminal 和 RFC2217 生效。
8. central-server 保存 TX+RX raw framed traffic。
9. 日志能按 channel + 时间范围导出 UTF-8 text 和 raw framed log。
10. 配额清理不会删除 active segment。
11. release tarball + install script 能完成 central-server 和 host-agent 安装。
