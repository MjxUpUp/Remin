// Package eval 评测 harness（FR-EVL）：规则可判定、模型无关。
// 宪法四件套（parity / provenance / conflict / roundtrip）+ trust 可信五指标 + budget。
package eval

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/remin-dev/remin/internal/core/audit"
	"github.com/remin-dev/remin/internal/core/exporter"
	"github.com/remin-dev/remin/internal/core/inbox"
	"github.com/remin-dev/remin/internal/core/inject"
	"github.com/remin-dev/remin/internal/core/promotion"
	"github.com/remin-dev/remin/internal/core/verify"
	"github.com/remin-dev/remin/internal/index"
	"github.com/remin-dev/remin/internal/search"
	"github.com/remin-dev/remin/internal/store"
)

// Check 单项断言
type Check struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail"`
}

// Report 套件报告
type Report struct {
	Suite    string  `json:"suite"`
	Passed   bool    `json:"passed"`
	Checks   []Check `json:"checks"`
	Duration string  `json:"duration"`
	Notes    string  `json:"notes,omitempty"`
}

func run(name string, f func() []Check) *Report {
	start := time.Now()
	checks := f()
	rep := &Report{Suite: name, Checks: checks, Passed: true}
	for _, c := range checks {
		if !c.Passed {
			rep.Passed = false
		}
	}
	rep.Duration = time.Since(start).String()
	return rep
}

// provenanceMissing 三要素任一缺失即计为无来源（A1：无来源不落库）
func provenanceMissing(ms []*store.Memory) int {
	missing := 0
	for _, m := range ms {
		if m.Provenance.Origin == "" || m.Provenance.Ref == "" || m.Provenance.Quote == "" {
			missing++
		}
	}
	return missing
}

func check(name string, passed bool, detail string) Check {
	return Check{Name: name, Passed: passed, Detail: detail}
}

// newEvalStore 沙盒真源（产品内自建 fixture；调用方负责 RemoveAll 清理）
func newEvalStore() (*store.Store, error) {
	// git 身份：全局配置优先，缺失时用沙盒身份（评测可归因到 eval）
	if _, err := store.GitHasIdentity("."); err != nil {
		os.Setenv("GIT_AUTHOR_NAME", "remin-eval")
		os.Setenv("GIT_AUTHOR_EMAIL", "eval@remin.local")
		os.Setenv("GIT_COMMITTER_NAME", "remin-eval")
		os.Setenv("GIT_COMMITTER_EMAIL", "eval@remin.local")
	}
	dir, err := os.MkdirTemp("", "remin-eval-")
	if err != nil {
		return nil, err
	}
	return store.Init(dir)
}

func mkCand(body, supersedes string) *inbox.Candidate {
	c := &inbox.Candidate{}
	c.Type = store.TypeSemantic
	c.Facet = "dev"
	c.Status = store.StatusCandidate
	c.CapturedAt = store.NowTime()
	c.ReviewedAt = store.TimeUnknown
	c.Modified = store.NowTime()
	c.Trust = store.TrustUnverified
	c.Source = store.SourceAgent
	c.Provenance = store.Provenance{Origin: "claude-code", Ref: "session#ev, line 1", Quote: body}
	c.Version = store.FormatVersion
	c.Supersedes = supersedes
	c.Body = body
	return c
}

func promoteOne(st *store.Store, in *inbox.Inbox, au *audit.Audit, body, supersedes string, mutate func(*inbox.Candidate)) (string, error) {
	c := mkCand(body, supersedes)
	if mutate != nil {
		mutate(c)
	}
	_, ids, err := in.AddBatch("mine", []*inbox.Candidate{c})
	if err != nil {
		return "", err
	}
	res, err := promotion.Promote(st, in, au, promotion.Request{CandidateIDs: ids})
	if err != nil {
		return "", err
	}
	return res.MemoryIDs[0], nil
}

func searcher(st *store.Store) (*search.Searcher, error) {
	v, err := st.Version()
	if err != nil {
		return nil, err
	}
	idx, err := index.Ensure(st, v)
	if err != nil {
		return nil, err
	}
	return search.New(idx), nil
}

