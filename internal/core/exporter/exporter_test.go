package exporter

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/remin-dev/remin/internal/core/audit"
	"github.com/remin-dev/remin/internal/core/inbox"
	"github.com/remin-dev/remin/internal/core/promotion"
	"github.com/remin-dev/remin/internal/store"
	"github.com/remin-dev/remin/internal/testutil"
)

func buildPopulatedStore(t *testing.T) *store.Store {
	t.Helper()
	st := testutil.NewStore(t)
	in := inbox.New(st)
	au := audit.New(st)
	_, ids1, _ := in.AddBatch("mine", []*inbox.Candidate{mkCand("用户主力语言是 Rust", "")})
	if _, err := promotion.Promote(st, in, au, promotion.Request{CandidateIDs: ids1}); err != nil {
		t.Fatal(err)
	}
	_, ids2, _ := in.AddBatch("mine", []*inbox.Candidate{mkCand("主力数据库已改为 Postgres", firstID(t, st))})
	if _, err := promotion.Promote(st, in, au, promotion.Request{CandidateIDs: ids2}); err != nil {
		t.Fatal(err)
	}
	return st
}

func firstID(t *testing.T, st *store.Store) string {
	t.Helper()
	ms, _ := st.ListMemories()
	if len(ms) == 0 {
		t.Fatal("应已有记忆")
	}
	return ms[0].ID
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
	c.Provenance = store.Provenance{Origin: "claude-code", Ref: "session#t, line 1", Quote: body}
	c.Version = store.FormatVersion
	c.Supersedes = supersedes
	c.Body = body
	return c
}

// M5 完成判据：export → restore → export 三点哈希一致（含 supersession 历史与审计）
func TestRoundtripHashConsistency(t *testing.T) {
	st := buildPopulatedStore(t)
	out1 := filepath.Join(t.TempDir(), "exp1")
	m1, err := Export(st, out1)
	if err != nil {
		t.Fatal(err)
	}

	// 全新真源还原
	st2 := testutil.NewStore(t)
	if err := Restore(st2, out1); err != nil {
		t.Fatal(err)
	}
	// supersession 历史与审计完整往返
	ms, _ := st2.ListMemories()
	if len(ms) != 2 {
		t.Fatalf("应还原 2 条: %d", len(ms))
	}
	var superseded int
	for _, m := range ms {
		if m.Status == store.StatusSuperseded {
			superseded++
		}
	}
	if superseded != 1 {
		t.Errorf("supersession 历史应完整: %d", superseded)
	}
	recs, _ := audit.New(st2).List("")
	if len(recs) == 0 {
		t.Error("审收记录应完整往返")
	}
	v2, _ := st2.Version()
	if v2 != 2 {
		t.Errorf("版本应还原为 2: %d", v2)
	}

	// 再导出：清单一致
	out2 := filepath.Join(t.TempDir(), "exp2")
	m2, err := Export(st2, out2)
	if err != nil {
		t.Fatal(err)
	}
	if len(m1.Files) != len(m2.Files) {
		t.Fatalf("文件数不一致: %d vs %d", len(m1.Files), len(m2.Files))
	}
	for rel, h1 := range m1.Files {
		h2, ok := m2.Files[rel]
		if !ok || h1 != h2 {
			t.Errorf("roundtrip 哈希不一致: %s", rel)
		}
	}
}

// 导出物被篡改 → 拒绝还原
func TestRestoreRejectsTamperedBundle(t *testing.T) {
	st := buildPopulatedStore(t)
	out := filepath.Join(t.TempDir(), "exp")
	if _, err := Export(st, out); err != nil {
		t.Fatal(err)
	}
	// 篡改一个记忆文件
	target := filepath.Join(out, "memory", "semantic")
	entries, _ := os.ReadDir(target)
	if len(entries) == 0 {
		t.Fatal("应有记忆文件")
	}
	p := filepath.Join(target, entries[0].Name())
	os.WriteFile(p, []byte("---\nid: tampered\n---\n被篡改"), 0o644)

	st2 := testutil.NewStore(t)
	if err := Restore(st2, out); err == nil {
		t.Fatal("篡改的导出物应拒绝还原")
	}
}
