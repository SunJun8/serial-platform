# Serial Platform

内部串口测试平台，用于集中管理通过 USB Hub 接入 Linux host 的 USB 转串口设备、串口日志、Web 终端和远程 COM 访问。

## 第一版范围

- `central-server` 提供 Web UI、HTTP API、WebSocket 入口和 RFC2217 代理入口。
- `host-agent` 运行在接 USB Hub 的 host 上，负责注册到 central-server。
- 串口数据按 TX+RX raw framed traffic 存到中心端文件分片。
- SQLite 只保存 agent、channel、日志分片等元数据。
- Web terminal 使用平台自定义 WebSocket 控制协议。
- 外部串口/烧录工具通过 RFC2217 入口访问串口。
- 安装脚本负责 systemd 一键部署；channel 级 udev rules 由 host-agent 运行时负责，不由安装脚本刷新。

第一版不包含登录、权限、审计、测试 slot、烧录 recipe、服务端全文搜索、Windows 虚拟 COM 工具或 agent 磁盘 spool。

## 设计文档

- [设计说明](docs/superpowers/specs/2026-05-19-serial-platform-design.md)
- [实现计划](docs/superpowers/plans/2026-05-19-serial-platform-implementation.md)
- [冒烟测试](docs/superpowers/plans/2026-05-19-serial-platform-smoke-test.md)

## 构建

需要 Go 1.22+、Node.js `^20.19.0 || >=22.12.0`、npm。

```bash
make test
make build
```

`make build` 会先构建 React/Vite 前端并同步到 `internal/server/webdist`，再构建：

- `bin/central-server`
- `bin/host-agent`
- `bin/serialctl`

## 本地运行

```bash
./bin/central-server --data-dir .server-data --listen 127.0.0.1:8080 --rfc2217-bind 127.0.0.1
```

打开：

```text
http://127.0.0.1:8080/
```

另一个终端启动 agent：

```bash
./bin/host-agent --server http://127.0.0.1:8080 --data-dir .agent-data
```

查看 API：

```bash
./bin/serialctl --server http://127.0.0.1:8080 hosts list
./bin/serialctl --server http://127.0.0.1:8080 channels list
```

## 日志下载

```bash
./bin/serialctl --server http://127.0.0.1:8080 logs download \
  --channel-id channel-1 \
  --from 2026-05-19T00:00:00Z \
  --to 2026-05-19T01:00:00Z \
  --direction both \
  --timestamp \
  --direction-label \
  --output channel-1.log
```

默认导出 UTF-8 文本。非 UTF-8 字节会按 `\xNN` 转义。`--format raw` 可导出中心端保存的 raw framed traffic。

## 发布包

```bash
bash scripts/build-release.sh
```

输出：

```text
serial-platform-linux.tar.gz
```

发布包包含：

- `central-server-linux-amd64`
- `host-agent-linux-amd64`
- `host-agent-linux-arm64`
- `host-agent-linux-armv7`
- `serialctl-linux-amd64`
- `install-central.sh`
- `install-agent.sh`

## 安装

在 central server 机器：

```bash
sudo ./install-central.sh --data-dir /data/serial-platform --listen :8080 --rfc2217-bind 0.0.0.0
```

在 host-agent 机器：

```bash
sudo ./install-agent.sh --server http://central-server:8080 --data-dir /var/lib/serial-agent
```

安装脚本会写 systemd unit 并执行 `systemctl daemon-reload` 和 `systemctl enable --now ...`。agent 安装脚本只检查 `udevadm` 可用，不生成、不刷新 udev rules。
