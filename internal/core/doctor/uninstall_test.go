package doctor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stagedInstall 测试脚手架：fake 源二进制 → 落位 → 全 agent 接线（生产入口同序）
func stagedInstall(t *testing.T, home, root string) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "remin")
	if err := os.WriteFile(src, []byte("remin-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	stable, err := Stage(root, src)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Install(home, root, stable, false); err != nil {
		t.Fatal(err)
	}
	return stable
}

// 卸载往返：install → uninstall 后用户配置回到接线前形态，自有痕迹全清
func TestUninstallRoundtrip(t *testing.T) {
	home := fixtureHome(t)
	root := t.TempDir()
	stagedInstall(t, home, root)

	rep, err := Uninstall(home, root, false)
	if err != nil {
		t.Fatal(err)
	}

	// .claude.json：memory 键摘除，原有键保留
	data, _ := os.ReadFile(filepath.Join(home, ".claude.json"))
	var cfg map[string]any
	json.Unmarshal(data, &cfg)
	if cfg["numStartups"] != float64(42) {
		t.Error("卸载不得动用户原有键")
	}
	if _, has := cfg["mcpServers"].(map[string]any)["memory"]; has {
		t.Error("mcpServers.memory 应被摘除")
	}

	// settings.json：theme 保留，remin hooks 摘净（空数组/空 hooks 键不留壳）
	sdata, _ := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	var sc map[string]any
	json.Unmarshal(sdata, &sc)
	if sc["theme"] != "dark" {
		t.Error("settings 原有键不得动")
	}
	if hooks, ok := sc["hooks"].(map[string]any); ok {
		for k, v := range hooks {
			if l, ok := v.([]any); ok && len(l) > 0 {
				t.Errorf("hooks.%s 应摘净: %v", k, v)
			}
		}
	}

	// codex TOML：memory 段摘除，原有段保留
	toml, _ := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	ts := string(toml)
	if strings.Contains(ts, "[mcp_servers.memory]") {
		t.Error("[mcp_servers.memory] 应被摘除")
	}
	if !strings.Contains(ts, "model = \"gpt-5\"") || !strings.Contains(ts, "[profiles.x]") {
		t.Error("TOML 原有内容不得动")
	}

	// cursor：原有文件保留、memory 摘除
	d, _ := os.ReadFile(filepath.Join(home, ".cursor", "mcp.json"))
	var c map[string]any
	json.Unmarshal(d, &c)
	if _, has := c["mcpServers"].(map[string]any)["memory"]; has {
		t.Error("cursor 的 memory 应被摘除")
	}
	// gemini：settings.json 本是我们创建的空壳，卸载应整文件删除
	if _, err := os.Stat(filepath.Join(home, ".gemini", "settings.json")); !os.IsNotExist(err) {
		t.Error("我们创建的 gemini settings.json 空壳应删除")
	}

	// 自有痕迹：备份、落位二进制、台账、gitignore 行
	for _, pat := range []string{
		filepath.Join(home, ".claude.json.remin-backup-*"),
		filepath.Join(home, ".claude", "settings.json.remin-backup-*"),
		filepath.Join(home, ".codex", "config.toml.remin-backup-*"),
	} {
		if m, _ := filepath.Glob(pat); len(m) != 0 {
			t.Errorf("备份应清理: %v", m)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "bin")); !os.IsNotExist(err) {
		t.Error("落位 bin 目录应删除")
	}
	if _, err := os.Stat(filepath.Join(root, "wiring.json")); !os.IsNotExist(err) {
		t.Error("台账应删除")
	}

	// 真源默认保留（记忆是用户资产）；报告如实
	if _, err := os.Stat(root); err != nil {
		t.Fatal("默认卸载必须保留真源 root")
	}
	if rep.StoreRemoved {
		t.Error("非 --purge 不得删真源")
	}
}

