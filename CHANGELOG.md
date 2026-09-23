# Changelog

本文件记录 Remin（随忆）各版本的用户可感变更。格式参考 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)。

## [0.6.0] — 2026-09-23

### 新增

- **agent headless 深提取引擎（第一优先级）**：探测本机已认证的 agent CLI（claude -p / codex exec
  一次性会话，prompt 走 stdin）作为深度提取引擎——数据不产生新的外流面（transcript 本就是该
  agent 产的）、复用已有订阅额度（企业内合规与额度焦虑友好）；手动 llm 端点降为第二选择。
  两种引擎共用同一系统约束、解析与 **quote 逐字溯源守卫**（守卫引擎无关——agent 的输出同样
  必须逐字溯源到源事件，编造即拒收）；prompt 长文本走 stdin（超 argv 上限）；单次会话 3 分钟
  硬预算（超时杀直系子进程 + 15s WaitDelay 强制关管道——孙进程持 stdout 不得拖过 ~3min15s）。`remin mine --deep` 与闲时 tick 排空均按此序自动解析；`REMIN_DEEP_ENGINE=llm|agent`
  可钉扎引擎（跳过探测，演练/测试确定性）。**零配置即用**：本机装了 claude 或 codex 并登录，
  `--deep` 直接可用。

### 新增

- **记忆管理面**（remin ui 记忆库 tab）：已沉淀记忆的浏览（类型分组+类型色条+trust/facet/
  状态筛选+正文搜索）与全生命周期看板（total/active/superseded/expired/by_type/by_trust/
  verify_failed）；详情抽屉带生命周期操作——**supersede 修改流**（提出新版本→inbox 审收→
  promote 后旧条退出检索，A6 原件不可变下的正确编辑；`propose --supersedes` CLI 同语义）
  与**退休**（status→expired 退出检索，理由进审计，可重新激活；VERSION 推进+BM25 重建
  保证 search/MCP 立即生效）。退休/重新激活走三层写防线+WithRoot+git 提交。

### 修复

- **启发式提取精度**（真实库 61 条候选诊断驱动）：正文改取**触发句**而非消息首句
  （原行为下 24/61 条正文是与触发点完全无关的状态播报）；触发词语境收紧——去掉裸
  「记得」（「我记得…」是回忆叙述）与裸「以后」（时间用法），保留指令形态（记得要/
  以后要等）；「教训」要求总结形态（这条/方法论/教训是），去掉裸 root cause（排查
  叙述）。「记得」保留指令形态、由回忆否决（我记得/不记得/还记得）排除叙述；补「以后…都/要」
  间隔形态；「别忘了」类型保持 procedural；跨句决策表达用句对窗口。真实数据校准：
  61 条噪声批次存活 33 条（含真实根因/方法论教训），噪声 -46%，召回（repo 自有
  指令 fixture 形态）经审查修正后完整保留。

### 新增

- **remin ui v2 交互迭代**（11 项原型确认全数落地）：三栏工作台（批次/候选流/上下文栏，
  窄屏折叠）；顶部状态带（真源点击复制·索引版本·记忆数·深挖待办徽章 hover 明细——接
  tick 留档）；键盘审收流（`j/k` 移动、`a` 采纳、`r` 拒绝、`?` 帮助，输入框聚焦不劫持）；
  候选卡片类型色条+长文截断+来源原话折叠；批次动作条拒绝需二次确认+类型分诊下拉化；
  动作回执带 commit 归因（采纳→vN+短哈希）；检索 trust 色条+弃权可解释（word_match_required/
  low_confidence/no_match 各配解释文案）；单条全貌抽屉（supersession 链时间线+verify）；
  审收趋势迷你图（`eval/history` 数据面，wrong-abstain 警示色）；亮/暗双主题（跟随系统+
  手动记忆）。修复三个 v0 静默缺陷：`/api/batch` 候选字段 snake_case 投影（内嵌结构 Go
  字段名直出导致单条按钮失效）、`/api/reject` 回执形状（rejected/commit）、`/api/memory` supersession 链键名语义修正（supersedes/superseded_by，v0 反写潜伏未发）。

## [0.5.0] — 2026-09-22

### 新增

