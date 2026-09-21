package syncpkg

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/remin-dev/remin/internal/store"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@t")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@t")
	st, err := store.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// set-remote → push → 第二真源 pull 拉齐
func TestSetRemotePushPull(t *testing.T) {
	st1 := newStore(t)
	if err := st1.WriteVersion(3); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GitCommit(st1.Root, "bump v3"); err != nil {
		t.Fatal(err)
	}
	remote := filepath.Join(t.TempDir(), "bare.git")
	if _, err := store.GitRun(st1.Root, "init", "--bare", "-q", remote); err != nil {
		// init --bare 需在远端目录语义下执行
		if _, err := store.GitRun(filepath.Dir(remote), "init", "--bare", "-q", filepath.Base(remote)); err != nil {
			t.Fatal(err)
		}
	}
	if err := SetRemote(st1, remote); err != nil {
		t.Fatal(err)
	}
	if _, err := Push(st1); err != nil {
		t.Fatalf("push: %v", err)
	}
	st2 := newStore(t)
	if err := SetRemote(st2, remote); err != nil {
		t.Fatal(err)
	}
	if _, err := Pull(st2); err != nil {
		t.Fatalf("pull: %v", err)
	}
	if v, _ := st2.Version(); v != 3 {
		t.Errorf("pull 后版本应为 3: %d", v)
	}
}

// index/VERSION 冲突自动取 max；其他冲突中止
func TestVersionConflictTakesMax(t *testing.T) {
	st1 := newStore(t)
	remote := filepath.Join(t.TempDir(), "bare.git")
	if _, err := store.GitRun(filepath.Dir(remote), "init", "--bare", "-q", filepath.Base(remote)); err != nil {
		t.Fatal(err)
	}
	if err := SetRemote(st1, remote); err != nil {
		t.Fatal(err)
	}
	if _, err := Push(st1); err != nil {
		t.Fatal(err)
	}

	// 两边各自推进到不同版本
	st1.WriteVersion(5)
	store.GitCommit(st1.Root, "a v5")
	st2 := newStore(t)
	SetRemote(st2, remote)
	Pull(st2)
	st2.WriteVersion(7)
	store.GitCommit(st2.Root, "b v7")
	Push(st2)

	// st1 落后且分叉：pull 冲突仅 index/VERSION → 取 max
	if err := st1.WriteVersion(6); err != nil {
		t.Fatal(err)
	}
	store.GitCommit(st1.Root, "a v6")
	out, err := Pull(st1)
	if err != nil {
		t.Fatalf("VERSION 冲突应自动取 max: %v（%s）", err, out)
	}
	if v, _ := st1.Version(); v != 7 {
		t.Errorf("应取 max=7: %d", v)
	}
	// 后续可正常 push
	if _, err := Push(st1); err != nil {
		t.Fatalf("冲突解决后应可 push: %v", err)
	}
}

// 无远端时报错清晰
func TestNoRemote(t *testing.T) {
	st := newStore(t)
	if _, err := Pull(st); err == nil || !strings.Contains(err.Error(), "set-remote") {
		t.Errorf("无远端应提示 set-remote: %v", err)
	}
}
