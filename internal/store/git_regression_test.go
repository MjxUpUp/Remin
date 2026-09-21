package store_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/remin-dev/remin/internal/core/audit"
	"github.com/remin-dev/remin/internal/core/inbox"
	"github.com/remin-dev/remin/internal/core/promotion"
	"github.com/remin-dev/remin/internal/store"
)

// 回归：连续高频原子提交不得因后台 auto-gc 竞态丢失 commit 对象。
// init 已设 gc.auto=0；此处再以 30 次连发提交 + fsck 验证仓库完好。
func TestRapidCommitsStayConsistent(t *testing.T) {
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@t")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@t")
	dir := t.TempDir()
	st, err := store.Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	gcAuto, err := store.GitRun(dir, "config", "gc.auto")
	if err != nil || strings.TrimSpace(gcAuto) != "0" {
		t.Errorf("init 应禁用 auto-gc: %q err=%v", gcAuto, err)
	}
	in := inbox.New(st)
	au := audit.New(st)
	for i := 0; i < 30; i++ {
		c := &inbox.Candidate{}
		c.Type = store.TypeSemantic
		c.Facet = "dev"
		c.Status = store.StatusCandidate
		c.CapturedAt = store.NowTime()
		c.ReviewedAt = store.TimeUnknown
		c.Modified = store.NowTime()
		c.Trust = store.TrustUnverified
		c.Source = store.SourceAgent
		c.Provenance = store.Provenance{Origin: "t", Ref: "t#1", Quote: "q"}
		c.Version = store.FormatVersion
		c.Body = "连续提交回归 " + strconv.Itoa(i)
		_, ids, err := in.AddBatch("mine", []*inbox.Candidate{c})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := promotion.Promote(st, in, au, promotion.Request{CandidateIDs: ids}); err != nil {
			t.Fatalf("第 %d 次提交失败: %v", i, err)
		}
	}
	out, err := store.GitRun(dir, "fsck", "--no-dangling")
	if err != nil || strings.TrimSpace(out) != "" {
		t.Errorf("fsck 应干净: %q err=%v", out, err)
	}
	if v, _ := st.Version(); v != 30 {
		t.Errorf("版本应为 30: %d", v)
	}
}
