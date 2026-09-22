// 卸载（cordis 可逆性的回放面）：按接线台账逆向摘除我们写过的每一处；
// 台账丢失时退化为启发式扫描（按命令路径特征，不误伤他人配置）。
// 真源 ~/.remin 默认保留——记忆是用户资产，只有 --purge 显式 opt-in 才删。
package doctor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Skip 回放时被跳过的项（如实报告，绝不盲删）
type Skip struct {
	File   string `json:"file"`
	Key    string `json:"key,omitempty"`
	Reason string `json:"reason"`
}

// UninstallReport 卸载结果（--json 契约同全局）
type UninstallReport struct {
	Removed             []string `json:"removed,omitempty"`
	Skipped             []Skip   `json:"skipped,omitempty"`
	BackupsCleaned      int      `json:"backups_cleaned"`
	BinRemoved          string   `json:"bin_removed,omitempty"`
	StoreRoot           string   `json:"store_root,omitempty"`
	StoreRemoved        bool     `json:"store_removed"`
	MemoriesBeforePurge int      `json:"memories_before_purge,omitempty"`
	Heuristic           bool     `json:"heuristic"`
}

// Uninstall 一键卸载：台账回放 → 备份清理 → 落位删除 → 台账删除 →（--purge）真源删除
func Uninstall(home, root string, purge bool) (*UninstallReport, error) {
	rep := &UninstallReport{}
	ledger, err := LoadLedger(root)
	if err != nil {
		return nil, err // 台账损坏：如实报错，拒绝猜测
	}
	if len(ledger.Effects) == 0 {
		rep.Heuristic = true
		heuristicSweep(home, rep)
	} else {
		// 逆序回放（后写的先撤，与安装时序对称）
		for i := len(ledger.Effects) - 1; i >= 0; i-- {
			e := ledger.Effects[i]
			ok, reason := revertEffect(e)
			switch {
			case ok:
				rep.Removed = append(rep.Removed, describeEffect(e))
			case reason == "":
				// 静默跳过：文件本就不存在（用户已自行清理）
			default:
				rep.Skipped = append(rep.Skipped, Skip{File: e.File, Key: e.Key, Reason: reason})
			}
		}
	}

	// 备份清理：所有被写过文件旁边的 .remin-backup-*（含台账外的历史残留）
	seen := map[string]bool{}
	for _, e := range ledger.Effects {
		if e.File != "" && !seen[e.File] {
			seen[e.File] = true
			rep.BackupsCleaned += removeAll(e.File + ".remin-backup-*")
		}
	}
	if rep.Heuristic {
		for _, f := range heuristicFiles(home) {
			rep.BackupsCleaned += removeAll(f + ".remin-backup-*")
		}
	}

	// 落位二进制与台账
	if err := os.RemoveAll(filepath.Join(root, "bin")); err == nil {
		rep.BinRemoved = filepath.Join(root, "bin")
	}
	_ = os.Remove(LedgerPath(root))

	// 真源处置：默认保留；--purge 显式删除（删除前如实报告记忆规模）
	rep.StoreRoot = root
	if purge {
		rep.MemoriesBeforePurge = countMemories(root)
		if err := os.RemoveAll(root); err != nil {
			return rep, fmt.Errorf("删除真源失败: %w", err)
		}
		rep.StoreRemoved = true
	}
	return rep, nil
}

