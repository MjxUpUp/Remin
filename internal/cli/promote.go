package cli

import (
	"fmt"
	"strings"

	"github.com/remin-dev/remin/internal/core/audit"
	"github.com/remin-dev/remin/internal/core/inbox"
	"github.com/remin-dev/remin/internal/core/promotion"
	"github.com/spf13/cobra"
)

type selectorFlags struct {
	batch  string
	all    bool
	ids    []string
	except []string
}

// resolveIDs 解析审收选择器：--batch + --all / --id / --except
func resolveIDs(in *inbox.Inbox, sel selectorFlags, what string) ([]string, error) {
	if len(sel.ids) > 0 {
		return sel.ids, nil
	}
	if sel.batch == "" {
		return nil, fmt.Errorf("需要 --id <候选id...> 或 --batch <id> --all/--except")
	}
	cands, err := in.ListCandidates(sel.batch)
	if err != nil {
		return nil, err
	}
	except := map[string]bool{}
	for _, e := range sel.except {
		except[e] = true
	}
	var ids []string
	for _, c := range cands {
		if sel.all || len(sel.except) > 0 {
			if !except[c.ID] {
				ids = append(ids, c.ID)
			}
		}
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("选择器未命中任何%s（--all 未给？）", what)
	}
	return ids, nil
}

var promoteSel selectorFlags

var promoteCmd = &cobra.Command{
	Use:   "promote",
	Short: "人审采纳：单一原子提交（记忆+supersession+版本+1+审计）",
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := mustStore()
		if err != nil {
			return fail(err)
		}
		in := inbox.New(st)
		au := audit.New(st)
		ids, err := resolveIDs(in, promoteSel, "候选")
		if err != nil {
			return fail(err)
		}
		res, err := promotion.Promote(st, in, au, promotion.Request{CandidateIDs: ids})
		if err != nil {
			return fail(err)
		}
		return output(func() {
			fmt.Printf("已采纳 %d 条 → v%d（commit %s）\n", len(res.MemoryIDs), res.Version, res.Commit)
			for i, id := range res.MemoryIDs {
				if i >= 20 {
					fmt.Printf("  … 共 %d 条\n", len(res.MemoryIDs))
					break
				}
				fmt.Printf("  + %s\n", id)
			}
			for _, id := range res.Superseded {
				fmt.Printf("  ⊖ %s 已被替代，退出检索（历史保留）\n", id)
			}
		}, res)
	},
}

var rejectSel selectorFlags

var rejectCmd = &cobra.Command{
	Use:   "reject",
	Short: "拒绝候选：归档可审计（不改变检索真值）",
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := mustStore()
		if err != nil {
			return fail(err)
		}
		in := inbox.New(st)
		au := audit.New(st)
		ids, err := resolveIDs(in, rejectSel, "候选")
		if err != nil {
			return fail(err)
		}
		res, err := promotion.Reject(st, in, au, ids, "")
		if err != nil {
			return fail(err)
		}
		return output(func() {
			fmt.Printf("已拒绝并归档 %d 条（commit %s，remin log 可查）\n", len(ids), res.Commit)
		}, map[string]interface{}{"rejected": ids, "commit": res.Commit})
	},
}

var logFlags struct {
	batch string
	limit int
}

var logCmd = &cobra.Command{
	Use:   "log",
	Short: "审收审计历史（谁、何时、采纳了什么）",
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := mustStore()
		if err != nil {
			return fail(err)
		}
		au := audit.New(st)
		recs, err := au.List("")
		if err != nil {
			return fail(err)
		}
		if logFlags.batch != "" {
			var filtered []audit.Record
			for _, r := range recs {
				if containsID(strings.Split(r.Batch, ","), logFlags.batch) || containsID(r.IDs, logFlags.batch) {
					filtered = append(filtered, r)
				}
			}
			recs = filtered
		}
		if logFlags.limit > 0 && len(recs) > logFlags.limit {
			recs = recs[:logFlags.limit]
		}
		return output(func() {
			if len(recs) == 0 {
				fmt.Println("暂无审收记录。")
				return
			}
			for _, r := range recs {
				fmt.Printf("%s  %s  %s  %d 条 %s\n", r.TS, actionLabel(r.Action), r.Actor, len(r.IDs), joinIDs(r.IDs, 4))
				if r.Note != "" {
					fmt.Printf("    %s\n", r.Note)
				}
			}
		}, recs)
	},
}

func actionLabel(a string) string {
	switch a {
	case audit.ActionPromote:
		return "采纳"
	case audit.ActionAutoPromote:
		return "自动采纳"
	case audit.ActionReject:
		return "拒绝"
	case audit.ActionRestore:
		return "还原"
	case audit.ActionVerify:
		return "验证"
	}
	return a
}

func joinIDs(ids []string, max int) string {
	if len(ids) > max {
		return fmt.Sprintf("[%s …共%d]", strings.Join(ids[:max], " "), len(ids))
	}
	return "[" + strings.Join(ids, " ") + "]"
}

func containsID(ids []string, id string) bool {
	for _, i := range ids {
		if i == id {
			return true
		}
	}
	return false
}

func init() {
	promoteCmd.Flags().StringVar(&promoteSel.batch, "batch", "", "批次 id")
	promoteCmd.Flags().BoolVar(&promoteSel.all, "all", false, "采纳批次全部候选")
	promoteCmd.Flags().StringSliceVar(&promoteSel.ids, "id", nil, "指定候选 id（逗号分隔，可跨批次）")
	promoteCmd.Flags().StringSliceVar(&promoteSel.except, "except", nil, "批次内排除的候选 id")
	rootCmd.AddCommand(promoteCmd)

	rejectCmd.Flags().StringVar(&rejectSel.batch, "batch", "", "批次 id")
	rejectCmd.Flags().BoolVar(&rejectSel.all, "all", false, "拒绝批次全部候选")
	rejectCmd.Flags().StringSliceVar(&rejectSel.ids, "id", nil, "指定候选 id")
	rejectCmd.Flags().StringSliceVar(&rejectSel.except, "except", nil, "批次内排除的候选 id")
	rootCmd.AddCommand(rejectCmd)

	logCmd.Flags().StringVar(&logFlags.batch, "batch", "", "按批次/记忆 id 过滤")
	logCmd.Flags().IntVar(&logFlags.limit, "limit", 20, "最多显示条数")
	rootCmd.AddCommand(logCmd)
}
