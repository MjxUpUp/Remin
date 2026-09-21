package promotion

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/remin-dev/remin/internal/core/audit"
	"github.com/remin-dev/remin/internal/core/inbox"
	"github.com/remin-dev/remin/internal/store"
	"github.com/remin-dev/remin/internal/testutil"
)

func fixture(t *testing.T) (*store.Store, *inbox.Inbox, *audit.Audit) {
	t.Helper()
	st := testutil.NewStore(t)
	return st, inbox.New(st), audit.New(st)
}

func cand(body string, supersedes string) *inbox.Candidate {
	c := &inbox.Candidate{}
	c.Type = store.TypeSemantic
	c.Facet = "dev"
	c.Status = store.StatusCandidate
	c.CapturedAt = store.NowTime()
	c.ReviewedAt = store.TimeUnknown
	c.Modified = store.NowTime()
	c.Trust = store.TrustUnverified
	c.Source = store.SourceAgent
	c.Provenance = store.Provenance{Origin: "claude-code", Ref: "session#t, lines 1-2", Quote: body}
	c.Version = store.FormatVersion
	c.Supersedes = supersedes
	c.Body = body
	return c
}

// M1 完成判据：promote 是单一原子提交（记忆+版本+1+审计+台账），幂等台账一致
func TestPromoteAtomicCommit(t *testing.T) {
	st, in, au := fixture(t)
	batch, ids, err := in.AddBatch("mine", []*inbox.Candidate{cand("用户主力语言是 Rust", "")})
	if err != nil {
		t.Fatal(err)
	}
	res, err := Promote(st, in, au, Request{CandidateIDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	if res.Version != 1 {
		t.Errorf("版本应推进到 1, got %d", res.Version)
	}
	if len(res.MemoryIDs) != 1 {
		t.Fatalf("应有 1 条新记忆")
	}
	m, err := st.GetMemory(res.MemoryIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	if m.Trust != store.TrustHumanVerified {
		t.Errorf("人审后应 human-verified, got %s", m.Trust)
	}
	if m.Status != store.StatusActive || m.ReviewedAt == store.TimeUnknown {
		t.Errorf("状态/审收时间不对: %s %s", m.Status, m.ReviewedAt)
	}
	// 审计台账
	recs, _ := au.List(audit.ActionPromote)
	if len(recs) != 1 || len(recs[0].IDs) != 1 || recs[0].Version != 1 {
		t.Errorf("审计记录不对: %+v", recs)
	}
	// 批次收口、候选消失
	b, _ := in.GetBatch(batch)
	if b.Status != "done" || len(b.Candidates) != 0 {
		t.Errorf("批次应收口: %+v", b)
	}
	if _, err := in.GetCandidate(ids[0]); err == nil {
		t.Error("候选文件应已删除")
	}
	// git 状态干净（全部进了提交）
	out, err := store.GitRun(st.Root, "status", "--porcelain")
	if err != nil || out != "" {
		t.Errorf("提交后工作区应干净: %q err=%v", out, err)
	}
	log, _ := store.GitRun(st.Root, "log", "--oneline")
	if !strings.Contains(log, "promote: 1 条记忆") {
		t.Errorf("提交信息不对: %s", log)
	}
}

// 冲突不共存（A2）：promote 后旧条目 superseded、双向指针成对
func TestPromoteSupersession(t *testing.T) {
	st, in, au := fixture(t)
	_, ids, _ := in.AddBatch("mine", []*inbox.Candidate{cand("主力数据库是 Mongo", "")})
	res1, _ := Promote(st, in, au, Request{CandidateIDs: ids})
	oldID := res1.MemoryIDs[0]

	_, ids2, _ := in.AddBatch("mine", []*inbox.Candidate{cand("主力数据库已改为 Postgres", oldID)})
	res2, err := Promote(st, in, au, Request{CandidateIDs: ids2})
	if err != nil {
		t.Fatal(err)
	}
	newID := res2.MemoryIDs[0]

	old, _ := st.GetMemory(oldID)
	if old.Status != store.StatusSuperseded || old.SupersededBy != newID {
		t.Errorf("旧条目应 superseded 且指针成对: %+v", old)
	}
	fresh, _ := st.GetMemory(newID)
	if fresh.Supersedes != oldID {
		t.Errorf("新条目应记录 supersedes: %+v", fresh)
	}
	if res2.Version != 2 {
		t.Errorf("版本应 2, got %d", res2.Version)
	}
}

// 同一旧事实只能被替代一次
func TestDoubleSupersedeRejected(t *testing.T) {
	st, in, au := fixture(t)
	_, ids, _ := in.AddBatch("mine", []*inbox.Candidate{cand("v1 事实", "")})
	res1, _ := Promote(st, in, au, Request{CandidateIDs: ids})
	oldID := res1.MemoryIDs[0]

	_, ids2, _ := in.AddBatch("mine", []*inbox.Candidate{cand("v2 事实", oldID)})
	if _, err := Promote(st, in, au, Request{CandidateIDs: ids2}); err != nil {
		t.Fatal(err)
	}
	_, ids3, _ := in.AddBatch("mine", []*inbox.Candidate{cand("v3 事实", oldID)})
	if _, err := Promote(st, in, au, Request{CandidateIDs: ids3}); err == nil {
		t.Error("已被替代的旧条目不可再次被替代")
	}
}

// 原子性：事务中途失败 → 文件全部恢复、暂存区干净（M1 完成判据：崩溃回滚）
func TestAtomicRollback(t *testing.T) {
	st, _, _ := fixture(t)
	path := filepath.Join(st.Root, "index", "VERSION")
	undo, err := Atomic(st.Root, []string{path}, func() error {
		_ = os.WriteFile(path, []byte("42\n"), 0o644)
		_ = os.MkdirAll(filepath.Join(st.Root, "memory", "semantic"), 0o755)
		_ = os.WriteFile(filepath.Join(st.Root, "memory", "semantic", "mem_X.md"), []byte("x"), 0o644)
		return errFake{}
	})
	if err == nil {
		t.Fatal("应返回错误")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "0\n" {
		t.Errorf("VERSION 应回滚为 0, got %q", data)
	}
	// 新建的目录文件不在 checkpoint 内，由调用方负责；这里验证暂存区干净
	out, _ := store.GitRun(st.Root, "status", "--porcelain")
	if strings.Contains(out, "VERSION") {
		t.Errorf("VERSION 不应残留在变更里: %q", out)
	}
	_ = undo
	_ = os.Remove(filepath.Join(st.Root, "memory", "semantic", "mem_X.md"))
}

type errFake struct{}

func (errFake) Error() string { return "fake" }

// 候选非法（绕过 AddBatch 校验直写文件——模拟外部篡改/损坏）→ promote 拒绝且无部分落盘
func TestPromoteInvalidCandidateNoPartialState(t *testing.T) {
	st, in, au := fixture(t)
	good := cand("正常记忆", "")
	batch, ids, err := in.AddBatch("mine", []*inbox.Candidate{good})
	if err != nil {
		t.Fatal(err)
	}
	// 直接改写候选文件：抹掉 provenance.origin（绕过 AddBatch 的入队校验）
	candPath := filepath.Join(st.Root, "inbox", "candidates", ids[0]+".md")
	data, err := os.ReadFile(candPath)
	if err != nil {
		t.Fatal(err)
	}
	tampered := strings.Replace(string(data), "origin: claude-code", "origin: \"\"", 1)
	if tampered == string(data) {
		t.Fatalf("候选文件格式与预期不符，无法构造篡改夹具:\n%s", data)
	}
	if err := os.WriteFile(candPath, []byte(tampered), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Promote(st, in, au, Request{CandidateIDs: ids}); err == nil {
		t.Fatal("非法候选（provenance 不齐）应拒绝")
	}
	ms, _ := st.ListMemories()
	if len(ms) != 0 {
		t.Errorf("不应有任何记忆落盘: %d", len(ms))
	}
	if v, _ := st.Version(); v != 0 {
		t.Errorf("版本不应推进: %d", v)
	}
	out, _ := store.GitRun(st.Root, "status", "--porcelain")
	if strings.Contains(out, "memory/") || strings.Contains(out, "VERSION") {
		t.Errorf("关键路径不应有半提交残留: %q", out)
	}
	_ = batch
}

func TestRejectArchives(t *testing.T) {
	st, in, au := fixture(t)
	batch, ids, _ := in.AddBatch("mine", []*inbox.Candidate{cand("不要的记忆", "")})
	if _, err := Reject(st, in, au, ids, "不需要"); err != nil {
		t.Fatal(err)
	}
	recs, _ := au.List(audit.ActionReject)
	if len(recs) != 1 || recs[0].Note != "不需要" {
		t.Errorf("拒绝台账不对: %+v", recs)
	}
	b, _ := in.GetBatch(batch)
	if b.Status != "done" {
		t.Errorf("批次应收口")
	}
	v, _ := st.Version()
	if v != 0 {
		t.Errorf("拒绝不改变检索真值，版本应仍为 0, got %d", v)
	}
}
