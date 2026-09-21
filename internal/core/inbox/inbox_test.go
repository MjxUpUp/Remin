package inbox

import (
	"strings"
	"testing"

	"github.com/remin-dev/remin/internal/store"
	"github.com/remin-dev/remin/internal/testutil"
)

func newInbox(t *testing.T) (*Inbox, *store.Store) {
	t.Helper()
	st := testutil.NewStore(t)
	return New(st), st
}

func cand(body, group string) *Candidate {
	c := &Candidate{}
	c.Type = store.TypeSemantic
	c.Facet = "dev"
	c.Status = store.StatusCandidate
	c.CapturedAt = store.NowTime()
	c.ReviewedAt = store.TimeUnknown
	c.Modified = store.NowTime()
	c.Trust = store.TrustUnverified
	c.Source = store.SourceAgent
	c.Provenance = store.Provenance{Origin: "t", Ref: "t#1", Quote: body}
	c.Version = store.FormatVersion
	c.Body = body
	c.Group = group
	return c
}

// AddBatch 落盘候选与清单；批次 id 前缀按来源分档
func TestAddBatchPrefixAndFiles(t *testing.T) {
	in, _ := newInbox(t)
	for _, src := range []struct{ source, prefix string }{
		{"mine", "mine-"}, {"import:claude-mem", "imp-"}, {"propose", "prop-"}, {"manual", "manual-"},
	} {
		batch, ids, err := in.AddBatch(src.source, []*Candidate{cand("x-"+src.prefix, "")})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(batch, src.prefix) {
			t.Errorf("%s 批次前缀应为 %s: %s", src.source, src.prefix, batch)
		}
		if len(ids) != 1 || !strings.HasPrefix(ids[0], "cand_") {
			t.Errorf("候选 id 应 cand_ 前缀: %v", ids)
		}
		if _, err := in.GetCandidate(ids[0]); err != nil {
			t.Errorf("候选文件应可读: %v", err)
		}
		b, _ := in.GetBatch(batch)
		if b.Status != "open" || len(b.Candidates) != 1 {
			t.Errorf("清单状态: %+v", b)
		}
	}
}

// 审收视图排序：conflict → duplicate → normal（FR-GOV-1 冲突建议排前）
func TestListCandidatesGroupOrder(t *testing.T) {
	in, _ := newInbox(t)
	_, ids, err := in.AddBatch("mine", []*Candidate{
		cand("普通", ""),
		cand("冲突", GroupConflict),
		cand("重复", GroupDuplicate),
	})
	if err != nil {
		t.Fatal(err)
	}
	batches, err := in.ListBatches()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, b := range batches {
		if b.Source == "mine" {
			cs, _ := in.ListCandidates(b.ID)
			for _, c := range cs {
				got = append(got, groupOf(c))
			}
		}
	}
	_ = ids
	if len(got) != 3 || got[0] != GroupConflict || got[1] != GroupDuplicate || got[2] != GroupNormal {
		t.Errorf("组序应为 conflict→duplicate→normal: %v", got)
	}
}

// RemoveCandidates：候选文件删除、批次收口 done
func TestRemoveCandidatesClosesBatch(t *testing.T) {
	in, _ := newInbox(t)
	batch, ids, _ := in.AddBatch("mine", []*Candidate{cand("a", "")})
	if err := in.RemoveCandidates(batch, ids); err != nil {
		t.Fatal(err)
	}
	b, _ := in.GetBatch(batch)
	if b.Status != "done" || len(b.Candidates) != 0 {
		t.Errorf("批次应收口: %+v", b)
	}
	if _, err := in.GetCandidate(ids[0]); err == nil {
		t.Error("候选文件应删除")
	}
}

// 部分移除 → partial
func TestRemoveCandidatesPartial(t *testing.T) {
	in, _ := newInbox(t)
	batch, ids, _ := in.AddBatch("mine", []*Candidate{cand("a", ""), cand("b", "")})
	if err := in.RemoveCandidates(batch, ids[:1]); err != nil {
		t.Fatal(err)
	}
	b, _ := in.GetBatch(batch)
	if b.Status != "partial" || len(b.Candidates) != 1 {
		t.Errorf("应 partial 剩 1: %+v", b)
	}
}

// 幂等指纹台账往返
func TestFingerprintsRoundtrip(t *testing.T) {
	in, _ := newInbox(t)
	fs := []Fingerprint{
		{Source: "chatgpt-export", OriginID: "m1", ContentHash: "h1", ImportedAt: "t", Batch: "b"},
		{Source: "codex-memories", OriginID: "f.md", ContentHash: "h2", ImportedAt: "t", Batch: "b"},
	}
	if err := in.AppendFingerprints(fs); err != nil {
		t.Fatal(err)
	}
	got, err := in.LoadFingerprints()
	if err != nil {
		t.Fatal(err)
	}
	if !got["chatgpt-export\x00m1\x00h1"] || !got["codex-memories\x00f.md\x00h2"] {
		t.Errorf("指纹往返丢失: %+v", got)
	}
	if err := in.AppendFingerprints(nil); err != nil {
		t.Errorf("空追加应无操作: %v", err)
	}
}
