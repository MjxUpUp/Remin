//go:build windows

package store

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// lockRepo Windows 无 flock：O_EXCL 锁文件 + 过期重置（尽力而为互斥；
// 主平台 macOS/Linux 走 flock 强互斥）。
func lockRepo(root string) (release func(), err error) {
	dir := filepath.Join(root, ".git")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "remin.lock")
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			_, _ = f.WriteString(fmt.Sprintf("%d", os.Getpid()))
			_ = f.Close()
			return func() { _ = os.Remove(path) }, nil
		}
		if info, statErr := os.Stat(path); statErr == nil && time.Since(info.ModTime()) > 10*time.Minute {
			_ = os.Remove(path) // 陈旧锁重置
			continue
		}
		return nil, fmt.Errorf("真源被其他进程锁定: %s", path)
	}
}
