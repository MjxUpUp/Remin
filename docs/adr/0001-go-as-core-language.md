# ADR-0001 核心语言选 Go

- 状态：accepted（2026-09-21）
- 决策人：项目宪法 P1/P4 约束推导

## 背景

CLI 与 MCP stdio server 需同体分发（客户端拉起本地进程，零部署零运维）；检索（BM25）与挖矿需要可靠并发；git 生态与跨平台单二进制是硬需求。备选 Rust（更长编译周期、迭代成本高）与 Node/TS（运行时分发负担、claude-mem 等同类项目验证过的性能天花板）。

## 决策

Go 1.26 作为唯一实现语言；`cmd/remin` 单二进制同时承担 CLI 与 `remin mcp` server。

## 后果

- 正：单二进制跨平台（darwin/linux/windows）；goroutine 天然适配挖矿异步与 MCP 常驻；官方 MCP Go SDK 可用（ADR-0004）
- 负：CLI 生态弱于 Rust（可接受）；依赖管理需白名单约束（宪法依赖扫描的由来）
