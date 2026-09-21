# Remin（随忆）

> 换脑不换忆，用 Remin。

面向个人用户的跨 agent 可信记忆层：记忆以人类可读、可审计、可迁移的 markdown 文件归用户所有（`~/.remin/`），跟随用户在任何 AI 工具之间无缝流转。系统对全部记忆提供来源溯源、冲突消解、信任分层与失效验证，承诺「宁可不知道，不能自信地错」。

- 需求文档：https://my.feishu.cn/wiki/RKC4wmZ3diKBzOkxy9ucUNIinOd
- 技术设计方案：https://my.feishu.cn/wiki/FLnRwQn1niJ7A1k28IVcTlS7nKb
- 项目宪法：[PRINCIPLES.md](./PRINCIPLES.md)
- 记忆文件格式规范：[docs/spec/memory-format-v0.md](./docs/spec/memory-format-v0.md)

命名约定：产品 `Remin`（中文：随忆）｜ CLI 与二进制 `remin` ｜ 存储目录 `~/.remin/` ｜ MCP server 别名 `memory` ｜ 工具前缀 `memory_*`

> **当前状态（2026-09-18）**：v0.2.0 重构完成——按[系统架构设计](docs/architecture/system-architecture.md)（技术选型见 [ADR 索引](docs/adr/README.md)）以 M0-M6 切片重建：三层架构（`adapters/` 边缘 / `internal/protocol/` 协议 / `internal/` 核心）、宪法四件套全量进 CI、五来源导入、transcript 挖矿、MCP 五工具与四 agent 接线全部落地。v0.1.0 实现保留在 git 历史（`67b4b6f`）仅作过程参考。

## 安装与快速开始

要求：Go 1.26+、git。审收动作需要 git 身份可归因（`git config --global user.name/user.email`）。

```bash
make build            # 产出 bin/remin
export REMIN_HOME=~/.remin    # 默认即此，可省
bin/remin init        # 创建唯一真源（git 管理的记忆仓库）
bin/remin doctor --install    # 一键接线：注册 MCP（别名 memory）+ 会话 hooks（已装 agent 全局配置，写前备份）
```

日常闭环：

```bash
remin mine                      # 挖矿：从 Claude Code 会话日志提取记忆候选 → inbox
remin inbox                     # 审收视图（冲突建议排前）
remin promote --batch <id> --all   # 人审采纳（原子提交：记忆+supersession+版本推进+审计）
remin search "部署 注意事项"      # 确定性 BM25 检索（trust/provenance 随行，低置信弃权）
```

## 它是怎么工作的

```text
边缘层（零适配器代码：doctor 写的是直调 remin CLI 的全局配置）
  SessionStart hook → remin inject   （开场追赶[800ms 预算] + 精简索引注入 ~200 行）
  Stop hook         → remin hook-stop（会话结束挖矿，尽力而为永不失败）
协议层（MCP stdio，别名 memory；agent 面）
  memory_search / memory_propose / memory_verify / memory_status / memory_refresh
  （明确不存在 memory_write：agent 无直写通道——它只能提案，人审才生效）
核心层（cmd/remin + internal/*；零 agent SDK，CI 强制）
  Miner（transcript 旁路挖矿）→ Extractor（启发式）→ Inbox → Promotion（原子 git 提交）
  Supersession 引擎 / Verifier（verify-condition 用前验证）
  Search（内嵌确定性 BM25）/ Injector / Importer / Exporter / Eval / Doctor
  Store：~/.remin/ 个人 git 仓库（唯一真源；多设备 = git push/pull）
```

关键不变量（详见 PRINCIPLES）：

- **一切新记忆的产生都过 inbox**（bundle 整库还原通道除外——还原的是已人审的库）；promote 是唯一盖章入口（human-verified）
- **promote 是单一原子提交**：记忆落库 + supersession 链 + index 版本 +1 + 审计记录 + inbox 台账，任一步失败整体回滚（含暂存区）
- **冲突不共存**：新事实显式 supersede 旧事实，旧条目退出检索但完整保留于 git 历史
- **确定性检索**：同索引版本 + 同查询 = 同结果；低置信主动弃权（宁可不知道）
- **id 永不复用**（查全 git 历史）；时间不可还原就如实标 unknown，绝不伪造

## 命令总表

