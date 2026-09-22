package eval

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/remin-dev/remin/internal/core/inject"
	"github.com/remin-dev/remin/internal/store"
)

// uplift 契约：同一任务集「空库基线 vs 带记忆」的差值可归因（recall 全中/基线零中）；
// 垃圾查询不算提升；superseded 不进结果；注入面同命中。真源模式只读检索面。
// 历史逐次落 history.jsonl，趋势含首跑/上跑 Δ（纵向衰减曲线的数据形态）。

func TestSuiteUpliftSandbox(t *testing.T) {
	rep := SuiteUplift()
	if !rep.Passed {
		for _, c := range rep.Checks {
			if !c.Passed {
				t.Errorf("✗ %s — %s", c.Name, c.Detail)
			}
		}
		t.Fatal("沙盒 uplift 套件应全过")
	}
	var hasRecall, hasBaseline, hasAbstain, hasStale, hasInject bool
	for _, c := range rep.Checks {
		switch c.Name {
		case "uplift_recall_with_memory":
			hasRecall = true
		case "baseline_without_memory_zero":
			hasBaseline = true
		case "abstain_garbage_not_counted":
			hasAbstain = true
		case "stale_superseded_not_hit":
			hasStale = true
		case "inject_surface_hits":
			hasInject = true
		}
	}
	if !hasRecall || !hasBaseline || !hasAbstain || !hasStale || !hasInject {
		t.Fatalf("五项指标应齐备: %+v", rep.Checks)
	}
}

// TestRunUpliftTasksAgainstStore 真源模式：任务命中/弃权/未中三态如实计量
func TestRunUpliftTasksAgainstStore(t *testing.T) {
	st, ids, err := buildFixture()
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(st.Root)
	tasks := []UpliftTask{
		{Query: "用户 主力 语言", ExpectID: ids["active"]},
		{Query: "球队 主力 前锋", ExpectID: ids["supNew"]},
		{Query: "完全无关 zzxxqq", ExpectAbstain: true},
	}
	m := RunUpliftTasks(st, tasks)
	if m.Tasks != 3 || m.Hits != 2 || m.AbstainCorrect != 1 {
		t.Fatalf("应 2 中 1 弃权: %+v", m)
	}
	if m.Recall != 2.0/2.0 { // 分母只计非弃权任务
		t.Fatalf("recall 应为 1.0: %v", m.Recall)
	}
	for _, r := range m.Results {
		if r.Query == "球队 主力 前锋" && strings.Contains(strings.Join(r.GotIDs, ","), ids["supOld"]) {
			t.Fatalf("superseded 旧事实不得进结果: %+v", r)
		}
	}
}

// TestRunUpliftTasksEmptyStoreBaseline 空库基线：正命中任务全未中（提升可归因）
func TestRunUpliftTasksEmptyStoreBaseline(t *testing.T) {
	st, err := newEvalStore()
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(st.Root)
	m := RunUpliftTasks(st, []UpliftTask{
		{Query: "用户 主力 语言", ExpectContains: "Rust"},
		{Query: "球队 前锋", ExpectContains: "Messi"},
	})
	if m.Hits != 0 {
		t.Fatalf("空库基线应零命中: %+v", m)
	}
	if m.Recall != 0 {
		t.Fatalf("空库 recall 应为 0: %v", m.Recall)
	}
}

// TestUpliftExpectContains 子串期望形态 + 衰减信号：任务集写成时 OAuth 记忆有效，
// verify 失败后系统正确退出该记忆并弃权——任务从「命中」衰减为 wrong-abstain
// （期望命中却拿不到），这正是纵向曲线要捕捉的衰减形态，如实计数而非误判
func TestUpliftExpectContains(t *testing.T) {
	st, _, err := buildFixture()
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(st.Root)
	m := RunUpliftTasks(st, []UpliftTask{{Query: "登录 认证 方式", ExpectContains: "OAuth"}})
	if m.Hits != 0 || m.WrongAbstains != 1 || m.Misses != 0 {
		t.Fatalf("verify-failed 记忆退出检索后应计 wrong-abstain（衰减信号）: %+v", m)
	}
}

