package doctor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fakeBin = "/usr/local/bin/remin"

func fixtureHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	// claude-code
	os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"numStartups": 42, "mcpServers": {"other": {"command": "x"}}}`), 0o644)
	os.MkdirAll(filepath.Join(home, ".claude"), 0o755)
	os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(`{"theme": "dark"}`), 0o644)
	// codex
	os.MkdirAll(filepath.Join(home, ".codex"), 0o755)
	os.WriteFile(filepath.Join(home, ".codex", "config.toml"), []byte("model = \"gpt-5\"\n\n[profiles.x]\nfoo = 1\n"), 0o644)
	// cursor
	os.MkdirAll(filepath.Join(home, ".cursor"), 0o755)
	os.WriteFile(filepath.Join(home, ".cursor", "mcp.json"), []byte(`{"mcpServers": {}}`), 0o644)
	// gemini
	os.MkdirAll(filepath.Join(home, ".gemini"), 0o755)
	return home
}

// M6 完成判据：doctor 在 fixture HOME 实测接线（备份 + 键级合并只增不删）
func TestInstallAllAgents(t *testing.T) {
	home := fixtureHome(t)
	wired, err := Install(home, t.TempDir(), fakeBin, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(wired) != 4 {
		t.Fatalf("应接线 4 个 agent: %d", len(wired))
	}

	// claude.json：保留原有键，memory 已注册
	data, _ := os.ReadFile(filepath.Join(home, ".claude.json"))
	var cfg map[string]any
	json.Unmarshal(data, &cfg)
	if cfg["numStartups"] != float64(42) {
		t.Error("原有键不得删除（只增不删）")
	}
	servers := cfg["mcpServers"].(map[string]any)
	mem := servers["memory"].(map[string]any)
	if mem["command"] != fakeBin {
		t.Error("memory server 命令不对")
	}
	if _, has := servers["other"]; !has {
		t.Error("其他 server 配置应保留")
	}

	// settings.json：theme 保留 + 双 hook
	sdata, _ := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	var sc map[string]any
	json.Unmarshal(sdata, &sc)
	if sc["theme"] != "dark" {
		t.Error("settings 原有键不得删除")
	}
	hooks := sc["hooks"].(map[string]any)
	for _, h := range []string{"SessionStart", "Stop"} {
		list, ok := hooks[h].([]any)
		if !ok || len(list) != 1 {
			t.Errorf("应注册 %s hook", h)
		}
	}
	stopList := hooks["Stop"].([]any)
	stopCmd := stopList[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)["command"].(string)
	if !strings.Contains(stopCmd, "hook-stop") {
		t.Errorf("Stop hook 命令不对: %s", stopCmd)
	}

	// codex TOML：原有内容保留 + memory 段
	toml, _ := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	ts := string(toml)
	if !strings.Contains(ts, "model = \"gpt-5\"") || !strings.Contains(ts, "[mcp_servers.memory]") {
		t.Errorf("TOML 合并不对: %s", ts)
	}
	if !strings.Contains(ts, "[profiles.x]") {
		t.Error("TOML 原有段不得删除")
	}

	// cursor / gemini JSON
	for _, p := range []string{filepath.Join(home, ".cursor", "mcp.json"), filepath.Join(home, ".gemini", "settings.json")} {
		d, _ := os.ReadFile(p)
		var c map[string]any
		json.Unmarshal(d, &c)
		if _, ok := c["mcpServers"].(map[string]any)["memory"]; !ok {
			t.Errorf("%s 应注册 memory", p)
		}
	}

	// 备份存在
	matches, _ := filepath.Glob(filepath.Join(home, ".claude.json.remin-backup-*"))
	if len(matches) != 1 {
		t.Errorf("写前应备份: %v", matches)
	}

	// 幂等：再次 install 不重复添加 hook
	if _, err := Install(home, t.TempDir(), fakeBin, false); err != nil {
		t.Fatal(err)
	}
	sdata2, _ := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	var sc2 map[string]any
	json.Unmarshal(sdata2, &sc2)
	hooks2 := sc2["hooks"].(map[string]any)
	if l := len(hooks2["SessionStart"].([]any)); l != 1 {
		t.Errorf("重复接线应幂等: %d", l)
	}

	// 检测状态：全部 wired
	for _, s := range Detect(home, fakeBin) {
		if s.Installed && !s.Wired {
			t.Errorf("%s 应已接线", s.Agent)
		}
	}
}

// 同名 server 冲突：无 --takeover 拒绝，有则替换
func TestTakeover(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".cursor"), 0o755)
	os.WriteFile(filepath.Join(home, ".cursor", "mcp.json"),
		[]byte(`{"mcpServers": {"memory": {"command": "/official/memory-server"}}}`), 0o644)
	if _, err := InstallCursor(home, fakeBin, false); err == nil {
		t.Fatal("存在同名 server 且未 --takeover 应拒绝")
	}
	if _, err := InstallCursor(home, fakeBin, true); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(home, ".cursor", "mcp.json"))
	var c map[string]any
	json.Unmarshal(data, &c)
	cmd := c["mcpServers"].(map[string]any)["memory"].(map[string]any)["command"].(string)
	if cmd != fakeBin {
		t.Errorf("takeover 后应替换: %s", cmd)
	}
	// 检测：conflicted → wired
	for _, s := range Detect(home, fakeBin) {
		if s.Agent == AgentCursor && (!s.Installed || !s.Wired) {
			t.Errorf("cursor 状态不对: %+v", s)
		}
	}
}
