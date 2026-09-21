// Package audit 审收审计记录（谁、何时、采纳了什么——promotions/rejections 双台账）
package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/remin-dev/remin/internal/store"
)

const (
	ActionPromote     = "promote"
	ActionAutoPromote = "auto-promote"
	ActionReject      = "reject"
	ActionRestore     = "restore"
	ActionVerify      = "verify"
)

// Record 一条审收/审计记录
type Record struct {
	TS      string   `json:"ts"`
	Actor   string   `json:"actor"`
	Action  string   `json:"action"`
	Batch   string   `json:"batch,omitempty"`
	IDs     []string `json:"ids"`
	Version int      `json:"version_after,omitempty"`
	Note    string   `json:"note,omitempty"`
}

// Audit audit/ 目录视图
type Audit struct{ Root string }

func New(st *store.Store) *Audit { return &Audit{Root: filepath.Join(st.Root, "audit")} }

func (a *Audit) file(action string) string {
	name := "promotions.jsonl"
	if action == ActionReject {
		name = "rejections.jsonl"
	}
	return filepath.Join(a.Root, name)
}

// Append 追加一条记录（不改 git；事务由调用方编排）
func (a *Audit) Append(rec Record) error {
	if err := os.MkdirAll(a.Root, 0o755); err != nil {
		return err
	}
	if rec.TS == "" {
		rec.TS = store.NowTime()
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(a.file(rec.Action), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(data, '\n'))
	return err
}

// List 读取台账（action 为空读双台账合并，按时间排序）
func (a *Audit) List(action string) ([]Record, error) {
	// 非 reject 动作共用 promotions.jsonl——只读一次，避免重复
	files := []string{a.file(ActionPromote)}
	if action == ActionReject {
		files = []string{a.file(ActionReject)}
	} else if action == "" {
		files = []string{a.file(ActionPromote), a.file(ActionReject)}
	}
	var out []Record
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			if line == "" {
				continue
			}
			var r Record
			if err := json.Unmarshal([]byte(line), &r); err == nil {
				out = append(out, r)
			}
		}
	}
	// 稳定排序：时间倒序（新→旧）
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].TS > out[i].TS {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out, nil
}
