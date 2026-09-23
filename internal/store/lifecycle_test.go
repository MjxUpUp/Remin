package store

import (
	"testing"
)

// 生命周期操作契约：退休/重新激活的状态迁移与前置校验；理由尾注进 Quote（可审计）
func TestRetireReactivate(t *testing.T) {
	// git 身份（Init 首提交需要归因——CI 容器无全局身份时兜底）
	t.Setenv("GIT_AUTHOR_NAME", "remin-test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@remin.local")
	t.Setenv("GIT_COMMITTER_NAME", "remin-test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@remin.local")
	st, err := Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := &Memory{
		Type: "preference", Facet: "dev", Status: StatusActive,
		CapturedAt: NowTime(), ReviewedAt: NowTime(), Modified: NowTime(),
		Trust: TrustHumanVerified, Source: SourceHuman,
		Provenance: Provenance{Origin: "t", Ref: "t#1", Quote: "原始引言"},
		Version:    FormatVersion, Body: "构建走 pnpm",
	}
	m.ID, _ = NewMemoryID()
	if err := st.SaveMemory(m); err != nil {
		t.Fatal(err)
	}

	// 退休
	if err := st.Retire(m.ID, "已切换到 turborepo"); err != nil {
		t.Fatal(err)
	}
	got, _ := st.GetMemory(m.ID)
	if got.Status != StatusExpired {
		t.Fatalf("退休后应 expired: %s", got.Status)
	}
	if !contains(got.Provenance.Quote, "turborepo") {
		t.Fatalf("理由应入 Quote 尾注: %q", got.Provenance.Quote)
	}
	if got.Body != "构建走 pnpm" {
		t.Fatalf("A6 原件不可变：body 不得被退休改动: %q", got.Body)
	}

	// 重新激活
	if err := st.Reactivate(m.ID, ""); err != nil {
		t.Fatal(err)
	}
	got, _ = st.GetMemory(m.ID)
	if got.Status != StatusActive {
		t.Fatalf("重新激活后应 active: %s", got.Status)
	}

	// 前置状态校验：superseded 不可退休
	got.Status = StatusSuperseded
	_ = st.SaveMemory(got)
	if err := st.Retire(m.ID, ""); err == nil {
		t.Fatal("superseded 不可退休（前置状态校验应拦）")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && len(sub) > 0 && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
