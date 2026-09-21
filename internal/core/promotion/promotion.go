// Package promotion 人审采纳的原子提交事务（P3：promote 是唯一盖章入口）。
// 一次 promote = 单个 git commit：新记忆 + supersession 双向指针 + index/VERSION +1
// + audit 追加 + inbox 台账更新；任一步失败整体回滚（含暂存区）。
package promotion

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/remin-dev/remin/internal/core/audit"
	"github.com/remin-dev/remin/internal/core/inbox"
	"github.com/remin-dev/remin/internal/index"
	"github.com/remin-dev/remin/internal/store"
)

// Atomic 事务原语：先对 paths 做内容快照，执行 change；change 失败时恢复原内容并清空暂存区。
// 返回的回滚函数供 git 提交失败时调用（幂等：仅在未被调用过时执行一次）。
// 成功后由调用方提交 git commit。
func Atomic(root string, paths []string, change func() error) (undo func(), err error) {
	snap := make(map[string][]byte, len(paths))
	existed := make(map[string]bool, len(paths))
	for _, p := range paths {
		data, rerr := os.ReadFile(p)
		snap[p] = data
		existed[p] = rerr == nil
	}
	undo = func() {
		rollback(root, snap, existed)
		snap = nil
	}
	defer func() {
		if err != nil && snap != nil {
			undo()
		}
	}()
	return undo, change()
}

func rollback(root string, snap map[string][]byte, existed map[string]bool) {
	for p, data := range snap {
		if existed[p] {
			_ = os.MkdirAll(filepath.Dir(p), 0o755)
			_ = os.WriteFile(p, data, 0o644)
		} else {
			_ = os.Remove(p)
		}
	}
	_ = store.GitResetStaged(root)
}

// Request 采纳请求
type Request struct {
	CandidateIDs []string // inbox 候选 id
	Auto         bool     // 快速档 ephemeral recap 自动生效：保留原 trust，不盖章 human-verified
	Note         string
}

// Result 采纳结果
type Result struct {
	Version    int
	Commit     string
	MemoryIDs  []string
	Superseded []string // 被替代退出检索的旧 id
}

