package importer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// ── claude-auto-memory：~/.claude/projects/*/memory/*.md ──────────────────────
// MEMORY.md 按段落/小节拆分；主题文件单条。

type claudeAutoMemory struct{ root string }

func (a *claudeAutoMemory) Name() string { return SrcClaudeAutoMemory }

func (a *claudeAutoMemory) Discover() ([]RawItem, error) {
	var out []RawItem
	projs, err := filepath.Glob(filepath.Join(a.root, "*", "memory"))
	if err != nil {
		return nil, err
	}
	for _, memDir := range projs {
		files, _ := filepath.Glob(filepath.Join(memDir, "*.md"))
		sort.Strings(files)
		for _, f := range files {
			data, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			mtime := mtimeOf(f)
			base := filepath.Base(f)
			if strings.EqualFold(base, "MEMORY.md") {
				for i, para := range splitParagraphs(string(data)) {
					out = append(out, RawItem{
						OriginID: f + "#p" + fmt.Sprint(i),
						Text:     para,
						MTime:    mtime,
						Ref:      f + " 段落 " + fmt.Sprint(i+1),
					})
				}
				continue
			}
			out = append(out, RawItem{
				OriginID: f,
				Text:     stripFrontmatter(string(data)),
				MTime:    mtime,
				Ref:      f,
			})
		}
	}
	return out, nil
}

// splitParagraphs 按空行分段；## 小节优先成段
func splitParagraphs(s string) []string {
	var out []string
	for _, sec := range strings.Split(s, "\n## ") {
		sec = strings.TrimSpace(sec)
		if sec == "" {
			continue
		}
		sec = strings.TrimPrefix(sec, "## ")
		title := ""
		if i := strings.Index(sec, "\n"); i > 0 {
			title = strings.TrimSpace(sec[:i])
			sec = strings.TrimSpace(sec[i+1:])
		}
		for _, para := range strings.Split(sec, "\n\n") {
			para = strings.TrimSpace(para)
			if para == "" {
				continue
			}
			if title != "" && !strings.HasPrefix(para, title) {
				para = title + "：" + para
			}
			if len([]rune(para)) >= 4 {
				out = append(out, para)
			}
		}
	}
	return out
}

func stripFrontmatter(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if strings.HasPrefix(s, "---\n") {
		if i := strings.Index(s[4:], "\n---\n"); i >= 0 {
			return strings.TrimSpace(s[4+i+5:])
		}
	}
	return strings.TrimSpace(s)
}

func mtimeOf(path string) string {
	if info, err := os.Stat(path); err == nil {
		return info.ModTime().Format("2006-01-02T15:04:05-07:00")
	}
	return ""
}

// ── claude-mem：SQLite（shell out sqlite3，ADR-0007：零 CGo）─────────────────

type claudeMem struct{ root string }

func (a *claudeMem) Name() string { return SrcClaudeMem }

func (a *claudeMem) Discover() ([]RawItem, error) {
	dbs, _ := filepath.Glob(filepath.Join(a.root, "*.db"))
	if len(dbs) == 0 {
		dbs2, _ := filepath.Glob(filepath.Join(a.root, "**", "*.db"))
		dbs = dbs2
	}
	var out []RawItem
	for _, db := range dbs {
		items, err := discoverSQLite(db)
		if err != nil {
			continue // 单库失败降级（缺 sqlite3 等），不拖垮整体
		}
		out = append(out, items...)
	}
	return out, nil
}

// discoverSQLite 探测含 content 类列的表并逐条读出（schema 漂移容错）
func discoverSQLite(db string) ([]RawItem, error) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		return nil, fmt.Errorf("系统缺 sqlite3（brew install sqlite）： %w", err)
	}
	out, err := runSQLite(db, "SELECT name FROM sqlite_master WHERE type='table'")
	if err != nil {
		return nil, err
	}
	var items []RawItem
	for _, table := range strings.Fields(out) {
		table = strings.Trim(table, "\"'[]")
		if table == "" {
			continue
		}
		cols, err := runSQLite(db, "PRAGMA table_info("+quoteIdent(table)+")")
		if err != nil {
			continue
		}
		contentCol, idCol, timeCol := pickCols(cols)
		if contentCol == "" {
			continue
		}
		q := "SELECT " + quoteIdent(contentCol)
		if idCol != "" {
			q += ", " + quoteIdent(idCol)
		} else {
			q += ", ''"
		}
		if timeCol != "" {
			q += ", " + quoteIdent(timeCol)
		} else {
			q += ", ''"
		}
		q += " FROM " + quoteIdent(table)
		rows, err := runSQLite(db, q)
		if err != nil {
			continue
		}
		for _, row := range strings.Split(strings.TrimSpace(rows), "\n") {
			parts := strings.SplitN(row, "|", 3)
			if len(parts) < 1 || strings.TrimSpace(parts[0]) == "" {
				continue
			}
			it := RawItem{Ref: db + " " + table}
			it.Text = strings.TrimSpace(parts[0])
			if len(parts) >= 2 {
				it.OriginID = table + "/" + strings.TrimSpace(parts[1])
			}
			if it.OriginID == "" || it.OriginID == table+"/" {
				it.OriginID = ""
			}
			if len(parts) >= 3 {
				it.MTime = normTime(strings.TrimSpace(parts[2]))
			}
			if it.Text != "" {
				items = append(items, it)
			}
		}
	}
	return items, nil
}

