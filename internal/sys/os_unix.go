//go:build unix

package sys

import (
	"os/exec"
	"syscall"
)

// isolateGroup puts the child in its own process group and makes
// cancellation kill that whole group.
func isolateGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
