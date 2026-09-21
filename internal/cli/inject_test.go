package cli

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/remin-dev/remin/internal/core/config"
	"github.com/remin-dev/remin/internal/store"
	"github.com/remin-dev/remin/internal/testutil"
)

// 回归（doc-review P1-1）：inject 真正读取 config inject_facet——
// 文档 §4.4 承诺的行为必须有自动化证据，不能只靠评审时手测。
func TestInjectFacetFromConfig(t *testing.T) {
	st := testutil.NewStore(t)
	cfg, err := config.Load(st.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	cfg.InjectFacet = "work"
	if err := cfg.Save(st.ConfigPath()); err != nil {
		t.Fatal(err)
	}
	// life 面记忆：inject_facet=work 时不应出现
	m := memForInject("mem_INJECTFACET0000000000000A", "生活面记忆内容", "life")
	if err := st.SaveMemory(m); err != nil {
		t.Fatal(err)
	}

	rootCmd.SetArgs([]string{"inject", "--root", st.Root})
	out := captureStdout(t, func() {
		if err := rootCmd.Execute(); err != nil {
			t.Errorf("inject 永不失败: %v", err)
		}
	})
	if !strings.Contains(out, "facet=work") {
		t.Errorf("注入头应显示 config 的 inject_facet=work:\n%s", out)
	}
	if strings.Contains(out, "mem_INJECTFACET") {
		t.Errorf("life 面记忆不应出现在 work 注入:\n%s", out)
	}
}

// 回归（doc-review P1-1 对偶面）：--facet 显式覆盖优先于 config
func TestInjectFacetFlagOverridesConfig(t *testing.T) {
	st := testutil.NewStore(t)
	cfg, _ := config.Load(st.ConfigPath())
	cfg.InjectFacet = "work"
	cfg.Save(st.ConfigPath())
	m := memForInject("mem_INJECTFLAG000000000000000B", "生活面记忆内容", "life")
	if err := st.SaveMemory(m); err != nil {
		t.Fatal(err)
	}
	rootCmd.SetArgs([]string{"inject", "--root", st.Root, "--facet", "life"})
	out := captureStdout(t, func() {
		if err := rootCmd.Execute(); err != nil {
			t.Errorf("inject 永不失败: %v", err)
		}
	})
	if !strings.Contains(out, "facet=life") || !strings.Contains(out, "mem_INJECTFLAG") {
		t.Errorf("--facet 应覆盖 config:\n%s", out)
	}
}

func memForInject(id, body, facet string) *store.Memory {
	now := store.NowTime()
	return &store.Memory{
		ID: id, Type: store.TypeSemantic, Facet: facet, Status: store.StatusActive,
		CapturedAt: now, ReviewedAt: now, Modified: now,
		Trust: store.TrustHumanVerified, Source: store.SourceHuman,
		Provenance: store.Provenance{Origin: "t", Ref: "t", Quote: body},
		Version:    store.FormatVersion, Body: body,
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	fn()
	os.Stdout = old
	w.Close()
	data, _ := io.ReadAll(r)
	return string(data)
}
