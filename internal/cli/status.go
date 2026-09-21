package cli

import (
	"fmt"

	"github.com/remin-dev/remin/internal/store"
	"github.com/spf13/cobra"
)

// status 单条全貌：supersession 链、双时间戳、verify、provenance
var statusCmd = &cobra.Command{
	Use:   "status <id>",
	Short: "单条记忆全貌（supersession 链 / 时间戳 / verify / 来源）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := mustStore()
		if err != nil {
			return fail(err)
		}
		m, err := st.GetMemory(args[0])
		if err != nil {
			return fail(err)
		}
		older, newer, _ := st.SupersessionChain(m.ID)
		v, _ := st.Version()
		return output(func() {
			fmt.Printf("%s  [%s · %s · %s · %s]\n", m.ID, m.Type, m.Facet, m.Status, store.TrustLabel(m.Trust))
			fmt.Printf("正文: %s\n", m.Body)
			fmt.Printf("captured_at: %s   reviewed_at: %s   modified: %s\n", m.CapturedAt, m.ReviewedAt, m.Modified)
			fmt.Printf("来源: %s（%s）\n  摘录: %s\n", m.Provenance.Origin, m.Provenance.Ref, truncate(m.Provenance.Quote, 120))
			if len(m.Context) > 0 {
				fmt.Printf("上下文: %v\n", m.Context)
			}
			if m.Expires != "" {
				fmt.Printf("ephemeral: %s（自动过期）\n", m.Expires)
			}
			if m.Verify != nil {
				fmt.Printf("verify: %s\n  条件: %s\n  最近: %s = %s\n",
					m.Verify.Result, m.Verify.Condition, m.Verify.LastCheck, m.Verify.Result)
			}
			if len(older) > 0 {
				fmt.Println("替代链（旧 → 新）:")
				for _, o := range older {
					fmt.Printf("  ⊖ 旧 %s: %s\n", o.ID, truncate(oneLine(o.Body), 60))
				}
				fmt.Printf("  ● 本条\n")
			}
			for _, n := range newer {
				fmt.Printf("  ⊕ 新 %s: %s（本条已退出检索）\n", n.ID, truncate(oneLine(n.Body), 60))
			}
			fmt.Printf("（索引 v%d）\n", v)
		}, map[string]interface{}{"memory": m, "superseded_older": older, "superseded_by": newer, "index_version": v})
	},
}

var refreshCmd = &cobra.Command{
	Use:   "refresh",
	Short: "查看当前快照版本（MCP 会话内用 memory_refresh 推进）",
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := mustStore()
		if err != nil {
			return fail(err)
		}
		v, err := st.Version()
		if err != nil {
			return fail(err)
		}
		return output(func() {
			fmt.Printf("当前索引版本: v%d（会话快照在开场钉住；显式推进请经 MCP memory_refresh，可归因）\n", v)
		}, map[string]interface{}{"index_version": v})
	},
}

func init() {
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(refreshCmd)
}
