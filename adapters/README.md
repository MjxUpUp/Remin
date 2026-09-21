# 边缘层（adapters/）

按 ADR-0006：**零适配器代码**。agent 接入不靠本目录的代码，而靠 `remin doctor --install`
写入各 agent 全局配置（用户主目录，repo 零污染），内容是直调 `remin` CLI 的 hook 与
MCP server 注册（别名 `memory`）。

- SessionStart hook → `remin inject`（开场追赶 + 精简索引注入，永不失败）
- Stop hook → `remin hook-stop`（transcript 入队，尽力异步挖矿）
- MCP → `remin mcp`（stdio，客户端拉起）

未来若出现真正的事件桥（如 DSH 薄壳），放本目录，受宪法预算约束：
全部 ≤ 总代码 20%、单个 ≤ 8%（`make adapter-budget` 检查）。
