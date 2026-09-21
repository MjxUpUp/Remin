// Package importer 记忆迁移引擎（FR-IMP）：一次「只读扫描 → 结构化候选 → inbox 批次审收
// → 原子提交」的受控迁移。全程只读直到用户确认；幂等只报增量；永不覆盖既有条目。
package importer

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/remin-dev/remin/internal/core/inbox"
	"github.com/remin-dev/remin/internal/index"
	"github.com/remin-dev/remin/internal/search"
	"github.com/remin-dev/remin/internal/store"
)

// 来源清单
const (
	SrcClaudeAutoMemory = "claude-auto-memory"
	SrcClaudeMem        = "claude-mem"
	SrcChatGPTExport    = "chatgpt-export"
	SrcCodexMemories    = "codex-memories"
	SrcMarkdownDir      = "markdown-dir"
)

// AllSources 全部支持的来源（发现顺序）
var AllSources = []string{SrcClaudeAutoMemory, SrcClaudeMem, SrcChatGPTExport, SrcCodexMemories, SrcMarkdownDir}

// RawItem 来源适配器读出的原始条目（未归一化）
type RawItem struct {
	OriginID string
	Text     string
	MTime    string // ISO 8601 或空（不可还原）
	Ref      string // 原始定位（文件路径/行/记录 id）
}

// Adapter 来源适配器统一契约：discover（只读扫描）→ read（逐条）
type Adapter interface {
	Name() string
	Discover() ([]RawItem, error)
}

// Report 迁移报告（dry-run 与 apply 共用）
type Report struct {
	Source     string   `json:"source"`
	Found      int      `json:"found"`
	NewItems   int      `json:"new"`
	Skipped    int      `json:"skipped_duplicates"`
	Duplicates int      `json:"similar_in_store"`
	Batch      string   `json:"batch,omitempty"`
	Preview    []string `json:"preview,omitempty"`
}

// NewAdapter 构造来源适配器；markdown-dir 与 chatgpt-export 需 --path
func NewAdapter(source, path, home string) (Adapter, error) {
	switch source {
	case SrcClaudeAutoMemory:
		return &claudeAutoMemory{root: filepath.Join(home, ".claude", "projects")}, nil
	case SrcClaudeMem:
		return &claudeMem{root: filepath.Join(home, ".claude-mem")}, nil
	case SrcChatGPTExport:
		if path == "" {
			return nil, fmt.Errorf("chatgpt-export 需要 --path 指向导出 JSON")
		}
		return &chatgptExport{path: path}, nil
	case SrcCodexMemories:
		return &codexMemories{root: filepath.Join(home, ".codex", "memories")}, nil
	case SrcMarkdownDir:
		if path == "" {
			return nil, fmt.Errorf("markdown-dir 需要 --path 指向手写笔记目录")
		}
		return &markdownDir{root: path}, nil
	}
	return nil, fmt.Errorf("未知来源 %s（可选: %s）", source, strings.Join(AllSources, " / "))
}

// TrustOf 按通道映射信任等级（PRD 记忆模型表）
func TrustOf(source string) string {
	if source == SrcMarkdownDir {
		return store.TrustHumanVerified // 人写的字
	}
	return store.TrustAgentClaimed // 机器记忆源
}

// SourceOf 写入通道类别（全部 import）
const ChannelSource = store.SourceImport

// guessType 关键词启发式类型猜测（默认 semantic）
func guessType(text string) string {
	switch {
	case containsAny(text, "记住这一点", "偏好", "喜欢", "习惯", "prefer", "always", "never", "用中文", "用英文"):
		return store.TypePreference
	case containsAny(text, "必须", "先跑", "步骤", "如何", "别忘", "教训", "坑", "before", "must"):
		return store.TypeProcedural
	case containsAny(text, "决定", "选择", "选了", "而不是", "decided", "chose"):
		return store.TypeDecision
	}
	return store.TypeSemantic
}

func containsAny(s string, subs ...string) bool {
	lower := strings.ToLower(s)
	for _, sub := range subs {
		if strings.Contains(lower, strings.ToLower(sub)) {
			return true
		}
	}
	return false
}

func contentHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:16])
}

