package bridge

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 桥契约：pull 分页拉全 + 单篇失败跳过列名（不静默）；push 块数分批
// （Notion 100/飞书 50 平台上限）；未配置/未设密钥显式报错；重名标题不静默覆盖。

func ts(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	return s
}

// ── Notion pull ──────────────────────────────────────────────────────────────

func TestNotionPull(t *testing.T) {
	var gotAuth string
	var paths []string
	srv := ts(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/v1/blocks/parent1/children"):
			io.WriteString(w, `{"results":[
				{"id":"p1","type":"child_page","child_page":{"title":"部署手册"}},
				{"id":"p2","type":"child_page","child_page":{"title":"团队约定"}}
			],"has_more":false}`)
		case strings.HasSuffix(r.URL.Path, "/v1/blocks/p1/children"):
			io.WriteString(w, `{"results":[
				{"id":"h1","type":"heading_1","has_children":false,"heading_1":{"rich_text":[{"text":{"content":"发布流程"}}]}},
				{"type":"paragraph","has_children":false,"paragraph":{"rich_text":[{"text":{"content":"先跑迁移再发布"}}]}},
				{"id":"b1","type":"bulleted_list_item","has_children":true,"bulleted_list_item":{"rich_text":[{"text":{"content":"回滚预案必须在场"}}]}}
			],"has_more":false}`)
		case strings.HasSuffix(r.URL.Path, "/v1/blocks/b1/children"):
			io.WriteString(w, `{"results":[{"type":"bulleted_list_item","has_children":false,"bulleted_list_item":{"rich_text":[{"text":{"content":"备库先行"}}]}}],"has_more":false}`)
		case strings.HasSuffix(r.URL.Path, "/v1/blocks/p2/children"):
			io.WriteString(w, `{"results":[{"type":"paragraph","has_children":false,"paragraph":{"rich_text":[{"text":{"content":"周会周三十点"}}]}}],"has_more":false}`)
		default:
			http.Error(w, "unexpected "+r.URL.Path, 404)
		}
	}))
	res, err := NotionPull(context.Background(), NotionConfig{Base: srv.URL, ParentPageID: "parent1", Token: "secret_t"})
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer secret_t" || !strings.Contains(strings.Join(paths, ";"), "/v1/blocks/parent1/children") {
		t.Fatalf("授权头/路径: %q %v", gotAuth, paths)
	}
	if len(res.Docs) != 2 || res.Docs[0].Title != "部署手册" || len(res.Skipped) != 0 {
		t.Fatalf("应拉 2 篇无跳过: %+v", res)
	}
	want := "# 发布流程\n先跑迁移再发布\n- 回滚预案必须在场\n  - 备库先行"
	if res.Docs[0].Text != want {
		t.Fatalf("块→markdown 行（含嵌套缩进）: %q", res.Docs[0].Text)
	}
}

// TestNotionPullPagination 分页拉全（has_more+next_cursor 两页）
func TestNotionPullPagination(t *testing.T) {
	srv := ts(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.RawQuery, "start_cursor=c2") {
			io.WriteString(w, `{"results":[{"id":"p2","type":"child_page","child_page":{"title":"第二页篇"}}],"has_more":false}`)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/v1/blocks/p2/children") {
			io.WriteString(w, `{"results":[{"type":"paragraph","paragraph":{"rich_text":[{"text":{"content":"内容"}}]}}],"has_more":false}`)
			return
		}
		io.WriteString(w, `{"results":[{"id":"p1","type":"child_page","child_page":{"title":"第一页篇"}}],"has_more":true,"next_cursor":"c2"}`)
	}))
	res, err := NotionPull(context.Background(), NotionConfig{Base: srv.URL, ParentPageID: "parent", Token: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Docs) != 2 {
		t.Fatalf("分页应拉全 2 篇: %+v", res)
	}
}

// TestNotionPullSkipsFailedChild 单篇子页失败：跳过并如实列名（不拖垮整批不静默）
func TestNotionPullSkipsFailedChild(t *testing.T) {
	srv := ts(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/v1/blocks/pBad/children") {
			http.Error(w, "forbidden", 403)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/v1/blocks/pOK/children") {
			io.WriteString(w, `{"results":[{"type":"paragraph","paragraph":{"rich_text":[{"text":{"content":"好"}}]}}],"has_more":false}`)
			return
		}
		io.WriteString(w, `{"results":[
			{"id":"pBad","type":"child_page","child_page":{"title":"无权限篇"}},
			{"id":"pOK","type":"child_page","child_page":{"title":"正常篇"}}
		],"has_more":false}`)
	}))
	res, err := NotionPull(context.Background(), NotionConfig{Base: srv.URL, ParentPageID: "parent", Token: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Docs) != 1 || len(res.Skipped) != 1 || res.Skipped[0] != "无权限篇" {
		t.Fatalf("失败篇应跳过列名: %+v", res)
	}
}

