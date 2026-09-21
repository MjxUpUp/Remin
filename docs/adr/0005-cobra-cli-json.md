# ADR-0005 CLI 框架 cobra；全命令 --json

- 状态：accepted（2026-09-21）

## 背景

FR-UI-1/3：全命令 `--json` 结构化输出、子命令语义干净可被 GUI 包裹、无未声明交互副作用。备选 stdlib flag（子命令需手写路由、help 体验差）与 urfave/cli（功能等价、社区份额更低）。

## 决策

CLI 用 cobra；`internal/cli` 每命令一个实现，统一输出包络 `{ok, data|error}`；退出码规范见架构文档 §8.2。所有状态落盘可查询（GUI = CLI 的皮肤）。

## 后果

- 正：成熟帮助/补全/flag 语义；--json 契约让 GUI/脚本包裹零成本
- 负：引入 cobra+pflag 依赖（进依赖白名单，宪法扫描放行仅此 CLI 框架）
