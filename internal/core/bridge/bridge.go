// Package bridge 飞书 / Notion 笔记桥（路线图：摄取 human-verified + 单向发布回视图）。
// 零 SDK：stdlib 直连官方 REST；密钥只走环境变量（config.yaml 在 git 真源内不落密钥）。
// pull 产物经 importer markdown-dir 通道（human-verified + 幂等指纹）；
// push 是 view 投影的单向快照发布（只创建新页面，永不回写——视图是真源派生物，H1）。
// 平台限制如实处理：分页拉全、块数分批写（Notion 100/批、飞书 50/批）、
// 单篇失败跳过并如实列名（不静默少拉）。
package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// 平台分批上限（官方文档：Notion 块批 100、飞书块批 50）
const (
	notionBatchLimit = 100
	feishuBatchLimit = 50
	pullBlockBudget  = 2000 // 单篇块数预算（防异常大页拖垮整次拉取）
)

// PullResult 拉取结果（Skipped 如实列名——跳过不静默）
type PullResult struct {
	Docs    []PulledDoc
	Skipped []string // 标题列表：拉取失败/无权限的篇目
}

// PulledDoc 一篇拉取的笔记（Title 为页标题；Text 为 markdown 行形态的正文）
type PulledDoc struct {
	Title string
	Text  string
}

var httpClient = &http.Client{Timeout: 60 * time.Second}

func httpJSON(ctx context.Context, method, url string, headers map[string]string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rd)
	if err != nil {
		return err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		tail := string(data)
		if len(tail) > 300 {
			tail = tail[len(tail)-300:]
		}
		return fmt.Errorf("%s %s → HTTP %d: %s", method, url, resp.StatusCode, tail)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("%s %s 响应非 JSON: %w", method, url, err)
	}
	return nil
}

// StageMarkdown 拉取文档落 staging 目录（importer markdown-dir 通道的输入面）。
// 标题非法字符/重复标题显式报错——不静默改名、不静默覆盖。
func StageMarkdown(dir string, docs []PulledDoc) ([]string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var files []string
	for _, d := range docs {
		title := strings.TrimSpace(d.Title)
		if bad, _ := badTitle(title); bad {
			return nil, fmt.Errorf("标题 %q 含路径非法字符（不静默改名，人工处理）", d.Title)
		}
		if seen[title] {
			return nil, fmt.Errorf("标题重复 %q（同名两篇会被静默覆盖——人工改名后再拉）", title)
		}
		seen[title] = true
		p := filepath.Join(dir, title+".md")
		if err := os.WriteFile(p, []byte(d.Text+"\n"), 0o644); err != nil {
			return nil, err
		}
		files = append(files, p)
	}
	return files, nil
}

var titleSanity = regexp.MustCompile(`[\\/:*?"<>|\x00-\x1f]`)

func badTitle(title string) (bool, error) {
	if title == "" || title == "." || title == ".." {
		return true, nil
	}
	return titleSanity.MatchString(title), nil
}

// markdown 行 → 平台块（heading/paragraph/bullet 三形态；与 view 投影的行形态对齐）
func classifyLine(line string) (kind, text string) {
	t := strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(t, "### "):
		return "h3", strings.TrimSpace(t[4:])
	case strings.HasPrefix(t, "## "):
		return "h2", strings.TrimSpace(t[3:])
	case strings.HasPrefix(t, "# "):
		return "h1", strings.TrimSpace(t[2:])
	case strings.HasPrefix(t, "- "), strings.HasPrefix(t, "* "):
		return "bullet", strings.TrimSpace(t[2:])
	default:
		return "p", t
	}
}

func pushBlocks(lines []string) []block {
	var blocks []block
	for _, line := range lines {
		kind, text := classifyLine(line)
		if text == "" {
			continue
		}
		blocks = append(blocks, block{Kind: kind, Text: text})
	}
	return blocks
}

type block struct {
	Kind string // h1|h2|h3|p|bullet
	Text string
}

