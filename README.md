# Serial Platform

Serial Platform 是一个内部局域网串口测试平台，用来统一管理接在 Linux host 上的大量 USB 转串口设备。

它解决的核心问题是：

- Linux `/dev/ttyUSB*`、`/dev/ttyACM*` 设备号会随插拔顺序变化。
- 串口日志分散在各个工具和各台机器上，不方便长期保存和下载。
- 远程烧录或调试时，需要通过网络访问串口，并控制 DTR/RTS。
- 多个工具直接打开同一个物理串口时容易互相抢占。

第一版的定位是“串口基础设施”，不是完整测试业务平台。它不包含登录、权限、审计、测试 slot、烧录 recipe、服务端全文搜索、Windows 虚拟 COM 工具、Docker 或 PostgreSQL。

## 架构

```text
浏览器 / CLI / RFC2217 客户端
  -> central-server
     -> host-agent WebSocket
        -> local serial worker
           -> /dev/ttyUSBx 或 /dev/ttyACMx
```

组件职责：

- `central-server`：统一入口，提供 Web UI、HTTP API、日志下载和 RFC2217 TCP 入口；保存 SQLite 元数据和日志分片。
- `host-agent`：运行在接 USB Hub 的 Linux host 上，只主动连接 central-server；负责发现、打开和控制本机串口。
- `serialctl`：简单 CLI，用来查看 host/channel 和下载日志。

串口所有权规则：

- `host-agent` 是唯一直接打开物理串口的进程。
- Web Terminal、RFC2217 客户端和后续烧录工具都通过 central-server 转发到 host-agent。
- 同一个 channel 同时只允许一个控制会话，避免 Web Terminal 和 RFC2217 同时写同一个串口。

## 数据目录

central-server 的 `--data-dir` 下会保存：

```text
<data-dir>/
├── meta.db       # SQLite 元数据：agent、channel、candidate、日志分片索引
└── logs/         # raw framed log 文件分片
```

host-agent 的 `--data-dir` 下会保存：

```text
<data-dir>/
└── agent_id      # 自动生成的 agent ID；删除后会重新注册为新 agent
```

## 快速本地试跑

依赖：

- Go 1.25+
- Node.js `^20.19.0 || >=22.12.0`
- npm
- Linux 上需要 `udevadm`

构建：

```bash
make build
```

启动 central-server：

```bash
./bin/central-server \
  --data-dir .server-data \
  --listen 127.0.0.1:8080 \
  --rfc2217-bind 127.0.0.1
```

打开 Web：

```text
http://127.0.0.1:8080/
```

另一个终端启动 host-agent：

```bash
./bin/host-agent \
  --server http://127.0.0.1:8080 \
  --data-dir .agent-data
```

开发机本地运行时不需要 `sudo`。如果 agent 发现串口但没有读写权限，把当前用户加入 `dialout` 组后重新登录：

```bash
sudo usermod -aG dialout "$USER"
newgrp dialout
```

## 首次接入串口

1. 在 host 机器上插入 USB 转串口设备。
2. 启动 host-agent。agent 会扫描 `/dev/ttyUSB*` 和 `/dev/ttyACM*`，并通过 `udevadm info` 读取 `ID_PATH`、`ID_PATH_TAG`、VID/PID、driver 等信息。
3. 打开 Web 的 `Agents` 页面，点击 `Approve` 激活新 agent。
4. 打开 `Devices` 页面，选择发现到的 candidate。
5. 确认 alias、role、RFC2217 port 和默认波特率，点击 `Confirm` 生成 channel。
6. 等待 `Channels` 页面中该 channel 变为 `online`。

channel 绑定的是 USB 物理路径 `ID_PATH`，不是 `/dev/ttyUSB0` 这种临时设备号，也不是某一颗 USB 转串口芯片。设备重插后，即使 Linux 设备号变化，只要物理路径不变，agent 会把它重新匹配到同一个 channel。

当前安装脚本不会创建或刷新 udev rules，也不要求用户手写 udev rules。第一版运行时直接使用 `ID_PATH -> 当前 DEVNAME` 的匹配结果打开串口。

## Web 使用

### Agents

查看所有注册过的 host-agent。新 agent 首次连接后状态为 `pending`，需要手动 `Approve` 后才会下发 channel 配置并参与串口管理。

### Devices

查看当前 agent 扫描到但尚未确认的串口 candidate。确认后会创建 channel，并分配一个固定 RFC2217 TCP 端口。

建议：

