// Package search 确定性检索引擎（P2-H6）：同索引版本 + 同查询 = 同结果。
// BM25 固定参数与固定排序（分数 desc、id asc）；低置信主动弃权（P3-A5）。
package search

import (
	"math"
	"sort"
	"time"

	"github.com/remin-dev/remin/internal/index"
	"github.com/remin-dev/remin/internal/store"
)

// BM25 固定参数（确定性要求：不许运行时可调）
const (
	k1 = 1.2
	b  = 0.75
	// MinScoreTop 最高分低于此值视为置信不足 → 弃权（宁可不知道）
	MinScore = 0.05
)

// Options 检索选项
type Options struct {
	Facet string // 空 = 不过滤
	TopK  int    // 默认 8
	Now   time.Time
}

// Hit 命中（trust/provenance/verify 随行——agent 必须知道自己吃到的是哪级记忆）
type Hit struct {
	ID           string           `json:"id"`
	Type         string           `json:"type"`
	Facet        string           `json:"facet"`
	Content      string           `json:"content"`
	Trust        string           `json:"trust"`
	Provenance   store.Provenance `json:"provenance"`
	VerifyResult string           `json:"verify_result,omitempty"`
	Score        float64          `json:"score"`
}

// Result 检索结果；abstained 是显式字段而非空数组（空=没有相关记忆，弃权=置信不足）
type Result struct {
	Hits         []Hit  `json:"results"`
	Abstained    bool   `json:"abstained,omitempty"`
	Reason       string `json:"abstain_reason,omitempty"`
	IndexVersion int    `json:"index_version"`
}

// Searcher 一个索引版本的检索器（快照）
type Searcher struct {
	idx   *index.Index
	avgdl float64
	df    map[string]int
	idf   map[string]float64
}

func New(idx *index.Index) *Searcher {
	s := &Searcher{idx: idx, df: map[string]int{}, idf: map[string]float64{}}
	total := 0
	for _, d := range idx.Docs {
		total += d.Len
		for t := range d.Terms {
			s.df[t]++
		}
	}
	if n := len(idx.Docs); n > 0 {
		s.avgdl = float64(total) / float64(n)
	}
	N := float64(len(idx.Docs))
	for t, dfv := range s.df {
		s.idf[t] = math.Log(1 + (N-float64(dfv)+0.5)/(float64(dfv)+0.5))
	}
	return s
}

// visible 可见性：active、未过期（ephemeral）、verify 未失败（stale 注入率 = 0）
func visible(d index.Doc, now time.Time) bool {
	if d.Status != store.StatusActive {
		return false
	}
	if d.VerifyResult == store.VerifyFailed {
		return false
	}
	if d.Expires != "" && d.ReviewedAt != "" && d.ReviewedAt != store.TimeUnknown {
		if dur, ok := store.ExpiryDuration(d.Expires); ok {
			if base, ok := store.ParseTime(d.ReviewedAt); ok && now.After(base.Add(dur)) {
				return false
			}
		}
	}
	return true
}

// Search 执行检索（确定性：同版本+同查询+同 options → 同结果）
func (s *Searcher) Search(query string, opts Options) Result {
	if opts.TopK <= 0 {
		opts.TopK = 8
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	qTerms, _ := index.Tokenize(query)
	var hits []Hit
	for _, d := range s.idx.Docs {
		if !visible(d, opts.Now) {
			continue
		}
		if opts.Facet != "" && d.Facet != opts.Facet {
			continue
		}
		score := 0.0
		for t := range qTerms {
			tf := float64(d.Terms[t])
			if tf == 0 {
				continue
			}
			idf := s.idf[t]
			dl := float64(d.Len)
			norm := s.avgdl
			if norm == 0 {
				norm = 1
			}
			score += idf * (tf * (k1 + 1)) / (tf + k1*(1-b+b*dl/norm))
		}
		if score > 0 {
			hits = append(hits, Hit{
				ID: d.ID, Type: d.Type, Facet: d.Facet, Content: d.Body,
				Trust: d.Trust, Provenance: d.Provenance,
				VerifyResult: d.VerifyResult, Score: round2(score),
			})
		}
	}
	res := Result{Hits: hits, IndexVersion: s.idx.Version}
	if len(hits) == 0 {
		res.Abstained = true
		res.Reason = "no_match"
		return res
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].ID < hits[j].ID
	})
	if hits[0].Score < MinScore {
		res.Hits = nil
		res.Abstained = true
		res.Reason = "low_confidence"
		return res
	}
	if len(hits) > opts.TopK {
		hits = hits[:opts.TopK]
	}
	res.Hits = hits
	return res
}

func round2(f float64) float64 {
	return math.Round(f*100) / 100
}
