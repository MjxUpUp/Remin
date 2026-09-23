package cli

import (
	"testing"

	"github.com/remin-dev/remin/internal/core/inbox"
	"github.com/remin-dev/remin/internal/store"
	"github.com/remin-dev/remin/internal/testutil"
)

// TestProposeSupersedes propose --supersedes：替代提案进 inbox，promote 后旧条退出检索
func TestProposeSupersedes(t *testing.T) {
	st := testutil.NewStore(t)
	t.Setenv("REMIN_HOME", st.Root)
	m := &store.Memory{
		Type: "semantic", Facet: "dev", Status: store.StatusActive,
		CapturedAt: store.NowTime(), ReviewedAt: store.NowTime(), Modified: store.NowTime(),
		Trust: store.TrustHumanVerified, Source: store.SourceHuman,
		Provenance: store.Provenance{Origin: "t", Ref: "t#1", Quote: "主力数据库是 Postgres"},
		Version:    store.FormatVersion, Body: "主力数据库是 Postgres",
	}
	m.ID, _ = store.NewMemoryID()
	if err := st.SaveMemory(m); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GitCommit(st.Root, "test: seed active memory"); err != nil {
		t.Fatal(err)
	}

	promoteSel = selectorFlags{} // 清上一测试可能残留的 promote flags
	rootCmd.SetArgs([]string{"propose", "主力数据库已迁移到 CockroachDB", "--type", "semantic", "--supersedes", m.ID, "--root", st.Root})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("propose --supersedes: %v", err)
	}

	// 批次 promote → 旧条 superseded
	in := inbox.New(st)
	batches, _ := in.ListBatches()
	b := batches[0]
	rootCmd.SetArgs([]string{"promote", "--batch", b.ID, "--all", "--root", st.Root})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("promote: %v", err)
	}
	old, _ := st.GetMemory(m.ID)
	if old.Status != store.StatusSuperseded {
		t.Fatalf("旧条应 superseded: %s", old.Status)
	}
}

// TestProposeSupersedesNotFound 指定不存在的 id 显式报错
func TestProposeSupersedesNotFound(t *testing.T) {
	st := testutil.NewStore(t)
	rootCmd.SetArgs([]string{"propose", "x", "--supersedes", "mem_NONEXIST", "--root", st.Root})
	if err := rootCmd.Execute(); err == nil {
		t.Fatal("不存在的 id 应报错")
	}
}
