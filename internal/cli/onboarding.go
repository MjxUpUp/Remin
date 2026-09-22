package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/remin-dev/remin/internal/store"
)

// 触点透明（新用户反馈「不知道记忆存到哪」）：人面命令文本输出统一尾部真源路径。
// json 模式不加——结构化消费者自己知道 root。

// abbrevHome 路径 ~ 缩写（与用户心智一致：真源就在 ~/.remin）
func abbrevHome(p string) string {
	if h := store.HomeDir(); h != "" && strings.HasPrefix(p, h) {
		return "~" + p[len(h):]
	}
	return p
}

func printRootFooter(root string) {
	fmt.Printf("\n真源: %s\n", abbrevHome(root))
}

// stdinIsTTY stdin 是否为交互终端（向导/交互确认的触发条件；非 TTY 一切保持旧行为）。
// 判据为 char device（真终端）；Name() 恒为 /dev/stdin 无法区分 /dev/null，
// 由调用侧的「首读 EOF 即回落」兜底（见 initWizard）。测试可注入覆写。
var stdinIsTTY = func() bool {
	info, err := os.Stdin.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

var errGitIdentityMissing = errors.New("git 身份未配置")

// wizardEnv 向导依赖注入（测试不碰真终端/真检测）
type wizardEnv struct {
	DefaultRoot  string
	GitIdentity  func() (string, error)
	DetectAgents func() []string
}

// wizardResult 向导结论（init 命令据此执行后续动作）
type wizardResult struct {
	Root       string // 最终真源路径
	Autonomy   string // conservative | fast
	WireAgents bool   // 是否接线检测到的 agent
	Remote     string // 备份私有远端（空=跳过）
}

// runInitWizard 交互式初始化向导（lark-cli 式首用引导）。
// read 注入一行输入（回车=空串=取默认）；返回结论或中止原因。
// 各步默认值即产品立场：官方推荐路径回车直达。
func runInitWizard(read func() string, env wizardEnv) (*wizardResult, error) {
	res := &wizardResult{Root: env.DefaultRoot, Autonomy: "conservative", WireAgents: true}

	fmt.Println("── Remin 初始化向导 ──（每步回车取默认）")
	fmt.Printf("\n1/4 记忆真源位置（你的私有 git 仓库，记忆归你所有）\n  [%s] ", env.DefaultRoot)
	if in := strings.TrimSpace(read()); in != "" {
		res.Root = in
	}

	if _, err := env.GitIdentity(); err != nil {
		return nil, fmt.Errorf("git 身份未配置（审收动作需要可归因）：\n  git config --global user.name  \"你的名字\"\n  git config --global user.email \"你的邮箱\"\n配置后重跑 remin init 继续")
	}

	fmt.Println("\n2/4 审收自治档位")
	fmt.Println("  [1] 保守（推荐）：一切记忆人审后生效——宁可不知道，不能自信地错")
	fmt.Println("  [2] 快速：仅 7 天时效的会话 recap 自动生效（trust 仍标未验证），其余人审")
	fmt.Print("  选择 [1]: ")
	switch strings.TrimSpace(read()) {
	case "2":
		res.Autonomy = "fast"
	default:
		res.Autonomy = "conservative"
	}

	agents := env.DetectAgents()
	if len(agents) == 0 {
		fmt.Println("\n3/4 未检测到已安装的 agent（之后随时 remin doctor --install 接线）")
		res.WireAgents = false
	} else {
		fmt.Printf("\n3/4 检测到 agent: %s\n  是否现在接线（开场注入+会话挖矿+MCP）？[Y/n]: ", strings.Join(agents, ", "))
		switch strings.ToLower(strings.TrimSpace(read())) {
		case "n":
			res.WireAgents = false
		default:
			res.WireAgents = true
		}
	}

	fmt.Println("\n4/4 备份 / 多设备（可选）")
	fmt.Print("  私有远端仓库 url（回车跳过，之后 remin sync --set-remote <url>）: ")
	if url := strings.TrimSpace(read()); url != "" {
		fmt.Print("  ⚠ 记忆是高敏数据。确认该仓库为【私有】？[y/N]: ")
		if strings.ToLower(strings.TrimSpace(read())) != "y" {
			return nil, fmt.Errorf("远端未确认为私有仓库——为防泄露已中止（记忆只留在本机，随时可重新 init 配置）")
		}
		res.Remote = url
	}
	return res, nil
}