func TestNotionPullUnconfigured(t *testing.T) {
	if _, err := NotionPull(context.Background(), NotionConfig{}); err == nil || !strings.Contains(err.Error(), "REMIN_NOTION_TOKEN") {
		t.Fatalf("未配置应显式报错: %v", err)
	}
}

// ── Notion push ──────────────────────────────────────────────────────────────

func TestNotionPush(t *testing.T) {
	var body map[string]any
	srv := ts(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		json.Unmarshal(data, &body)
		if r.Header.Get("Notion-Version") == "" {
			http.Error(w, "missing version", 400)
			return
		}
		io.WriteString(w, `{"id":"newpage1","url":"https://notion.so/newpage1"}`)
	}))
	url, err := NotionPush(context.Background(), NotionConfig{Base: srv.URL, ParentPageID: "parent1", Token: "t"}, "记忆投影",
		[]string{"# preference", "- (human-verified) 用中文回复", "正文段落"})
	if err != nil {
		t.Fatal(err)
	}
	if url != "https://notion.so/newpage1" {
		t.Fatalf("应返回页面 URL: %s", url)
	}
	parent := body["parent"].(map[string]any)
	if parent["page_id"] != "parent1" {
		t.Fatalf("应挂父页: %+v", body)
	}
	children := body["children"].([]any)
	if len(children) != 3 {
		t.Fatalf("三行三块: %+v", children)
	}
	b0 := children[0].(map[string]any)
	if b0["object"] != "block" || b0["type"] != "heading_1" {
		t.Fatalf("标题行映射: %+v", b0)
	}
	if _, has := b0["has_children"]; has {
		t.Fatalf("只读字段 has_children 不得出现在创建载荷")
	}
	if children[1].(map[string]any)["type"] != "bulleted_list_item" {
		t.Fatalf("列表行映射: %+v", children[1])
	}
	if children[2].(map[string]any)["type"] != "paragraph" {
		t.Fatalf("正文行映射: %+v", children[2])
	}
}

// TestNotionPushChunksOverLimit 超过 100 块：创建批 ≤100，余量走 append 分批
// （审查 P1 修复的杀灭测试——平台 children 上限 100）
func TestNotionPushChunksOverLimit(t *testing.T) {
	var createChildren int
	var appends []int
	srv := ts(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		var body map[string]any
		json.Unmarshal(data, &body)
		switch {
		case strings.HasSuffix(r.URL.Path, "/v1/pages"):
			createChildren = len(body["children"].([]any))
			io.WriteString(w, `{"id":"newp","url":"https://notion.so/newp"}`)
		case strings.HasSuffix(r.URL.Path, "/children") && r.Method == "PATCH":
			appends = append(appends, len(body["children"].([]any)))
			io.WriteString(w, `{}`)
		default:
			http.Error(w, "unexpected", 404)
		}
	}))
	lines := make([]string, 250)
	for i := range lines {
		lines[i] = "行内容之" + string(rune('A'+i%26))
	}
	if _, err := NotionPush(context.Background(), NotionConfig{Base: srv.URL, ParentPageID: "p", Token: "t"}, "t", lines); err != nil {
		t.Fatal(err)
	}
	if createChildren != 100 {
		t.Fatalf("创建批应恰好 100: %d", createChildren)
	}
	if len(appends) != 2 || appends[0] != 100 || appends[1] != 50 {
		t.Fatalf("余量 150 应按上限分 100+50 两批: %v", appends)
	}
}

// ── Feishu pull / push ───────────────────────────────────────────────────────

