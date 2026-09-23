package miner

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/remin-dev/remin/internal/core/audit"
	"github.com/remin-dev/remin/internal/core/config"
	"github.com/remin-dev/remin/internal/core/extractor"
	"github.com/remin-dev/remin/internal/core/inbox"
	"github.com/remin-dev/remin/internal/core/promotion"
	"github.com/remin-dev/remin/internal/store"
)

// DefaultClaudeDir Claude Code 会话日志根
func DefaultClaudeDir() string {
	if env := os.Getenv("REMIN_CLAUDE_DIR"); env != "" {
		return env
	}
	return filepath.Join(store.HomeDir(), ".claude", "projects")
}

// Options 挖矿选项
type Options struct {
	ClaudeDir  string
	ExtraRoots []string // ClaudeDir 之外的发现根（codex/dsh 会话目录、自定义根；跨根去重）
	FromQueue  bool     // 只处理 Stop hook 入队的 transcript
	Force      bool     // 重置游标全量重挖（兼审计）
	DryRun     bool     // 只报告不写 inbox
	SkipLocked bool     // 锁忙即让路（hook 开场追赶路径：永不阻塞，降级跳过）
	Deep       bool     // 深度提取路径（手动 mine 专用；需 config llm 节，hook 路径永不触发）
	SinceDays  int      // 首挖限量：仅挖 mtime 近 N 天的 transcript（0=不限；发现路径专用——queue/force 不受限）
}

// isDeepEcho 文件是否为深提取会话回声（含哨兵标记；64KB 内探测——prompt 在会话头部）
func isDeepEcho(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 64*1024)
	n, _ := io.ReadFull(f, buf)
	return bytes.Contains(buf[:n], []byte(extractor.DeepEchoSentinel))
}

// cfgLLMOf nil 安全取 cfg.LLM
func cfgLLMOf(cfg *config.Config) *config.LLMConfig {
	if cfg == nil {
		return nil
	}
	return cfg.LLM
}

// Report 挖矿报告
type Report struct {
	Transcripts  int      `json:"transcripts"`
	Candidates   int      `json:"candidates"`
	Batch        string   `json:"batch,omitempty"`
	AutoPromoted int      `json:"auto_promoted"`
	SkippedOld   int      `json:"skipped_old,omitempty"` // 因 --since 窗口跳过的老 transcript 数
	Preview      []string `json:"preview,omitempty"`
	Note         string   `json:"note,omitempty"`
}

// Mine 执行挖矿：发现 → 解析（增量游标）→ 提取 → inbox 批次（→ 快速档 recap 自动生效）。
// ctx 到期后不再开新文件（已处理的照常收尾）——开场追赶的硬预算降级点。
// 变更段（游标/队列/批次落盘）在 WithRoot 互斥下执行；快速档自动提升在锁外
// （promotion 自带互斥，避免嵌套自锁）。
func Mine(ctx context.Context, st *store.Store, cfg *config.Config, opts Options) (*Report, error) {
	rep := &Report{}
	// --deep 无可用引擎即失败前置（锁外快速失败，用户显式要求过深路径，不静默降级）。
	// 引擎解析序（用户定义）：本机 agent headless 第一优先级（数据不产生新外流面、
	// 复用已有订阅额度），手动 llm 端点第二。静态不可用不允许静默降级（评审 P2 同判）。
	if opts.Deep && extractor.ResolveDeepEngine(cfgLLMOf(cfg)).Kind == extractor.DeepEngineNone {
		return nil, fmt.Errorf("--deep 需要本机 agent CLI（claude/codex 已装并登录）或 config.yaml 配 llm 节且设 REMIN_LLM_API_KEY——其一即可")
	}
	mine := func() error {
		r, err := mineLocked(ctx, st, cfg, opts)
		if err != nil {
			return err
		}
		*rep = *r
		return nil
	}
	var err error
	if opts.SkipLocked {
		var acquired bool
		acquired, err = store.TryWithRoot(st.Root, mine)
		if err == nil && !acquired {
			rep.Note = "跳过：真源正被其他操作占用（hook 路径不等待；下次追赶续挖）"
		}
	} else {
		err = store.WithRoot(st.Root, mine)
	}
	if err != nil {
		return nil, err
	}

	// 快速档（FR-GOV-3）：仅 ephemeral recap 自动生效，trust 保持 unverified，永不盖章。
	// hook 追赶路径（SkipLocked）下自动提升同样 try-lock：锁忙则留批次待下次追赶——
	// 「inject 永不阻塞」硬约束覆盖本分支。
	if cfg != nil && cfg.Autonomy == config.AutonomyFast && rep.Batch != "" {
		var recapIDs []string
		cands, _ := inbox.New(st).ListCandidates(rep.Batch)
		for _, c := range cands {
			if c.Expires != "" {
				recapIDs = append(recapIDs, c.ID)
			}
		}
		if len(recapIDs) > 0 {
			promote := func() (int, error) {
				res, err := promotion.Promote(st, inbox.New(st), audit.New(st), promotion.Request{CandidateIDs: recapIDs, Auto: true})
				if err != nil {
					return 0, err
				}
				return len(res.MemoryIDs), nil
			}
			var n int
			var err error
			if opts.SkipLocked {
				var acquired bool
				acquired, err = store.TryWithRoot(st.Root, func() error {
					var e error
					n, e = promote()
					return e
				})
				if err == nil && !acquired {
					rep.Note = "快速档自动提升跳过：真源被占用（下次追赶续办）"
				}
			} else {
				n, err = promote()
			}
			if err == nil {
				rep.AutoPromoted = n
			}
		}
	}
	return rep, nil
}

