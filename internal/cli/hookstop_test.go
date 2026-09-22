package cli

import (
	"path/filepath"
	"testing"

	"github.com/remin-dev/remin/internal/store"
)

// hook 输入按不可信外部数据处理：白名单外的路径不入队
func TestPathAllowedWhitelist(t *testing.T) {
	root := t.TempDir()
	t.Setenv("REMIN_TRANSCRIPT_ROOTS", root)
	ok := filepath.Join(root, "proj", "s.jsonl")
	bad := filepath.Join(root+"-sibling", "s.jsonl")
	if !pathAllowed(ok) {
		t.Error("白名单内应允许")
	}
	if pathAllowed(bad) {
		t.Error("白名单外应拒绝（前缀不构成目录边界）")
	}
	if pathAllowed(filepath.Join("/etc", "passwd.jsonl")) {
		t.Error("系统路径应拒绝")
	}
}

// 三家 agent 会话根默认都在白名单（含 .jsonl.zstd 形态——DSH）
func TestPathAllowedDefaultAgentRoots(t *testing.T) {
	t.Setenv("REMIN_TRANSCRIPT_ROOTS", "")
	for _, c := range []struct{ root, file string }{
		{".claude", "projects/p/s.jsonl"},
		{".codex", "sessions/2026/09/22/rollout-x.jsonl"},
		{".dsh", "sessions/--p--/session-x/session.jsonl.zstd"},
	} {
		p := filepath.Join(store.HomeDir(), c.root, c.file)
		if !pathAllowed(p) {
			t.Errorf("%s 应在默认白名单内", c.root)
		}
	}
	if pathAllowed(filepath.Join(store.HomeDir(), ".dsh-sibling", "s.jsonl")) {
		t.Error("前缀不构成目录边界（.dsh 兄弟目录应拒绝）")
	}
}