// chunk 分批（平台块数上限）
func chunk[T any](items []T, size int) [][]T {
	var out [][]T
	for len(items) > size {
		out = append(out, items[:size])
		items = items[size:]
	}
	if len(items) > 0 {
		out = append(out, items)
	}
	return out
}

// ── Notion ───────────────────────────────────────────────────────────────────

const notionDefaultBase = "https://api.notion.com"

// NotionConfig Notion 桥配置（Token 只从 REMIN_NOTION_TOKEN 注入，不落 yaml）
type NotionConfig struct {
	Base         string // 缺省官方；测试注入假端点
	ParentPageID string // pull 的子页来源 / push 的落点父页
	Token        string
}

func (c NotionConfig) base() string {
	if c.Base != "" {
		return c.Base
	}
	return notionDefaultBase
}

func (c NotionConfig) validate() error {
	if c.Token == "" {
		return fmt.Errorf("Notion 桥需设置 REMIN_NOTION_TOKEN（integration token，只走环境变量不落盘）")
	}
	if c.ParentPageID == "" {
		return fmt.Errorf("Notion 桥需 config.yaml bridge.notion.parent_page_id")
	}
	return nil
}

func (c NotionConfig) headers() map[string]string {
	return map[string]string{
		"Authorization":  "Bearer " + c.Token,
		"Notion-Version": "2022-06-28",
		"Content-Type":   "application/json",
	}
}

type notionBlock struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	HasChildren bool   `json:"has_children"`
	ChildPage   struct {
		Title string `json:"title"`
	} `json:"child_page"`
	Heading1 struct {
		RT []notionRT `json:"rich_text"`
	} `json:"heading_1"`
	Heading2 struct {
		RT []notionRT `json:"rich_text"`
	} `json:"heading_2"`
	Heading3 struct {
		RT []notionRT `json:"rich_text"`
	} `json:"heading_3"`
	Paragraph struct {
		RT []notionRT `json:"rich_text"`
	} `json:"paragraph"`
	BulletedListItem struct {
		RT []notionRT `json:"rich_text"`
	} `json:"bulleted_list_item"`
}

type notionBlocks struct {
	Results    []notionBlock `json:"results"`
	HasMore    bool          `json:"has_more"`
	NextCursor string        `json:"next_cursor"`
}

type notionRT struct {
	Text struct {
		Content string `json:"content"`
	} `json:"text"`
}

// notionChildren 分页拉全一个块的子块
func notionChildren(ctx context.Context, cfg NotionConfig, blockID string) ([]notionBlock, error) {
	var all []notionBlock
	cursor := ""
	for {
		url := cfg.base() + "/v1/blocks/" + blockID + "/children?page_size=100"
		if cursor != "" {
			url += "&start_cursor=" + cursor
		}
		var page notionBlocks
		if err := httpJSON(ctx, "GET", url, cfg.headers(), nil, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Results...)
		if !page.HasMore || page.NextCursor == "" {
			return all, nil
		}
		cursor = page.NextCursor
	}
}

