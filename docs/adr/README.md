# ADR 索引（架构决策记录）

技术选型决策按 ADR 轻量格式记录（状态/背景/决策/后果）。修改决策 = 新增 ADR 并标记旧条 superseded。

| 编号 | 决策 | 状态 |
|---|---|---|
| [0001](0001-go-as-core-language.md) | 核心语言选 Go | accepted |
| [0002](0002-embedded-bm25.md) | 内嵌确定性 BM25，不引入向量库 | accepted |
| [0003](0003-git-via-shellout.md) | git 操作 shell out 到系统 git | accepted |
| [0004](0004-mcp-official-sdk.md) | MCP 用官方 Go SDK，仅限协议层 | accepted |
| [0005](0005-cobra-cli-json.md) | CLI 框架 cobra；全命令 --json | accepted |
| [0006](0006-zero-adapter-edge.md) | 边缘层零适配器代码（doctor 写全局配置直调 CLI） | accepted |
| [0007](0007-sqlite-shellout.md) | claude-mem SQLite 导入 shell out sqlite3 | accepted |
| [0008](0008-distribution-lifecycle.md) | 分发与生命周期：npm 获取 + 落位 + 台账 + 自更新 + 干净卸载 | accepted |
