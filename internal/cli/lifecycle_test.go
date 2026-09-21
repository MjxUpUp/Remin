package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// CLI 面回归：uninstall 全链（接线 → 摘除 → 配置还原）与 upgrade --check 的 JSON 契约。
// 重的逻辑在 doctor/upgrade 包单测；这里钉 CLI 接线与输出面。

func TestCLIUninstallRoundtrip(t *testing.T) {
	// cobra 持久 flag 是包级全局：隔离其他测试残留的 --root/--json
	resetLifecycleFlags(t)

	home := t.TempDir()
	root := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("REMIN_HOME", root)
	os.Setenv("GIT_AUTHOR_NAME", "t") // store init 需要可归因身份
	os.Setenv("GIT_COMMITTER_NAME", "t")
	os.Setenv("GIT_AUTHOR_EMAIL", "t@x")
	os.Setenv("GIT_COMMITTER_EMAIL", "t@x")
	t.Cleanup(func() {
		os.Unsetenv("GIT_AUTHOR_NAME")
		os.Unsetenv("GIT_COMMITTER_NAME")
		os.Unsetenv("GIT_AUTHOR_EMAIL")
		os.Unsetenv("GIT_COMMITTER_EMAIL")
	})

	// fixture：四个 agent 目录
	os.MkdirAll(filepath.Join(home, ".claude"), 0o755)
	os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"mcpServers": {"other": {"command": "x"}}}`), 0o644)
	os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(`{"theme": "dark"}`), 0o644)
	os.MkdirAll(filepath.Join(home, ".codex"), 0o755)
	os.WriteFile(filepath.Join(home, ".codex", "config.toml"), []byte("model = \"gpt-5\"\n"), 0o644)
	os.MkdirAll(filepath.Join(home, ".cursor"), 0o755)
	os.WriteFile(filepath.Join(home, ".cursor", "mcp.json"), []byte(`{"mcpServers": {}}`), 0o644)
	os.MkdirAll(filepath.Join(home, ".gemini"), 0o755)

	// doctor --install
	doctorFlags.install = true
	doctorFlags.takeover = false
	rootCmd.SetArgs([]string{"doctor", "--install", "--json"})
	var execErr error
	out := captureStdout(t, func() { execErr = rootCmd.Execute() })
	if execErr != nil {
		t.Fatalf("doctor --install: %v\n%s", execErr, out)
	}
	var rep struct {
		Data struct {
			Staged string `json:"staged"`
			Wired  []struct {
				Agent string `json:"agent"`
			} `json:"wired"`
		} `json:"data"`
	}
	json.Unmarshal([]byte(out), &rep)
	if rep.Data.Staged == "" || filepath.Join(root, "bin") != filepath.Dir(rep.Data.Staged) {
		t.Fatalf("应落位到 root/bin: %q", rep.Data.Staged)
	}
	if len(rep.Data.Wired) != 4 {
		t.Fatalf("应接线 4 agent: %+v", rep.Data.Wired)
	}
	if _, err := os.Stat(filepath.Join(root, "wiring.json")); err != nil {
		t.Fatal("台账应存在")
	}

	// uninstall
	uninstallFlags.purge = false
	rootCmd.SetArgs([]string{"uninstall", "--json"})
	out = captureStdout(t, func() { execErr = rootCmd.Execute() })
	if execErr != nil {
		t.Fatalf("uninstall: %v\n%s", execErr, out)
	}
	var urep struct {
		Data struct {
			Removed      []string `json:"removed"`
			StoreRemoved bool     `json:"store_removed"`
			BinRemoved   string   `json:"bin_removed"`
		} `json:"data"`
	}
	json.Unmarshal([]byte(out), &urep)
	if len(urep.Data.Removed) < 6 { // 4×MCP + 2 hooks（gitignore 未发生：root 无 .gitignore 非 git 仓库）
		t.Fatalf("摘除项不足: %+v", urep.Data.Removed)
	}
	if urep.Data.StoreRemoved {
		t.Fatal("默认不得删真源")
	}
	// 配置还原核验
	data, _ := os.ReadFile(filepath.Join(home, ".claude.json"))
	if strings.Contains(string(data), "memory") {
		t.Errorf(".claude.json 未摘净: %s", data)
	}
	if _, err := os.Stat(filepath.Join(root, "bin")); !os.IsNotExist(err) {
		t.Error("落位 bin 应删除")
	}
	if _, err := os.Stat(filepath.Join(root, "wiring.json")); !os.IsNotExist(err) {
		t.Error("台账应删除")
	}
	if _, err := os.Stat(root); err != nil {
		t.Error("真源默认保留")
	}
}

func TestCLIUpgradeCheck(t *testing.T) {
	resetLifecycleFlags(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"version": "99.0.0"}`))
	}))
	defer srv.Close()
	t.Setenv("REMIN_NPM_REGISTRY", srv.URL)
	t.Setenv("REMIN_HOME", t.TempDir())

	upgradeFlags.check = true
	rootCmd.SetArgs([]string{"upgrade", "--check", "--json"})
	out := captureStdout(t, func() { _ = rootCmd.Execute() })
	var rep struct {
		Data struct {
			Current string `json:"current"`
			Latest  string `json:"latest"`
			Newer   bool   `json:"newer"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("JSON: %v\n%s", err, out)
	}
	if rep.Data.Latest != "99.0.0" || !rep.Data.Newer || rep.Data.Current != Version {
		t.Fatalf("check 契约不符: %+v", rep.Data)
	}
}

// resetLifecycleFlags 测试进出均复位持久 flag（不泄漏给包内其他测试）
func resetLifecycleFlags(t *testing.T) {
	t.Helper()
	rootPath, jsonOut = "", false
	t.Cleanup(func() { rootPath, jsonOut = "", false })
}
