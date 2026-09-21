// Package extractor 启发式快速路径（零 LLM，硬预算内）：从 transcript 事件提取记忆候选。
// 只产提案（unverified → inbox），绝不直接落库。
package extractor

import (
	"regexp"
	"strings"

	"github.com/remin-dev/remin/internal/core/inbox"
	"github.com/remin-dev/remin/internal/store"
)

// Event 从 transcript 提取的结构化事件（provenance 锚点：行号）。
// 定义在本包（叶子），miner 解析后交由 Extract 消费，依赖单向。
type Event struct {
	Line        int
	Role        string // user | assistant
	Text        string // 文本内容（工具结果不计）
	IsToolUse   bool
	SessionID   string
	CWD         string
	Timestamp   string
	ProjectName string
}

// 用户显式指令（「现在就记」类）：最强信号
var userDirective = regexp.MustCompile(`(?i)(记住|记得|以后请|以后要|以后|总是|永远|别再|不要再用|每次都|always |never |prefer |remember |from now on )`)

// 用户程序性指令（怎么做）
var userProcedural = regexp.MustCompile(`(?i)(必须|先跑|先执行|先跑一下|再执行|之前要|别忘了|务必|make sure to|before .* run)`)

// 助手决策表达（为什么选 X）
var assistantDecision = regexp.MustCompile(`(?i)(我们决定|最终选择|最终决定|决定选|选了.+而不是|而非).{0,40}(因为|原因是|due to|because)`)

// 教训/坑
var assistantLesson = regexp.MustCompile(`(?i)(教训|踩坑|这个坑|失败的原因|根因是|root cause|lesson)`)

// recap 默认有效期
const RecapExpires = "7d"

// Extract 从一批事件提取候选（启发式；quota 限制防爆量）
func Extract(events []Event) []*inbox.Candidate {
	var out []*inbox.Candidate
	seen := map[string]bool{} // 同文本去重
	add := func(c *inbox.Candidate) {
		key := c.Body
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, c)
	}
	var lastAssistant Event
	var firstTs, lastTs string
	var sid string
	for _, ev := range events {
		if sid == "" && ev.SessionID != "" {
			sid = ev.SessionID
		}
		if ev.Timestamp != "" {
			if firstTs == "" {
				firstTs = ev.Timestamp
			}
			lastTs = ev.Timestamp
		}
		text := cleanText(ev.Text)
		if text == "" {
			continue
		}
		switch ev.Role {
		case "user":
			if isNoise(text) {
				continue
			}
			if r := []rune(text); len(r) < 4 || len(r) > 500 {
				continue
			}
			mtype := ""
			switch {
			case userProcedural.MatchString(text):
				mtype = store.TypeProcedural
			case userDirective.MatchString(text):
				mtype = store.TypePreference
			}
			if mtype != "" {
				add(makeCandidate(mtype, normalizeDirective(text), ev, store.TrustUnverified))
			}
		case "assistant":
			if ev.IsToolUse {
				continue
			}
			lastAssistant = ev
			if r := []rune(text); len(r) > 600 {
				continue
			}
			switch {
			case assistantDecision.MatchString(text):
				add(makeCandidate(store.TypeDecision, firstSentence(text), ev, store.TrustUnverified))
			case assistantLesson.MatchString(text):
				add(makeCandidate(store.TypeProcedural, firstSentence(text), ev, store.TrustUnverified))
			}
		}
	}
	// session-recap：每个 transcript 一条（episodic + ephemeral 7d）
	if sid != "" || lastAssistant.Text != "" {
		recap := makeRecap(sid, firstTs, lastTs, events, lastAssistant)
		if recap != nil {
			out = append(out, recap)
		}
	}
	return out
}

func makeCandidate(mtype, body string, ev Event, trust string) *inbox.Candidate {
	c := &inbox.Candidate{}
	c.Type = mtype
	c.Facet = "dev"
	if p := ev.ProjectName; p != "" {
		c.Context = []string{p}
	}
	c.Status = store.StatusCandidate
	c.CapturedAt = ev.Timestamp
	c.ReviewedAt = store.TimeUnknown
	c.Modified = store.NowTime()
	c.Trust = trust
	c.Source = store.SourceAgent
	c.Provenance = store.Provenance{
		Origin: "claude-code",
		Ref:    refOf(ev),
		Quote:  truncate(ev.Text, 400),
	}
	c.Version = store.FormatVersion
	c.Body = body
	return c
}

