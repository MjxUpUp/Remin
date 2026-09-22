package cli

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/remin-dev/remin/internal/core/miner"
	"github.com/remin-dev/remin/internal/store"
	"github.com/spf13/cobra"
)

// transcriptWhitelist 允许入队的 transcript 根目录（hook 输入按不可信外部数据处理）
func transcriptWhitelist() []string {
	roots := []string{
		filepath.Join(store.HomeDir(), ".claude", "projects"),
		filepath.Join(store.HomeDir(), ".codex", "sessions"),
		filepath.Join(store.HomeDir(), ".dsh", "sessions"),
	}
	if env := os.Getenv("REMIN_TRANSCRIPT_ROOTS"); env != "" {
		roots = append(roots, strings.Split(env, string(os.PathListSeparator))...)
	}
	return roots
}

func pathAllowed(p string) bool {
	abs, err := filepath.Abs(p)
	if err != nil {
		return false
	}
	for _, root := range transcriptWhitelist() {
		r, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		if strings.HasPrefix(abs, r+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

// hookStopPayload Claude Code Stop hook stdin 载荷（只提取已知字段）
type hookStopPayload struct {
	TranscriptPath string `json:"transcript_path"`
	SessionID      string `json:"session_id"`
}

// hookStop Stop hook 入口：transcript 入队（按路径幂等去重）→ 尽力异步挖矿。
// 永不失败、永不非零、不做任何锁等待/DB/网络操作。
var hookStopCmd = &cobra.Command{
	Use:   "hook-stop",
	Short: "Stop hook 入口：transcript 入队并尽力异步挖矿（永不失败）",
	RunE: func(cmd *cobra.Command, args []string) error {
		defer func() { _ = recover() }()
		root := store.ResolveRoot(rootPath)
		path := extractTranscriptPath(args)
		if path == "" || !pathAllowed(path) {
			return nil // 无路径或不在白名单：安静退出（hook 安全）
		}
		if _, err := os.Stat(path); err != nil {
			return nil
		}
		// 入队走 try-lock（忙则无锁追加：与 RemovePaths 的读改写竞态最坏丢一次入队，
		// 下次 Stop 信号重排——hook 永不等待）
		q := miner.LoadQueue(root)
		if acquired, _ := store.TryWithRoot(root, func() error {
			return q.Append(path)
		}); !acquired {
			_ = q.Append(path)
		}
		spawnAsyncMine(root)
		return nil
	},
}

func extractTranscriptPath(args []string) string {
	if len(args) > 0 && args[0] != "" {
		return args[0]
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil || len(data) == 0 {
		return ""
	}
	var p hookStopPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return ""
	}
	return p.TranscriptPath
}

// spawnAsyncMine 尽力触发：分离进程拉起自身 --from-queue 挖矿（失败静默）
func spawnAsyncMine(root string) {
	self, err := os.Executable()
	if err != nil {
		return
	}
	c := exec.Command(self, "mine", "--from-queue", "--root", root)
	logDir := filepath.Join(root, "transcripts-cache")
	_ = os.MkdirAll(logDir, 0o755)
	if f, err := os.OpenFile(filepath.Join(logDir, "hook.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		c.Stdout = f
		c.Stderr = f
	}
	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		setDetached(c)
	}
	_ = c.Start()
}

func init() {
	rootCmd.AddCommand(hookStopCmd)
}
