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
	"strings"

	"github.com/remin-dev/remin/internal/core/audit"
	"github.com/remin-dev/remin/internal/core/eval"
	"github.com/remin-dev/remin/internal/core/inbox"
	"github.com/remin-dev/remin/internal/core/miner"
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
		stateView := map[string]any{
			"root":         st.Root,
			"version":      v,
			"batches":      out,
			"deep_pending": len(miner.LoadDeepQueue(st.Root).All()),
			"tick_last":    miner.LoadTickLast(st.Root),
		}
		// 记忆数失败时缺省（前端显示 …）——错误时给 0 会误导
		if ms, err := st.ListMemories(); err == nil {
			stateView["memories"] = len(ms)
		}
		writeJSON(w, stateView)
	})
	mux.HandleFunc("GET /api/history", func(w http.ResponseWriter, r *http.Request) {
		runs, err := eval.LoadHistory(st.Root)
		if err != nil {
			writeErr(w, err)
			return
		}
		if runs == nil {
			runs = []eval.HistoryRun{} // 列表契约：空为 [] 而非 null
		}
		writeJSON(w, runs)
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
		// 投影为 snake_case 视图：inbox.Candidate 内嵌 store.Memory（Go 字段名无 json
		// tag），直出会让前端读到不存在的 snake_case 键（v0 单条按钮因此静默失效）
		type provView struct {
			Origin string `json:"origin,omitempty"`
			Ref    string `json:"ref,omitempty"`
			Quote  string `json:"quote,omitempty"`
		}
		type candView struct {
			ID         string   `json:"id"`
			Type       string   `json:"type"`
			Trust      string   `json:"trust"`
			Body       string   `json:"body"`
			CapturedAt string   `json:"captured_at"`
			Expires    string   `json:"expires,omitempty"`
			Provenance provView `json:"provenance"`
		}
		out := make([]candView, 0, len(cands))
		for _, c := range cands {
			out = append(out, candView{
				ID: c.ID, Type: c.Type, Trust: c.Trust, Body: c.Body,
				CapturedAt: c.CapturedAt, Expires: c.Expires,
				Provenance: provView{Origin: c.Provenance.Origin, Ref: c.Provenance.Ref, Quote: c.Provenance.Quote},
			})
		}
		writeJSON(w, map[string]any{"id": id, "candidates": out})
	})
	mux.HandleFunc("POST /api/promote", reviewAction(st, func(in *inbox.Inbox, au *audit.Audit, ids []string) (any, error) {
		return promotion.Promote(st, in, au, promotion.Request{CandidateIDs: ids})
	}))
	mux.HandleFunc("POST /api/reject", reviewAction(st, func(in *inbox.Inbox, au *audit.Audit, ids []string) (any, error) {
		res, err := promotion.Reject(st, in, au, ids, "")
		if err != nil {
			return nil, err
		}
		// 与 CLI --json 同形（rejected/commit）；promotion.Result 无 json tag 直出会是 Go 字段名
		return map[string]any{"rejected": ids, "commit": res.Commit}, nil
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
		// 键名按语义：supersedes=本条替代的旧链；superseded_by=替代本条的新链
		writeJSON(w, map[string]any{"memory": m, "supersedes": older, "superseded_by": newer})
	})
	mux.HandleFunc("GET /api/memories", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		ms, err := st.ListMemories()
		if err != nil {
			writeErr(w, err)
			return
		}
		type memView struct {
			ID         string `json:"id"`
			Type       string `json:"type"`
			Facet      string `json:"facet"`
			Status     string `json:"status"`
			Trust      string `json:"trust"`
			Body       string `json:"body"`
			CapturedAt string `json:"captured_at"`
			Expires    string `json:"expires,omitempty"`
			Origin     string `json:"origin"`
			Verify     string `json:"verify,omitempty"`
		}
		out := []memView{} // 列表契约：空为 []
		ftype, fstatus, ftrust := q.Get("type"), q.Get("status"), q.Get("trust")
		fq := q.Get("q")
		for _, m := range ms {
			if ftype != "" && m.Type != ftype {
				continue
			}
			if fstatus != "" && m.Status != fstatus {
				continue
			}
			if ftrust != "" && m.Trust != ftrust {
				continue
			}
			if fq != "" && !strings.Contains(m.Body, fq) {
				continue
			}
			v := memView{
				ID: m.ID, Type: m.Type, Facet: m.Facet, Status: m.Status, Trust: m.Trust,
				Body: m.Body, CapturedAt: m.CapturedAt, Expires: m.Expires,
				Origin: m.Provenance.Origin,
			}
			if m.Verify != nil {
				v.Verify = m.Verify.Result
			}
			out = append(out, v)
		}
		writeJSON(w, map[string]any{"memories": out})
	})
	mux.HandleFunc("GET /api/dashboard", func(w http.ResponseWriter, r *http.Request) {
		ms, err := st.ListMemories()
		if err != nil {
			writeErr(w, err)
			return
		}
		byStatus, byType, byTrust := map[string]int{}, map[string]int{}, map[string]int{}
		verifyFailed := 0
		for _, m := range ms {
			byStatus[m.Status]++
			byType[m.Type]++
			byTrust[m.Trust]++
			if m.Verify != nil && m.Verify.Result == store.VerifyFailed {
				verifyFailed++
			}
		}
		writeJSON(w, map[string]any{
			"total":         len(ms),
			"by_status":     byStatus,
			"by_type":       byType,
			"by_trust":      byTrust,
			"verify_failed": verifyFailed,
		})
	})
	// 退休/重新激活（写操作，走三层防线 + WithRoot + VERSION 推进 + 索引重建）
	mux.HandleFunc("POST /api/memory/retire", memoryAction(st, func(id, reason string) (any, error) {
		if err := lifecycleTransition(st, id, reason, false); err != nil {
			return nil, err
		}
		return map[string]any{"id": id, "status": store.StatusExpired}, nil
	}))
	mux.HandleFunc("POST /api/memory/reactivate", memoryAction(st, func(id, reason string) (any, error) {
		if err := lifecycleTransition(st, id, reason, true); err != nil {
			return nil, err
		}
		return map[string]any{"id": id, "status": store.StatusActive}, nil
	}))
	// propose（「提出新版本」按钮 → 与 CLI propose --supersedes 同语义）
	mux.HandleFunc("POST /api/propose", func(w http.ResponseWriter, r *http.Request) {
		if !loopbackHost(r.Host) {
			writeErr(w, fmt.Errorf("写操作只接受本机回环 Host"))
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && !loopbackOrigin(origin) {
			writeErr(w, fmt.Errorf("写操作只接受本机来源"))
			return
		}
		mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mt != "application/json" {
			writeErr(w, fmt.Errorf("写操作要求 Content-Type: application/json"))
			return
		}
		var req struct {
			Body       string `json:"body"`
			Type       string `json:"type"`
			Supersedes string `json:"supersedes"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil || req.Body == "" {
			writeErr(w, fmt.Errorf("请求体须为 {body, type, supersedes}"))
			return
		}
		mtype := req.Type
		if mtype == "" {
			mtype = store.TypeSemantic
		}
		if !store.ValidTypes[mtype] {
			writeErr(w, fmt.Errorf("非法 type: %s", mtype))
			return
		}
		facet := "dev"
		if req.Supersedes != "" {
			old, err := st.GetMemory(req.Supersedes)
			if err != nil {
				writeErr(w, fmt.Errorf("supersedes 指定的记忆不存在: %s", req.Supersedes))
				return
			}
			if old.Status != store.StatusActive {
				writeErr(w, fmt.Errorf("supersedes 指定的记忆状态为 %s（仅 active 可被替代）", old.Status))
				return
			}
			if old.Facet != "" {
				facet = old.Facet // 继承旧记忆 facet——否则替代品落在 dev，work 记忆替换后从原 facet 视图消失
			}
		}
		now := store.NowTime()
		cand := &inbox.Candidate{}
		cand.Type = mtype
		cand.Facet = facet
		cand.Status = store.StatusCandidate
		cand.CapturedAt = now
		cand.ReviewedAt = store.TimeUnknown
		cand.Modified = now
		cand.Trust = store.TrustHumanVerified
		cand.Source = store.SourceHuman
		cand.Provenance = store.Provenance{Origin: "human-ui", Ref: "remin ui 提出新版本", Quote: req.Body}
		cand.Version = store.FormatVersion
		cand.Body = req.Body
		cand.Supersedes = req.Supersedes
		var batchID string
		var ids []string
		err = store.WithRoot(st.Root, func() error {
			var e error
			batchID, ids, e = inbox.New(st).AddBatch("manual", []*inbox.Candidate{cand})
			return e
		})
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, map[string]any{"batch": batchID, "candidate": ids[0]})
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
// memoryAction 记忆管理写动作共用管线：三层防线（Host/Origin/JSON）→ WithRoot 互斥 → 核心调用
func memoryAction(st *store.Store, call func(id, reason string) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !loopbackHost(r.Host) {
			writeErr(w, fmt.Errorf("写操作只接受本机回环 Host"))
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && !loopbackOrigin(origin) {
			writeErr(w, fmt.Errorf("写操作只接受本机来源"))
			return
		}
		mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mt != "application/json" {
			writeErr(w, fmt.Errorf("写操作要求 Content-Type: application/json"))
			return
		}
		var req struct {
			ID     string `json:"id"`
			Reason string `json:"reason"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil || req.ID == "" {
			writeErr(w, fmt.Errorf("请求体须为 {id, reason}"))
			return
		}
		var result any
		err = store.WithRoot(st.Root, func() error {
			var e error
			result, e = call(req.ID, req.Reason)
			return e
		})
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, result)
	}
}

// lifecycleTransition 生命周期事务（持锁内调用）：状态迁移 → VERSION 推进 →
// 索引重建 → git 提交（改变检索真值必须推版本——否则 BM25 快照仍含旧状态，
// 退休的记忆在 search/MCP 里仍然可见，违反「退休=退出检索」承诺）
func lifecycleTransition(st *store.Store, id, reason string, reactivate bool) error {
	var err error
	if reactivate {
		err = st.Reactivate(id, reason)
	} else {
		err = st.Retire(id, reason)
	}
	if err != nil {
		return err
	}
	v, err := st.Version()
	if err != nil {
		return err
	}
	if err := st.WriteVersion(v + 1); err != nil {
		return err
	}
	v++
	// 索引重建（加速层，落盘失败不致命——版本号已推进，检索会触发 Ensure 重建）
	if ms, le := st.ListMemories(); le == nil {
		_ = index.Build(v, ms).Persist(st.Root)
	}
	action := "retire"
	if reactivate {
		action = "reactivate"
	}
	if _, err := store.GitCommit(st.Root, fmt.Sprintf("%s: %s%s", action, id, commitNote(reason))); err != nil {
		return err
	}
	return nil
}

func truncateUI(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func commitNote(reason string) string {
	if reason == "" {
		return ""
	}
	tr := []rune(reason)
	if len(tr) > 80 {
		tr = tr[:80]
	}
	return "（" + string(tr) + "）"
}

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
