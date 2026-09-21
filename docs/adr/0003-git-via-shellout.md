# ADR-0003 git 操作 shell out 到系统 git

- 状态：accepted（2026-09-21）

## 背景

真源原子性依赖 git 提交语义（promotion 单提交事务）。备选 go-git 纯库（功能面大但行为保真度存疑，且是重依赖）。

## 决策

全部 git 操作 shell out 到系统 `git`（init/add/commit/status/log/remote/push/pull）。核心只依赖 git 行为契约（提交原子性、可回滚），用户可随时手工修复仓库。

## 后果

- 正：行为与官方 git 完全一致；零重依赖；用户可用原生 git 工具审计
- 负：要求目标机装有 git（`remin init`/`doctor` 显式检测并提示）；进程调用开销在毫秒级（人面命令，可接受）
