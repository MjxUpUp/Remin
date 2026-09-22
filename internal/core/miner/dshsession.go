// dsh session 适配器：~/.dsh/sessions/<proj>/session-*/session.jsonl.zstd。
// 形态（真实样本逆向）：session(cwd/createdAt) 头行 + user/message{content,source.kind} +
// assistant/message{message.content[text|reasoning|tool-call]}；plugin 注入（source.kind!=user）
// 是系统噪声必须过滤；时间戳 epoch 毫秒。压缩走 zstd 二进制 shell-out（Go 标准库无 zstd，
// 与 git shell-out 同先例；可注入）。
package miner

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/remin-dev/remin/internal/core/extractor"
)

// dshOpen 打开（并解压）DSH transcript——测试可注入假解压器。
// zstd 的运行失败发生在 Wait 而非 Start（Start 只解析二进制路径），文件缺失先行
// 拦截；解压失败在读侧表现为空流 + Close 返回非零退出（调用方按 0 事件 + 出队处理）。
var dshOpen = func(path string) (io.ReadCloser, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	c := exec.Command("zstd", "-dc", path)
	stdout, err := c.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := c.Start(); err != nil {
		return nil, err
	}
	return &cmdReader{ReadCloser: stdout, cmd: c}, nil
}

// cmdReader 读尽后回收子进程（Close 即 Wait，防僵尸）
type cmdReader struct {
	io.ReadCloser
	cmd *exec.Cmd
}

func (r *cmdReader) Close() error {
	err := r.ReadCloser.Close()
	if werr := r.cmd.Wait(); err == nil {
		err = werr
	}
	return err
}

// ParseDSHSession 解析单个 DSH session（从 fromLine 起，1-based；返回新事件与总行数）。
// 行号为解压后 JSONL 行号（cursor.Size 存压缩尺寸做变更检测）。
// 解压失败（截断/损档）以错误返回：调用方不推进游标，下次重试——绝不把
// 「半档成功」钉成永久已挖。
func ParseDSHSession(path string, fromLine int) (events []extractor.Event, totalLines int, err error) {
	f, err := dshOpen(path)
	if err != nil {
		return nil, 0, err
	}
	defer func() {
		// Close 携带 zstd 退出状态：截断/损档时读到半档即 EOF 且 sc.Err()==nil，
		// 只有 Close 错误能暴露「这不是完整数据」
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}()
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
			Type string `json:"type"`
			ID   string `json:"id"`
			CWD  string `json:"cwd"`
			Time int64  `json:"time"`
			Data struct {
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
				Source struct {
					Kind string `json:"kind"`
				} `json:"source"`
				Role    string `json:"role"`
				Message struct {
					Role    string `json:"role"`
					Content []struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"content"`
				} `json:"message"`
			} `json:"data"`
		}
		if err := json.Unmarshal(raw, &obj); err != nil {
			continue // 格式漂移容错：坏行跳过不致命
		}
		var sb strings.Builder
		switch obj.Type {
		case "session":
			// 会话头是粘性状态：全量扫描累积（增量段也继承 session/cwd，不受 fromLine 截断）
			if obj.ID != "" {
				sessionID = obj.ID
			}
			if obj.CWD != "" {
				cwd = obj.CWD
			}
			continue
		case "user/message", "assistant/message":
			if line < fromLine {
				continue // 增量跳过在头状态累积之后：段内事件完整归因
			}
		default:
			continue
		}
		ev := extractor.Event{Line: line, Origin: "dsh", SessionID: sessionID, CWD: cwd, ProjectName: projectOf(cwd, path)}
		if obj.Time > 0 {
			ev.Timestamp = time.UnixMilli(obj.Time).UTC().Format(time.RFC3339)
		}
		switch obj.Type {
		case "user/message":
			if obj.Data.Source.Kind != "user" {
				continue // plugin 注入（审批变更/运行时快照/系统提醒）：非真人发言，过滤
			}
			ev.Role = "user"
			for _, part := range obj.Data.Content {
				if part.Type == "text" {
					if sb.Len() > 0 {
						sb.WriteByte('\n')
					}
					sb.WriteString(part.Text)
				}
			}
		case "assistant/message":
			ev.Role = "assistant"
			for _, part := range obj.Data.Message.Content {
				switch part.Type {
				case "text":
					if sb.Len() > 0 {
						sb.WriteByte('\n')
					}
					sb.WriteString(part.Text)
				case "tool-call":
					ev.IsToolUse = true
				}
			}
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
