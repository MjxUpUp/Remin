// Package inbox 候选与批次管理（FR-CAP-6：一切写入过 inbox，无一例外）。
// 候选文件同为 markdown+frontmatter，可审计；批次清单纯 JSON 台账。
package inbox

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/remin-dev/remin/internal/store"
	"gopkg.in/yaml.v3"
)

// 候选分组（审收视图排序：冲突建议排前）
const (
	GroupConflict  = "conflict"
	GroupDuplicate = "duplicate"
	GroupNormal    = "normal"
)

// Candidate inbox 候选：记忆字段 + 批次与分组元数据
type Candidate struct {
	store.Memory `yaml:",inline"`
	Batch        string `yaml:"batch"`
	Group        string `yaml:"group,omitempty"`
	DuplicateOf  string `yaml:"duplicate_of,omitempty"`
}

const candidatePrefix = "cand_"

// Inbox inbox/ 目录视图
type Inbox struct {
	Root string // <store>/inbox
}

func New(st *store.Store) *Inbox {
	return &Inbox{Root: filepath.Join(st.Root, "inbox")}
}

func (in *Inbox) candidatesDir() string { return filepath.Join(in.Root, "candidates") }
func (in *Inbox) batchesDir() string    { return filepath.Join(in.Root, "batches") }
func (in *Inbox) importsFile() string   { return filepath.Join(in.Root, "imports.jsonl") }

// NewCandidateID cand_ + ULID
func NewCandidateID() (string, error) {
	u, err := store.NewULID()
	if err != nil {
		return "", err
	}
	return candidatePrefix + u, nil
}

// AddBatch 新建批次并落盘候选；返回批次 id 与候选 id 列表。
// source 例：mine / import:claude-auto-memory / manual / propose
func (in *Inbox) AddBatch(source string, cands []*Candidate) (string, []string, error) {
	now := store.NowTime()
	id, err := in.nextBatchID(source)
	if err != nil {
		return "", nil, err
	}
	b := Batch{ID: id, Source: source, CreatedAt: now, Status: "open"}
	if err := os.MkdirAll(in.candidatesDir(), 0o755); err != nil {
		return "", nil, err
	}
	if err := os.MkdirAll(in.batchesDir(), 0o755); err != nil {
		return "", nil, err
	}
	for _, c := range cands {
		cid, err := NewCandidateID()
		if err != nil {
			return "", nil, err
		}
		c.ID = cid
		if c.Status == "" {
			c.Status = store.StatusCandidate
		}
		if c.Version == 0 {
			c.Version = store.FormatVersion
		}
		if c.Batch == "" {
			c.Batch = id
		}
		if err := c.Validate(); err != nil {
			return "", nil, fmt.Errorf("候选非法: %w", err)
		}
		content, err := c.Render()
		if err != nil {
			return "", nil, err
		}
		path := filepath.Join(in.candidatesDir(), cid+".md")
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return "", nil, err
		}
		b.Candidates = append(b.Candidates, cid)
	}
	if err := in.saveBatch(&b); err != nil {
		return "", nil, err
	}
	return id, b.Candidates, nil
}

func batchPrefix(source string) string {
	switch {
	case strings.HasPrefix(source, "import"):
		return "imp"
	case source == "mine":
		return "mine"
	case source == "propose":
		return "prop"
	default:
		return "manual"
	}
}

func (in *Inbox) nextBatchID(source string) (string, error) {
	day := time.Now().Format("20060102")
	prefix := fmt.Sprintf("%s-%s-", batchPrefix(source), day)
	entries, err := os.ReadDir(in.batchesDir())
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	max := 0
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, prefix) {
			var n int
			if _, err := fmt.Sscanf(strings.TrimSuffix(name, ".json"), prefix+"%d", &n); err == nil && n > max {
				max = n
			}
		}
	}
	return fmt.Sprintf("%s%02d", prefix, max+1), nil
}

// Batch 批次清单
type Batch struct {
	ID         string   `json:"id"`
	Source     string   `json:"source"`
	CreatedAt  string   `json:"created_at"`
	Candidates []string `json:"candidates"`
	Status     string   `json:"status"`
	Note       string   `json:"note,omitempty"`
}

func (in *Inbox) saveBatch(b *Batch) error {
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(in.batchesDir(), b.ID+".json"), data, 0o644)
}

func (in *Inbox) GetBatch(id string) (*Batch, error) {
	data, err := os.ReadFile(filepath.Join(in.batchesDir(), id+".json"))
	if err != nil {
		return nil, fmt.Errorf("批次 %s 不存在", id)
	}
	var b Batch
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, err
	}
	return &b, nil
}

