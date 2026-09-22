// uplift 评测（端到端任务提升 + 纵向衰减曲线的数据面）：
// 同一任务集在「空库基线」与「带记忆库」上跑，差值归因记忆贡献；垃圾查询弃权不算提升；
// superseded 不进结果；注入面同命中。真源模式（用户自备任务集）只读检索面，
// 逐次结果落 <root>/eval/history.jsonl——衰减曲线随真实使用自然累积（R4：规则可判定）。
package eval

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/remin-dev/remin/internal/core/inject"
	"github.com/remin-dev/remin/internal/search"
	"github.com/remin-dev/remin/internal/store"
)

// UpliftTask 一道评测题（真实任务集形态：期望 ID / 期望子串 / 期望弃权三选一）
type UpliftTask struct {
	Query          string `json:"query"`
	Facet          string `json:"facet,omitempty"`
	ExpectID       string `json:"expect_id,omitempty"`
	ExpectContains string `json:"expect_contains,omitempty"`
	ExpectAbstain  bool   `json:"expect_abstain,omitempty"`
	TopK           int    `json:"top_k,omitempty"` // 缺省 3
}

// TaskResult 单题结果（如实：命中/未中/弃权三态 + 实得 ID 集）
type TaskResult struct {
	Query  string   `json:"query"`
	Pass   bool     `json:"pass"`
	State  string   `json:"state"` // hit | miss | abstain | wrong-abstain
	GotIDs []string `json:"got_ids,omitempty"`
	Detail string   `json:"detail,omitempty"`
}

// UpliftMetrics 汇总指标（Tasks = Hits+Misses+AbstainCorrect+WrongAbstains+FalseHits 恒等）
type UpliftMetrics struct {
	Tasks          int          `json:"tasks"`
	Hits           int          `json:"hits"`
	Misses         int          `json:"misses"`
	AbstainCorrect int          `json:"abstain_correct"`
	WrongAbstains  int          `json:"wrong_abstains"` // 期望命中却弃权：宁可不知道的最坏违反面（衰减信号）
	FalseHits      int          `json:"false_hits"`     // 期望弃权却命中：低置信放行（校准缺口信号）
	Recall         float64      `json:"recall"`         // 命中 /（命中+未中+期望命中却弃权），弃权题不计分母
	IndexErr       string       `json:"index_err,omitempty"`
	Results        []TaskResult `json:"results"`
}

// LoadUpliftTasks 读任务文件（JSONL；坏行报错——任务集是评测真值，不容静默丢题）
func LoadUpliftTasks(path string) ([]UpliftTask, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var tasks []UpliftTask
	sc := bufio.NewScanner(f)
	line := 0
	for sc.Scan() {
		line++
		raw := strings.TrimSpace(sc.Text())
		if raw == "" {
			continue
		}
		var t UpliftTask
		if err := json.Unmarshal([]byte(raw), &t); err != nil {
			return nil, fmt.Errorf("任务文件 %s 第 %d 行非 JSON: %w", path, line, err)
		}
		if t.Query == "" || (t.ExpectID == "" && t.ExpectContains == "" && !t.ExpectAbstain) {
			return nil, fmt.Errorf("任务文件 %s 第 %d 行缺期望（expect_id/expect_contains/expect_abstain 三选一）", path, line)
		}
		if t.ExpectAbstain && (t.ExpectID != "" || t.ExpectContains != "") {
			return nil, fmt.Errorf("任务文件 %s 第 %d 行 expect_abstain 与命中期望互斥", path, line)
		}
		tasks = append(tasks, t)
	}
	return tasks, sc.Err()
}

