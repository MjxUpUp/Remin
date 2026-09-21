package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/remin-dev/remin/internal/core/config"
	"github.com/remin-dev/remin/internal/core/inject"
	"github.com/remin-dev/remin/internal/core/miner"
	"github.com/remin-dev/remin/internal/store"
	"github.com/spf13/cobra"
)

var injectFlags struct {
	facet    string
	budgetMs int
	maxLines int
}

// inject 会话开场注入（hook 入口）：追赶（硬预算）→ 快照 → 精简索引 + recap 提示。
// 永不失败、永不非零退出（hook 安全）。
var injectCmd = &cobra.Command{
	Use:   "inject",
	Short: "会话开场注入（SessionStart hook 入口；永不失败）",
	RunE: func(cmd *cobra.Command, args []string) error {
		defer func() {
			if r := recover(); r != nil {
				fmt.Println("# Remin 记忆索引（注入异常，已降级为空注入）")
			}
		}()
		root := store.ResolveRoot(rootPath)
		st, err := store.Open(root)
		if err != nil {
			fmt.Println("# Remin：真源未初始化（remin init），本次不注入")
			return nil // 永不非零
		}
		cfg, errCfg := config.Load(st.ConfigPath())
		if errCfg != nil {
			cfg = config.Default() // 配置损坏降级默认值（hook 永不失败）
		}
		facet := injectFlags.facet
		if facet == "" {
			facet = cfg.InjectFacet // config.yaml inject_facet（默认 dev；工具绑定待 OP）
		}
		res, err := inject.Run(st, inject.Options{
			Facet:    facet,
			MaxLines: injectFlags.maxLines,
			Budget:   time.Duration(injectFlags.budgetMs) * time.Millisecond,
			Drain: func(ctx context.Context) (int, error) {
				return miner.Drain(ctx, st, cfg, miner.DefaultClaudeDir())
			},
		})
		if err != nil {
			fmt.Println("# Remin：注入生成失败，已降级为空注入（会话不受影响）")
			return nil
		}
		fmt.Print(res.Text)
		return nil
	},
}

func init() {
	injectCmd.Flags().StringVar(&injectFlags.facet, "facet", "", "分面（默认 dev）")
	injectCmd.Flags().IntVar(&injectFlags.budgetMs, "budget-ms", 800, "开场追赶硬预算（毫秒，超时降级异步）")
	injectCmd.Flags().IntVar(&injectFlags.maxLines, "max-lines", inject.DefaultMaxLines, "注入索引行数预算")
	rootCmd.AddCommand(injectCmd)
}
