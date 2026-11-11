//go:build !windows

package hostclient

import (
	"os/exec"
	"syscall"
)

// detach puts the host in its own session so it outlives this process.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
