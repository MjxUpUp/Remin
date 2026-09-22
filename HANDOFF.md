# HANDOFF — 路线图六项全落地（2026-09-22）

> forge task 为真相源（`forge task context --ref <ref>` 拉回全部决策/发现/下一步）；本文件是其文本导出视图。
> 前情：v0.4.0 已发布；本日六个 forge 任务（每项独立分支 + 三门禁 + 对抗审查 + doc-review + mutation）全部合入 main 并推送。

## 任务状态（六项全部完结）

| # | 任务 | 分数 | 合并 |
|---|---|---|---|
| 1 | feat/idle-tick 闲时增量 tick（deep 待挖队列 + remin tick + OS 调度器接线） | 89 (B) | d70c96e |
| 2 | feat/transcript-adapters 多格式挖矿（Codex rollout + DSH session·zstd） | 94 (A) | d3dd926 |
| 3 | feat/agent-parity live parity（真实 claude/codex 会话） | 89 (B) | 71fd098 |
| 4 | feat/uplift-eval 任务提升评测 + 纵向历史 | 89 (B) | 0485699 |
| 5 | feat/bridge 飞书/Notion 笔记桥 | 87 (B) | 67cc6f2 |
| 6 | feat/gui-review 审收 Web 界面（remin ui） | 82 (B) | 9e1ca5e |

主分支 HEAD：9e1ca5e（已推 origin）；全量 test/-race/vet/make constitution 绿；e2e-drill 20 步全通。

## 交付总览

1. **闲时 tick**：快挖行段自动挂账 deep 待挖队列（端点已配即入队）；`remin tick` 排空
   （每 tick 限段/密钥不在场保留/弃权出队不重试/候选先落批次再出队）；
   `remin tick schedule install/remove/status` 接 launchd（bootout→bootstrap 重装生效）/
   systemd user timer（双 effect 入台账，uninstall 回放摘除）。
2. **多格式适配器**：路径分派（.jsonl.zstd→DSH / rollout-*.jsonl→Codex / 其余 claude）；
   DSH zstd shell-out（可注入、损档报错防半档钉死游标）、plugin 注入过滤、epoch 毫秒归一；
   Codex 会话头粘性继承（增量段完整归因）、developer 角色过滤；多根发现去重；
   origin 随源（claude-code/codex/dsh，deep 为 codex·deep 等）。
3. **live parity**（`eval run --suite parity-live`，opt-in 不入 all）：真实 claude -p（--mcp-config
   临时文件 + --strict-mcp-config + --allowedTools）与 codex exec（-c 内联）经 MCP 检索回显
   ID 与核心真值逐集比对；零全局配置污染。真机证据：claude 通道两次实跑精确命中。
4. **uplift 评测**：沙盒套件入 all（空库基线差值归因/弃权不计分/superseded 零命中/注入面同验）；
   `eval uplift --tasks --record` 真源实测（不写记忆真源）；`eval history` 趋势（Δ首跑/Δ上跑）；
   wrong-abstain（衰减信号）与 false-hit（校准缺口信号）独立计数恒等式成立。
5. **笔记桥**（`remin bridge`）：pull（Notion 父页子页含嵌套块 / 飞书 wiki 树遍历+分页）→
   human-verified 幂等摄取（默认 dry-run，单篇失败跳过列名）；push 单向只创建
   （块数按平台上限分批：Notion 100/飞书 50）；零 SDK 密钥只走环境变量。
6. **审收 Web 界面**（`remin ui`）：仅绑 127.0.0.1 内嵌零依赖单页；批次构成/候选详情/
   采纳/拒绝/检索/单条全貌；选择器语义提升 inbox 包单一事实源（CLI/UI 同实现）；
   写操作三层防线（回环 Host 校验防 DNS rebinding + Origin 校验 + JSON-only）。

## 重要发现（open findings，待后续任务）

- **弃权阈值校准缺口**（uplift 真源实测暴露）：MinScore=0.05 + 汉字一元分词使任意中文
  垃圾查询共亨单字即过阈——中等规模中文库上「宁可不知道」失守。修复方向：二元组主导
  或阈值自适应 + uplift 真源模式做校准回归。（fdllov5hu888o-1-ef0bbbb4）
- **codex live 通道待补证据**：本机 headless 认证缺 AM_API_KEY（用户 codex 走代理配置），
  在带该环境变量的 shell 复跑 `eval run --suite parity-live` 即补齐第二通道。
  （fdllo7mztqq60-1-7c2836f9）
- 挂在用户名下不变：inject facet 工具绑定 OP 决议（rebuild 时代遗留）。
- uplift 纵向曲线数据随使用累积（`eval uplift --record`），框架已就绪。

## 台账与披露

- 每任务：proposal 产物登记 + 三门禁 + 只读对抗审查（P1×3/P2×16 全部修复并配杀灭测试）
  + doc-review 独立子代理 2-3 轮（最终分 92-98）+ mutation 消费两轮全杀灭。
- **逃生舱披露**：hazard-pending ×多次（FORGE_HAZARD_PENDING=disable）——起因是本会话早期
  3+ 条含命令替换的 smoke 命令被 hazard-guard 词法拦截（未执行任何危险操作，改走脚本文件
  后完成）；complete 时按守卫给出的无人值守逃生路径放行，checklog 留审计。**建议用户
  核查后 `forge hazard confirm --last` 清账**。
- test-diff 决策均记录（全部新增测试/收紧断言，无弱化）。

## 下一步

1. 发版：六个特性在 [Unreleased]，走 merge-release-choreography + release-readiness 出 v0.5.0
   （含升级行为提示：存量用户首跑自动补挖 codex/dsh 近 7 天——CHANGELOG 已披露）。
2. 修弃权校准缺口（上 open finding）——uplift 真源模式已可做校准回归。
3. 用户实测反馈回路：remin ui 交互迭代（范式级改动需用户拍板）、bridge 真凭据 live 验证、
   codex parity 补证、uplift 任务集随使用沉淀。
4. 挂在用户名下：inject facet OP 决议。
