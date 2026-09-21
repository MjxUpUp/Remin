// remin 入口：装配 CLI 命令与协议层（三层架构的最外薄壳）。
// MCP SDK 只经 internal/protocol 进入，由装配层注入 cli——核心层零 agent SDK（P1-N1）。
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/remin-dev/remin/internal/cli"
	"github.com/remin-dev/remin/internal/protocol"
)

func main() {
	cli.SetMCPRunner(protocol.Run)
	if err := cli.Execute(); err != nil {
		var se cli.SilentExit
		if errors.As(err, &se) {
			os.Exit(se.Code())
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
