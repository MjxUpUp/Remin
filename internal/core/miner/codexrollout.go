// codex rollout 适配器：~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl。
// 形态（真实样本逆向）：session_meta(session_id/cwd) 头行 + response_item{message} 文本
// + function_call 类工具调用；格式漂移容错（缺字段跳过不致命）。
package miner

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/remin-dev/remin/internal/core/extractor"
)

// ParseCodexRollout 解析单个 codex rollout（从 fromLine 起，1-based；返回新事件与总行数）
func ParseCodexRollout(path string, fromLine int) ([]extractor.Event, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	var events []extractor.Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	line := 0
	var sessionID, cwd string
	for sc.Scan() {
		line++
		raw := sc.Bytes()
		if len(strings.TrimSpace(string(raw))) == 0 {
			continue
		}
		var obj struct {
			Timestamp string `json:"timestamp"`
			Type      string `json:"type"`
			Payload   struct {
				SessionID string `json:"session_id"`
				CWD       string `json:"cwd"`
				Type      string `json:"type"`
				Role      string `json:"role"`
				Content   []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(raw, &obj); err != nil {
			continue // 格式漂移容错：坏行跳过不致命
		}
		switch obj.Type {
		case "session_meta":
			// 会话头是粘性状态：全量扫描累积（增量段也继承 session/cwd，不受 fromLine 截断）
			if obj.Payload.SessionID != "" {
				sessionID = obj.Payload.SessionID
			}
			if obj.Payload.CWD != "" {
				cwd = obj.Payload.CWD
			}
			continue
		case "response_item":
			// 文本消息与工具调用；其余（reasoning 等内部形态）不产事件
		default:
			continue
		}
		if line < fromLine {
			continue // 增量跳过在头状态累积之后：段内事件完整归因
		}
		ev := extractor.Event{Line: line, Origin: "codex", SessionID: sessionID, CWD: cwd, Timestamp: obj.Timestamp, ProjectName: projectOf(cwd, path)}
		var sb strings.Builder
		switch obj.Payload.Type {
		case "message":
			ev.Role = obj.Payload.Role
			if ev.Role != "user" && ev.Role != "assistant" {
				continue // developer 等系统角色是注入噪声（与 DSH plugin 过滤同构），不产事件
			}
			for _, part := range obj.Payload.Content {
				switch part.Type {
				case "input_text", "output_text":
					if sb.Len() > 0 {
						sb.WriteByte('\n')
					}
					sb.WriteString(part.Text)
				}
			}
		case "function_call", "local_shell_call", "custom_tool_call", "tool_call":
			ev.IsToolUse = true
		default:
			continue
		}
		ev.Text = sb.String()
		if strings.TrimSpace(ev.Text) != "" || ev.IsToolUse {
			events = append(events, ev)
		}
	}
	return events, line, sc.Err()
}

// projectOf 项目标签：优先 cwd 末段（rollout 按日期分目录，路径段不是项目名）
func projectOf(cwd, path string) string {
	if cwd != "" {
		seg := filepath.Base(cwd)
		if seg != "" && seg != "/" && seg != "." && seg != string(filepath.Separator) {
			return seg
		}
	}
	return filepath.Base(filepath.Dir(path))
}
