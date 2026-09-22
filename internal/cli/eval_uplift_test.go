package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/remin-dev/remin/internal/testutil"
)

// eval uplift/history CLI 接线契约：任务集驱动真源实测、--record 落历史、history 可读回。

func TestEvalUpliftAndHistoryRoundtrip(t *testing.T) {
	st := testutil.NewStore(t)
	t.Setenv("REMIN_HOME", st.Root)
	t.Setenv("REMIN_CLAUDE_DIR", t.TempDir())
	for _, env := range []string{"REMIN_CODEX_DIR", "REMIN_DSH_DIR", "REMIN_TRANSCRIPT_ROOTS"} {
		t.Setenv(env, t.TempDir())
	}
	// 先落一条可检索记忆
	rootCmd.SetArgs([]string{"propose", "部署前必须检查数据库迁移脚本", "--type", "procedural", "--root", st.Root})
	if err := rootCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	rootCmd.SetArgs([]string{"inbox", "--json", "--root", st.Root})
	_ = rootCmd.Execute()
	os.Stdout = old
	w.Close()
	data := make([]byte, 1<<16)
	n, _ := r.Read(data)
	var env struct {
		Data struct {
			Batches []struct {
				ID      string `json:"id"`
				Pending int    `json:"pending"`
			} `json:"batches"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data[:n], &env); err != nil {
		t.Fatalf("inbox json: %v", err)
	}
	if len(env.Data.Batches) == 0 {
		t.Fatal("应有批次")
	}
	rootCmd.SetArgs([]string{"promote", "--batch", env.Data.Batches[0].ID, "--all", "--root", st.Root})
	if err := rootCmd.Execute(); err != nil {
		t.Fatal(err)
	}

	// 任务集：一题子串命中 + 一题弃权
	tf := filepath.Join(t.TempDir(), "tasks.jsonl")
	os.WriteFile(tf, []byte(`{"query":"数据库 部署 迁移","expect_contains":"迁移"}
{"query":"完全无关 zzxxqq","expect_abstain":true}
`), 0o644)

	r2, w2, _ := os.Pipe()
	os.Stdout = w2
	rootCmd.SetArgs([]string{"eval", "uplift", "--tasks", tf, "--record", "--json", "--root", st.Root})
	err := rootCmd.Execute()
	os.Stdout = old
	w2.Close()
	n2, _ := r2.Read(data)
	if err != nil {
		t.Fatalf("uplift 不应失败: %v", err)
	}
	var rep struct {
		Data struct {
			Hits           int     `json:"hits"`
			AbstainCorrect int     `json:"abstain_correct"`
			Recall         float64 `json:"recall"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data[:n2], &rep); err != nil {
		t.Fatalf("uplift json: %v\n%s", err, data[:n2])
	}
	if rep.Data.Hits != 1 || rep.Data.AbstainCorrect != 1 || rep.Data.Recall != 1 {
		t.Fatalf("应 1 命中 1 弃权 recall=1: %+v", rep.Data)
	}
	if _, err := os.Stat(filepath.Join(st.Root, "eval", "history.jsonl")); err != nil {
		t.Fatalf("历史应落盘: %v", err)
	}

	// history 读回
	r3, w3, _ := os.Pipe()
	os.Stdout = w3
	rootCmd.SetArgs([]string{"eval", "history", "--json", "--root", st.Root})
	err = rootCmd.Execute()
	os.Stdout = old
	w3.Close()
	n3, _ := r3.Read(data)
	if err != nil {
		t.Fatalf("history 不应失败: %v", err)
	}
	var hist struct {
		Data []struct {
			Recall float64 `json:"recall"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data[:n3], &hist); err != nil || len(hist.Data) != 1 || hist.Data[0].Recall != 1 {
		t.Fatalf("history 应 1 条 recall=1: %v %s", err, data[:n3])
	}
}

func TestEvalUpliftRequiresTasks(t *testing.T) {
	st := testutil.NewStore(t)
	rootCmd.SetArgs([]string{"eval", "uplift", "--root", st.Root})
	if err := rootCmd.Execute(); err == nil {
		t.Fatal("缺 --tasks 应报错")
	}
}