func makeRecap(sid, firstTs, lastTs string, events []Event, lastAssistant Event) *inbox.Candidate {
	c := &inbox.Candidate{}
	c.Type = store.TypeEpisodic
	c.Facet = "dev"
	if len(events) > 0 && events[0].ProjectName != "" {
		c.Context = []string{events[0].ProjectName}
	}
	c.Status = store.StatusCandidate
	c.CapturedAt = lastTs
	if c.CapturedAt == "" {
		c.CapturedAt = store.NowTime()
	}
	c.ReviewedAt = store.TimeUnknown
	c.Modified = store.NowTime()
	c.Trust = store.TrustUnverified
	c.Source = store.SourceAgent
	// quote 取会话内最后一段可用文本（优先助手末状态；无则回退末条文本；再无则如实描述）
	quote := truncate(lastAssistant.Text, 200)
	if quote == "" {
		quote = truncate(lastText(events), 200)
	}
	if quote == "" {
		quote = "（transcript 无文本事件，仅行范围）"
	}
	c.Provenance = store.Provenance{
		Origin: "claude-code",
		Ref:    recapRef(sid, events),
		Quote:  quote,
	}
	c.Expires = RecapExpires
	c.Version = store.FormatVersion
	turns := 0
	for _, ev := range events {
		if ev.Role == "user" {
			turns++
		}
	}
	var sb strings.Builder
	sb.WriteString("会话交接：")
	if p := firstProject(events); p != "" {
		sb.WriteString("项目 " + p + "，")
	}
	sb.WriteString(itoa(turns) + " 轮对话。")
	if quote != "" {
		sb.WriteString("末状态：" + truncate(oneLine(quote), 120))
	}
	c.Body = sb.String()
	return c
}

func firstProject(events []Event) string {
	for _, ev := range events {
		if ev.ProjectName != "" {
			return ev.ProjectName
		}
	}
	return ""
}

// lastText 会话内最后一段非空文本（quote 回退用）
func lastText(events []Event) string {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Text != "" {
			return events[i].Text
		}
	}
	return ""
}

func recapRef(sid string, events []Event) string {
	if len(events) == 0 {
		return "session#" + shortSID(sid)
	}
	return "session#" + shortSID(sid) + ", lines " + itoa(events[0].Line) + "-" + itoa(events[len(events)-1].Line)
}

func refOf(ev Event) string {
	return "session#" + shortSID(ev.SessionID) + ", line " + itoa(ev.Line)
}

func shortSID(sid string) string {
	if len(sid) > 8 {
		return sid[:8]
	}
	return sid
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// cleanText 去掉系统注入噪音的首尾空白
func cleanText(s string) string {
	return strings.TrimSpace(s)
}

// isNoise 过滤系统提醒、命令回显等非用户意图文本
func isNoise(text string) bool {
	if strings.HasPrefix(text, "<") { // <system-reminder> 等标签包裹的系统注入
		return true
	}
	if strings.HasPrefix(text, "Caveat:") {
		return true
	}
	if strings.HasPrefix(text, "[Request interrupted") {
		return true
	}
	return false
}

// normalizeDirective 用户指令整理为记忆正文
func normalizeDirective(text string) string {
	return firstSentence(text)
}

func firstSentence(text string) string {
	text = strings.TrimSpace(text)
	if i := strings.Index(text, "\n"); i >= 0 {
		text = text[:i]
	}
	// 截到首个句号（中英文）后的完整句
	for _, sep := range []string{"。", "；", ";"} {
		if i := strings.Index(text, sep); i >= 0 && i > 8 {
			return strings.TrimSpace(text[:i+len(sep)])
		}
	}
	return truncate(text, 200)
}

func oneLine(s string) string {
	if i := strings.Index(s, "\n"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