- **多格式 transcript 适配器**：`remin mine` / `remin tick` 现挖三格式的会话日志——
  Claude Code JSONL（原有）+ **Codex rollout**（`~/.codex/sessions`，response_item/message 形态）
  + **DSH session**（`~/.dsh/sessions`，zstd 压缩 JSONL；plugin 注入噪声过滤，epoch 毫秒时间戳
  归一）。多根发现（`REMIN_TRANSCRIPT_ROOTS` 同源扩展，各根 env 可覆盖）、跨根去重、
  增量游标与 deep 待挖队列对三格式一致。候选 `provenance.origin` 随源标注
  （claude-code/codex/dsh，深路径 codex·deep 等），命令回显/系统提醒噪声沿用过滤。
  升级提示：装过 tick 调度的存量用户升级后，首次 mine/tick 会自动补挖 Codex/DSH 近 7 天
  会话（`--since` 默认窗口内）；不挖更早历史需显式 `--full-history`，关闭某根把
  `REMIN_CODEX_DIR`/`REMIN_DSH_DIR` 指到空目录即可。
- **审收 Web 界面**（`remin ui`）：本地审收 GUI（仅绑 127.0.0.1，--port/--no-open 可调）——
  批次列表（构成统计）/候选详情/采纳/拒绝（全批·按类型·单条，与 CLI 同选择器语义——
  选择器实现提升为 inbox 包单一事实源）/检索（trust 随行，弃权如实展示）/单条全貌。
  零前端依赖内嵌单页（go:embed）；写操作三层防线：回环 Host 校验（防 DNS rebinding）+ Origin 校验 + application/json-only（跨站表单无法伪造）；
  GUI 只是皮肤，不引入新核心能力（架构预留兑现）。
- **飞书 / Notion 笔记桥**（`remin bridge`）：`pull --from notion|feishu` 把平台笔记
  （Notion 父页子页 / 飞书 wiki 文档）经 markdown 暂存送入 importer **human-verified** 通道
  （幂等指纹，默认 dry-run）；`push --to notion|feishu` 把记忆视图单向发布为新页面
  （heading/段落/列表块映射；只创建、永不回写——视图是真源派生物）。零 SDK（stdlib 直连
  官方 REST），密钥只走环境变量（REMIN_NOTION_TOKEN / REMIN_FEISHU_APP_ID+SECRET），
  目标 ID 配置在 config `bridge:` 节（非密钥，随真源 git）。可靠性如实处理：拉取分页拉全
  （飞书含 wiki 树遍历），推送块数按平台上限分批（Notion 100/批、飞书 50/批），
  单篇失败跳过并逐名列出（不静默少拉），`--json` 契约含 applied/pulled/skipped/report。
- **任务提升评测（uplift）**：`eval run --suite uplift`（入 all）——同一任务集「空库基线
  vs 带记忆」差值归因记忆贡献（recall/弃权不计分/superseded 零命中/注入面同验，全规则可判定）；
  `remin eval uplift --tasks <file> [--record]` 真源实测模式（自备任务集 JSONL，只读检索面）；
  `remin eval history` 趋势（各次 recall + 相对首跑/上跑 Δ）——纵向衰减曲线的数据面，
  随真实使用逐次累积；wrong-abstain（期望命中却弃权）作为衰减信号一等公民如实计数。
- **live parity 评测**（`remin eval run --suite parity-live`，opt-in 不入 all）：接入真实
  第二 agent 会话——`claude -p`（--mcp-config 临时文件）与 `codex exec`（-c 内联）各自经
  MCP 通道调 memory_search 并逐字回显命中 ID，与核心确定性检索真值逐集比对；双通道在场时
  另验彼此一致。通道构造不碰用户全局配置。真实 agent 调用有成本，按需运行。
- **闲时增量 tick**（`remin tick`）：OS 调度器按档拉起的一次性增量维护（无常驻 daemon）——
  增量快挖 + deep 待挖队列排空。快速路径挖过、配置了 LLM 端点的行段自动挂账待挖队列；
  `remin tick` 每 tick 限段深挖（默认 3 段），密钥不在场保留队列，端点失败弃权出队不重试；
  手动 `mine --deep` 深挖过的范围自动出队不重复计费；深挖候选与既有 inbox 候选跨批次去重。
