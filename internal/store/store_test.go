package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func mustMemory() *Memory {
	return &Memory{
		ID: "mem_TEST0000000000000000", Type: TypeDecision, Facet: "dev",
		Status:     StatusActive,
		CapturedAt: NowTime(), ReviewedAt: NowTime(), Modified: NowTime(),
		Trust: TrustHumanVerified, Source: SourceAgent,
		Provenance: Provenance{Origin: "claude-code", Ref: "session#A, lines 1-2", Quote: "原文"},
		Version:    FormatVersion, Body: "选 Postgres 而非 Mongo。",
	}
}

func TestFrontmatterRoundtrip(t *testing.T) {
	m := mustMemory()
	m.Context = []string{"payment-service"}
	content, err := m.Render()
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseMemory(content)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if got.ID != m.ID || got.Type != m.Type || got.Facet != m.Facet ||
		got.Trust != m.Trust || got.Source != m.Source || got.Status != m.Status {
		t.Errorf("字段往返不一致: %+v", got)
	}
	if len(got.Context) != 1 || got.Context[0] != "payment-service" {
		t.Errorf("context 往返失败: %v", got.Context)
	}
	if got.Body != m.Body {
		t.Errorf("正文往返失败: %q", got.Body)
	}
	if got.Provenance != m.Provenance {
		t.Errorf("provenance 往返失败: %+v", got.Provenance)
	}
}

func TestVerifyFieldRoundtrip(t *testing.T) {
	m := mustMemory()
	m.Verify = &Verify{Condition: "ADR 0003 仍存在", LastCheck: NowTime(), Result: VerifyPassed}
	m.Expires = "7d"
	got, err := ParseMemory(mustRender(t, m))
	if err != nil {
		t.Fatal(err)
	}
	if got.Verify == nil || got.Verify.Condition != m.Verify.Condition || got.Verify.Result != VerifyPassed {
		t.Errorf("verify 往返失败: %+v", got.Verify)
	}
	if got.Expires != "7d" {
		t.Errorf("expires 往返失败: %q", got.Expires)
	}
}

func mustRender(t *testing.T, m *Memory) string {
	t.Helper()
	c, err := m.Render()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// spec 不变量 2：无 provenance 不落库
func TestValidateProvenanceRequired(t *testing.T) {
	m := mustMemory()
	m.Provenance = Provenance{Origin: "x", Ref: "", Quote: "y"}
	if err := m.Validate(); err == nil {
		t.Error("provenance.ref 缺失应当校验失败")
	}
}

func TestValidateRequiredFields(t *testing.T) {
	m := mustMemory()
	m.Trust = "super-trusted"
	if err := m.Validate(); err == nil {
		t.Error("非法 trust 应当校验失败")
	}
	m2 := mustMemory()
	m2.CapturedAt = "2026-09-17 18:02" // 缺时区
	if err := m2.Validate(); err == nil {
		t.Error("缺时区的时间应当校验失败")
	}
	m3 := mustMemory()
	m3.CapturedAt = TimeUnknown
	if err := m3.Validate(); err != nil {
		t.Errorf("unknown 时间应当合法: %v", err)
	}
}

func TestULIDFormatAndUniqueness(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		u, err := NewULID()
		if err != nil {
			t.Fatal(err)
		}
		if len(u) != 26 {
			t.Fatalf("ULID 长度应为 26: %s", u)
		}
		if !strings.HasPrefix(u, "0") && u[0] > '7' {
			t.Fatalf("2026 年时间戳首字符应 ≤ '7': %s", u)
		}
		if seen[u] {
			t.Fatalf("ULID 重复: %s", u)
		}
		seen[u] = true
	}
}

func TestInitLayoutAndVersion(t *testing.T) {
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@t")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@t")
	dir := t.TempDir()
	st, err := Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"memory", "inbox/candidates", "inbox/batches", "audit", "index"} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Errorf("缺少目录 %s: %v", rel, err)
		}
	}
	v, err := st.Version()
	if err != nil || v != 0 {
		t.Errorf("初始版本应为 0, got %d err=%v", v, err)
	}
	if !IsGitRepo(dir) {
		t.Error("init 后应为 git 仓库")
	}
	// 重复 init 拒绝
	if _, err := Init(dir); err == nil {
		t.Error("重复 init 应当报错")
	}
}

func TestSaveGetListMemories(t *testing.T) {
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@t")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@t")
	dir := t.TempDir()
	st, _ := Init(dir)
	m := mustMemory()
	if err := st.SaveMemory(m); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetMemory(m.ID)
	if err != nil || got.Body != m.Body {
		t.Fatalf("读取失败: %v", err)
	}
	// 换 type 目录也能查到（GetMemory 全类型扫描）
	list, err := st.ListMemories()
	if err != nil || len(list) != 1 {
		t.Fatalf("ListMemories 应为 1: %v %d", err, len(list))
	}
}

func TestExpiry(t *testing.T) {
	m := mustMemory()
	m.Expires = "7d"
	m.ReviewedAt = "2026-01-01T00:00:00+08:00"
	if !m.ExpiredAt(mustTime(t, "2026-01-10T00:00:00+08:00")) {
		t.Error("8 天后应判定过期")
	}
	if m.ExpiredAt(mustTime(t, "2026-01-05T00:00:00+08:00")) {
		t.Error("4 天后不应过期")
	}
	m.ReviewedAt = TimeUnknown
	if m.ExpiredAt(mustTime(t, "2030-01-01T00:00:00+08:00")) {
		t.Error("reviewed_at 不可还原时不应判定")
	}
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	tm, ok := ParseTime(s)
	if !ok {
		t.Fatalf("时间解析失败: %s", s)
	}
	return tm
}
