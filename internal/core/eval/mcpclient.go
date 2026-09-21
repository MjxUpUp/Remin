package eval

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// mcpStdioClient 极简 MCP stdio 客户端（newline-delimited JSON-RPC 2.0）。
// parity 评测必须实跑 MCP 通道（架构 §9：CLI inject 产物与 MCP memory_search
// 结果内容对等）——不允许用同进程引擎调用偷换通道。零 SDK 依赖（N1）。
type mcpStdioClient struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Scanner
	nextID int
}

type mcpSearchResult struct {
	Results []struct {
		ID    string `json:"id"`
		Trust string `json:"trust"`
	} `json:"results"`
	Abstained    bool `json:"abstained"`
	IndexVersion int  `json:"index_version"`
}

func startMCPStdio(binPath, root string) (*mcpStdioClient, error) {
	cmd := exec.Command(binPath, "mcp", "--root", root)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("拉起 MCP server 失败（%s mcp）: %w", binPath, err)
	}
	c := &mcpStdioClient{cmd: cmd, stdin: stdin, stdout: bufio.NewScanner(stdout)}
	c.stdout.Buffer(make([]byte, 1<<20), 16<<20)
	if err := c.initialize(); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

func (c *mcpStdioClient) send(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = c.stdin.Write(append(data, '\n'))
	return err
}

// recv 读到带 id 的响应为止（跳过 server 通知）
func (c *mcpStdioClient) recv(id int) (json.RawMessage, error) {
	for c.stdout.Scan() {
		line := strings.TrimSpace(c.stdout.Text())
		if line == "" {
			continue
		}
		var msg struct {
			ID    *int `json:"id"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}
		if msg.ID == nil || *msg.ID != id {
			continue
		}
		if msg.Error != nil {
			return nil, fmt.Errorf("MCP 错误: %s", msg.Error.Message)
		}
		return msg.Result, nil
	}
	return nil, fmt.Errorf("MCP server 无响应")
}

func (c *mcpStdioClient) initialize() error {
	c.nextID++
	id := c.nextID
	if err := c.send(map[string]any{
		"jsonrpc": "2.0", "id": id, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "remin-eval", "version": "0"},
		},
	}); err != nil {
		return err
	}
	if _, err := c.recv(id); err != nil {
		return err
	}
	return c.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
}

// memorySearch 实跑 memory_search 工具
func (c *mcpStdioClient) memorySearch(query string, topK int) (*mcpSearchResult, error) {
	c.nextID++
	id := c.nextID
	args := map[string]any{"query": query}
	if topK > 0 {
		args["top_k"] = topK
	}
	if err := c.send(map[string]any{
		"jsonrpc": "2.0", "id": id, "method": "tools/call",
		"params": map[string]any{"name": "memory_search", "arguments": args},
	}); err != nil {
		return nil, err
	}
	raw, err := c.recv(id)
	if err != nil {
		return nil, err
	}
	var res struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		StructuredContent json.RawMessage `json:"structuredContent"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	payload := res.StructuredContent
	if len(payload) == 0 && len(res.Content) > 0 {
		payload = json.RawMessage(res.Content[0].Text)
	}
	var out mcpSearchResult
	if err := json.Unmarshal(payload, &out); err != nil {
		return nil, fmt.Errorf("memory_search 结果解析失败: %w", err)
	}
	return &out, nil
}

func (c *mcpStdioClient) Close() {
	_ = c.stdin.Close()
	_ = c.cmd.Wait()
}
