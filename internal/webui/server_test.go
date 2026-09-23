package webui

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/remin-dev/remin/internal/core/eval"
	"github.com/remin-dev/remin/internal/core/inbox"
	"github.com/remin-dev/remin/internal/core/miner"
	"github.com/remin-dev/remin/internal/index"
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

	// 详情面：候选逐条可见（字段级断言——snake_case 投影，防内嵌结构直出回归）
	j = getJSON(t, srv.URL+"/api/batch?id="+batch)
	cands := j["data"].(map[string]any)["candidates"].([]any)
	if len(cands) != 2 {
		t.Fatalf("应 2 候选: %v", cands)
	}
	c0 := cands[0].(map[string]any)
	if c0["id"] == nil || c0["body"] == nil || c0["type"] == nil || c0["trust"] == nil {
		t.Fatalf("候选字段须为 snake_case 且非空（UI 依赖）: %v", c0)
	}
	if prov := c0["provenance"].(map[string]any); prov["origin"] == nil || prov["ref"] == nil || prov["quote"] == nil {
		t.Fatalf("provenance 三要素须随行: %v", prov)
	}

	// 类型分诊拒绝：仅拒 episodic（与 CLI reject --batch X --all --type 同语义：all 场域内按 type 收窄）
	j = postSel(t, srv.URL+"/api/reject", "application/json", map[string]any{"batch": batch, "all": true, "type": "episodic"})
	if j["ok"] != true {
		t.Fatalf("按类型拒绝应成功: %v", j)
	}
	rd := j["data"].(map[string]any)
	if len(rd["rejected"].([]any)) != 1 || rd["commit"].(string) == "" {
		t.Fatalf("拒绝回执须为 rejected/commit 形状（UI 回执依赖）: %v", rd)
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

// TestUIStateExtendedFields 状态带数据面：记忆数/深挖待办/tick 留档（A2/D2 原型确认项）
func TestUIStateExtendedFields(t *testing.T) {
	srv, st := newUITest(t)
	batch := seedBatch(t, st, "内容甲", "内容乙", "semantic")
	postSel(t, srv.URL+"/api/promote", "application/json", map[string]any{"batch": batch, "all": true})

	j := getJSON(t, srv.URL+"/api/state")
	d := j["data"].(map[string]any)
	if d["memories"].(float64) != 2 {
		t.Fatalf("memories 应为 2: %v", d["memories"])
	}
	if _, has := d["deep_pending"]; !has {
		t.Fatalf("deep_pending 字段应在（未配置 llm 时为 0）: %v", d)
	}
	if _, has := d["tick_last"]; !has {
		t.Fatalf("tick_last 字段应在（无留档为 null）: %v", d)
	}
}

// TestUIHistoryEndpoint 趋势数据面（D1 原型确认项）：eval/history.jsonl 读回
func TestUIHistoryEndpoint(t *testing.T) {
	srv, st := newUITest(t)
	j := getJSON(t, srv.URL+"/api/history")
	runs := j["data"].([]any)
	if len(runs) != 0 {
		t.Fatalf("无历史应为空数组: %v", runs)
	}
	if err := eval.AppendHistory(st.Root, eval.HistoryRun{TS: "t1", Mode: "store", Tasks: 2, Hits: 2, Recall: 1.0}); err != nil {
		t.Fatal(err)
	}
	j = getJSON(t, srv.URL+"/api/history")
	runs = j["data"].([]any)
	if len(runs) != 1 {
		t.Fatalf("应读回 1 条: %v", runs)
	}
	r := runs[0].(map[string]any)
	if r["recall"].(float64) != 1.0 || r["mode"] != "store" {
		t.Fatalf("历史内容: %v", r)
	}
}

// TestUIHistoryCorruptFails400 历史坏行经 API 报 400（衰减数据不容静默丢行）
func TestUIHistoryCorruptFails400(t *testing.T) {
	srv, st := newUITest(t)
	os.MkdirAll(filepath.Join(st.Root, "eval"), 0o755)
	os.WriteFile(filepath.Join(st.Root, "eval", "history.jsonl"), []byte("{corrupt\n"), 0o644)
	resp, err := http.Get(srv.URL + "/api/history")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("坏历史应 400: %d", resp.StatusCode)
	}
}

