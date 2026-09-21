package miner

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/remin-dev/remin/internal/core/config"
	"github.com/remin-dev/remin/internal/core/inbox"
	"github.com/remin-dev/remin/internal/store"
	"github.com/remin-dev/remin/internal/testutil"
)

const sessID = "3f2a1b8c-d4e5-4f60-9a7b-8c9d0e1f2a3b"

func writeTranscript(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func transcript(dir, name string) string {
	return filepath.Join(dir, "Users-jx-own-projects-Remin", name)
}

// M4 完成判据：fixture 会话日志挖出候选且 provenance 指向行号
func TestMineExtractsWithLineRefs(t *testing.T) {
	st := testutil.NewStore(t)
	dir := t.TempDir()
	tp := transcript(dir, sessID+".jsonl")
	writeTranscript(t, tp, `{"type":"user","sessionId":"`+sessID+`","cwd":"/Users/jx/own-projects/Remin","timestamp":"2026-09-20T10:00:00+08:00","message":{"role":"user","content":"记住：部署前必须先跑迁移脚本"}}
{"type":"system","timestamp":"2026-09-20T10:00:01+08:00","content":"hook fired"}
{"type":"user","sessionId":"`+sessID+`","timestamp":"2026-09-20T10:00:02+08:00","message":{"role":"user","content":[{"type":"tool_result","content":"ok"}]}}
{"type":"user","sessionId":"`+sessID+`","timestamp":"2026-09-20T10:00:03+08:00","message":{"role":"user","content":"<system-reminder>内部注入</system-reminder>"}}
{"type":"assistant","sessionId":"`+sessID+`","timestamp":"2026-09-20T10:01:00+08:00","message":{"role":"assistant","content":[{"type":"text","text":"我们决定选 Postgres 而不是 Mongo，因为事务一致性是硬需求"}]}}
{"type":"assistant","sessionId":"`+sessID+`","timestamp":"2026-09-20T10:02:00+08:00","message":{"role":"assistant","content":[{"type":"text","text":"这次的教训是 CI 挂了，因为环境变量没加载"}]}}
`)

	cfg := config.Default()
	rep, err := Mine(context.Background(), st, cfg, Options{ClaudeDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Transcripts != 1 {
		t.Fatalf("应处理 1 个 transcript: %+v", rep)
	}
	in := inbox.New(st)
	cands, err := in.ListCandidates(rep.Batch)
	if err != nil {
		t.Fatal(err)
	}
	var sawProcedural, sawDecision, sawLesson, sawRecap bool
	refRe := regexp.MustCompile(`^session#3f2a1b8c, line(s)? \d+(-\d+)?$`)
	for _, c := range cands {
		if c.Provenance.Origin != "claude-code" || !refRe.MatchString(c.Provenance.Ref) {
			t.Errorf("provenance 应指向会话与行号: %+v", c.Provenance)
		}
		if c.Trust != store.TrustUnverified {
			t.Errorf("挖矿候选应 unverified: %s", c.Trust)
		}
		switch {
		case c.Type == store.TypeProcedural && strings.Contains(c.Body, "迁移脚本"):
			sawProcedural = true
		case c.Type == store.TypeDecision && strings.Contains(c.Body, "Postgres"):
			sawDecision = true
		case c.Type == store.TypeProcedural && strings.Contains(c.Body, "教训"):
			sawLesson = true
		case c.Type == store.TypeEpisodic && c.Expires == "7d":
			sawRecap = true
		}
	}
	if !sawProcedural || !sawDecision || !sawRecap {
		t.Errorf("应挖出程序性/决策/recap 候选: %+v", cands)
	}
	_ = sawLesson
}

// 断点续挖：追加行后重挖只提取增量；无变化则零产出（幂等）
func TestIncrementalCursor(t *testing.T) {
	st := testutil.NewStore(t)
	dir := t.TempDir()
	tp := transcript(dir, sessID+".jsonl")
	writeTranscript(t, tp, `{"type":"user","sessionId":"`+sessID+`","cwd":"/x","timestamp":"2026-09-20T10:00:00+08:00","message":{"role":"user","content":"以后回复都用中文"}}
`)
	cfg := config.Default()
	rep1, err := Mine(context.Background(), st, cfg, Options{ClaudeDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if rep1.Candidates == 0 {
		t.Fatal("首轮应挖出候选")
	}
	// 无变化 → 零产出
	rep2, _ := Mine(context.Background(), st, cfg, Options{ClaudeDir: dir})
	if rep2.Candidates != 0 {
		t.Errorf("无变化不应重复产出: %+v", rep2)
	}
	// 追加一行 → 只挖增量
	f, _ := os.OpenFile(tp, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString(`{"type":"user","sessionId":"` + sessID + `","cwd":"/x","timestamp":"2026-09-20T11:00:00+08:00","message":{"role":"user","content":"记住：代码注释用英文"}}
`)
	f.Close()
	rep3, _ := Mine(context.Background(), st, cfg, Options{ClaudeDir: dir})
	if rep3.Candidates == 0 {
		t.Fatal("追加后应挖出增量候选")
	}
}

// Stop hook 队列：幂等去重 + from-queue 消费后清空
func TestQueueDedupAndDrain(t *testing.T) {
	st := testutil.NewStore(t)
	dir := t.TempDir()
	tp := transcript(dir, sessID+".jsonl")
	writeTranscript(t, tp, `{"type":"user","sessionId":"`+sessID+`","cwd":"/x","timestamp":"2026-09-20T10:00:00+08:00","message":{"role":"user","content":"永远不要在周五下午部署"}}
`)
	q := LoadQueue(st.Root)
	if err := q.Append(tp); err != nil {
		t.Fatal(err)
	}
	if err := q.Append(tp); err != nil {
		t.Fatal(err)
	}
	if got := len(q.All()); got != 1 {
		t.Fatalf("按路径幂等去重应剩 1: %d", got)
	}
	cfg := config.Default()
	rep, err := Mine(context.Background(), st, cfg, Options{FromQueue: true, ClaudeDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Candidates == 0 {
		t.Fatal("队列消费应挖出候选")
	}
	if got := len(LoadQueue(st.Root).All()); got != 0 {
		t.Errorf("处理后队列应清空: %d", got)
	}
}

// 快速档：recap 自动生效（trust 保持 unverified、ephemeral 7d），普通候选仍待审
func TestFastModeAutoPromotesRecapOnly(t *testing.T) {
	st := testutil.NewStore(t)
	dir := t.TempDir()
	tp := transcript(dir, sessID+".jsonl")
	writeTranscript(t, tp, `{"type":"user","sessionId":"`+sessID+`","cwd":"/x","timestamp":"2026-09-20T10:00:00+08:00","message":{"role":"user","content":"记住：必须先跑测试再提交"}}
{"type":"assistant","sessionId":"`+sessID+`","timestamp":"2026-09-20T10:05:00+08:00","message":{"role":"assistant","content":[{"type":"text","text":"已完成"}]}}
`)
	cfg := config.Default()
	cfg.Autonomy = config.AutonomyFast
	rep, err := Mine(context.Background(), st, cfg, Options{ClaudeDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if rep.AutoPromoted == 0 {
		t.Fatal("快速档应自动生效 recap")
	}
	ms, _ := st.ListMemories()
	if len(ms) != 1 {
		t.Fatalf("应只有 recap 落库: %d", len(ms))
	}
	if ms[0].Trust != store.TrustUnverified || ms[0].Expires != "7d" {
		t.Errorf("recap 应保持未验证且 ephemeral: %+v", ms[0])
	}
	if v, _ := st.Version(); v != 1 {
		t.Errorf("自动生效也走原子提交推进版本: %d", v)
	}
	// 普通候选仍在 inbox 待审
	in := inbox.New(st)
	cands, _ := in.ListCandidates(rep.Batch)
	if len(cands) == 0 {
		t.Error("普通候选不应被自动采纳")
	}
}

// 预算到：Drain 停在新文件前（剩余留队列）
func TestDrainRespectsBudget(t *testing.T) {
	st := testutil.NewStore(t)
	dir := t.TempDir()
	for i := 0; i < 3; i++ {
		tp := transcript(dir, sessID[:8]+string(rune('a'+i))+"-0000-0000-000000000000.jsonl")
		writeTranscript(t, tp, `{"type":"user","sessionId":"`+sessID[:8]+string(rune('a'+i))+`","cwd":"/x","timestamp":"2026-09-20T10:00:00+08:00","message":{"role":"user","content":"记住：事项`+string(rune('A'+i))+`"}}
`)
		q := LoadQueue(st.Root)
		_ = q.Append(tp)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即超时
	cfg := config.Default()
	n, err := Drain(ctx, st, cfg, dir)
	if err == nil && n > 0 {
		t.Errorf("预算到不应处理: %d", n)
	}
	if got := len(LoadQueue(st.Root).All()); got != 3 {
		t.Errorf("队列应完整保留: %d", got)
	}
}
