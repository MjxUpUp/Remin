# Changelog

本文件记录 Remin（随忆）各版本的用户可感变更。格式参考 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)。

## [Unreleased]

### 新增

- **闲时增量 tick**（`remin tick`）：OS 调度器按档拉起的一次性增量维护（无常驻 daemon）——
  增量快挖 + deep 待挖队列排空。快速路径挖过、配置了 LLM 端点的行段自动挂账待挖队列；
  `remin tick` 每 tick 限段深挖（默认 3 段），密钥不在场保留队列，端点失败弃权出队不重试；
  手动 `mine --deep` 深挖过的范围自动出队不重复计费；深挖候选与既有 inbox 候选跨批次去重。
- **OS 调度器接线**（`remin tick schedule install [--every 4h] / remove / status`）：
  macOS launchd agent / Linux systemd user timer；间隔下限 15 分钟；effect 挂接线台账，
  `remin uninstall` 按台账回放摘除；加载 best-effort（失败给手工指引，文件已落位）。

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
