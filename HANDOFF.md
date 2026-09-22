# HANDOFF — v0.5.0 已发版（2026-09-22）

> forge task 为真相源（`forge task context --ref <ref>` 拉回全部决策/发现/下一步）；本文件是其文本导出视图。
> 前情：路线图六项（idle-tick/adapters/parity-live/uplift/bridge/gui）+ 弃权校准修复，全部经 forge 三门禁 + 对抗审查 + doc-review 合入 main。

## 发版结果（v0.5.0）

- **npm 六包官方源核实**（版本化端点）：main + 五平台包全部 0.5.0，latest 已切。
- **GitHub Release**：https://github.com/MjxUpUp/Remin/releases/tag/v0.5.0（11 资产；notes 含升级提示/回滚配方/已知事项）。
- **readiness**：双只读审计——风险面 M1/M2/M5/M6 全 PASS（GO-WITH-RISK：M6 文档一致性无自动化守卫为强制注记）；运行面 M3/M4/M7/R1 全 PASS（体积 +6.4%、零新依赖、v0.4.0↔v0.5.0 双向兼容实测、回滚 runbook）。
- **装机验证**：真实 npm 安装路径全命令面通过（search/弃权词级判据/tick/eval all/uplift+record/history/bridge dry-run）；用户 `~/.remin/bin` 已升级 v0.5.0（下次会话生效）。

## 发版过程中的真实事故与修复（重要前车之鉴）

1. **main Linux CI 连红未察觉**（gui-review 合并起 4 次 push 全红）：systemd 调度测试断言
   与台账守卫按 darwin 形态写——timer 文件不含 bin 路径，守卫把我们自己的 timer 误判为
   「用户改过」拒绝摘除。task-1 审查 P1#2 曾预警「linux CI 会红」，当时的修复只覆盖了
   status 排序——**不完整修复 + 合并前未核查分支 CI** 双重失误。修复（feat/sched-linux-ci，
   89/B）：守卫标记按文件真实内容（service=bin、timer=Unit= 行）+ 断言/调用序平台化；
   真实 Linux 容器（非 root）验证后走分支 CI 绿才合并。main CI 已恢复绿。
   注意：constitution.yml 触发器仅 main + feat/**——**fix/ 分支没有 CI，别用**。
2. **孤儿 tag 处置**：首次 v0.5.0 tag 在红线 main 上，release workflow 41s 失败（未产任何
   Release/npm 资产，零暴露）。核实无 Release 对象、npm latest 仍 0.4.0 后删除重打。
   若已有任何发布产物则绝不可重打（tag 不可变规则的用户缓存面）。

## 本日任务台账（全部完结）

| 任务 | 分数 | 合并 |
|---|---|---|
| feat/idle-tick | 89 (B) | d70c96e |
| feat/transcript-adapters | 94 (A) | d3dd926 |
| feat/agent-parity | 89 (B) | 71fd098 |
| feat/uplift-eval | 89 (B) | 0485699 |
| feat/bridge | 87 (B) | 67cc6f2 |
| feat/gui-review | 82 (B) | 9e1ca5e |
| fix/abstain-calibration（弃权词级判据，mutation 6/6） | 89 (B) | 28a6547 |
| feat/sched-linux-ci（Linux CI 红线清除） | 89 (B) | 修复后主线 |

## open findings / 挂在用户名下

- **codex live parity 补证**（fdllo7mztqq60-1-7c2836f9）：本机 headless 缺 AM_API_KEY（用户
  codex 走代理配置）；在带该环境变量的终端跑 `remin eval run --suite parity-live` 即补齐
  第二通道。claude 通道已三次真机精确命中。
- **inject facet 工具绑定 OP 决议**（rebuild 遗留，一直挂在用户名下）。
- M6 文档一致性守卫：下次发布前建议建 docs-consistency-guard 自动化测试（本次 GO-WITH-RISK 注记）。
- hazard 台账待用户核查清账（`forge hazard confirm --last`）。

## 下一步（无待执行代码任务——推到无可做位置）

1. 用户实测反馈回路：`remin ui` 交互迭代（范式级需用户拍板）、bridge 真凭据 live 验证、
   uplift 任务集随使用沉淀（`eval uplift --record` 逐次累积衰减曲线）。
2. 可选增强（无阻塞）：M6 文档守卫自动化；`remin upgrade` 版本钉扎（回滚更顺）；
   深路径 LLM 自动触发实测（`tick schedule install` 已可装）。