// RunUpliftTasks 在给定真源上跑任务集（不写记忆真源；索引缓存缺失时按需重建本地
// 派生缓存 index/bm25-<v>，gitignored 可重建物）
func RunUpliftTasks(st *store.Store, tasks []UpliftTask) *UpliftMetrics {
	m := &UpliftMetrics{Tasks: len(tasks)}
	se, err := searcher(st)
	if err != nil {
		m.IndexErr = err.Error() // 基建失败≠任务未中：如实区分，防误记为衰减
		for _, t := range tasks {
			m.Results = append(m.Results, TaskResult{Query: t.Query, State: "miss", Detail: "索引不可用: " + err.Error()})
		}
		m.Misses = len(tasks)
		return m
	}
	scored := 0 // recall 分母 = 命中 + 未中 + 期望命中却弃权（弃权题不入分母）
	for _, t := range tasks {
		k := t.TopK
		if k <= 0 {
			k = 3
		}
		res := se.Search(t.Query, search.Options{Facet: t.Facet, TopK: k})
		var got []string
		for _, h := range res.Hits {
			got = append(got, h.ID)
		}
		r := TaskResult{Query: t.Query, GotIDs: got}
		switch {
		case t.ExpectAbstain:
			if res.Abstained || len(res.Hits) == 0 {
				r.State, r.Pass = "abstain", true
				m.AbstainCorrect++
			} else {
				r.State = "false-hit" // 期望弃权却命中：低置信放行（校准缺口，如实独立计数）
				r.Detail = fmt.Sprintf("期望弃权，实得 %v", got)
				m.FalseHits++
			}
		case res.Abstained:
			r.State = "wrong-abstain" // 期望命中却弃权：宁可不知道的最坏违反面
			r.Detail = "期望命中，检索弃权: " + res.Reason
			m.WrongAbstains++
			scored++
		default:
			scored++
			if matchTask(t, res.Hits) {
				r.State, r.Pass = "hit", true
				m.Hits++
			} else {
				r.State = "miss"
				r.Detail = fmt.Sprintf("期望 %s，实得 %v", taskExpect(t), got)
				m.Misses++
			}
		}
		m.Results = append(m.Results, r)
	}
	if scored > 0 {
		m.Recall = float64(m.Hits) / float64(scored)
	}
	return m
}

func taskExpect(t UpliftTask) string {
	if t.ExpectID != "" {
		return t.ExpectID
	}
	return "含「" + t.ExpectContains + "」"
}

// matchTask 期望命中判定：expect_id 命中 ID，或 expect_contains 命中任一结果正文
func matchTask(t UpliftTask, hits []search.Hit) bool {
	for _, h := range hits {
		if t.ExpectID != "" && h.ID == t.ExpectID {
			return true
		}
		if t.ExpectContains != "" && strings.Contains(h.Content, t.ExpectContains) {
			return true
		}
	}
	return false
}

// SuiteUplift 沙盒 uplift 套件（入 all）：机制正确性的端到端任务面
func SuiteUplift() *Report {
	return run("uplift", func() []Check {
		st, ids, err := buildFixture()
		if st != nil {
			defer os.RemoveAll(st.Root)
		}
		if err != nil {
			return []Check{check("fixture", false, err.Error())}
		}
		tasks := []UpliftTask{
			{Query: "用户 主力 语言", ExpectID: ids["active"]},
			{Query: "球队 主力 前锋", ExpectID: ids["supNew"]},
			{Query: "完全无关的垃圾查询 zzxxqq", ExpectAbstain: true},
		}
		withMem := RunUpliftTasks(st, tasks)

		empty, err := newEvalStore()
		if err == nil {
			defer os.RemoveAll(empty.Root)
		}
		base := &UpliftMetrics{}
		if err == nil {
			base = RunUpliftTasks(empty, tasks)
		}

		inj, err := inject.Run(st, inject.Options{Facet: "dev"})
		injOK := err == nil && strings.Contains(inj.Text, ids["active"])

		staleHit := ""
		for _, r := range withMem.Results {
			if strings.Contains(strings.Join(r.GotIDs, " "), ids["supOld"]) {
				staleHit = r.Query
			}
		}
		return []Check{
			check("uplift_recall_with_memory", withMem.Hits == 2 && withMem.AbstainCorrect == 1,
				fmt.Sprintf("命中 %d/%d 弃权 %d： %+v", withMem.Hits, withMem.Tasks, withMem.AbstainCorrect, withMem.Results)),
			check("baseline_without_memory_zero", base.Hits == 0,
				fmt.Sprintf("空库基线命中 %d（差值可归因记忆贡献）", base.Hits)),
			check("abstain_garbage_not_counted", withMem.AbstainCorrect >= 1 && withMem.Recall == 1.0,
				fmt.Sprintf("垃圾查询弃权不计入 recall 分母（recall=%.2f 应为满分）", withMem.Recall)),
			check("stale_superseded_not_hit", staleHit == "",
				fmt.Sprintf("superseded 旧事实出现在结果（查询 %q）", staleHit)),
			check("inject_surface_hits", injOK,
				"注入面应含期望记忆（检索面之外的第二个消费面）"),
		}
	})
}

