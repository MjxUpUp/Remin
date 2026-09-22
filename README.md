# Remin（随忆）

> 换脑不换忆，用 Remin。

面向个人用户的跨 agent 可信记忆层：记忆以人类可读、可审计、可迁移的 markdown 文件归用户所有（`~/.remin/`），跟随用户在任何 AI 工具之间无缝流转。系统对全部记忆提供来源溯源、冲突消解、信任分层与失效验证，承诺「宁可不知道，不能自信地错」。

- 需求文档：https://my.feishu.cn/wiki/RKC4wmZ3diKBzOkxy9ucUNIinOd
- 技术设计方案：https://my.feishu.cn/wiki/FLnRwQn1niJ7A1k28IVcTlS7nKb
- 项目宪法：[PRINCIPLES.md](./PRINCIPLES.md)
- 记忆文件格式规范：[docs/spec/memory-format-v0.md](./docs/spec/memory-format-v0.md)

命名约定：产品 `Remin`（中文：随忆）｜ CLI 与二进制 `remin` ｜ 存储目录 `~/.remin/` ｜ MCP server 别名 `memory` ｜ 工具前缀 `memory_*`

> **当前状态（2026-09-22）**：v0.2.0 重建之上，v0.3.x 生命周期与 v0.4.0 深度提取/引导体验已发版；主线推进闲时增量 tick（feat/idle-tick）。端到端演练（`scripts/e2e-drill.sh`）16 步全通；全量测试 + `-race` + `make constitution` 全绿。

## 安装与快速开始

安装（npm，推荐）：要求 Node ≥ 18 + git。审收动作需要 git 身份可归因（`git config --global user.name/user.email`）。

```bash
npm install -g @reminmem/remin
remin init                 # 创建唯一真源 + 交互式向导（TTY；路径/自治档/agent 接线/备份远端；脚本/非终端用 --defaults）
remin doctor --install     # 落位二进制到 ~/.remin/bin + 一键接线（备份 + 台账记账）
```

npm 只是获取渠道：接线钉的是 `~/.remin/bin/remin` 稳定路径，nvm 切版本 / npm 目录变化都不影响已接线配置（[ADR-0008](docs/adr/0008-distribution-lifecycle.md)）。

从源码构建（开发）：要求 Go 1.26+。

```bash
make build && bin/remin init && bin/remin doctor --install
```

## 升级与卸载

```bash
remin upgrade            # npm registry → 完整性校验 → 原位原子替换落位二进制（配置零改动）
remin upgrade --check    # 只看有无新版本
remin uninstall          # 按接线台账精确摘除全部接线（cordis 可逆），默认保留记忆真源
remin uninstall --purge  # 连同 ~/.remin（含全部记忆）彻底删除
```

说明：

- `remin upgrade` 换的是落位真身（agent 接线指向它）；npm 渠道的命令副本不参与运行时，升级后可按需 `npm update -g @reminmem/remin` 同步渠道，或直接用 `~/.remin/bin/remin`。
- `remin uninstall` 摘接线/落位/备份；npm 安装的用户另需 `npm uninstall -g @reminmem/remin` 清掉渠道命令本体。
- 镜像源：`REMIN_NPM_REGISTRY=https://registry.npmmirror.com`（中国大陆推荐）；关闭版本检查提示：`REMIN_NO_UPGRADE_CHECK=1`。

日常闭环：

