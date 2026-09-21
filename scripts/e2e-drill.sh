#!/usr/bin/env bash
# 端到端可用性演练：完整用户旅程（沙盒，不碰真实 HOME）
set -uo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN="$ROOT/bin/remin"

SB="$(mktemp -d)/sandbox"
mkdir -p "$SB"
export REMIN_HOME="$SB/remin-store"
VB_FILE="$SB/vb.txt"
export REMIN_TRANSCRIPT_ROOTS="$SB/claude/projects"
export REMIN_CLAUDE_DIR="$SB/claude/projects"
export GIT_AUTHOR_NAME="演练用户" GIT_AUTHOR_EMAIL=demo@remin.local
export GIT_COMMITTER_NAME="演练用户" GIT_COMMITTER_EMAIL=demo@remin.local

step() { echo; echo "═══ $1 ═══"; }

step "1. init 真源"
"$BIN" init || exit 1
"$BIN" version

step "2. propose（现在就记）→ inbox → promote"
"$BIN" propose "回复用中文，代码注释用英文" --type preference --origin human-cli || exit 1
"$BIN" inbox
BATCH=$("$BIN" inbox --json | python3 -c 'import json,sys; print(json.load(sys.stdin)["data"]["batches"][0]["id"])')
CAND=$("$BIN" inbox --json | python3 -c 'import json,sys; print(json.load(sys.stdin)["data"]["batches"][0]["pending"])')
echo "批次=$BATCH 候选数=$CAND"
"$BIN" promote --batch "$BATCH" --all || exit 1

step "3. search 检索（trust 随行）"
"$BIN" search "中文 回复" || exit 1
"$BIN" search "完全无关的垃圾查询 zzxxqq" || true

step "4. mine 挖矿（fixture transcript）"
mkdir -p "$SB/claude/projects/Users-demo-proj"
cat > "$SB/claude/projects/Users-demo-proj/aaaabbbb-1111-2222-3333-444455556666.jsonl" <<'EOF'
{"type":"user","sessionId":"aaaabbbb-1111-2222-3333-444455556666","cwd":"/Users/demo/proj","timestamp":"2026-09-20T10:00:00+08:00","message":{"role":"user","content":"记住：部署前必须先跑迁移脚本"}}
{"type":"assistant","sessionId":"aaaabbbb-1111-2222-3333-444455556666","timestamp":"2026-09-20T10:03:00+08:00","message":{"role":"assistant","content":[{"type":"text","text":"我们决定选 Postgres 而不是 Mongo，因为事务一致性是硬需求"}]}}
EOF
"$BIN" mine || exit 1
MB=$("$BIN" inbox --json | python3 -c 'import json,sys; bs=json.load(sys.stdin)["data"]["batches"]; print(next(b["id"] for b in bs if b["source"].startswith("mine")))')
"$BIN" inbox --batch "$MB" | head -20
"$BIN" promote --batch "$MB" --all || exit 1

step "5. 冲突消解：新事实 supersede 旧事实"
"$BIN" propose "主力数据库已从 Postgres 迁移到 CockroachDB" --type semantic || exit 1
OLD=$("$BIN" search "Postgres 事务" --json | python3 -c 'import json,sys; d=json.load(sys.stdin)["data"]; print(d["results"][0]["id"] if d.get("results") else "")')
echo "旧事实 id=$OLD"
PB=$("$BIN" inbox --json | python3 -c 'import json,sys; bs=json.load(sys.stdin)["data"]["batches"]; print(next(b["id"] for b in bs if b["source"]=="manual"))')
# 手工构造 supersede：用 CLI 无直接 --supersedes，走 import 冲突建议路径在步骤 6 验证；此处直接 promote 后再验证 status 链
"$BIN" promote --batch "$PB" --all || exit 1
"$BIN" status "$OLD" | head -8

