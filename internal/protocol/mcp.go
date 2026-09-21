// Package protocol MCP stdio server（机面；ADR-0004：官方 Go SDK 仅允许出现在本层）。
// server 别名 memory；五工具；**明确不存在 memory_write**——agent 无直写通道，
// 只能提案（memory_propose → inbox，agent-claimed，永不自动升级）。
package protocol

import (
	"context"
	"fmt"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/remin-dev/remin/internal/core/audit"
	"github.com/remin-dev/remin/internal/core/inbox"
	"github.com/remin-dev/remin/internal/core/verify"
	"github.com/remin-dev/remin/internal/index"
	"github.com/remin-dev/remin/internal/search"
	"github.com/remin-dev/remin/internal/store"
)

// session 会话态：快照钉住（FR-READ-2：开场钉住版本，会话内检索服务该快照）。
// MCP 客户端可能并发调用工具（同一 server 进程多 in-flight 请求）——
// pinned 与索引缓存都在 mu 保护下读写；缓存按版本失效。
type session struct {
	mu       sync.Mutex
	pinned   int
	root     string
	cacheVer int
	cache    *search.Searcher
}

// searcherFor 取钉住版本的检索器（缓存命中零重建；refresh 后自动失效）
func (s *session) searcherFor() (*search.Searcher, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cache != nil && s.cacheVer == s.pinned {
		return s.cache, nil
	}
	st, err := store.Open(s.root)
	if err != nil {
		return nil, err
	}
	idx, err := index.Ensure(st, s.pinned)
	if err != nil {
		return nil, fmt.Errorf("快照 v%d 不可用: %w", s.pinned, err)
	}
	sr := search.New(idx)
	s.cache, s.cacheVer = sr, s.pinned
	return sr, nil
}

