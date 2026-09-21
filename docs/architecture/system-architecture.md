# Remin 系统架构设计（v1.1）

> 本文是《Remin（随忆）技术设计方案 v1.0》（飞书）在仓库内的落地版：技术选型定案（见 [ADR 索引](../adr/README.md)）、组件与包结构、接口契约、前端交互设计与 M0-M6 实施切片。
> 上位文档：[PRINCIPLES.md](../../PRINCIPLES.md)（宪法，冲突时以宪法为准）· [记忆格式 spec v0](../spec/memory-format-v0.md)。

## 1. 设计目标与硬约束

| 宪法原则 | 翻译为硬约束 |
|---|---|
| P1 Agent 中立 | 核心零 agent SDK（CI 依赖扫描强制）；适配器总代码 ≤20%、单个 ≤8%；能力先在协议层（CLI/MCP）可用 |
| P2 换脑不换忆 | 唯一真源 + 视图投影；检索主权在核心；export/import roundtrip 哈希一致 |
| P3 极高准确性 | 一切新记忆过 inbox，promote 是唯一盖章入口（原子 git 提交）；冲突显式 supersession；trust/provenance 随行；低置信弃权 |
| P4 记忆比智能活得久 | markdown+frontmatter 语义自包含；索引/视图全部可重建；六类型 schema 第一天定义全集 |

## 2. 分层架构

```text
边缘层（adapters/，当前为零适配器代码：doctor 写的是直调 remin CLI 的各 agent 全局配置）
  SessionStart hook → remin inject   （开场追赶[硬预算] + 精简索引注入，永不失败）
  Stop hook         → remin hook-stop（transcript 入队，按路径幂等去重，尽力异步触发）
        |  仅事件搬运与配置接管，无策略
协议层（internal/protocol/，语言中立，核心主权）
  MCP stdio server（别名 memory，五工具，无 memory_write）
  CLI（cmd/remin → internal/cli，全命令 --json）
        |  同一套核心 API
核心层（internal/，零 agent SDK，CI 强制）
  Miner → Extractor → Inbox → Promotion（原子提交）
  Supersession 引擎 / Verifier（verify-condition 用前验证）
  Search（版本化确定性 BM25）/ Injector / Importer / Exporter / Eval / Doctor
  Store：~/.remin/ 个人 git 仓库（唯一真源）
```

## 3. 包结构（三层对应）

```text
cmd/remin/            入口：装配 cobra 命令
internal/cli/         CLI 命令面（人面；每命令 --json）
internal/protocol/    MCP stdio server（机面；官方 Go SDK，仅此层可依赖它）
internal/store/       真源仓库：git 封装、布局、记忆文件读写、frontmatter 解析
internal/core/        领域核心：inbox/promotion（含 supersession 引擎）/miner/extractor/
                      verify/importer/exporter/eval/doctor/view/inject/config/audit/syncpkg
internal/index/       版本化 BM25 索引（可重建加速层）
internal/search/      检索引擎：BM25 查询、快照过滤、弃权判定
adapters/             边缘层占位（零代码 + README 说明）
docs/                 spec / architecture / adr
scripts/              宪法 CI 检查（依赖扫描、适配器预算）
```

依赖方向单向：`cli/protocol → core → store/index/search`。`store/index/search` 只依赖标准库 + yaml。

## 4. 数据设计

### 4.1 存储布局（~/.remin/）

```text
~/.remin/
  memory/<type>/<id>.md    记忆真源（markdown+frontmatter，入 git）
  inbox/                   候选与批次台账（candidate 文件 + batch 清单 + imports 幂等指纹，入 git）
  audit/                   审收记录（promotions.jsonl / rejections.jsonl，入 git）
  index/VERSION            索引版本号（单调递增，入 git；promotion 事务 +1）
  index/bm25-<ver>.json    BM25 索引数据（可重建，不入 git）
  views/                   生成视图（可重建，不入 git）
  transcripts-cache/       挖矿队列 queue.jsonl、增量游标 cursors.json、指纹（不入 git）
  config.yaml              facet 定义、自治档位、工具绑定、同步远端（入 git）
```

