# ADR-0004 MCP 用官方 Go SDK，仅限协议层

- 状态：accepted（2026-09-21）

## 背景

机面主通道是 MCP stdio（客户端拉起本地进程）。备选手写 JSON-RPC（协议版本演进自担）与第三方 SDK（mark3labs/mcp-go 等，社区维护）。

## 决策

采用 `github.com/modelcontextprotocol/go-sdk`（官方），**仅 `internal/protocol` 允许 import**。宪法依赖扫描将白名单限定于此层：核心层（core/store/index/search/cli）不得 import 该 SDK——协议是核心的消费者，不是反过来（P1-N3 协议先行的依赖方向表达）。

## 后果

- 正：协议版本握手、通知、工具 schema 由官方维护；DSH/Claude Code/Codex 等 MCP 客户端兼容性有保障
- 负：SDK API 演进需跟随升级（锁定在协议层一个包内，影响面收敛）