// NewMCPServer 构造 MCP server；stdio 传输由客户端拉起（remin mcp）
func NewMCPServer(root string) (*mcp.Server, error) {
	if _, err := store.Open(root); err != nil {
		return nil, err
	}
	sess := &session{root: root}
	if st, err := store.Open(root); err == nil {
		if v, err := st.Version(); err == nil {
			sess.pinned = v
		}
	}

	server := mcp.NewServer(&mcp.Implementation{Name: "memory", Version: "remin/0.2.0"}, nil)

	// memory_search
	type searchIn struct {
		Query string `json:"query" jsonschema:"检索词（支持中英文混合）"`
		Facet string `json:"facet,omitempty" jsonschema:"分面过滤（dev/work/life；缺省不过滤）"`
		TopK  int    `json:"top_k,omitempty" jsonschema:"返回条数（默认 8）"`
	}
	type searchOut struct {
		Results      []search.Hit `json:"results"`
		Abstained    bool         `json:"abstained,omitempty"`
		Reason       string       `json:"abstain_reason,omitempty"`
		IndexVersion int          `json:"index_version"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "memory_search",
		Description: "检索用户记忆。返回的每条结果携带 trust（人审/agent断言/未验证）、provenance（来源）与 verify_result；置信不足时 abstained=true（宁可不知道，请向用户确认而非编造）。",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in searchIn) (*mcp.CallToolResult, searchOut, error) {
		sr, err := sess.searcherFor()
		if err != nil {
			return nil, searchOut{}, err
		}
		r := sr.Search(in.Query, search.Options{Facet: in.Facet, TopK: in.TopK})
		return nil, searchOut{Results: r.Hits, Abstained: r.Abstained, Reason: r.Reason, IndexVersion: r.IndexVersion}, nil
	})

	// memory_propose
	type proposeIn struct {
		Type    string   `json:"type" jsonschema:"记忆类型：episodic|semantic|procedural|preference|decision"`
		Content string   `json:"content" jsonschema:"记忆内容（一到三句话，语义自包含）"`
		Facet   string   `json:"facet,omitempty" jsonschema:"分面（默认 dev）"`
		Context []string `json:"context,omitempty" jsonschema:"上下文标签（如项目名）"`
	}
	type proposeOut struct {
		ProposalID string `json:"proposal_id"`
		Batch      string `json:"batch"`
		Note       string `json:"note"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "memory_propose",
		Description: "提案一条新记忆。提案不落库、需人审（remin inbox / promote）才会生效；信任等级 agent-claimed，永不自动升级。",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in proposeIn) (*mcp.CallToolResult, proposeOut, error) {
		st, err := store.Open(root)
		if err != nil {
			return nil, proposeOut{}, err
		}
		if !store.ValidTypes[in.Type] {
			return nil, proposeOut{}, fmt.Errorf("非法 type: %s", in.Type)
		}
		now := store.NowTime()
		c := &inbox.Candidate{}
		c.Type = in.Type
		c.Facet = defaultStr(in.Facet, "dev")
		c.Context = in.Context
		c.Status = store.StatusCandidate
		c.CapturedAt = now
		c.ReviewedAt = store.TimeUnknown
		c.Modified = now
		c.Trust = store.TrustAgentClaimed // MCP 通道：agent 断言
		c.Source = store.SourceAgent
		c.Provenance = store.Provenance{Origin: "mcp", Ref: "memory_propose", Quote: truncate(in.Content, 400)}
		c.Version = store.FormatVersion
		c.Body = in.Content
		batch, ids, err := inbox.New(st).AddBatch("propose", []*inbox.Candidate{c})
		if err != nil {
			return nil, proposeOut{}, err
		}
		return nil, proposeOut{ProposalID: ids[0], Batch: batch,
			Note: "已进 inbox 待人审；agent 无直写通道"}, nil
	})

	// memory_verify
	type verifyIn struct {
		ID string `json:"id" jsonschema:"记忆 id"`
	}
	type verifyOut struct {
		Result   string `json:"result"`
		Evidence string `json:"evidence"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "memory_verify",
		Description: "对带 verify-condition 的记忆执行用前验证（JIT）。自然语言条件返回 unknown 待人判；failed 的记忆已退出检索。",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in verifyIn) (*mcp.CallToolResult, verifyOut, error) {
		st, err := store.Open(root)
		if err != nil {
			return nil, verifyOut{}, err
		}
		outcomes, err := verify.Run(st, audit.New(st), in.ID, "")
		if err != nil {
			return nil, verifyOut{}, err
		}
		o := outcomes[0]
		return nil, verifyOut{Result: o.Result, Evidence: o.Evidence}, nil
	})

	// memory_status
	type statusIn struct {
		ID string `json:"id" jsonschema:"记忆 id"`
	}
	type statusOut struct {
		ID           string           `json:"id"`
		Status       string           `json:"status"`
		Type         string           `json:"type"`
		Facet        string           `json:"facet"`
		Trust        string           `json:"trust"`
		Content      string           `json:"content"`
		Supersedes   string           `json:"supersedes,omitempty"`
		SupersededBy string           `json:"superseded_by,omitempty"`
		ChainNewer   []string         `json:"chain_newer,omitempty"`
		ChainOlder   []string         `json:"chain_older,omitempty"`
		Provenance   store.Provenance `json:"provenance"`
		CapturedAt   string           `json:"captured_at"`
		ReviewedAt   string           `json:"reviewed_at"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "memory_status",
		Description: "查询单条记忆全貌：状态、supersession 链、双时间戳与来源（审计与冲突查询）。",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in statusIn) (*mcp.CallToolResult, statusOut, error) {
		st, err := store.Open(root)
		if err != nil {
			return nil, statusOut{}, err
		}
		m, err := st.GetMemory(in.ID)
		if err != nil {
			return nil, statusOut{}, err
		}
		older, newer, _ := st.SupersessionChain(m.ID)
		var co, cn []string
		for _, o := range older {
			co = append(co, o.ID)
		}
		for _, n := range newer {
			cn = append(cn, n.ID)
		}
		return nil, statusOut{
			ID: m.ID, Status: m.Status, Type: m.Type, Facet: m.Facet, Trust: m.Trust,
			Content: m.Body, Supersedes: m.Supersedes, SupersededBy: m.SupersededBy,
			ChainOlder: co, ChainNewer: cn, Provenance: m.Provenance,
			CapturedAt: m.CapturedAt, ReviewedAt: m.ReviewedAt,
		}, nil
	})

	// memory_refresh
	type refreshIn struct{}
	type refreshOut struct {
		IndexVersion int `json:"index_version"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "memory_refresh",
		Description: "把当前会话快照推进到最新索引版本（会话边界外的唯一显式推进入口，可归因）。之后 memory_search 服务新快照。",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in refreshIn) (*mcp.CallToolResult, refreshOut, error) {
		st, err := store.Open(root)
		if err != nil {
			return nil, refreshOut{}, err
		}
		v, err := st.Version()
		if err != nil {
			return nil, refreshOut{}, err
		}
		sess.mu.Lock()
		sess.pinned = v
		sess.cache = nil // 快照推进：缓存失效
		sess.mu.Unlock()
		return nil, refreshOut{IndexVersion: v}, nil
	})

	return server, nil
}

// Run 启动 stdio server（客户端拉起）
func Run(ctx context.Context, root string) error {
	server, err := NewMCPServer(root)
	if err != nil {
		return err
	}
	return server.Run(ctx, &mcp.StdioTransport{})
}

func defaultStr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