// Promote 人审采纳（或快速档自动采纳）：单一原子提交
func Promote(st *store.Store, in *inbox.Inbox, au *audit.Audit, req Request) (*Result, error) {
	if len(req.CandidateIDs) == 0 {
		return nil, fmt.Errorf("未指定候选（--id 或 --batch ... --all）")
	}
	actor, err := store.GitHasIdentity(st.Root)
	if err != nil {
		return nil, err
	}

	// 阶段一：加载与校验（不改任何文件）
	cands := make([]*inbox.Candidate, 0, len(req.CandidateIDs))
	for _, cid := range req.CandidateIDs {
		c, err := in.GetCandidate(cid)
		if err != nil {
			return nil, err
		}
		if err := c.Validate(); err != nil {
			return nil, fmt.Errorf("候选 %s: %w", cid, err)
		}
		cands = append(cands, c)
	}

	// 分配新记忆 id（查当前与全 git 历史，永不复用）
	newIDs := make([]string, len(cands))
	for i := range cands {
		id, err := store.NewMemoryID()
		if err != nil {
			return nil, err
		}
		if st.MemoryExists(id) {
			return nil, fmt.Errorf("id 冲突（ULID 碰撞，请重试）")
		}
		used, _ := store.GitIDEverUsed(st.Root, id)
		if used {
			return nil, fmt.Errorf("id %s 曾在历史中使用，永不复用", id)
		}
		newIDs[i] = id
	}

	// supersession 预校验：目标存在、仍 active、未被替代；同批次不得重复替代同一旧条
	supersededOld := map[string]string{} // oldID -> newID
	for i, c := range cands {
		if c.Supersedes == "" {
			continue
		}
		old, err := st.GetMemory(c.Supersedes)
		if err != nil {
			return nil, fmt.Errorf("候选 %s 的 supersedes 目标不存在: %s", c.ID, c.Supersedes)
		}
		if old.Status != store.StatusActive {
			return nil, fmt.Errorf("候选 %s 想替代的 %s 已是 %s（不可再次替代）", c.ID, old.ID, old.Status)
		}
		if old.SupersededBy != "" {
			return nil, fmt.Errorf("%s 已被 %s 替代（同一旧事实只能被替代一次）", old.ID, old.SupersededBy)
		}
		if prev, dup := supersededOld[old.ID]; dup {
			return nil, fmt.Errorf("本批次中 %s 与 %s 都想替代 %s", prev, newIDs[i], old.ID)
		}
		supersededOld[old.ID] = newIDs[i]
	}

	now := store.NowTime()

	// 阶段二：收集事务涉及的全部路径（checkpoint 用）
	var touched []string
	for i, c := range cands {
		touched = append(touched, st.MemoryPath(c.Type, newIDs[i]))
		if c.Supersedes != "" {
			if old, err := st.GetMemory(c.Supersedes); err == nil {
				touched = append(touched, st.MemoryPath(old.Type, old.ID))
			}
		}
	}
	touched = append(touched, filepath.Join(st.Root, "index", "VERSION"))
	touched = append(touched, filepath.Join(st.Root, "audit", "promotions.jsonl"))
	batches := map[string][]string{}
	for _, c := range cands {
		batches[c.Batch] = append(batches[c.Batch], c.ID)
		touched = append(touched, filepath.Join(st.Root, "inbox", "batches", c.Batch+".json"))
		touched = append(touched, filepath.Join(st.Root, "inbox", "candidates", c.ID+".md"))
	}

	res := &Result{}
	action := audit.ActionPromote
	if req.Auto {
		action = audit.ActionAutoPromote
	}

	undo, err := Atomic(st.Root, touched, func() error {
		// 写新记忆
		for i, c := range cands {
			m := c.Memory // 拷贝
			m.ID = newIDs[i]
			m.Status = store.StatusActive
			if !req.Auto {
				m.Trust = store.TrustHumanVerified // 人审盖章（A3：trust 只在人审通道产生）
			}
			m.ReviewedAt = now
			m.Modified = now
			if m.CapturedAt == "" {
				m.CapturedAt = now
			}
			if err := st.SaveMemory(&m); err != nil {
				return err
			}
			res.MemoryIDs = append(res.MemoryIDs, m.ID)
		}
		// 应用 supersession：旧条目退出检索、双向指针成对（不变量 3/4）
		for oldID, newID := range supersededOld {
			old, err := st.GetMemory(oldID)
			if err != nil {
				return err
			}
			old.Status = store.StatusSuperseded
			old.SupersededBy = newID
			old.Modified = now
			if err := st.SaveMemory(old); err != nil {
				return err
			}
			res.Superseded = append(res.Superseded, oldID)
		}
		sort.Strings(res.Superseded)
		// 版本推进
		v, err := st.Version()
		if err != nil {
			return err
		}
		if err := st.WriteVersion(v + 1); err != nil {
			return err
		}
		res.Version = v + 1
		// 审计
		if err := au.Append(audit.Record{
			Actor: actor, Action: action, IDs: res.MemoryIDs,
			Version: res.Version, Note: req.Note,
		}); err != nil {
			return err
		}
		// inbox 台账
		for batchID, ids := range batches {
			if err := in.RemoveCandidates(batchID, ids); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("promote 失败，已整体回滚: %w", err)
	}

	// 单一原子提交
	batchLabel := strings.Join(mapKeys(batches), ",")
	commit, err := store.GitCommit(st.Root, fmt.Sprintf("promote: %d 条记忆（批次 %s，v%d）%s",
		len(res.MemoryIDs), batchLabel, res.Version, autoTag(req.Auto)))
	if err != nil {
		undo() // 提交失败也要回滚工作区
		return nil, fmt.Errorf("git 提交失败，已回滚: %w", err)
	}
	res.Commit = commit
	persistIndex(st, res.Version) // 可重建加速层，落盘失败不致命
	return res, nil
}

// persistIndex 版本推进后重建并落盘该版本索引（快照钉住依赖旧版本索引留存）
func persistIndex(st *store.Store, version int) {
	ms, err := st.ListMemories()
	if err != nil {
		return
	}
	_ = index.Build(version, ms).Persist(st.Root)
}

func autoTag(auto bool) string {
	if auto {
		return " [auto]"
	}
	return ""
}

func mapKeys(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Reject 拒绝归档：audit 可查；不改变检索真值（版本不动）
func Reject(st *store.Store, in *inbox.Inbox, au *audit.Audit, ids []string, note string) (*Result, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("未指定候选")
	}
	actor, err := store.GitHasIdentity(st.Root)
	if err != nil {
		return nil, err
	}
	batches := map[string][]string{}
	var touched []string
	for _, cid := range ids {
		c, err := in.GetCandidate(cid)
		if err != nil {
			return nil, err
		}
		batches[c.Batch] = append(batches[c.Batch], cid)
		touched = append(touched, filepath.Join(st.Root, "inbox", "batches", c.Batch+".json"))
		touched = append(touched, filepath.Join(st.Root, "inbox", "candidates", c.ID+".md"))
	}
	touched = append(touched, filepath.Join(st.Root, "audit", "rejections.jsonl"))

	res := &Result{}
	undo, err := Atomic(st.Root, touched, func() error {
		if err := au.Append(audit.Record{Actor: actor, Action: audit.ActionReject, IDs: ids, Note: note}); err != nil {
			return err
		}
		for batchID, cids := range batches {
			if err := in.RemoveCandidates(batchID, cids); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("reject 失败，已整体回滚: %w", err)
	}
	commit, err := store.GitCommit(st.Root, fmt.Sprintf("reject: %d 条候选归档（%s）", len(ids), strings.Join(mapKeys(batches), ",")))
	if err != nil {
		undo()
		return nil, fmt.Errorf("git 提交失败，已回滚: %w", err)
	}
	res.Commit = commit
	return res, nil
}
