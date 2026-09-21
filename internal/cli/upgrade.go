package cli

import (
	"fmt"

	"github.com/remin-dev/remin/internal/core/upgrade"
	"github.com/remin-dev/remin/internal/store"
	"github.com/spf13/cobra"
)

var upgradeFlags struct {
	check bool
}

// upgrade 自更新：查 npm registry（@reminmem）→ 校验 tarball → 原子替换落位二进制。
// 渠道副本（npm 全局目录/brew）不参与运行时；落位不存在时指引 doctor --install。
var upgradeCmd = &cobra.Command{
	Use:   "upgrade [--check]",
	Short: "升级落位二进制（npm registry → 完整性校验 → 原位原子替换）",
	RunE: func(cmd *cobra.Command, args []string) error {
		root := store.ResolveRoot(rootPath)
		reg := upgrade.Registry()
		if upgradeFlags.check {
			latest, err := upgrade.CheckLatest(reg)
			if err != nil {
				return fail(fmt.Errorf("查询最新版本失败: %w", err))
			}
			state := "已是最新"
			if upgrade.IsNewer(latest, Version) {
				state = "有新版本，remin upgrade 升级"
			}
			data := map[string]any{"current": Version, "latest": latest, "newer": upgrade.IsNewer(latest, Version)}
			return output(func() {
				fmt.Printf("当前 %s  最新 %s  %s\n", Version, latest, state)
			}, data)
		}
		res, err := upgrade.Run(upgrade.Options{Root: root, Registry: reg, CurrentVersion: Version})
		if err != nil {
			return fail(err)
		}
		return output(func() {
			if res.Updated {
				fmt.Printf("已升级 %s → %s（%s）\n新二进制自下次会话生效（运行中的旧进程不受影响）。\n", res.Current, res.Latest, res.Target)
			} else {
				fmt.Printf("已是最新（%s）\n", res.Current)
			}
		}, res)
	},
}

func init() {
	upgradeCmd.Flags().BoolVar(&upgradeFlags.check, "check", false, "只检查新版本，不升级")
	rootCmd.AddCommand(upgradeCmd)
}
