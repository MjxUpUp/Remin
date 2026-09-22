// live parity（H5 实测扩展）：接入真实第二 agent 会话（claude -p / codex exec
// 一次性调用），agent 经各自 MCP 通道调 memory_search 并逐字回显命中 ID——
// 与核心确定性检索的真值集比对。通道构造不碰用户全局配置（claude 走 --mcp-config
// 临时文件；codex 走 -c 内联覆盖）。opt-in 套件（真实 agent 调用有成本，不入 all）。
package eval

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/remin-dev/remin/internal/core/audit"
	"github.com/remin-dev/remin/internal/core/inbox"
	"github.com/remin-dev/remin/internal/search"
)

// liveQuery 固定查询（确定性 BM25 → 真值集稳定）
const liveQuery = "数据库 部署 迁移"

// livePrompt 让 agent 调 memory_search 并逐字回显 ID（输出形态约束到可机械解析）
const livePrompt = "调用 memory_search 工具，参数 query 为「" + liveQuery +
	"」，top_k 为 3。然后把结果里所有 mem_ 开头的 ID 逐行原样输出，每行一个，不要输出任何其他文字。"

var liveIDRe = regexp.MustCompile(`mem_[A-Z0-9]{26}`)

// liveAgent 一个可实测的 agent 通道
type liveAgent struct {
	Name    string
	Bin     string
	Build   func(binPath, root, workdir string) (*exec.Cmd, error) // 构造一次性会话命令
	Cleanup func()
}

// detectLiveAgents 探测在场且可执行的真实 agent CLI（claude / codex）
func detectLiveAgents() []liveAgent {
	var agents []liveAgent
	if bin, err := exec.LookPath("claude"); err == nil {
		agents = append(agents, liveAgent{Name: "claude", Bin: bin, Build: buildClaudeLive})
	}
	if bin, err := exec.LookPath("codex"); err == nil {
		agents = append(agents, liveAgent{Name: "codex", Bin: bin, Build: buildCodexLive})
	}
	return agents
}

