package extractor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/remin-dev/remin/internal/core/config"
	"github.com/remin-dev/remin/internal/store"
)

// 深路径契约（P3-A5 弃权优于编造）：
//   - quote 逐字溯源：LLM 返回的 quote 必须出现在源事件文本中，否则拒收
//   - 类型白名单外的候选拒收
//   - 端点失败 / 非 JSON → 返回错误（弃权），绝不编造候选
//   - 只产 trust=unverified 候选，provenance 定位到 quote 命中的事件行号

func openAIContent(t *testing.T, content string) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"choices": []map[string]any{{"message": map[string]any{"content": content}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func deepCfg(url string) *config.LLMConfig {
	return &config.LLMConfig{Endpoint: url, Model: "test-model", APIKey: "k-test", TimeoutMs: 3000}
}

var deepEvents = []Event{
	{Line: 3, Role: "user", Text: "这个项目跑测试别单跑 go test，要 make constitution，不然依赖扫描漏掉", SessionID: "deep1234", Timestamp: "2026-09-21T10:00:00+08:00"},
	{Line: 7, Role: "assistant", Text: "好的，我会在每次验证时用 make constitution", SessionID: "deep1234", Timestamp: "2026-09-21T10:01:00+08:00"},
}

func TestExtractDeepKeepsOnlySourcedCandidates(t *testing.T) {
	var gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		cands := `[` +
			`{"type":"preference","body":"验证统一走 make constitution，不单跑 go test","quote":"这个项目跑测试别单跑 go test，要 make constitution，不然依赖扫描漏掉"},` +
			`{"type":"preference","body":"编造的候选","quote":"这句话根本不在会话里"},` +
			`{"type":"opinion","body":"类型不在白名单","quote":"好的，我会在每次验证时用 make constitution"}` +
			`]`
		fmt.Fprint(w, openAIContent(t, cands))
	}))
	defer srv.Close()

	got, err := ExtractDeep(context.Background(), deepCfg(srv.URL), deepEvents)
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer k-test" {
		t.Errorf("应携带 Bearer 鉴权: %q", gotAuth)
	}
	if !strings.Contains(gotBody, "make constitution") {
		t.Errorf("prompt 应包含源事件文本")
	}
	if !strings.Contains(gotBody, "test-model") {
		t.Errorf("请求应携带配置的 model")
	}
	if len(got) != 1 {
		t.Fatalf("应只保留 1 条溯源通过候选（编造 quote 与白名单外类型均拒收）: %+v", got)
	}
	c := got[0]
	if c.Type != store.TypePreference {
		t.Errorf("类型: %s", c.Type)
	}
	if c.Trust != store.TrustUnverified || c.Status != store.StatusCandidate {
		t.Errorf("深路径只产 unverified 候选: trust=%s status=%s", c.Trust, c.Status)
	}
	if c.Provenance.Origin != "claude-code·deep" {
		t.Errorf("origin 应标注深路径: %s", c.Provenance.Origin)
	}
	if c.Provenance.Ref != "session#deep1234, line 3" {
		t.Errorf("ref 应定位到 quote 命中的事件: %s", c.Provenance.Ref)
	}
	if c.Provenance.Quote == "" {
		t.Errorf("quote 必填（A1）")
	}
	if c.Source != store.SourceAgent {
		t.Errorf("source: %s", c.Source)
	}
	if c.CapturedAt != "2026-09-21T10:00:00+08:00" {
		t.Errorf("captured_at 应取命中事件时间: %s", c.CapturedAt)
	}
}

// 空白差异不阻断溯源（模型重排空白是常态，语义仍是逐字）
func TestExtractDeepQuoteMatchNormalizesWhitespace(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cands := `[{"type":"procedural","body":"验证前先跑宪法检查","quote":"好的，我会在每次验证时\n用  make  constitution"}]`
		fmt.Fprint(w, openAIContent(t, cands))
	}))
	defer srv.Close()

	got, err := ExtractDeep(context.Background(), deepCfg(srv.URL), deepEvents)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("空白归一后应溯源通过: %+v", got)
	}
	if got[0].Provenance.Ref != "session#deep1234, line 7" {
		t.Errorf("ref: %s", got[0].Provenance.Ref)
	}
}

// content 带 ```json 围栏也能解析（模型常见输出形态）
func TestExtractDeepParsesFencedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cands := "```json\n[{\"type\":\"preference\",\"body\":\"验证统一走 make constitution\",\"quote\":\"这个项目跑测试别单跑 go test，要 make constitution，不然依赖扫描漏掉\"}]\n```"
		fmt.Fprint(w, openAIContent(t, cands))
	}))
	defer srv.Close()

	got, err := ExtractDeep(context.Background(), deepCfg(srv.URL), deepEvents)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("围栏内 JSON 应解析: %+v", got)
	}
}

// 失败即弃权：HTTP 500 / 200 非 JSON 都返回错误，不产出任何候选
func TestExtractDeepAbstainsOnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	if got, err := ExtractDeep(context.Background(), deepCfg(srv.URL), deepEvents); err == nil || len(got) != 0 {
		t.Errorf("500 应弃权: err=%v got=%d", err, len(got))
	}

	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "这不是 JSON")
	}))
	defer srv2.Close()
	if got, err := ExtractDeep(context.Background(), deepCfg(srv2.URL), deepEvents); err == nil || len(got) != 0 {
		t.Errorf("非 JSON 应弃权: err=%v got=%d", err, len(got))
	}
}