```bash
remin mine                      # 挖矿：默认只挖近 7 天（--full-history 开时间窗补挖更早；--force 重置游标重挖；--deep 追加 LLM 深度提取）
remin inbox                     # 审收视图（构成统计 + 行动指引；详情视图 --type 分诊）
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
  Miner（transcript 旁路挖矿）→ Extractor（启发式快速路径；mine --deep 时追加 LLM 深度路径，quote 溯源守卫）→ Inbox → Promotion（原子 git 提交）
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
| `remin init [--defaults]` | 创建真源仓库（`--root`/`$REMIN_HOME` 可指定位置）；TTY 下交互式向导（非 TTY 自动直通） |
| `remin doctor [--install] [--takeover]` | 检测已装 agent / 一键接线 / 健康检查 / 接管同名 memory server |
| `remin propose` | 显式记忆提案（进 inbox 待审） |
| `remin mine [--dry-run] [--force] [--from-queue] [--deep] [--since N\|--full-history]` | transcript 挖矿（Claude Code JSONL，增量断点续挖）；默认仅挖 mtime 近 7 天（首挖限量防历史 recap 洪泛），`--full-history` 显式不限时间窗（补挖更早）；`--deep` 追加 LLM 深度提取（见下方「深度提取」）；配置 llm 端点时，快挖处理过的行段自动挂账 deep 待挖队列（闲时 tick 深挖） |
| `remin tick [--deep-max N] [--since N\|--full-history]` | 闲时增量维护：增量快挖 + deep 待挖排空（每 tick 限段防长跑；密钥不在场保留队列；弃权段出队不重试）——供 OS 调度器按档拉起，无常驻 daemon |
| `remin tick schedule install [--every 4h] / remove / status` | OS 调度器接线（macOS launchd / Linux systemd user timer；effect 挂接线台账，`remin uninstall` 可回放摘除；间隔下限 15m） |
| `remin import [--from 来源] [--path 路径] [--apply]` | 从既有产品迁移（claude-auto-memory / claude-mem / chatgpt-export / codex-memories / markdown-dir；默认 dry-run，幂等只报增量；markdown-dir 为用户亲笔 → human-verified） |
| `remin inbox [--batch <id> --type <t>]` / `promote` / `reject` | 审收：批次构成统计 + 行动指引 / 原子采纳 / 归档拒绝；`--type` 分诊（inbox 的 `--type` 配 `--batch` 详情视图生效；纯快速路径批次内 episodic 即 recap，`reject --batch <id> --all --type episodic` 一键清；--deep 批次可能含深度提取的 episodic 候选，先 `inbox --batch <id> --type episodic` 看一眼再拒） |
| `remin search` / `status` / `log` | 检索（trust 随行）/ 单条全貌（supersession 链）/ 审收审计历史 |
| `remin verify [id\|all]` | verify-condition 用前验证（原子回写，结果改变检索真值则版本 +1） |
| `remin refresh` | 查看当前快照版本（MCP 会话内用 memory_refresh 推进） |
| `remin inject` | 会话开场注入索引（hook 入口；--facet/--budget-ms/--max-lines 可调，facet 默认取 config `inject_facet`） |
| `remin view [--write <path>]` | AGENTS.md 形态视图投影（默认预览；显式 opt-in 才落 repo） |
| `remin export --out <dir>` / `restore --bundle <dir> [--check]` | 全量导出（哈希清单）/ 从导出物整库还原（原子换入，roundtrip 哈希一致）；`--check` 只验哈希不还原 |
| `remin sync [--set-remote <url> --yes]` | 多设备同步（git push/pull；远端仅托管，真源永在本地）；设置远端需确认私有仓库（TTY 交互确认，非 TTY 加 `--yes` 显式自担） |
| `remin eval run [--suite trust\|roundtrip\|parity\|conflict\|budget\|all]` | 评测（规则可判定、模型无关），JSON 报告；parity 实跑 MCP stdio 通道；有失败项时退出码 1 |
| `remin mcp` | MCP stdio server（客户端拉起，别名 memory） |
| `remin upgrade [--check]` | 自更新：npm registry → sha512 校验 → 原子替换落位；--check 只查不动 |
| `remin uninstall [--purge]` | 按台账回放摘除全部接线与备份；--purge 连记忆真源一并删除 |
| `remin hook-stop` / `version` | 会话结束 hook 入口（人工不常用）/ 版本与真源状态（有新版时轻提示） |

面向人的命令全支持 `--json` 结构化输出（GUI/脚本可包裹——FR-UI-3）；inject/hook-stop（hook 面，永不失败文本输出）与 mcp（机面 stdio）除外。

## 深度提取（可选，默认关闭）

快速路径是启发式 regex（零 LLM、离线可用、确定性），抓不到自然表达的记忆。`remin mine --deep` 追加 LLM 语义提取补充召回，宪法约束不放松：

- **零 SDK**：stdlib 直连 OpenAI 兼容端点（`config.yaml` 的 `llm:` 节，`endpoint`/`model`/`timeout_ms`）
- **密钥不落盘**：只从环境变量 `REMIN_LLM_API_KEY` 读——config.yaml 在 git 真源内，密钥写盘等于提交进历史
- **弃权优于编造**（P3-A5）：LLM 返回的每条候选必须带 `quote` 且逐字溯源到源会话文本（空白归一），对不上即拒收；端点失败/响应异常 → 该次弃权（报告 Note 披露），快速路径结果不受影响
- **只产候选**：与快速路径同构，trust=unverified 进 inbox 人审，LLM 无落库权；origin 标注 `claude-code·deep` 可区分
- **hook 路径永不触网**：Stop hook / inject 追赶仍是纯快速路径（延迟预算硬约束）
- **闲时自动深挖**：快挖处理过的行段自动挂账 deep 待挖队列；`remin tick schedule install` 装 OS 调度器（launchd / systemd user timer）定期跑 `remin tick` 排空（每 tick 限段、密钥不在场保留队列、端点失败弃权出队不重试）

```yaml
# config.yaml（真源内；llm: 节需手动添加，remin init 默认不生成）
llm:
  endpoint: https://api.example.com/v1/chat/completions  # 任意 OpenAI 兼容端点
  model: your-model
  timeout_ms: 10000   # 单次调用硬预算（缺省 10s）
