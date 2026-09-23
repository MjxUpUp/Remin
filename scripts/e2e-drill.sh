#!/usr/bin/env bash
# 端到端可用性演练：完整用户旅程（沙盒，不碰真实 HOME）
set -uo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN="$ROOT/bin/remin"

# 自建二进制：演练必须测当前工作区代码——陈旧 bin/remin 会给出假绿
# （2026-09-21 实例：v0.2 时代二进制跑过 lifecycle 后新增的 step15 断言）
go build -o "$BIN" ./cmd/remin || exit 1

SB="$(mktemp -d)/sandbox"
mkdir -p "$SB"
export REMIN_HOME="$SB/remin-store"
VB_FILE="$SB/vb.txt"
export REMIN_TRANSCRIPT_ROOTS="$SB/claude/projects"
export REMIN_CLAUDE_DIR="$SB/claude/projects"
# 多根发现隔离：防止真实 ~/.codex、~/.dsh 会话泄入演练（步骤 17 按命令内联覆盖）
export REMIN_CODEX_DIR="$SB/codex-empty"
# 深提取引擎钉 llm：演练世界用假 LLM 端点（防本机真 agent 抢占引擎解析）
export REMIN_DEEP_ENGINE=llm
export REMIN_DSH_DIR="$SB/dsh-empty"
mkdir -p "$REMIN_CODEX_DIR" "$REMIN_DSH_DIR"
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
PB=$("$BIN" inbox --json | python3 -c 'import json,sys; bs=json.load(sys.stdin)["data"]["batches"]; print(next(b["id"] for b in bs if b["source"]=="manual" and b["status"]!="done"))')
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
echo "--- 落位与台账 ---"
test -x "$REMIN_HOME/bin/remin" && echo "落位 OK: $REMIN_HOME/bin/remin"
test -f "$REMIN_HOME/wiring.json" && echo "台账 OK: $REMIN_HOME/wiring.json"

step "15. uninstall 卸载往返（cordis 台账回放，记忆默认保留）"
HOME="$FAKE_HOME" "$BIN" uninstall || exit 1
python3 - "$FAKE_HOME" "$REMIN_HOME" <<'PYEOF' || exit 1
import os, sys, glob
home, root = sys.argv[1], sys.argv[2]
# fixture HOME 从零创建：五处 agent 配置均为 install 创建的空壳，
# 卸载必须整删（正向钉死 ledger Created 语义；真实主目录中这些文件
# 含用户自有内容，不满足空壳条件会保留且无残留键——那是单测覆盖面）
for p in (".claude.json", ".claude/settings.json", ".codex/config.toml",
          ".cursor/mcp.json", ".gemini/settings.json"):
    fp = os.path.join(home, p)
    assert not os.path.exists(fp), f"{p} 空壳未整删"
# 备份清零、落位/台账删除、真源保留
assert not glob.glob(os.path.join(home, "**", "*.remin-backup-*"), recursive=True), "备份未清零"
assert not os.path.exists(os.path.join(root, "bin")), "落位 bin 未删除"
assert not os.path.exists(os.path.join(root, "wiring.json")), "台账未删除"
assert os.path.isdir(root), "真源被误删（默认必须保留）"
print("卸载往返全净：配置还原 / 备份清零 / 落位与台账删除 / 记忆真源保留")
PYEOF
HOME="$FAKE_HOME" "$BIN" doctor | tail -6
echo "--- 真源记忆仍在（卸载不动用户资产）---"
"$BIN" search "迁移脚本" | head -3

step "16. tick 闲时深挖 + OS 调度接线（fake LLM + launchctl shim）"
# 16a. deep 待挖队列：快挖入队（端点已配即可，无需密钥）
cat >> "$REMIN_HOME/config.yaml" <<'EOF'
llm:
  endpoint: http://127.0.0.1:18471/v1/chat/completions
  model: drill-fake
  timeout_ms: 3000
