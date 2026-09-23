// agent headless 深提取引擎：复用本机已认证的 agent CLI（claude -p / codex exec）
// 作为深路径第一优先级引擎——数据不产生新的外流面（transcript 本就是该 agent 产的），
// 且复用用户已有订阅额度；手动配置的 llm 端点为第二选择。
// 输出走与 LLM 端点完全相同的解析（deepParseContent）与 quote 逐字溯源守卫（deepGuard）。
package extractor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/remin-dev/remin/internal/core/config"
	"github.com/remin-dev/remin/internal/core/inbox"
)

// 引擎种类
const (
	DeepEngineAgent = "agent"
	DeepEngineLLM   = "llm"
	DeepEngineNone  = "none"
)

// DeepEngine 深路径引擎解析结果
type DeepEngine struct {
	Kind  string // agent | llm | none
	Agent string // claude | codex（Kind=agent 时）
}

// deepAgentOrder agent 探测顺序（与 parity-live 一致：claude 先于 codex）
var deepAgentOrder = []string{"claude", "codex"}

// DeepAgentLookPath agent 探测的可注入执行面（测试替身用）
var DeepAgentLookPath = exec.LookPath

// DetectDeepAgents 探测在场且可执行的 agent CLI（确定性顺序）
func DetectDeepAgents() []string {
	var found []string
	for _, name := range deepAgentOrder {
		if _, err := DeepAgentLookPath(name); err == nil {
			found = append(found, name)
		}
	}
	return found
}

// ResolveDeepEngine 引擎解析序（用户定义）：agent（在场即用，第一个探测到的）
// > llm（已配端点+密钥）> none（调用方显式报错，不静默降级）。
// REMIN_DEEP_ENGINE=llm|agent 可钉扎（跳过探测直接走指定引擎；测试/演练确定性，
// 用户也可强制 llm 优先）。
func ResolveDeepEngine(llm *config.LLMConfig) DeepEngine {
	switch os.Getenv("REMIN_DEEP_ENGINE") {
	case "llm":
		// 独占：钉 llm 即跳过 agent 探测（未配置→none，不回落 auto；非法值回落 auto）
		if llm != nil && llm.Endpoint != "" && llm.APIKey != "" {
			return DeepEngine{Kind: DeepEngineLLM}
		}
		return DeepEngine{Kind: DeepEngineNone}
	case "agent":
		// 独占：钉 agent 即不用 llm（不在线→none）
		if agents := DetectDeepAgents(); len(agents) > 0 {
			return DeepEngine{Kind: DeepEngineAgent, Agent: agents[0]}
		}
		return DeepEngine{Kind: DeepEngineNone}
	}
	if agents := DetectDeepAgents(); len(agents) > 0 {
		return DeepEngine{Kind: DeepEngineAgent, Agent: agents[0]}
	}
	if llm != nil && llm.Endpoint != "" && llm.APIKey != "" {
		return DeepEngine{Kind: DeepEngineLLM}
	}
	return DeepEngine{Kind: DeepEngineNone}
}

// agentDeepTimeout agent 一次性会话硬预算（headless 冷启动+生成，比裸 API 慢；测试可注入缩短）
var agentDeepTimeout = 180 * time.Second

// DeepEchoSentinel 提取会话回灌判别标记：agent 的会话日志含完整提取 prompt（含本
// 标记 + 源 transcript 原文）——这些日志落在我们的发现根里，下次挖矿会再见到它们。
// 含此标记的 transcript 是我们自己的提取回声（内容已处理过），整文件跳过防无限回灌。
const DeepEchoSentinel = "<remin-deep-extraction-session>"

// agentBinArgs 各 agent 的一次性调用形态（prompt 走 stdin——长文本超 argv 上限；
// 工具面收紧到零：提取是纯文本任务，禁工具同时封死 prompt 注入的行动面）
func agentBinArgs(agent string) (bin string, args []string, err error) {
	switch agent {
	case "claude":
		// --allowedTools ""：不授权任何工具；--strict-mcp-config：不带用户 MCP 服务器
		return "claude", []string{"-p", "--allowedTools", "", "--strict-mcp-config"}, nil
	case "codex":
		// --sandbox read-only：只读沙箱（无文件写/命令执行）
		return "codex", []string{"exec", "--skip-git-repo-check", "--sandbox", "read-only", "-"}, nil
	}
	return "", nil, fmt.Errorf("未知 agent 引擎 %q（可选: claude/codex）", agent)
}

// scrubAgentEnv 净化传给 agent 子进程的环境：剔除密钥类变量（提取 prompt 含不可信
// transcript 文本——子进程不该拿到任何可外传的凭据）
func scrubAgentEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "REMIN_LLM_API_KEY=") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// cappedWriter 有界输出（对齐 llm 路径 1MB 响应上限）
type cappedWriter struct {
	sb     strings.Builder
	remain int
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	n := len(p)
	if w.remain <= 0 {
		return n, nil // 超限丢弃（解析会失败→弃权，符合语义）
	}
	if len(p) > w.remain {
		p = p[:w.remain]
	}
	w.remain -= len(p)
	w.sb.Write(p)
	return n, nil
}

