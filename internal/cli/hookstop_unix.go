//go:build !windows

package cli

import (
	"os/exec"
	"syscall"
)

// setDetached 分离进程组（unix）：父进程退出不影响挖矿子进程
func setDetached(c *exec.Cmd) {
	if c.SysProcAttr == nil {
		c.SysProcAttr = &syscall.SysProcAttr{}
	}
	c.SysProcAttr.Setpgid = true
}