EOF
mkdir -p "$SB/claude/projects/Users-demo-proj2"
cat > "$SB/claude/projects/Users-demo-proj2/ccccdddd-1111-2222-3333-444455556666.jsonl" <<'EOF'
{"type":"user","sessionId":"ccccdddd-1111-2222-3333-444455556666","cwd":"/Users/demo/proj2","timestamp":"2026-09-22T10:00:00+08:00","message":{"role":"user","content":"这个项目验证要走 make constitution 才完整，单跑 go test 会漏依赖扫描"}}
EOF
"$BIN" mine >/dev/null || exit 1
python3 - "$REMIN_HOME/transcripts-cache/deep-queue.jsonl" <<'PYEOF' || exit 1
import json, sys
q = [json.loads(l) for l in open(sys.argv[1]) if l.strip()]
assert len(q) == 1 and q[0]["from_line"] == 1, q
print("deep 队列挂账 OK: %s" % q[0])
PYEOF
# 16b. tick 排空（fake LLM server）
python3 - <<'PYEOF' &
import json
from http.server import BaseHTTPRequestHandler, HTTPServer
RESP = json.dumps({"choices":[{"message":{"content":'[{"type":"preference","body":"验证统一走 make constitution，不单跑 go test","quote":"这个项目验证要走 make constitution 才完整，单跑 go test 会漏依赖扫描"}]'}}]}).encode()
class H(BaseHTTPRequestHandler):
    def do_POST(self):
        self.rfile.read(int(self.headers.get('Content-Length', 0)))
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(RESP)))
        self.end_headers()
        self.wfile.write(RESP)
    def log_message(self, *a): pass
HTTPServer(('127.0.0.1', 18471), H).serve_forever()
PYEOF
LLM_PID=$!
sleep 0.5
export REMIN_LLM_API_KEY=drill-dummy
"$BIN" tick --json | python3 -c 'import json,sys; d=json.load(sys.stdin)["data"]; assert d["deep_drained"]==1 and d["deep_candidates"]==1 and d["deep_pending"]==0, d; print("tick 深挖排空 OK: drained=%d candidates=%d"%(d["deep_drained"],d["deep_candidates"]))' || { kill $LLM_PID; exit 1; }
"$BIN" inbox --json | python3 -c 'import json,sys; bs=json.load(sys.stdin)["data"]["batches"]; mb=[b for b in bs if b["source"].startswith("mine") and b["status"]!="done"]; assert mb and mb[0]["pending"]>=1, bs; print("tick 深挖批次 OK: %s 候选 %d 条"%(mb[0]["id"],mb[0]["pending"]))' || { kill $LLM_PID; exit 1; }
kill $LLM_PID 2>/dev/null
# 16c. 调度接线：launchctl shim 拦截（不碰真实 launchd）
mkdir -p "$SB/shim"
printf '#!/bin/sh\necho "launchctl $*" >> "%s/launchctl.log"\nexit 0\n' "$SB/shim" > "$SB/shim/launchctl"
chmod +x "$SB/shim/launchctl"
HOME="$FAKE_HOME" PATH="$SB/shim:$PATH" "$BIN" tick schedule install --every 30m --json | python3 -c 'import json,sys; d=json.load(sys.stdin); assert d["ok"], d; print("schedule install OK")' || exit 1
test -f "$FAKE_HOME/Library/LaunchAgents/dev.reminmem.tick.plist" && echo "plist 落位 OK"
grep -q bootstrap "$SB/shim/launchctl.log" && echo "launchctl bootstrap 经 shim 调起 OK"
HOME="$FAKE_HOME" PATH="$SB/shim:$PATH" "$BIN" tick schedule status --json | python3 -c 'import json,sys; d=json.load(sys.stdin)["data"]; assert d["installed"] and d["interval"]=="1800s", d; print("schedule status OK: %s/%s"%(d["file"],d["interval"]))' || exit 1
HOME="$FAKE_HOME" PATH="$SB/shim:$PATH" "$BIN" tick schedule remove || exit 1
test ! -f "$FAKE_HOME/Library/LaunchAgents/dev.reminmem.tick.plist" && echo "schedule remove OK"

