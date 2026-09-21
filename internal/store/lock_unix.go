//go:build !windows

package store

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// lockRepoNB 非阻塞 flock（try-lock）：忙时返回错误而非等待
func lockRepoNB(root string) (release func(), err error) {
	return lockRepoMode(root, syscall.LOCK_EX|syscall.LOCK_NB)
}

// lockRepo 跨进程互斥：flock 独占锁于 <root>/.git/remin.lock（git 不追踪，restore 不触碰）。
// 进程退出自动释放；同进程不同调用经 rootMutex 串行，不会自锁。
func lockRepo(root string) (release func(), err error) {
	return lockRepoMode(root, syscall.LOCK_EX)
}

func lockRepoMode(root string, how int) (release func(), err error) {
	path := filepath.Join(root, ".git", "remin.lock")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), how); err != nil {
		f.Close()
		return nil, fmt.Errorf("获取真源变更锁失败: %w", err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
