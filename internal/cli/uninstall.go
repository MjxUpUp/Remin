package cli

import (
	"fmt"

	"github.com/remin-dev/remin/internal/core/doctor"
	"github.com/remin-dev/remin/internal/store"
	"github.com/spf13/cobra"
)

var uninstallFlags struct {
	purge bool
}

// uninstall 卸载：按接线台账回放摘除（cordis 可逆），清备份/落位/台账；
// 真源默认保留（记忆是用户资产），--purge 显式 opt-in 才删。
var uninstallCmd = &cobra.Command{
	Use:   "uninstall [--purge]",
	Short: "卸载 remin：摘除全部接线（按台账精确回放），默认保留记忆真源",
	RunE: func(cmd *cobra.Command, args []string) error {
		home := store.HomeDir()
		root := store.ResolveRoot(rootPath)
		rep, err := doctor.Uninstall(home, root, uninstallFlags.purge)
		if err != nil {
			return fail(err)
		}
		return output(func() {
			fmt.Printf("已摘除接线 %d 处：\n", len(rep.Removed))
			for _, r := range rep.Removed {
				fmt.Printf("  - %s\n", r)
			}
			if len(rep.Skipped) > 0 {
				fmt.Println("⚠ 以下项被跳过（与台账记录不符，请手工检查）：")
				for _, s := range rep.Skipped {
					fmt.Printf("  - %s %s：%s\n", s.File, s.Key, s.Reason)
				}
			}
			if rep.Heuristic {
				fmt.Println("（台账缺失，已按启发式扫描摘除——匹配保守，请复查 agent 配置）")
			}
			fmt.Printf("已清理备份 %d 份\n", rep.BackupsCleaned)
			if rep.BinRemoved != "" {
				fmt.Printf("已删除落位二进制：%s\n", rep.BinRemoved)
			}
			if rep.StoreRemoved {
				fmt.Printf("已删除记忆真源：%s（%d 条记忆）\n", rep.StoreRoot, rep.MemoriesBeforePurge)
			} else {
				fmt.Printf("记忆真源已保留：%s（记忆是你的资产；彻底清除请用 remin uninstall --purge）\n", rep.StoreRoot)
			}
			if rep.StoreRemoved {
				fmt.Println("remin 已完全卸载。")
			} else {
				fmt.Println("remin 接线已完全摘除；如需重装：remin doctor --install")
			}
		}, rep)
	},
}

func init() {
	uninstallCmd.Flags().BoolVar(&uninstallFlags.purge, "purge", false, "连同记忆真源 ~/.remin 一并删除（不可恢复）")
	rootCmd.AddCommand(uninstallCmd)
}
