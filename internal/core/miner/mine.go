package miner

import (
	"context"
	"os"
	"path/filepath"

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
	FromQueue  bool // 只处理 Stop hook 入队的 transcript
	Force      bool // 重置游标全量重挖（兼审计）
	DryRun     bool // 只报告不写 inbox
	SkipLocked bool // 锁忙即让路（hook 开场追赶路径：永不阻塞，降级跳过）
}

// Report 挖矿报告
type Report struct {
	Transcripts  int      `json:"transcripts"`
	Candidates   int      `json:"candidates"`
	Batch        string   `json:"batch,omitempty"`
	AutoPromoted int      `json:"auto_promoted"`
	Preview      []string `json:"preview,omitempty"`
	Note         string   `json:"note,omitempty"`
}

// Mine 执行挖矿：发现 → 解析（增量游标）→ 提取 → inbox 批次（→ 快速档 recap 自动生效）。
// ctx 到期后不再开新文件（已处理的照常收尾）——开场追赶的硬预算降级点。
// 变更段（游标/队列/批次落盘）在 WithRoot 互斥下执行；快速档自动提升在锁外
// （promotion 自带互斥，避免嵌套自锁）。
func Mine(ctx context.Context, st *store.Store, cfg *config.Config, opts Options) (*Report, error) {
	rep := &Report{}
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

	// 快速档（FR-GOV-3）：仅 ephemeral recap 自动生效，trust 保持 unverified，永不盖章
	if cfg != nil && cfg.Autonomy == config.AutonomyFast && rep.Batch != "" {
		var recapIDs []string
		cands, _ := inbox.New(st).ListCandidates(rep.Batch)
		for _, c := range cands {
			if c.Expires != "" {
				recapIDs = append(recapIDs, c.ID)
			}
		}
		if len(recapIDs) > 0 {
			res, err := promotion.Promote(st, inbox.New(st), audit.New(st), promotion.Request{CandidateIDs: recapIDs, Auto: true})
			if err == nil {
				rep.AutoPromoted = len(res.MemoryIDs)
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
		files, err = Discover(opts.ClaudeDir)
		if err != nil {
			return nil, err
		}
	}

	var allCands []*inbox.Candidate
	mined := map[string]bool{}
	for _, path := range files {
		if ctx.Err() != nil {
			break // 预算到：剩余留队列，下次追赶
		}
		info, err := os.Stat(path)
		if err != nil {
			continue
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
		events, lines, err := ParseClaudeJSONL(path, fromLine)
		if err != nil {
			continue // 单文件失败不拖垮整批（尽力而为）
		}
		if len(events) > 0 {
			cands := extractor.Extract(events)
			allCands = append(allCands, cands...)
		}
		cursors.Set(path, Cursor{Size: info.Size(), Lines: lines})
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

func firstLine(s string) string {
	if i := len(s); i > 72 {
		s = s[:72]
	}
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return s[:i]
		}
	}
	return s
}
