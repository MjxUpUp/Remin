package cli

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"

	"github.com/remin-dev/remin/internal/store"
	"github.com/spf13/cobra"
)

// mcpRunner 由 cmd/remin 装配层注入（internal/protocol.Run）。
// 注入而非直接 import：N1 要求 internal/cli 零 agent SDK 依赖，
// MCP 官方 SDK 仅允许 internal/protocol 引用（ADR-0004）。
var mcpRunner func(ctx context.Context, root string) error

// SetMCPRunner 装配 MCP stdio server 启动函数
func SetMCPRunner(f func(ctx context.Context, root string) error) { mcpRunner = f }

var errNotWired = errors.New("MCP server 未装配（请用 cmd/remin 入口）")

// mcp MCP stdio server（客户端拉起；别名 memory，五工具，无 write）
var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "MCP stdio server（由客户端拉起；别名 memory，五工具，无 write）",
	RunE: func(cmd *cobra.Command, args []string) error {
		if mcpRunner == nil {
			return fail(errNotWired)
		}
		root := store.ResolveRoot(rootPath)
		if _, err := store.Open(root); err != nil {
			return fail(err)
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := mcpRunner(ctx, root); err != nil {
			return fail(err)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(mcpCmd)
}
