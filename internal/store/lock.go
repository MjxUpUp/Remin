package store

import (
	"fmt"
	"path/filepath"
	"sync"
)

// WithRoot 执行需要独占真源变更权的临界区：
// 进程内按 root 互斥 + 跨进程 flock（.git/remin.lock，不入版本控制），
// 入口先检查关键事务路径（memory/ index/VERSION audit/）无未提交变更——
// 上一次操作崩溃留下的脏树会在下一次变更前大声失败，而不是被 add -A 静默固化。
func WithRoot(root string, fn func() error) error {
	mu := rootMutex(root)
	mu.Lock()
	defer mu.Unlock()

	release, err := lockRepo(root)
	if err != nil {
		return err
	}
	defer release()

	if err := assertCleanCriticalPaths(root); err != nil {
		return err
	}
	return fn()
}

// TryWithRoot 非阻塞版临界区：拿不到锁立即返回 acquired=false（hook 路径用——
// 架构 §14「inject/hook-stop 永不阻塞」的硬约束；降级跳过优于等待）。
func TryWithRoot(root string, fn func() error) (acquired bool, err error) {
	mu := rootMutex(root)
	if !mu.TryLock() {
		return false, nil
	}
	defer mu.Unlock()

	release, err := lockRepoNB(root)
	if err != nil {
		return false, nil // 跨进程锁忙：同样立即让路
	}
	defer release()

	if err := assertCleanCriticalPaths(root); err != nil {
		return true, err
	}
	return true, fn()
}

// assertCleanCriticalPaths memory/、index/VERSION、audit/ 必须无未提交变更。
// inbox/ 不在此列：propose/mine 允许留下待审候选（它们等下一次原子提交收纳）。
func assertCleanCriticalPaths(root string) error {
	out, err := GitRun(root, "status", "--porcelain", "--", "memory", "index/VERSION", "audit")
	if err != nil {
		return nil // git 异常时不阻断（保守放行，事务本身仍互斥）
	}
	if out != "" {
		return fmt.Errorf("真源关键路径存在未提交变更（上次操作可能中断）:\n%s\n请先 git -C %s status 检查；确认无用后 git -C %s checkout -- memory index/VERSION audit 或提交修复", out, root, root)
	}
	return nil
}

var rootMutexes sync.Map // root → *sync.Mutex

func rootMutex(root string) *sync.Mutex {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	m, _ := rootMutexes.LoadOrStore(abs, &sync.Mutex{})
	return m.(*sync.Mutex)
}
