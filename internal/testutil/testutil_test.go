package testutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/remin-dev/remin/internal/store"
)

// 脚手架自身可用性：NewStore 产出可用的已初始化真源
func TestNewStoreSanity(t *testing.T) {
	st := NewStore(t)
	if st == nil || st.Root == "" {
		t.Fatal("应返回已初始化真源")
	}
	for _, rel := range []string{"memory", "inbox", "audit", "index", ".git"} {
		if _, err := os.Stat(filepath.Join(st.Root, rel)); err != nil {
			t.Errorf("缺少 %s: %v", rel, err)
		}
	}
	if v, err := st.Version(); err != nil || v != 0 {
		t.Errorf("初始版本应 0: %d err=%v", v, err)
	}
	if !store.IsGitRepo(st.Root) {
		t.Error("应为 git 仓库")
	}
	// 身份环境已注入（后续审收可归因）
	if _, err := store.GitHasIdentity(st.Root); err != nil {
		t.Errorf("git 身份应可用: %v", err)
	}
}