// buildFixture 评测夹具：正常/被替代旧+新/verify 失败/ephemeral 过期记忆
func buildFixture() (*store.Store, map[string]string, error) {
	st, err := newEvalStore()
	if err != nil {
		return nil, nil, err
	}
	in := inbox.New(st)
	au := audit.New(st)
	ids := map[string]string{}

	if ids["active"], err = promoteOne(st, in, au, "用户主力语言是 Rust", "", nil); err != nil {
		return nil, nil, err
	}
	if ids["supOld"], err = promoteOne(st, in, au, "球队主力前锋是 Ronaldo", "", nil); err != nil {
		return nil, nil, err
	}
	if ids["supNew"], err = promoteOne(st, in, au, "球队主力前锋已改为 Messi", ids["supOld"], nil); err != nil {
		return nil, nil, err
	}
	if ids["vfail"], err = promoteOne(st, in, au, "登录走 OAuth（旧描述）", "", func(c *inbox.Candidate) {
		c.Verify = &store.Verify{Condition: "path-exists:/definitely/not/exists", Result: store.VerifyUnknown}
	}); err != nil {
		return nil, nil, err
	}
	if _, err := verify.Run(st, au, ids["vfail"], ""); err != nil {
		return nil, nil, err
	}
	if ids["expired"], err = promoteOne(st, in, au, "上次会话的交接内容", "", func(c *inbox.Candidate) {
		c.Expires = "7d"
	}); err != nil {
		return nil, nil, err
	}
	// 把 ephemeral 的时间改到 30 天前（用过期时间替换 reviewed_at，使其自然过期）
	m, err := st.GetMemory(ids["expired"])
	if err == nil {
		past := time.Now().AddDate(0, 0, -30).Format("2006-01-02T15:04:05-07:00")
		m.ReviewedAt = past
		m.CapturedAt = past
		if st.SaveMemory(m) == nil {
			_, _ = store.GitCommit(st.Root, "fixture: ephemeral 过期时间回写")
		}
		// 刷新当前版本的持久化索引快照（否则检索仍按 promote 时的 fresh 时间判定）
		if v, verr := st.Version(); verr == nil {
			if ms, lerr := st.ListMemories(); lerr == nil {
				_ = index.Build(v, ms).Persist(st.Root)
			}
		}
	}
	return st, ids, nil
}

// SuiteTrust 可信四指标 + 分层随行 + 注入双通道 stale=0
func SuiteTrust() *Report {
	return run("trust", func() []Check {
		st, ids, err := buildFixture()
		if st != nil {
			defer os.RemoveAll(st.Root)
		}
		if err != nil {
			return []Check{check("fixture", false, err.Error())}
		}
		s, err := searcher(st)
		if err != nil {
			return []Check{check("index", false, err.Error())}
		}
		var checks []Check

		// 1. stale 注入率 = 0：过期/被替代/验证失败不可见
		r := s.Search("Ronaldo OAuth 会话 交接", search.Options{TopK: 50})
		bad := map[string]bool{ids["supOld"]: true, ids["vfail"]: true, ids["expired"]: true}
		var stale []string
		for _, h := range r.Hits {
			if bad[h.ID] {
				stale = append(stale, h.ID)
			}
		}
		checks = append(checks, check("stale_injection_zero", len(stale) == 0, fmt.Sprintf("stale=%v", stale)))

		// 2. provenance 覆盖率 = 100%（无来源不落库）
		ms, _ := st.ListMemories()
		missing := provenanceMissing(ms)
		checks = append(checks, check("provenance_coverage_100", missing == 0,
			fmt.Sprintf("missing=%d/%d", missing, len(ms))))

		// 3. 弃权正确：垃圾查询必 abstain（显式字段，非空数组）
		g := s.Search("zzxxqq 完全无关词", search.Options{})
		checks = append(checks, check("abstain_on_garbage", g.Abstained, g.Reason))

		// 4. trust 分层随行（agent 必须知道自己吃到的是哪级记忆）——精确断言值，非仅非空
		rA := s.Search("Rust 主力语言", search.Options{})
		carried := false
		detail := "无命中"
		if len(rA.Hits) > 0 {
			h := rA.Hits[0]
			want, ok := ids["active"]
			carried = ok && h.ID == want && h.Trust == store.TrustHumanVerified &&
				h.Provenance.Origin == "claude-code" && h.Provenance.Ref != ""
			detail = fmt.Sprintf("id=%s trust=%s（应为 human-verified）", h.ID, h.Trust)
		}
		checks = append(checks, check("trust_and_provenance_carried", carried, detail))

		// 5. 注入通道同样排除 stale（双通道一致）
		inj, err := inject.Run(st, inject.Options{Facet: "dev"})
		if err != nil {
			checks = append(checks, check("inject_stale_zero", false, err.Error()))
		} else {
			var leak []string
			for badID := range bad {
				if strings.Contains(inj.Text, badID) {
					leak = append(leak, badID)
				}
			}
			checks = append(checks, check("inject_stale_zero", len(leak) == 0, fmt.Sprintf("leak=%v", leak)))
		}
		return checks
	})
}

