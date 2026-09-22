// 闲时增量 tick：OS 调度器按档拉起的一次性增量维护（无常驻 daemon）。
// 快挖增量（游标断点续挖）→ 排空 deep 待挖队列（P3：深路径只产候选，人审才生效）。
package miner

import (
	"context"
	"fmt"
	"strings"

	"github.com/remin-dev/remin/internal/core/config"
	"github.com/remin-dev/remin/internal/core/extractor"
	"github.com/remin-dev/remin/internal/core/inbox"
	"github.com/remin-dev/remin/internal/store"
)

// DefaultTickDeepMax 每 tick 深挖预算（transcript 段数）：闲时档保持小步快走
const DefaultTickDeepMax = 3

// TickOptions tick 选项
type TickOptions struct {
	ClaudeDir   string
	SinceDays   int // 与 mine 同义（默认窗口防历史洪泛；tick 复发场景由游标保证增量）
	FullHistory bool
	DeepMax     int // 每 tick 最多深挖的段数（<=0 取 DefaultTickDeepMax）
}

// TickReport tick 报告
type TickReport struct {
	Mine           *Report `json:"mine"`
	DeepDrained    int     `json:"deep_drained"`    // 本次深挖成功完成的段数（含空段）
	DeepAbstained  int     `json:"deep_abstained"`  // 弃权段数（端点失败；已出队不重试）
	DeepCandidates int     `json:"deep_candidates"` // 深挖产出的候选数
	DeepBatch      string  `json:"deep_batch,omitempty"`
	DeepPending    int     `json:"deep_pending"` // 剩余待挖段数
	Note           string  `json:"note,omitempty"`
}

// Tick 执行一次增量维护：快挖（内部自带入队）→ 深挖排空（预算内）。
// SinceDays 语义与 mine 一致（0=不限；CLI 层默认 7 防历史洪泛）。
func Tick(ctx context.Context, st *store.Store, cfg *config.Config, opts TickOptions) (*TickReport, error) {
	mineOpts := Options{ClaudeDir: opts.ClaudeDir, SinceDays: opts.SinceDays}
	if opts.FullHistory {
		mineOpts.SinceDays = 0
	}
	rep := &TickReport{}
	mineRep, err := Mine(ctx, st, cfg, mineOpts)
	if err != nil {
		return nil, err
	}
	rep.Mine = mineRep

	err = store.WithRoot(st.Root, func() error {
		drainDeep(ctx, st, cfg, opts, rep)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return rep, nil
}

// drainDeep 排空 deep 待挖队列（调用方须持 WithRoot 锁）。
// 密钥不在场：队列保留 + 披露；段失败：弃权出队（不重试防毒丸）+ 披露；
// 候选先落 inbox 再出队（落批次失败则成功段保留重试，不静默丢候选）。
func drainDeep(ctx context.Context, st *store.Store, cfg *config.Config, opts TickOptions, rep *TickReport) {
	q := LoadDeepQueue(st.Root)
	items := q.All()
	rep.DeepPending = len(items)
	if len(items) == 0 {
		return
	}
	if cfg == nil || cfg.LLM == nil || cfg.LLM.Endpoint == "" || cfg.LLM.APIKey == "" {
		rep.Note = fmt.Sprintf("deep 待挖 %d 段保留：未配置 llm 端点或未设 REMIN_LLM_API_KEY", len(items))
		return
	}
	max := opts.DeepMax
	if max <= 0 {
		max = DefaultTickDeepMax
	}

	in := inbox.New(st)
	existing := existingCandidateBodies(in)
	var deepCands []*inbox.Candidate
	done := map[string]bool{}      // 无条件出队（弃权/失效/空段）
	succeeded := map[string]bool{} // 已产候选，落批次成功后才出队
	var abstained, vanished int
	worked := 0
	for _, it := range items {
		if worked >= max {
			break // 预算到：剩余留队列，下次 tick 续挖（失效段不占预算）
		}
		events, _, err := ParseClaudeJSONL(it.Path, it.FromLine)
		key := deepKey(it.Path, it.FromLine, it.ToLine)
		if err != nil {
			done[key] = true
			vanished++
			continue // 文件已不可读：段失效出队（如实计数披露）
		}
		bounded := events[:0]
		for _, ev := range events {
			if ev.Line <= it.ToLine {
				bounded = append(bounded, ev)
			}
		}
		if len(bounded) == 0 {
			done[key] = true
			rep.DeepDrained++
			continue // 空段（行号漂移/纯工具行）：如实计完成，不占预算
		}
		worked++
		deep, derr := extractor.ExtractDeep(ctx, cfg.LLM, bounded)
		if derr != nil {
			done[key] = true
			abstained++
			continue // 弃权出队（P3：宁弃权不编造；不重试防毒丸段）
		}
		for _, d := range deep {
			if existing[d.Body] {
				continue // 跨批次去重：与既有 inbox 候选同 body 抑制
			}
			deepCands = append(deepCands, d)
			existing[d.Body] = true
		}
		succeeded[key] = true
		rep.DeepDrained++
	}
	if abstained > 0 || vanished > 0 {
		rep.DeepAbstained = abstained
		if vanished > 0 {
			rep.Note = strings.TrimSpace(fmt.Sprintf("失效段 %d（transcript 已不可读，出队）", vanished) + " " + rep.Note)
		}
		rep.Note = strings.TrimSpace(strings.TrimSpace(rep.Note) + fmt.Sprintf(" 深挖弃权 %d 段（端点失败，不重试）", abstained))
	}
	// 候选先落 inbox：成功段出队挂在落批次之后（失败则保留段下次重挖，不静默丢候选）
	if len(deepCands) > 0 {
		batch, _, err := in.AddBatch("mine", deepCands)
		if err != nil {
			rep.Note = strings.TrimSpace(strings.TrimSpace(rep.Note) +
				fmt.Sprintf(" 深挖候选落 inbox 失败（%d 条；对应段保留重试）: %v", len(deepCands), err))
		} else {
			rep.DeepBatch = batch
			rep.DeepCandidates = len(deepCands)
			for k := range succeeded {
				done[k] = true
			}
		}
	} else {
		for k := range succeeded {
			done[k] = true // 无候选产出（全被去重抑制/空返回）：段完成出队
		}
	}
	if err := q.RemoveDone(done); err != nil {
		rep.Note = strings.TrimSpace(strings.TrimSpace(rep.Note) + " 待挖队列出队失败: " + err.Error())
	}
	rep.DeepPending = len(LoadDeepQueue(st.Root).All())
}

// existingCandidateBodies 全部待审批次的候选 body 集（跨批次去重用）
func existingCandidateBodies(in *inbox.Inbox) map[string]bool {
	set := map[string]bool{}
	batches, err := in.ListBatches()
	if err != nil {
		return set
	}
	for _, b := range batches {
		cands, err := in.ListCandidates(b.ID)
		if err != nil {
			continue
		}
		for _, c := range cands {
			set[c.Body] = true
		}
	}
	return set
}