```

```bash
export REMIN_LLM_API_KEY=sk-...
remin mine --deep
```

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
make constitution                   # 宪法五套件 + 依赖扫描（P1-N1）+ 适配器预算（P1-N2），CI 阻断合并
make adapter-budget                 # 适配器预算（P1-N2；当前零适配器代码）
```

架构：[系统架构设计](docs/architecture/system-architecture.md)（Go 单二进制、三层：边缘 `adapters/` / 协议 `internal/protocol/` / 核心 `internal/`，无常驻 daemon）· 技术选型决策见 [ADR 索引](docs/adr/README.md)。P1：核心零 agent SDK，CI 强制。里程碑计划（M0-M6）见架构文档 §13。

## 路线图（如实标注未实现项）

已落地（v0.2.0 feat/rebuild-product 重建 + v0.3.x 增量）：核心数据层与原子审收（含高频提交 git 竞态回归防护）/ 确定性 BM25 倒排检索与快照语义 / MCP 五工具与快照钉住（stdio JSON-RPC 端到端实测）/ transcript 挖矿（启发式快速路径 + 增量游标 + recap 候选 + 快速档）/ doctor 接线（claude-code/codex/cursor/gemini-cli，写前备份键级合并幂等）与注入索引 / 五来源导入（幂等指纹 + 相似聚簇 + 冲突建议）/ 视图投影与多设备同步（VERSION 冲突自动取 max）/ 可信评测入口（trust/roundtrip/parity/conflict/budget 套件，规则可判定模型无关）/ 宪法检查全量进 CI（依赖扫描 + 适配器预算）/ LLM 深度提取路径（remin mine --deep：端点可配零 SDK，quote 逐字溯源守卫拒收幻觉候选，弃权语义；v0.4.0，feat/llm-deep-extract）/ 闲时增量 tick（deep 待挖队列 + `remin tick` 排空 + OS 调度器接线 launchd/systemd，无常驻 daemon；feat/idle-tick）。

未实现（下一阶段）：

- transcript 格式适配器扩展（Codex/DSH 日志；当前仅 claude-jsonl）
- 跨 agent 通道实测 parity eval 扩展（当前为 CLI 注入 × MCP 检索双通道；待接入真实第二 agent 会话）
- 端到端任务提升评测与纵向衰减曲线（需真实任务集与长期数据）
- 飞书 / Notion 笔记桥（摄取 human-verified + 单向发布回视图）
- GUI 审收界面（核心已按可包裹设计：CLI 全 `--json`）

## License

[MIT](./LICENSE)