// SuiteConflict 冲突回归（Ronaldo→Messi 事实变更集）：supersession 后旧事实注入率 = 0
func SuiteConflict() *Report {
	return run("conflict", func() []Check {
		st, ids, err := buildFixture()
		if st != nil {
			defer os.RemoveAll(st.Root)
		}
		if err != nil {
			return []Check{check("fixture", false, err.Error())}
		}
		s, err := searcher(st)
		if err != nil {
			return []Check{check("index", false, err.Error())}
		}
		oldLeak := false
		for _, h := range s.Search("Ronaldo 主力前锋", search.Options{TopK: 10}).Hits {
			if h.ID == ids["supOld"] {
				oldLeak = true
			}
		}
		newSeen := false
		for _, h := range s.Search("Messi 主力前锋", search.Options{TopK: 10}).Hits {
			if h.ID == ids["supNew"] {
				newSeen = true
			}
		}
		// 旧条目状态与链完整性
		old, _ := st.GetMemory(ids["supOld"])
		chainOK := old != nil && old.Status == store.StatusSuperseded && old.SupersededBy == ids["supNew"]
		// 注入通道同样不得泄漏旧事实
		inj, err := inject.Run(st, inject.Options{Facet: "dev"})
		injectLeak := err != nil || strings.Contains(inj.Text, ids["supOld"])
		return []Check{
			check("old_fact_injection_zero", !oldLeak && newSeen, fmt.Sprintf("oldLeak=%v newSeen=%v", oldLeak, newSeen)),
			check("supersession_chain_intact", chainOK, fmt.Sprintf("old=%+v", old)),
			check("inject_old_fact_zero", !injectLeak, "注入通道旧事实泄漏"),
		}
	})
}

// SuiteRoundtrip export → restore → export 三点哈希一致
func SuiteRoundtrip() *Report {
	return run("roundtrip", func() []Check {
		st, err := newEvalStore()
		if err != nil {
			return []Check{check("fixture", false, err.Error())}
		}
		defer os.RemoveAll(st.Root)
		in := inbox.New(st)
		au := audit.New(st)
		if _, err := promoteOne(st, in, au, "roundtrip 记忆 A", "", nil); err != nil {
			return []Check{check("fixture", false, err.Error())}
		}
		// 自定义 config（facets/autonomy/bindings/remote）——restore 必须完整带回（H4）
		cfgPath := st.ConfigPath()
		cfgData := []byte("facets: [dev, work, life, custom-face]\nautonomy: fast\ninject_facet: work\ntool_bindings:\n  claude-code: dev\nsync_remote: git@example:u/mem.git\n")
		if err := os.WriteFile(cfgPath, cfgData, 0o644); err != nil {
			return []Check{check("fixture_config", false, err.Error())}
		}
		if _, err := store.GitCommit(st.Root, "fixture: 自定义 config"); err != nil {
			return []Check{check("fixture_config", false, err.Error())}
		}
		out1, _ := os.MkdirTemp("", "remin-eval-exp1-")
		defer os.RemoveAll(out1)
		m1, err := exporter.Export(st, out1)
		if err != nil {
			return []Check{check("export", false, err.Error())}
		}
		st2, err := newEvalStore()
		if err != nil {
			return []Check{check("fixture2", false, err.Error())}
		}
		defer os.RemoveAll(st2.Root)
		if err := exporter.Restore(st2, out1); err != nil {
			return []Check{check("restore", false, err.Error())}
		}
		out2, _ := os.MkdirTemp("", "remin-eval-exp2-")
		defer os.RemoveAll(out2)
		m2, err := exporter.Export(st2, out2)
		if err != nil {
			return []Check{check("reexport", false, err.Error())}
		}
		equal := len(m1.Files) == len(m2.Files)
		if equal {
			for rel, h := range m1.Files {
				if m2.Files[rel] != h {
					equal = false
					break
				}
			}
		}
		restoredCfg, rerr := os.ReadFile(st2.ConfigPath())
		cfgPreserved := rerr == nil && string(restoredCfg) == string(cfgData)
		return []Check{
			check("roundtrip_hash_equal", equal, fmt.Sprintf("%d files", len(m1.Files))),
			check("roundtrip_config_preserved", cfgPreserved,
				fmt.Sprintf("restore 后 config.yaml %s", map[bool]string{true: "完整保留", false: "丢失/被改写"}[cfgPreserved])),
		}
	})
}

