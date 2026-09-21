package cli

import (
	"fmt"
	"strings"

	"github.com/remin-dev/remin/internal/core/inbox"
	"github.com/remin-dev/remin/internal/store"
	"github.com/spf13/cobra"
)

var inboxFlags struct {
	batch string
	group string
}

// inbox 审收视图（FR-GOV-1）：批次 → 组（冲突建议排前）→ 条目
var inboxCmd = &cobra.Command{
	Use:   "inbox [--batch <id>]",
	Short: "审收视图：待审批次与候选（冲突建议排前）",
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := mustStore()
		if err != nil {
			return fail(err)
		}
		in := inbox.New(st)
		if inboxFlags.batch != "" {
			return renderCandidates(in, inboxFlags.batch)
		}
		batches, err := in.ListBatches()
		if err != nil {
			return fail(err)
		}
		open := 0
		for _, b := range batches {
			if b.Status != "done" {
				open++
			}
		}
		type batchView struct {
			ID        string `json:"id"`
			Source    string `json:"source"`
			CreatedAt string `json:"created_at"`
			Status    string `json:"status"`
			Pending   int    `json:"pending"`
		}
		var views []batchView
		for _, b := range batches {
			views = append(views, batchView{b.ID, b.Source, b.CreatedAt, b.Status, len(b.Candidates)})
		}
		return output(func() {
			if len(views) == 0 {
				fmt.Println("inbox 为空——没有待审候选。")
				return
			}
			fmt.Printf("待审批次（open %d / 全部 %d）：\n", open, len(views))
			for _, v := range views {
				fmt.Printf("  %s  [%s] %s  剩余 %d 条  (%s)\n", v.ID, v.Status, v.Source, v.Pending, v.CreatedAt)
			}
			fmt.Println("\n查看详情: remin inbox --batch <id>；采纳: remin promote --batch <id> --all")
		}, map[string]interface{}{"open": open, "batches": views})
	},
}

func renderCandidates(in *inbox.Inbox, batchID string) error {
	b, err := in.GetBatch(batchID)
	if err != nil {
		return fail(err)
	}
	cands, err := in.ListCandidates(batchID)
	if err != nil {
		return fail(err)
	}
	return output(func() {
		fmt.Printf("批次 %s（来源 %s，%s）待审 %d 条：\n", b.ID, b.Source, b.CreatedAt, len(cands))
		lastGroup := ""
		for _, c := range cands {
			g := c.Group
			if g == "" {
				g = inbox.GroupNormal
			}
			if g != lastGroup {
				lastGroup = g
				switch g {
				case inbox.GroupConflict:
					fmt.Println("\n── 冲突建议（可能替代旧记忆，请人裁）──")
				case inbox.GroupDuplicate:
					fmt.Println("\n── 重复簇（与库内相似）──")
				default:
					fmt.Println("\n── 普通候选 ──")
				}
			}
			fmt.Printf("  %s [%s/%s/%s] %s\n", c.ID, c.Type, c.Facet, store.TrustLabel(c.Trust), truncate(oneLine(c.Body), 72))
			if c.Supersedes != "" {
				fmt.Printf("      ↳ 建议替代: %s\n", c.Supersedes)
			}
			if c.DuplicateOf != "" {
				fmt.Printf("      ↳ 与库内 %s 相似\n", c.DuplicateOf)
			}
			fmt.Printf("      来源: %s (%s)\n", c.Provenance.Origin, truncate(c.Provenance.Ref, 60))
		}
		fmt.Printf("\n采纳: remin promote --batch %s --all（或 --id/--except 挑选）\n拒绝: remin reject --batch %s --all\n", b.ID, b.ID)
	}, cands)
}

func oneLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\n"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func init() {
	inboxCmd.Flags().StringVar(&inboxFlags.batch, "batch", "", "查看指定批次详情")
	rootCmd.AddCommand(inboxCmd)
}
