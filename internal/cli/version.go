package cli

import (
	"fmt"

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
		return output(func() {
			fmt.Printf("remin %s\n真源: %s\n", Version, root)
			if v, ok := data["index_version"]; ok {
				fmt.Printf("索引版本: v%d  记忆: %d 条\n", v, data["memories"])
			} else {
				fmt.Println("真源未初始化（remin init）")
			}
		}, data)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
