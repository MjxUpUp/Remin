package store

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// GitRun 在 dir 内执行 git 子命令（ADR-0003：shell out 保真原子提交语义）
func GitRun(dir string, args ...string) (string, error) {
	full := append([]string{"-C", dir}, args...)
	cmd := exec.Command("git", full...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return stdout.String(), nil
}

// GitInit 初始化仓库（main 分支）
func GitInit(dir string) error {
	_, err := GitRun(dir, "init", "-q", "-b", "main")
	return err
}

// GitCommit 暂存全部并提交，返回提交哈希
func GitCommit(dir, msg string) (string, error) {
	if _, err := GitRun(dir, "add", "-A"); err != nil {
		return "", err
	}
	out, err := GitRun(dir, "commit", "-q", "-m", msg)
	if err != nil {
		return "", err
	}
	hash, err := GitRun(dir, "rev-parse", "--short", "HEAD")
	if err != nil {
		_ = out
		return "", err
	}
	return strings.TrimSpace(hash), nil
}

// GitResetStaged 清空暂存区（回滚用，不动工作区内容）
func GitResetStaged(dir string) error {
	st, err := GitRun(dir, "status", "--porcelain")
	if err != nil {
		return err
	}
	if strings.TrimSpace(st) == "" {
		return nil
	}
	_, err = GitRun(dir, "reset", "-q")
	return err
}

// GitHasIdentity git 身份是否可归因（审收动作需要）；config 优先，环境变量回退（CI/测试）
func GitHasIdentity(dir string) (string, error) {
	name, err1 := GitRun(dir, "config", "user.name")
	email, err2 := GitRun(dir, "config", "user.email")
	n, e := strings.TrimSpace(name), strings.TrimSpace(email)
	if err1 != nil || err2 != nil || n == "" || e == "" {
		n = firstNonEmpty(os.Getenv("GIT_AUTHOR_NAME"), os.Getenv("GIT_COMMITTER_NAME"))
		e = firstNonEmpty(os.Getenv("GIT_AUTHOR_EMAIL"), os.Getenv("GIT_COMMITTER_EMAIL"))
	}
	if n == "" || e == "" {
		return "", fmt.Errorf("git 身份未配置（git config --global user.name/user.email），审收无法归因")
	}
	return fmt.Sprintf("%s <%s>", n, e), nil
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

// GitIDEverUsed id 在全部 git 历史中是否出现过（spec 不变量 1：永不复用）
func GitIDEverUsed(dir, id string) (bool, error) {
	out, err := GitRun(dir, "log", "--all", "--diff-filter=A", "--name-only", "--format=", "--", "memory/")
	if err != nil {
		return false, nil // 历史为空等情形不视为占用失败
	}
	return strings.Contains(out, id+".md"), nil
}

// IsGitRepo 路径是否已是 git 仓库
func IsGitRepo(dir string) bool {
	_, err := GitRun(dir, "rev-parse", "--is-inside-work-tree")
	return err == nil
}
