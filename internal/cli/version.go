package cli

import (
	"fmt"
	"os"

	"github.com/remin-dev/remin/internal/core/upgrade"
	"github.com/remin-dev/remin/internal/store"
	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "版本与真源状态",
	RunE: func(cmd *cobra.Command, args []string) error {
		root := store.ResolveRoot(rootPath)
		data := map[string]interface{}{"version": Version, "root": root}
		if st, err := store.Open(root); err == nil {
			if v, err := st.Version(); err == nil {
				data["index_version"] = v
			}
			if ms, err := st.ListMemories(); err == nil {
				data["memories"] = len(ms)
			}
		} else {
			data["initialized"] = false
		}
		// 新版本轻提示：仅人面主动查询时出现（缓存 24h，失败静默——升级检查永不构成干扰）
		if os.Getenv("REMIN_NO_UPGRADE_CHECK") != "1" {
			if latest, err := upgrade.CachedLatest(root, upgrade.Registry()); err == nil && upgrade.IsNewer(latest, Version) {
				data["latest"] = latest
			}
		}
		return output(func() {
			fmt.Printf("remin %s\n真源: %s\n", Version, root)
			if v, ok := data["index_version"]; ok {
				fmt.Printf("索引版本: v%d  记忆: %d 条\n", v, data["memories"])
			} else {
				fmt.Println("真源未初始化（remin init）")
			}
			if latest, ok := data["latest"].(string); ok {
				fmt.Printf("有新版本 %s：remin upgrade\n", latest)
			}
		}, data)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