`.gitignore` 随 `init` 生成：忽略 `index/bm25-*`、`views/`、`transcripts-cache/`。

### 4.2 记忆文件格式

见 [spec v0](../spec/memory-format-v0.md)（16 frontmatter 字段、状态机、七条不变量）。

### 4.3 索引与版本语义

- `index/VERSION` 单调整数；每次 promotion 原子提交 +1；restore 取 max(本地, bundle)
- 快照 = 版本 + facet + 过滤参数；同版本 + 同查询 → 同结果（确定性 BM25，固定分词与参数，按 id 稳定排序）。
  注：ephemeral 过期判定依赖当前墙钟（过滤参数的一部分）——同版本同查询在
  跨越过期边界的两个时刻结果可不同，属快照定义内行为而非 H6 破坏
- 一切注入/检索结果携带 `index_version`（可评测与重放的落点）
- 索引损坏/缺失：由真源全量重建，用户无感

### 4.4 注入索引格式（SessionStart）

> 实现现状（v0.2.0）：hook 入口（`remin inject`）无法得知调用方 agent 身份，
> facet 取 `config.yaml` 的 `inject_facet`（默认 dev），`--facet` 可覆盖；
> 按工具绑定的 facet 分发待开放问题 OP 决议（需 agent 身份信令）。

生成规则：facet 过滤（按工具绑定）→ 状态过滤（active、未过期、verify 通过）→ 重要性排序（类型权重 × recency × 检索命中）→ **约 200 行预算**截断。每条一行：`id · 类型 · 一句话摘要 · trust 标记`；详情由 agent 经 MCP 按需拉取。尾部附 recap 候选提示（一键采纳指引 = `remin promote --batch <id>`）。

## 5. 核心流程

### 5.1 捕获流水线（FR-CAP）

```text
Stop hook / 日志落盘 → queue.jsonl（按 transcript 路径幂等去重）
  → Miner 解析（claude-jsonl 适配器，增量游标 cursors.json）
  → Extractor 快速路径（启发式，零 LLM，硬预算内）→ inbox 批次（trust=unverified）
  → 人审 promote（原子提交）
触发点：会话结束（Stop 入队，agent 零延迟）/ 开场追赶（inject 内，硬预算 800ms，超时降级）
        / 手动 remin mine（全量重挖，兼审计）
```

### 5.2 可见性契约（快照隔离）

- 提升是单个 git commit：新记忆 + supersedes/superseded_by + index/VERSION +1 + audit 追加 + inbox 台账更新，任一步失败整体回滚（含暂存区）
- 版本推进只有两个入口：会话边界（自动）与 `remin refresh`（可归因）
- MCP server 在 initialize 时钉住当前版本，会话内检索服务该快照；`memory_refresh` 显式推进

### 5.3 检索（JIT）与弃权

```text
query → 快照版本定位 → facet/trust 过滤 → 确定性 BM25
      → verify 条目用前验证（结果改变检索真值则原子回写并版本 +1）
      → {id, content, trust, provenance, verify_result, index_version}
      → 最高分低于阈值 / 无命中：abstain（宁可不知道，绝不模糊返回）
```

## 6. 接口设计

### 6.1 CLI 命令面（全命令 --json；无未声明的交互式副作用）

