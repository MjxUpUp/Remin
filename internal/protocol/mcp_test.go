package protocol

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/remin-dev/remin/internal/core/audit"
	"github.com/remin-dev/remin/internal/core/inbox"
	"github.com/remin-dev/remin/internal/core/promotion"
	"github.com/remin-dev/remin/internal/index"
	"github.com/remin-dev/remin/internal/store"
	"github.com/remin-dev/remin/internal/testutil"
)

var binPath string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "remin-mcp-bin")
	if err != nil {
		os.Exit(1)
	}
	binPath = filepath.Join(dir, "remin")
	out, err := exec.Command("go", "build", "-o", binPath, "../../cmd/remin").CombinedOutput()
	if err != nil {
		os.Stderr.Write(out)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func connect(t *testing.T, root string) *mcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	client := mcp.NewClient(&mcp.Implementation{Name: "remin-test", Version: "v0"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{
		Command: exec.Command(binPath, "mcp", "--root", root),
	}, nil)
	if err != nil {
		t.Fatalf("MCP 连接失败: %v", err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

func call[Out any](t *testing.T, s *mcp.ClientSession, tool string, args map[string]any) Out {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	res, err := s.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("%s 调用失败: %v", tool, err)
	}
	if res.IsError {
		t.Fatalf("%s 返回错误: %+v", tool, res.Content)
	}
	var out Out
	data, _ := json.Marshal(res.StructuredContent)
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("%s 结构化输出解析失败: %v (%s)", tool, err, data)
	}
	return out
}

func fixtureWithMemory(t *testing.T) (*store.Store, string) {
	t.Helper()
	st := testutil.NewStore(t)
	in := inbox.New(st)
	_, ids, err := in.AddBatch("mine", []*inbox.Candidate{mkCand("用户主力语言是 Rust", "")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := promotion.Promote(st, in, audit.New(st), promotion.Request{CandidateIDs: ids}); err != nil {
		t.Fatal(err)
	}
	return st, st.Root
}

func mkCand(body, supersedes string) *inbox.Candidate {
	c := &inbox.Candidate{}
	c.Type = store.TypeSemantic
	c.Facet = "dev"
	c.Status = store.StatusCandidate
	c.CapturedAt = store.NowTime()
	c.ReviewedAt = store.TimeUnknown
	c.Modified = store.NowTime()
	c.Trust = store.TrustUnverified
	c.Source = store.SourceAgent
	c.Provenance = store.Provenance{Origin: "claude-code", Ref: "session#t, lines 3-8", Quote: body}
	c.Version = store.FormatVersion
	c.Supersedes = supersedes
	c.Body = body
	return c
}

type searchOutT struct {
	Results []struct {
		ID      string `json:"id"`
		Content string `json:"content"`
		Trust   string `json:"trust"`
	} `json:"results"`
	Abstained    bool   `json:"abstained"`
	Reason       string `json:"abstain_reason"`
	IndexVersion int    `json:"index_version"`
}

// M3 完成判据：MCP stdio 端到端（JSON-RPC 实跑）五工具可用
func TestMCPFiveToolsE2E(t *testing.T) {
	st, root := fixtureWithMemory(t)
	s := connect(t, root)

	// memory_search：命中且 trust 随行
	out := call[searchOutT](t, s, "memory_search", map[string]any{"query": "主力 语言 Rust"})
	if len(out.Results) == 0 || out.Results[0].Trust != store.TrustHumanVerified {
		t.Fatalf("search 应命中且 trust 随行: %+v", out)
	}
	if out.IndexVersion != 1 {
		t.Errorf("index_version 应为 1: %+v", out)
	}
	// 垃圾查询 → abstained 显式字段
	g := call[searchOutT](t, s, "memory_search", map[string]any{"query": "zzxxqqw"})
	if !g.Abstained {
		t.Errorf("垃圾查询应 abstained: %+v", g)
	}

	// memory_propose：进 inbox，agent-claimed
	p := call[struct {
		ProposalID string `json:"proposal_id"`
		Batch      string `json:"batch"`
	}](t, s, "memory_propose", map[string]any{"type": "preference", "content": "回复用中文"})
	if p.ProposalID == "" {
		t.Fatal("propose 应返回 proposal_id")
	}
	cand, err := inbox.New(st).GetCandidate(p.ProposalID)
	if err != nil {
		t.Fatalf("提案应进 inbox: %v", err)
	}
	if cand.Trust != store.TrustAgentClaimed {
		t.Errorf("MCP 提案应 agent-claimed: %s", cand.Trust)
	}

	// memory_status：单条全貌
	memID := out.Results[0].ID
	stt := call[struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}](t, s, "memory_status", map[string]any{"id": memID})
	if stt.ID != memID || stt.Status != store.StatusActive {
		t.Fatalf("status 不对: %+v", stt)
	}

	// memory_refresh：返回当前版本
	r := call[struct {
		IndexVersion int `json:"index_version"`
	}](t, s, "memory_refresh", map[string]any{})
	if r.IndexVersion < 1 {
		t.Fatalf("refresh 应回版本: %+v", r)
	}

	// tools/list：五工具，且无 memory_write
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tools, err := s.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tl := range tools.Tools {
		names[tl.Name] = true
	}
	for _, want := range []string{"memory_search", "memory_propose", "memory_verify", "memory_status", "memory_refresh"} {
		if !names[want] {
			t.Errorf("缺少工具 %s", want)
		}
	}
	if names["memory_write"] {
		t.Error("不允许存在 memory_write（agent 无直写通道）")
	}
}

// 快照钉住：会话内版本推进不可见，memory_refresh 后可见（FR-READ-2）
func TestMCPSnapshotPinning(t *testing.T) {
	st, root := fixtureWithMemory(t)
	s := connect(t, root)

	// 会话进行中：外部（人审）新增记忆并推进版本
	in := inbox.New(st)
	_, ids, _ := in.AddBatch("mine", []*inbox.Candidate{mkCand("部署工具是 ArgoCD", "")})
	res, err := promotion.Promote(st, in, audit.New(st), promotion.Request{CandidateIDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	if res.Version != 2 {
		t.Fatalf("版本应 2: %d", res.Version)
	}

	// 钉住的会话仍服务 v1：看不到新记忆
	out := call[searchOutT](t, s, "memory_search", map[string]any{"query": "ArgoCD"})
	if !out.Abstained {
		t.Fatalf("v1 快照不应看到 v2 新增: %+v", out)
	}
	// 显式 refresh → 推进到 v2
	r := call[struct {
		IndexVersion int `json:"index_version"`
	}](t, s, "memory_refresh", map[string]any{})
	if r.IndexVersion != 2 {
		t.Fatalf("refresh 应到 v2: %+v", r)
	}
	out2 := call[searchOutT](t, s, "memory_search", map[string]any{"query": "ArgoCD"})
	if out2.Abstained || len(out2.Results) == 0 {
		t.Fatalf("refresh 后应命中: %+v", out2)
	}
	if out2.IndexVersion != 2 {
		t.Errorf("服务版本应为 2: %+v", out2)
	}
}

// 索引被清（可重建加速层）：当前版本自动重建
func TestIndexRebuildAfterWipe(t *testing.T) {
	st, root := fixtureWithMemory(t)
	matches, _ := filepath.Glob(filepath.Join(root, "index", "bm25-*.json"))
	for _, m := range matches {
		os.Remove(m)
	}
	idx, err := index.Ensure(st, 1)
	if err != nil || len(idx.Docs) != 1 {
		t.Fatalf("索引应可重建: %v", err)
	}
}
