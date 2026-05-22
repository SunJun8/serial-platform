# Serial Platform

<p align="center">
  <strong>内部局域网串口基础设施。</strong>
</p>

<p align="center">
  稳定管理 USB 转串口设备，集中采集日志，并通过 Web、CLI 和 RFC2217 远程访问串口。
</p>

Serial Platform 面向嵌入式测试环境。它把接在 Linux host 上的大量 USB 转串口设备统一纳入 central-server 管理，避免 `/dev/ttyUSB*`、`/dev/ttyACM*` 因插拔顺序变化导致串口混乱。

第一版专注串口基础设施：设备发现、物理端口绑定、实时日志、历史下载、Web Terminal、RFC2217 远程串口和一键部署。它不是完整测试业务平台，不包含登录权限、测试 slot、烧录 recipe、服务端全文搜索、Windows 虚拟 COM 或 Docker/PostgreSQL 部署。

## 架构

```text
浏览器 / CLI / RFC2217 客户端
  -> central-server
     -> host-agent WebSocket
        -> serial worker
           -> /dev/ttyUSBx 或 /dev/ttyACMx
```

- `central-server`：统一入口，提供 Web、API、日志下载和 RFC2217 端口。
- `host-agent`：运行在接 USB Hub 的 Linux host 上，负责发现、打开和控制本机串口。
- `serialctl`：命令行工具，用于查看 host/channel、RFC2217 端口和下载日志。

## 快速开始

本地开发依赖：

- Go 1.25+
- Node.js `^20.19.0 || >=22.12.0`
- npm
- Linux 上需要 `udevadm`

```bash
make build

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

如果 agent 能发现串口但无法读写，请把运行用户加入 `dialout` 组后重新登录。

## 使用流程

1. 在 host 机器上插入 USB 转串口设备。
2. 启动 host-agent，让它扫描 `/dev/ttyUSB*` 和 `/dev/ttyACM*`。
3. 在 Web 的 `Agents` 页面 approve 新 agent。
4. 在 `Devices` 页面确认串口 candidate，设置 alias、role、RFC2217 port 和默认波特率。
5. 在 `Channels` 页面确认 channel 变为 `online`。
6. 使用 `Terminal` 收发串口数据，或通过 RFC2217 客户端连接固定端口。
7. 在 `Logs` 页面按 channel 和时间范围下载日志。

channel 绑定的是 USB 物理路径 `ID_PATH`，不是临时设备号，也不是某一颗 USB 转串口芯片。

## 特性

- 基于 `ID_PATH` 绑定 USB 物理端口，减少设备号漂移影响。
- pending agent 需要 Web 确认后才会进入 active 状态。
- host-agent 是唯一直接打开物理串口的进程，避免多个工具抢占。
- Web Terminal 和 RFC2217 共用同一套串口控制能力。
- 同一个 channel 同时只允许一个控制会话，多人可同时查看日志。
- central-server 保存完整 TX+RX raw framed traffic。
- 日志可导出为 UTF-8 文本或 raw framed log。
- SQLite 保存元数据，文件系统保存日志分片。
- React/Vite 前端会嵌入 Go central-server 生产二进制。
- release tarball 和安装脚本支持一键部署。

## CLI

```bash
./bin/serialctl --server http://127.0.0.1:8080 hosts list
./bin/serialctl --server http://127.0.0.1:8080 channels list
./bin/serialctl --server http://127.0.0.1:8080 rfc2217 list

./bin/serialctl --server http://127.0.0.1:8080 logs download \
  --channel-id <channel-id> \
  --from 2026-05-21T00:00:00Z \
  --to 2026-05-21T01:00:00Z \
  --direction both \
  --format text \
  --output channel.log
```

## 生产部署

生成发布包：

```bash
bash scripts/build-release.sh
```

发布包包含 central-server、host-agent、serialctl 和安装脚本。host-agent 提供 `linux/amd64`、`linux/arm64`、`linux/armv7` 版本，目标机器不需要 Go、Node 或 Python 构建环境。

安装 central-server：

```bash
sudo ./install-central.sh \
  --data-dir /data/serial-platform \
  --listen :8080 \
  --rfc2217-bind 0.0.0.0
```

安装 host-agent：

```bash
sudo ./install-agent.sh \
  --server http://central-server:8080 \
  --data-dir /var/lib/serial-agent \
  --user "$USER"
```

## 状态

Serial Platform 仍处于第一版内部工具阶段。当前范围刻意保持克制：先把串口资产管理、日志采集、远程访问和部署链路打通，再扩展测试业务能力。

## 开发

```bash
make test
make build
bash scripts/install_scripts_test.sh
```

真实串口 loopback 验收：

```bash
make test-real-serial REAL_SERIAL_DEV=/dev/ttyUSB0
```

执行前请短接该串口的 TX 和 RX。

## 文档

- [总体设计](docs/superpowers/specs/2026-05-19-serial-platform-design.md)
- [真实设备工作流设计](docs/superpowers/specs/2026-05-20-serial-platform-real-device-workflow-design.md)
- [Web UX 与 Terminal 国际化设计](docs/superpowers/specs/2026-05-21-web-ux-terminal-i18n-design.md)
- [本地冒烟验证](docs/superpowers/plans/2026-05-19-serial-platform-smoke-test.md)
