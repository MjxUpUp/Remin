// Package index 版本化 BM25 索引（P2-H6 确定性检索；P4-R1 可重建加速层）。
// index/bm25-<ver>.json 不入 git；损坏/缺失由真源全量重建，用户无感。
package index

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/remin-dev/remin/internal/store"
)

// Doc 索引文档：检索所需的完整投影（快照服务不依赖工作区文件）
type Doc struct {
	ID           string           `json:"id"`
	Type         string           `json:"type"`
	Facet        string           `json:"facet"`
	Context      []string         `json:"context,omitempty"`
	Status       string           `json:"status"`
	Trust        string           `json:"trust"`
	Body         string           `json:"body"`
	Provenance   store.Provenance `json:"provenance"`
	VerifyResult string           `json:"verify_result,omitempty"`
	Expires      string           `json:"expires,omitempty"`
	CapturedAt   string           `json:"captured_at,omitempty"`
	ReviewedAt   string           `json:"reviewed_at"`
	Modified     string           `json:"modified"`
	Terms        map[string]int   `json:"terms"`
	Len          int              `json:"len"`
}

// Index 一个版本的全量索引
type Index struct {
	Version int    `json:"version"`
	BuiltAt string `json:"built_at"`
	Docs    []Doc  `json:"docs"`
}

// Build 由真源记忆构建索引（确定性：ListMemories 已按 id 排序；分词与参数固定）
func Build(version int, memories []*store.Memory) *Index {
	idx := &Index{Version: version, BuiltAt: store.NowTime()}
	for _, m := range memories {
		terms, length := Tokenize(m.Body + " " + strings.Join(m.Context, " "))
		vr := ""
		if m.Verify != nil {
			vr = m.Verify.Result
		}
		idx.Docs = append(idx.Docs, Doc{
			ID: m.ID, Type: m.Type, Facet: m.Facet, Context: m.Context,
			Status: m.Status, Trust: m.Trust, Body: m.Body,
			Provenance: m.Provenance, VerifyResult: vr, Expires: m.Expires,
			CapturedAt: m.CapturedAt, ReviewedAt: m.ReviewedAt, Modified: m.Modified,
			Terms: terms, Len: length,
		})
	}
	return idx
}

// Persist 落盘 index/bm25-<ver>.json（可重建加速层，不入 git）
func (idx *Index) Persist(root string) error {
	dir := filepath.Join(root, "index")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(idx)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, fmt.Sprintf("bm25-%d.json", idx.Version)), data, 0o644)
}

// LoadExact 读指定版本索引文件；不存在返回错误（调用方决定是否重建）
func LoadExact(root string, version int) (*Index, error) {
	path := filepath.Join(root, "index", fmt.Sprintf("bm25-%d.json", version))
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("索引 v%d 不存在: %w", version, err)
	}
	var idx Index
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("索引 v%d 损坏: %w", version, err)
	}
	return &idx, nil
}

// Ensure 取指定版本索引：优先读文件；缺失且即当前版本时由真源重建并落盘。
// 这是「索引损坏/缺失由真源全量重建」的落点。
func Ensure(st *store.Store, version int) (*Index, error) {
	if idx, err := LoadExact(st.Root, version); err == nil {
		return idx, nil
	}
	cur, err := st.Version()
	if err != nil {
		return nil, err
	}
	if version != cur {
		return nil, fmt.Errorf("索引 v%d 缺失且非当前版本（v%d），无法重建快照", version, cur)
	}
	ms, err := st.ListMemories()
	if err != nil {
		return nil, err
	}
	idx := Build(version, ms)
	if err := idx.Persist(st.Root); err != nil {
		return nil, err
	}
	return idx, nil
}

// Tokenize 固定分词器（确定性根基）：
// ASCII [a-z0-9]+ 词元；CJK 逐字 unigram + 相邻 bigram（中文无空词界）。
func Tokenize(s string) (map[string]int, int) {
	terms := map[string]int{}
	count := 0
	add := func(t string) {
		if t != "" {
			terms[t]++
			count++
		}
	}
	var word strings.Builder
	var han []rune
	flushWord := func() {
		add(strings.ToLower(word.String()))
		word.Reset()
	}
	flushHan := func() {
		for _, r := range han {
			add(string(r))
		}
		for i := 0; i+1 < len(han); i++ {
			add(string(han[i : i+2]))
		}
		han = han[:0]
	}
	for _, r := range s {
		switch {
		case r < 0x80 && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			flushHan()
			word.WriteRune(r)
		case isHan(r):
			flushWord()
			han = append(han, r)
		default:
			flushWord()
			flushHan()
		}
	}
	flushWord()
	flushHan()
	return terms, count
}

func isHan(r rune) bool {
	return unicode.Is(unicode.Han, r)
}
