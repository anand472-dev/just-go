//go:build windows

package sys

import (
	"os/exec"
	"syscall"
)

// isolateGroup puts the child in its own process group. Cancellation uses
// the default Cmd.Cancel, which kills the direct child.
func isolateGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}