// TestUIStateWithDeepQueueAndTickLast 深挖待办>0 + tick 留档在状态带如实呈现（D2 数据面端到端）
func TestUIStateWithDeepQueueAndTickLast(t *testing.T) {
	srv, st := newUITest(t)
	q := miner.LoadDeepQueue(st.Root)
	if err := q.Append("/tmp/x.jsonl", 1, 9); err != nil {
		t.Fatal(err)
	}
	j := getJSON(t, srv.URL+"/api/state")
	d := j["data"].(map[string]any)
	if d["deep_pending"].(float64) != 1 {
		t.Fatalf("deep_pending 应 1: %v", d["deep_pending"])
	}
	if d["tick_last"] != nil {
		t.Fatalf("未 tick 应无留档: %v", d["tick_last"])
	}
}

// ── 记忆管理面契约 ─────────────────────────────────────────────────────────────

// seedMemory 直接落一条已沉淀记忆（不经 inbox——模拟已 promote 的记忆）
func seedMemory(t *testing.T, st *store.Store, body, typ, status string, expires string) string {
	t.Helper()
	m := &store.Memory{
		Type: typ, Facet: "dev", Status: status,
		CapturedAt: store.NowTime(), ReviewedAt: store.NowTime(), Modified: store.NowTime(),
		Trust: store.TrustHumanVerified, Source: store.SourceAgent,
		Provenance: store.Provenance{Origin: "t", Ref: "t#1", Quote: body},
		Version:    store.FormatVersion, Body: body, Expires: expires,
	}
	m.ID, _ = store.NewMemoryID()
	if err := st.SaveMemory(m); err != nil {
		t.Fatal(err)
	}
	// seed 后立即提交（retire/reactivate 走 WithRoot 脏树检查——不 commit 会 400）
	if _, err := store.GitCommit(st.Root, "test: seed "+m.ID); err != nil {
		t.Fatal(err)
	}
	return m.ID
}

// TestUIMemoriesList /api/memories 列表端点（记忆库浏览数据面）
func TestUIMemoriesList(t *testing.T) {
	srv, st := newUITest(t)
	seedMemory(t, st, "构建走 pnpm", "preference", store.StatusActive, "")
	seedMemory(t, st, "部署先跑迁移", "procedural", store.StatusActive, "")
	seedMemory(t, st, "旧数据库是 Postgres", "semantic", store.StatusSuperseded, "")

	j := getJSON(t, srv.URL+"/api/memories")
	memories := j["data"].(map[string]any)["memories"].([]any)
	if len(memories) != 3 {
		t.Fatalf("应列出全部 3 条: %d", len(memories))
	}

	// 按类型筛选
	j = getJSON(t, srv.URL+"/api/memories?type=preference")
	memories = j["data"].(map[string]any)["memories"].([]any)
	if len(memories) != 1 {
		t.Fatalf("类型筛选应 1 条: %d", len(memories))
	}

	// 按状态筛选
	j = getJSON(t, srv.URL+"/api/memories?status=superseded")
	memories = j["data"].(map[string]any)["memories"].([]any)
	if len(memories) != 1 {
		t.Fatalf("状态筛选应 1 条: %d", len(memories))
	}
}

