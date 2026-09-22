package cli

import (
	"context"
	"fmt"

	"github.com/remin-dev/remin/internal/core/config"
	"github.com/remin-dev/remin/internal/core/miner"
	"github.com/spf13/cobra"
)

var mineFlags struct {
	dryRun      bool
	force       bool
	fromQueue   bool
	deep        bool
	sinceDays   int  // 首挖限量：仅挖 mtime 近 N 天（默认 7；0=不限）
	fullHistory bool // 显式全量（关掉 since 过滤）
}

// mine transcript 挖矿（增量断点续挖；--force 全量重挖兼审计）
var mineCmd = &cobra.Command{
	Use:   "mine",
	Short: "从 Claude Code 会话日志挖矿：提取记忆候选 → inbox（增量断点续挖）",
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := mustStore()
		if err != nil {
			return fail(err)
		}
		cfg, err := config.Load(st.ConfigPath())
		if err != nil {
			return fail(err)
		}
		since := mineFlags.sinceDays
		if mineFlags.fullHistory {
			since = 0 // 显式全量（重挖审计场景）
		}
		rep, err := miner.Mine(context.Background(), st, cfg, miner.Options{
			DryRun: mineFlags.dryRun, Force: mineFlags.force, FromQueue: mineFlags.fromQueue,
			Deep: mineFlags.deep, SinceDays: since,
		})
		if err != nil {
			return fail(err)
		}
		return output(func() {
			if mineFlags.dryRun {
				fmt.Printf("[dry-run] 处理 %d 个 transcript，提取 %d 条候选：\n", rep.Transcripts, rep.Candidates)
			} else {
				fmt.Printf("挖矿完成：%d 个 transcript，%d 条候选进 inbox", rep.Transcripts, rep.Candidates)
				if rep.Batch != "" {
					fmt.Printf("（批次 %s）", rep.Batch)
				}
				if rep.AutoPromoted > 0 {
					fmt.Printf("；快速档自动生效 recap %d 条（trust 保持未验证）", rep.AutoPromoted)
				}
				fmt.Println()
				if rep.Batch != "" {
					fmt.Printf("人审: remin inbox --batch %s\n", rep.Batch)
				}
			}
			if rep.Note != "" {
				fmt.Printf("备注: %s\n", rep.Note)
			}
			if rep.SkippedOld > 0 {
				fmt.Printf("跳过 %d 个 %d 天前的 transcript（--full-history 全量重挖）\n", rep.SkippedOld, since)
			}
			printRootFooter(st.Root)
			for i, p := range rep.Preview {
				if i >= 10 {
					fmt.Printf("  … 共 %d 条\n", rep.Candidates)
					break
				}
				fmt.Printf("  · %s\n", p)
			}
		}, rep)
	},
}

func init() {
	mineCmd.Flags().BoolVar(&mineFlags.dryRun, "dry-run", false, "只报告不写 inbox")
	mineCmd.Flags().BoolVar(&mineFlags.force, "force", false, "全量重挖（重置增量游标，兼审计；不受 --since 限制）")
	mineCmd.Flags().BoolVar(&mineFlags.fromQueue, "from-queue", false, "只处理 Stop hook 入队的 transcript")
	mineCmd.Flags().BoolVar(&mineFlags.deep, "deep", false, "深度提取：LLM 语义补充召回（需 config llm 节 + REMIN_LLM_API_KEY；快速路径结果保留）")
	mineCmd.Flags().IntVar(&mineFlags.sinceDays, "since", 7, "仅挖最近 N 天的 transcript（首挖限量，防历史 recap 洪泛）")
	mineCmd.Flags().BoolVar(&mineFlags.fullHistory, "full-history", false, "不限时间全量挖（显式关闭 --since）")
	rootCmd.AddCommand(mineCmd)
}
