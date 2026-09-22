// transcript 格式分派与多根发现：按路径判定格式（zstd→dsh / rollout→codex / 其余→claude），
// 统一入口 ParseTranscript；发现面覆盖全部 agent 会话日志根（去重）。
package miner

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/remin-dev/remin/internal/core/extractor"
	"github.com/remin-dev/remin/internal/store"
)

// detectFormat 按路径判定 transcript 格式（确定性：扩展名与命名约定，不做内容嗅探）
func detectFormat(path string) string {
	base := filepath.Base(path)
	switch {
	case strings.HasSuffix(path, ".jsonl.zstd"):
		return "dsh"
	case strings.HasPrefix(base, "rollout-") && strings.HasSuffix(path, ".jsonl"):
		return "codex"
	default:
		return "claude"
	}
}

// ParseTranscript 统一解析入口（格式分派；增量从 fromLine 起，1-based）
func ParseTranscript(path string, fromLine int) ([]extractor.Event, int, error) {
	switch detectFormat(path) {
	case "dsh":
		return ParseDSHSession(path, fromLine)
	case "codex":
		return ParseCodexRollout(path, fromLine)
	default:
		return ParseClaudeJSONL(path, fromLine)
	}
}

// DefaultCodexDir Codex 会话日志根
func DefaultCodexDir() string {
	if env := os.Getenv("REMIN_CODEX_DIR"); env != "" {
		return env
	}
	return filepath.Join(store.HomeDir(), ".codex", "sessions")
}

// DefaultDSHDir DSH 会话日志根
func DefaultDSHDir() string {
	if env := os.Getenv("REMIN_DSH_DIR"); env != "" {
		return env
	}
	return filepath.Join(store.HomeDir(), ".dsh", "sessions")
}

// DefaultExtraRoots ClaudeDir 之外的默认发现根：codex/dsh 会话目录 + 通用扩展根
// （REMIN_TRANSCRIPT_ROOTS 与 hook 白名单同源——白名单放行的自定义根同样参与发现）
func DefaultExtraRoots() []string {
	roots := []string{DefaultCodexDir(), DefaultDSHDir()}
	if env := os.Getenv("REMIN_TRANSCRIPT_ROOTS"); env != "" {
		roots = append(roots, strings.Split(env, string(os.PathListSeparator))...)
	}
	return roots
}

// discoverAll 跨根发现全部 transcript（确定性排序；跨根去重——按 Clean 归一后的路径）
func discoverAll(roots ...string) []string {
	seen := map[string]bool{}
	var out []string
	for _, root := range roots {
		if root == "" {
			continue
		}
		files, err := Discover(filepath.Clean(root))
		if err != nil {
			continue // 根缺失/权限：跳过（各 agent 不必都在场）
		}
		for _, f := range files {
			if !seen[f] {
				seen[f] = true
				out = append(out, f)
			}
		}
	}
	// Discover 各根内已排序；跨根按根序保持稳定（不全局重排：根优先级 = 发现顺序）
	return out
}