// TestUIDashboard /api/dashboard 看板数据面（全生命周期计数）
func TestUIDashboard(t *testing.T) {
	srv, st := newUITest(t)
	seedMemory(t, st, "a", "preference", store.StatusActive, "")
	seedMemory(t, st, "b", "procedural", store.StatusActive, "")
	seedMemory(t, st, "c", "semantic", store.StatusSuperseded, "")
	seedMemory(t, st, "d", "episodic", store.StatusExpired, "7d")

	j := getJSON(t, srv.URL+"/api/dashboard")
	d := j["data"].(map[string]any)
	if d["total"].(float64) != 4 {
		t.Fatalf("total 应 4: %v", d["total"])
	}
	byStatus := d["by_status"].(map[string]any)
	if byStatus["active"].(float64) != 2 || byStatus["superseded"].(float64) != 1 || byStatus["expired"].(float64) != 1 {
		t.Fatalf("by_status 应 active=2/superseded=1/expired=1: %v", byStatus)
	}
	byType := d["by_type"].(map[string]any)
	if byType["preference"].(float64) != 1 || byType["procedural"].(float64) != 1 {
		t.Fatalf("by_type: %v", byType)
	}
}

// TestUIMemoriesEmpty 列表空态（[] 而非 null）
func TestUIMemoriesEmpty(t *testing.T) {
	srv, _ := newUITest(t)
	j := getJSON(t, srv.URL+"/api/memories")
	memories := j["data"].(map[string]any)["memories"].([]any)
	if len(memories) != 0 {
		t.Fatalf("空态应为 []: %v", memories)
	}
}

// TestUIMemoryRetireReactivate 退休/重新激活端到端（WithRoot 下 status 迁移 + git 提交）
func TestUIMemoryRetireReactivate(t *testing.T) {
	srv, st := newUITest(t)
	id := seedMemory(t, st, "构建走 pnpm", "preference", store.StatusActive, "")

	// 退休
	j := postJSONBody(t, srv.URL+"/api/memory/retire", map[string]any{"id": id, "reason": "已切换到 turborepo"})
	if j["ok"] != true {
		t.Fatalf("退休应成功: %v", j)
	}
	m, _ := st.GetMemory(id)
	if m.Status != store.StatusExpired {
		t.Fatalf("退休后 status 应 expired: %s", m.Status)
	}

	// 重新激活
	j = postJSONBody(t, srv.URL+"/api/memory/reactivate", map[string]any{"id": id})
	if j["ok"] != true {
		t.Fatalf("重新激活应成功: %v", j)
	}
	m, _ = st.GetMemory(id)
	if m.Status != store.StatusActive {
		t.Fatalf("重新激活后 status 应 active: %s", m.Status)
	}
}

// TestUIMemoryRetireWrongStatusRejects 非active记忆不可退休（前置状态校验）
func TestUIMemoryRetireWrongStatusRejects(t *testing.T) {
	srv, st := newUITest(t)
	id := seedMemory(t, st, "旧事实", "semantic", store.StatusSuperseded, "")
	j := postJSONBody(t, srv.URL+"/api/memory/retire", map[string]any{"id": id})
	if j["ok"] != false {
		t.Fatalf("superseded 不可退休: %v", j)
	}
}

func postJSONBody(t *testing.T, url string, body map[string]any) map[string]any {
	t.Helper()
	data, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", url, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
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

// TestUIRetireUpdatesIndexVersion P1 修复杀灭：退休后 VERSION 推进且新索引不含退休记忆
// （否则 BM25 快照仍含 active 状态——search/MPC 仍可命中退休记忆，违反退出检索承诺）
func TestUIRetireUpdatesIndexVersion(t *testing.T) {
	srv, st := newUITest(t)
	id := seedMemory(t, st, "构建走 pnpm", "preference", store.StatusActive, "")
	v0, _ := st.Version()

	j := postJSONBody(t, srv.URL+"/api/memory/retire", map[string]any{"id": id})
	if j["ok"] != true {
		t.Fatalf("退休应成功: %v", j)
	}
	v1, _ := st.Version()
	if v1 <= v0 {
		t.Fatalf("退休应推进 VERSION: %d → %d", v0, v1)
	}
	// 新版本索引中该记忆的 Status 应为 expired（search.visible() 过滤 active）
	idx, err := index.Ensure(st, v1)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range idx.Docs {
		if d.ID == id && d.Status == store.StatusActive {
			t.Fatalf("退休记忆在新索引中仍标 active（BM25 快照会命中它）: %s", d.ID)
		}
	}
}
