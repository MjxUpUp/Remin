# 记忆文件格式规范 v0

> 真源 = markdown + 显式 frontmatter，语义自包含（P4-R1）：不依赖任何模型、索引或二进制黑盒即可完整解读。
> 本规范是存储层唯一权威；实现以本文为准，演进需 bump frontmatter `version` 与本文版本号。

## 1. 文件与目录

- 真源仓库：`~/.remin/`（个人 git 仓库，唯一真源）
- 记忆文件：`memory/<type>/<id>.md`，一文件一条记忆
- `<type>` ∈ `episodic | semantic | procedural | preference | decision | spatial-context`（spatial-context 仅 schema 定义，暂无消费逻辑）
- 文件名即 `<id>`，扩展名 `.md`

## 2. frontmatter 字段（16 项）

| 字段 | 必填 | 类型 | 说明 |
|---|---|---|---|
| `id` | ✓ | string | 全局唯一、内容无关（`mem_` + ULID），**永不复用**（查全 git 历史） |
| `type` | ✓ | enum | 六类型之一 |
| `facet` | ✓ | string | 所属分面（默认 `dev` / `work` / `life`，可自定义） |
| `context` | | string[] | 项目/环境标签（从捕获来源自动标注，可改） |
| `status` | ✓ | enum | `active` / `superseded` / `expired` / `rejected` |
| `supersedes` | | string | 本条替代的旧条目 id（显式 supersession，单向指针由新指旧） |
| `superseded_by` | | string | 本条被何条替代（由 promote 事务维护） |
| `captured_at` | ✓ | time \| `unknown` | 事实发生时间（尽量从源头还原）；无法还原如实写 `unknown`，**绝不伪造** |
| `reviewed_at` | ✓ | time \| `unknown` | 记忆生效时间。人审通道（默认保守档）= 人审时刻；快速档自动生效的 ephemeral recap = 自动生效时刻（此时 `trust` 如实保持 `unverified`，见 config `autonomy`）。未经任一生效通道的产物不得落 `memory/` |
| `modified` | ✓ | time | 最后修改时间（ISO 8601，带时区） |
| `trust` | ✓ | enum | `human-verified` / `agent-claimed` / `unverified`；**永不自动升级** |
| `source` | ✓ | enum | `agent` / `human` / `import`（写入通道类别） |
| `provenance` | ✓ | object | `{ origin, ref, quote }`：来源系统、原始定位（会话 ID + 行号 / 文档 ID）、原文摘录；**覆盖率 100%** |
| `verify` | | object | `{ condition, last_check, result }`：失效条件与最近验证结果（`passed` / `failed` / `unknown`） |
| `expires` | | string | ephemeral 专用：`<N>d`（N 天自动过期，从 reviewed_at 起算）；recap 类默认 `7d` |
| `version` | ✓ | int | 格式版本号，当前 `1` |

时间格式统一 ISO 8601 含时区偏移（如 `2026-09-17T18:02:00+08:00`）。

## 3. 正文

frontmatter 之后为记忆正文：人类可读的一段话（首选一句到三句）。正文是注入与检索的主文本；原件摘录放 `provenance.quote`，正文永不被摘要覆盖（A6）。

## 4. 示例

```markdown
---
id: mem_01HZX...
type: decision
facet: dev
context: [payment-service]
status: active
captured_at: 2026-09-17T18:02:00+08:00
reviewed_at: 2026-09-17T21:40:00+08:00
modified: 2026-09-17T21:40:00+08:00
trust: human-verified
source: agent
provenance:
  origin: claude-code
  ref: session#A-8f3k, lines 1204-1211
  quote: 选 Postgres 而非 Mongo，因为事务一致性是硬需求
verify:
  condition: 数据库选型记录仍存在于 docs/adr/0003
  last_check: 2026-09-17T21:39:00+08:00
  result: passed
version: 1
---
选 Postgres 而非 Mongo：事务一致性是硬需求（本人确认）。
```

ephemeral（session-recap）示例：

```markdown
---
id: mem_01HZY...
type: episodic
facet: dev
status: active
captured_at: 2026-09-18T10:12:00+08:00
reviewed_at: 2026-09-18T10:30:00+08:00
modified: 2026-09-18T10:30:00+08:00
trust: unverified
source: agent
provenance:
  origin: claude-code
  ref: session#B-2c1d, lines 881-900
  quote: 上次重构到 auth 模块，两个测试挂了
expires: 7d
version: 1
---
重构进行到 auth 模块；users_test.go 两个用例挂了（token 过期断言）；下一步修 refresh 流程。
```

## 5. 状态机

```text
            promote(人审)              supersede(被新事实替代)
 candidate ────────────▶ active ───────────────────────▶ superseded
                           │  ▲                              （退出检索，保留 git 历史）
                           │  └──── verify 通过 / 未到期 ◀───
                           ▼
                        expired（ephemeral 到期或 verify 失效且未复验）
```

- `active`：可进入检索与注入集
- `superseded`：被显式替代，**退出检索**；文件保留、git 历史完整可审计
- `expired`：ephemeral 到期或验证失效；退出注入集。verify 失效的 expired 可经人判复活（`remin verify <id> --set passed`）；ephemeral 到期不可复活
- `rejected`：人审拒绝后的归档态（仅存于 audit 记录，不落 `memory/`）

## 6. 不变量（违反即 bug）

1. `id` 全局唯一且永不复用（含全部 git 历史）
2. 无 `provenance` 不落库；`origin`/`ref`/`quote` 三要素齐全
3. `supersedes` 与 `superseded_by` 成对出现（由 promote 事务保证一致性）
4. 冲突不共存：同一旧事实只能被一条新事实 supersede；supersede 后旧条目必须退出检索
5. 时间不可还原就如实 `unknown`，绝不伪造
6. `trust` 只在人审通道产生 `human-verified`；agent 通道最高 `agent-claimed`
7. 正文与 provenance.quote 一经人审落库即不可变（原件不可变；后续仅允许状态/verify 字段演进，且演进走 git 提交可审计）
