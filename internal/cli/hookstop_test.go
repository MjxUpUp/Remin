package cli

import (
	"path/filepath"
	"testing"
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
