package audit

import (
	"strings"
	"testing"

	"github.com/remin-dev/remin/internal/store"
	"github.com/remin-dev/remin/internal/testutil"
)

func newAudit(t *testing.T) (*Audit, *store.Store) {
	t.Helper()
	st := testutil.NewStore(t)
	return New(st), st
}

// 台账合并读取不得重复（曾因按 action 列表遍历同文件导致 5 倍重复）
func TestListMergesWithoutDuplication(t *testing.T) {
	a, _ := newAudit(t)
	for i := 0; i < 3; i++ {
		if err := a.Append(Record{Action: ActionPromote, IDs: []string{"m1"}, Version: i + 1}); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.Append(Record{Action: ActionReject, IDs: []string{"c1"}}); err != nil {
		t.Fatal(err)
	}
	all, err := a.List("")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 4 {
		t.Fatalf("合并应 4 条无重复: %d", len(all))
	}
	promotes, _ := a.List(ActionPromote)
	if len(promotes) != 3 {
		t.Errorf("按 action 过滤应 3 条: %d", len(promotes))
	}
	rejects, _ := a.List(ActionReject)
	if len(rejects) != 1 || rejects[0].IDs[0] != "c1" {
		t.Errorf("reject 台账独立: %+v", rejects)
	}
}

// 记录字段完整落盘（TS 自动补、Actor、Version）
func TestAppendFillsTimestamp(t *testing.T) {
	a, _ := newAudit(t)
	if err := a.Append(Record{Actor: "u <u@x>", Action: ActionPromote, IDs: []string{"m"}, Version: 7}); err != nil {
		t.Fatal(err)
	}
	recs, _ := a.List(ActionPromote)
	if len(recs) != 1 || recs[0].TS == "" || !strings.Contains(recs[0].TS, "T") {
		t.Fatalf("TS 应自动补齐 ISO 格式: %+v", recs)
	}
	if recs[0].Version != 7 || recs[0].Actor != "u <u@x>" {
		t.Errorf("字段丢失: %+v", recs[0])
	}
}

// 时间倒序（新→旧）
func TestListOrdersNewestFirst(t *testing.T) {
	a, _ := newAudit(t)
	_ = a.Append(Record{TS: "2026-01-01T00:00:00+08:00", Action: ActionPromote, IDs: []string{"old"}})
	_ = a.Append(Record{TS: "2026-02-01T00:00:00+08:00", Action: ActionPromote, IDs: []string{"new"}})
	recs, _ := a.List(ActionPromote)
	if len(recs) != 2 || recs[0].IDs[0] != "new" {
		t.Fatalf("应新→旧: %+v", recs)
	}
}