func pickCols(pragmaOut string) (content, id, timeCol string) {
	for _, line := range strings.Split(pragmaOut, "\n") {
		f := strings.Split(line, "|")
		if len(f) < 3 {
			continue
		}
		name := strings.TrimSpace(f[1])
		typ := strings.ToLower(strings.TrimSpace(f[2]))
		switch {
		case strings.Contains(name, "content") || strings.Contains(name, "memory") || strings.Contains(name, "text"):
			if content == "" {
				content = name
			}
		case name == "id" || strings.HasSuffix(name, "_id"):
			if id == "" {
				id = name
			}
		case strings.Contains(typ, "time") || strings.Contains(name, "created") || strings.Contains(name, "updated"):
			if timeCol == "" {
				timeCol = name
			}
		}
	}
	return
}

func runSQLite(db, query string) (string, error) {
	cmd := exec.Command("sqlite3", "-readonly", db, query)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("sqlite3: %s", errb.String())
	}
	return out.String(), nil
}

func quoteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

func normTime(s string) string {
	// Unix 秒/毫秒 → ISO；ISO 原样；其他丢弃（绝不伪造）
	if s == "" {
		return ""
	}
	var n int64
	if _, err := fmt.Sscanf(s, "%d", &n); err == nil && n > 1000000000 {
		if n > 1e14 { // 毫秒
			n = n / 1000
		}
		return timeUnixISO(n)
	}
	return ""
}

// ── chatgpt-export：导出 JSON（string 数组 / 对象数组两形态）──────────────────

type chatgptExport struct{ path string }

func (a *chatgptExport) Name() string { return SrcChatGPTExport }

func (a *chatgptExport) Discover() ([]RawItem, error) {
	data, err := os.ReadFile(a.path)
	if err != nil {
		return nil, err
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(data, &arr); err != nil {
		return nil, fmt.Errorf("ChatGPT 导出应为 JSON 数组: %w", err)
	}
	var out []RawItem
	for i, raw := range arr {
		var s string
		if json.Unmarshal(raw, &s) == nil && strings.TrimSpace(s) != "" {
			out = append(out, RawItem{OriginID: fmt.Sprint(i), Text: s, Ref: a.path + " #" + fmt.Sprint(i)})
			continue
		}
		var obj map[string]any
		if json.Unmarshal(raw, &obj) != nil {
			continue
		}
		text := firstStr(obj, "content", "text", "memory", "value")
		if strings.TrimSpace(text) == "" {
			continue
		}
		it := RawItem{OriginID: firstStr(obj, "id"), Text: text,
			Ref: a.path + " #" + fmt.Sprint(i), MTime: normTime(firstStr(obj, "created_at", "updated_at", "create_time"))}
		if it.OriginID == "" {
			it.OriginID = fmt.Sprint(i)
		}
		out = append(out, it)
	}
	return out, nil
}

func firstStr(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch t := v.(type) {
			case string:
				return t
			case float64:
				return fmt.Sprintf("%.0f", t)
			}
		}
	}
	return ""
}

// ── codex-memories：~/.codex/memories/*.md ────────────────────────────────────

type codexMemories struct{ root string }

func (a *codexMemories) Name() string { return SrcCodexMemories }

func (a *codexMemories) Discover() ([]RawItem, error) {
	files, _ := filepath.Glob(filepath.Join(a.root, "*.md"))
	sort.Strings(files)
	var out []RawItem
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if text := stripFrontmatter(string(data)); text != "" {
			out = append(out, RawItem{OriginID: f, Text: text, MTime: mtimeOf(f), Ref: f})
		}
	}
	return out, nil
}

// ── markdown-dir：用户手写笔记目录（human-verified：人写的字）────────────────

type markdownDir struct{ root string }

func (a *markdownDir) Name() string { return SrcMarkdownDir }

func (a *markdownDir) Discover() ([]RawItem, error) {
	var out []RawItem
	err := filepath.Walk(a.root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		rel, _ := filepath.Rel(a.root, path)
		if text := stripFrontmatter(string(data)); strings.TrimSpace(text) != "" {
			out = append(out, RawItem{OriginID: rel, Text: text, MTime: mtimeOf(path),
				Ref: path + "（用户亲笔）"})
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].OriginID < out[j].OriginID })
	return out, err
}

// timeUnixISO Unix 秒 → 本地时区 ISO 8601
func timeUnixISO(sec int64) string {
	return unixTime(sec).Format("2006-01-02T15:04:05-07:00")
}
