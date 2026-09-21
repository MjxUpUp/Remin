# ADR-0007 claude-mem SQLite 导入 shell out sqlite3

- 状态：accepted（2026-09-21）

## 背景

claude-mem 将记忆存 SQLite。读取备选：modernc.org/sqlite（纯 Go，但体积/依赖重）、mattn/go-sqlite3（CGo，交叉编译负担）。

## 决策

导入时 shell out 系统 `sqlite3` CLI（只读查询 `-json` 模式）。sqlite3 缺失时该来源降级为「提示安装后重试」，不影响其他来源与其余功能。

## 后果

- 正：零重依赖、零 CGo；读取路径极简（单次 SELECT 导出 JSON）
- 负：依赖目标机装有 sqlite3 CLI（macOS/Linux 桌面普遍自带；doctor 报告该来源可用性）