| 命令 | 关键 flags | 行为 |
|---|---|---|
| `remin init` | `--root` | 创建真源 git 仓库（目录骨架、.gitignore、config.yaml、VERSION=0、首提交） |
| `remin doctor` | `--install` `--takeover` | 检测已装 agent / 一键接线（写前备份）/ 健康检查 / 接管同名 memory server |
| `remin propose` | `--type --facet --context --origin --ref --quote [--ephemeral] [--verify-condition]` | 显式记忆提案 → inbox（human 面通道） |
| `remin mine` | `--dry-run` `--force` `--from-queue` | transcript 挖矿（claude-jsonl；增量断点续挖；--force 全量重挖） |
| `remin import` | `--from --path --apply` | 五来源迁移；默认 dry-run 分析报告；--apply 生成 inbox 批次；幂等只报增量 |
| `remin inbox` | `--batch` | 审收视图（批次内自动分组：冲突建议排前、重复簇、普通） |
| `remin promote` | `--batch <id> --all / --id <id>... / --except` | 人审采纳：单一原子提交 |
| `remin reject` | `--batch <id> --all / --id <id>...` | 拒绝归档（audit 可查） |
| `remin search` | `--facet --top-k --json` | 确定性 BM25（trust/provenance/index_version 随行；低置信 abstain） |
| `remin status` | `<id>` | 单条全貌（supersession 链、时间戳、verify） |
| `remin log` | `--batch --limit` | 审收审计历史（谁/何时/采纳了什么） |
| `remin verify` | `<id> / all`、`--set passed\|failed` | verify-condition 用前验证（原子回写；改变检索真值则版本 +1）；自然语言条件经 --set 人判 |
| `remin refresh` | | 显示当前快照版本（MCP 会话内用 memory_refresh 推进） |
| `remin inject` | `--facet --budget-ms` | hook 入口：排空队列（硬预算）→ 快照 → 注入索引 + recap 提示；永不失败 |
| `remin view` | `--write <path>` | AGENTS.md 形态投影（默认预览 stdout；显式 opt-in 才落盘） |
| `remin export` / `restore` | `--out <dir>` / `--bundle <dir>` | 全量导出（sha256 清单）/ 整库还原（roundtrip 哈希一致；唯一绕过 inbox 的通道——还原的是已人审的库） |
| `remin sync` | `--set-remote <url>` `--push/--pull` | git push/pull 包装（远端仅托管；VERSION 冲突取 max） |
| `remin eval run` | `--suite trust/roundtrip/all --out` | 评测套件（规则可判定、模型无关），JSON 报告 |
| `remin mcp` | | MCP stdio server（客户端拉起） |
| `remin hook-stop` | | Stop hook 入口：stdin JSON / argv 双源收 transcript 路径 → 入队 → 尽力异步触发挖矿 |
| `remin version` | | 版本与真源状态 |

### 6.2 MCP 工具协议（stdio；server 别名 `memory`）

| 工具 | 入参 | 出参语义 |
|---|---|---|
| `memory_search` | `{query, facet?, top_k?}` | `{results:[{id,content,trust,provenance,verify_result}], index_version, abstained?}`；服务会话快照；置信不足 abstain |
| `memory_propose` | `{type, content, context?, facet?}` | `{proposal_id}`；进 inbox 标 agent-claimed，永不自动升级 |
| `memory_verify` | `{id}` | `{result, evidence}`；执行用前验证 |
| `memory_status` | `{id}` | `{status, supersedes 链, 时间戳}`；审计与冲突查询 |
| `memory_refresh` | `{}` | 会话快照推进至最新版本 |

**明确不存在 `memory_write`**：agent 无直写通道——它只能提案，人审才生效。接管官方 memory server 时语义兼容（搜索/记忆类调用映射到 search/propose）。

### 6.3 doctor 配置接管映射（全部写用户主目录，repo 零污染）

| Agent | 写入位置 | 内容 |
|---|---|---|
| claude-code | `~/.claude.json` + `~/.claude/settings.json` | mcpServers.memory（command=remin 绝对路径）；SessionStart/Stop 两会话级 hook |
| codex | `~/.codex/config.toml` | `[mcp_servers.memory]` |
| cursor | `~/.cursor/mcp.json` | mcpServers.memory |
| gemini-cli | `~/.gemini/settings.json` | mcpServers.memory |

写前备份（`<file>.remin-backup-<ts>`），JSON 键级合并（只增不删），TOML 段级替换；`--takeover` 替换同名 `memory` server 条目。

## 7. Import 引擎

统一契约：`discover`（本机只读扫描）→ `read`（逐条）→ `normalize`（标准候选 + provenance + 通道信任）。

| 适配器 | 来源 | 信任 | 备注 |
|---|---|---|---|
| claude-auto-memory | `~/.claude/projects/*/memory/*.md` | agent-claimed | MEMORY.md 按段落拆分 |
| claude-mem | SQLite（`~/.claude-mem/` 下 `*.db`） | agent-claimed | shell out `sqlite3`（系统自带；缺失则该来源降级提示） |
| chatgpt-export | 导出 JSON（`--path` 指定） | agent-claimed | 兼容 string 数组 / 对象数组两种形态 |
| codex-memories | `~/.codex/memories/*.md` | agent-claimed | |
| markdown-dir | `--path` 用户目录 | human-verified（人写的字） | provenance 注明「用户亲笔·来源·日期」 |