// 台账丢失（用户删过 root）：启发式兜底——按命令路径特征摘除，不误伤他人 server
func TestUninstallHeuristic(t *testing.T) {
	home := fixtureHome(t)
	root := t.TempDir()
	stagedInstall(t, home, root)
	os.Remove(filepath.Join(root, "wiring.json")) // 台账丢失

	if _, err := Uninstall(home, root, false); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(home, ".claude.json"))
	var cfg map[string]any
	json.Unmarshal(data, &cfg)
	servers := cfg["mcpServers"].(map[string]any)
	if _, has := servers["memory"]; has {
		t.Error("启发式应摘除指向 remin 的 memory server")
	}
	if _, has := servers["other"]; !has {
		t.Error("他人 server 不得误伤")
	}
	sdata, _ := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	var sc map[string]any
	json.Unmarshal(sdata, &sc)
	if hooks, ok := sc["hooks"].(map[string]any); ok {
		for k, v := range hooks {
			if l, ok := v.([]any); ok && len(l) > 0 {
				t.Errorf("启发式应摘净 hooks.%s", k)
			}
		}
	}
}

// 用户手改过我们写的值：跳过并如实报告，绝不盲删
func TestUninstallMismatchSkips(t *testing.T) {
	home := fixtureHome(t)
	root := t.TempDir()
	stagedInstall(t, home, root)

	// 手改 memory server 的 command（模拟用户接管给了别的二进制）
	p := filepath.Join(home, ".claude.json")
	data, _ := os.ReadFile(p)
	var cfg map[string]any
	json.Unmarshal(data, &cfg)
	cfg["mcpServers"].(map[string]any)["memory"].(map[string]any)["command"] = "/custom/patched-remin"
	out, _ := json.MarshalIndent(cfg, "", "  ")
	os.WriteFile(p, append(out, '\n'), 0o644)

	rep, err := Uninstall(home, root, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Skipped) == 0 {
		t.Fatal("手改项应跳过并报告")
	}
	data2, _ := os.ReadFile(p)
	var cfg2 map[string]any
	json.Unmarshal(data2, &cfg2)
	if _, has := cfg2["mcpServers"].(map[string]any)["memory"]; !has {
		t.Error("被手改的键不得盲删")
	}
}

// --purge：真源整体删除（显式 opt-in 才动记忆资产）
func TestUninstallPurge(t *testing.T) {
	home := fixtureHome(t)
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "memory"), 0o755)
	os.WriteFile(filepath.Join(root, "memory", "m1.md"), []byte("---\nid: x\n---\nbody"), 0o644)
	stagedInstall(t, home, root)

	rep, err := Uninstall(home, root, true)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.StoreRemoved {
		t.Fatal("--purge 应删除真源")
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Error("purge 后 root 应不存在")
	}
	if rep.MemoriesBeforePurge != 1 {
		t.Errorf("purge 前应如实报告记忆条数: %d", rep.MemoriesBeforePurge)
	}
}

// install 创建的文件（写前不存在）：uninstall 后整文件删除，不留空壳
func TestUninstallRemovesCreatedFiles(t *testing.T) {
	home := t.TempDir()
	// 只有目录骨架，无任何配置文件——全部由 install 创建
	for _, d := range []string{".claude", ".codex", ".cursor", ".gemini"} {
		os.MkdirAll(filepath.Join(home, d), 0o755)
	}
	root := t.TempDir()
	stagedInstall(t, home, root)

	if _, err := Uninstall(home, root, false); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{
		filepath.Join(home, ".claude.json"),
		filepath.Join(home, ".claude", "settings.json"),
		filepath.Join(home, ".codex", "config.toml"),
		filepath.Join(home, ".gemini", "settings.json"),
	} {
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Errorf("我们创建的文件应整删: %s", f)
		}
	}
	// cursor/mcp.json：install 前 fixture 未建它（.cursor 目录存在但无文件）→ 同样应删
	if _, err := os.Stat(filepath.Join(home, ".cursor", "mcp.json")); !os.IsNotExist(err) {
		t.Error("我们创建的 cursor mcp.json 应整删")
	}
	// 目录骨架保留（那是用户/我们建的目录，删键级内容即可；目录非我们的内容载体）
	if _, err := os.Stat(filepath.Join(home, ".claude")); err != nil {
		t.Error("目录骨架应保留（只删我们创建的文件）")
	}
}
