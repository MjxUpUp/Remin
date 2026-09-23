package miner

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/remin-dev/remin/internal/core/config"
	"github.com/remin-dev/remin/internal/core/inbox"
	"github.com/remin-dev/remin/internal/testutil"
)

// 闲时 tick 契约：快速路径挖过的行段进 deep 待挖队列；remin tick 排空队列
// （候选进 inbox，同 P3 只产候选）；密钥不在场保留队列；弃权段出队不重试。

// TestDeepQueueAppendDedupeRemove 队列基础契约：同段幂等、RemoveDone 摘除、持久化往返
func TestDeepQueueAppendDedupeRemove(t *testing.T) {
	st := testutil.NewStore(t)
	q := LoadDeepQueue(st.Root)
	q.Append("/tmp/a.jsonl", 1, 10)
	q.Append("/tmp/a.jsonl", 1, 10) // 同段幂等
	q.Append("/tmp/a.jsonl", 11, 20)
	if got := len(q.All()); got != 2 {
		t.Fatalf("同段应幂等去重: %d", got)
	}
	if err := q.Save(); err != nil {
		t.Fatal(err)
	}
	q2 := LoadDeepQueue(st.Root)
	items := q2.All()
	if len(items) != 2 {
		t.Fatalf("持久化往返应保 2 段: %+v", items)
	}
	q2.RemoveDone(map[string]bool{deepKey("/tmp/a.jsonl", 1, 10): true})
	if got := len(q2.All()); got != 1 {
		t.Fatalf("RemoveDone 应摘 1 段: %d", got)
	}
	if q2.All()[0].ToLine != 20 {
		t.Fatalf("摘错的段: %+v", q2.All())
	}
}

// TestDeepQueueRemoveCovered 手动 --deep 深挖过的范围应出队（按实际深挖区间，不重复计费）
func TestDeepQueueRemoveCovered(t *testing.T) {
	st := testutil.NewStore(t)
	q := LoadDeepQueue(st.Root)
	q.Append("/tmp/a.jsonl", 1, 10)
	q.Append("/tmp/a.jsonl", 11, 30)
	q.Append("/tmp/a.jsonl", 31, 40)
	q.RemoveCovered("/tmp/a.jsonl", 11, 30)
	items := q.All()
	if len(items) != 2 {
		t.Fatalf("被 [11,30] 覆盖的段应出队: %+v", items)
	}
	for _, it := range items {
		if it.FromLine == 11 {
			t.Fatalf("仅真正被覆盖的段出队: %+v", items)
		}
	}
}

// TestMineIncrementalDeepKeepsEarlierSegments 增量 --deep（断点续挖）不得出队
// 断点前从未深挖的待挖段——只出队本次真正深挖的区间（审查 P1 修复的杀灭测试）
func TestMineIncrementalDeepKeepsEarlierSegments(t *testing.T) {
	st := testutil.NewStore(t)
	dir := t.TempDir()
	tp := transcript(dir, sessID+".jsonl")
	writeTranscript(t, tp, deepFixture) // 行 1-2

	// 第一次快挖：入队 [1,2]
	cfg := config.Default()
	cfg.LLM = &config.LLMConfig{Endpoint: "http://127.0.0.1:1", Model: "m", TimeoutMs: 500}
	if _, err := Mine(context.Background(), st, cfg, Options{ClaudeDir: dir}); err != nil {
		t.Fatal(err)
	}
	// 追加行 3-4（游标推进到 2）
	writeTranscript(t, tp, deepFixture+
		`{"type":"user","sessionId":"`+sessID+`","timestamp":"2026-09-21T10:02:00+08:00","message":{"role":"user","content":"记得部署前先看 runbook"}}
`+
		`{"type":"assistant","sessionId":"`+sessID+`","timestamp":"2026-09-21T10:03:00+08:00","message":{"role":"assistant","content":[{"type":"text","text":"好的，部署前先看 runbook"}]}}
`)
	// 增量 --deep（非 force）：只深挖行 3-4
	srv := deepServer(t, `[]`)
	defer srv.Close()
	cfg.LLM.Endpoint = srv.URL
	cfg.LLM.APIKey = "k"
	if _, err := Mine(context.Background(), st, cfg, Options{ClaudeDir: dir, Deep: true}); err != nil {
		t.Fatal(err)
	}
	items := LoadDeepQueue(st.Root).All()
	if len(items) != 1 || items[0].FromLine != 1 || items[0].ToLine != 2 {
		t.Fatalf("增量深挖后 [1,2] 段（从未深挖）必须保留: %+v", items)
	}
}

