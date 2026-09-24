// Package cli 命令行面（人面）。FR-UI-3：全命令 --json 结构化输出，GUI/脚本唯一契约。
package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Version 产品版本（release 构建经 -ldflags -X 注入 tag 版本；源码构建回落此默认值）
var Version = "0.6.1"

var (
	jsonOut  bool
	rootPath string
)

// silentExit 已完成输出后的静默退出码（避免重复打印）
type SilentExit struct{ code int }

func (e SilentExit) Error() string { return fmt.Sprintf("exit %d", e.code) }

type envelope struct {
	OK    bool        `json:"ok"`
	Data  interface{} `json:"data,omitempty"`
	Error *errBody    `json:"error,omitempty"`
}

type errBody struct {
	Message string `json:"message"`
}

// output 输出统一入口：--json 时打结构化包络，否则由 human 渲染
func output(human func(), data interface{}) error {
	if jsonOut {
		return printJSON(envelope{OK: true, Data: data})
	}
	if human != nil {
		human()
	}
	return nil
}

func printJSON(v interface{}) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// fail 业务失败：json 打错误包络 / 人面打 stderr；退出码 1
func fail(err error) error {
	if jsonOut {
		_ = printJSON(envelope{OK: false, Error: &errBody{Message: err.Error()}})
		return SilentExit{1}
	}
	fmt.Fprintf(os.Stderr, "错误: %v\n", err)
	return SilentExit{1}
}

var rootCmd = &cobra.Command{
	Use:   "remin",
	Short: "Remin（随忆）：个人跨 agent 可信记忆层——换脑不换忆",
	Long: "Remin（随忆）——个人跨 agent 可信记忆层。\n" +
		"记忆以人类可读的 markdown 归你所有（~/.remin/ 个人 git 仓库，唯一真源），\n" +
		"提供来源溯源、冲突消解、信任分层与失效验证：宁可不知道，不能自信地错。",
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	rootCmd.PersistentFlags().StringVar(&rootPath, "root", "", "真源仓库位置（默认 $REMIN_HOME 或 ~/.remin）")
	rootCmd.PersistentFlags().BoolVar(&jsonOut, "json", false, "结构化 JSON 输出（GUI/脚本契约）")
}

// Execute CLI 入口
func Execute() error {
	rootCmd.SetFlagErrorFunc(func(c *cobra.Command, err error) error {
		fmt.Fprintln(os.Stderr, "用法错误:", err) // 用法错误退出码 2
		return SilentExit{2}
	})
	return rootCmd.Execute()
}

// Code 退出码
func (e SilentExit) Code() int { return e.code }
