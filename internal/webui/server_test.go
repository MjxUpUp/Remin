package webui

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/remin-dev/remin/internal/core/inbox"
	"github.com/remin-dev/remin/internal/store"
	"github.com/remin-dev/remin/internal/testutil"
)

// webui 契约：API 是核心包的皮肤（状态/批次/详情/采纳/拒绝/检索/单条全貌），
// 与 CLI 同选择器语义；写操作 JSON-only（跨站表单防线）；错误走统一包络。

func newUITest(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	st := testutil.NewStore(t)
	srv := httptest.NewServer(Handler(st))
	t.Cleanup(srv.Close)
	return srv, st
}

func getJSON(t *testing.T, url string) map[string]any {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var j map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&j); err != nil {
		t.Fatal(err)
	}
	return j
}

func postSel(t *testing.T, url string, contentType string, sel map[string]any) map[string]any {
	t.Helper()
	data, _ := json.Marshal(sel)
	req, _ := http.NewRequest("POST", url, bytes.NewReader(data))
	req.Header.Set("Content-Type", contentType)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var j map[string]any
	json.NewDecoder(resp.Body).Decode(&j)
	j["_status"] = float64(resp.StatusCode)
	return j
}

func seedBatch(t *testing.T, st *store.Store, body1, body2, typ2 string) string {
	t.Helper()
	in := inbox.New(st)
	mk := func(body, typ string) *inbox.Candidate {
		c := &inbox.Candidate{}
		c.Type = typ
		c.Facet = "dev"
		c.Status = store.StatusCandidate
		c.CapturedAt = store.NowTime()
		c.ReviewedAt = store.TimeUnknown
		c.Modified = store.NowTime()
		c.Trust = store.TrustUnverified
		c.Source = store.SourceAgent
		c.Provenance = store.Provenance{Origin: "claude-code", Ref: "session#t, line 1", Quote: body}
		c.Version = store.FormatVersion
		c.Body = body
		return c
	}
	batch, _, err := in.AddBatch("mine", []*inbox.Candidate{mk(body1, "preference"), mk(body2, typ2)})
	if err != nil {
		t.Fatal(err)
	}
	return batch
}

func TestUIReviewFlow(t *testing.T) {
	srv, st := newUITest(t)
	batch := seedBatch(t, st, "部署前必须检查迁移脚本", "会话交接：测试", "episodic")

	// 状态面：批次 + 构成统计
	j := getJSON(t, srv.URL+"/api/state")
	if j["ok"] != true {
		t.Fatalf("state 应 ok: %v", j)
	}
	batches := j["data"].(map[string]any)["batches"].([]any)
	if len(batches) != 1 {
		t.Fatalf("应 1 批次: %v", batches)
	}
	b0 := batches[0].(map[string]any)
	if b0["id"] != batch || b0["pending"].(float64) != 2 {
		t.Fatalf("批次元数据: %v", b0)
	}
	types := b0["types"].(map[string]any)
	if types["preference"].(float64) != 1 || types["episodic"].(float64) != 1 {
		t.Fatalf("构成统计: %v", types)
	}

	// 详情面：候选逐条可见
	j = getJSON(t, srv.URL+"/api/batch?id="+batch)
	cands := j["data"].(map[string]any)["candidates"].([]any)
	if len(cands) != 2 {
		t.Fatalf("应 2 候选: %v", cands)
	}

	// 类型分诊拒绝：仅拒 episodic（与 CLI reject --batch X --all --type 同语义：all 场域内按 type 收窄）
	j = postSel(t, srv.URL+"/api/reject", "application/json", map[string]any{"batch": batch, "all": true, "type": "episodic"})
	if j["ok"] != true {
		t.Fatalf("按类型拒绝应成功: %v", j)
	}
	// 全批采纳剩余
	j = postSel(t, srv.URL+"/api/promote", "application/json", map[string]any{"batch": batch, "all": true})
	if j["ok"] != true {
		t.Fatalf("全批采纳应成功: %v", j)
	}

	// 检索面：已采纳记忆可检索（trust 随行）
	j = getJSON(t, srv.URL+"/api/search?q="+strings.ReplaceAll("部署 迁移", " ", "%20"))
	d := j["data"].(map[string]any)
	hits := d["results"].([]any)
	if len(hits) == 0 {
		t.Fatalf("采纳后应可检索: %v", j)
	}
	h0 := hits[0].(map[string]any)
	if h0["trust"] != store.TrustHumanVerified {
		t.Fatalf("人审采纳应 human-verified: %v", h0)
	}
	id := h0["id"].(string)

	// 单条全貌（JSON 契约与 CLI status --json 同源：store.Memory 字段名）
	j = getJSON(t, srv.URL+"/api/memory?id="+id)
	m := j["data"].(map[string]any)["memory"].(map[string]any)
	if m["ID"] != id || m["Body"].(string) == "" {
		t.Fatalf("单条全貌: %v", m)
	}
}

