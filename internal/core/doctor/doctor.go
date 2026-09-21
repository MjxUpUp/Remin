// Package doctor agent 接线（FR-INT）：检测已装 agent、写各自全局配置（repo 零污染）、
// 健康检查、一键接管同名 memory server。写前备份，JSON 键级合并只增不删。
package doctor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// 支持的 agent 清单
const (
	AgentClaudeCode = "claude-code"
	AgentCodex      = "codex"
	AgentCursor     = "cursor"
	AgentGeminiCLI  = "gemini-cli"
)

var AllAgents = []string{AgentClaudeCode, AgentCodex, AgentCursor, AgentGeminiCLI}

// AgentStatus 检测结果
type AgentStatus struct {
	Agent      string `json:"agent"`
	Installed  bool   `json:"installed"`
	Wired      bool   `json:"wired"`      // remin 已接线
	Conflicted bool   `json:"conflicted"` // 存在同名 memory server（非 remin）
	Note       string `json:"note,omitempty"`
}

// Detect 检测已装 agent 与接线状态
func Detect(home, binPath string) []AgentStatus {
	out := []AgentStatus{
		detectClaudeCode(home, binPath),
		detectCodex(home, binPath),
		detectCursor(home, binPath),
		detectGemini(home, binPath),
	}
	return out
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func mcpEntry(binPath string) map[string]any {
	return map[string]any{
		"type":    "stdio",
		"command": binPath,
		"args":    []string{"mcp"},
		"env":     map[string]any{},
	}
}

// ── claude-code ───────────────────────────────────────────────────────────────

func detectClaudeCode(home, binPath string) AgentStatus {
	s := AgentStatus{Agent: AgentClaudeCode}
	global := filepath.Join(home, ".claude.json")
	settings := filepath.Join(home, ".claude")
	s.Installed = exists(global) || exists(settings)
	if !s.Installed {
		return s
	}
	if cfg := readJSONObjectLenient(global); cfg != nil {
		servers, _ := cfg["mcpServers"].(map[string]any)
		switch ent := servers["memory"].(type) {
		case map[string]any:
			if cmd, _ := ent["command"].(string); cmd == binPath {
				s.Wired = true
			} else {
				s.Conflicted = true
				s.Note = "存在同名 memory server（--takeover 替换）"
			}
		}
	}
	return s
}

// InstallClaudeCode ~/.claude.json 注册 MCP + ~/.claude/settings.json 注册两会话 hook。
// 返回本次生效的 effects（含未发生写入但已处于我们名下的项——台账反映归属现状）
func InstallClaudeCode(home, binPath string, takeover bool) ([]Effect, error) {
	var effects []Effect
	global := filepath.Join(home, ".claude.json")
	cfg, err := readJSONObject(global)
	if err != nil {
		return nil, err
	}
	servers, _ := cfg["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	ent := mcpEntry(binPath)
	needWrite := true
	if cur, exists := servers["memory"].(map[string]any); exists {
		if cmd, _ := cur["command"].(string); cmd == binPath {
			needWrite = false // 已接线，幂等
		} else if !takeover {
			return nil, fmt.Errorf("已存在同名 memory server（command=%q）——确认接管请加 --takeover", cmd)
		}
	}
	if needWrite {
		servers["memory"] = ent
		cfg["mcpServers"] = servers
		bk, err := backupAndWriteJSON(global, cfg)
		if err != nil {
			return nil, err
		}
		effects = append(effects, Effect{ID: effectID("mcp-json", global, "mcpServers.memory"),
			Kind: "mcp-json", File: global, Key: "mcpServers.memory", Command: binPath, Backup: bk,
			Created: bk == ""})
	} else {
		effects = append(effects, Effect{ID: effectID("mcp-json", global, "mcpServers.memory"),
			Kind: "mcp-json", File: global, Key: "mcpServers.memory", Command: binPath})
	}

	settingsPath := filepath.Join(home, ".claude", "settings.json")
	sc, err := readJSONObject(settingsPath)
	if err != nil {
		return nil, err
	}
	hooks, _ := sc["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	// shell 命令里的路径含空格/特殊字符必须加引号（hook 由 agent 经 shell 执行）
	injectCmd := shellQuote(binPath) + " inject"
	stopCmd := shellQuote(binPath) + " hook-stop"
	startList, addStart := appendHookOnce(hooks["SessionStart"], injectCmd)
	stopList, addStop := appendHookOnce(hooks["Stop"], stopCmd)
	if addStart || addStop {
		hooks["SessionStart"] = startList
		hooks["Stop"] = stopList
		sc["hooks"] = hooks
		bk, err := backupAndWriteJSON(settingsPath, sc)
		if err != nil {
			return nil, err
		}
		created := bk == ""
		effects = append(effects,
			Effect{ID: effectID("hook-entry", settingsPath, "SessionStart"), Kind: "hook-entry",
				File: settingsPath, Key: "SessionStart", Command: injectCmd, Backup: bk, Created: created},
			Effect{ID: effectID("hook-entry", settingsPath, "Stop"), Kind: "hook-entry",
				File: settingsPath, Key: "Stop", Command: stopCmd, Backup: bk, Created: created})
	} else {
		effects = append(effects,
			Effect{ID: effectID("hook-entry", settingsPath, "SessionStart"), Kind: "hook-entry",
				File: settingsPath, Key: "SessionStart", Command: injectCmd},
			Effect{ID: effectID("hook-entry", settingsPath, "Stop"), Kind: "hook-entry",
				File: settingsPath, Key: "Stop", Command: stopCmd})
	}
	return effects, nil
}

func jsonEscape(s string) string { return strings.ReplaceAll(s, "\"", "\\\"") }

// shellQuote 包裹双引号并转义内部引号与反斜杠
func shellQuote(s string) string {
	return "\"" + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + "\""
}

// appendHookOnce Claude Code hooks 形态：[{matcher?, hooks:[{type:command, command}]}]
// 返回（可能追加了 command 的列表, 是否发生了追加）
func appendHookOnce(existing any, command string) ([]any, bool) {
	var list []any
	if l, ok := existing.([]any); ok {
		list = l
	}
	for _, m := range list {
		mm, _ := m.(map[string]any)
		hs, _ := mm["hooks"].([]any)
		for _, h := range hs {
			hh, _ := h.(map[string]any)
			if c, _ := hh["command"].(string); c == command {
				return list, false // 已接线，幂等
			}
		}
	}
	list = append(list, map[string]any{
		"hooks": []any{map[string]any{"type": "command", "command": command}},
	})
	return list, true
}

// ── codex（config.toml）───────────────────────────────────────────────────────

func detectCodex(home, binPath string) AgentStatus {
	s := AgentStatus{Agent: AgentCodex}
	path := filepath.Join(home, ".codex", "config.toml")
	s.Installed = exists(path) || exists(filepath.Join(home, ".codex"))
	if !s.Installed {
		return s
	}
	data, _ := os.ReadFile(path)
	section := tomlSection(string(data), "mcp_servers.memory")
	if string(section) == "" {
		return s
	}
	if strings.Contains(string(section), "command = \""+binPath+"\"") {
		s.Wired = true
	} else {
		s.Conflicted = true
		s.Note = "存在同名 memory server（--takeover 替换）"
	}
	return s
}

func InstallCodex(home, binPath string, takeover bool) ([]Effect, error) {
	path := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	data, _ := os.ReadFile(path)
	body := string(data)
	section := tomlSection(body, "mcp_servers.memory")
	if len(section) > 0 {
		if strings.Contains(string(section), "command = \""+binPath+"\"") {
			// 已接线，幂等（仍记账：归属现状）
			return []Effect{{ID: effectID("toml-section", path, "mcp_servers.memory"),
				Kind: "toml-section", File: path, Section: "mcp_servers.memory", Command: binPath}}, nil
		}
		if !takeover {
			return nil, fmt.Errorf("已存在 [mcp_servers.memory]——确认接管请加 --takeover")
		}
		body = strings.Replace(body, string(section), "", 1)
	}
	body = strings.TrimRight(body, "\n") + "\n\n[mcp_servers.memory]\ncommand = \"" + binPath + "\"\nargs = [\"mcp\"]\n"
	bk, err := backupAndWrite(path, []byte(body))
	if err != nil {
		return nil, err
	}
	return []Effect{{ID: effectID("toml-section", path, "mcp_servers.memory"),
		Kind: "toml-section", File: path, Section: "mcp_servers.memory", Command: binPath, Backup: bk,
		Created: bk == ""}}, nil
}

// tomlSection 提取 [section] 段（含头，到下一个 [ 段或 EOF）。
// 尾部空行不入段：段串必须与 body 精确子串匹配（Replace 删除依赖此性质）。
func tomlSection(body, section string) []byte {
	lines := strings.Split(body, "\n")
	var out []string
	in := false
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "[") {
			if in {
				break
			}
			if t == "["+section+"]" {
				in = true
			}
		}
		if in {
			out = append(out, l)
		}
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	if len(out) == 0 {
		return nil
	}
	return []byte(strings.Join(out, "\n") + "\n")
}

// ── cursor / gemini-cli（JSON mcpServers）────────────────────────────────────

func installJSONMCPServers(path string, binPath string, takeover bool) ([]Effect, error) {
	cfg, err := readJSONObject(path)
	if err != nil {
		return nil, err
	}
	servers, _ := cfg["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	needWrite := true
	if cur, exists := servers["memory"].(map[string]any); exists {
		if cmd, _ := cur["command"].(string); cmd == binPath {
			needWrite = false // 已接线，幂等
		} else if !takeover {
			return nil, fmt.Errorf("已存在同名 memory server（command=%q）——确认接管请加 --takeover", cmd)
		}
	}
	e := Effect{ID: effectID("mcp-json", path, "mcpServers.memory"),
		Kind: "mcp-json", File: path, Key: "mcpServers.memory", Command: binPath}
	if needWrite {
		servers["memory"] = mcpEntry(binPath)
		cfg["mcpServers"] = servers
		bk, err := backupAndWriteJSON(path, cfg)
		if err != nil {
			return nil, err
		}
		e.Backup = bk
		e.Created = bk == "" // 无备份 ⇒ 写前文件不存在，是我们创建的
	}
	return []Effect{e}, nil
}

func detectCursor(home, binPath string) AgentStatus {
	return detectJSONMCPServer(AgentCursor, filepath.Join(home, ".cursor", "mcp.json"), binPath)
}

func detectGemini(home, binPath string) AgentStatus {
	return detectJSONMCPServer(AgentGeminiCLI, filepath.Join(home, ".gemini", "settings.json"), binPath)
}

func detectJSONMCPServer(agent, path, binPath string) AgentStatus {
	s := AgentStatus{Agent: agent}
	s.Installed = exists(filepath.Dir(path))
	if !s.Installed {
		return s
	}
	cfg := readJSONObjectLenient(path)
	servers, _ := cfg["mcpServers"].(map[string]any)
	if ent, ok := servers["memory"].(map[string]any); ok {
		if cmd, _ := ent["command"].(string); cmd == binPath {
			s.Wired = true
		} else {
			s.Conflicted = true
			s.Note = "存在同名 memory server（--takeover 替换）"
		}
	}
	return s
}

func InstallCursor(home, binPath string, takeover bool) ([]Effect, error) {
	path := filepath.Join(home, ".cursor", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return installJSONMCPServers(path, binPath, takeover)
}

func InstallGemini(home, binPath string, takeover bool) ([]Effect, error) {
	path := filepath.Join(home, ".gemini", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return installJSONMCPServers(path, binPath, takeover)
}

// Install 一键接线（全部已装 agent；写前备份；全部 effect 入台账）
func Install(home, root, binPath string, takeover bool) ([]AgentStatus, error) {
	var done []AgentStatus
	ledger, err := LoadLedger(root)
	if err != nil {
		return done, fmt.Errorf("读取接线台账失败: %w", err)
	}
	for _, s := range Detect(home, binPath) {
		if !s.Installed {
			continue
		}
		var effects []Effect
		var err error
		switch s.Agent {
		case AgentClaudeCode:
			effects, err = InstallClaudeCode(home, binPath, takeover)
		case AgentCodex:
			effects, err = InstallCodex(home, binPath, takeover)
		case AgentCursor:
			effects, err = InstallCursor(home, binPath, takeover)
		case AgentGeminiCLI:
			effects, err = InstallGemini(home, binPath, takeover)
		}
		if err != nil {
			return done, fmt.Errorf("%s 接线失败: %w", s.Agent, err)
		}
		for _, e := range effects {
			e.Agent = s.Agent
			ledger.Append(e)
		}
		done = append(done, AgentStatus{Agent: s.Agent, Installed: true, Wired: true})
	}
	if err := ledger.Save(root); err != nil {
		return done, fmt.Errorf("接线台账写入失败: %w", err)
	}
	return done, nil
}

// ── 备份与写回 ────────────────────────────────────────────────────────────────
// 备份保留策略：每个目标文件至多 maxBackups 份（时间戳后缀排序，旧的先清），
// 反复 doctor --install 不再无限堆积 .remin-backup-* 污染用户目录。

const maxBackups = 3

func backupAndWriteJSON(path string, cfg map[string]any) (string, error) {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", err
	}
	return backupAndWrite(path, append(data, '\n'))
}

func backupAndWrite(path string, data []byte) (string, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	backup := ""
	if old, err := os.ReadFile(path); err == nil {
		backup = path + ".remin-backup-" + strings.ReplaceAll(nowStamp(), ":", "")
		if err := os.WriteFile(backup, old, 0o644); err != nil {
			return "", err
		}
		pruneBackups(path)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return backup, nil
}

// pruneBackups 只保留最新 maxBackups 份（backupAndWrite 刚写的那份最新）
func pruneBackups(path string) {
	matches, _ := filepath.Glob(path + ".remin-backup-*")
	if len(matches) <= maxBackups {
		return
	}
	sort.Strings(matches) // 时间戳字典序 = 时间序
	for _, old := range matches[:len(matches)-maxBackups] {
		_ = os.Remove(old)
	}
}

func nowStamp() string { return timeNow().Format("20060102T150405") }

var timeNow = func() time.Time { return time.Now() }

// readJSONObject 读 JSON 对象（缺失返回空 map；损坏返回错误——绝不把用户现有
// 配置吞成空 map 后整体覆写，违反"键级合并只增不删"的承诺）
func readJSONObject(path string) (map[string]any, error) {
	cfg := map[string]any{}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("现有配置 %s 损坏（非 JSON）：%w——拒绝覆写，请先修复或删除", path, err)
	}
	return cfg, nil
}

// readJSONObjectLenient 检测场景容错读取（损坏视为无配置，只读不写）
func readJSONObjectLenient(path string) map[string]any {
	cfg, err := readJSONObject(path)
	if err != nil {
		return map[string]any{}
	}
	return cfg
}
