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
	typ   string // 只看该类型（分诊：923 条里挑出 recap / preference）
}

// typeComposition 批次构成统计（新用户反馈：923 条不知道是什么、要干嘛）
func typeComposition(cands []*inbox.Candidate) string {
	var order = []string{store.TypePreference, store.TypeProcedural, store.TypeDecision, store.TypeSemantic, store.TypeEpisodic}
	counts := map[string]int{}
	for _, c := range cands {
		counts[c.Type]++
	}
	var parts []string
	for _, t := range order {
		if n := counts[t]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", t, n))
		}
	}
	return strings.Join(parts, " · ")
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
		if inboxFlags.typ != "" && !validTypes[inboxFlags.typ] {
			return fail(fmt.Errorf("未知类型 %q（可选：%s）", inboxFlags.typ, "preference/procedural/decision/episodic/semantic"))
		}
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
		type composition struct {
			id    string
			label string
		}
		var comps []composition
		for _, b := range batches {
			views = append(views, batchView{b.ID, b.Source, b.CreatedAt, b.Status, len(b.Candidates)})
			if b.Status != "done" && len(b.Candidates) > 0 {
				if cs, err := in.ListCandidates(b.ID); err == nil && len(cs) > 0 {
					comps = append(comps, composition{b.ID, typeComposition(cs)})
				}
			}
		}
		return output(func() {
			if len(views) == 0 {
				fmt.Println("inbox 为空——没有待审候选。")
				printRootFooter(st.Root)
				return
			}
			fmt.Printf("待审批次（open %d / 全部 %d）：\n", open, len(views))
			for _, v := range views {
				fmt.Printf("  %s  [%s] %s  剩余 %d 条  (%s)\n", v.ID, v.Status, v.Source, v.Pending, v.CreatedAt)
			}
			for _, c := range comps {
				if c.label != "" {
					fmt.Printf("  构成 %s: %s\n", c.id, c.label)
				}
			}
			fmt.Println("\n查看详情:   remin inbox --batch <id>（--type <类型> 只看某类）")
			fmt.Println("采纳: remin promote --batch <id> --all（--type 过滤）")
			fmt.Println("拒绝: remin reject --batch <id> --all（--type episodic 一键清 recap）")
			printRootFooter(st.Root)
		}, map[string]interface{}{"open": open, "batches": views})
	},
}

func renderCandidates(in *inbox.Inbox, batchID string) error {
	st, err := mustStore()
	if err != nil {
		return fail(err)
	}
	b, err := in.GetBatch(batchID)
	if err != nil {
		return fail(err)
	}
	cands, err := in.ListCandidates(batchID)
	if err != nil {
		return fail(err)
	}
	total := len(cands)
	if inboxFlags.typ != "" {
		var filtered []*inbox.Candidate
		for _, c := range cands {
			if c.Type == inboxFlags.typ {
				filtered = append(filtered, c)
			}
		}
		cands = filtered
	}
	return output(func() {
		if inboxFlags.typ != "" {
			fmt.Printf("批次 %s（来源 %s，%s）共 %d 条（本视图 %d 条，--type %s）：\n", b.ID, b.Source, b.CreatedAt, total, len(cands), inboxFlags.typ)
		} else {
			fmt.Printf("批次 %s（来源 %s，%s）待审 %d 条（%s）：\n", b.ID, b.Source, b.CreatedAt, len(cands), typeComposition(cands))
		}
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
		fmt.Printf("\n采纳: remin promote --batch %s --all（或 --id/--except/--type 挑选）\n拒绝: remin reject --batch %s --all（--type episodic 一键清 recap）\n", b.ID, b.ID)
		printRootFooter(st.Root)
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
	inboxCmd.Flags().StringVar(&inboxFlags.typ, "type", "", "只看该类型（配 --batch 详情视图生效；preference/procedural/decision/episodic/semantic）")
	rootCmd.AddCommand(inboxCmd)
}

// validTypes 已知类型集（--type 拼错早失败，不给人空视图的错觉）
var validTypes = map[string]bool{
	store.TypePreference: true, store.TypeProcedural: true, store.TypeDecision: true,
	store.TypeSemantic: true, store.TypeEpisodic: true,
}
