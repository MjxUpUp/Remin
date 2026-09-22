// 审收选择器（CLI 与 Web UI 共用的单一事实源：batch/all/id/except/type 语义只此一份）
package inbox

import "fmt"

// Selector 审收选择器（json tag 供 Web UI 的 API 选择器直解，与 CLI flag 同语义）
type Selector struct {
	Batch  string   `json:"batch,omitempty"`
	All    bool     `json:"all,omitempty"`
	IDs    []string `json:"ids,omitempty"`
	Except []string `json:"except,omitempty"`
	Type   string   `json:"type,omitempty"` // 类型分诊：批次内只作用于该类型（如 episodic 批量拒 recap）
}

// ResolveIDs 解析选择器为候选 id 集（ids 精确指定优先；type 与 ids 互斥）
func (in *Inbox) ResolveIDs(sel Selector, what string) ([]string, error) {
	if len(sel.IDs) > 0 {
		if sel.Type != "" {
			return nil, fmt.Errorf("id 与 type 互斥（id 已精确指定候选）")
		}
		return sel.IDs, nil
	}
	if sel.Batch == "" {
		return nil, fmt.Errorf("需要 id 或 batch+all/except")
	}
	cands, err := in.ListCandidates(sel.Batch)
	if err != nil {
		return nil, err
	}
	except := map[string]bool{}
	for _, e := range sel.Except {
		except[e] = true
	}
	var ids []string
	for _, c := range cands {
		if sel.Type != "" && c.Type != sel.Type {
			continue
		}
		if sel.All || len(sel.Except) > 0 {
			if !except[c.ID] {
				ids = append(ids, c.ID)
			}
		}
	}
	if len(ids) == 0 {
		if sel.Type != "" {
			return nil, fmt.Errorf("选择器未命中任何%s（type %s 在该批次无候选）", what, sel.Type)
		}
		return nil, fmt.Errorf("选择器未命中任何%s（all 未给？）", what)
	}
	return ids, nil
}
