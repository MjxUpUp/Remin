// Package verify verify-condition 用前验证（P3-A4）。
// 零 LLM：机器可判定条件（path-exists / file-contains）直接评估；
// 自然语言条件如实返回 unknown 待人裁（--set passed|failed 记录人判）。
package verify

import (
	"fmt"
	"os"
	"strings"

	"github.com/remin-dev/remin/internal/core/audit"
	"github.com/remin-dev/remin/internal/core/promotion"
	"github.com/remin-dev/remin/internal/index"
	"github.com/remin-dev/remin/internal/store"
)

// 机器可判定条件前缀
const (
	CondPathExists   = "path-exists:"
	CondFileContains = "file-contains:"
)

// Evaluate 评估单条条件；result ∈ passed|failed|unknown
func Evaluate(condition string) (result, evidence string) {
	cond := strings.TrimSpace(condition)
	switch {
	case strings.HasPrefix(cond, CondPathExists):
		p := strings.TrimSpace(strings.TrimPrefix(cond, CondPathExists))
		if _, err := os.Stat(p); err == nil {
			return store.VerifyPassed, fmt.Sprintf("存在: %s", p)
		}
		return store.VerifyFailed, fmt.Sprintf("不存在: %s", p)
	case strings.HasPrefix(cond, CondFileContains):
		rest := strings.TrimPrefix(cond, CondFileContains)
		parts := strings.SplitN(rest, "::", 2)
		if len(parts) != 2 {
			return store.VerifyUnknown, "file-contains 条件需 <path>::<text>"
		}
		p, text := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		data, err := os.ReadFile(p)
		if err != nil {
			return store.VerifyFailed, fmt.Sprintf("不可读: %s", p)
		}
		if strings.Contains(string(data), text) {
			return store.VerifyPassed, fmt.Sprintf("%s 含 %q", p, text)
		}
		return store.VerifyFailed, fmt.Sprintf("%s 不含 %q", p, text)
	default:
		// 自然语言条件：机器无法判定，绝不猜（宁可不知道）
		return store.VerifyUnknown, "自然语言条件需人判（remin verify <id> --set passed|failed）"
	}
}

// Outcome 单条验证结果
type Outcome struct {
	ID       string `json:"id"`
	Result   string `json:"result"`
	Evidence string `json:"evidence"`
	Changed  bool   `json:"truth_changed"` // 是否改变检索真值（版本 +1 依据）
}

// Run 对 id（或 all=全部带 verify 条件的记忆）执行验证并原子回写。
// forced 非空时为人判覆盖（passed|failed）。
func Run(st *store.Store, au *audit.Audit, idOrAll string, forced string) ([]Outcome, error) {
	var targets []*store.Memory
	if idOrAll == "all" {
		ms, err := st.ListMemories()
		if err != nil {
			return nil, err
		}
		for _, m := range ms {
			if m.Verify != nil && m.Verify.Condition != "" {
				targets = append(targets, m)
			}
		}
	} else {
		m, err := st.GetMemory(idOrAll)
		if err != nil {
			return nil, err
		}
		if m.Verify == nil {
			return nil, fmt.Errorf("记忆 %s 无 verify 条件", m.ID)
		}
		targets = append(targets, m)
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("没有带 verify 条件的记忆")
	}
	if forced != "" && forced != store.VerifyPassed && forced != store.VerifyFailed {
		return nil, fmt.Errorf("--set 只接受 passed|failed")
	}

	now := store.NowTime()
	var outcomes []Outcome
	var touched []string
	changed := false
	// 先在内存中算结果，再统一原子落盘
	updated := map[string]*store.Memory{}
	for _, m := range targets {
		result, evidence := Evaluate(m.Verify.Condition)
		if forced != "" {
			result = forced
			evidence = "人判"
		}
		prev := m.Verify.Result
		truthChanged := false
		if prev != result {
			// 结果改变检索真值：failed 退出检索（置 expired）；failed→passed 复活（active）
			if result == store.VerifyFailed {
				m.Status = store.StatusExpired
				truthChanged = true
			} else if prev == store.VerifyFailed && result == store.VerifyPassed && m.Status == store.StatusExpired {
				m.Status = store.StatusActive
				truthChanged = true
			}
		}
		if truthChanged {
			changed = true
		}
		m.Verify.LastCheck = now
		m.Verify.Result = result
		m.Modified = now
		updated[m.ID] = m
		touched = append(touched, st.MemoryPath(m.Type, m.ID))
		outcomes = append(outcomes, Outcome{ID: m.ID, Result: result, Evidence: evidence,
			Changed: truthChanged})
	}

	actor, err := store.GitHasIdentity(st.Root)
	if err != nil {
		return nil, err
	}
	version := 0
	_, err = promotion.Atomic(st.Root, touched, func() error {
		for _, m := range updated {
			if err := st.SaveMemory(m); err != nil {
				return err
			}
		}
		if changed {
			v, err := st.Version()
			if err != nil {
				return err
			}
			if err := st.WriteVersion(v + 1); err != nil {
				return err
			}
			version = v + 1
		} else {
			v, _ := st.Version()
			version = v
		}
		ids := make([]string, 0, len(updated))
		for id := range updated {
			ids = append(ids, id)
		}
		return au.Append(audit.Record{Actor: actor, Action: audit.ActionVerify, IDs: ids,
			Version: version, Note: "verify-condition 用前验证"})
	})
	if err != nil {
		return nil, fmt.Errorf("verify 回写失败，已回滚: %w", err)
	}
	if _, err := store.GitCommit(st.Root, fmt.Sprintf("verify: %d 条记忆复验（v%d）", len(updated), version)); err != nil {
		return nil, fmt.Errorf("git 提交失败: %w", err)
	}
	if changed {
		// 真值变化 → 重建索引（可重建加速层，失败不致命）
		if ms, err := st.ListMemories(); err == nil {
			_ = index.Build(version, ms).Persist(st.Root)
		}
	}
	return outcomes, nil
}
