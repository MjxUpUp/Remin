// 落位（staging）：分发渠道（npm/brew/源码）只负责获取二进制；接线一律指向
// 用户领地内的稳定路径 <root>/bin/remin——渠道目录漂移（nvm 切版本、npm 目录被清、
// clone 目录删除）不再影响已接线配置，升级 = 原位原子替换。
package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/remin-dev/remin/internal/core/upgrade"
	"github.com/remin-dev/remin/internal/store"
)

// StablePath 落位稳定路径（与真源同根：一个领地，卸载一次收净）
func StablePath(root string) string {
	name := "remin"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(root, "bin", name)
}

// Stage 落位：selfPath 已是稳定路径 → 原样返回；否则复制到稳定路径（原子替换）。
// 同时保证 root/.gitignore 忽略 bin/ 与 wiring.json（add -A 提交路径防扫入）并记账。
func Stage(root, selfPath string) (string, error) {
	stable := StablePath(root)
	if filepath.Clean(selfPath) == filepath.Clean(stable) {
		return stable, nil
	}
	if err := os.MkdirAll(filepath.Dir(stable), 0o755); err != nil {
		return "", err
	}
	self, err := os.ReadFile(selfPath)
	if err != nil {
		return "", fmt.Errorf("读取自身二进制失败: %w", err)
	}
	if err := upgrade.AtomicReplace(stable, self); err != nil {
		return "", fmt.Errorf("落位复制失败（%s → %s）: %w", selfPath, stable, err)
	}
	if err := ensureGitignore(root); err != nil {
		return stable, err
	}
	return stable, nil
}

// gitignoreLines 落位产物必须被 git 忽略的行（store init 与此处双写，单一来源常量）
const gitignoreBinLine = "bin/"
const gitignoreLedgerLine = "wiring.json"

// ensureGitignore 已有 .gitignore（或已是 git 仓库）时补齐忽略行并记 effect；
// 未 init 的 root 不制造文件——init 会写含这些行的新默认值。
func ensureGitignore(root string) error {
	path := filepath.Join(root, ".gitignore")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if !store.IsGitRepo(root) {
			return nil
		}
		data = nil // git 仓库却无 .gitignore：创建
	} else if err != nil {
		return err
	}
	body := string(data)
	var added []string
	for _, line := range []string{gitignoreBinLine, gitignoreLedgerLine} {
		if !hasExactLine(body, line) {
			added = append(added, line)
		}
	}
	if len(added) == 0 {
		return nil
	}
	if len(body) > 0 && !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	body += strings.Join(added, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return err
	}
	l, err := LoadLedger(root)
	if err != nil {
		return nil // 台账读不了（损坏）不阻断落位；卸载走启发式兜底
	}
	l.Append(Effect{
		ID:    effectID("gitignore", path, "remin"),
		Kind:  "gitignore",
		File:  path,
		Lines: added,
	})
	return l.Save(root)
}

func hasExactLine(body, line string) bool {
	for _, l := range strings.Split(body, "\n") {
		if strings.TrimSpace(l) == line {
			return true
		}
	}
	return false
}
