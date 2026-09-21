package cli

import (
	"fmt"
	"os"

	"github.com/remin-dev/remin/internal/core/doctor"
	"github.com/remin-dev/remin/internal/store"
	"github.com/spf13/cobra"
)

var doctorFlags struct {
	install  bool
	takeover bool
}

// doctor 检测已装 agent / 一键接线（写前备份）/ 健康检查 / 接管同名 memory server
var doctorCmd = &cobra.Command{
	Use:   "doctor [--install] [--takeover]",
	Short: "检测已装 agent、健康检查；--install 一键接线（repo 零污染）",
	RunE: func(cmd *cobra.Command, args []string) error {
		bin, err := os.Executable()
		if err != nil {
			return fail(err)
		}
		home := store.HomeDir()
		type report struct {
			Health map[string]interface{} `json:"health"`
			Agents []doctor.AgentStatus   `json:"agents"`
			Wired  []doctor.AgentStatus   `json:"wired,omitempty"`
		}
		rep := report{Health: map[string]interface{}{}, Agents: doctor.Detect(home, bin)}
		root := store.ResolveRoot(rootPath)
		if st, err := store.Open(root); err == nil {
			rep.Health["store"] = root
			if v, err := st.Version(); err == nil {
				rep.Health["index_version"] = v
			}
			if _, err := store.GitHasIdentity(root); err == nil {
				rep.Health["git_identity"] = true
			} else {
				rep.Health["git_identity"] = false
				rep.Health["note"] = "git 身份未配置：git config --global user.name/user.email"
			}
		} else {
			rep.Health["store"] = "未初始化（remin init）"
		}

		if doctorFlags.install {
			wired, err := doctor.Install(home, bin, doctorFlags.takeover)
			if err != nil {
				return fail(err)
			}
			rep.Wired = wired
		}
		return output(func() {
			fmt.Printf("真源: %v\n", rep.Health["store"])
			if v, ok := rep.Health["index_version"]; ok {
				fmt.Printf("索引: v%v\n", v)
			}
			if ok, _ := rep.Health["git_identity"].(bool); !ok {
				fmt.Printf("⚠ %v\n", rep.Health["note"])
			}
			fmt.Println("agent 检测：")
			for _, a := range rep.Agents {
				mark := "未安装"
				if a.Installed {
					mark = "已安装·未接线"
					if a.Wired {
						mark = "已接线 ✓"
					} else if a.Conflicted {
						mark = "已安装·同名 memory server 冲突（--takeover 接管）"
					}
				}
				fmt.Printf("  %-12s %s\n", a.Agent, mark)
			}
			if len(rep.Wired) > 0 {
				fmt.Println("已接线（写前已备份原配置）：")
				for _, w := range rep.Wired {
					fmt.Printf("  %-12s MCP memory + 会话 hooks\n", w.Agent)
				}
			} else if doctorFlags.install {
				fmt.Println("（没有已安装的 agent 可接线）")
			} else {
				fmt.Println("一键接线: remin doctor --install")
			}
		}, rep)
	},
}

func init() {
	doctorCmd.Flags().BoolVar(&doctorFlags.install, "install", false, "写入各 agent 全局配置（MCP + hooks；写前备份）")
	doctorCmd.Flags().BoolVar(&doctorFlags.takeover, "takeover", false, "接管同名 memory server")
	rootCmd.AddCommand(doctorCmd)
}
