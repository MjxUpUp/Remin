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
	Origin      string // 来源 agent（claude-code/codex/dsh；空回退 claude-code——存量行为）
}

// originOf 事件来源归因（空回退 claude-code：历史 fixture 与既有批次行为不变）
func originOf(origin string) string {
	if origin != "" {
		return origin
	}
	return "claude-code"
}

// 用户显式指令（「现在就记」类）：最强信号。
// 收紧（真实库 61 条诊断）：裸「以后」是时间用法（以后再说/以后的版本），保留指令
// 形态与「以后…都/要」间隔形态；裸「记得」保留但由 userRecallVeto 排除回忆形态。
var userDirective = regexp.MustCompile(`(?i)(记住|记得|别忘了|以后(请|要|都|别)|以后.{0,6}(都|要)|总是|永远|别再|不要再用|每次都|always |never |prefer |remember to |from now on )`)

// 回忆形态否决：「我记得/不记得/还记得/记得吗」是叙述不是指令（记得×11 误触主因）
var userRecallVeto = regexp.MustCompile(`(我记得|不记得|还记得|记得吗|谁记得)`)

// 用户程序性指令（怎么做）。「别忘了」归 procedural（v0 类型行为保持）
var userProcedural = regexp.MustCompile(`(?i)(必须|先跑|先执行|先跑一下|再执行|之前要|别忘了|务必|make sure to|before .* run)`)

// 助手决策表达（为什么选 X）
var assistantDecision = regexp.MustCompile(`(?i)(我们决定|最终选择|最终决定|决定选|选了.+而不是|而非).{0,40}(因为|原因是|due to|because)`)

// 教训/坑。收紧：裸「教训」在行文中是随口引用（「先把状态与教训落盘」），
// 要求总结形态（这条/一条/方法论…教训、教训是、吸取教训）；
// 裸 root cause 是排查叙述（Confirming the root cause），仅保留中文「根因是」。
var assistantLesson = regexp.MustCompile(`(?i)((这条|一条|个|方法论|惨痛|深刻).{0,2}教训|教训是|吸取教训|记住.{0,6}教训|踩坑|这个坑|失败的原因|根因是|lesson)`)

// recap 默认有效期
const RecapExpires = "7d"

// splitSentences 按句边界切句（与 firstSentence 同一套分隔符）
func splitSentences(text string) []string {
	var out []string
	start := 0
	for i, r := range text {
		if r == '\n' || r == '。' || r == '；' || r == ';' {
			if s := strings.TrimSpace(text[start:i]); s != "" {
				out = append(out, s)
			}
			start = i + len(string(r))
		}
	}
	if s := strings.TrimSpace(text[start:]); s != "" {
		out = append(out, s)
	}
	return out
}

// triggerSentence 返回首条命中触发词的句子（正文=触发句：消息首句常是与触发点
// 无关的状态播报——真实库 61 条中 24 条正文与触发点脱节）。veto 非空时先否决
// （回忆形态）。未命中返回空。
func triggerSentence(text string, veto *regexp.Regexp, pats ...*regexp.Regexp) string {
	for _, s := range splitSentences(text) {
		if veto != nil && veto.MatchString(s) {
			continue
		}
		for _, p := range pats {
			if p.MatchString(s) {
				return s
			}
		}
	}
	return ""
}

// triggerPair 句对窗口匹配：决策表达常跨句（「我们决定选 A。原因是 X。」——句级
// 化后单句不命中，需当前句+下一句拼接窗口）。返回决策起始句。
func triggerPair(sents []string, p *regexp.Regexp) string {
	for i := 0; i < len(sents); i++ {
		if p.MatchString(sents[i]) {
			return sents[i]
		}
		if i+1 < len(sents) && p.MatchString(sents[i]+"。"+sents[i+1]) {
			return sents[i]
		}
	}
	return ""
}

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
			// 正文=触发句：先按类型优先级找首条命中的句子（procedural 强于 preference）；
			// 指令检索先过回忆形态否决（「我记得…」是叙述）
			if s := triggerSentence(text, nil, userProcedural); s != "" {
				add(makeCandidate(store.TypeProcedural, normalizeDirective(s), ev, store.TrustUnverified))
			} else if s := triggerSentence(text, userRecallVeto, userDirective); s != "" {
				add(makeCandidate(store.TypePreference, normalizeDirective(s), ev, store.TrustUnverified))
			}
		case "assistant":
			if ev.IsToolUse {
				continue
			}
			lastAssistant = ev
			if r := []rune(text); len(r) > 600 {
				continue
			}
			sents := splitSentences(text)
			if s := triggerPair(sents, assistantDecision); s != "" {
				add(makeCandidate(store.TypeDecision, normalizeDirective(s), ev, store.TrustUnverified))
			} else if s := triggerSentence(text, nil, assistantLesson); s != "" {
				add(makeCandidate(store.TypeProcedural, normalizeDirective(s), ev, store.TrustUnverified))
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
		Origin: originOf(ev.Origin),
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
		Origin: originOf(firstOrigin(events)),
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

// firstOrigin 批内首个非空来源（recap 归因用；事件同源）
func firstOrigin(events []Event) string {
	for _, ev := range events {
		if ev.Origin != "" {
			return ev.Origin
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
