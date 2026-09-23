package extractor

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/remin-dev/remin/internal/core/config"
)

// agent headless 深提取契约：探测本机 agent（claude/codex 一次性调用）作为深路径
// 引擎（第一优先级，手动 llm 第二）；提示词携带系统约束+事件正文；输出走与
// LLM 端点完全相同的解析与 quote 逐字溯源守卫；引擎解析序 agent > llm；
// 无引擎可用时显式报错（不静默降级）。

const agentDeepFixture = `{"type":"user","sessionId":"s1","cwd":"/p","timestamp":"2026-09-23T10:00:00+08:00","message":{"role":"user","content":"这个项目验证要走 make constitution 才完整，单跑 go test 会漏依赖扫描"}}
{"type":"assistant","sessionId":"s1","timestamp":"2026-09-23T10:01:00+08:00","message":{"role":"assistant","content":[{"type":"text","text":"明白了"}]}}
`

func withFakeAgent(t *testing.T, name string, respond string, fail bool) string {
	t.Helper()
	dir := t.TempDir()
	body := "#!/bin/sh\n"
	if fail {
		body += `echo "agent auth failed" >&2
exit 1
`
	} else {
		// 假 agent：把收到的 prompt 落盘（供断言），回固定响应
		body += `cat > "` + dir + `/prompt.txt"
printf '%s\n' '` + respond + `'
`
	}
	p := filepath.Join(dir, name)
	os.WriteFile(p, []byte(body), 0o755)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+"/usr/bin:/bin")
	return dir
}

func TestAgentDeepClaude(t *testing.T) {
	dir := withFakeAgent(t, "claude", `[{"type":"preference","body":"验证统一走 make constitution，不单跑 go test","quote":"这个项目验证要走 make constitution 才完整，单跑 go test 会漏依赖扫描"}]`, false)
	events := parseFixture(t, agentDeepFixture)
	cands, err := ExtractDeepAgent(ctxbg(), "claude", events)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 1 || cands[0].Body != "验证统一走 make constitution，不单跑 go test" {
		t.Fatalf("假 agent 响应应产 1 候选: %+v", cands)
	}
	// 提示词含系统约束与事件正文
	promptData, _ := os.ReadFile(filepath.Join(dir, "prompt.txt"))
	prompt := string(promptData)
	if !strings.Contains(prompt, "quote") || !strings.Contains(prompt, "make constitution") {
		t.Fatalf("prompt 应含系统约束与事件正文:\n%s", prompt[:min(300, len(prompt))])
	}
	if !strings.Contains(prompt, "type") {
		t.Fatalf("prompt 应含类型白名单")
	}
}

func TestAgentDeepCodex(t *testing.T) {
	withFakeAgent(t, "codex", `[{"type":"procedural","body":"部署前先看 runbook","quote":"这个项目验证要走 make constitution 才完整"}]`, false)
	events := parseFixture(t, agentDeepFixture)
	cands, err := ExtractDeepAgent(ctxbg(), "codex", events)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 1 || cands[0].Type != "procedural" {
		t.Fatalf("codex 假 agent 应产 1 procedural: %+v", cands)
	}
}

// TestAgentDeepGuardRejectsFabricatedQuote 编造 quote 拒收（守卫引擎无关）
func TestAgentDeepGuardRejectsFabricatedQuote(t *testing.T) {
	withFakeAgent(t, "claude", `[{"type":"preference","body":"x","quote":"这句原话不在会话里"}]`, false)
	events := parseFixture(t, agentDeepFixture)
	cands, err := ExtractDeepAgent(ctxbg(), "claude", events)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 0 {
		t.Fatalf("编造 quote 应拒收: %+v", cands)
	}
}

// TestAgentDeepCallFailureFailsExplicitly agent 失败如实报错（弃权语义同 LLM 端点）
func TestAgentDeepCallFailureFailsExplicitly(t *testing.T) {
	withFakeAgent(t, "claude", "", true)
	events := parseFixture(t, agentDeepFixture)
	_, err := ExtractDeepAgent(ctxbg(), "claude", events)
	if err == nil || !strings.Contains(err.Error(), "claude") {
		t.Fatalf("agent 失败应显式报错: %v", err)
	}
}

// TestAgentDeepNoAgentError 无 agent 在场显式报错
func TestAgentDeepNoAgentError(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	events := parseFixture(t, agentDeepFixture)
	_, err := ExtractDeepAgent(ctxbg(), "claude", events)
	if err == nil || !strings.Contains(err.Error(), "claude") {
		t.Fatalf("无 agent 应报错: %v", err)
	}
}

// TestResolveDeepEngine 引擎解析序：agent 优先于 llm（用户定义的第一/第二优先级）
func TestResolveDeepEngine(t *testing.T) {
	withFakeAgent(t, "claude", `[]`, false)
	// agent 在场 → agent 引擎
	e := ResolveDeepEngine(nil)
	if e.Kind != DeepEngineAgent || e.Agent != "claude" {
		t.Fatalf("agent 在场应为 agent 引擎: %+v", e)
	}
	// agent 不在场 + llm 已配 → llm 引擎
	t.Setenv("PATH", t.TempDir())
	e = ResolveDeepEngine(withTestLLM())
	if e.Kind != DeepEngineLLM {
		t.Fatalf("仅 llm 应为 llm 引擎: %+v", e)
	}
	// 都不在场 → none（调用方显式报错）
	e = ResolveDeepEngine(nil)
	if e.Kind != DeepEngineNone {
		t.Fatalf("无引擎应为 none: %+v", e)
	}
}