幂等指纹 = `source + 原始ID(缺失用内容哈希)`，记于 `inbox/imports.jsonl`；重复导入默认跳过只报增量。重复检测：BM25 自检索 + 阈值聚簇。冲突建议：同主题新旧两条且均有 captured_at → 时间新者 supersede 旧者，**作为建议呈现**；无时间证据并排呈现由人裁（OP-4）。永不覆盖：import 只追加 + supersede 提案。

## 8. 前端交互设计

### 8.1 设计原则

- **卖记忆不卖管道**（FR-UI-4）：MCP、git、frontmatter 全是内部器官，用户可见面只有「记了什么、可信吗、从哪来」
- **可被 GUI 包裹**（FR-UI-3）：子命令语义干净、全命令 `--json`、状态全部落盘可查询、CLI 与 GUI 同一核心二进制
- **无未声明的交互式副作用**：任何命令不弹隐藏提问；破坏性动作（restore/接管）要求显式 flag

### 8.2 CLI 交互规范

| 面 | 规范 |
|---|---|
| 默认输出 | 人类可读（分组表格：批次→组→条目；颜色仅 trust/状态标记） |
| `--json` | 全命令结构化输出（`{ok, data|error}` 包络），GUI/脚本唯一契约 |
| 退出码 | 0 成功；1 业务失败；2 用法错误；inject/hook-stop 永不非零（hook 安全） |
| 审收动线 | `inbox`（看）→ `promote --batch <id> --all/--id/--except`（批）→ `log`（审计回看） |
| 提案动线 | `propose`（现在就记）→ inbox → promote |
| 安全措辞 | trust 三级以中文标签随行（人审/agent 断言/未验证），冲突条目并列呈现禁止系统代裁 |

### 8.3 MCP 协议 UX（agent 面）

- 工具描述即行为契约：每个工具 description 写明「提案不落库、需人审」语义，防 agent 误用
- `memory_search` 返回自带 trust/provenance/verify_result 字段与 `index_version`——agent 必须知道自己吃到的是哪级记忆
- abstain 是显式字段而非空数组（空数组=没有相关记忆；abstained=true=置信不足，建议向用户确认）

### 8.4 GUI（下一阶段，OP-6 待定）

核心已按可包裹设计：inbox 审收界面 = `inbox --json` + `promote/reject --json`；一键接线 = `doctor --install --json`；内嵌 MCP = 拉起 `remin mcp`。GUI 只是皮肤，不引入新核心能力。

## 9. Eval Harness（模型无关，规则可判定）

| 套件 | 断言 |
|---|---|
| trust（可信四指标） | stale 注入率=0；supersession 后旧事实注入率=0；provenance 覆盖率=100%；弃权正确（垃圾查询必 abstain）；trust 分层随行 |
| roundtrip | export→restore→export 三点哈希一致；supersession 历史与 audit 完整往返 |
| parity（宪法） | 同一 repo fixture：CLI inject 产物与 MCP memory_search 结果内容对等 |
| conflict（宪法） | Ronaldo→Messi 事实变更集：旧事实注入率=0 |
| budget（宪法） | SessionStart 注入含追赶 ≤ 800ms 硬预算（fixture 实测）；JIT 检索延迟测量待扩 |

`remin eval run` 输出 JSON 报告；`make constitution` = 宪法五套件（trust 内含 provenance 覆盖率审计 / roundtrip / parity / conflict / budget）+ 依赖扫描 + 适配器预算；CI 接线后阻断合并。

## 10. 安全与隐私

- 全部数据与计算本地；同步仅经用户自选 git 远端；零遥测
- 投毒防护三层：入口全审收（无直写）；trust 永不自动升级；provenance+audit 全链路可追责
- agent 权限最小化：MCP 窄接口（检索/提案/验证/状态），无 shell、无直写、无配置修改
- hook 输入按不可信外部数据处理（hook-stop 对 stdin JSON 只提取已知字段，路径校验前缀必须在 agent 日志目录白名单内）

