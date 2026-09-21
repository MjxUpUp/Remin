# HANDOFF — feat/llm-deep-extract（2026-09-21）

> forge task 为真相源（`forge task context` 拉回全部决策/发现/下一步）；本文件是其文本导出视图。
> 前情：v0.2.0 重建（feat/rebuild-product）与 v0.3.x 分发生命周期（feat/lifecycle-distribution、
> fix/release-exec-bit）均已完成合入 main；v0.3.1 已发布（GitHub Release + npm 双侧，安装冒烟过，
> 执行位热修验证生效）。

## 任务状态

- **任务**：LLM 深度提取路径（端点可配，零 SDK）——路线图首项
- **分支**：`feat/llm-deep-extract`（工作提交：0eaeefd 主体 → 27aa9de 审查返工）；**未合入 main**
- **门禁**：✅ task-implement → ✅ task-verify（验收 3/3 实跑）→ ✅ 完成确认 gate →
  ✅ 独立对抗审查（8/10 PASS with P2，P2 已修）→ ✅ doc-review（95 分 round 1 pass）
- **阻塞（唯一）**：hazard 清账——本会话一次 `rm -rf <临时目录>` 被拦（2026-09-21 17:59），
  需用户在终端人工核查后执行 `forge hazard confirm --last`，然后重跑 `forge task complete`

## 交付内容

- `remin mine --deep`：stdlib 零 SDK 直连 OpenAI 兼容端点；config `llm:` 节
  （endpoint/model/timeout_ms，缺省即全系统零 LLM 不变）；密钥只走 `REMIN_LLM_API_KEY`
  （config.yaml 在 git 真源内不落密钥）
- **quote 逐字溯源守卫**（P3-A5）：LLM 候选引文空白归一后必须命中源事件文本，编造即拒收；
  类型白名单外拒收；端点失败弃权（Note 披露）不拖垮快速路径；只产 unverified inbox 候选
  （origin=claude-code·deep，ref 定位事件行号）；hook 路径永不触网
- 审查返工：缺密钥 fail-fast（原会静默弃权且游标推进=深提取机会永久丢失）、
  TimeoutMs 120s 上限、firstLine rune 截断、4 个守卫测试补洞

## 质量证据链（全绿，返工后复跑）

- `go test -race ./...`：21 包全过（新增 21 个测试：extractor 11 / miner 4 / config 4 / cli 1 + drill 严格化）
- `make constitution`：五套件 + 分层白名单依赖扫描 + 适配器预算
- `scripts/e2e-drill.sh`：15 步 exit 0
- 真实二进制 + mock OpenAI 端点 e2e：deep 候选 unverified/origin/ref 行号/quote 溯源/
  编造拒收/未配置与缺密钥错误面，逐项验证通过

## 本会话顺手修的 main 既有缺陷

- e2e-drill step15 断言过期：lifecycle 引入「install 创建的空壳整删」后，fixture 中从零创建的
  `.claude.json`/`.claude/settings.json`/`.codex/config.toml` 卸载后被删净，旧断言仍要求存在——
  main 干净构建复现确认。已修为严格正向断言（空壳必须整删）+ 演练自建二进制（根除陈旧
  二进制假绿——此前 v0.2 时代二进制把过期断言跑绿过）。finding 已登记。

## 台账

- decide ×2（零 SDK + env-only 密钥；只挂手动 mine --deep，hook 永不触网）
- finding ×4：drill 断言过期（已修）；深路径瞬时失败弃权永久性（待闲时 tick，带触发条件）；
  在途暴露面加固（多设备同步启用时复验）；溯源精度两处小瑕疵（下次动 deep.go 顺手）
- intent ×1（test-diff 依据：全部新增测试，无改期望值放水）
- proposal 产物已登记（specs/feat-llm-deep-extract/proposal.md）
- checklist 2/2 全勾

## 下一步

1. 用户终端：`forge hazard confirm --last`（核查被拦的 rm -rf 临时目录清理命令）→ `forge task complete`
2. 合并 `feat/llm-deep-extract` → main（`forge task finish` 或手动 merge；此前的合并均用户确认后进行）
3. 下一阶段路线图（README 如实标注）：闲时增量 tick（挂深路径自动触发 + deep 待挖队列）、
   transcript 适配器扩展（Codex/DSH）、第二 agent parity 实测、端到端任务提升评测、飞书/Notion 桥、GUI