// revertEffect 回放一个 effect 的 inverse。ok=已摘除；reason 非空=被改动需报告；
// reason 空=目标已不存在，静默（用户已自行清理）。
func revertEffect(e Effect) (ok bool, reason string) {
	switch e.Kind {
	case "mcp-json":
		cfg, err := readJSONObject(e.File)
		if err != nil {
			return false, fmt.Sprintf("读取失败: %v", err)
		}
		// e.Key 是点分路径（mcpServers.memory）：逐层下钻取目标 map
		parts := strings.Split(e.Key, ".")
		cur := cfg
		for _, p := range parts[:len(parts)-1] {
			next, _ := cur[p].(map[string]any)
			if next == nil {
				return false, ""
			}
			cur = next
		}
		last := parts[len(parts)-1]
		ent, exists := cur[last]
		if !exists {
			return false, ""
		}
		m, _ := ent.(map[string]any)
		if cmd, _ := m["command"].(string); cmd != e.Command {
			return false, "已被用户修改（command 不再是我们写入的值），请手工处理"
		}
		delete(cur, last)
		// 我们创建的文件摘空后不留守卫文件（cordis：不留自己的痕迹）
		if e.Created && len(cfg) == 1 {
			if servers, ok := cfg["mcpServers"].(map[string]any); ok && len(servers) == 0 {
				if err := os.Remove(e.File); err != nil {
					return false, err.Error()
				}
				return true, ""
			}
		}
		if err := writePlainJSON(e.File, cfg); err != nil {
			return false, err.Error()
		}
		return true, ""

	case "hook-entry":
		cfg, err := readJSONObject(e.File)
		if err != nil {
			return false, fmt.Sprintf("读取失败: %v", err)
		}
		hooks, _ := cfg["hooks"].(map[string]any)
		list, _ := hooks[e.Key].([]any)
		var kept []any
		found := false
		for _, g := range list {
			gm, _ := g.(map[string]any)
			hs, _ := gm["hooks"].([]any)
			var keptHooks []any
			for _, h := range hs {
				hm, _ := h.(map[string]any)
				if c, _ := hm["command"].(string); c == e.Command {
					found = true
					continue
				}
				keptHooks = append(keptHooks, h)
			}
			if len(keptHooks) == 0 {
				continue // 组内摘空：整组移除（我们写入的组本就只有这一条命令）
			}
			gm["hooks"] = keptHooks
			kept = append(kept, gm)
		}
		if !found {
			return false, ""
		}
		if len(kept) == 0 {
			delete(hooks, e.Key)
		} else {
			hooks[e.Key] = kept
		}
		if len(hooks) == 0 {
			delete(cfg, "hooks")
		}
		if e.Created && len(cfg) == 0 {
			if err := os.Remove(e.File); err != nil {
				return false, err.Error()
			}
			return true, ""
		}
		if err := writePlainJSON(e.File, cfg); err != nil {
			return false, err.Error()
		}
		return true, ""

	case "toml-section":
		data, err := os.ReadFile(e.File)
		if os.IsNotExist(err) {
			return false, ""
		}
		if err != nil {
			return false, fmt.Sprintf("读取失败: %v", err)
		}
		body := string(data)
		section := tomlSection(body, e.Section)
		if len(section) == 0 {
			return false, ""
		}
		if !strings.Contains(string(section), "command = \""+e.Command+"\"") {
			return false, "已被用户修改（段内 command 不再是我们写入的值），请手工处理"
		}
		body = strings.Replace(body, string(section), "", 1)
		body = collapseBlankLines(body)
		if e.Created && strings.TrimSpace(body) == "" {
			if err := os.Remove(e.File); err != nil {
				return false, err.Error()
			}
			return true, ""
		}
		if err := os.WriteFile(e.File, []byte(body), 0o644); err != nil {
			return false, err.Error()
		}
		return true, ""

	case "gitignore":
		data, err := os.ReadFile(e.File)
		if os.IsNotExist(err) {
			return false, ""
		}
		if err != nil {
			return false, fmt.Sprintf("读取失败: %v", err)
		}
		lines := strings.Split(string(data), "\n")
		drop := map[string]bool{}
		for _, l := range e.Lines {
			drop[l] = true
		}
		var out []string
		for _, l := range lines {
			if !drop[strings.TrimSpace(l)] {
				out = append(out, l)
			}
		}
		if err := os.WriteFile(e.File, []byte(strings.Join(out, "\n")), 0o644); err != nil {
			return false, err.Error()
		}
		return true, ""
	case "sched-file":
		// 调度器侧卸载 best-effort（文件删除是主清理面）；不再含我们写的命令 = 用户改过，不盲删
		_ = schedTeardown(e.File)
		data, err := os.ReadFile(e.File)
		if os.IsNotExist(err) {
			return false, ""
		}
		if err != nil {
			return false, fmt.Sprintf("读取失败: %v", err)
		}
		if e.Command != "" && !strings.Contains(string(data), e.Command) {
			return false, "已被用户修改（不再含我们写入的命令），请手工处理"
		}
		if err := os.Remove(e.File); err != nil {
			return false, err.Error()
		}
		return true, ""
	}
	return false, "未知 effect 类型: " + e.Kind
}

func writePlainJSON(path string, cfg map[string]any) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func collapseBlankLines(body string) string {
	for strings.Contains(body, "\n\n\n") {
		body = strings.ReplaceAll(body, "\n\n\n", "\n\n")
	}
	return body
}

func describeEffect(e Effect) string {
	switch e.Kind {
	case "hook-entry":
		return fmt.Sprintf("%s hooks.%s 摘除", e.File, e.Key)
	case "toml-section":
		return fmt.Sprintf("%s [%s] 摘除", e.File, e.Section)
	case "gitignore":
		return fmt.Sprintf("%s 忽略行摘除（%s）", e.File, strings.Join(e.Lines, ","))
	default:
		return fmt.Sprintf("%s %s 摘除", e.File, e.Key)
	}
}

func removeAll(glob string) int {
	matches, _ := filepath.Glob(glob)
	n := 0
	for _, m := range matches {
		if err := os.Remove(m); err == nil {
			n++
		}
	}
	return n
}