// 空数组与空事件：无候选也无错误（不值得记就是弃权，不是失败）
func TestExtractDeepEmptyIsNotError(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		fmt.Fprint(w, openAIContent(t, "[]"))
	}))
	defer srv.Close()

	if got, err := ExtractDeep(context.Background(), deepCfg(srv.URL), deepEvents); err != nil || len(got) != 0 {
		t.Errorf("空数组: err=%v got=%d", err, len(got))
	}
	if got, err := ExtractDeep(context.Background(), deepCfg(srv.URL), nil); err != nil || len(got) != 0 {
		t.Errorf("空事件: err=%v got=%d", err, len(got))
	}
	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Errorf("空事件不应发起 HTTP 调用: hits=%d", n)
	}
}

// 未配置 / 缺密钥：明确报错，不静默降级（用户显式要了 --deep）
func TestExtractDeepUnconfiguredErrors(t *testing.T) {
	if _, err := ExtractDeep(context.Background(), nil, deepEvents); err == nil || !strings.Contains(err.Error(), "llm") {
		t.Errorf("nil 配置应报错: %v", err)
	}
	if _, err := ExtractDeep(context.Background(), &config.LLMConfig{APIKey: "k"}, deepEvents); err == nil || !strings.Contains(err.Error(), "endpoint") {
		t.Errorf("缺 endpoint 应报错: %v", err)
	}
	if _, err := ExtractDeep(context.Background(), &config.LLMConfig{Endpoint: "http://x"}, deepEvents); err == nil || !strings.Contains(err.Error(), "REMIN_LLM_API_KEY") {
		t.Errorf("缺密钥应报错: %v", err)
	}
}

// 端点超时受 TimeoutMs 硬预算约束
func TestExtractDeepTimeoutBudget(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		fmt.Fprint(w, openAIContent(t, "[]"))
	}))
	defer srv.Close()
	cfg := deepCfg(srv.URL)
	cfg.TimeoutMs = 50
	if _, err := ExtractDeep(context.Background(), cfg, deepEvents); err == nil {
		t.Errorf("超预算应返回错误")
	}
}

// 同 body 去重（模型重复输出是常态）
func TestExtractDeepDedupsByBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := "这个项目跑测试别单跑 go test，要 make constitution，不然依赖扫描漏掉"
		cands := fmt.Sprintf(`[{"type":"preference","body":"同一句话","quote":%q},{"type":"preference","body":"同一句话","quote":%q}]`, q, q)
		fmt.Fprint(w, openAIContent(t, cands))
	}))
	defer srv.Close()
	got, err := ExtractDeep(context.Background(), deepCfg(srv.URL), deepEvents)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Errorf("同 body 应去重: %d", len(got))
	}
}

// 响应候选数上限：超 50 条截断（防端点灌爆 inbox）
func TestExtractDeepCapsCandidateCount(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := "这个项目跑测试别单跑 go test，要 make constitution，不然依赖扫描漏掉"
		var parts []string
		for i := 0; i < 80; i++ {
			parts = append(parts, fmt.Sprintf(`{"type":"preference","body":"第 %d 条","quote":%q}`, i, q))
		}
		fmt.Fprint(w, openAIContent(t, "["+strings.Join(parts, ",")+"]"))
	}))
	defer srv.Close()
	got, err := ExtractDeep(context.Background(), deepCfg(srv.URL), deepEvents)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 50 {
		t.Errorf("候选数应截断到 50: %d", len(got))
	}
}

// 送入条数上限：超 200 条事件只送尾部最新（服务端实证收到的条数）
func TestExtractDeepSendsOnlyTailEvents(t *testing.T) {
	evtCount := make(chan int, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		// 信封/提示词均无「事件」字样，仅事件文本含——精确计送入条数
		evtCount <- strings.Count(string(b), "事件 ")
		fmt.Fprint(w, openAIContent(t, "[]"))
	}))
	defer srv.Close()

	var many []Event
	for i := 0; i < 260; i++ {
		many = append(many, Event{Line: i + 1, Role: "user", Text: fmt.Sprintf("事件 %d", i), SessionID: "s"})
	}
	if _, err := ExtractDeep(context.Background(), deepCfg(srv.URL), many); err != nil {
		t.Fatal(err)
	}
	// 信封/提示词均无「事件」字样，仅事件文本含——精确计送入条数
	if gotCount := <-evtCount; gotCount != 200 {
		t.Errorf("应只送尾部 200 条事件: %d", gotCount)
	}
}

// 响应体超 1MB 截断 → 非法 JSON → 弃权（不部分解析）
func TestExtractDeepRejectsOversizedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(make([]byte, 2<<20)) // 2MB 零值字节
	}))
	defer srv.Close()
	if got, err := ExtractDeep(context.Background(), deepCfg(srv.URL), deepEvents); err == nil || len(got) != 0 {
		t.Errorf("超限响应应弃权: err=%v got=%d", err, len(got))
	}
}