- alias 使用能表达物理位置的名称，例如 `rack01-hub02-port15-console`。
- RFC2217 port 在平台内要唯一，默认从 `7001` 开始递增。
- 默认波特率按设备实际需求填写，例如 BL616/BL618 烧录常用 `2000000`。

### Channels

查看已确认 channel 的状态、默认串口参数、绑定路径和 RFC2217 端口。

常见状态：

- `online`：agent 已匹配并打开物理串口。
- `offline`：channel 已配置，但当前没有匹配到可用设备。
- `busy`：已有 Web Terminal 或 RFC2217 控制会话占用。
- `disabled`：channel 被手动禁用。
- `error`：打开串口或权限检查失败。

### Terminal

用于 Web 里直接查看实时 RX/TX，并对串口发送数据。

可用操作：

- `Connect`：打开控制会话。
- 文本输入框 + `Send`：向串口写入数据。
- `Baudrate` + `Apply`：修改当前控制会话的串口参数。
- `DTR` / `RTS`：控制对应 modem line。
- `Break`：发送 break。

Web Terminal 不是 RFC2217 客户端；它使用平台自己的 WebSocket 控制协议，但底层和 RFC2217 共用同一个串口控制抽象。

### Logs

按 channel 和时间范围下载中心端保存的日志。

选项：

- `Direction`：`RX+TX`、`RX only`、`TX only`
- `Format`：`Text` 或 `Raw`
- `Timestamp`：文本导出时添加时间戳
- `Direction label`：文本导出时添加 `RX` / `TX`
- `Strip ANSI`：文本导出时去掉 ANSI escape sequence

默认用户应下载 `Text` 格式。`Raw` 是平台内部保存的 framed traffic，可作为原始参考数据。

## RFC2217 远程串口访问

每个 channel 有一个 RFC2217 TCP 端口，由 central-server 对外监听。

连接路径：

```text
RFC2217 client -> central-server:<channel-port>
  -> tunnel WebSocket
  -> host-agent
  -> local serial worker
  -> /dev/ttyUSBx
```

central-server 不直接打开物理串口，只代理到对应 host-agent。

典型用途：

- 远程串口工具连接平台暴露的 TCP/RFC2217 端点。
- 烧录工具使用 RFC2217 地址访问远端串口。
- 通过 RFC2217 设置 baud、DTR、RTS 后进入 boot 模式并烧录。

示例端点：

```text
rfc2217://central-server:7001
```

不同客户端对 RFC2217 URL 的写法不同，请按客户端工具文档填写 host 和 port。

如果某个 channel 正被 Web Terminal 占用，RFC2217 连接会失败或快速关闭；反过来也一样。

## CLI 使用

查看 hosts：

```bash
./bin/serialctl --server http://127.0.0.1:8080 hosts list
```

查看 channels：

```bash
./bin/serialctl --server http://127.0.0.1:8080 channels list
```

查看 RFC2217 端口列表：

```bash
./bin/serialctl --server http://127.0.0.1:8080 rfc2217 list
```

下载文本日志：

```bash
./bin/serialctl --server http://127.0.0.1:8080 logs download \
  --channel-id <channel-id> \
  --from 2026-05-21T00:00:00Z \
  --to 2026-05-21T01:00:00Z \
  --direction both \
  --timestamp \
  --direction-label \
  --output channel.log
```

下载 raw framed 日志：

```bash
./bin/serialctl --server http://127.0.0.1:8080 logs download \
  --channel-id <channel-id> \
  --format raw \
  --direction both \
  --output channel.raw
```

参数说明：

- `--channel-id` 必填。
- `--from` / `--to` 使用 RFC3339/RFC3339Nano 时间，例如 `2026-05-21T01:02:03Z`。
- `--direction` 可选 `rx`、`tx`、`both`。
- `--format` 可选 `text`、`raw`。
- 文本导出中，非 UTF-8 字节会用 `\xNN` 转义。

## 生产安装

先生成发布包：

```bash
bash scripts/build-release.sh
```

输出：

```text
serial-platform-linux.tar.gz
```

发布包内容：

- `central-server-linux-amd64`
- `host-agent-linux-amd64`
- `host-agent-linux-arm64`
- `host-agent-linux-armv7`
- `serialctl-linux-amd64`
- `install-central.sh`
- `install-agent.sh`

### 安装 central-server

在 central server 机器上解压发布包后执行：

```bash
sudo ./install-central.sh \
  --data-dir /data/serial-platform \
  --listen :8080 \
  --rfc2217-bind 0.0.0.0
```

安装脚本会：

