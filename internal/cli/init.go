package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"

	"github.com/remin-dev/remin/internal/core/config"
	"github.com/remin-dev/remin/internal/core/doctor"
	"github.com/remin-dev/remin/internal/core/syncpkg"
	"github.com/remin-dev/remin/internal/store"
	"github.com/spf13/cobra"
)

// mustStore 打开已初始化的真源仓库
func mustStore() (*store.Store, error) {
	return store.Open(store.ResolveRoot(rootPath))
}

var initFlags struct {
	defaults bool // 跳过向导（脚本/CI；非 TTY 自动走此路径）
}

// init 创建记忆真源仓库；TTY 下进入交互式向导（路径/自治档/agent 接线/备份远端），
// 非 TTY、--defaults 或零输入（如 stdin=/dev/null）回落直通行为（逐字节兼容）。
var initCmd = &cobra.Command{
	Use:   "init [--defaults]",
	Short: "创建记忆真源仓库（~/.remin 个人 git 仓库；TTY 下交互式向导）",
	RunE: func(cmd *cobra.Command, args []string) error {
		root := store.ResolveRoot(rootPath)
		if !initFlags.defaults && stdinIsTTY() {
			return initWizard(root)
		}
		return initPlain(root)
	},
}

func initPlain(root string) error {
	st, err := store.Init(root)
	if err != nil {
		return fail(err)
	}
	return output(func() {
		fmt.Printf("真源仓库已建立: %s（VERSION=0）\n", st.Root)
		fmt.Println("下一步: remin doctor --install 一键接线各 agent")
	}, map[string]interface{}{"root": st.Root, "version": 0})
}

// initWizard 向导主流程：问询 → 建库 → 落选择（autonomy/接线/远端）→ 收尾示例。
// 零输入回落：stdin 非/真终端（如 </dev/null）时全部读为 EOF——向导输出（已在
// 缓冲中）整体丢弃并回落直通路径，输出与旧版逐字节一致，防脚本被默认值劫持。
func initWizard(defaultRoot string) error {
	sc := bufio.NewScanner(os.Stdin)
	sawInput := false
	read := func() string {
		if sc.Scan() {
			sawInput = true
			return sc.Text()
		}
		return ""
	}
	// 向导提示先入缓冲（真交互才回放）
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		return fail(err)
	}
	os.Stdout = w
	res, werr := runInitWizard(read, wizardEnv{
		DefaultRoot:  defaultRoot,
		GitIdentity:  func() (string, error) { return store.GitHasIdentity(defaultRoot) },
		DetectAgents: detectAgentNames(defaultRoot),
	})
	w.Close()
	os.Stdout = oldStdout
	bufed, _ := io.ReadAll(r)
	if werr != nil {
		os.Stdout.Write(bufed)
		return fail(werr)
	}
	if !sawInput {
		return initPlain(res.Root)
	}
	os.Stdout.Write(bufed)

	st, err := store.Init(res.Root)
	if err != nil {
		return fail(err)
	}
	// 落 autonomy 选择
	if res.Autonomy == config.AutonomyFast {
		if cfg, err := config.Load(st.ConfigPath()); err == nil {
			cfg.Autonomy = config.AutonomyFast
			_ = cfg.Save(st.ConfigPath())
		}
	}
	// 接线（复用 doctor：落位稳定路径 + 写各 agent 全局配置 + 台账）
	if res.WireAgents {
		if bin, err := os.Executable(); err == nil {
			if stable, err := doctor.Stage(st.Root, bin); err == nil {
				_, _ = doctor.Install(store.HomeDir(), st.Root, stable, false)
			}
		}
	}
	// 备份远端（向导内已确认私有）
	if res.Remote != "" {
		_ = syncpkg.SetRemote(st, res.Remote)
	}
	return output(func() {
		fmt.Printf("\n真源仓库已建立: %s（VERSION=0）\n", st.Root)
		if res.WireAgents {
			fmt.Println("已接线: 开场注入 + 会话挖矿 + MCP（remin doctor 查看详情；remin uninstall 可摘净）")
		} else {
			fmt.Println("未接线 agent：随时 remin doctor --install")
		}
		if res.Autonomy == config.AutonomyFast {
			fmt.Println("自治档位: 快速（recap 自动生效，trust 仍标未验证；改回: config autonomy=conservative）")
		}
		if res.Remote != "" {
			fmt.Printf("备份远端: %s（remin sync --push 首推）\n", res.Remote)
		} else {
			fmt.Println("备份（可选）: remin sync --set-remote <私有仓库 url> --yes")
		}
		fmt.Println("\n日常闭环：")
		fmt.Println("  remin mine          # 会话结束后挖矿（默认只挖近 7 天，--full-history 全量）")
		fmt.Println("  remin inbox         # 审收（--type 分诊；一键清 recap: reject --type episodic）")
		fmt.Println("  remin search \"关键词\"  # 检索（trust/来源随行）")
		fmt.Printf("\n真源: %s（记忆归你所有；remin export 任意时刻全量导出）\n", abbrevHome(st.Root))
	}, map[string]interface{}{"root": st.Root, "version": 0, "wizard": map[string]string{
		"autonomy": res.Autonomy, "wired": fmt.Sprintf("%v", res.WireAgents), "remote": res.Remote,
	}})
}

// detectAgentNames 向导用的 agent 检测（落位过用稳定路径，否则用当前二进制——确认接线后才真正落位）
func detectAgentNames(root string) func() []string {
	return func() []string {
		bin, err := os.Executable()
		if err != nil {
			return nil
		}
		stable := doctor.StablePath(root)
		if _, err := os.Stat(stable); err != nil {
			stable = bin
		}
		var names []string
		for _, a := range doctor.Detect(store.HomeDir(), stable) {
			if a.Installed {
				names = append(names, a.Agent)
			}
		}
		return names
	}
}

func init() {
	initCmd.Flags().BoolVar(&initFlags.defaults, "defaults", false, "跳过交互向导（脚本/CI；非 TTY 自动跳过）")
	rootCmd.AddCommand(initCmd)
}
