// Package store 实现 ~/.remin/ 记忆真源仓库：布局、记忆文件读写与 git 封装。
// 依赖纪律（P1-N1）：本包只依赖标准库 + yaml，零 agent SDK。
package store

import (
	"fmt"
	"strings"
)

// 记忆类型（P4-R2：schema 第一天定义全集；spatial-context 仅定义暂无消费逻辑）
const (
	TypeEpisodic       = "episodic"
	TypeSemantic       = "semantic"
	TypeProcedural     = "procedural"
	TypePreference     = "preference"
	TypeDecision       = "decision"
	TypeSpatialContext = "spatial-context"
)

var ValidTypes = map[string]bool{
	TypeEpisodic: true, TypeSemantic: true, TypeProcedural: true,
	TypePreference: true, TypeDecision: true, TypeSpatialContext: true,
}

// 状态机（spec v0 §5）：candidate 仅存在于 inbox/，不落 memory/
const (
	StatusActive     = "active"
	StatusSuperseded = "superseded"
	StatusExpired    = "expired"
	StatusRejected   = "rejected"
	StatusCandidate  = "candidate"
)

// 信任分层（P3-A3）：永不自动升级
const (
	TrustHumanVerified = "human-verified"
	TrustAgentClaimed  = "agent-claimed"
	TrustUnverified    = "unverified"
)

// 写入通道类别
const (
	SourceAgent  = "agent"
	SourceHuman  = "human"
	SourceImport = "import"
)

// Verify 结果
const (
	VerifyPassed  = "passed"
	VerifyFailed  = "failed"
	VerifyUnknown = "unknown"
)

// TimeUnknown：时间不可还原就如实标注，绝不伪造（spec 不变量 5）
const TimeUnknown = "unknown"

// Provenance 覆盖率 100%（P3-A1）：三要素齐全才允许落库
type Provenance struct {
	Origin string `yaml:"origin"`
	Ref    string `yaml:"ref"`
	Quote  string `yaml:"quote"`
}

// Verify 失效条件与最近验证结果（P3-A4）
type Verify struct {
	Condition string `yaml:"condition"`
	LastCheck string `yaml:"last_check,omitempty"`
	Result    string `yaml:"result,omitempty"`
}

// Memory 一条记忆（spec v0 §2：16 个 frontmatter 字段 + 正文）
type Memory struct {
	ID           string     `yaml:"id"`
	Type         string     `yaml:"type"`
	Facet        string     `yaml:"facet"`
	Context      []string   `yaml:"context,omitempty"`
	Status       string     `yaml:"status"`
	Supersedes   string     `yaml:"supersedes,omitempty"`
	SupersededBy string     `yaml:"superseded_by,omitempty"`
	CapturedAt   string     `yaml:"captured_at"`
	ReviewedAt   string     `yaml:"reviewed_at"`
	Modified     string     `yaml:"modified"`
	Trust        string     `yaml:"trust"`
	Source       string     `yaml:"source"`
	Provenance   Provenance `yaml:"provenance"`
	Verify       *Verify    `yaml:"verify,omitempty"`
	Expires      string     `yaml:"expires,omitempty"`
	Version      int        `yaml:"version"`

	Body string `yaml:"-"`
}

// FormatVersion 当前格式版本号（spec v0 = 1）
const FormatVersion = 1

// Validate 校验单条记忆的 frontmatter 完整性（spec 不变量 2 的文件级部分；
// supersedes 成对性等跨条目不变量由 promotion 事务保证）。
func (m *Memory) Validate() error {
	missing := []string{}
	if m.ID == "" {
		missing = append(missing, "id")
	}
	if !ValidTypes[m.Type] {
		missing = append(missing, "type")
	}
	if m.Facet == "" {
		missing = append(missing, "facet")
	}
	switch m.Status {
	case StatusActive, StatusSuperseded, StatusExpired, StatusRejected, StatusCandidate:
	default:
		missing = append(missing, "status")
	}
	if m.CapturedAt == "" {
		missing = append(missing, "captured_at")
	}
	if m.ReviewedAt == "" {
		missing = append(missing, "reviewed_at")
	}
	if m.Modified == "" {
		missing = append(missing, "modified")
	}
	switch m.Trust {
	case TrustHumanVerified, TrustAgentClaimed, TrustUnverified:
	default:
		missing = append(missing, "trust")
	}
	switch m.Source {
	case SourceAgent, SourceHuman, SourceImport:
	default:
		missing = append(missing, "source")
	}
	if m.Version == 0 {
		missing = append(missing, "version")
	}
	if len(missing) > 0 {
		return fmt.Errorf("记忆 %s 缺少必填字段: %s", m.ID, strings.Join(missing, ", "))
	}
	if m.Provenance.Origin == "" || m.Provenance.Ref == "" || m.Provenance.Quote == "" {
		return fmt.Errorf("记忆 %s provenance 三要素不齐（origin/ref/quote），无来源不落库", m.ID)
	}
	for _, ts := range []string{m.CapturedAt, m.ReviewedAt, m.Modified} {
		if ts == TimeUnknown {
			continue
		}
		if !validTime(ts) {
			return fmt.Errorf("记忆 %s 时间字段非法（需 ISO 8601 含时区或 unknown）: %q", m.ID, ts)
		}
	}
	if m.Body == "" {
		return fmt.Errorf("记忆 %s 正文为空", m.ID)
	}
	return nil
}

// Active 可见性判定：status=active 且 verify 未失败（过期由调用方按 expires 时间判定）
func (m *Memory) Searchable() bool {
	return m.Status == StatusActive && (m.Verify == nil || m.Verify.Result != VerifyFailed)
}

func validTime(s string) bool {
	for _, layout := range []string{"2006-01-02T15:04:05Z07:00", "2006-01-02T15:04:05.999999999Z07:00"} {
		if parseTimeOK(s, layout) {
			return true
		}
	}
	return false
}

// TrustLabel 信任分级的中文标签（A3：注入时分层信息随行）
func TrustLabel(trust string) string {
	switch trust {
	case TrustHumanVerified:
		return "人审"
	case TrustAgentClaimed:
		return "agent 断言"
	default:
		return "未验证"
	}
}