| 命令 | 行为 |
|---|---|
| `remin init` | 创建真源仓库（`--root`/`$REMIN_HOME` 可指定位置） |
| `remin doctor [--install] [--takeover]` | 检测已装 agent / 一键接线 / 健康检查 / 接管同名 memory server |
| `remin propose` | 显式记忆提案（进 inbox 待审） |
| `remin mine [--dry-run] [--force]` | transcript 挖矿（Claude Code JSONL，增量断点续挖） |
| `remin import [--from 来源] [--path 路径] [--apply]` | 从既有产品迁移（claude-auto-memory / claude-mem / chatgpt-export / codex-memories / markdown-dir；默认 dry-run，幂等只报增量；markdown-dir 为用户亲笔 → human-verified） |
| `remin inbox` / `promote` / `reject` | 审收：批次分组 / 原子采纳 / 归档拒绝 |
| `remin search` / `status` / `log` | 检索（trust 随行）/ 单条全貌（supersession 链）/ 审收审计历史 |
| `remin verify [id\|all]` | verify-condition 用前验证（原子回写，结果改变检索真值则版本 +1） |
| `remin refresh` | 查看当前快照版本（MCP 会话内用 memory_refresh 推进） |
| `remin inject` | 会话开场注入索引（hook 入口；--facet/--budget 可调） |
| `remin view [--write <path>]` | AGENTS.md 形态视图投影（默认预览；显式 opt-in 才落 repo） |
| `remin export --out <dir>` / `restore --bundle <dir>` | 全量导出（哈希清单）/ 从导出物整库还原（roundtrip 哈希一致） |
| `remin sync [--set-remote <url>]` | 多设备同步（git push/pull；远端仅托管，真源永在本地） |
| `remin eval run [--suite trust\|roundtrip\|all]` | 评测（规则可判定、模型无关），JSON 报告 |
| `remin mcp` | MCP stdio server（客户端拉起，别名 memory） |
| `remin hook-stop` / `version` | 会话结束 hook 入口（人工不常用）/ 版本与真源状态 |

全命令支持 `--json` 结构化输出（GUI/脚本可包裹——FR-UI-3）。

## 接入 agent

```bash
remin doctor              # 查看检测
remin doctor --install    # claude-code / codex / cursor / gemini-cli 全局配置接线（备份后合并）
```

- MCP：所有 MCP 客户端天然支持（别名 `memory`，五工具）
- Claude Code 额外获得会话级 hooks：开场自动注入 + 结束自动挖矿（仅 SessionStart/Stop，不用每工具调用级 hook）
- 文件视图型 agent（Cline/Roo 等）：`remin view --write AGENTS.md`（显式 opt-in）
- 同名官方 memory server：`--takeover` 一键替换

## 开发

```bash
make build / test / race / vet      # 常规
make constitution                   # 宪法测试套件（六项）+ 依赖扫描（P1-N1），CI 阻断合并
make adapter-budget                 # 适配器预算（P1-N2；当前零适配器代码）
```

架构：[系统架构设计](docs/architecture/system-architecture.md)（Go 单二进制、三层：边缘 `adapters/` / 协议 `internal/protocol/` / 核心 `internal/`，无常驻 daemon）· 技术选型决策见 [ADR 索引](docs/adr/README.md)。P1：核心零 agent SDK，CI 强制。里程碑计划（M0-M6）见架构文档 §13。

## 路线图（如实标注未实现项）

已落地（v0.2.0 重构）：核心数据层与原子审收 / 确定性 BM25 倒排检索与快照语义（JIT p95 1.3ms@10⁵ 条实测）/ MCP 五工具与快照钉住 / transcript 挖矿（启发式快速路径 + 增量游标 + recap 候选）/ doctor 接线（claude-code/codex/cursor/gemini-cli）与注入索引 / 五来源导入 / 视图投影与多设备同步（VERSION 冲突自动取 max）/ 可信评测入口（trust/roundtrip 套件）/ 宪法四件套全量进 CI。

未实现（下一阶段）：

- LLM 深度提取路径（端点可配；当前为启发式快速路径）
- 闲时增量 tick（OS 调度器档位）与 transcript 格式适配器扩展（Codex/DSH 日志；当前仅 claude-jsonl）
- 跨 agent 通道实测 parity eval 扩展（当前为 CLI 注入 × MCP 检索双通道；待接入真实第二 agent 会话）
- 端到端任务提升评测与纵向衰减曲线（需真实任务集与长期数据）
- 飞书 / Notion 笔记桥（摄取 human-verified + 单向发布回视图）
- GUI 审收界面（核心已按可包裹设计：CLI 全 `--json`）

## License

待定（开源协议由项目所有者选择后在此声明）。
