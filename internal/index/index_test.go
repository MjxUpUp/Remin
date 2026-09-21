package index

import (
	"testing"

	"github.com/remin-dev/remin/internal/store"
)

func mem(id, body string) *store.Memory {
	return &store.Memory{
		ID: id, Type: store.TypeProcedural, Facet: "dev", Status: store.StatusActive,
		CapturedAt: store.NowTime(), ReviewedAt: store.NowTime(), Modified: store.NowTime(),
		Trust: store.TrustHumanVerified, Source: store.SourceAgent,
		Provenance: store.Provenance{Origin: "t", Ref: "t#1", Quote: body},
		Version:    store.FormatVersion, Body: body,
	}
}

func TestTokenizeCJKAndASCII(t *testing.T) {
	terms, n := Tokenize("部署注意事项 deploy-V2")
	for _, want := range []string{"部", "部署", "署注", "注意", "事项", "deploy", "v2"} {
		if terms[want] == 0 {
			t.Errorf("缺少词元 %q（got %v）", want, terms)
		}
	}
	if n == 0 {
		t.Error("词元数应大于 0")
	}
	terms2, _ := Tokenize("Deploy DEPLOY")
	if terms2["deploy"] != 2 {
		t.Errorf("大小写应归一: %v", terms2)
	}
}

func TestTokenizeDeterministic(t *testing.T) {
	a, na := Tokenize("同一输入 Same Input 123")
	b, nb := Tokenize("同一输入 Same Input 123")
	if na != nb {
		t.Errorf("词元数不一致: %d vs %d", na, nb)
	}
	for k, v := range a {
		if b[k] != v {
			t.Errorf("词元不一致: %s %d vs %d", k, v, b[k])
		}
	}
}

func TestBuildOrderStable(t *testing.T) {
	m1 := mem("mem_A", "alpha body")
	m2 := mem("mem_B", "beta 身体")
	i1 := Build(3, []*store.Memory{m1, m2})
	i2 := Build(3, []*store.Memory{m1, m2})
	if len(i1.Docs) != 2 || i1.Docs[0].ID != "mem_A" || i1.Docs[1].ID != "mem_B" {
		t.Errorf("构建顺序不稳定: %+v", i1.Docs)
	}
	if i1.Version != i2.Version || len(i1.Docs) != len(i2.Docs) {
		t.Error("两次构建应等价")
	}
}
