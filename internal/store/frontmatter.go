package store

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Render 渲染为 markdown 文件内容：frontmatter + 正文（spec v0 §3）
func (m *Memory) Render() (string, error) {
	fm, err := yaml.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("渲染 frontmatter 失败: %w", err)
	}
	var b strings.Builder
	b.WriteString("---\n")
	b.Write(fm)
	b.WriteString("---\n")
	body := strings.ReplaceAll(m.Body, "\r\n", "\n")
	b.WriteString(strings.TrimSpace(body))
	b.WriteString("\n")
	return b.String(), nil
}

// ParseMemory 解析 markdown 记忆文件（frontmatter + 正文）
func ParseMemory(content string) (*Memory, error) {
	fm, body, err := SplitFrontmatter(content)
	if err != nil {
		return nil, err
	}
	var m Memory
	dec := yaml.NewDecoder(bytes.NewReader([]byte(fm)))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("解析 frontmatter 失败: %w", err)
	}
	m.Body = body
	return &m, nil
}

// SplitFrontmatter 拆出 frontmatter YAML 块与正文（供 inbox 候选等扩展结构复用）
func SplitFrontmatter(content string) (fm string, body string, err error) {
	rest := strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasPrefix(rest, "---\n") && rest != "---" {
		return "", "", fmt.Errorf("缺少 frontmatter 起始分隔符")
	}
	lines := strings.SplitN(rest, "\n", -1)
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], " \t") == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return "", "", fmt.Errorf("缺少 frontmatter 结束分隔符")
	}
	return strings.Join(lines[1:end], "\n"), strings.TrimSpace(strings.Join(lines[end+1:], "\n")), nil
}

// NowTime 统一时间格式：ISO 8601 含时区偏移
func NowTime() string {
	return time.Now().Format("2006-01-02T15:04:05-07:00")
}

func parseTimeOK(s, layout string) bool {
	_, err := time.Parse(layout, s)
	return err == nil
}

// ParseTime 解析记忆时间字段；unknown 与空返回零值与 false（调用方如实处理）
func ParseTime(s string) (time.Time, bool) {
	if s == "" || s == TimeUnknown {
		return time.Time{}, false
	}
	for _, layout := range []string{"2006-01-02T15:04:05Z07:00", "2006-01-02T15:04:05.999999999Z07:00"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// ExpiryDuration 解析 expires 字段（"<N>d"），如 "7d"
func ExpiryDuration(s string) (time.Duration, bool) {
	if s == "" || !strings.HasSuffix(s, "d") {
		return 0, false
	}
	var n int
	if _, err := fmt.Sscanf(strings.TrimSuffix(s, "d"), "%d", &n); err != nil || n <= 0 {
		return 0, false
	}
	return time.Duration(n) * 24 * time.Hour, true
}

// ExpiredAt 计算 ephemeral 过期时刻（从 reviewed_at 起算；不可还原则不判定）
func (m *Memory) ExpiredAt(now time.Time) bool {
	d, ok := ExpiryDuration(m.Expires)
	if !ok {
		return false
	}
	base, ok := ParseTime(m.ReviewedAt)
	if !ok {
		return false
	}
	return now.After(base.Add(d))
}
