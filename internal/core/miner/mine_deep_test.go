package miner

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/remin-dev/remin/internal/core/config"
	"github.com/remin-dev/remin/internal/core/inbox"
	"github.com/remin-dev/remin/internal/testutil"
)

// 深路径接线契约：--deep 时快速路径结果保留，深路径候选合并进同一批次；
// 端点失败只降级不失败（弃权语义）；未配置时显式报错。

// 自然表达（无触发词）的 fixture：快速路径抓不到 typed 候选，只产 recap。
const deepFixture = `{"type":"user","sessionId":"` + sessID + `","cwd":"/Users/jx/own-projects/Remin","timestamp":"2026-09-21T10:00:00+08:00","message":{"role":"user","content":"这个项目验证要走 make constitution 才完整，单跑 go test 会漏依赖扫描"}}
{"type":"assistant","sessionId":"` + sessID + `","timestamp":"2026-09-21T10:01:00+08:00","message":{"role":"assistant","content":[{"type":"text","text":"明白了，之后验证我都先跑宪法检查"}]}}
`

func deepServer(t *testing.T, content string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		fmt.Fprint(w, `{"choices":[{"message":{"content":`+quoteJSON(content)+`}}]}`)
	}))
}

func quoteJSON(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func TestMineDeepMergesFastAndDeep(t *testing.T) {
	stubNoDeepAgents(t)
	st := testutil.NewStore(t)
	dir := t.TempDir()
	writeTranscript(t, transcript(dir, sessID+".jsonl"), deepFixture)

	srv := deepServer(t, `[{"type":"preference","body":"验证统一走 make constitution，不单跑 go test","quote":"这个项目验证要走 make constitution 才完整，单跑 go test 会漏依赖扫描"}]`)
	defer srv.Close()

	cfg := config.Default()
	cfg.LLM = &config.LLMConfig{Endpoint: srv.URL, Model: "m", APIKey: "k", TimeoutMs: 3000}
	rep, err := Mine(context.Background(), st, cfg, Options{ClaudeDir: dir, Deep: true})
	if err != nil {
		t.Fatal(err)
	}
	// 快速路径：recap 1 条（typed 0——无触发词）；深路径：1 条
	if rep.Candidates != 2 {
		t.Fatalf("应为快速 1（recap）+ 深度 1: %+v", rep)
	}
	in := inbox.New(st)
	cands, err := in.ListCandidates(rep.Batch)
	if err != nil {
		t.Fatal(err)
	}
	var deep int
	for _, c := range cands {
		if c.Provenance.Origin == "claude-code·deep" {
			deep++
			if c.Trust != "unverified" {
				t.Errorf("深路径候选必须 unverified: %+v", c)
			}
		}
	}
	if deep != 1 {
		t.Errorf("应有 1 条深路径候选进批次: %+v", cands)
	}
}

func TestMineDeepUnconfiguredFailsFast(t *testing.T) {
	stubNoDeepAgents(t)
	st := testutil.NewStore(t)
	dir := t.TempDir()
	writeTranscript(t, transcript(dir, sessID+".jsonl"), deepFixture)

	_, err := Mine(context.Background(), st, config.Default(), Options{ClaudeDir: dir, Deep: true})
	if err == nil || !strings.Contains(err.Error(), "llm") {
		t.Fatalf("未配置 llm 节时 --deep 应显式报错: %v", err)
	}

	// 缺密钥同样前置拦截（评审 P2：否则逐 transcript 弃权只进 Note，
	// 游标照常推进——该增量深提取机会永久丢失，静态配置错误不允许静默降级）
	cfg := config.Default()
	cfg.LLM = &config.LLMConfig{Endpoint: "http://127.0.0.1:1", Model: "m", TimeoutMs: 500}
	_, err = Mine(context.Background(), st, cfg, Options{ClaudeDir: dir, Deep: true})
	if err == nil || !strings.Contains(err.Error(), "REMIN_LLM_API_KEY") {
		t.Fatalf("缺密钥应前置报错（不进挖矿循环）: %v", err)
	}
}

// 快速路径与深路径同 body：快速路径优先，深路径重复被抑制
func TestMineDeepSuppressesDuplicateBodies(t *testing.T) {
	stubNoDeepAgents(t)
	st := testutil.NewStore(t)
	dir := t.TempDir()
	// 带触发词的 fixture：快速路径产 1 条 typed 候选 + recap
	tp := transcript(dir, sessID+".jsonl")
	writeTranscript(t, tp, `{"type":"user","sessionId":"`+sessID+`","cwd":"/Users/demo/proj","timestamp":"2026-09-21T10:00:00+08:00","message":{"role":"user","content":"记住：验证统一走 make constitution，不单跑 go test"}}
`)

	srv := deepServer(t, `[{"type":"preference","body":"记住：验证统一走 make constitution，不单跑 go test","quote":"记住：验证统一走 make constitution，不单跑 go test"}]`)
	defer srv.Close()

	cfg := config.Default()
	cfg.LLM = &config.LLMConfig{Endpoint: srv.URL, Model: "m", APIKey: "k", TimeoutMs: 3000}
	rep, err := Mine(context.Background(), st, cfg, Options{ClaudeDir: dir, Deep: true})
	if err != nil {
		t.Fatal(err)
	}
	// 快速路径 typed 1 + recap 1；深路径同 body 被抑制 → 共 2
	if rep.Candidates != 2 {
		t.Fatalf("同 body 应被抑制（快速优先）: %+v", rep)
	}
	in := inbox.New(st)
	cands, err := in.ListCandidates(rep.Batch)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cands {
		if c.Provenance.Origin == "claude-code·deep" {
			t.Errorf("与快速路径同 body 的深路径候选不应进批次: %+v", c)
		}
	}
}

func TestMineDeepEndpointFailureDegrades(t *testing.T) {
	stubNoDeepAgents(t)
	st := testutil.NewStore(t)
	dir := t.TempDir()
	writeTranscript(t, transcript(dir, sessID+".jsonl"), deepFixture)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := config.Default()
	cfg.LLM = &config.LLMConfig{Endpoint: srv.URL, Model: "m", APIKey: "k", TimeoutMs: 3000}
	rep, err := Mine(context.Background(), st, cfg, Options{ClaudeDir: dir, Deep: true})
	if err != nil {
		t.Fatalf("端点失败应降级不失败: %v", err)
	}
	if rep.Candidates != 1 { // 只剩快速路径 recap
		t.Errorf("端点失败应只剩快速路径结果: %+v", rep)
	}
	if rep.Note == "" || !strings.Contains(rep.Note, "深度") {
		t.Errorf("降级应在报告备注可见: %q", rep.Note)
	}
}