// TestMineForceRemineDedupesQueuedSegments force 全量重挖时，被新段完全覆盖的旧段去重（不重复计费）
func TestMineForceRemineDedupesQueuedSegments(t *testing.T) {
	st := testutil.NewStore(t)
	dir := t.TempDir()
	tp := transcript(dir, sessID+".jsonl")
	writeTranscript(t, tp, deepFixture)

	cfg := config.Default()
	cfg.LLM = &config.LLMConfig{Endpoint: "http://127.0.0.1:1", Model: "m", TimeoutMs: 500}
	if _, err := Mine(context.Background(), st, cfg, Options{ClaudeDir: dir}); err != nil {
		t.Fatal(err)
	}
	// force 重挖同范围：旧段 [1,2] 应被新段 [1,2] 替换（不叠加）
	if _, err := Mine(context.Background(), st, cfg, Options{ClaudeDir: dir, Force: true}); err != nil {
		t.Fatal(err)
	}
	items := LoadDeepQueue(st.Root).All()
	if len(items) != 1 || items[0].FromLine != 1 || items[0].ToLine != 2 {
		t.Fatalf("force 重挖应去重旧段: %+v", items)
	}
}

// TestMineEnqueuesDeepRange 快挖（含 hook 追赶）处理过的行段应进 deep 队列（端点已配即入队，密钥后置校验）
func TestMineEnqueuesDeepRange(t *testing.T) {
	st := testutil.NewStore(t)
	dir := t.TempDir()
	tp := transcript(dir, sessID+".jsonl")
	writeTranscript(t, tp, deepFixture)

	cfg := config.Default()
	cfg.LLM = &config.LLMConfig{Endpoint: "http://127.0.0.1:1", Model: "m", TimeoutMs: 500} // 无密钥：入队照常
	if _, err := Mine(context.Background(), st, cfg, Options{ClaudeDir: dir}); err != nil {
		t.Fatal(err)
	}
	items := LoadDeepQueue(st.Root).All()
	if len(items) != 1 {
		t.Fatalf("快挖后应有 1 段待深挖: %+v", items)
	}
	if items[0].Path != tp || items[0].FromLine != 1 || items[0].ToLine != 2 {
		t.Fatalf("段范围应为 1-2（fixture 两行）: %+v", items[0])
	}
}

// TestMineNoDeepQueueWithoutLLM 未配置 llm 节不入队（队列不为无深路径用户增长）
func TestMineNoDeepQueueWithoutLLM(t *testing.T) {
	st := testutil.NewStore(t)
	dir := t.TempDir()
	writeTranscript(t, transcript(dir, sessID+".jsonl"), deepFixture)

	if _, err := Mine(context.Background(), st, config.Default(), Options{ClaudeDir: dir}); err != nil {
		t.Fatal(err)
	}
	if got := len(LoadDeepQueue(st.Root).All()); got != 0 {
		t.Fatalf("未配置 llm 不应入队: %d", got)
	}
}