// TestUIWriteRequiresJSON CSRF 防线：非 JSON content-type 的写请求必须被拒
func TestUIWriteRequiresJSON(t *testing.T) {
	srv, st := newUITest(t)
	batch := seedBatch(t, st, "内容甲", "内容乙", "semantic")
	j := postSel(t, srv.URL+"/api/promote", "application/x-www-form-urlencoded", map[string]any{"batch": batch, "all": true})
	if j["ok"] != false {
		t.Fatalf("表单写必须被拒（跨站表单无法伪造 JSON POST）: %v", j)
	}
	if j["_status"].(float64) != 400 {
		t.Fatalf("应 400: %v", j)
	}
}

// TestUISelectorErrors 选择器错误如实上报（互斥/未命中）
func TestUISelectorErrors(t *testing.T) {
	srv, st := newUITest(t)
	batch := seedBatch(t, st, "内容甲", "内容乙", "semantic")
	j := postSel(t, srv.URL+"/api/promote", "application/json", map[string]any{"batch": batch, "all": true, "type": "episodic", "ids": []string{"x"}})
	if j["ok"] != false || !strings.Contains(j["error"].(string), "互斥") {
		t.Fatalf("ids+type 互斥应报错: %v", j)
	}
	j = postSel(t, srv.URL+"/api/reject", "application/json", map[string]any{"batch": batch, "type": "episodic"})
	if j["ok"] != false || !strings.Contains(j["error"].(string), "未命中") {
		t.Fatalf("type 未命中应报错: %v", j)
	}
}

// TestUIIndexServed 首页为内嵌单页
func TestUIIndexServed(t *testing.T) {
	srv, _ := newUITest(t)
	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("首页应 200 html: %v", resp)
	}
	buf := make([]byte, 512)
	n, _ := resp.Body.Read(buf)
	if !strings.Contains(string(buf[:n]), "Remin") {
		t.Fatalf("首页应含标题: %s", buf[:n])
	}
	// 静态路径外 404
	resp2, _ := http.Get(srv.URL + "/../etc/passwd")
	if resp2 != nil && resp2.StatusCode != 404 {
		t.Fatalf("未知路径应 404: %v", resp2.StatusCode)
	}
}

// TestUIPromoteAtomicity UI 采纳走同一原子提交核心（失败整体回滚语义由 promotion 测试钉死，
// 此处钉 UI 管线确实调用了 promotion 而非旁路）
func TestUIPromoteAtomicity(t *testing.T) {
	srv, st := newUITest(t)
	batch := seedBatch(t, st, "唯一候选内容", "占位", "semantic")
	j := postSel(t, srv.URL+"/api/promote", "application/json", map[string]any{"batch": batch, "ids": func() []string {
		in := inbox.New(st)
		cands, _ := in.ListCandidates(batch)
		return []string{cands[0].ID}
	}()})
	if j["ok"] != true {
		t.Fatalf("单条采纳应成功: %v", j)
	}
	data := j["data"].(map[string]any)
	if data["Version"].(float64) < 1 || data["Commit"].(string) == "" {
		t.Fatalf("采纳结果应含版本与 commit（原子提交核心产物）: %v", data)
	}
}

// TestUIWriteBlocksRebindingHost DNS rebinding 防线：写操作 Host 非回环字面量必须被拒
// （审查 P2 杀灭测试——rebinding 域名解析到 127.0.0.1 时只有 Host 校验能拦）
func TestUIWriteBlocksRebindingHost(t *testing.T) {
	srv, st := newUITest(t)
	batch := seedBatch(t, st, "内容甲", "内容乙", "semantic")
	data, _ := json.Marshal(map[string]any{"batch": batch, "all": true})
	req, _ := http.NewRequest("POST", srv.URL+"/api/promote", bytes.NewReader(data))
	req.Host = "evil.example.com"
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var j map[string]any
	json.NewDecoder(resp.Body).Decode(&j)
	if j["ok"] != false || !strings.Contains(j["error"].(string), "回环") {
		t.Fatalf("rebinding Host 写必须被拒: %v", j)
	}
	// Origin 附加校验：跨源 Origin 被拒
	req2, _ := http.NewRequest("POST", srv.URL+"/api/reject", bytes.NewReader(data))
	req2.Host = "127.0.0.1"
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Origin", "https://evil.example.com")
	resp2, _ := http.DefaultClient.Do(req2)
	if resp2 == nil {
		t.Fatal("请求应可达")
	}
	defer resp2.Body.Close()
	var j2 map[string]any
	json.NewDecoder(resp2.Body).Decode(&j2)
	if j2["ok"] != false {
		t.Fatalf("跨源 Origin 写必须被拒: %v", j2)
	}
}

// TestUIWriteAcceptsJSONCharset content-type 容忍 charset=utf-8 后缀（不弱化 JSON 防线）
func TestUIWriteAcceptsJSONCharset(t *testing.T) {
	srv, st := newUITest(t)
	batch := seedBatch(t, st, "内容甲", "内容乙", "semantic")
	j := postSel(t, srv.URL+"/api/reject", "application/json; charset=utf-8", map[string]any{"batch": batch, "all": true})
	if j["ok"] != true {
		t.Fatalf("charset=utf-8 的 JSON 写应被接受: %v", j)
	}
}
