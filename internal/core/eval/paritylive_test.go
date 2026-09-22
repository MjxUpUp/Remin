package eval

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// live parity 契约：在场 agent 通道回显 ID 集必须与核心确定性检索真值逐集相等；
// agent 会话失败如实失败并带 stderr 细节；无 agent CLI 时套件显式报不可运行。
// 真实 agent（claude/codex）调用有成本且依赖认证——单测用假 agent 二进制钉住
// 命令构造/回显解析/集合比对全链路；真实通道以手动 live 运行作证（HANDOFF 记录）。

// fakeAgentScript 生成假 agent 可执行体：从入参解析出真源 root，直读 memory 文件
// 回显全部活跃 ID（fixture 保证真值=全集；模拟「agent 检索后逐字回显」的行为契约）
func fakeAgentScript(t *testing.T, dir, name, mode string) {
	t.Helper()
	var body string
	switch mode {
	case "claude":
		body = `#!/bin/sh
# 假 claude：-p PROMPT --mcp-config <file> [--allowedTools ...]
cfg=""
prev=""
for a in "$@"; do
  [ "$prev" = "--mcp-config" ] && cfg="$a"
  prev="$a"
done
root=$(python3 -c "import json,sys;print(json.load(open('$cfg'))['mcpServers']['memory']['args'][2])")
ls "$root"/memory/*/*.md | sed 's|.*/||; s|\.md$||' | sort
`
	case "codex":
		body = `#!/bin/sh
# 假 codex：exec --skip-git-repo-check -c mcp_servers.memory.command=... -c mcp_servers.memory.args=[...] PROMPT
root=""
for a in "$@"; do
  case "$a" in
    mcp_servers.memory.args=*) root=$(python3 -c "import json,sys;print(json.loads(sys.argv[1].split('=',1)[1])[2])" "$a") ;;
  esac
done
ls "$root"/memory/*/*.md | sed 's|.*/||; s|\.md$||' | sort
`
	case "wrong":
		body = `#!/bin/sh
echo "mem_0000000000000000000000000"
`
	case "fail":
		body = `#!/bin/sh
echo "auth broken" >&2
exit 7
`
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func withFakeAgents(t *testing.T, modes map[string]string) {
	t.Helper()
	dir := t.TempDir()
	for name, mode := range modes {
		fakeAgentScript(t, dir, name, mode)
	}
	// 全量替换 PATH（仅保留系统基础路径供假脚本内 ls/sed/sort/python3）：
	// 预置式 PATH 会漏进真实 claude/codex——单测内跑真实 agent 会话（成本+不确定+递归执行风险）
	t.Setenv("PATH", dir+string(os.PathListSeparator)+"/usr/bin:/bin")
}

func buildSelf(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("平台护栏：假 agent 为 shell 脚本，仅 unix 形态（windows 走真机手测）")
	}
	if testBin == "" {
		t.Fatal("TestMain 未构建 testBin")
	}
	return testBin // 真实 CLI 产物（PATH 全隔离下假 agent 不执行它；防递归执行 go test 二进制）
}

func TestSuiteParityLiveFakesAgree(t *testing.T) {
	bin := buildSelf(t)
	withFakeAgents(t, map[string]string{"claude": "claude", "codex": "codex"})
	rep, err := SuiteParityLive(bin)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Passed {
		t.Fatalf("双假 agent 均按真值回显应全过: %+v", rep.Checks)
	}
	var hasClaude, hasCodex, hasAgree bool
	for _, c := range rep.Checks {
		if c.Name == "claude_live_ids_match_ground_truth" && c.Passed {
			hasClaude = true
		}
		if c.Name == "codex_live_ids_match_ground_truth" && c.Passed {
			hasCodex = true
		}
		if c.Name == "live_channels_agree" && c.Passed {
			hasAgree = true
		}
	}
	if !hasClaude || !hasCodex || !hasAgree {
		t.Fatalf("应含双通道比对与一致检查: %+v", rep.Checks)
	}
}

func TestSuiteParityLiveWrongIDsFail(t *testing.T) {
	bin := buildSelf(t)
	withFakeAgents(t, map[string]string{"claude": "wrong"})
	rep, err := SuiteParityLive(bin)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Passed {
		t.Fatal("回显错误 ID 集必须失败（宁可不知道，不能自信地错）")
	}
	found := false
	for _, c := range rep.Checks {
		if c.Name == "claude_live_ids_match_ground_truth" && !c.Passed && strings.Contains(c.Detail, "差集") {
			found = true
		}
	}
	if !found {
		t.Fatalf("失败细节应含差集: %+v", rep.Checks)
	}
}

func TestSuiteParityLiveSessionFailureFails(t *testing.T) {
	bin := buildSelf(t)
	withFakeAgents(t, map[string]string{"claude": "fail"})
	rep, err := SuiteParityLive(bin)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Passed {
		t.Fatal("agent 会话失败必须如实失败并披露 stderr")
	}
	ok := false
	for _, c := range rep.Checks {
		if c.Name == "claude_live_session" && !c.Passed && strings.Contains(c.Detail, "auth broken") {
			ok = true
		}
	}
	if !ok {
		t.Fatalf("失败细节应携带 stderr 尾部: %+v", rep.Checks)
	}
}

func TestSuiteParityLiveNoAgentsErrors(t *testing.T) {
	bin := buildSelf(t)
	t.Setenv("PATH", t.TempDir()) // 空 PATH：无任何 agent CLI
	_, err := SuiteParityLive(bin)
	if err == nil || !strings.Contains(err.Error(), "agent") {
		t.Fatalf("无 agent CLI 应显式报不可运行: %v", err)
	}
}
