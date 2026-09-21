package cli

import (
	"fmt"

	"github.com/remin-dev/remin/internal/store"
	"github.com/spf13/cobra"
)

// mustStore 打开已初始化的真源仓库
func mustStore() (*store.Store, error) {
	return store.Open(store.ResolveRoot(rootPath))
}

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "创建记忆真源仓库（~/.remin 个人 git 仓库）",
	RunE: func(cmd *cobra.Command, args []string) error {
		root := store.ResolveRoot(rootPath)
		st, err := store.Init(root)
		if err != nil {
			return fail(err)
		}
		return output(func() {
			fmt.Printf("真源仓库已建立: %s（VERSION=0）\n", st.Root)
			fmt.Println("下一步: remin doctor --install 一键接线各 agent")
		}, map[string]interface{}{"root": st.Root, "version": 0})
	},
}

func init() {
	rootCmd.AddCommand(initCmd)
}
