//go:build windows

package hostclient

import (
	"os/exec"
	"syscall"
)

const detachedProcess = 0x00000008

// detach starts the host without a console, in its own process group.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | detachedProcess}
}