// notionBlocksToMarkdown 块→markdown 行（嵌套子弹块缩进一级；超预算截断如实标注）
func notionBlocksToMarkdown(ctx context.Context, cfg NotionConfig, blocks []notionBlock, budget *int) string {
	var sb strings.Builder
	for _, blk := range blocks {
		if *budget <= 0 {
			sb.WriteString("（超出块数预算，其余内容截断——原页为准）\n")
			return strings.TrimSuffix(sb.String(), "\n")
		}
		*budget--
		switch blk.Type {
		case "child_page":
			continue // 子页标题行不进正文（单独成篇）
		case "heading_1", "heading_2", "heading_3", "paragraph", "bulleted_list_item":
			var rt []notionRT
			hash := ""
			switch blk.Type {
			case "heading_1":
				rt, hash = blk.Heading1.RT, "# "
			case "heading_2":
				rt, hash = blk.Heading2.RT, "## "
			case "heading_3":
				rt, hash = blk.Heading3.RT, "### "
			case "paragraph":
				rt = blk.Paragraph.RT
			case "bulleted_list_item":
				rt, hash = blk.BulletedListItem.RT, "- "
			}
			var parts []string
			for _, r := range rt {
				parts = append(parts, r.Text.Content)
			}
			line := strings.Join(parts, "")
			if strings.TrimSpace(line) == "" && !blk.HasChildren {
				continue
			}
			if strings.TrimSpace(line) != "" {
				sb.WriteString(hash + line + "\n")
			}
			if blk.HasChildren { // 嵌套列表/折叠块：递归一级缩进
				kids, err := notionChildren(ctx, cfg, blk.ID)
				if err == nil && len(kids) > 0 {
					nested := notionBlocksToMarkdown(ctx, cfg, kids, budget)
					for _, l := range strings.Split(nested, "\n") {
						if strings.TrimSpace(l) != "" {
							sb.WriteString("  " + l + "\n")
						}
					}
				}
			}
		}
	}
	return strings.TrimSuffix(sb.String(), "\n")
}

// NotionPull 拉父页全部子页正文（分页拉全；单篇失败跳过并如实列名）
func NotionPull(ctx context.Context, cfg NotionConfig) (PullResult, error) {
	res := PullResult{}
	if err := cfg.validate(); err != nil {
		return res, err
	}
	pages, err := notionChildren(ctx, cfg, cfg.ParentPageID)
	if err != nil {
		return res, err
	}
	for _, blk := range pages {
		if blk.Type != "child_page" || blk.ChildPage.Title == "" {
			continue
		}
		content, err := notionChildren(ctx, cfg, blk.ID)
		if err != nil {
			res.Skipped = append(res.Skipped, blk.ChildPage.Title) // 单篇失败不拖垮整批，如实列名
			continue
		}
		budget := pullBlockBudget
		res.Docs = append(res.Docs, PulledDoc{
			Title: blk.ChildPage.Title,
			Text:  notionBlocksToMarkdown(ctx, cfg, content, &budget),
		})
	}
	return res, nil
}

func notionBlockPayload(b block) map[string]any {
	typeMap := map[string]string{"h1": "heading_1", "h2": "heading_2", "h3": "heading_3", "p": "paragraph", "bullet": "bulleted_list_item"}
	key := typeMap[b.Kind]
	return map[string]any{
		"object": "block",
		"type":   key,
		key:      map[string]any{"rich_text": []any{map[string]any{"text": map[string]any{"content": b.Text}}}},
	}
}