- 安装 binary 到 `/usr/local/bin/central-server`
- 创建 `/etc/systemd/system/serial-platform-central.service`
- 创建 data dir 和 logs dir
- 执行 `systemctl daemon-reload`
- 执行 `systemctl enable --now serial-platform-central.service`

查看状态：

```bash
systemctl status serial-platform-central.service
journalctl -u serial-platform-central.service -f
```

### 安装 host-agent

在接 USB Hub 的 host 机器上解压发布包后执行：

```bash
sudo ./install-agent.sh \
  --server http://central-server:8080 \
  --data-dir /var/lib/serial-agent \
  --user "$USER"
```

安装脚本会：

- 按当前 CPU 架构选择 `host-agent-linux-amd64`、`host-agent-linux-arm64` 或 `host-agent-linux-armv7`
- 安装 binary 到 `/usr/local/bin/host-agent`
- 创建 `/etc/systemd/system/serial-platform-agent.service`
- 使用非 root 用户运行 host-agent
- 如果系统存在 `dialout` 组，把运行用户加入 `dialout`
- 检查 `udevadm` 是否存在
- 执行 `systemctl daemon-reload`
- 执行 `systemctl enable --now serial-platform-agent.service`

如果这是第一次把用户加入 `dialout`，需要重新登录，或在组成员关系生效后重启 service：

```bash
sudo systemctl restart serial-platform-agent.service
```

查看状态：

```bash
systemctl status serial-platform-agent.service
journalctl -u serial-platform-agent.service -f
```

## 开发与测试

常用命令：

```bash
make test
make build
```

`make test` 会：

1. 运行普通 Go 测试。
2. 尝试执行非阻塞真实串口 loopback 测试。

真实串口默认设备是 `/dev/ttyUSB0`，默认波特率是 `2000000`。如果设备不存在、权限不足、打开失败或没有读回 loopback payload，`make test` 会报告 skipped 原因并继续。

强制真实串口测试：

```bash
make test-real-serial REAL_SERIAL_DEV=/dev/ttyUSB0
```

执行前请将该串口的 TX 和 RX 短接。强制测试会失败而不是 skip，适合手动验收真实设备链路。

可选环境变量：

```bash
REAL_SERIAL_DEV=/dev/ttyUSB0
REAL_SERIAL_BAUD=2000000
REAL_SERIAL_EXPECT_LOOPBACK=1
```

构建命令：

```bash
make build
```

`make build` 会：

1. 在 `web/` 下执行 `npm ci`、`npm run lint`、`npm run build`。
2. 将 Web 产物同步到 `internal/server/webdist/`。
3. 构建 `bin/central-server`、`bin/host-agent`、`bin/serialctl`。

发布包构建：

```bash
bash scripts/build-release.sh
```

安装脚本 smoke test：

```bash
bash scripts/install_scripts_test.sh
```

## 常见问题

### Web 看不到 candidate

检查：

```bash
ls -l /dev/ttyUSB* /dev/ttyACM* 2>/dev/null
udevadm info -q property -n /dev/ttyUSB0
journalctl -u serial-platform-agent.service -f
```

如果 agent 日志提示权限不足，把运行用户加入 `dialout` 后重新登录或重启 service。

### channel 一直 offline

常见原因：

- 设备插在了不同 USB 物理端口，`ID_PATH` 变化。
- 设备被其它本机进程打开。
- host-agent 运行用户没有串口读写权限。
- channel 被 disabled。

可以在 Web 的 `Devices` 页面查看当前实际发现的 candidate，再决定是否创建新 channel。

### RFC2217 连接不上

检查：

- channel 是否 `online`。
- central-server 的 `--rfc2217-bind` 是否绑定到了可访问地址。
- 防火墙是否放行对应 channel port。
- channel 是否已被 Web Terminal 或另一个 RFC2217 会话占用。
- host-agent 是否在线。

### 日志下载为空

检查：

- 是否选错 channel。
- 时间范围是否覆盖实际日志时间。
- direction 是否选错，例如只选了 `rx` 但设备没有回传。
- host-agent 是否已经成功连接 central-server 的 log WebSocket。

### Ctrl+C 无法退出

当前 central-server 和 host-agent 都监听 SIGINT/SIGTERM。若发现退出异常，先检查是否有外部进程仍在连接 RFC2217 端口或 WebSocket，再查看对应服务日志。

## 设计文档

- [总体设计](docs/superpowers/specs/2026-05-19-serial-platform-design.md)
- [真实设备工作流设计](docs/superpowers/specs/2026-05-20-serial-platform-real-device-workflow-design.md)
- [实现计划](docs/superpowers/plans/2026-05-20-serial-platform-real-device-workflow-implementation.md)