// TestLoadUpliftTasks 任务文件解析（JSONL）
func TestLoadUpliftTasks(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "tasks.jsonl")
	os.WriteFile(p, []byte(`{"query":"部署 注意事项","expect_contains":"迁移"}
{"query":"无关垃圾查询","expect_abstain":true}
`), 0o644)
	tasks, err := LoadUpliftTasks(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 || !tasks[1].ExpectAbstain || tasks[0].ExpectContains != "迁移" {
		t.Fatalf("解析错: %+v", tasks)
	}
	// 空行容错 + 坏行报错
	os.WriteFile(p, []byte("\n{bad json\n"), 0o644)
	if _, err := LoadUpliftTasks(p); err == nil {
		t.Fatal("坏行应报错（任务集是评测真值，不容静默丢题）")
	}
}

// TestHistoryRoundtripAndTrend 历史读写与趋势渲染（衰减曲线数据形态）
func TestHistoryRoundtripAndTrend(t *testing.T) {
	st, err := newEvalStore()
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(st.Root)
	if err := AppendHistory(st.Root, HistoryRun{TS: "2026-09-01T10:00:00+08:00", Mode: "store", Tasks: 10, Hits: 9, Abstains: 1, Recall: 0.9}); err != nil {
		t.Fatal(err)
	}
	if err := AppendHistory(st.Root, HistoryRun{TS: "2026-10-01T10:00:00+08:00", Mode: "store", Tasks: 10, Hits: 7, Abstains: 1, Recall: 0.7}); err != nil {
		t.Fatal(err)
	}
	runs, err := LoadHistory(st.Root)
	if err != nil || len(runs) != 2 {
		t.Fatalf("历史应 2 条: %v %v", runs, err)
	}
	out := RenderHistory(runs, 10)
	if !strings.Contains(out, "0.90") || !strings.Contains(out, "0.70") {
		t.Fatalf("趋势应含各次 recall: %s", out)
	}
	if !strings.Contains(out, "-0.20") { // 相对首跑衰减可见
		t.Fatalf("趋势应含相对首跑 Δ: %s", out)
	}
}

// TestSuiteUpliftInAll uplift 入 all（机制正确性随 CI 常跑）
func TestSuiteUpliftInAll(t *testing.T) {
	reps, err := Run("all", "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range reps {
		if r.Suite == "uplift" {
			found = true
		}
	}
	if !found {
		t.Fatalf("all 应含 uplift: %+v", reps)
	}
}

// 编译期锚：inject 面在套件内被真实消费（防未来重构把注入面检查静默丢掉）
var _ = inject.Run
var _ = store.WithRoot
var _ = json.Marshal
var _ = context.Background

// TestUpliftFalseHitCounted 期望弃权却命中：false-hit 独立计数（校准缺口信号，
// 审查 P2-3 修复的杀灭测试——不再与 wrong-abstain 混用同一状态名）
func TestUpliftFalseHitCounted(t *testing.T) {
	st, ids, err := buildFixture()
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(st.Root)
	// 主力语言查询必然命中 active——把它当弃权题即构造 false-hit
	m := RunUpliftTasks(st, []UpliftTask{
		{Query: "用户 主力 语言", ExpectAbstain: true},
		{Query: "用户 主力 语言", ExpectID: ids["active"]},
	})
	if m.FalseHits != 1 || m.WrongAbstains != 0 || m.Hits != 1 {
		t.Fatalf("false-hit 应独立计数: %+v", m)
	}
	if m.Tasks != m.Hits+m.Misses+m.AbstainCorrect+m.WrongAbstains+m.FalseHits {
		t.Fatalf("计数恒等式应成立: %+v", m)
	}
	for _, r := range m.Results {
		if r.Query == "用户 主力 语言" && r.Pass && r.State == "abstain" {
			t.Fatalf("命中被误判弃权")
		}
	}
}

