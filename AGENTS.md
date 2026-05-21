# AGENTS.md

## Language

- 对话沟通：中文
- 文档编写：中文，除非用户特别要求
- 代码注释：英文

## 项目概述

本项目是内部局域网串口测试平台，用于统一管理大量通过 USB Hub 接入 Linux 主机的 USB 转串口设备。平台目标是解决 Linux `ttyUSB*`/Windows COM 号不稳定、串口日志分散、远程串口访问和 DTR/RTS 控制困难的问题。

正式设计文档：

- `docs/superpowers/specs/2026-05-19-serial-platform-design.md`

开始实现前必须先阅读该设计文档。如果实现计划与文档冲突，应先和用户确认并更新设计。

## 第一版范围

第一版做串口基础设施，不做测试业务平台。

必须包含：

- central-server 统一入口
- host-agent 接 USB Hub 主机
- host-agent 自动注册，初始 pending，Web 确认后 active
- 基于 `ID_PATH` 的物理端口绑定
- host-agent 生成本机 udev rules 和 `/dev/lab/...` symlink
- serial-agent 永远唯一打开物理串口
- WebSocket JSON 管理协议
- WebSocket binary framed log 上报
- central-server 对外暴露固定 RFC2217 端口
- central-server RFC2217 proxy 到 host-agent tunnel WebSocket
- Web terminal 使用平台自定义控制协议，不实现 RFC2217
- 每个 channel 同时只有一个控制会话
- Web 多人实时查看日志
- 中心端保存完整 TX+RX raw framed traffic
- 日志按 channel + 时间范围导出 UTF-8 text 或 raw framed log
- SQLite 保存元数据，文件系统保存日志分片
- Go 后端和 agent
- React/Vite 前端，生产构建嵌入 Go central-server
- release tarball + install script 一键部署

第一版不做：

- 登录、权限、审计、多用户账户体系
- DUT/test slot 上层对象
- 烧录 recipe 或平台内置烧录任务编排
- 服务端日志关键字搜索或全文索引
- Windows 虚拟 COM 客户端/驱动
- Docker/PostgreSQL
- agent 磁盘 spool
- 批量 Hub 端口标定

## 核心架构

```text
用户/工具
  -> central-server
     -> host-agent WebSocket
        -> serial-agent
           -> /dev/ttyUSBx
```

部署模型：

- central-server：Go 后端，SQLite + 文件分片，嵌入 React 静态资源，对外提供 Web/API/CLI/RFC2217。
- host-agent：Go 单二进制，以非 root systemd service 运行，交叉编译到 Linux amd64/arm64/armv7；安装脚本由 root 执行，仅负责安装 binary/unit、设置 data dir ownership、按需加入 dialout，运行时不应需要 sudo/root。
- host-agent 节点不应要求 Go/Node/Python 构建环境。

## 关键设计约束

### 身份与拓扑

- 稳定身份绑定 USB 物理路径，不绑定某个 USB 转串口芯片。
- 主绑定字段：`ID_PATH`。
- `ID_PATH_TAG` 只作为 udev/symlink 安全字段。
- sysfs devpath 用于诊断和 fallback。
- VID/PID/USB serial/driver/manufacturer/product 只展示或告警，不默认参与匹配。
- USB 复合设备或多串口设备按 interface 自动命名，如 `host01.hub02.port07.if00`。
- channel 内部主键用 UUID，不使用 alias/auto_name 当主键。

### 串口控制

host serial-agent 永远是唯一物理串口 owner。所有控制入口都走统一 `SerialControl` 接口：

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

调用方：

```text
Web terminal -> platform WebSocket control -> SerialControl
RFC2217 proxy -> RFC2217 parser -> SerialControl
future flash recipe -> SerialControl
```

控制会话结束后恢复 channel 默认串口参数。

### 协议

```text
管理协议:
  central-server <-> host-agent
  WebSocket JSON

日志上报:
  host-agent -> central-server
  WebSocket binary
  一条 binary message = 一个自定义 log frame

远程 COM:
  user tool -> central-server RFC2217 TCP port
  central-server -> host-agent tunnel WebSocket
  host-agent -> SerialControl -> local serial
```

RFC2217 只作为外部远程 COM 兼容协议，不应渗透到核心串口模型。

### 日志

- central-server 始终保存完整 TX+RX raw framed traffic。
- raw 原始数据是权威记录，不能丢。
- SQLite 只存日志分片元数据，raw 日志写文件分片。
- 默认下载 UTF-8 文本文档。
- 下载支持 RX only、TX only、RX+TX。
- 文本导出可选是否带时间戳、是否带方向、是否 strip ANSI。
- 非 UTF-8 字节用 `\xNN` 转义。
- 第一版不做服务端搜索。
- agent 不做磁盘 spool，只做有界内存缓冲：默认每 channel 256KB，每 host 16MB，满了 drop oldest 并上报 `DROP`/`LOG_GAP`。

### 配额

支持全局默认 + channel override：

```text
global_max_storage_bytes
default_retention_days
default_channel_max_storage_bytes
per_channel_max_storage_bytes override
warning_threshold_percent
critical_threshold_percent
cleanup_interval
```

active segment 不删除。

## 开发约束

- 默认使用 Go 实现 central-server、host-agent、CLI。
- 前端使用 React/Vite；生产部署时静态资源嵌入 central-server。
- 避免引入 Docker、PostgreSQL 或需要额外守护进程的依赖，除非用户重新确认。
- 对方案或实现有歧义时，先确认，不要默默扩大第一版范围。
- 改动应遵循“低耦合、高内聚”：RFC2217、Web terminal、未来烧录 recipe 都通过统一 serial control 抽象进入核心。
- 对配置变更区分热更新和需要 channel restart；控制会话期间不自动重启 channel，延后应用或管理员 force apply。

## 建议目录结构

```text
cmd/
  central-server/
  host-agent/
  serialctl/
internal/
  agent/
  protocol/
  rfc2217/
  serial/
  server/
  storage/
  topology/
web/
  src/
docs/
  superpowers/
    specs/
scripts/
```

## 重要提示

正式设计在 `docs/superpowers/specs/2026-05-19-serial-platform-design.md`。开始实现前，先阅读该文档；如果实现计划与文档冲突，应先和用户确认更新设计。
