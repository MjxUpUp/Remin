// 深度提取路径（P3 约束下的 LLM 语义提取）：与快速路径同构——只产 unverified 候选，
// 人审才落库。失败即弃权（返回错误），绝不编造；每条候选的 quote 必须逐字溯源
// 到源事件（空白归一后包含匹配），对不上即拒收——宁可不知道，不能自信地错。
package extractor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/remin-dev/remin/internal/core/config"
	"github.com/remin-dev/remin/internal/core/inbox"
	"github.com/remin-dev/remin/internal/store"
)

// 深路径硬预算（端点失控的五重上限：送入条数 / 文本长度 / 响应体 / 候选数 / 单次超时）。
// 单次超时同时是 WithRoot 互斥下最坏持锁时长的上界（手动 mine --deep 串行 N 个 transcript）
const (
	deepMaxEvents        = 200     // 单 transcript 送入 LLM 的事件上限（取尾部最新）
	deepMaxEventRunes    = 2000    // 单事件文本截断
	deepMaxBodyRunes     = 300     // 候选正文上限
	deepMaxCandidates    = 50      // 单次响应候选上限
	deepMaxResponseBytes = 1 << 20 // 响应体 1MB
	deepMaxTimeoutMs     = 120000  // TimeoutMs 上限（防配置笔误把锁内 IO 拖到小时级）
	deepOrigin           = "claude-code·deep"
)

const deepSystemPrompt = `你是个人记忆系统的提取器。从给定的 agent 会话记录中提取值得长期记住的记忆候选。
只输出严格 JSON 数组，不要输出任何其他文字：[{"type":"...","body":"...","quote":"..."}]
- type 只能取：preference | procedural | decision | episodic | semantic
- body：一句话记忆（120 字内），陈述句
- quote：支撑该记忆的原文片段，必须逐字取自会话记录（这是溯源锚点，编造的候选会被拒收）
- 宁缺毋滥：没有值得记的就输出 []
- 不提取：临时事实、待办事项、代码实现细节、寒暄`

type deepCandidate struct {
	Type  string `json:"type"`
	Body  string `json:"body"`
	Quote string `json:"quote"`
}

type deepRequest struct {
	Model    string        `json:"model"`
	Messages []deepMessage `json:"messages"`
}

type deepMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type deepResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// ExtractDeep 深度提取：LLM 语义理解补充快速路径的召回（regex 抓不到的自然表达）。
// 挂手动 remin mine --deep 与闲时 tick 排空；Stop hook 与 inject 追赶路径永不触网（延迟预算硬约束）。
func ExtractDeep(ctx context.Context, llm *config.LLMConfig, events []Event) ([]*inbox.Candidate, error) {
	if llm == nil || llm.Endpoint == "" {
		return nil, fmt.Errorf("深度提取未配置：config.yaml 缺 llm.endpoint（端点可配，OpenAI 兼容）")
	}
	if llm.APIKey == "" {
		return nil, fmt.Errorf("深度提取缺密钥：设置环境变量 REMIN_LLM_API_KEY（密钥不落 config.yaml——它在 git 真源内）")
	}
	if len(events) == 0 {
		return nil, nil
	}
	timeout := time.Duration(llm.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = time.Duration(config.LLMDefaultTimeoutMs) * time.Millisecond
	}
	if max := time.Duration(deepMaxTimeoutMs) * time.Millisecond; timeout > max {
		timeout = max
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	content, err := deepCall(ctx, llm, events)
	if err != nil {
		return nil, err
	}
	raw, err := deepParseContent(content)
	if err != nil {
		return nil, err
	}
	if len(raw) > deepMaxCandidates {
		raw = raw[:deepMaxCandidates]
	}
	return deepGuard(raw, events), nil
}

// deepCall 调用 OpenAI 兼容端点（stdlib net/http，零 SDK——N1 白名单天然过）
func deepCall(ctx context.Context, llm *config.LLMConfig, events []Event) (string, error) {
	if len(events) > deepMaxEvents {
		events = events[len(events)-deepMaxEvents:]
	}
	var sb strings.Builder
	sb.WriteString("会话记录（JSON 行，role/text）：\n")
	for _, ev := range events {
		text := truncate(cleanText(ev.Text), deepMaxEventRunes)
		if text == "" {
			continue
		}
		b, err := json.Marshal(struct {
			Role string `json:"role"`
			Text string `json:"text"`
		}{ev.Role, text})
		if err != nil {
			return "", err
		}
		sb.Write(b)
		sb.WriteByte('\n')
	}
	reqBody, err := json.Marshal(deepRequest{
		Model: llm.Model,
		Messages: []deepMessage{
			{Role: "system", Content: deepSystemPrompt},
			{Role: "user", Content: sb.String()},
		},
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, llm.Endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+llm.APIKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("深度提取端点调用失败: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, deepMaxResponseBytes))
	if err != nil {
		return "", fmt.Errorf("深度提取响应读取失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("深度提取端点返回 %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	var dr deepResponse
	if err := json.Unmarshal(body, &dr); err != nil {
		return "", fmt.Errorf("深度提取响应非 OpenAI 兼容结构: %w", err)
	}
	if len(dr.Choices) == 0 {
		return "", fmt.Errorf("深度提取响应无 choices")
	}
	return dr.Choices[0].Message.Content, nil
}

// deepParseContent 解出候选数组：容忍 ```json 围栏与前后杂文（守卫在后，解析宽容不引入幻觉）
func deepParseContent(content string) ([]deepCandidate, error) {
	s := strings.TrimSpace(content)
	if strings.HasPrefix(s, "```") {
		if i := strings.Index(s, "\n"); i >= 0 {
			s = s[i+1:]
		}
		if j := strings.LastIndex(s, "```"); j >= 0 {
			s = s[:j]
		}
		s = strings.TrimSpace(s)
	}
	var raw []deepCandidate
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		// 模型偶发前后缀杂文：取首个 [ 到末个 ] 再试一次
		if i, j := strings.Index(s, "["), strings.LastIndex(s, "]"); i >= 0 && j > i {
			if err2 := json.Unmarshal([]byte(s[i:j+1]), &raw); err2 != nil {
				return nil, fmt.Errorf("深度提取 content 非严格 JSON: %w", err)
			}
		} else {
			return nil, fmt.Errorf("深度提取 content 非严格 JSON: %w", err)
		}
	}
	return raw, nil
}

// deepGuard 逐条守卫：类型白名单 + quote 逐字溯源 + 长度 + 去重。拒收不报错（弃权语义）
func deepGuard(raw []deepCandidate, events []Event) []*inbox.Candidate {
	allowed := map[string]bool{
		store.TypePreference: true, store.TypeProcedural: true, store.TypeDecision: true,
		store.TypeEpisodic: true, store.TypeSemantic: true,
	}
	var out []*inbox.Candidate
	seen := map[string]bool{}
	for _, rc := range raw {
		if !allowed[rc.Type] {
			continue
		}
		body := strings.TrimSpace(rc.Body)
		if r := []rune(body); len(r) < 4 || len(r) > deepMaxBodyRunes {
			continue
		}
		quote := strings.TrimSpace(rc.Quote)
		if r := []rune(quote); len(r) < 4 || len(r) > 400 {
			continue
		}
		ev, ok := locateEvent(events, quote)
		if !ok {
			continue // 编造 quote：拒收（A5——宁可不知道）
		}
		if seen[body] {
			continue
		}
		seen[body] = true
		out = append(out, deepCandidateFrom(body, rc.Type, quote, ev))
	}
	return out
}

// locateEvent 溯源：空白归一后 quote 被事件文本包含即命中，取最早事件（确定性）
func locateEvent(events []Event, quote string) (Event, bool) {
	nq := normWS(quote)
	if nq == "" {
		return Event{}, false
	}
	for _, ev := range events {
		if strings.Contains(normWS(cleanText(ev.Text)), nq) {
			return ev, true
		}
	}
	return Event{}, false
}

// deepOriginOf 深路径候选来源归因（随源 agent；空回退存量 claude-code·deep）
func deepOriginOf(ev Event) string {
	if ev.Origin != "" {
		return ev.Origin + "·deep"
	}
	return deepOrigin
}

func deepCandidateFrom(body, mtype, quote string, ev Event) *inbox.Candidate {
	c := &inbox.Candidate{}
	c.Type = mtype
	c.Facet = "dev"
	if p := ev.ProjectName; p != "" {
		c.Context = []string{p}
	}
	c.Status = store.StatusCandidate
	c.CapturedAt = ev.Timestamp
	if c.CapturedAt == "" {
		c.CapturedAt = store.NowTime()
	}
	c.ReviewedAt = store.TimeUnknown
	c.Modified = store.NowTime()
	c.Trust = store.TrustUnverified
	c.Source = store.SourceAgent
	c.Provenance = store.Provenance{
		Origin: deepOriginOf(ev),
		Ref:    refOf(ev),
		Quote:  truncate(quote, 400),
	}
	c.Version = store.FormatVersion
	c.Body = body
	return c
}

// normWS 剔除全部空白（模型重排/插空白不阻断逐字溯源判定；两侧对称处理，
// 内容仍须逐字符一致，防幻觉性质不变。CJK 文本无词间空格，折叠为单空格仍会错位）
func normWS(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if unicode.IsSpace(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