func TestFeishuPullAndPush(t *testing.T) {
	var tokenCalls int
	var nodeQueries []string
	var pushBatches []int
	var firstBatch []any
	srv := ts(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/open-apis/auth/v3/tenant_access_token/internal"):
			tokenCalls++
			io.WriteString(w, `{"code":0,"tenant_access_token":"ttok"}`)
		case strings.Contains(r.URL.Path, "/wiki/v2/spaces/sp1/nodes"):
			if r.Header.Get("Authorization") != "Bearer ttok" {
				http.Error(w, "bad token", 401)
				return
			}
			nodeQueries = append(nodeQueries, r.URL.RawQuery)
			if strings.Contains(r.URL.RawQuery, "parent_node_token=doc1") {
				io.WriteString(w, `{"code":0,"data":{"items":[{"obj_token":"doc1c","obj_type":"docx","title":"子节点文档"}],"has_more":false}}`)
				return
			}
			if strings.Contains(r.URL.RawQuery, "page_token=pg2") {
				io.WriteString(w, `{"code":0,"data":{"items":[{"obj_token":"doc3","obj_type":"docx","title":"第二页文档"}],"has_more":false}}`)
				return
			}
			io.WriteString(w, `{"code":0,"data":{"items":[
				{"obj_token":"doc1","obj_type":"docx","title":"部署手册","has_child":true},
				{"obj_token":"doc2","obj_type":"bitable","title":"非文档略过"}
			],"has_more":true,"page_token":"pg2"}}`)
		case strings.Contains(r.URL.Path, "/docx/v1/documents/doc1/raw_content"):
			io.WriteString(w, `{"code":0,"data":"先跑迁移再发布\n回滚预案必须在场"}`)
		case strings.Contains(r.URL.Path, "/docx/v1/documents/doc1c/raw_content"):
			io.WriteString(w, `{"code":0,"data":"子节点内容"}`)
		case strings.Contains(r.URL.Path, "/docx/v1/documents/doc3/raw_content"):
			io.WriteString(w, `{"code":2,"data":""}`) // 无权限：跳过列名
		case strings.HasSuffix(r.URL.Path, "/open-apis/docx/v1/documents") && r.Method == "POST":
			io.WriteString(w, `{"code":0,"data":{"document":{"document_id":"newdoc1","title":"记忆投影"}}}`)
		case strings.Contains(r.URL.Path, "/blocks/newdoc1/children"):
			data, _ := io.ReadAll(r.Body)
			var body map[string]any
			json.Unmarshal(data, &body)
			n := len(body["children"].([]any))
			pushBatches = append(pushBatches, n)
			if firstBatch == nil {
				firstBatch = body["children"].([]any)
			}
			io.WriteString(w, `{"code":0,"data":{}}`)
		default:
			http.Error(w, "unexpected "+r.URL.Path, 404)
		}
	}))
	cfg := FeishuConfig{Base: srv.URL, WikiSpaceID: "sp1", PushFolderToken: "fold1", AppID: "ai", AppSecret: "as"}

	res, err := FeishuPull(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if tokenCalls != 1 {
		t.Fatalf("token 应取一次: %d", tokenCalls)
	}
	// 树遍历（doc1 子层）+ 分页（第二页）+ 非 docx 略过 + 无权限跳过列名
	if len(res.Docs) != 2 || res.Docs[0].Title != "部署手册" || res.Docs[1].Title != "子节点文档" {
		t.Fatalf("树+分页应拉 doc1/子节点两篇: %+v", res)
	}
	if len(res.Skipped) != 1 || res.Skipped[0] != "第二页文档" {
		t.Fatalf("无权限篇应跳过列名: %+v", res.Skipped)
	}
	joined := strings.Join(nodeQueries, " | ")
	if !strings.Contains(joined, "parent_node_token=doc1") || !strings.Contains(joined, "page_token=pg2") {
		t.Fatalf("应有树遍历与分页查询: %s", joined)
	}

	// push：120 行 → 50/50/20 三批（平台上限 50）
	lines := make([]string, 120)
	for i := range lines {
		lines[i] = "内容行" + string(rune('A'+i%26))
	}
	id, err := FeishuPush(context.Background(), cfg, "记忆投影", lines)
	if err != nil {
		t.Fatal(err)
	}
	if id != "newdoc1" {
		t.Fatalf("应返回文档 ID: %s", id)
	}
	if len(pushBatches) != 3 || pushBatches[0] != 50 || pushBatches[1] != 50 || pushBatches[2] != 20 {
		t.Fatalf("应按 50 分三批: %v", pushBatches)
	}
	b0 := firstBatch[0].(map[string]any)
	if b0["block_type"] != float64(2) {
		t.Fatalf("段落块类型: %+v", b0)
	}
}

func TestFeishuUnconfigured(t *testing.T) {
	if _, err := FeishuPull(context.Background(), FeishuConfig{}); err == nil || !strings.Contains(err.Error(), "REMIN_FEISHU_APP_ID") {
		t.Fatalf("未配置应显式报错: %v", err)
	}
}

// ── staging ──────────────────────────────────────────────────────────────────

func TestStageMarkdown(t *testing.T) {
	dir := t.TempDir()
	files, err := StageMarkdown(dir, []PulledDoc{
		{Title: "部署手册", Text: "# 发布\n先跑迁移"},
		{Title: "团队约定", Text: "周会周三"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("应落 2 文件: %v", files)
	}
	data, err := os.ReadFile(filepath.Join(dir, "部署手册.md"))
	if err != nil || !strings.Contains(string(data), "先跑迁移") {
		t.Fatalf("staging 内容: %v %s", err, data)
	}
	// 非法路径字符
	if _, err := StageMarkdown(dir, []PulledDoc{{Title: "a/b:c", Text: "x"}}); err == nil {
		t.Fatal("非法标题应报错（不静默改名）")
	}
	// 重复标题：不静默覆盖（审查 P2-4 杀灭测试）
	if _, err := StageMarkdown(dir, []PulledDoc{
		{Title: "同名", Text: "第一篇"},
		{Title: "同名", Text: "第二篇"},
	}); err == nil || !strings.Contains(err.Error(), "重复") {
		t.Fatalf("重复标题应报错: %v", err)
	}
}