func mineLocked(ctx context.Context, st *store.Store, cfg *config.Config, opts Options) (*Report, error) {
	if opts.ClaudeDir == "" {
		opts.ClaudeDir = DefaultClaudeDir()
	}
	rep := &Report{}
	cursors, err := LoadCursors(st.Root)
	if err != nil {
		return nil, err
	}

	var files []string
	if opts.FromQueue {
		q := LoadQueue(st.Root)
		for _, it := range q.All() {
			if _, err := os.Stat(it.Path); err == nil {
				files = append(files, it.Path)
			}
		}
	} else {
		files = discoverAll(append([]string{opts.ClaudeDir}, opts.ExtraRoots...)...)
	}

	var allCands []*inbox.Candidate
	mined := map[string]bool{}
	// 首挖限量窗口（发现路径专用：queue 是刚结束的会话、force 是显式审计，均不过滤）
	var cutoff time.Time
	if opts.SinceDays > 0 && !opts.FromQueue && !opts.Force {
		cutoff = time.Now().AddDate(0, 0, -opts.SinceDays)
	}
	for _, path := range files {
		if ctx.Err() != nil {
			break // 预算到：剩余留队列，下次追赶
		}
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		// 提取会话回灌跳过：agent 深提取的一次性会话日志含哨兵标记与源文本回声，
		// 落在发现根内会被再次挖到——整文件跳过防无限回灌
		if isDeepEcho(path) {
			continue
		}
		if !cutoff.IsZero() && info.ModTime().Before(cutoff) {
			rep.SkippedOld++
			continue // 老历史跳过（--full-history / --force 显式全量）
		}
		fromLine := 1
		if !opts.Force {
			if cur, ok := cursors.Get(path); ok && !cursors.NeedMine(path, info.Size()) {
				mined[path] = true // 无增量也视为已处理：出队，防队列泄漏
				continue
			} else if ok && cur.Lines > 0 && info.Size() >= cur.Size {
				fromLine = cur.Lines + 1 // 断点续挖
			}
		}
		events, lines, err := ParseTranscript(path, fromLine)
		if err != nil {
			continue // 单文件失败不拖垮整批（尽力而为）
		}
		if len(events) > 0 {
			cands := extractor.Extract(events)
			// 深路径（增量召回）：快速路径结果永远保留，深路径只补不替；
			// 单 transcript 失败即弃权（Note 披露），不拖垮整批。
			if opts.Deep {
				deep, derr := extractor.ExtractDeepAuto(ctx, cfgLLMOf(cfg), events)
				if derr != nil {
					rep.Note = strings.TrimSpace(strings.TrimSpace(rep.Note) + " 深度提取弃权（" + firstLine(derr.Error()) + "）")
				} else {
					seenBodies := make(map[string]bool, len(cands))
					for _, c := range cands {
						seenBodies[c.Body] = true
					}
					for _, d := range deep {
						if !seenBodies[d.Body] {
							cands = append(cands, d)
							seenBodies[d.Body] = true
						}
					}
				}
			}
			allCands = append(allCands, cands...)
		}
		cursors.Set(path, Cursor{Size: info.Size(), Lines: lines})
		// deep 待挖队列挂账：快速路径挖过的新行段留给闲时 tick 深挖（端点已配才入队，
		// 密钥后置到排空时校验）；--deep 已深挖的范围出队（按实际深挖区间 [fromLine,lines]——
		// 增量深挖不断点前的待挖段，防未深挖段被静默丢弃）
		if !opts.DryRun {
			dq := LoadDeepQueue(st.Root)
			if len(events) > 0 && !opts.Deep && extractor.DeepEngineAvailable(cfgLLMOf(cfg)) {
				_ = dq.RemoveCovered(path, fromLine, lines) // 旧段被新段完全覆盖时去重（force 重挖防双重计费）
				_ = dq.Append(path, fromLine, lines)
			}
			if opts.Deep {
				_ = dq.RemoveCovered(path, fromLine, lines)
			}
		}
		mined[path] = true
		rep.Transcripts++
	}

	// 队列模式：处理完成即出队
	if opts.FromQueue {
		_ = LoadQueue(st.Root).RemovePaths(mined)
	}

	rep.Candidates = len(allCands)
	if len(allCands) == 0 {
		if !opts.DryRun {
			_ = cursors.Save()
		}
		return rep, nil
	}
	for _, c := range allCands {
		rep.Preview = append(rep.Preview, firstLine(c.Body))
	}
	if opts.DryRun {
		return rep, nil
	}
	if err := cursors.Save(); err != nil {
		return nil, err
	}
	in := inbox.New(st)
	batch, _, err := in.AddBatch("mine", allCands)
	if err != nil {
		return nil, err
	}
	rep.Batch = batch
	return rep, nil
}

// Drain 排空队列（开场追赶用；尊重 ctx 预算，超时即停，剩余留给下次）
func Drain(ctx context.Context, st *store.Store, cfg *config.Config, claudeDir string) (int, error) {
	if len(LoadQueue(st.Root).All()) == 0 {
		return 0, nil
	}
	rep, err := Mine(ctx, st, cfg, Options{FromQueue: true, ClaudeDir: claudeDir, SkipLocked: true})
	if err != nil {
		return 0, err // 追赶失败上报；inject 侧降级
	}
	return rep.Transcripts, nil
}

// firstLine 单行预览：先截行再限长（按 rune，防切中文出断尾 UTF-8）
func firstLine(s string) string {
	if i := strings.Index(s, "\n"); i >= 0 {
		s = s[:i]
	}
	if r := []rune(s); len(r) > 72 {
		return string(r[:72])
	}
	return s
}