// TestMineDeepConsumesQueue 手动 --deep 深挖后，该文件待挖段应出队
func TestMineDeepConsumesQueue(t *testing.T) {
	st := testutil.NewStore(t)
	dir := t.TempDir()
	tp := transcript(dir, sessID+".jsonl")
	writeTranscript(t, tp, deepFixture)

	// 先快挖入队
	cfg := config.Default()
	cfg.LLM = &config.LLMConfig{Endpoint: "http://127.0.0.1:1", Model: "m", TimeoutMs: 500}
	if _, err := Mine(context.Background(), st, cfg, Options{ClaudeDir: dir}); err != nil {
		t.Fatal(err)
	}
	// 追加新行使游标推进，再手动 --deep（force 重置游标全段深挖）
	writeTranscript(t, tp, deepFixture+`{"type":"user","sessionId":"`+sessID+`","timestamp":"2026-09-21T10:02:00+08:00","message":{"role":"user","content":"记得部署前先看 runbook"}}
`)
	srv := deepServer(t, `[]`)
	defer srv.Close()
	cfg.LLM.Endpoint = srv.URL
	cfg.LLM.APIKey = "k"
	if _, err := Mine(context.Background(), st, cfg, Options{ClaudeDir: dir, Force: true, Deep: true}); err != nil {
		t.Fatal(err)
	}
	if got := len(LoadDeepQueue(st.Root).All()); got != 0 {
		t.Fatalf("--deep 深挖过的段应出队: %+v", LoadDeepQueue(st.Root).All())
	}
}

