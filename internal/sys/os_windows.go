//go:build windows

package sys

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// defaultPath is searched for a bare program name when the child's
// environment has no PATH.
const defaultPath = `C:\Windows\System32;C:\Windows`

// pathSeparators are the characters that make a program name a path.
const pathSeparators = `/\:`

// sameKey reports whether two environment keys name the same variable.
// Windows keys are case-insensitive. Pure.
func sameKey(a, b string) bool {
	return strings.EqualFold(a, b)
}

// pathExts returns the file-name suffixes tried for a bare program name: the
// name alone (isExecutable accepts it only when it has an extension), then
// each PATHEXT entry of env, or the default list when env has no PATHEXT.
// Pure.
func pathExts(env []string) []string {
	list, ok := envValue(env, "PATHEXT")
	if !ok {
		list = ".COM;.EXE;.BAT;.CMD"
	}
	exts := []string{""}
	for _, e := range strings.Split(list, ";") {
		if e != "" {
			exts = append(exts, strings.ToLower(e))
		}
	}
	return exts
}

// isExecutable reports whether path is a regular file. Impure: it touches
// the real filesystem.
func isExecutable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && filepath.Ext(path) != ""
}

// isolateGroup puts the child in its own process group. Cancellation uses
// the default Cmd.Cancel, which kills the direct child. Impure: it configures
// process behavior of cmd.
func isolateGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}
