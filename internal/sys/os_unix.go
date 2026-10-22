//go:build unix

package sys

import (
	"os"
	"os/exec"
	"syscall"
)

// defaultPath is searched for a bare program name when the child's
// environment has no PATH. It is the POSIX execvp default.
const defaultPath = "/usr/bin:/bin"

// pathSeparators are the characters that make a program name a path.
const pathSeparators = "/"

// sameKey reports whether two environment keys name the same variable.
// Unix keys are case-sensitive. Pure.
func sameKey(a, b string) bool {
	return a == b
}

// pathExts returns the file-name suffixes tried for a bare program name.
// Unix tries the name alone. Pure.
func pathExts([]string) []string {
	return []string{""}
}

// isExecutable reports whether path is a regular file with an execute bit
// set. Impure: it touches the real filesystem.
func isExecutable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}

// fatalSignals are the signals upstream treats as fatal.
func fatalSignals() []os.Signal {
	return []os.Signal{syscall.SIGHUP, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM}
}

// forwardSignal sends signal sig to the process pid; a child that is gone
// already is no failure. Impure: it signals a process.
func forwardSignal(pid, sig int) {
	_ = syscall.Kill(pid, syscall.Signal(sig))
}

// isolateGroup puts the child in its own process group and makes
// cancellation kill that whole group. Impure: it configures process and
// signal behavior of cmd.
func isolateGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