// countMemories purge 前如实报告记忆规模（memory/ 下 markdown 条数）
func countMemories(root string) int {
	n := 0
	_ = filepath.Walk(filepath.Join(root, "memory"), func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.HasSuffix(path, ".md") {
			n++
		}
		return nil
	})
	return n
}

// ── 启发式兜底（台账丢失：v0.2.0 时代接线 / 用户删过 root）──────────────────
// 匹配规则保守：命令路径 basename 是 remin/remin.exe，且路径含 .remin 或以
// /bin/remin 结尾（覆盖落位路径与旧版 repo 内 bin 路径）。他人的 memory server 不动。

var reminHookRe = regexp.MustCompile(`"([^"]*[/\\]?remin(?:\.exe)?)" (?:inject|hook-stop)`)

func heuristicFiles(home string) []string {
	return []string{
		filepath.Join(home, ".claude.json"),
		filepath.Join(home, ".claude", "settings.json"),
		filepath.Join(home, ".codex", "config.toml"),
		filepath.Join(home, ".cursor", "mcp.json"),
		filepath.Join(home, ".gemini", "settings.json"),
	}
}

func isReminCommand(cmd string) bool {
	if cmd == "" {
		return false
	}
	base := filepath.Base(strings.Trim(cmd, `"`))
	if base != "remin" && base != "remin.exe" {
		return false
	}
	slash := strings.ReplaceAll(cmd, `\`, "/")
	return strings.Contains(slash, ".remin") || strings.HasSuffix(slash, "/bin/remin")
}

func heuristicSweep(home string, rep *UninstallReport) {
	// JSON mcpServers 类
	for _, f := range []string{
		filepath.Join(home, ".claude.json"),
		filepath.Join(home, ".cursor", "mcp.json"),
		filepath.Join(home, ".gemini", "settings.json"),
	} {
		cfg, err := readJSONObject(f)
		if err != nil {
			continue
		}
		servers, _ := cfg["mcpServers"].(map[string]any)
		if servers == nil {
			continue
		}
		if ent, ok := servers["memory"].(map[string]any); ok {
			if cmd, _ := ent["command"].(string); isReminCommand(cmd) {
				delete(servers, "memory")
				if writePlainJSON(f, cfg) == nil {
					rep.Removed = append(rep.Removed, f+" mcpServers.memory 摘除（启发式）")
				}
			} else if cmd != "" {
				rep.Skipped = append(rep.Skipped, Skip{File: f, Key: "mcpServers.memory",
					Reason: "command 非典型 remin 路径，请手工确认"})
			}
		}
	}
	// settings.json hooks
	sp := filepath.Join(home, ".claude", "settings.json")
	if cfg, err := readJSONObject(sp); err == nil {
		if hooks, ok := cfg["hooks"].(map[string]any); ok {
			changed := false
			for name, v := range hooks {
				list, _ := v.([]any)
				var kept []any
				for _, g := range list {
					gm, _ := g.(map[string]any)
					hs, _ := gm["hooks"].([]any)
					var keptHooks []any
					for _, h := range hs {
						hm, _ := h.(map[string]any)
						if c, _ := hm["command"].(string); reminHookRe.MatchString(c) {
							changed = true
							continue
						}
						keptHooks = append(keptHooks, h)
					}
					if len(keptHooks) > 0 {
						gm["hooks"] = keptHooks
						kept = append(kept, gm)
					}
				}
				if len(kept) == 0 {
					delete(hooks, name)
				} else {
					hooks[name] = kept
				}
			}
			if changed {
				if len(hooks) == 0 {
					delete(cfg, "hooks")
				}
				if writePlainJSON(sp, cfg) == nil {
					rep.Removed = append(rep.Removed, sp+" hooks 摘除（启发式）")
				}
			}
		}
	}
	// codex TOML
	tp := filepath.Join(home, ".codex", "config.toml")
	if data, err := os.ReadFile(tp); err == nil {
		body := string(data)
		section := tomlSection(body, "mcp_servers.memory")
		if len(section) > 0 {
			if isReminCommand(tomlCommand(string(section))) {
				body = collapseBlankLines(strings.Replace(body, string(section), "", 1))
				if os.WriteFile(tp, []byte(body), 0o644) == nil {
					rep.Removed = append(rep.Removed, tp+" [mcp_servers.memory] 摘除（启发式）")
				}
			} else {
				rep.Skipped = append(rep.Skipped, Skip{File: tp, Key: "mcp_servers.memory",
					Reason: "段内 command 非典型 remin 路径，请手工确认"})
			}
		}
	}
}

// tomlCommand 提取段内 command = "..." 的值
func tomlCommand(section string) string {
	for _, l := range strings.Split(section, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(l), "command = \""); ok {
			return strings.TrimSuffix(v, "\"")
		}
	}
	return ""
}
