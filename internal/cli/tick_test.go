package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/remin-dev/remin/internal/store"
	"github.com/remin-dev/remin/internal/testutil"
)

// CLI 接线契约：tick 走 --json 包络；schedule install/remove/status 可驱动。

func TestTickCommandJSON(t *testing.T) {
	st := testutil.NewStore(t)
	dir := t.TempDir()
	tp := filepath.Join(dir, "s.jsonl")
	os.WriteFile(tp, []byte(`{"type":"user","sessionId":"s1","cwd":"/p","timestamp":"2026-09-22T10:00:00+08:00","message":{"role":"user","content":"记住：构建前先跑 go vet"}}
`), 0o644)
	t.Setenv("REMIN_CLAUDE_DIR", dir)

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	rootCmd.SetArgs([]string{"tick", "--json", "--root", st.Root})
	err := rootCmd.Execute()
	os.Stdout = old
	w.Close()
	data := make([]byte, 65536)
	n, _ := r.Read(data)
	os.Stdout = old
	if err != nil {
		t.Fatalf("tick 不应失败: %v", err)
	}
	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			Mine struct {
				Candidates int `json:"candidates"`
			} `json:"mine"`
			DeepPending int `json:"deep_pending"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data[:n], &env); err != nil {
		t.Fatalf("输出应为 --json 包络: %v\n%s", err, data[:n])
	}
	if !env.OK {
		t.Fatalf("tick 应成功: %s", data[:n])
	}
	// 无 llm 配置：快挖产候选（typed+recap≥1），deep 队列不增长
	if env.Data.Mine.Candidates < 1 {
		t.Fatalf("快挖应产候选: %s", data[:n])
	}
	if env.Data.DeepPending != 0 {
		t.Fatalf("未配置 llm 不应有待挖段: %s", data[:n])
	}
}

func TestTickScheduleStatusNotInstalled(t *testing.T) {
	st := testutil.NewStore(t)
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("平台护栏：schedule 子命令仅 darwin/linux 有实现面，其余平台走显式报错用例")
	}
	// 用可注入 HomeDir 指向临时目录保证未装状态（ScheduleStatus 读 ~/Library/...）
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	home := t.TempDir()
	oldHome := store.HomeDir
	store.HomeDir = func() string { return home }
	t.Cleanup(func() { store.HomeDir = oldHome })
	rootCmd.SetArgs([]string{"tick", "schedule", "status", "--json", "--root", st.Root})
	err := rootCmd.Execute()
	os.Stdout = old
	w.Close()
	data := make([]byte, 65536)
	n, _ := r.Read(data)
	if err != nil {
		t.Fatalf("status 不应失败: %v", err)
	}
	var env struct {
		Data struct {
			Installed bool `json:"installed"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data[:n], &env); err != nil {
		t.Fatalf("status 应为 --json 包络: %v\n%s", err, data[:n])
	}
	if env.Data.Installed {
		t.Fatalf("临时 HOME 下应未装: %s", data[:n])
	}
}
