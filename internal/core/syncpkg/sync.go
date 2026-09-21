// Package syncpkg 多设备同步（FR-STO-2）：git push/pull 包装；远端仅托管，真源永在本地。
// index/VERSION 冲突自动取 max；其他冲突中止并给出人工指引。
package syncpkg

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/remin-dev/remin/internal/core/config"
	"github.com/remin-dev/remin/internal/store"
)

// SetRemote 配置同步远端（git remote + config.yaml）
func SetRemote(st *store.Store, url string) error {
	if _, err := store.GitRun(st.Root, "remote", "get-url", "origin"); err == nil {
		if _, err := store.GitRun(st.Root, "remote", "set-url", "origin", url); err != nil {
			return err
		}
	} else if _, err := store.GitRun(st.Root, "remote", "add", "origin", url); err != nil {
		return err
	}
	cfg, err := config.Load(st.ConfigPath())
	if err != nil {
		return err
	}
	cfg.SyncRemote = url
	return cfg.Save(st.ConfigPath())
}

func remoteOf(st *store.Store) (string, error) {
	url, err := store.GitRun(st.Root, "remote", "get-url", "origin")
	if err != nil || strings.TrimSpace(url) == "" {
		return "", fmt.Errorf("未配置同步远端（remin sync --set-remote <url>）")
	}
	return strings.TrimSpace(url), nil
}

// Pull 拉取并合并；VERSION 冲突取 max 后续提交
func Pull(st *store.Store) (string, error) {
	if _, err := remoteOf(st); err != nil {
		return "", err
	}
	var out string
	err := store.WithRoot(st.Root, func() error {
		var err error
		// allow-unrelated-histories：两台设备各自 remin init 后首次同步是常态（init 骨架语义一致）
		out, err = store.GitRun(st.Root, "pull", "--no-edit", "--no-rebase", "--allow-unrelated-histories", "origin", "main")
		return err
	})
	if err != nil {
		// 仅当确实是合并冲突时才走冲突解析；其他失败（网络等）原样上报
		if isMergeConflict(st) {
			return resolveVersionConflict(st, out, err)
		}
		return "", err
	}
	return out, nil
}

// isUnmergedLine porcelain 行是否为未合并状态
func isUnmergedLine(line string) bool {
	for _, code := range []string{"AA ", "DD ", "AU ", "UD ", "UA ", "DU ", "UU "} {
		if strings.HasPrefix(line, code) {
			return true
		}
	}
	return false
}

// isMergeConflict 工作区是否处于未合并状态（区分网络失败与真冲突）。
// porcelain v1 未合并码全集：AA/DD/AU/UD/UA/DU/UU（unrelated histories 走 AA）。
func isMergeConflict(st *store.Store) bool {
	status, err := store.GitRun(st.Root, "status", "--porcelain")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(status, "\n") {
		for _, code := range []string{"AA ", "DD ", "AU ", "UD ", "UA ", "DU ", "UU "} {
			if strings.HasPrefix(line, code) {
				return true
			}
		}
	}
	return false
}

// Push 推送
func Push(st *store.Store) (string, error) {
	if _, err := remoteOf(st); err != nil {
		return "", err
	}
	var out string
	err := store.WithRoot(st.Root, func() error {
		var err error
		out, err = store.GitRun(st.Root, "push", "-u", "origin", "main")
		return err
	})
	return out, err
}

// Sync 拉取 + 推送
func Sync(st *store.Store) (string, error) {
	if _, err := Pull(st); err != nil {
		return "", fmt.Errorf("pull 失败: %w", err)
	}
	return Push(st)
}

// resolveVersionConflict 冲突仅限 index/VERSION 时自动取 max；否则中止交人工
func resolveVersionConflict(st *store.Store, out string, pullErr error) (string, error) {
	status, err := store.GitRun(st.Root, "status", "--porcelain")
	if err != nil {
		return "", pullErr
	}
	conflicted := []string{}
	for _, line := range strings.Split(status, "\n") {
		if isUnmergedLine(line) {
			conflicted = append(conflicted, strings.TrimSpace(line[3:]))
		}
	}
	onlyVersion := len(conflicted) > 0
	for _, c := range conflicted {
		if c != "index/VERSION" {
			onlyVersion = false
		}
	}
	if !onlyVersion {
		_, _ = store.GitRun(st.Root, "merge", "--abort")
		return "", fmt.Errorf("合并冲突不止 index/VERSION（%v），已中止——请手动 git 处理后再 remin sync", conflicted)
	}
	// 取 max：读双方 stage（2=ours, 3=theirs）
	maxV := 0
	for _, stage := range []string{"2", "3"} {
		out, err := store.GitRun(st.Root, "show", fmt.Sprintf(":%s:index/VERSION", stage))
		if err != nil {
			continue
		}
		if v, err := strconv.Atoi(strings.TrimSpace(out)); err == nil && v > maxV {
			maxV = v
		}
	}
	if err := st.WriteVersion(maxV); err != nil {
		return "", err
	}
	if _, err := store.GitRun(st.Root, "add", "index/VERSION"); err != nil {
		return "", err
	}
	msg, err := store.GitRun(st.Root, "commit", "-q", "-m", fmt.Sprintf("sync: VERSION 冲突取 max（v%d）", maxV))
	if err != nil {
		return "", err
	}
	return msg, nil
}