// Import 执行迁移：discover → 指纹去幂等 → 归一化 → 重复/冲突建议 → (--apply) inbox 批次
func Import(st *store.Store, source, path string, apply bool, home string) (*Report, error) {
	adapter, err := NewAdapter(source, path, home)
	if err != nil {
		return nil, err
	}
	items, err := adapter.Discover()
	if err != nil {
		return nil, err
	}
	rep := &Report{Source: source, Found: len(items)}
	in := inbox.New(st)
	seen, err := in.LoadFingerprints()
	if err != nil {
		return nil, err
	}

	// 库内相似检测（BM25 自检索；候选→建议，绝不自动裁决）
	var searcher *search.Searcher
	var idx *index.Index
	if ms, err := st.ListMemories(); err == nil {
		v, _ := st.Version()
		idx = index.Build(v, ms)
		searcher = search.New(idx)
	}

	var cands []*inbox.Candidate
	var fps []inbox.Fingerprint
	dupText := map[string]bool{}
	for _, it := range items {
		text := strings.TrimSpace(it.Text)
		if text == "" {
			continue
		}
		originID := it.OriginID
		if originID == "" {
			originID = contentHash(text)
		}
		key := source + "\x00" + originID + "\x00" + contentHash(text)
		if seen[key] || dupText[contentHash(text)] {
			rep.Skipped++
			continue
		}
		dupText[contentHash(text)] = true

		c := &inbox.Candidate{}
		c.Type = guessType(text)
		c.Facet = "dev"
		c.Status = store.StatusCandidate
		c.CapturedAt = it.MTime
		if c.CapturedAt == "" {
			c.CapturedAt = store.TimeUnknown // 不可还原就如实标注，绝不伪造
		}
		c.ReviewedAt = store.TimeUnknown
		c.Modified = store.NowTime()
		c.Trust = TrustOf(source)
		c.Source = ChannelSource
		c.Provenance = store.Provenance{
			Origin: source,
			Ref:    it.Ref,
			Quote:  truncate(text, 400),
		}
		if source == SrcMarkdownDir {
			c.Provenance.Origin = "用户亲笔·" + source
		}
		c.Version = store.FormatVersion
		c.Body = oneLine2(text, 400)

		// 库内相似 → 重复簇；有时间证据且更新 → 冲突建议（supersede 由人裁）。
		// BM25 召回 top-k，词元覆盖率判定（语料规模无关的相似度）。
		if searcher != nil {
			r := searcher.Search(text, search.Options{TopK: 3})
			for _, hit := range r.Hits {
				if coverage(text, docTerms(idx, hit.ID)) < SimilarThreshold {
					continue
				}
				c.DuplicateOf = hit.ID
				c.Group = inbox.GroupDuplicate
				rep.Duplicates++
				if newer, ok := isNewer(c.CapturedAt, docCapturedAt(idx, hit.ID)); ok && newer {
					c.Supersedes = hit.ID
					c.Group = inbox.GroupConflict
				}
				break
			}
		}
		cands = append(cands, c)
		fps = append(fps, inbox.Fingerprint{Source: source, OriginID: originID,
			ContentHash: contentHash(text), ImportedAt: store.NowTime()})
		rep.NewItems++
		if len(rep.Preview) < 10 {
			rep.Preview = append(rep.Preview, truncate(c.Body, 60))
		}
	}

	if !apply || len(cands) == 0 {
		return rep, nil
	}
	// 批次落盘 + 指纹台账 + 导入提交为一个临界区（互斥下 add -A 不会扫入并发半成品）
	err = store.WithRoot(st.Root, func() error {
		batch, _, err := in.AddBatch("import:"+source, cands)
		if err != nil {
			return err
		}
		rep.Batch = batch
		if err := in.AppendFingerprints(fps); err != nil {
			return err
		}
		if _, err := store.GitCommit(st.Root, fmt.Sprintf("import: %s 批次 %s（%d 条候选待审）", source, batch, len(cands))); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return rep, nil
}

// SimilarThreshold 词元覆盖率阈值（重复簇判定；可调参数，不在检索确定性路径上）
const SimilarThreshold = 0.6

// coverage 查询词元在库内条目中的覆盖率（0..1）
func coverage(query string, docTerms map[string]int) float64 {
	if len(docTerms) == 0 {
		return 0
	}
	qt, _ := index.Tokenize(query)
	if len(qt) == 0 {
		return 0
	}
	matched := 0
	for t := range qt {
		if docTerms[t] > 0 {
			matched++
		}
	}
	return float64(matched) / float64(len(qt))
}

// docTerms 从索引取条目词元集
func docTerms(idx *index.Index, id string) map[string]int {
	if idx == nil {
		return nil
	}
	for _, d := range idx.Docs {
		if d.ID == id {
			return d.Terms
		}
	}
	return nil
}

// docCapturedAt 从索引取库内条目的 captured_at（双 unknown → 冲突交人裁）
func docCapturedAt(idx *index.Index, id string) string {
	if idx == nil {
		return ""
	}
	for _, d := range idx.Docs {
		if d.ID == id {
			return d.CapturedAt
		}
	}
	return ""
}

// isNewer 候选时间是否晚于库内时间（双 unknown → false，交人裁）
func isNewer(candTS, storeTS string) (bool, bool) {
	ct, ok1 := store.ParseTime(candTS)
	st2, ok2 := store.ParseTime(storeTS)
	if !ok1 || !ok2 {
		return false, false
	}
	return ct.After(st2), true
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func oneLine2(s string, n int) string {
	if i := strings.Index(s, "\n"); i >= 0 {
		s = s[:i]
	}
	return truncate(strings.TrimSpace(s), n)
}
