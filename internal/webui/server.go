// Package webui 本地审收 Web 界面（remin ui）：包裹核心包（inbox/promotion/search/store），
// 是 CLI --json 之上的皮肤——不引入新核心能力（架构文档预留）。仅绑 127.0.0.1；
// 写操作三层防线：回环 Host 校验（防 DNS rebinding）+ Origin 校验 + JSON-only
// （跨站表单无法伪造 JSON POST，无 CORS）。
package webui

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"

	"github.com/remin-dev/remin/internal/core/audit"
	"github.com/remin-dev/remin/internal/core/inbox"
	"github.com/remin-dev/remin/internal/core/promotion"
	"github.com/remin-dev/remin/internal/index"
	"github.com/remin-dev/remin/internal/search"
	"github.com/remin-dev/remin/internal/store"
)

//go:embed index.html
var assets embed.FS

// Handler 构建全部路由（st 由调用方解析真源根）
func Handler(st *store.Store) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		data, _ := assets.ReadFile("index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	}))
	mux.HandleFunc("GET /api/state", func(w http.ResponseWriter, r *http.Request) {
		in := inbox.New(st)
		batches, err := in.ListBatches()
		if err != nil {
			writeErr(w, err)
			return
		}
		type batchView struct {
			ID      string         `json:"id"`
			Source  string         `json:"source"`
			Created string         `json:"created_at"`
			Pending int            `json:"pending"`
			Status  string         `json:"status"`
			Types   map[string]int `json:"types"`
		}
		var out []batchView
		for _, b := range batches {
			bv := batchView{ID: b.ID, Source: b.Source, Created: b.CreatedAt, Pending: len(b.Candidates), Status: b.Status, Types: map[string]int{}}
			if b.Status != "done" {
				cands, _ := in.ListCandidates(b.ID)
				for _, c := range cands {
					bv.Types[c.Type]++
				}
			}
			out = append(out, bv)
		}
		v, _ := st.Version()
		writeJSON(w, map[string]any{"root": st.Root, "version": v, "batches": out})
	})
	mux.HandleFunc("GET /api/batch", func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("id")
		if id == "" {
			writeErr(w, fmt.Errorf("缺少 id"))
			return
		}
		cands, err := inbox.New(st).ListCandidates(id)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, map[string]any{"id": id, "candidates": cands})
	})
	mux.HandleFunc("POST /api/promote", reviewAction(st, func(in *inbox.Inbox, au *audit.Audit, ids []string) (any, error) {
		return promotion.Promote(st, in, au, promotion.Request{CandidateIDs: ids})
	}))
	mux.HandleFunc("POST /api/reject", reviewAction(st, func(in *inbox.Inbox, au *audit.Audit, ids []string) (any, error) {
		return promotion.Reject(st, in, au, ids, "")
	}))
	mux.HandleFunc("GET /api/search", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		if q == "" {
			writeErr(w, fmt.Errorf("缺少 q"))
			return
		}
		v, err := st.Version()
		if err != nil {
			writeErr(w, err)
			return
		}
		idx, err := index.Ensure(st, v)
		if err != nil {
			writeErr(w, err)
			return
		}
		res := search.New(idx).Search(q, search.Options{TopK: 8})
		writeJSON(w, res)
	})
	mux.HandleFunc("GET /api/memory", func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("id")
		if id == "" {
			writeErr(w, fmt.Errorf("缺少 id"))
			return
		}
		m, err := st.GetMemory(id)
		if err != nil {
			writeErr(w, err)
			return
		}
		older, newer, _ := st.SupersessionChain(id)
		writeJSON(w, map[string]any{"memory": m, "superseded_by": older, "supersedes": newer})
	})
	return mux
}

// reviewAction 审收写动作的共用管线：本地来源防线 → JSON-only 防线 → 选择器解析 → 核心调用
func reviewAction(st *store.Store, call func(*inbox.Inbox, *audit.Audit, []string) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 防线一（DNS rebinding）：写操作 Host 必须是本机回环字面量——rebinding 域名
		// 解析到 127.0.0.1 后浏览器视为同源可直发 JSON，只有 Host 校验能拦
		if !loopbackHost(r.Host) {
			writeErr(w, fmt.Errorf("写操作只接受本机回环 Host"))
			return
		}
		// 防线二（Origin 附加校验）：在场时必须是本机来源
		if origin := r.Header.Get("Origin"); origin != "" && !loopbackOrigin(origin) {
			writeErr(w, fmt.Errorf("写操作只接受本机来源"))
			return
		}
		// 防线三（CSRF）：只收 application/json（HTML 表单无法跨站发 JSON POST；
		// fetch 跨站 JSON 会触发 CORS 预检而本服务无 CORS 头）；容忍 charset=utf-8 后缀
		mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mt != "application/json" {
			writeErr(w, fmt.Errorf("写操作要求 Content-Type: application/json"))
			return
		}
		var sel inbox.Selector
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&sel); err != nil {
			writeErr(w, fmt.Errorf("请求体须为 JSON 选择器 {batch,all,ids,except,type}"))
			return
		}
		in := inbox.New(st)
		ids, err := in.ResolveIDs(sel, "候选")
		if err != nil {
			writeErr(w, err)
			return
		}
		res, err := call(in, audit.New(st), ids)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, res)
	}
}

// loopbackHost Host 头必须是回环字面量（含可选端口）
func loopbackHost(host string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}

// loopbackOrigin Origin 在场时必须是回环 http 来源
func loopbackOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && loopbackHost(u.Host)
}

func writeJSON(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": data})
}

func writeErr(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()})
}
