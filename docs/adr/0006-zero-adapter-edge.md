# ADR-0006 边缘层零适配器代码（doctor 写全局配置直调 CLI）

- 状态：accepted（2026-09-21）

## 背景

P1-N2 适配器预算（全部 ≤20%、单个 ≤8%）与 N3 协议先行。v0.1.0 原设计含 TS 胶壳/hooks 脚本等适配器目录；重构结论：Claude Code/Codex/Cursor/Gemini CLI 均支持「全局配置注册 MCP server + 会话级 hook 直调命令行」——hook 调用的也是普通 CLI 子命令（`remin inject` / `remin hook-stop`），无需任何 per-agent 运行时代码。

## 决策

`adapters/` 目录保持**零代码**：doctor 直接写各 agent 全局配置（备份后合并），配置内容 = 以绝对路径直调 `remin` CLI/MCP。未来确需胶壳（如 DSH 事件桥）时按预算逐个引入并触发架构评审。

## 后果

- 正：适配器预算恒 0%；「拔适配器」演练平凡通过；无 per-agent 运行时维护面
- 负：依赖各 agent「hook 执行任意命令 + 全局 MCP 配置」能力（2026-09 时四个目标 agent 均满足；未来不满足的 agent 才需要胶壳）
