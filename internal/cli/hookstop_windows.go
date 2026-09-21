//go:build windows

package cli

import "os/exec"

func setDetached(c *exec.Cmd) {}