step "17. 多格式适配器挖矿（codex rollout + DSH session）"
mkdir -p "$SB/codex-sessions/2026/09/22" "$SB/dsh-sessions/--Users-demo-proj--/session-aaaa1111"
cat > "$SB/codex-sessions/2026/09/22/rollout-2026-09-22T10-00-00-aaaa1111.jsonl" <<'EOF'
{"timestamp":"2026-09-22T09:00:00.000Z","type":"session_meta","payload":{"session_id":"aaaa1111-2222-3333-4444-555566667777","cwd":"/Users/demo/proj","cli_version":"drill"}}
{"timestamp":"2026-09-22T09:00:05.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"记住：发布前必须先跑 changelog 检查"}]}}
{"timestamp":"2026-09-22T09:00:10.000Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"好的，发布前先检查 changelog"}]}}
EOF
cat > "$SB/dsh-plain.jsonl" <<'EOF'
{"type":"session","version":0,"id":"session-bbbb2222","createdAt":1787213329432,"cwd":"/Users/demo/proj2"}
{"type":"user/message","seq":15,"time":1787213444046,"data":{"content":[{"type":"text","text":"以后所有 DSH 项目都要先跑插件验证再发布"}],"source":{"kind":"user"},"role":"user"}}
{"type":"user/message","seq":16,"time":1787213445046,"data":{"content":[{"type":"text","text":"runtime context snapshot (plugin noise)"}],"source":{"kind":"plugin"},"role":"user"}}
EOF
if command -v zstd >/dev/null 2>&1; then
  zstd -q -c "$SB/dsh-plain.jsonl" > "$SB/dsh-sessions/--Users-demo-proj--/session-aaaa1111/session.jsonl.zstd"
  MIN_FILES=2
else
  echo "（zstd 不在场：DSH 压缩档解压失败被跳过属预期；DSH 解析由单测覆盖）"
  cp "$SB/dsh-plain.jsonl" "$SB/dsh-sessions/--Users-demo-proj--/session-aaaa1111/session.jsonl.zstd"
  MIN_FILES=1
fi
REMIN_CODEX_DIR="$SB/codex-sessions" REMIN_DSH_DIR="$SB/dsh-sessions" "$BIN" mine --json | python3 -c "import json,sys; d=json.load(sys.stdin)['data']; assert d['transcripts']>=$MIN_FILES, d; print('多格式发现 %d 个 transcript'%d['transcripts'])" || exit 1
"$BIN" inbox --json | python3 -c 'import json,sys; bs=json.load(sys.stdin)["data"]["batches"]; mine=[b for b in bs if b["source"].startswith("mine") and b["status"]!="done"]; assert mine, bs; print("多格式批次 %s：%d 条候选待审"%(mine[0]["id"],mine[0]["pending"]))' || exit 1
echo "（候选 origin 归因经单测钉死：codex/dsh/claude-code；此处验证发现与批次面）"

step "18. uplift 任务提升实测 + 纵向历史"
cat > "$SB/uplift-tasks.jsonl" <<'EOF'
{"query":"迁移脚本 部署","expect_contains":"迁移"}
{"query":"完全无关的查询 zzxxqq","expect_abstain":true}
EOF
"$BIN" eval uplift --tasks "$SB/uplift-tasks.jsonl" --record --json | python3 -c 'import json,sys;d=json.load(sys.stdin)["data"];assert d["hits"]==1 and d["abstain_correct"]==1 and d["false_hits"]==0 and d["wrong_abstains"]==0, d; print("uplift 实测 OK: 1 命中 1 弃权")' || exit 1
"$BIN" eval history | head -4
"$BIN" eval history --json | python3 -c 'import json,sys;d=json.load(sys.stdin)["data"];assert d and d[-1]["recall"]>0, d; print("历史往返 OK: recall=%.2f"%d[-1]["recall"])' || exit 1
step "19. 笔记桥（假 Notion 端点：pull 摄取 + push dry-run）"
python3 - <<'PYEOF' &
import json
from http.server import BaseHTTPRequestHandler, HTTPServer
class H(BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        body = b'{}'
        if self.path.endswith('/v1/blocks/parent1/children'):
            body = json.dumps({"results":[{"id":"p1","type":"child_page","child_page":{"title":"部署手册"}}]}).encode()
        elif self.path.endswith('/v1/blocks/p1/children'):
            body = json.dumps({"results":[{"type":"paragraph","paragraph":{"rich_text":[{"text":{"content":"先跑迁移再发布"}}]}}]}).encode()
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        self.wfile.write(body)
    def do_POST(self):
        self.rfile.read(int(self.headers.get('Content-Length', 0)))
        body = json.dumps({"id":"newp","url":"https://notion.so/newp"}).encode()
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        self.wfile.write(body)
    def log_message(self, *a): pass
HTTPServer(('127.0.0.1', 18472), H).serve_forever()
PYEOF
NB_PID=$!
sleep 0.5
cat >> "$REMIN_HOME/config.yaml" <<'CFGEOF' || { kill $NB_PID; exit 1; }
bridge:
  notion:
    api_base: http://127.0.0.1:18472
    parent_page_id: parent1
CFGEOF
export REMIN_NOTION_TOKEN=drill-n
"$BIN" bridge pull --from notion --apply >/dev/null || { kill $NB_PID; exit 1; }
"$BIN" inbox --json | python3 -c 'import json,sys;bs=json.load(sys.stdin)["data"]["batches"];assert any("import" in b["source"] for b in bs), bs; print("桥摄取 OK（human-verified 通道批次在 inbox）")' || { kill $NB_PID; exit 1; }
"$BIN" bridge push --to notion --dry-run | grep -q "dry-run" && echo "桥 push dry-run OK" || { kill $NB_PID; exit 1; }
kill $NB_PID 2>/dev/null
step "20. 审收 Web 界面（remin ui：API 审收往返）"
"$BIN" propose "UI 验证用偏好：回复一律用中文" --type preference >/dev/null || exit 1
"$BIN" ui --port 18473 --no-open &
UI_PID=$!
for i in $(seq 1 20); do curl -s -m 1 -o /dev/null http://127.0.0.1:18473/api/state && break; sleep 0.5; done
curl -fsS http://127.0.0.1:18473/api/state | python3 -c 'import json,sys;d=json.load(sys.stdin)["data"];assert d["batches"] and "memories" in d and "deep_pending" in d and "tick_last" in d, d; print("ui state OK（含状态带扩展字段）")' || { kill $UI_PID; exit 1; }
curl -fsS http://127.0.0.1:18473/api/history | python3 -c 'import json,sys;d=json.load(sys.stdin)["data"];assert d==[] or isinstance(d,list), d; print("ui history OK")' || { kill $UI_PID; exit 1; }
curl -fsS "http://127.0.0.1:18473/api/batch?id=$("$BIN" inbox --json | python3 -c 'import json,sys;bs=json.load(sys.stdin)["data"]["batches"];print(next((b["id"] for b in bs if b["status"]!="done" and b["pending"]>0), ""))')" | python3 -c 'import json,sys;d=json.load(sys.stdin)["data"];c=d["candidates"][0];assert c["id"] and c["body"] and c["provenance"], c; print("ui batch 候选 snake_case 投影 OK")' || { kill $UI_PID; exit 1; }
curl -fsS http://127.0.0.1:18473/ | grep -q "Remin" && echo "ui 首页 OK" || { kill $UI_PID; exit 1; }
UB=$("$BIN" inbox --json | python3 -c 'import json,sys;bs=json.load(sys.stdin)["data"]["batches"];print(next(b["id"] for b in bs if b["status"]!="done" and b["pending"]>0))')
printf '{"batch":"%s","all":true}' "$UB" > "$SB/ui-sel.json"
curl -sS -X POST -H "Content-Type: application/json" --data-binary "@$SB/ui-sel.json" http://127.0.0.1:18473/api/promote -o "$SB/ui-promote.json" -w "http=%{http_code}" || { kill $UI_PID; exit 1; }
cat "$SB/ui-promote.json" | python3 -c 'import json,sys;d=json.load(sys.stdin);assert d["ok"] and d["data"]["Version"]>=1, d; print("ui adopt OK (atomic commit v%d)"%d["data"]["Version"])' || { kill $UI_PID; exit 1; }
"$BIN" inbox --json | python3 -c 'import json,sys;bs=json.load(sys.stdin)["data"]["batches"];b=next(x for x in bs if x["id"]==sys.argv[1]);assert b["status"]=="done" and b["pending"]==0, b; print("ui 审收后该批次清空 OK")' "$UB" || { kill $UI_PID; exit 1; }
kill $UI_PID 2>/dev/null

echo
echo "═══ 演练完成 ═══"
echo "沙盒: ${SB}（未清理，供检查）"
