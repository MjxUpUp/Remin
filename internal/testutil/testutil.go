// Package testutil 测试脚手架：临时真源仓库与候选工厂（仅测试引用，不入产品路径）
package testutil

import (
	"testing"

	"github.com/remin-dev/remin/internal/store"
)

// NewStore 建立临时真源（git 身份用环境变量注入，保证提交可归因）
func NewStore(t *testing.T) *store.Store {
	t.Helper()
	t.Setenv("GIT_AUTHOR_NAME", "remin-test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@remin.local")
	t.Setenv("GIT_COMMITTER_NAME", "remin-test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@remin.local")
	dir := t.TempDir()
	st, err := store.Init(dir)
	if err != nil {
		t.Fatalf("初始化临时真源失败: %v", err)
	}
	return st
}