// ── 纵向历史（衰减曲线的数据形态）────────────────────────────────────────────

// HistoryRun 一次实测记录（<root>/eval/history.jsonl 逐行；随下次审收提交入库，
// 索引缓存等派生物不入 git）
type HistoryRun struct {
	TS            string  `json:"ts"`
	Mode          string  `json:"mode"` // store（真源实测）| sandbox
	Tasks         int     `json:"tasks"`
	Hits          int     `json:"hits"`
	Misses        int     `json:"misses"`
	Abstains      int     `json:"abstains"`
	WrongAbstains int     `json:"wrong_abstains,omitempty"` // 期望命中却弃权：衰减信号随历史可见
	FalseHits     int     `json:"false_hits,omitempty"`     // 期望弃权却命中：校准缺口信号
	Recall        float64 `json:"recall"`
	Note          string  `json:"note,omitempty"`
}

func historyPath(root string) string { return filepath.Join(root, "eval", "history.jsonl") }

// AppendHistory 追加一次运行（调用方负责 WithRoot 互斥）
func AppendHistory(root string, r HistoryRun) error {
	if r.TS == "" {
		r.TS = time.Now().Format("2006-01-02T15:04:05-07:00")
	}
	if err := os.MkdirAll(filepath.Dir(historyPath(root)), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(historyPath(root), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	data, _ := json.Marshal(r)
	_, err = f.Write(append(data, '\n'))
	return err
}

// LoadHistory 读全部历史（时间升序 = 追加序）
func LoadHistory(root string) ([]HistoryRun, error) {
	data, err := os.ReadFile(historyPath(root))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var runs []HistoryRun
	for i, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var r HistoryRun
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			return nil, fmt.Errorf("历史 %s 第 %d 行损坏（衰减数据不容静默丢行）: %w", historyPath(root), i+1, err)
		}
		runs = append(runs, r)
	}
	return runs, nil
}

// RenderHistory 趋势文本形态（各次 recall + 相对首跑/上跑 Δ——衰减一眼可见）
func RenderHistory(runs []HistoryRun, limit int) string {
	if len(runs) == 0 {
		return "暂无历史（remin eval uplift --tasks <file> --record 逐次累积）"
	}
	if limit <= 0 || limit > len(runs) {
		limit = len(runs)
	}
	shown := runs[len(runs)-limit:]
	var sb strings.Builder
	fmt.Fprintf(&sb, "历史 %d 次（显示最近 %d 次）\n", len(runs), limit)
	first := runs[0].Recall
	// Δ上跑基准取窗口前一跑（窗口化时不是首跑——否则边界行 Δ 静默算错）
	prev := first
	if limit < len(runs) {
		prev = runs[len(runs)-limit-1].Recall
	}
	for _, r := range shown {
		deltaFirst := r.Recall - first
		deltaPrev := r.Recall - prev
		fmt.Fprintf(&sb, "  %s  recall %.2f（Δ首跑 %+0.2f Δ上跑 %+0.2f）tasks %d hits %d abstain %d",
			r.TS, r.Recall, deltaFirst, deltaPrev, r.Tasks, r.Hits, r.Abstains)
		if r.WrongAbstains > 0 || r.FalseHits > 0 {
			fmt.Fprintf(&sb, " · wrong-abstain %d false-hit %d", r.WrongAbstains, r.FalseHits)
		}
		if r.Note != "" {
			fmt.Fprintf(&sb, " · %s", r.Note)
		}
		sb.WriteByte('\n')
		prev = r.Recall
	}
	return sb.String()
}
