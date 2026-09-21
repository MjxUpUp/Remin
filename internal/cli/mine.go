package cli

import (
	"context"
	"fmt"

	"github.com/remin-dev/remin/internal/core/config"
	"github.com/remin-dev/remin/internal/core/miner"
	"github.com/spf13/cobra"
)

var mineFlags struct {
	dryRun    bool
	force     bool
	fromQueue bool
	deep      bool
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
		rep, err := miner.Mine(context.Background(), st, cfg, miner.Options{
			DryRun: mineFlags.dryRun, Force: mineFlags.force, FromQueue: mineFlags.fromQueue,
			Deep: mineFlags.deep,
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
	mineCmd.Flags().BoolVar(&mineFlags.force, "force", false, "全量重挖（重置增量游标，兼审计）")
	mineCmd.Flags().BoolVar(&mineFlags.fromQueue, "from-queue", false, "只处理 Stop hook 入队的 transcript")
	mineCmd.Flags().BoolVar(&mineFlags.deep, "deep", false, "深度提取：LLM 语义补充召回（需 config llm 节 + REMIN_LLM_API_KEY；快速路径结果保留）")
	rootCmd.AddCommand(mineCmd)
}
