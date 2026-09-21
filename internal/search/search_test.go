package search

import (
	"reflect"
	"testing"
	"time"

	"github.com/remin-dev/remin/internal/index"
	"github.com/remin-dev/remin/internal/store"
	"github.com/remin-dev/remin/internal/testutil"
)

func doc(id, body, facet, status, trust, expires, reviewed, verifyResult string) *store.Memory {
	m := &store.Memory{
		ID: id, Type: store.TypeSemantic, Facet: facet, Status: status,
		CapturedAt: store.NowTime(), ReviewedAt: reviewed, Modified: store.NowTime(),
		Trust: trust, Source: store.SourceAgent,
		Provenance: store.Provenance{Origin: "t", Ref: "t#1", Quote: body},
		Version:    store.FormatVersion, Body: body, Expires: expires,
	}
	if verifyResult != "" {
		m.Verify = &store.Verify{Condition: "c", Result: verifyResult}
	}
	return m
}

var now = time.Date(2026, 9, 21, 12, 0, 0, 0, time.Local)

func build(docs ...*store.Memory) *Searcher {
	return New(index.Build(1, docs))
}

// M2 完成判据：确定性——同版本+同查询=同结果
func TestDeterministic(t *testing.T) {
	s := build(
		doc("mem_A", "部署前必须先跑迁移脚本", "dev", store.StatusActive, store.TrustHumanVerified, "", store.NowTime(), ""),
		doc("mem_B", "回复用中文，代码注释用英文", "dev", store.StatusActive, store.TrustHumanVerified, "", store.NowTime(), ""),
	)
	r1 := s.Search("部署 注意事项", Options{Now: now})
	r2 := s.Search("部署 注意事项", Options{Now: now})
	if !reflect.DeepEqual(r1, r2) {
		t.Errorf("同查询应同结果:\n%+v\n%+v", r1, r2)
	}
	if len(r1.Hits) == 0 || r1.Hits[0].ID != "mem_A" {
		t.Errorf("中文查询首位应命中 mem_A: %+v", r1.Hits)
	}
	if len(r1.Hits) > 1 && r1.Hits[1].Score >= r1.Hits[0].Score {
		t.Errorf("排序应为分数降序: %+v", r1.Hits)
	}
}

// 弃权（A5）：垃圾查询必 abstain，且是显式字段
func TestAbstainGarbageQuery(t *testing.T) {
	s := build(doc("mem_A", "部署前必须先跑迁移脚本", "dev", store.StatusActive, store.TrustHumanVerified, "", store.NowTime(), ""))
	r := s.Search("zzxxqq 无关词", Options{Now: now})
	if !r.Abstained || r.Reason != "no_match" {
		t.Errorf("垃圾查询应弃权: %+v", r)
	}
	if len(r.Hits) != 0 {
		t.Errorf("弃权时不应有结果")
	}
}

// stale 注入率 = 0：superseded / verify failed / ephemeral 过期均不可见
func TestStaleExcluded(t *testing.T) {
	s := build(
		doc("mem_active", "数据库是 Postgres 新事实", "dev", store.StatusActive, store.TrustHumanVerified, "", store.NowTime(), ""),
		doc("mem_super", "数据库是 Mongo 旧事实", "dev", store.StatusSuperseded, store.TrustHumanVerified, "", store.NowTime(), ""),
		doc("mem_vfail", "登录走 OAuth", "dev", store.StatusActive, store.TrustHumanVerified, "", store.NowTime(), store.VerifyFailed),
		doc("mem_expired", "上次会话交接内容", "dev", store.StatusActive, store.TrustUnverified, "7d", "2026-01-01T00:00:00+08:00", ""),
	)
	r := s.Search("数据库 Mongo OAuth 会话", Options{Now: now, TopK: 10})
	for _, h := range r.Hits {
		if h.ID != "mem_active" {
			t.Errorf("过期/被替代/验证失败的记忆不应出现: %+v", h)
		}
	}
}

// facet 过滤 + trust 随行
func TestFacetFilterAndTrustCarried(t *testing.T) {
	s := build(
		doc("mem_dev", "部署脚本在 scripts 下", "dev", store.StatusActive, store.TrustAgentClaimed, "", store.NowTime(), ""),
		doc("mem_life", "部署家里的路由器", "life", store.StatusActive, store.TrustHumanVerified, "", store.NowTime(), ""),
	)
	r := s.Search("部署", Options{Now: now, Facet: "life"})
	if len(r.Hits) != 1 || r.Hits[0].ID != "mem_life" {
		t.Errorf("facet 过滤失败: %+v", r.Hits)
	}
	if r.Hits[0].Trust != store.TrustHumanVerified {
		t.Errorf("trust 应随行: %+v", r.Hits[0])
	}
}

// 快照语义：钉住旧版本的索引看不到新落库的记忆
func TestSnapshotPinning(t *testing.T) {
	st := testutil.NewStore(t)
	m1 := doc("mem_A", "Postgres 迁移脚本", "dev", store.StatusActive, store.TrustHumanVerified, "", store.NowTime(), "")
	m1.ReviewedAt = store.NowTime()
	if err := st.SaveMemory(m1); err != nil {
		t.Fatal(err)
	}
	if err := st.WriteVersion(1); err != nil {
		t.Fatal(err)
	}
	v1, err := index.Ensure(st, 1)
	if err != nil {
		t.Fatal(err)
	}
	// 之后新增记忆、版本推进
	m2 := doc("mem_B", "Mongo 已弃用", "dev", store.StatusActive, store.TrustHumanVerified, "", store.NowTime(), "")
	if err := st.SaveMemory(m2); err != nil {
		t.Fatal(err)
	}
	if err := st.WriteVersion(2); err != nil {
		t.Fatal(err)
	}
	v2, err := index.Ensure(st, 2)
	if err != nil {
		t.Fatal(err)
	}
	r1 := New(v1).Search("Mongo", Options{Now: now})
	r2 := New(v2).Search("Mongo", Options{Now: now})
	if len(r1.Hits) != 0 {
		t.Errorf("v1 快照不应看到 v2 新增: %+v", r1.Hits)
	}
	if len(r2.Hits) != 1 {
		t.Errorf("v2 应看到: %+v", r2.Hits)
	}
}

// 索引缺失且非当前版本 → 报错（不静默用新数据顶替旧快照）
func TestMissingOldIndexRefuses(t *testing.T) {
	st := testutil.NewStore(t)
	m := doc("mem_A", "x", "dev", store.StatusActive, store.TrustHumanVerified, "", store.NowTime(), "")
	if err := st.SaveMemory(m); err != nil {
		t.Fatal(err)
	}
	if err := st.WriteVersion(5); err != nil {
		t.Fatal(err)
	}
	if _, err := index.Ensure(st, 3); err == nil {
		t.Error("缺失旧版本索引应报错而非静默重建")
	}
	// 当前版本缺失 → 重建
	idx, err := index.Ensure(st, 5)
	if err != nil || len(idx.Docs) != 1 {
		t.Errorf("当前版本应自动重建: %v", err)
	}
}
