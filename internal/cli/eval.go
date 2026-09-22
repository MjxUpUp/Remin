package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/remin-dev/remin/internal/core/eval"
	"github.com/spf13/cobra"
)

var evalFlags struct {
	suite string
	out   string
}

// eval 评测套件（规则可判定、模型无关），JSON 报告
var evalCmd = &cobra.Command{
	Use:   "eval",
	Short: "评测套件（remin eval run）",
}

var evalRunCmd = &cobra.Command{
	Use:   "run [--suite trust|roundtrip|all|parity-live] [--out file]",
	Short: "运行评测（沙盒 fixture，不碰真源数据；parity-live 需真实 agent CLI 在场）",
	RunE: func(cmd *cobra.Command, args []string) error {
		bin, _ := os.Executable() // parity 套件实跑 MCP stdio 通道需要真实二进制
		reps, err := eval.Run(evalFlags.suite, bin)
		if err != nil {
			return fail(err)
		}
		allPassed := true
		for _, r := range reps {
			if !r.Passed {
				allPassed = false
			}
		}
		if evalFlags.out != "" {
			data, _ := json.MarshalIndent(reps, "", "  ")
			if err := os.WriteFile(evalFlags.out, append(data, '\n'), 0o644); err != nil {
				return fail(err)
			}
		}
		if err := output(func() {
			for _, r := range reps {
				status := "通过 ✓"
				if !r.Passed {
					status = "失败 ✗"
				}
				fmt.Printf("[%s] %s（%s）\n", status, r.Suite, r.Duration)
				for _, c := range r.Checks {
					mark := "  ✓"
					if !c.Passed {
						mark = "  ✗"
					}
					fmt.Printf("%s %s — %s\n", mark, c.Name, c.Detail)
				}
			}
			if evalFlags.out != "" {
				fmt.Printf("报告已写入 %s\n", evalFlags.out)
			}
			if !allPassed {
				fmt.Println("存在失败项（退出码 1）")
			}
		}, reps); err != nil {
			return err
		}
		if !allPassed {
			return SilentExit{1} // 报告已输出；失败以退出码阻断脚本化使用
		}
		return nil
	},
}

func init() {
	evalRunCmd.Flags().StringVar(&evalFlags.suite, "suite", "all", "套件: trust/roundtrip/parity/conflict/budget/uplift/all；parity-live（opt-in，真实 agent 会话）")
	evalRunCmd.Flags().StringVar(&evalFlags.out, "out", "", "报告输出文件（JSON）")
	evalCmd.AddCommand(evalRunCmd)
	rootCmd.AddCommand(evalCmd)
}