- **OS 调度器接线**（`remin tick schedule install [--every 4h] / remove / status`）：
  macOS launchd agent / Linux systemd user timer；间隔下限 15 分钟；effect 挂接线台账，
  `remin uninstall` 按台账回放摘除；加载 best-effort（失败给手工指引，文件已落位）。

### 修复

- **弃权校准**（uplift 真源实测暴露）：垃圾填充查询（如「完全无关的查询 zzxxqq」）此前
  仅靠高频汉字散字碰撞即可越过低分阈值返回命中——中等规模中文库上「宁可不知道」失守。
  现新增确定性词级重合判据：最高命中与查询须至少共享一个词级 term（汉字二元组或 ≥2 字
  西文词），仅散字重合即弃权（新 reason `word_match_required`）；纯单字查询同判弃权。
  实验证实分数阈值原理上无法分离长垃圾查询与简短合法查询（区间重叠），词级判据与库规模
  无关且零模型（P2-H6 确定性不受影响）；简短合法查询（共享词级）不受影响。

## [0.4.0] — 2026-09-22

### 新增

- **LLM 深度提取路径**（`remin mine --deep`）：OpenAI 兼容端点可配（config `llm:` 节，零 SDK），
  语义提取补充启发式快速路径的召回。宪法约束不放松——LLM 候选的引文必须逐字溯源到源会话
  文本（空白归一），对不上即拒收（宁可不知道，不能自信地错）；端点失败弃权不拖垮快速路径；
  产出仍为 unverified 候选等人审；hook 路径永不触网。密钥只走环境变量 `REMIN_LLM_API_KEY`
  （config.yaml 在 git 真源内，不落密钥）。
- **`remin init` 交互式向导**（终端环境）：真源路径 → git 身份检查 → 审收自治档位 →
  agent 接线 → 备份私有远端，收尾打印日常闭环。脚本/非终端环境（含 `--defaults`）行为与旧版
  逐字节一致。
- **首挖限量**：`remin mine` 默认只挖 mtime 近 7 天的 transcript（防首日历史 recap 洪泛）；
  `--full-history` 显式开时间窗补挖更早，`--force` 重置游标全量重挖。
- **类型分诊**：`remin promote / reject / inbox` 支持 `--type`（如
  `reject --batch <id> --all --type episodic` 一键清 recap）。
- **inbox 行动引导**：待审批次显示构成统计（如 `episodic 862 · procedural 47`）与
  查看/采纳/拒绝命令指引。
- **触点真源透明**：mine/inbox/promote/reject/search/status 输出尾部统一显示真源路径
  （`真源: ~/.remin`）。
- **sync 私有仓防呆**：`remin sync --set-remote` 需确认远端为私有仓库（终端交互 y/n；
  非终端环境必须显式 `--yes`）。

### 修复

- e2e 演练（`scripts/e2e-drill.sh`）卸载断言过期：lifecycle 的「install 创建空壳整删」语义
  上线后 fixture 文件被正确删净而断言仍要求存在——main 上即挂，此前被陈旧二进制掩盖。
  演练现自建二进制（根除陈旧二进制假绿）。

### 行为变更

- `remin mine` 默认挖矿范围从「全部历史」变为「近 7 天」（`--full-history` 恢复旧行为）。
  其余命令在非终端环境行为不变。

## [0.3.1] — 2026-09-21

### 修复

- npm 平台包二进制执行位丢失（upload-artifact@v4 不保留 POSIX mode）：CI 组装时 chmod
  归位 + launcher chmod 兜底 + Release 资产恢复 755。

## [0.3.0] — 2026-09-21

### 新增

- 产品化生命周期：npm（@reminmem/remin）五平台分发、`remin doctor --install` 落位
  `~/.remin/bin` + 接线台账、`remin upgrade` 原位原子替换、`remin uninstall` 台账回放摘净
  （[ADR-0008](docs/adr/0008-distribution-lifecycle.md)）。

## [0.2.0] — 2026-09-21

### 新增

- 从零重建完整产品：核心数据层与原子审收、确定性 BM25 检索、MCP 五工具、transcript 挖矿
  （启发式快速路径）、doctor 四 agent 接线、五来源导入、视图投影与多设备同步、可信评测套件
  （trust/roundtrip/parity/conflict/budget）、宪法检查进 CI。