// buildClaudeLive claude 一次性会话：--mcp-config 临时文件注入 memory server，
// --allowedTools 只放行检索工具（prompt 必须置于变长 flag 之前，防被吞）
func buildClaudeLive(binPath, root, workdir string) (*exec.Cmd, error) {
	cfg := map[string]any{
		"mcpServers": map[string]any{
			"memory": map[string]any{
				"type":    "stdio",
				"command": binPath,
				"args":    []string{"mcp", "--root", root},
			},
		},
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	cfgFile := filepath.Join(workdir, "mcp.json")
	if err := os.WriteFile(cfgFile, data, 0o644); err != nil {
		return nil, err
	}
	cmd := exec.Command("claude", "-p", livePrompt, "--mcp-config", cfgFile,
		"--strict-mcp-config", // 只用本次注入的 memory server，不合并用户全局 MCP 配置
		"--allowedTools", "mcp__memory__memory_search")
	cmd.Dir = workdir
	return cmd, nil
}

// buildCodexLive codex 一次性会话：-c 内联覆盖 mcp_servers（TOML 值），不落配置文件
func buildCodexLive(binPath, root, workdir string) (*exec.Cmd, error) {
	argsJSON, err := json.Marshal([]string{"mcp", "--root", root})
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("codex", "exec", "--skip-git-repo-check",
		"-c", fmt.Sprintf("mcp_servers.memory.command=%q", binPath),
		"-c", fmt.Sprintf("mcp_servers.memory.args=%s", argsJSON),
		livePrompt)
	cmd.Dir = workdir
	return cmd, nil
}

// runLiveAgent 执行一次性 agent 会话，解析回显 ID 集（超时 120s）
func runLiveAgent(a liveAgent, binPath, root string) (ids []string, errDetails string, err error) {
	workdir, err := os.MkdirTemp("", "remin-live-"+a.Name+"-")
	if err != nil {
		return nil, "", err
	}
	defer os.RemoveAll(workdir)
	cmd, err := a.Build(binPath, root, workdir)
	if err != nil {
		return nil, "", err
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd = exec.CommandContext(ctx, cmd.Path, cmd.Args[1:]...)
	cmd.Dir = workdir
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		tail := strings.TrimSpace(stderr.String())
		if len(tail) > 300 {
			tail = tail[len(tail)-300:]
		}
		reason := err.Error()
		if ctx.Err() == context.DeadlineExceeded {
			reason = fmt.Sprintf("%s 会话超时（>120s，已终止）", a.Name)
		}
		return nil, fmt.Sprintf("stderr: %s", tail), fmt.Errorf("%s 会话失败: %s", a.Name, reason)
	}
	seen := map[string]bool{}
	for _, m := range liveIDRe.FindAllString(stdout.String(), -1) {
		if !seen[m] {
			seen[m] = true
			ids = append(ids, m)
		}
	}
	sort.Strings(ids)
	return ids, "", nil
}

// SuiteParityLive 真实第二 agent 会话 parity（opt-in：真实 agent 调用，有成本）。
// 真值 = 核心确定性检索（同查询同 top-k）的命中集；每个在场的 agent 通道
// 独立回显，须与真值逐集相等；多 agent 在场时另验彼此一致。
func SuiteParityLive(binPath string) (*Report, error) {
	if binPath == "" {
		return nil, fmt.Errorf("parity-live 需要可执行的 remin 二进制路径")
	}
	agents := detectLiveAgents()
	if len(agents) == 0 {
		return nil, fmt.Errorf("无可实测的 agent CLI（需 claude 或 codex 在 PATH 且已认证）——live parity 为 opt-in 套件")
	}
	rep := run("parity-live", func() []Check {
		st, err := newEvalStore()
		if st != nil {
			defer os.RemoveAll(st.Root)
		}
		if err != nil {
			return []Check{check("fixture", false, err.Error())}
		}
		in, au := inbox.New(st), audit.New(st)
		if _, err := promoteOne(st, in, au, "部署前必须检查数据库迁移脚本与回滚方案", "", nil); err != nil {
			return []Check{check("fixture", false, err.Error())}
		}
		if _, err := promoteOne(st, in, au, "数据库部署窗口固定在周五低峰，迁移需提前报备", "", nil); err != nil {
			return []Check{check("fixture", false, err.Error())}
		}
		// 真值：核心确定性检索（快照语义，同版本同查询同结果）
		se, err := searcher(st)
		if err != nil {
			return []Check{check("ground_truth", false, err.Error())}
		}
		res := se.Search(liveQuery, search.Options{TopK: 3})
		if res.Abstained {
			return []Check{check("ground_truth", false, "核心检索弃权（fixture 异常）: "+res.Reason)}
		}
		want := []string{}
		for _, h := range res.Hits {
			want = append(want, h.ID)
		}
		sort.Strings(want)
		// fixture 两条正文均含全部查询词（Han 一元+二元分词下双命中）——钉死真值规模，
		// 防分词/检索漂移静默退化成单命中仍「对上」的假绿
		if len(want) != 2 {
			return []Check{check("ground_truth", false, fmt.Sprintf("核心检索应双命中，实际 %d: %v", len(want), want))}
		}

		var checks []Check
		sets := map[string][]string{}
		ranCount := 0
		for _, a := range agents {
			ids, detail, err := runLiveAgent(a, binPath, st.Root)
			if err != nil {
				checks = append(checks, check(a.Name+"_live_session", false, err.Error()+"｜"+detail))
				continue
			}
			ranCount++
			sets[a.Name] = ids
			checks = append(checks, check(a.Name+"_live_ids_match_ground_truth",
				equalSets(ids, want),
				fmt.Sprintf("agent=%v 真值=%v（差集 agent−真值=%v 真值−agent=%v）",
					ids, want, diff(ids, want), diff(want, ids))))
		}
		if ranCount >= 2 {
			names := make([]string, 0, len(sets))
			for n := range sets {
				names = append(names, n)
			}
			sort.Strings(names)
			agree := true
			for i := 1; i < len(names); i++ {
				agree = agree && equalSets(sets[names[0]], sets[names[i]])
			}
			checks = append(checks, check("live_channels_agree", agree,
				fmt.Sprintf("通道 ID 集：%v", sets)))
		}
		return checks
	})
	return rep, nil
}

func equalSets(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func diff(a, b []string) []string {
	var out []string
	set := map[string]bool{}
	for _, x := range b {
		set[x] = true
	}
	for _, x := range a {
		if !set[x] {
			out = append(out, x)
		}
	}
	return out
}
