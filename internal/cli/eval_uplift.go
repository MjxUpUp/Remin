package cli

import (
	"fmt"

	"github.com/remin-dev/remin/internal/core/eval"
	"github.com/remin-dev/remin/internal/store"
	"github.com/spf13/cobra"
)

var upliftFlags struct {
	tasks  string
	record bool
}

// eval uplift 真源实测模式：用户自备任务集跑在真源检索面上（只读）；
// --record 逐次落 eval/history.jsonl——纵向衰减曲线随真实使用累积
var evalUpliftCmd = &cobra.Command{
	Use:   "uplift --tasks <file> [--record]",
	Short: "任务提升实测：自备任务集跑真源检索面（测量非门禁——退出码恒 0），可记录历史看衰减",
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := mustStore()
		if err != nil {
			return fail(err)
		}
		if upliftFlags.tasks == "" {
			return fail(fmt.Errorf("--tasks <file> 必填（JSONL：query/facet/expect_id|expect_contains|expect_abstain）"))
		}
		tasks, err := eval.LoadUpliftTasks(upliftFlags.tasks)
		if err != nil {
			return fail(err)
		}
		m := eval.RunUpliftTasks(st, tasks)
		if upliftFlags.record {
			rec := eval.HistoryRun{Mode: "store", Tasks: m.Tasks, Hits: m.Hits, Misses: m.Misses,
				Abstains: m.AbstainCorrect, WrongAbstains: m.WrongAbstains, FalseHits: m.FalseHits, Recall: m.Recall}
			if m.IndexErr != "" {
				rec.Note = "索引不可用（基建失败，非任务衰减）"
			}
			err := store.WithRoot(st.Root, func() error {
				return eval.AppendHistory(st.Root, rec)
			})
			if err != nil {
				return fail(err)
			}
		}
		return output(func() {
			fmt.Printf("任务 %d：命中 %d · 未中 %d · 弃权 %d · 期望命中却弃权 %d · 期望弃权却命中 %d（recall %.2f）\n",
				m.Tasks, m.Hits, m.Misses, m.AbstainCorrect, m.WrongAbstains, m.FalseHits, m.Recall)
			for _, r := range m.Results {
				mark := "✗"
				if r.Pass {
					mark = "✓"
				}
				fmt.Printf("  %s [%s] %s", mark, r.State, r.Query)
				if r.Detail != "" {
					fmt.Printf(" — %s", r.Detail)
				}
				fmt.Println()
			}
			if upliftFlags.record {
				fmt.Println("已记录历史：remin eval history 看趋势")
			}
			printRootFooter(st.Root)
		}, m)
	},
}

var historyFlags struct {
	limit int
}

// eval history 纵向趋势（衰减曲线的文本形态）
var evalHistoryCmd = &cobra.Command{
	Use:   "history [--limit N]",
	Short: "查看 uplift 实测历史与趋势（相对首跑/上跑 Δ）",
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := mustStore()
		if err != nil {
			return fail(err)
		}
		runs, err := eval.LoadHistory(st.Root)
		if err != nil {
			return fail(err)
		}
		text := eval.RenderHistory(runs, historyFlags.limit)
		return output(func() {
			fmt.Print(text)
			printRootFooter(st.Root)
		}, runs)
	},
}

func init() {
	evalUpliftCmd.Flags().StringVar(&upliftFlags.tasks, "tasks", "", "任务文件（JSONL，必填）")
	evalUpliftCmd.Flags().BoolVar(&upliftFlags.record, "record", false, "结果记入 eval/history.jsonl（纵向累积）")
	evalCmd.AddCommand(evalUpliftCmd)

	evalHistoryCmd.Flags().IntVar(&historyFlags.limit, "limit", 20, "显示最近 N 次")
	evalCmd.AddCommand(evalHistoryCmd)
}