// SuiteParity 内容对等（H5，架构 §9）：CLI inject 产物与 MCP memory_search 结果
// 内容对等——两个通道都真实实跑：inject 走 Injector，另一侧经 stdio JSON-RPC
// 拉起真实 MCP server 调 memory_search（不允许用同进程引擎调用偷换通道）。
func SuiteParity(binPath string) *Report {
	return run("parity", func() []Check {
		if binPath == "" {
			return []Check{check("mcp_binary", false, "未提供 remin 二进制路径，无法实跑 MCP 通道")}
		}
		st, _, err := buildFixture()
		if st != nil {
			defer os.RemoveAll(st.Root)
		}
		if err != nil {
			return []Check{check("fixture", false, err.Error())}
		}
		inj, err := inject.Run(st, inject.Options{Facet: "dev"})
		if err != nil {
			return []Check{check("inject", false, err.Error())}
		}
		idRe := regexp.MustCompile(`^(mem_[A-Z0-9]{26}) ·`)
		var injected []string
		for _, line := range strings.Split(inj.Text, "\n") {
			if m := idRe.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
				injected = append(injected, m[1])
			}
		}

		// MCP 通道：真实 stdio server
		client, err := startMCPStdio(binPath, st.Root)
		if err != nil {
			return []Check{check("mcp_start", false, err.Error())}
		}
		defer client.Close()

		// 逐条注入记忆经 memory_search 取回：每条 inject id 必须可被 MCP 检索到
		bodyOf := map[string]string{}
		ms, _ := st.ListMemories()
		for _, m := range ms {
			bodyOf[m.ID] = m.Body
		}
		notRetrievable := []string{}
		retrieved := map[string]bool{}
		mcpVersion := -1
		for _, id := range injected {
			res, err := client.memorySearch(bodyOf[id], 3)
			if err != nil {
				return []Check{check("mcp_search", false, err.Error())}
			}
			if res.IndexVersion > mcpVersion {
				mcpVersion = res.IndexVersion
			}
			found := false
			for _, h := range res.Results {
				retrieved[h.ID] = true
				if h.ID == id {
					found = true
				}
			}
			if !found {
				notRetrievable = append(notRetrievable, id)
			}
		}
		return []Check{
			check("inject_ids_all_visible", len(notRetrievable) == 0,
				fmt.Sprintf("MCP 检索不到的注入记忆=%v", notRetrievable)),
			check("mcp_serves_nothing_hidden", len(retrieved) <= len(injected),
				fmt.Sprintf("MCP 可检索 %d / 注入 %d（MCP 不得多给不可见记忆）", len(retrieved), len(injected))),
			check("channels_agree_on_index_version", mcpVersion == inj.Version,
				fmt.Sprintf("MCP index_version=%d / inject v%d", mcpVersion, inj.Version)),
		}
	})
}

// SuiteBudget 性能预算：SessionStart 注入含追赶 ≤ 800ms（硬预算示例值）
func SuiteBudget() *Report {
	return run("budget", func() []Check {
		st, _, err := buildFixture()
		if st != nil {
			defer os.RemoveAll(st.Root)
		}
		if err != nil {
			return []Check{check("fixture", false, err.Error())}
		}
		start := time.Now()
		inj, err := inject.Run(st, inject.Options{Facet: "dev"})
		d := time.Since(start)
		if err != nil {
			return []Check{check("inject", false, err.Error())}
		}
		return []Check{
			check("inject_under_800ms", d < 800*time.Millisecond, d.String()),
			check("inject_lines_within_budget", inj.Lines <= inject.DefaultMaxLines,
				fmt.Sprintf("lines=%d", inj.Lines)),
		}
	})
}

// Run 运行套件：trust | roundtrip | parity | conflict | budget | all。
// binPath 为 remin 二进制路径（parity 套件实跑 MCP stdio 通道必需；空则该套件如实失败）。
func Run(suite string, binPath string) ([]*Report, error) {
	suites := []string{suite}
	if suite == "all" || suite == "" {
		suites = []string{"trust", "roundtrip", "parity", "conflict", "budget"}
	}
	var reps []*Report
	for _, s := range suites {
		switch s {
		case "trust", "roundtrip", "parity", "conflict", "budget":
			runners := map[string]func() *Report{
				"trust": SuiteTrust, "roundtrip": SuiteRoundtrip,
				"parity":   func() *Report { return SuiteParity(binPath) },
				"conflict": SuiteConflict, "budget": SuiteBudget,
			}
			reps = append(reps, runners[s]())
		default:
			return nil, fmt.Errorf("未知套件 %s（可选: trust/roundtrip/parity/conflict/budget/all）", s)
		}
	}
	return reps, nil
}
