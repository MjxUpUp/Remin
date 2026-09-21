// Package miner transcript 旁路挖矿（FR-CAP-1：事后异步读会话日志，agent 带内延迟为零）。
// claude-jsonl 适配器：~/.claude/projects/<proj>/<session>.jsonl；格式漂移容错（缺字段跳过）。
package miner

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/remin-dev/remin/internal/core/extractor"
)

// ParseClaudeJSONL 解析单个 transcript（从 fromLine 起，1-based；返回新事件与总行数）
func ParseClaudeJSONL(path string, fromLine int) ([]extractor.Event, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	var events []extractor.Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024) // transcript 行可能很长
	line := 0
	projectName := projectNameFromPath(path)
	for sc.Scan() {
		line++
		if line < fromLine {
			continue
		}
		raw := sc.Bytes()
		if len(strings.TrimSpace(string(raw))) == 0 {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal(raw, &obj); err != nil {
			continue // 格式漂移容错：坏行跳过不致命
		}
		ev := extractor.Event{Line: line, ProjectName: projectName}
		if t, ok := obj["type"].(string); ok {
			if t != "user" && t != "assistant" {
				continue
			}
			ev.Role = t
		} else {
			continue
		}
		if sid, ok := obj["sessionId"].(string); ok {
			ev.SessionID = sid
		}
		if cwd, ok := obj["cwd"].(string); ok {
			ev.CWD = cwd
		}
		if ts, ok := obj["timestamp"].(string); ok {
			ev.Timestamp = ts
		}
		msg, ok := obj["message"].(map[string]any)
		if !ok {
			continue
		}
		content, _ := msg["content"]
		switch c := content.(type) {
		case string:
			ev.Text = c
		case []any:
			var sb strings.Builder
			for _, part := range c {
				m, ok := part.(map[string]any)
				if !ok {
					continue
				}
				pt, _ := m["type"].(string)
				switch pt {
				case "text":
					if s, ok := m["text"].(string); ok {
						if sb.Len() > 0 {
							sb.WriteByte('\n')
						}
						sb.WriteString(s)
					}
				case "tool_use":
					ev.IsToolUse = true
				}
			}
			ev.Text = sb.String()
		}
		if strings.TrimSpace(ev.Text) != "" {
			events = append(events, ev)
		}
	}
	return events, line, sc.Err()
}

// projectNameFromPath 从 transcript 路径还原项目名（目录名去 munging）
func projectNameFromPath(path string) string {
	dir := filepath.Base(filepath.Dir(path))
	// Claude Code 把 cwd 的 / 与 . munging 成 -；取末段作为项目标签（尽力而为）
	parts := strings.Split(dir, "-")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return dir
}

// Discover 扫描 dir 下全部 transcript（*.jsonl，确定性排序）
func Discover(dir string) ([]string, error) {
	var out []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // 目录缺失/权限：跳过
		}
		if !info.IsDir() && strings.HasSuffix(path, ".jsonl") {
			out = append(out, path)
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// Cursor 增量游标（断点续挖）
type Cursor struct {
	Size  int64 `json:"size"`
	Lines int   `json:"lines"`
}

// Cursors 游标表 transcripts-cache/cursors.json
type Cursors struct {
	Path string
	m    map[string]Cursor
}

func LoadCursors(root string) (*Cursors, error) {
	c := &Cursors{Path: filepath.Join(root, "transcripts-cache", "cursors.json"), m: map[string]Cursor{}}
	data, err := os.ReadFile(c.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return nil, err
	}
	_ = json.Unmarshal(data, &c.m)
	return c, nil
}

func (c *Cursors) Get(path string) (Cursor, bool) {
	cur, ok := c.m[path]
	return cur, ok
}

// NeedMine 文件是否需要挖：新文件、追加过、或收缩（轮转/重写 → 全量重挖）
func (c *Cursors) NeedMine(path string, size int64) bool {
	cur, ok := c.m[path]
	if !ok {
		return true
	}
	return size != cur.Size
}

func (c *Cursors) Set(path string, cur Cursor) { c.m[path] = cur }

func (c *Cursors) Save() error {
	if err := os.MkdirAll(filepath.Dir(c.Path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c.m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(c.Path, data, 0o644)
}

// Queue Stop hook 入队的 transcript 队列（按路径幂等去重）
type Queue struct {
	Path string
}

type QueueItem struct {
	Path     string `json:"path"`
	QueuedAt string `json:"queued_at"`
}

func LoadQueue(root string) *Queue {
	return &Queue{Path: filepath.Join(root, "transcripts-cache", "queue.jsonl")}
}

func (q *Queue) All() []QueueItem {
	data, err := os.ReadFile(q.Path)
	if err != nil {
		return nil
	}
	var items []QueueItem
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var it QueueItem
		if json.Unmarshal([]byte(line), &it) == nil {
			items = append(items, it)
		}
	}
	return items
}

// Append 幂等追加：已在队列中的路径不重复入队
func (q *Queue) Append(path string) error {
	for _, it := range q.All() {
		if it.Path == path {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(q.Path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(q.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	data, _ := json.Marshal(QueueItem{Path: path, QueuedAt: nowISO()})
	_, err = f.Write(append(data, '\n'))
	return err
}

// RemovePaths 处理完成后从队列移除
func (q *Queue) RemovePaths(paths map[string]bool) error {
	items := q.All()
	var sb strings.Builder
	for _, it := range items {
		if paths[it.Path] {
			continue
		}
		data, _ := json.Marshal(it)
		sb.Write(data)
		sb.WriteByte('\n')
	}
	if sb.Len() == 0 {
		return os.Remove(q.Path)
	}
	return os.WriteFile(q.Path, []byte(sb.String()), 0o644)
}

func nowISO() string {
	return time.Now().Format("2006-01-02T15:04:05-07:00")
}
