package cli

import (
	"fmt"

	"github.com/remin-dev/remin/internal/core/importer"
	"github.com/remin-dev/remin/internal/store"
	"github.com/spf13/cobra"
)

var importFlags struct {
	from  string
	path  string
	apply bool
}

// import 从既有产品迁移（默认 dry-run；--apply 生成 inbox 批次；幂等只报增量）
var importCmd = &cobra.Command{
	Use:   "import [--from 来源] [--path 路径] [--apply]",
	Short: "从既有记忆产品迁移（五来源；默认 dry-run，全程只读，原库不动）",
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := mustStore()
		if err != nil {
			return fail(err)
		}
		home := store.HomeDir()
		sources := []string{importFlags.from}
		if importFlags.from == "" {
			sources = importer.AllSources
		}
		var reports []*importer.Report
		var applied int
		for _, src := range sources {
			rep, err := importer.Import(st, src, importFlags.path, importFlags.apply, home)
			if err != nil {
				if importFlags.from == "" {
					continue // 全扫描：缺 --path 的来源（chatgpt/markdown-dir）跳过不报错
				}
				return fail(err)
			}
			reports = append(reports, rep)
			if rep.Batch != "" {
				applied++
			}
		}
		return output(func() {
			mode := "[dry-run] "
			if importFlags.apply {
				mode = ""
			}
			for _, rep := range reports {
				if rep.Found == 0 {
					continue
				}
				fmt.Printf("%s%s：发现 %d 条，新增 %d（跳过已导入 %d，与库内相似 %d）\n",
					mode, rep.Source, rep.Found, rep.NewItems, rep.Skipped, rep.Duplicates)
				for _, p := range rep.Preview {
					fmt.Printf("  · %s\n", p)
				}
				if rep.Batch != "" {
					fmt.Printf("  批次 %s 已进 inbox：remin inbox --batch %s\n", rep.Batch, rep.Batch)
				}
			}
			if importFlags.from == "" && importFlags.path == "" {
				fmt.Println("（chatgpt-export / markdown-dir 需要 --path 才会扫描）")
			}
			if !importFlags.apply {
				fmt.Println("确认迁移: 追加 --apply（候选进 inbox，人审后才生效）")
			}
		}, reports)
	},
}

func init() {
	importCmd.Flags().StringVar(&importFlags.from, "from", "", "来源: "+fmt.Sprint(importer.AllSources))
	importCmd.Flags().StringVar(&importFlags.path, "path", "", "来源路径（chatgpt-export JSON / markdown-dir 目录）")
	importCmd.Flags().BoolVar(&importFlags.apply, "apply", false, "生成 inbox 批次（默认 dry-run 只读）")
	rootCmd.AddCommand(importCmd)
}
