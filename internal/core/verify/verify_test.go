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
	if _, err := store.GitCommit(st.Root, "fixture: v1"); err != nil {
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

// superseded 是更强终态：verify 失效不改写其状态（链完整性优先）
func TestRunFailedDoesNotOverwriteSuperseded(t *testing.T) {
	st := testutil.NewStore(t)
	// 直接构造 superseded 记忆（正常流由 promotion 产生；此处聚焦 verify 语义）
	m := memWithVerify("mem_SS", "path-exists:/definitely/not/exists")
	m.Status = store.StatusSuperseded
	m.SupersededBy = "mem_NEW00000000000000000000000"
	if err := st.SaveMemory(m); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GitCommit(st.Root, "fixture: superseded 记忆"); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(st, audit.New(st), "all", ""); err != nil {
		t.Fatal(err)
	}
	got, _ := st.GetMemory("mem_SS")
	if got.Status != store.StatusSuperseded || got.SupersededBy == "" {
		t.Fatalf("superseded 不应被 verify 改写: %+v", got)
	}
	if v, _ := st.Version(); v != 0 {
		t.Errorf("非 active 状态未变，不应推进版本: %d", v)
	}
}

// file-contents 不可读（权限）≠ 不存在：unknown 待人判，绝不猜
func TestEvaluateUnreadableFileIsUnknown(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "locked.txt")
	os.WriteFile(p, []byte("secret"), 0o000)
	defer os.Chmod(p, 0o644)
	if r, _ := Evaluate("file-contains:" + p + "::secret"); r != store.VerifyUnknown {
		t.Errorf("不可读应 unknown（待人判）: %s", r)
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
	if _, err := store.GitCommit(st.Root, "fixture: v3"); err != nil {
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
