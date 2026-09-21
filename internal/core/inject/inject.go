// Package inject SessionStart 注入（FR-READ-1）：排空队列追赶（硬预算，超时降级异步）
// → 快照 → 精简索引（约 200 行纪律）+ recap 候选提示。永不失败。
package inject

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/remin-dev/remin/internal/core/inbox"
	"github.com/remin-dev/remin/internal/store"
)

// 默认注入预算（行数纪律 ~200 行）
const DefaultMaxLines = 200

// 类型权重（重要性排序：类型 × recency；确定性）
var typeWeight = map[string]float64{
	store.TypePreference: 1.3,
	store.TypeDecision:   1.25,
	store.TypeProcedural: 1.2,
	store.TypeSemantic:   1.1,
	store.TypeEpisodic:   1.0,
}

// Result 注入产物
type Result struct {
	Version  int    `json:"version"`
	Text     string `json:"-"`
	Lines    int    `json:"lines"`
	Drained  int    `json:"drained"`               // 追赶排空的 transcript 数
	TimedOut bool   `json:"timed_out,omitempty"`   // 追赶超时（降级异步，不影响注入）
	DrainErr string `json:"drain_error,omitempty"` // 追赶失败原因（如实上报，注入继续）
}

// Options 注入参数
type Options struct {
	Facet    string
	MaxLines int
	Budget   time.Duration // 追赶硬预算（默认 800ms）
	Drain    func(ctx context.Context) (int, error)
	Now      time.Time
}

// Run 生成注入产物（不写 stdout；输出由调用方决定）
func Run(st *store.Store, opts Options) (*Result, error) {
	if opts.MaxLines <= 0 {
		opts.MaxLines = DefaultMaxLines
	}
	if opts.Budget <= 0 {
		opts.Budget = 800 * time.Millisecond
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	res := &Result{}

	// 1. 追赶：排空队列（硬预算；超时降级——剩余留给异步/下次）
	if opts.Drain != nil {
		ctx, cancel := context.WithTimeout(context.Background(), opts.Budget)
		n, err := opts.Drain(ctx)
		res.Drained = n
		if ctx.Err() == context.DeadlineExceeded {
			res.TimedOut = true // 预算到：降级异步，剩余留队列
		} else if err != nil {
			res.DrainErr = err.Error() // 失败如实上报（--json 可见），注入本身继续
		}
		cancel()
	}

	// 2. 快照 + 注入集
	v, err := st.Version()
	if err != nil {
		return nil, err
	}
	res.Version = v
	ms, err := st.ListMemories()
	if err != nil {
		return nil, err
	}
	type entry struct {
		m    *store.Memory
		rank float64
	}
	var entries []entry
	for _, m := range ms {
		if !injectable(m, opts.Now) {
			continue
		}
		if opts.Facet != "" && m.Facet != opts.Facet {
			continue
		}
		entries = append(entries, entry{m, rank(m, opts.Now)})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].rank != entries[j].rank {
			return entries[i].rank > entries[j].rank
		}
		return entries[i].m.ID < entries[j].m.ID
	})
	if len(entries) > opts.MaxLines {
		entries = entries[:opts.MaxLines]
	}

	// 3. 渲染
	var b strings.Builder
	fmt.Fprintf(&b, "# Remin 记忆索引（v%d", res.Version)
	if opts.Facet != "" {
		fmt.Fprintf(&b, "，facet=%s", opts.Facet)
	}
	b.WriteString("）\n")
	b.WriteString("> 以下为你的长期记忆精简索引；详情用 MCP 工具 memory_search 检索。\n")
	b.WriteString("> 每条含信任分级：[人审]=human-verified，[agent 断言]，[未验证]。\n\n")
	for _, e := range entries {
		fmt.Fprintf(&b, "%s · %s · %s · [%s]\n", e.m.ID, e.m.Type, summarize(e.m.Body), store.TrustLabel(e.m.Trust))
		res.Lines++
	}

	// 4. recap 候选提示（一键采纳指引）
	recaps := recapBatches(st)
	if len(recaps) > 0 {
		b.WriteString("\n## 上一会话交接候选（一键采纳）\n")
		for _, rb := range recaps {
			fmt.Fprintf(&b, "- `remin inbox --batch %s`（`remin promote --batch %s --all` 采纳）\n", rb, rb)
		}
	}
	res.Text = b.String()
	return res, nil
}

func injectable(m *store.Memory, now time.Time) bool {
	return m.Searchable() && !m.ExpiredAt(now) // 可见性策略单一来源：store.Memory.Searchable
}

// rank 重要性 = 类型权重 × recency（确定性）
func rank(m *store.Memory, now time.Time) float64 {
	w := typeWeight[m.Type]
	if w == 0 {
		w = 1.0
	}
	recency := 1.0
	if t, ok := store.ParseTime(m.Modified); ok {
		days := now.Sub(t).Hours() / 24
		if days < 0 {
			days = 0
		}
		recency = 1.0 / (1.0 + days/30.0)
	}
	return w * recency
}

func summarize(body string) string {
	s := strings.TrimSpace(body)
	if i := strings.IndexAny(s, "\n"); i >= 0 {
		s = s[:i]
	}
	r := []rune(s)
	if len(r) > 80 {
		r = r[:80]
	}
	return strings.TrimSpace(string(r))
}

// recapBatches 有 ephemeral recap 候选待审的批次
func recapBatches(st *store.Store) []string {
	in := inbox.New(st)
	batches, err := in.ListBatches()
	if err != nil {
		return nil
	}
	var out []string
	for _, b := range batches {
		if b.Status == "done" {
			continue
		}
		cands, err := in.ListCandidates(b.ID)
		if err != nil {
			continue
		}
		for _, c := range cands {
			if c.Expires != "" {
				out = append(out, b.ID)
				break
			}
		}
	}
	return out
}
