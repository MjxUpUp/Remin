package cli

import (
	"fmt"
	"os"

	"github.com/remin-dev/remin/internal/core/view"
	"github.com/spf13/cobra"
)

var viewFlags struct {
	write string
	facet string
}

// view AGENTS.md 形态投影（默认预览 stdout；显式 --write 才落盘）
var viewCmd = &cobra.Command{
	Use:   "view",
	Short: "AGENTS.md 形态视图投影（可重建；--write 显式 opt-in 才落 repo）",
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := mustStore()
		if err != nil {
			return fail(err)
		}
		text, err := view.AGENTS(st, viewFlags.facet)
		if err != nil {
			return fail(err)
		}
		if viewFlags.write == "" {
			return output(func() { fmt.Print(text) }, map[string]interface{}{"content": text})
		}
		if err := os.WriteFile(viewFlags.write, []byte(text), 0o644); err != nil {
			return fail(err)
		}
		return output(func() {
			fmt.Printf("已写入 %s（真源投影；可随时删除重建）\n", viewFlags.write)
		}, map[string]interface{}{"written": viewFlags.write})
	},
}

func init() {
	viewCmd.Flags().StringVar(&viewFlags.write, "write", "", "落盘路径（显式 opt-in）")
	viewCmd.Flags().StringVar(&viewFlags.facet, "facet", "", "分面过滤")
	rootCmd.AddCommand(viewCmd)
}
