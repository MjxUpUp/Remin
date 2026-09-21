package cli

import (
	"fmt"

	"github.com/remin-dev/remin/internal/core/audit"
	"github.com/remin-dev/remin/internal/core/verify"
	"github.com/spf13/cobra"
)

var verifyFlags struct {
	set string
}

// verify verify-condition 用前验证（原子回写；改变检索真值则版本 +1）
var verifyCmd = &cobra.Command{
	Use:   "verify <id|all>",
	Short: "verify-condition 用前验证（机器条件直判，自然语言待人裁 --set）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := mustStore()
		if err != nil {
			return fail(err)
		}
		outcomes, err := verify.Run(st, audit.New(st), args[0], verifyFlags.set)
		if err != nil {
			return fail(err)
		}
		return output(func() {
			for _, o := range outcomes {
				label := map[string]string{"passed": "通过", "failed": "失效（退出检索）", "unknown": "待人判"}[o.Result]
				fmt.Printf("%s  %s  %s\n", o.ID, label, o.Evidence)
				if o.Changed {
					fmt.Printf("    ↳ 检索真值已变，版本推进\n")
				}
			}
		}, outcomes)
	},
}

func init() {
	verifyCmd.Flags().StringVar(&verifyFlags.set, "set", "", "人判覆盖：passed|failed（自然语言条件的人工裁决）")
	rootCmd.AddCommand(verifyCmd)
}
