// 接线台账（cordis 可逆性）：doctor --install 的每个 effect 记录其 inverse 所需
// 的全部信息，uninstall 按台账回放摘除——不靠卸载时重新扫描猜测。
package doctor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Effect 一次接线写入；Kind 决定 inverse 的回放方式
type Effect struct {
	ID      string   `json:"id"`                // kind|file|key —— 幂等去重键
	Kind    string   `json:"kind"`              // mcp-json | hook-entry | toml-section | gitignore
	Agent   string   `json:"agent,omitempty"`   // claude-code / codex / cursor / gemini-cli
	File    string   `json:"file,omitempty"`    // 被写的用户配置文件（绝对路径）
	Key     string   `json:"key,omitempty"`     // mcpServers.memory / hook 名（SessionStart|Stop）
	Command string   `json:"command,omitempty"` // 我们写入的二进制路径或 hook 命令串
	Section string   `json:"section,omitempty"` // TOML 段名
	Lines   []string `json:"lines,omitempty"`   // gitignore 追加的行
	Backup  string   `json:"backup,omitempty"`  // 写前备份文件（卸载时清理；空=写前文件不存在）
	Created bool     `json:"created,omitempty"` // 目标文件由我们创建（卸载时删净空壳）
	TS      string   `json:"ts,omitempty"`
}

// Ledger 台账整体（<root>/wiring.json）
type Ledger struct {
	Version int      `json:"version"`
	Effects []Effect `json:"effects"`
}

// LedgerPath 台账文件位置
func LedgerPath(root string) string { return filepath.Join(root, "wiring.json") }

// effectID 幂等键：同一文件同一键重复接线 → 替换不重复
func effectID(kind, file, key string) string { return kind + "|" + file + "|" + key }

// LoadLedger 读台账（不存在 → 空台账；损坏 → 如实报错，绝不吞成空后盲目回放）
func LoadLedger(root string) (*Ledger, error) {
	l := &Ledger{Version: 1}
	data, err := os.ReadFile(LedgerPath(root))
	if err != nil {
		if os.IsNotExist(err) {
			return l, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, l); err != nil {
		return nil, fmt.Errorf("接线台账 %s 损坏: %w——拒绝猜测回放，请手工检查", LedgerPath(root), err)
	}
	return l, nil
}

// Save 原子落盘（tmp + rename）
func (l *Ledger) Save(root string) error {
	if l.Version == 0 {
		l.Version = 1
	}
	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	tmp := LedgerPath(root) + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, LedgerPath(root))
}

// Append 记一个 effect（同 ID 替换，保留最新）
func (l *Ledger) Append(e Effect) {
	if e.TS == "" {
		e.TS = timeNow().Format(time.RFC3339)
	}
	for i := range l.Effects {
		if l.Effects[i].ID == e.ID {
			// 创建事实不可逆：幂等重跑的新 effect 不带 Created 时保留旧记录，
			// 否则「创建→重跑→卸载」路径空壳整删失效（R2 评审发现的残余）
			if !e.Created {
				e.Created = l.Effects[i].Created
			}
			l.Effects[i] = e
			return
		}
	}
	l.Effects = append(l.Effects, e)
}

// Remove 摘除一个 effect（回放完成后调用）
func (l *Ledger) Remove(id string) {
	out := l.Effects[:0]
	for _, e := range l.Effects {
		if e.ID != id {
			out = append(out, e)
		}
	}
	l.Effects = out
}
