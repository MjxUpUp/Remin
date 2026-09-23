// 记忆生命周期操作（管理面）：退休/重新激活——文件与 git 历史保留，
// 退休=退出检索（可逆）；审计走 audit 台账（调用方传入 Record）。
package store

import "fmt"

// Retire 退休一条 active 记忆：status → expired（退出检索，与过期同口径），
// 旧 body/provenance 零改动（A6 原件不可变），可通过 Reactivate 恢复。
func (s *Store) Retire(id, reason string) error {
	return s.transitStatus(id, StatusActive, StatusExpired, reason)
}

// Reactivate 重新激活一条 retired(expired-by-retire) 记忆：status → active。
// 语义边界：ephemeral 自然过期的记忆不应被重新激活（会再过期）——由调用方判断；
// 本方法做保守校验：仅 expired → active（superseded 的恢复应走反向 supersede，不在此面）。
func (s *Store) Reactivate(id, reason string) error {
	return s.transitStatus(id, StatusExpired, StatusActive, reason)
}

// transitStatus 状态迁移（含前置状态校验）：改 status + Modified 时间戳。
// 调用方负责 WithRoot 互斥与 git 提交（本方法只改文件，保持与 SaveMemory 同粒度）。
func (s *Store) transitStatus(id, from, to, reason string) error {
	m, err := s.GetMemory(id)
	if err != nil {
		return err
	}
	if m.Status != from {
		return fmt.Errorf("记忆 %s 状态为 %s，期望 %s（拒绝意外迁移）", id, m.Status, from)
	}
	m.Status = to
	m.Modified = NowTime()
	if reason != "" {
		m.Provenance.Quote = truncateLifecycleNote(m.Provenance.Quote, reason)
	}
	return s.SaveMemory(m)
}

// truncateLifecycleNote 理由尾注进 Quote（可审计，不另立字段——存量 frontmatter 不动）
func truncateLifecycleNote(quote, reason string) string {
	note := quote + " ｜ 理由: " + reason
	r := []rune(note)
	if len(r) <= 400 {
		return note
	}
	return string(r[:400]) + "…"
}
