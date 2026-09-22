package cli

import (
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"time"

	"github.com/remin-dev/remin/internal/webui"
	"github.com/spf13/cobra"
)

var uiFlags struct {
	port   int
	noOpen bool
}

// ui 本地审收 Web 界面：仅绑 127.0.0.1（包裹核心包，GUI 只是皮肤不引入新核心能力）
var uiCmd = &cobra.Command{
	Use:   "ui [--port N] [--no-open]",
	Short: "本地审收 Web 界面（127.0.0.1：批次/候选/采纳/拒绝/检索）",
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := mustStore()
		if err != nil {
			return fail(err)
		}
		addr := "127.0.0.1:0"
		if uiFlags.port > 0 {
			addr = fmt.Sprintf("127.0.0.1:%d", uiFlags.port)
		}
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return fail(err)
		}
		url := fmt.Sprintf("http://%s", ln.Addr().String())
		fmt.Printf("Remin 审收界面：%s（仅本机回环；Ctrl-C 退出）\n真源: %s\n", url, st.Root)
		if !uiFlags.noOpen {
			openBrowser(url)
		}
		srv := &http.Server{
			Handler:           webui.Handler(st),
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       30 * time.Second,
			IdleTimeout:       120 * time.Second,
		}
		if err := srv.Serve(ln); err != nil {
			return fail(err)
		}
		return nil
	},
}

// openBrowser 尽力打开（失败安静——URL 已打印）
func openBrowser(url string) {
	var c *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		c = exec.Command("open", url)
	case "windows":
		c = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		c = exec.Command("xdg-open", url)
	}
	_ = c.Start()
}

func init() {
	uiCmd.Flags().IntVar(&uiFlags.port, "port", 0, "端口（缺省随机可用端口）")
	uiCmd.Flags().BoolVar(&uiFlags.noOpen, "no-open", false, "不自动打开浏览器")
	rootCmd.AddCommand(uiCmd)
}