// TestUpliftRecallZeroWithWrongAbstain wrong-abstain 进 recall 分母（recall=0 钉死）
func TestUpliftRecallZeroWithWrongAbstain(t *testing.T) {
	st, _, err := buildFixture()
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(st.Root)
	m := RunUpliftTasks(st, []UpliftTask{{Query: "登录 认证 方式", ExpectContains: "OAuth"}})
	if m.WrongAbstains != 1 || m.Recall != 0 {
		t.Fatalf("期望命中却弃权应压低 recall 到 0: %+v", m)
	}
}

// TestRenderHistoryWindowing 窗口化时 Δ上跑取窗口前一跑（审查 P2-1 杀灭测试）
func TestRenderHistoryWindowing(t *testing.T) {
	runs := []HistoryRun{
		{TS: "t1", Recall: 0.9},
		{TS: "t2", Recall: 0.6},
		{TS: "t3", Recall: 0.8},
	}
	out := RenderHistory(runs, 2) // 显示 t2 t3
	if !strings.Contains(out, "t3") || strings.Contains(out, "t1\n") == false && !strings.Contains(out, "历史 3 次（显示最近 2 次）") {
		t.Fatalf("窗口语义: %s", out)
	}
	// t3 的 Δ上跑 = 0.8-0.6 = +0.20（不是相对 t1 的 -0.10）
	if !strings.Contains(out, "Δ上跑 +0.20") {
		t.Fatalf("Δ上跑 应相对窗口前一跑（t2）: %s", out)
	}
}

// TestRenderHistoryEmpty 空历史提示
func TestRenderHistoryEmpty(t *testing.T) {
	out := RenderHistory(nil, 10)
	if !strings.Contains(out, "暂无历史") {
		t.Fatalf("空历史应提示: %s", out)
	}
}

// TestLoadHistoryCorruptFails 历史坏行报错（衰减数据不容静默丢行）
func TestLoadHistoryCorruptFails(t *testing.T) {
	st, err := newEvalStore()
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(st.Root)
	if err := AppendHistory(st.Root, HistoryRun{TS: "t1", Mode: "store", Recall: 0.5}); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(st.Root+"/eval", 0o755)
	os.WriteFile(st.Root+"/eval/history.jsonl", []byte("{corrupt\n"), 0o644)
	if _, err := LoadHistory(st.Root); err == nil {
		t.Fatal("坏行应报错")
	}
}

// TestLoadUpliftTasksMutualExclusion expect_abstain 与命中期望互斥
func TestLoadUpliftTasksMutualExclusion(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "t.jsonl")
	os.WriteFile(p, []byte(`{"query":"q","expect_abstain":true,"expect_id":"mem_X"}`+"\n"), 0o644)
	if _, err := LoadUpliftTasks(p); err == nil || !strings.Contains(err.Error(), "互斥") {
		t.Fatalf("互斥校验应报错: %v", err)
	}
}

// TestLoadUpliftTasksValidationStates 校验三态杀灭（mutation 存活位点 uplift.go:73
// ||→&&：空 query 有期望 / 有 query 缺期望两态都必须独立报错，缺一即被变异钻过）
func TestLoadUpliftTasksValidationStates(t *testing.T) {
	dir := t.TempDir()
	// 有 query 缺期望
	p1 := filepath.Join(dir, "no_expect.jsonl")
	os.WriteFile(p1, []byte(`{"query":"部署"}`+"\n"), 0o644)
	if _, err := LoadUpliftTasks(p1); err == nil || !strings.Contains(err.Error(), "缺期望") {
		t.Fatalf("缺期望应报错: %v", err)
	}
	// 空 query 有期望（单独即报错——不被「缺期望」分支掩盖）
	p2 := filepath.Join(dir, "no_query.jsonl")
	os.WriteFile(p2, []byte(`{"expect_contains":"迁移"}`+"\n"), 0o644)
	if _, err := LoadUpliftTasks(p2); err == nil {
		t.Fatal("空 query 应报错")
	}
}