step "6. import（markdown-dir，human-verified）"
mkdir -p "$SB/notes"
echo "构建一律走 pnpm build，不要用 npm。" > "$SB/notes/build.md"
"$BIN" import --from markdown-dir --path "$SB/notes" || exit 1
"$BIN" import --from markdown-dir --path "$SB/notes" --apply || exit 1
IB=$("$BIN" inbox --json | python3 -c 'import json,sys; bs=json.load(sys.stdin)["data"]["batches"]; print(next(b["id"] for b in bs if b["source"].startswith("import")))')
"$BIN" inbox --batch "$IB" | head -12
"$BIN" promote --batch "$IB" --all || exit 1
"$BIN" import --from markdown-dir --path "$SB/notes" --apply | head -3

step "7. verify 用前验证（通过 + 失效两例）"
echo "remin verify 条件锚点文件" > "$SB/anchor.txt"
promote_newest_manual() {
  "$BIN" propose "$1" --type semantic --verify-condition "$2" >/dev/null || exit 1
  local b=$("$BIN" inbox --json | python3 -c 'import json,sys; bs=json.load(sys.stdin)["data"]["batches"]; print(next(b["id"] for b in bs if b["source"]=="manual" and b["status"]!="done"))')
  "$BIN" promote --batch "$b" --all >/dev/null || exit 1
}
promote_newest_manual "构建锚点文件存在" "path-exists:$SB/anchor.txt"
promote_newest_manual "过时事实：依赖已删除的路径" "path-exists:$SB/never-exists.txt"
VID=$("$BIN" search "锚点文件" --json | python3 -c 'import json,sys; d=json.load(sys.stdin)["data"]; print(d["results"][0]["id"])')
"$BIN" verify "$VID"
OLDVID=$("$BIN" search "过时事实 依赖" --json | python3 -c 'import json,sys; d=json.load(sys.stdin)["data"]; print(d["results"][0]["id"] if d.get("results") else "")')
"$BIN" verify "$OLDVID"
"$BIN" search "过时事实" || true

step "8. inject 注入（SessionStart 形态）"
"$BIN" inject | head -15

step "9. export → restore roundtrip"
"$BIN" export --out "$SB/bundle" || exit 1
export REMIN_HOME="$SB/remin-store2"
"$BIN" init >/dev/null || exit 1
"$BIN" restore --bundle "$SB/bundle" || exit 1
"$BIN" search "迁移脚本" | head -5
export REMIN_HOME="$SB/remin-store"

step "10. hook-stop（Stop hook 载荷 → 入队）"
echo "{\"session_id\":\"s1\",\"transcript_path\":\"$SB/claude/projects/Users-demo-proj/aaaabbbb-1111-2222-3333-444455556666.jsonl\"}" | "$BIN" hook-stop
echo "hook-stop 退出码=$?"
echo "{\"session_id\":\"s2\",\"transcript_path\":\"$SB/evil-outside.jsonl\"}" | "$BIN" hook-stop
echo "白名单外退出码=$?"

step "11. log 审计"
"$BIN" log --limit 5

step "12. eval 全套件"
"$BIN" eval run --suite all --out "$SB/eval-report.json" | tail -20

step "13. --json 输出契约抽查"
"$BIN" search "数据库" --json | python3 -c 'import json,sys; d=json.load(sys.stdin); assert d["ok"] and "results" in d["data"]; print("JSON 契约 OK")'

step "14. doctor（fixture HOME）"
FAKE_HOME="$SB/fake-home"
mkdir -p "$FAKE_HOME/.claude" "$FAKE_HOME/.codex" "$FAKE_HOME/.cursor" "$FAKE_HOME/.gemini"
HOME="$FAKE_HOME" "$BIN" doctor
HOME="$FAKE_HOME" "$BIN" doctor --install
echo "--- .claude.json 摘要 ---"
HOME="$FAKE_HOME" "$BIN" doctor --json | python3 -c 'import json,sys; d=json.load(sys.stdin)["data"]; print([ (a["agent"],a["wired"]) for a in d["agents"]])'

echo
echo "═══ 演练完成 ═══"
echo "沙盒: $SB（未清理，供检查）"
