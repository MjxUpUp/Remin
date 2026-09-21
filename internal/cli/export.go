package cli

import (
	"fmt"

	"github.com/remin-dev/remin/internal/core/exporter"
	"github.com/remin-dev/remin/internal/core/syncpkg"
	"github.com/spf13/cobra"
)

var exportFlags struct {
	out string
}

// export 全量导出（sha256 清单；含 supersession 历史与 provenance）
var exportCmd = &cobra.Command{
	Use:   "export --out <dir>",
	Short: "全量导出（含全部历史与审计；roundtrip 哈希一致）",
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := mustStore()
		if err != nil {
			return fail(err)
		}
		if exportFlags.out == "" {
			return fail(fmt.Errorf("需要 --out <dir>"))
		}
		m, err := exporter.Export(st, exportFlags.out)
		if err != nil {
			return fail(err)
		}
		return output(func() {
			fmt.Printf("已导出 %d 个文件到 %s（v%d；MANIFEST.json 含 sha256 清单）\n",
				len(m.Files), exportFlags.out, m.Version)
		}, m)
	},
}

var restoreFlags struct {
	bundle string
	check  bool
}

// restore 整库还原（唯一绕过 inbox 的通道——还原的是已人审的库）
var restoreCmd = &cobra.Command{
	Use:   "restore --bundle <dir> [--check]",
	Short: "从导出物整库还原（先验哈希；VERSION 取 max）；--check 只验不还原",
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := mustStore()
		if err != nil {
			return fail(err)
		}
		if restoreFlags.bundle == "" {
			return fail(fmt.Errorf("需要 --bundle <dir>（remin export 的输出目录）"))
		}
		if restoreFlags.check {
			m, err := exporter.VerifyBundle(restoreFlags.bundle)
			if err != nil {
				return fail(err)
			}
			return output(func() {
				fmt.Printf("导出物完整（v%d，%d 文件哈希一致）\n", m.Version, len(m.Files))
			}, map[string]interface{}{"verified": true, "version": m.Version, "files": len(m.Files)})
		}
		if err := exporter.Restore(st, restoreFlags.bundle); err != nil {
			return fail(err)
		}
		v, _ := st.Version()
		return output(func() {
			fmt.Printf("整库还原完成（当前 v%d）\n", v)
		}, map[string]interface{}{"restored": true, "version": v})
	},
}

var syncFlags struct {
	setRemote string
	pull      bool
	push      bool
}

// sync 多设备同步（git push/pull；远端仅托管，真源永在本地）
var syncCmd = &cobra.Command{
	Use:   "sync [--set-remote <url>] [--pull] [--push]",
	Short: "多设备同步（git push/pull 包装；VERSION 冲突自动取 max）",
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := mustStore()
		if err != nil {
			return fail(err)
		}
		if syncFlags.setRemote != "" {
			if err := syncpkg.SetRemote(st, syncFlags.setRemote); err != nil {
				return fail(err)
			}
			return output(func() {
				fmt.Printf("同步远端已设置: %s\n", syncFlags.setRemote)
			}, map[string]interface{}{"remote": syncFlags.setRemote})
		}
		var out string
		switch {
		case syncFlags.pull:
			out, err = syncpkg.Pull(st)
		case syncFlags.push:
			out, err = syncpkg.Push(st)
		default:
			out, err = syncpkg.Sync(st)
		}
		if err != nil {
			return fail(err)
		}
		v, _ := st.Version()
		return output(func() {
			fmt.Printf("同步完成（v%d）\n%s", v, out)
		}, map[string]interface{}{"version": v, "output": out})
	},
}

func init() {
	exportCmd.Flags().StringVar(&exportFlags.out, "out", "", "导出目录")
	rootCmd.AddCommand(exportCmd)

	restoreCmd.Flags().StringVar(&restoreFlags.bundle, "bundle", "", "导出物目录")
	restoreCmd.Flags().BoolVar(&restoreFlags.check, "check", false, "只校验导出物哈希，不还原")
	rootCmd.AddCommand(restoreCmd)

	syncCmd.Flags().StringVar(&syncFlags.setRemote, "set-remote", "", "设置远端 git url")
	syncCmd.Flags().BoolVar(&syncFlags.pull, "pull", false, "仅拉取")
	syncCmd.Flags().BoolVar(&syncFlags.push, "push", false, "仅推送")
	rootCmd.AddCommand(syncCmd)
}