// TestTickDrainsDeepQueue tick 端到端：增量快挖 → 排空 deep 队列 → 候选落 inbox
func TestTickDrainsDeepQueue(t *testing.T) {
	st := testutil.NewStore(t)
	dir := t.TempDir()
	writeTranscript(t, transcript(dir, sessID+".jsonl"), deepFixture)

	srv := deepServer(t, `[{"type":"preference","body":"验证统一走 make constitution，不单跑 go test","quote":"这个项目验证要走 make constitution 才完整，单跑 go test 会漏依赖扫描"}]`)
	defer srv.Close()

	cfg := config.Default()
	cfg.LLM = &config.LLMConfig{Endpoint: srv.URL, Model: "m", APIKey: "k", TimeoutMs: 3000}
	rep, err := Tick(context.Background(), st, cfg, TickOptions{ClaudeDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Mine == nil || rep.Mine.Candidates != 1 {
		t.Fatalf("tick 快挖应产 recap 1: %+v", rep.Mine)
	}
	if rep.DeepDrained != 1 || rep.DeepCandidates != 1 {
		t.Fatalf("tick 应排空 1 段产 1 候选: %+v", rep)
	}
	if rep.DeepPending != 0 {
		t.Fatalf("排空后应无待挖: %+v", rep)
	}
	if rep.DeepBatch == "" {
		t.Fatal("深挖候选应落 inbox 批次")
	}
	cands, err := inbox.New(st).ListCandidates(rep.DeepBatch)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 1 || cands[0].Provenance.Origin != "claude-code·deep" {
		t.Fatalf("深挖候选 origin 应为 claude-code·deep: %+v", cands)
	}
	// 幂等：再 tick 无增量无待挖
	rep2, err := Tick(context.Background(), st, cfg, TickOptions{ClaudeDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if rep2.DeepDrained != 0 || rep2.DeepPending != 0 {
		t.Fatalf("二次 tick 应无深挖动作: %+v", rep2)
	}
}

// TestTickKeepsQueueWithoutKey 密钥不在场：队列保留并披露
func TestTickKeepsQueueWithoutKey(t *testing.T) {
	st := testutil.NewStore(t)
	dir := t.TempDir()
	writeTranscript(t, transcript(dir, sessID+".jsonl"), deepFixture)

	cfg := config.Default()
	cfg.LLM = &config.LLMConfig{Endpoint: "http://127.0.0.1:1", Model: "m", TimeoutMs: 500} // 无 key
	rep, err := Tick(context.Background(), st, cfg, TickOptions{ClaudeDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if rep.DeepDrained != 0 || rep.DeepPending != 1 {
		t.Fatalf("无密钥应保留待挖段: %+v", rep)
	}
	if !strings.Contains(rep.Note, "REMIN_LLM_API_KEY") {
		t.Fatalf("应在备注披露密钥缺失: %q", rep.Note)
	}
}

// TestTickAbstainsOnFailure 端点失败：该段弃权出队（不重试防毒丸），报告披露
func TestTickAbstainsOnFailure(t *testing.T) {
	st := testutil.NewStore(t)
	dir := t.TempDir()
	writeTranscript(t, transcript(dir, sessID+".jsonl"), deepFixture)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := config.Default()
	cfg.LLM = &config.LLMConfig{Endpoint: srv.URL, Model: "m", APIKey: "k", TimeoutMs: 3000}
	rep, err := Tick(context.Background(), st, cfg, TickOptions{ClaudeDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if rep.DeepDrained != 0 || rep.DeepPending != 0 {
		t.Fatalf("弃权段应出队: %+v", rep)
	}
	if rep.DeepAbstained != 1 {
		t.Fatalf("弃权段应计数披露: %+v", rep)
	}
	if !strings.Contains(rep.Note, "弃权") {
		t.Fatalf("弃权应在备注披露: %q", rep.Note)
	}
}

// TestTickRespectsDeepBudget 每 tick 深挖预算：超额段留队列下次 tick 续挖
func TestTickRespectsDeepBudget(t *testing.T) {
	st := testutil.NewStore(t)
	dir := t.TempDir()
	writeTranscript(t, transcript(dir, sessID+"-1.jsonl"), deepFixture)
	writeTranscript(t, transcript(dir, sessID+"-2.jsonl"), strings.ReplaceAll(deepFixture, sessID, sessID+"-2"))
	writeTranscript(t, transcript(dir, sessID+"-3.jsonl"), strings.ReplaceAll(deepFixture, sessID, sessID+"-3"))

	srv := deepServer(t, `[]`)
	defer srv.Close()

	cfg := config.Default()
	cfg.LLM = &config.LLMConfig{Endpoint: srv.URL, Model: "m", APIKey: "k", TimeoutMs: 3000}
	rep, err := Tick(context.Background(), st, cfg, TickOptions{ClaudeDir: dir, DeepMax: 2})
	if err != nil {
		t.Fatal(err)
	}
	if rep.DeepDrained != 2 || rep.DeepPending != 1 {
		t.Fatalf("预算 2 应只挖 2 段留 1 段: %+v", rep)
	}
}

// TestTickDeepSuppressesExistingBodies 与既有 inbox 候选同 body 的深挖候选应抑制（跨批次去重）
func TestTickDeepSuppressesExistingBodies(t *testing.T) {
	st := testutil.NewStore(t)
	dir := t.TempDir()
	// 触发词 fixture：快挖产 typed 候选；深路径返回同 body → 应抑制
	writeTranscript(t, transcript(dir, sessID+".jsonl"), `{"type":"user","sessionId":"`+sessID+`","cwd":"/Users/demo/proj","timestamp":"2026-09-21T10:00:00+08:00","message":{"role":"user","content":"记住：验证统一走 make constitution，不单跑 go test"}}
`)

	srv := deepServer(t, `[{"type":"preference","body":"记住：验证统一走 make constitution，不单跑 go test","quote":"记住：验证统一走 make constitution，不单跑 go test"}]`)
	defer srv.Close()

	cfg := config.Default()
	cfg.LLM = &config.LLMConfig{Endpoint: srv.URL, Model: "m", APIKey: "k", TimeoutMs: 3000}
	rep, err := Tick(context.Background(), st, cfg, TickOptions{ClaudeDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if rep.DeepCandidates != 0 {
		t.Fatalf("与快挖候选同 body 的深挖候选应抑制: %+v", rep)
	}
}

// TestTickPersistsLastReport tick 留档（webui 深挖徽章数据面）：跑一次 tick 后可读回，
// 未跑过时 LoadTickLast 返回 nil（徽章降级为只显待挖数）
func TestTickPersistsLastReport(t *testing.T) {
	st := testutil.NewStore(t)
	if LoadTickLast(st.Root) != nil {
		t.Fatal("未跑过 tick 应无留档")
	}
	dir := t.TempDir()
	writeTranscript(t, transcript(dir, sessID+".jsonl"), deepFixture)
	rep, err := Tick(context.Background(), st, config.Default(), TickOptions{ClaudeDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	last := LoadTickLast(st.Root)
	if last == nil || last.Rep == nil {
		t.Fatal("tick 后应留档")
	}
	if last.TS == "" || last.Rep.Mine == nil || last.Rep.Mine.Candidates != rep.Mine.Candidates {
		t.Fatalf("留档内容失真: %+v", last)
	}
}