// NotionPush 单向发布：在父页下创建新页面（markdown 行→块；块数超限分批追加；
// 不改既有页面）
func NotionPush(ctx context.Context, cfg NotionConfig, title string, lines []string) (string, error) {
	if err := cfg.validate(); err != nil {
		return "", err
	}
	blocks := pushBlocks(lines)
	var first []any
	for _, b := range blocks {
		first = append(first, notionBlockPayload(b))
	}
	payload := map[string]any{
		"parent":     map[string]any{"page_id": cfg.ParentPageID},
		"properties": map[string]any{"title": map[string]any{"title": []any{map[string]any{"text": map[string]any{"content": title}}}}},
	}
	if len(first) > 0 {
		payload["children"] = first[:min(notionBatchLimit, len(first))]
	}
	var resp struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	if err := httpJSON(ctx, "POST", cfg.base()+"/v1/pages", cfg.headers(), payload, &resp); err != nil {
		return "", err
	}
	// 超出创建批上限的块：分批追加（append children 端点）
	if len(first) > notionBatchLimit {
		for _, part := range chunk(first[notionBatchLimit:], notionBatchLimit) {
			if err := httpJSON(ctx, "PATCH", cfg.base()+"/v1/blocks/"+resp.ID+"/children",
				cfg.headers(), map[string]any{"children": part}, nil); err != nil {
				return "", fmt.Errorf("页面已建（%s），追加块失败: %w", resp.URL, err)
			}
		}
	}
	if resp.URL == "" {
		resp.URL = "https://notion.so/" + resp.ID
	}
	return resp.URL, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ── 飞书 ─────────────────────────────────────────────────────────────────────

const feishuDefaultBase = "https://open.feishu.cn"

// FeishuConfig 飞书桥配置（AppID/AppSecret 只从 REMIN_FEISHU_APP_ID/SECRET 注入）
type FeishuConfig struct {
	Base             string // 缺省官方；测试注入假端点
	WikiSpaceID      string // pull 的 wiki 空间
	PushFolderToken  string // push 落点文件夹
	AppID, AppSecret string
}

func (c FeishuConfig) base() string {
	if c.Base != "" {
		return c.Base
	}
	return feishuDefaultBase
}

func (c FeishuConfig) validate(needWiki bool) error {
	if c.AppID == "" || c.AppSecret == "" {
		return fmt.Errorf("飞书桥需设置 REMIN_FEISHU_APP_ID / REMIN_FEISHU_APP_SECRET（只走环境变量不落盘）")
	}
	if needWiki && c.WikiSpaceID == "" {
		return fmt.Errorf("飞书 pull 需 config.yaml bridge.feishu.wiki_space_id")
	}
	if !needWiki && c.PushFolderToken == "" {
		return fmt.Errorf("飞书 push 需 config.yaml bridge.feishu.push_folder_token")
	}
	return nil
}

type feishuClient struct {
	base, token string
}

func newFeishuClient(ctx context.Context, cfg FeishuConfig) (*feishuClient, error) {
	var tok struct {
		Code  int    `json:"code"`
		Token string `json:"tenant_access_token"`
		Msg   string `json:"msg"`
	}
	if err := httpJSON(ctx, "POST", cfg.base()+"/open-apis/auth/v3/tenant_access_token/internal",
		map[string]string{"Content-Type": "application/json"},
		map[string]string{"app_id": cfg.AppID, "app_secret": cfg.AppSecret}, &tok); err != nil {
		return nil, err
	}
	if tok.Code != 0 || tok.Token == "" {
		return nil, fmt.Errorf("飞书 tenant_access_token 获取失败: code=%d msg=%s", tok.Code, tok.Msg)
	}
	return &feishuClient{base: cfg.base(), token: tok.Token}, nil
}

func (c *feishuClient) call(ctx context.Context, method, path string, body, out any) error {
	return httpJSON(ctx, method, c.base+path, map[string]string{
		"Authorization": "Bearer " + c.token,
		"Content-Type":  "application/json",
	}, body, out)
}

type feishuNode struct {
	ObjToken string `json:"obj_token"`
	ObjType  string `json:"obj_type"`
	Title    string `json:"title"`
	HasChild bool   `json:"has_child"`
}

// feishuNodes 分页拉全一层 wiki 节点（parentToken 空 = 根层）
func (cl *feishuClient) feishuNodes(ctx context.Context, spaceID, parentToken string) ([]feishuNode, error) {
	var all []feishuNode
	pageToken := ""
	for {
		path := "/open-apis/wiki/v2/spaces/" + spaceID + "/nodes?page_size=50"
		if parentToken != "" {
			path += "&parent_node_token=" + parentToken
		}
		if pageToken != "" {
			path += "&page_token=" + pageToken
		}
		var nodes struct {
			Code int `json:"code"`
			Data struct {
				Items     []feishuNode `json:"items"`
				PageToken string       `json:"page_token"`
				HasMore   bool         `json:"has_more"`
			} `json:"data"`
		}
		if err := cl.call(ctx, "GET", path, nil, &nodes); err != nil {
			return nil, err
		}
		if nodes.Code != 0 {
			return nil, fmt.Errorf("飞书 wiki 节点拉取失败: code=%d", nodes.Code)
		}
		all = append(all, nodes.Data.Items...)
		if !nodes.Data.HasMore || nodes.Data.PageToken == "" {
			return all, nil
		}
		pageToken = nodes.Data.PageToken
	}
}

// FeishuPull 拉 wiki 空间全部 docx 文档正文（树遍历 + 分页；单篇失败跳过列名）
func FeishuPull(ctx context.Context, cfg FeishuConfig) (PullResult, error) {
	res := PullResult{}
	if err := cfg.validate(true); err != nil {
		return res, err
	}
	cl, err := newFeishuClient(ctx, cfg)
	if err != nil {
		return res, err
	}
	var walk func(parentToken string, depth int) error
	walk = func(parentToken string, depth int) error {
		if depth > 6 {
			return nil // 深度护栏（异常深树防失控；根层为 0）
		}
		nodes, err := cl.feishuNodes(ctx, cfg.WikiSpaceID, parentToken)
		if err != nil {
			return err
		}
		for _, n := range nodes {
			if n.ObjType == "docx" && n.Title != "" {
				var raw struct {
					Code int    `json:"code"`
					Data string `json:"data"`
				}
				if err := cl.call(ctx, "GET", "/open-apis/docx/v1/documents/"+n.ObjToken+"/raw_content", nil, &raw); err != nil || raw.Code != 0 {
					res.Skipped = append(res.Skipped, n.Title) // 无权限/已删除/传输失败：跳过列名不拖垮整批
					continue
				}
				res.Docs = append(res.Docs, PulledDoc{Title: n.Title, Text: raw.Data})
			}
			if n.HasChild {
				if err := walk(n.ObjToken, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk("", 0); err != nil {
		return res, err
	}
	return res, nil
}

// FeishuPush 单向发布：新建文档并分批写入块（heading1=3/2=paragraph/12=bullet；
// 返回 文档ID——租户域名不可知，如实标注而非伪造 URL）
func FeishuPush(ctx context.Context, cfg FeishuConfig, title string, lines []string) (string, error) {
	if err := cfg.validate(false); err != nil {
		return "", err
	}
	cl, err := newFeishuClient(ctx, cfg)
	if err != nil {
		return "", err
	}
	var created struct {
		Code int `json:"code"`
		Data struct {
			Document struct {
				DocumentID string `json:"document_id"`
			} `json:"document"`
		} `json:"data"`
	}
	if err := cl.call(ctx, "POST", "/open-apis/docx/v1/documents",
		map[string]any{"title": title, "folder_token": cfg.PushFolderToken}, &created); err != nil {
		return "", err
	}
	if created.Code != 0 || created.Data.Document.DocumentID == "" {
		return "", fmt.Errorf("飞书文档创建失败: code=%d", created.Code)
	}
	docID := created.Data.Document.DocumentID

	var children []any
	for _, b := range pushBlocks(lines) {
		blockType := 2 // paragraph
		switch b.Kind {
		case "h1":
			blockType = 3
		case "h2":
			blockType = 4
		case "h3":
			blockType = 5
		case "bullet":
			blockType = 12
		}
		key := map[int]string{2: "paragraph", 3: "heading1", 4: "heading2", 5: "heading3", 12: "bullet"}[blockType]
		children = append(children, map[string]any{
			"block_type": blockType,
			key:          map[string]any{"elements": []any{map[string]any{"text_run": map[string]any{"content": b.Text}}}},
		})
	}
	for i, part := range chunk(children, feishuBatchLimit) {
		var resp struct {
			Code int    `json:"code"`
			Msg  string `json:"msg"`
		}
		if err := cl.call(ctx, "POST", "/open-apis/docx/v1/documents/"+docID+"/blocks/"+docID+"/children",
			map[string]any{"children": part, "index": i * feishuBatchLimit}, &resp); err != nil {
			return "", fmt.Errorf("文档已建（%s），块写入失败: %w", docID, err)
		}
		if resp.Code != 0 {
			return "", fmt.Errorf("飞书块写入失败: code=%d msg=%s（文档 %s 已建）", resp.Code, resp.Msg, docID)
		}
	}
	return docID, nil
}
