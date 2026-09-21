package verify

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/remin-dev/remin/internal/core/audit"
	"github.com/remin-dev/remin/internal/store"
	"github.com/remin-dev/remin/internal/testutil"
)

func memWithVerify(id, cond string) *store.Memory {
	return &store.Memory{
		ID: id, Type: store.TypeSemantic, Facet: "dev", Status: store.StatusActive,
		CapturedAt: store.NowTime(), ReviewedAt: store.NowTime(), Modified: store.NowTime(),
		Trust: store.TrustHumanVerified, Source: store.SourceAgent,
		Provenance: store.Provenance{Origin: "t", Ref: "t#1", Quote: "q"},
		Verify:     &store.Verify{Condition: cond, Result: store.VerifyUnknown},
		Version:    store.FormatVersion, Body: "正文 " + id,
	}
}

func TestEvaluateMachineConditions(t *testing.T) {
	dir := t.TempDir()
	exists := filepath.Join(dir, "yes.txt")
	os.WriteFile(exists, []byte("hello world"), 0o644)

	if r, _ := Evaluate("path-exists:" + exists); r != store.VerifyPassed {
		t.Errorf("存在的路径应 passed: %s", r)
	}
	if r, _ := Evaluate("path-exists:" + filepath.Join(dir, "no.txt")); r != store.VerifyFailed {
		t.Errorf("不存在的路径应 failed: %s", r)
	}
	if r, _ := Evaluate("file-contains:" + exists + "::world"); r != store.VerifyPassed {
		t.Errorf("包含应 passed: %s", r)
	}
	if r, _ := Evaluate("file-contains:" + exists + "::missing"); r != store.VerifyFailed {
		t.Errorf("不包含应 failed: %s", r)
	}
	if r, _ := Evaluate("数据库选型记录仍存在于 docs/adr/0003"); r != store.VerifyUnknown {
		t.Errorf("自然语言条件应 unknown（绝不猜）: %s", r)
	}
}

// 失效 → status expired + 版本 +1（改变检索真值）；复活对称
func TestRunFailedExpiresAndBumpsVersion(t *testing.T) {
	st := testutil.NewStore(t)
	cond := "path-exists:" + filepath.Join(st.Root, "definitely-missing.txt")
	if err := st.SaveMemory(memWithVerify("mem_V", cond)); err != nil {
		t.Fatal(err)
	}
	if err := st.WriteVersion(1); err != nil {
		t.Fatal(err)
	}
	outcomes, err := Run(st, audit.New(st), "mem_V", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 1 || outcomes[0].Result != store.VerifyFailed {
		t.Fatalf("应 failed: %+v", outcomes)
	}
	m, _ := st.GetMemory("mem_V")
	if m.Status != store.StatusExpired {
		t.Errorf("失效记忆应置 expired: %s", m.Status)
	}
	if v, _ := st.Version(); v != 2 {
		t.Errorf("真值改变应版本 +1: %d", v)
	}
	// 复活：人判 passed
	if _, err := Run(st, audit.New(st), "mem_V", "passed"); err != nil {
		t.Fatal(err)
	}
	m, _ = st.GetMemory("mem_V")
	if m.Status != store.StatusActive || m.Verify.Result != store.VerifyPassed {
		t.Errorf("复活失败: %+v", m)
	}
	if v, _ := st.Version(); v != 3 {
		t.Errorf("复活也应 +1: %d", v)
	}
}

// 自然语言条件机器返回 unknown，不改变真值（不 bump）
func TestRunUnknownNoBump(t *testing.T) {
	st := testutil.NewStore(t)
	if err := st.SaveMemory(memWithVerify("mem_U", "登录走 OAuth 吗")); err != nil {
		t.Fatal(err)
	}
	if err := st.WriteVersion(3); err != nil {
		t.Fatal(err)
	}
	outcomes, err := Run(st, audit.New(st), "mem_U", "")
	if err != nil {
		t.Fatal(err)
	}
	if outcomes[0].Result != store.VerifyUnknown {
		t.Errorf("应 unknown: %+v", outcomes)
	}
	if v, _ := st.Version(); v != 3 {
		t.Errorf("unknown 不改变真值不应 bump: %d", v)
	}
}
