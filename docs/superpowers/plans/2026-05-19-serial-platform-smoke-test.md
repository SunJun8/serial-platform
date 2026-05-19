# Serial Platform Smoke Test

本文档用于第一版本地冒烟验证，目标是确认 central-server、host-agent、Web UI、CLI、安装脚本和日志导出基础路径可用。

## 1. 构建

```bash
make test
make build
bash scripts/install_scripts_test.sh
```

预期：

- Go 测试全部通过。
- React/Vite 前端 lint 和 build 通过。
- `bin/central-server`、`bin/host-agent`、`bin/serialctl` 生成。
- 安装脚本 smoke test 输出 `install script smoke tests passed`。

## 2. 启动 central-server

```bash
./bin/central-server --data-dir .server-data --listen 127.0.0.1:8080 --rfc2217-bind 127.0.0.1
```

预期：

- 进程打印 `central-server ... listening on 127.0.0.1:8080`。
- `http://127.0.0.1:8080/` 返回 Web UI。
- `http://127.0.0.1:8080/api/agents` 返回 JSON 数组，空库时为 `[]`。

## 3. 启动 host-agent

另开终端：

```bash
./bin/host-agent --server http://127.0.0.1:8080 --data-dir .agent-data
```

预期：

- agent 自动生成 `.agent-data/agent_id`。
- agent 连接 central-server 并收到 `pending` 状态。
- `./bin/serialctl --server http://127.0.0.1:8080 hosts list` 能看到该 agent。

## 4. 验证 Web UI

打开：

```text
http://127.0.0.1:8080/
```

预期：

- 页面首屏是操作界面，不是 landing page。
- 左侧包含 `Hosts`、`Channels`、`Calibration`、`Live Log / Terminal`、`Logs` 五个视图。
- API 可用时底部状态显示 `API connected`。

## 5. 验证 CLI

```bash
./bin/serialctl --server http://127.0.0.1:8080 hosts list
./bin/serialctl --server http://127.0.0.1:8080 channels list
./bin/serialctl --server http://127.0.0.1:8080 rfc2217 list
```

预期：

- 命令返回格式化 JSON。
- 空 channel 时返回 `[]`。

## 6. 验证日志上传和下载

第一版没有完整业务测试对象，可用现有 Go 测试验证日志路径：

```bash
go test ./internal/logstore ./internal/server -run 'TestLogDownload|TestExport' -count=1
```

预期：

- raw framed log 能按 `uint32 length + encoded LogFrame` 保存和导出。
- text 导出支持 RX/TX 方向过滤。
- text/raw 下载都按 frame timestamp 做 `[from, to]` 闭区间过滤。
- 非 UTF-8 字节在 text 导出中按 `\xNN` 转义。

## 7. 验证控制会话

```bash
go test ./internal/server ./internal/serial ./internal/rfc2217 -run 'Test.*Control|Test.*Terminal|Test.*RFC2217' -count=1
```

预期：

- Web terminal 通过统一 `SerialControl` 入口控制串口。
- 同一个 channel 同时只允许一个控制 owner。
- RFC2217 parser 能转换 baudrate、data bits、parity、stop bits、DTR、RTS 和 break 操作。

## 8. 验证发布包

```bash
bash scripts/build-release.sh
tar -tzf serial-platform-linux.tar.gz | sort
```

预期包含：

```text
./
./central-server-linux-amd64
./host-agent-linux-amd64
./host-agent-linux-arm64
./host-agent-linux-armv7
./install-agent.sh
./install-central.sh
./serialctl-linux-amd64
```

不要在开发机直接运行安装脚本，除非明确要写入 `/usr/local/bin` 和 systemd。

## 9. 清理

```bash
rm -rf .server-data .agent-data dist serial-platform-linux.tar.gz
```