## 11. 技术选型（决策记录见 [ADR](../adr/README.md)）

| 项 | 选型 | ADR |
|---|---|---|
| 核心语言 | Go 1.26（单二进制：CLI 与 MCP server 同体） | [0001](../adr/0001-go-as-core-language.md) |
| 检索 | 内嵌确定性 BM25，零向量库 | [0002](../adr/0002-embedded-bm25.md) |
| git | shell out 到 git（保真原子提交语义） | [0003](../adr/0003-git-via-shellout.md) |
| MCP | 官方 Go SDK，仅协议层 | [0004](../adr/0004-mcp-official-sdk.md) |
| CLI | cobra；全命令 --json | [0005](../adr/0005-cobra-cli-json.md) |
| 边缘 | 零适配器代码（doctor 写全局配置直调 CLI） | [0006](../adr/0006-zero-adapter-edge.md) |
| claude-mem SQLite | shell out sqlite3（系统自带），零 CGo 依赖 | [0007](../adr/0007-sqlite-shellout.md) |
| LLM | 仅 Extractor 深度路径（端点可配）；快速路径零 LLM | 设计文档 §技术选型 |

## 12. 宪法映射（验收清单）

| 不变量 | 落点 | 验证 |
|---|---|---|
| N1 核心零 SDK | `internal/{core,store,index,search,cli}` 依赖白名单 | `make constitution` 依赖扫描 |
| N2 适配器预算 | `adapters/` 零代码 | `make adapter-budget` |
| A1 无来源不落库 | frontmatter 必填校验 + provenance 必填 | provenance audit 测试 |
| A2 冲突不共存 | promotion 事务 supersedes 成对写入 | conflict regression 测试 |
| A3 信任分层 | trust 字段 + 检索结果随行 | trust 套件 |
| H4 导出即完整 | export/restore + manifest | roundtrip 套件 |
| H6 确定性检索 | 固定分词/参数/排序 + 快照版本 | parity + 重复查询一致测试 |
| H5 内容对等 | Injector 唯一生成注入产物 | parity 测试 |

## 13. 里程碑切片（M0-M6）

每片完成即 `go test ./...` 全绿 + 对应宪法项可用；片内顺序 TDD。

| 切片 | 范围 | 完成判据 |
|---|---|---|
| M0 骨架 | go.mod、三层目录、Makefile、宪法检查脚本、ADR/spec 落库 | `make build` 出二进制；`make constitution` 依赖扫描过 |
| M1 数据层 | store（git/markdown/frontmatter）、init、propose、inbox、promote/reject 原子提交、supersession、audit | 原子提交崩溃回滚测试；幂等；台账一致 |
| M2 检索层 | 版本化 BM25、search/status/log/refresh、abstention、verify | 确定性（同版本同查询同结果）测试；stale=0；版本快照语义测试 |
| M3 协议层 | MCP 五工具（快照钉住）、inject（追赶+注入+预算降级）、view | MCP stdio 端到端测试（JSON-RPC 实跑）；inject 永不失败测试 |
| M4 挖矿 | claude-jsonl Miner、hook-stop 队列、启发式 Extractor、增量游标 | fixture 会话日志挖出候选且 provenance 指向行号；断点续挖；hook 幂等 |
| M5 迁移 | 五来源 importer、export/restore、sync | roundtrip 哈希一致；幂等只报增量；冲突建议呈现 |
| M6 接线评测 | doctor 四 agent、eval trust/roundtrip、README 修订 | doctor 在 fixture HOME 实测接线（备份+合并）；`make constitution` 全绿 |

## 14. 风险与对策

| 风险 | 对策 |
|---|---|
| transcript 格式漂移 | 格式适配器版本化 + doctor 兼容检查；失败降级注入-only |
| 平台收编加速 | 押注结构性空位：跨厂商中立 + 所有权 + 审计；格式 spec 卡位 |
| 性能回归 | budget 进 CI（eval budget 套件）；追赶超时降级异步 |
| hook 环境异常 | inject/hook-stop 永不非零退出、永不阻塞（超时自杀保护） |
