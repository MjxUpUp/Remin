# HANDOFF — feat/onboarding-ux（2026-09-22）

> forge task 为真相源（`forge task context` 拉回全部决策/发现/下一步）；本文件是其文本导出视图。
> 前情：v0.3.1 已发布；LLM 深度提取路径（feat/llm-deep-extract，94 分 A）已合入 main（b6c6481）并推送。

## 任务状态

- **任务**：新用户引导与审收体验（feat/onboarding-ux）——**95 分（A）完结**
- **分支**：已合入 main（a89268f，--no-ff）并推送 origin（27ec68d..a89268f）
- **门禁**：三门禁 + review gate（对抗 7.5/10，P2×3 已返工）+ doc-review 四轮（95/93/96/96）+
  mutation 6/6 全杀灭 + race 21 包 + constitution + e2e-drill 15 步零向导泄漏

## 交付内容（全部来自真实首用反馈）

1. **remin init 交互式向导**（TTY）：真源路径 → git 身份检查（前置，缺失给指引退出）→
   自治档位 → agent 接线 → 备份私有远端（确认私有才放行）；收尾打印日常闭环。
   零新依赖（bufio.Scanner + 输入注入可测）；非 TTY / `--defaults` / 零输入 EOF 三态
   回落直通路径**含错误路径**逐字节兼容（向导输出走缓冲，EOF 即丢弃）
2. **mine 首挖默认限量**：`--since 7`（mtime 窗口）；`--full-history` 仅开时间窗补挖更早；
   `--force` 重置游标重挖；queue 路径不受限；跳过计数如实输出
3. **触点真源透明**：mine/inbox/promote/reject/search/status 尾部统一 `真源: ~/.remin`
4. **inbox 行动引导**：open 批次构成统计（episodic 862 · procedural 47 …）+ 查看/采纳/拒绝三行指引
5. **类型分诊**：promote/reject/inbox `--type`（inbox 配 --batch 详情视图；未知类型早失败；--id 互斥显式报错）
6. **sync --set-remote 私有仓防呆**：TTY y/n（提示走 stderr）；非 TTY 必须 `--yes`

## 用户真源实测（2026-09-22）

- 923 条批次实测构成：862 recap（93%）+ 61 真候选；`reject --batch mine-20260922-01
  --all --type episodic` 一键归档 862 条（commit 1d03367，git 历史可回溯），923→61 真信号
- pty 真实终端双场景向导 e2e：全默认 / 快速档+私有远端确认，均通过

## 台账

- decide ×3（向导零依赖/限量产品立场/sync 防呆只在 CLI 面）
- finding ×1（架构 §6.1 命令表滞后，存量项，下次动该文件时顺手）
- intent ×1（test-diff：全部新增测试）；checklist 2/2；proposal 已登记
- mutation 首次消费：6/6 全杀灭（存活位点 stdinIsTTY 判据已补杀灭测试 b355439）

## v0.4.0 已发版（2026-09-22）

- 流程：merge-release-choreography 8 站走完——双只读审计（M1/M2/M5 ✅ + M3/M4/M7/R1 ✅，
  M6 判人工核对级 → **GO-WITH-RISK**）→ tag v0.4.0 → release workflow 2m7s 成功 →
  npm 六包官方源核实（CDN 滞后需查版本化端点）→ Release notes 补回滚配方与已知事项 →
  分支清理 → 真实安装验证（v0.4.0 + 新 flag 面 + 执行位）→ 用户 ~/.remin/bin 已 upgrade
- GitHub Release：https://github.com/MjxUpUp/Remin/releases/tag/v0.4.0（11 资产）

## 下一步
2. 路线图：闲时增量 tick（挂深路径自动触发 + deep 待挖队列）、Codex/DSH transcript 适配器、
   第二 agent parity、端到端任务提升评测、飞书/Notion 桥、GUI
3. 挂在用户名下：inject facet 工具绑定 OP 决议（rebuild 时代遗留 open finding）
