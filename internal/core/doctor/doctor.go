// Package doctor agent 接线（FR-INT）：检测已装 agent、写各自全局配置（repo 零污染）、
// 健康检查、一键接管同名 memory server。写前备份，JSON 键级合并只增不删。
package doctor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

// InstallClaudeCode ~/.claude.json 注册 MCP + ~/.claude/settings.json 注册两会话 hook
func InstallClaudeCode(home, binPath string, takeover bool) error {
	global := filepath.Join(home, ".claude.json")
	cfg, err := readJSONObject(global)
	if err != nil {
		return err
	}
	servers, _ := cfg["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	if ent, exists := servers["memory"].(map[string]any); exists {
		if cmd, _ := ent["command"].(string); cmd != binPath && !takeover {
			return fmt.Errorf("已存在同名 memory server（command=%q）——确认接管请加 --takeover", cmd)
		}
	}
	servers["memory"] = mcpEntry(binPath)
	cfg["mcpServers"] = servers
	if err := backupAndWriteJSON(global, cfg); err != nil {
		return err
	}

	settingsPath := filepath.Join(home, ".claude", "settings.json")
	sc, err := readJSONObject(settingsPath)
	if err != nil {
		return err
	}
	hooks, _ := sc["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	injectCmd := jsonEscape(binPath) + " inject"
	stopCmd := jsonEscape(binPath) + " hook-stop"
	hooks["SessionStart"] = appendHookOnce(hooks["SessionStart"], injectCmd)
	hooks["Stop"] = appendHookOnce(hooks["Stop"], stopCmd)
	sc["hooks"] = hooks
	return backupAndWriteJSON(settingsPath, sc)
}

func jsonEscape(s string) string { return strings.ReplaceAll(s, "\"", "\\\"") }

// appendHookOnce Claude Code hooks 形态：[{matcher?, hooks:[{type:command, command}]}]
func appendHookOnce(existing any, command string) []any {
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
				return list // 已接线，幂等
			}
		}
	}
	list = append(list, map[string]any{
		"hooks": []any{map[string]any{"type": "command", "command": command}},
	})
	return list
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

func InstallCodex(home, binPath string, takeover bool) error {
	path := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, _ := os.ReadFile(path)
	body := string(data)
	section := tomlSection(body, "mcp_servers.memory")
	if len(section) > 0 {
		if strings.Contains(string(section), "command = \""+binPath+"\"") {
			return nil // 已接线
		}
		if !takeover {
			return fmt.Errorf("已存在 [mcp_servers.memory]——确认接管请加 --takeover")
		}
		body = strings.Replace(body, string(section), "", 1)
	}
	body = strings.TrimRight(body, "\n") + "\n\n[mcp_servers.memory]\ncommand = \"" + binPath + "\"\nargs = [\"mcp\"]\n"
	return backupAndWrite(path, []byte(body))
}

// tomlSection 提取 [section] 段（含头，到下一个 [ 段或 EOF）
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
	if len(out) == 0 {
		return nil
	}
	return []byte(strings.Join(out, "\n") + "\n")
}

// ── cursor / gemini-cli（JSON mcpServers）────────────────────────────────────

func installJSONMCPServers(path string, binPath string, takeover bool) error {
	cfg, err := readJSONObject(path)
	if err != nil {
		return err
	}
	servers, _ := cfg["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	if ent, exists := servers["memory"].(map[string]any); exists {
		if cmd, _ := ent["command"].(string); cmd != binPath && !takeover {
			return fmt.Errorf("已存在同名 memory server（command=%q）——确认接管请加 --takeover", cmd)
		}
	}
	servers["memory"] = mcpEntry(binPath)
	cfg["mcpServers"] = servers
	return backupAndWriteJSON(path, cfg)
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

func InstallCursor(home, binPath string, takeover bool) error {
	path := filepath.Join(home, ".cursor", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return installJSONMCPServers(path, binPath, takeover)
}

func InstallGemini(home, binPath string, takeover bool) error {
	path := filepath.Join(home, ".gemini", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return installJSONMCPServers(path, binPath, takeover)
}

// Install 一键接线（全部已装 agent；写前备份）
func Install(home, binPath string, takeover bool) ([]AgentStatus, error) {
	var done []AgentStatus
	for _, s := range Detect(home, binPath) {
		if !s.Installed {
			continue
		}
		var err error
		switch s.Agent {
		case AgentClaudeCode:
			err = InstallClaudeCode(home, binPath, takeover)
		case AgentCodex:
			err = InstallCodex(home, binPath, takeover)
		case AgentCursor:
			err = InstallCursor(home, binPath, takeover)
		case AgentGeminiCLI:
			err = InstallGemini(home, binPath, takeover)
		}
		if err != nil {
			return done, fmt.Errorf("%s 接线失败: %w", s.Agent, err)
		}
		done = append(done, AgentStatus{Agent: s.Agent, Installed: true, Wired: true})
	}
	return done, nil
}

// ── 备份与写回 ────────────────────────────────────────────────────────────────

func backupAndWriteJSON(path string, cfg map[string]any) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return backupAndWrite(path, append(data, '\n'))
}

func backupAndWrite(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if old, err := os.ReadFile(path); err == nil {
		backup := path + ".remin-backup-" + strings.ReplaceAll(nowStamp(), ":", "")
		_ = os.WriteFile(backup, old, 0o644)
	}
	return os.WriteFile(path, data, 0o644)
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