// ExtractDeepAgent 用 agent headless 会话执行深提取（与 ExtractDeep 同构：
// 相同的系统约束、相同的解析与守卫；失败弃权不拖垮快速路径）。
func ExtractDeepAgent(ctx context.Context, agent string, events []Event) ([]*inbox.Candidate, error) {
	if _, err := DeepAgentLookPath(agent); err != nil {
		return nil, fmt.Errorf("深度提取 agent %s 不在 PATH（安装并登录后重试，或在 config.yaml 配 llm 节走端点引擎）", agent)
	}
	if len(events) == 0 {
		return nil, nil
	}
	bin, args, err := agentBinArgs(agent)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, agentDeepTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdin = strings.NewReader(deepAgentPrompt(events))
	// 隔离：临时工作目录（不继承用户项目 CWD——防 CLAUDE.md/项目 MCP 污染提取语境）、
	// 净化 env（密钥不进子进程）、WaitDelay（超时杀直系子进程后强制关管道——agent 的
	// 孙进程持有 stdout 管道时 Wait 不至于无限等待，预算才是真硬）
	sandboxDir, err2 := os.MkdirTemp("", "remin-deep-agent-")
	if err2 == nil {
		cmd.Dir = sandboxDir
		defer os.RemoveAll(sandboxDir)
	}
	cmd.Env = scrubAgentEnv()
	stdout := &cappedWriter{remain: deepMaxResponseBytes}
	stderr := &cappedWriter{remain: 1 << 16}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.WaitDelay = 15 * time.Second
	if err := cmd.Run(); err != nil {
		tail := strings.TrimSpace(stderr.sb.String())
		if len(tail) > 300 {
			tail = tail[len(tail)-300:]
		}
		reason := err.Error()
		if ctx.Err() == context.DeadlineExceeded {
			reason = "会话超时（>3min，已终止）"
		}
		return nil, fmt.Errorf("深度提取 %s 会话失败: %s｜stderr: %s", agent, reason, tail)
	}

	raw, err := deepParseContent(stdout.sb.String())
	if err != nil {
		return nil, fmt.Errorf("深度提取 %s 响应解析失败: %w", agent, err)
	}
	if len(raw) > deepMaxCandidates {
		raw = raw[:deepMaxCandidates]
	}
	return deepGuard(raw, events), nil
}

// deepAgentPrompt agent 一次性调用的完整提示词（系统约束 + 事件正文，stdin 递入）
func deepAgentPrompt(events []Event) string {
	if len(events) > deepMaxEvents {
		events = events[len(events)-deepMaxEvents:]
	}
	var sb strings.Builder
	sb.WriteString(deepSystemPrompt)
	sb.WriteString("\n" + DeepEchoSentinel + "\n\n--- 会话记录开始 ---\n")
	for _, ev := range events {
		text := truncate(cleanText(ev.Text), deepMaxEventRunes)
		if text == "" {
			continue
		}
		sb.WriteString(ev.Role + ": " + text + "\n")
	}
	sb.WriteString("--- 会话记录结束 ---\n只输出 JSON 数组。")
	return sb.String()
}

func deepEnginePinDesc() string {
	if v := os.Getenv("REMIN_DEEP_ENGINE"); v != "" {
		return v
	}
	return "auto"
}

// ExtractDeepAuto 引擎自动调度：agent（第一优先级）→ llm（第二）→ 显式报错。
// 供 mine --deep 与 tick 排空共用；调用方无需感知引擎细节。
func ExtractDeepAuto(ctx context.Context, llm *config.LLMConfig, events []Event) ([]*inbox.Candidate, error) {
	e := ResolveDeepEngine(llm)
	switch e.Kind {
	case DeepEngineAgent:
		return ExtractDeepAgent(ctx, e.Agent, events)
	case DeepEngineLLM:
		return ExtractDeep(ctx, llm, events)
	}
	return nil, fmt.Errorf("深度提取无可用引擎：未探测到 agent CLI（claude/codex），且 config.yaml 未配 llm 节或未设 REMIN_LLM_API_KEY——装其一即可")
}

// DeepEngineAvailable 引擎可用性（deep 待挖队列挂账与排空门条件）：
// agent 在场或 llm 端点已配——密钥不在此要求（密钥可以后出现：挂账时无密钥、
// 排空时已 export 是合法时序；缺密钥的实际调用由引擎自身显式报错）
func DeepEngineAvailable(llm *config.LLMConfig) bool {
	switch os.Getenv("REMIN_DEEP_ENGINE") {
	case "llm":
		return llm != nil && llm.Endpoint != ""
	case "agent":
		return len(DetectDeepAgents()) > 0
	}
	return len(DetectDeepAgents()) > 0 || (llm != nil && llm.Endpoint != "")
}