// ── 测试脚手架 ───────────────────────────────────────────────────────────────

func ctxbg() context.Context { return context.Background() }

func parseFixture(t *testing.T, jsonl string) []Event {
	t.Helper()
	// 复用 miner 的 claude-jsonl 解析逻辑形状（这里直接构造——extractor 不依赖 miner）
	return []Event{
		{Line: 1, Role: "user", Text: "这个项目验证要走 make constitution 才完整，单跑 go test 会漏依赖扫描", SessionID: "s1", CWD: "/p", Timestamp: "2026-09-23T10:00:00+08:00", Origin: "claude-code"},
		{Line: 2, Role: "assistant", Text: "明白了", SessionID: "s1", Timestamp: "2026-09-23T10:01:00+08:00", Origin: "claude-code"},
	}
}

func withTestLLM() *config.LLMConfig {
	return &config.LLMConfig{Endpoint: "http://x", Model: "m", APIKey: "k"}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestAgentDeepTimeoutKillsOrphans 超时硬杀：假 agent 派生孙进程持有 stdout 管道，
// WaitDelay 强制关管道——cmd.Run 必须在预算+宽限内返回（审查 P2 杀灭测试）
func TestAgentDeepTimeoutKillsOrphans(t *testing.T) {
	dir := t.TempDir()
	// 假 agent：打印一行后派生 sleep 孙进程（持有继承的 stdout）
	script := "#!/bin/sh\nprintf '%s\\n' '[{\"type\":\"semantic\",\"body\":\"x\",\"quote\":\"y\"}]'\n/bin/sleep 30 &\nwait\n"
	os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0o755)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+"/usr/bin:/bin")
	old := agentDeepTimeout
	agentDeepTimeout = 2 * time.Second
	t.Cleanup(func() { agentDeepTimeout = old })
	events := parseFixture(t, agentDeepFixture)
	start := time.Now()
	_, err := ExtractDeepAgent(ctxbg(), "claude", events)
	d := time.Since(start)
	if err == nil {
		t.Fatal("超时应报错")
	}
	if d >= 30*time.Second {
		t.Fatalf("孙进程持管道不得拖过整个 sleep 时长（WaitDelay 应已强制关管道）: %v", d)
	}
	if d < 2*time.Second {
		t.Fatalf("应等到超时后才返回: %v", d)
	}
}

// TestDeepEchoSentinelSkip 回灌哨兵：含提取标记的 transcript 被挖矿跳过
func TestDeepEchoSentinelSkip(t *testing.T) {
	dir := t.TempDir()
	// 一个正常文件 + 一个提取回声文件
	os.WriteFile(filepath.Join(dir, "normal.jsonl"), []byte(agentDeepFixture), 0o644)
	echo := `{"type":"user","sessionId":"e1","cwd":"/p","timestamp":"2026-09-23T12:00:00+08:00","message":{"role":"user","content":"` + DeepEchoSentinel + ` 你是个人记忆系统的提取器…源文本回声"}}
`
	os.WriteFile(filepath.Join(dir, "rollout-echo.jsonl"), []byte(echo), 0o644)
	data1, _ := os.ReadFile(filepath.Join(dir, "rollout-echo.jsonl"))
	data2, _ := os.ReadFile(filepath.Join(dir, "normal.jsonl"))
	if !bytes.Contains(data1, []byte(DeepEchoSentinel)) {
		t.Fatal("哨兵文件应含标记")
	}
	if bytes.Contains(data2, []byte(DeepEchoSentinel)) {
		t.Fatal("正常文件不得含标记")
	}
}

// TestEnginePinBranches 钉扎分支：llm-pin 未配置→none；agent-pin 无 agent→none
func TestEnginePinBranches(t *testing.T) {
	t.Setenv("REMIN_DEEP_ENGINE", "llm")
	if e := ResolveDeepEngine(nil); e.Kind != DeepEngineNone {
		t.Fatalf("llm 钉扎未配置应 none: %+v", e)
	}
	t.Setenv("REMIN_DEEP_ENGINE", "agent")
	t.Setenv("PATH", t.TempDir())
	if e := ResolveDeepEngine(withTestLLM()); e.Kind != DeepEngineNone {
		t.Fatalf("agent 钉扎无 agent 应 none（不回落 llm）: %+v", e)
	}
	// 可用性同步尊重钉扎
	t.Setenv("REMIN_DEEP_ENGINE", "llm")
	if DeepEngineAvailable(nil) {
		t.Fatal("llm 钉扎无端点应不可用")
	}
	if !DeepEngineAvailable(withTestLLM()) {
		t.Fatal("llm 钉扎有端点应可用")
	}
}