// ListBatches 批次列表（新→旧）
func (in *Inbox) ListBatches() ([]Batch, error) {
	entries, err := os.ReadDir(in.batchesDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Batch
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := in.GetBatch(strings.TrimSuffix(e.Name(), ".json"))
		if err != nil {
			continue
		}
		out = append(out, *b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out, nil
}

// Render 渲染候选文件（记忆字段内联 + 候选元数据），覆盖 Memory.Render
func (c *Candidate) Render() (string, error) {
	fm, err := yaml.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("渲染候选 frontmatter 失败: %w", err)
	}
	var b strings.Builder
	b.WriteString("---\n")
	b.Write(fm)
	b.WriteString("---\n")
	b.WriteString(strings.TrimSpace(c.Body))
	b.WriteString("\n")
	return b.String(), nil
}

// GetCandidate 读取单个候选（含 batch/group 等候选元数据）
func (in *Inbox) GetCandidate(id string) (*Candidate, error) {
	data, err := os.ReadFile(filepath.Join(in.candidatesDir(), id+".md"))
	if err != nil {
		return nil, fmt.Errorf("候选 %s 不存在", id)
	}
	fm, body, err := store.SplitFrontmatter(string(data))
	if err != nil {
		return nil, err
	}
	var c Candidate
	dec := yaml.NewDecoder(strings.NewReader(fm))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("解析候选 %s 失败: %w", id, err)
	}
	c.Body = body
	return &c, nil
}

// ListCandidates 批次内候选（组序：conflict → duplicate → normal，同组按 id）
func (in *Inbox) ListCandidates(batchID string) ([]*Candidate, error) {
	b, err := in.GetBatch(batchID)
	if err != nil {
		return nil, err
	}
	var out []*Candidate
	for _, cid := range b.Candidates {
		c, err := in.GetCandidate(cid)
		if err != nil {
			continue // 已被处理的候选
		}
		out = append(out, c)
	}
	rank := map[string]int{GroupConflict: 0, GroupDuplicate: 1, GroupNormal: 2}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := rank[groupOf(out[i])], rank[groupOf(out[j])]
		if ri != rj {
			return ri < rj
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func groupOf(c *Candidate) string {
	if c.Group != "" {
		return c.Group
	}
	return GroupNormal
}

// RemoveCandidates 从批次移除已处理候选并更新清单；批次清空时置 done。
// 仅改台账与候选文件，git 提交由调用方（promotion）统一编排。
func (in *Inbox) RemoveCandidates(batchID string, ids []string) error {
	b, err := in.GetBatch(batchID)
	if err != nil {
		return err
	}
	remove := map[string]bool{}
	for _, id := range ids {
		remove[id] = true
	}
	var kept []string
	for _, cid := range b.Candidates {
		if !remove[cid] {
			kept = append(kept, cid)
		}
	}
	for id := range remove {
		_ = os.Remove(filepath.Join(in.candidatesDir(), id+".md"))
	}
	b.Candidates = kept
	if len(kept) == 0 {
		b.Status = "done"
	} else {
		b.Status = "partial"
	}
	return in.saveBatch(b)
}

// Fingerprint 导入幂等指纹（FR-IMP-5：来源 + 原始 ID，缺失用内容哈希）
type Fingerprint struct {
	Source      string `json:"source"`
	OriginID    string `json:"origin_id"`
	ContentHash string `json:"content_hash"`
	ImportedAt  string `json:"imported_at"`
	Batch       string `json:"batch"`
}

// LoadFingerprints 已导入指纹（同来源重复导入默认跳过）
func (in *Inbox) LoadFingerprints() (map[string]bool, error) {
	out := map[string]bool{}
	data, err := os.ReadFile(in.importsFile())
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var f Fingerprint
		if err := json.Unmarshal([]byte(line), &f); err == nil {
			out[f.Source+"\x00"+f.OriginID+"\x00"+f.ContentHash] = true
		}
	}
	return out, nil
}

// AppendFingerprints 追加指纹（幂等台账）
func (in *Inbox) AppendFingerprints(fs []Fingerprint) error {
	if len(fs) == 0 {
		return nil
	}
	var sb strings.Builder
	for _, f := range fs {
		data, _ := json.Marshal(f)
		sb.Write(data)
		sb.WriteByte('\n')
	}
	f, err := os.OpenFile(in.importsFile(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(sb.String())
	return err
}
