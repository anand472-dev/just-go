package sys

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
)

// killGrace bounds how long Run waits for output pipes to drain after a
// cancelled child has been killed.
const killGrace = 2 * time.Second

// OSFS is the FS backed by the real filesystem.
type OSFS struct {
	// goos names the operating system that decides where MkdirTemp puts a
	// directory it is given no place for, as runtime.GOOS does. It is empty for
	// the system the program runs on, so only a test sets it.
	goos string
}

// ReadFile reads the named file. Impure: it touches the real filesystem.
func (OSFS) ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// IsFile reports whether path is an existing regular file. A missing path is
// not an error. Impure: it touches the real filesystem.
func (OSFS) IsFile(path string) (bool, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return info.Mode().IsRegular(), nil
}

// ReadDir returns the sorted names of the entries in the directory at path.
// Impure: it touches the real filesystem.
func (OSFS) ReadDir(path string) ([]string, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}
	return names, nil
}

// WriteFile writes the named file. Impure: it touches the real filesystem.
func (OSFS) WriteFile(path string, data []byte, perm fs.FileMode) error {
	return os.WriteFile(path, data, perm)
}

// MkdirTemp creates a directory in dir, or, for an empty dir, in the place
// upstream makes its scripts: the directory called just in the Linux runtime
// directory ($XDG_RUNTIME_DIR), made if need be, or else the system's
// temporary directory. Impure: it touches the real filesystem and reads the
// environment.
func (o OSFS) MkdirTemp(dir, prefix string) (string, error) {
	if dir == "" {
		goos := o.goos
		if goos == "" {
			goos = runtime.GOOS
		}
		dir = os.TempDir()
		if root := runtimeRoot(goos, os.Getenv("XDG_RUNTIME_DIR")); root != "" {
			dir = root
			if err := os.MkdirAll(root, 0o700); err != nil {
				return "", err
			}
		}
	}
	return os.MkdirTemp(dir, prefix)
}

// runtimeRoot is the directory that holds the scripts of a system whose
// runtime directory is xdg, the value of $XDG_RUNTIME_DIR: the directory called
// just in it, on Linux when xdg is an absolute path. It is "" for a system that
// has none. Pure.
func runtimeRoot(goos, xdg string) string {
	if goos != "linux" || !filepath.IsAbs(xdg) {
		return ""
	}
	return filepath.Join(xdg, "just")
}

// RemoveAll removes path and what is below it. Impure: it touches the real
// filesystem.
func (OSFS) RemoveAll(path string) error {
	return os.RemoveAll(path)
}

// OSProcess is the Process backed by os/exec.
//
// Without Signals, as a library uses it, each child runs in its own process
// group and cancelling the context kills that whole group. With Signals, as
// the command line uses it, each child stays in the caller's process group,
// so it can read the terminal and receives the terminal's Ctrl-C itself, as
// upstream's children do; Signals then tracks it and reports the fatal
// signals caught while it ran.
type OSProcess struct {
	Signals *Signals
}

// Run executes spec. A bare Argv[0] is resolved against the PATH of
// spec.Env, never the parent's PATH (see resolveProgram). Cancelling ctx
// kills the child: its whole process group when it has its own. Impure: it
// spawns an operating-system process.
func (p OSProcess) Run(ctx context.Context, spec ProcessSpec) (int, error) {
	if len(spec.Argv) == 0 {
		return -1, errors.New("sys: empty argv")
	}
	prog, err := resolveProgram(spec.Argv[0], spec.Env)
	if err != nil {
		return -1, err
	}
	cmd := exec.CommandContext(ctx, prog, spec.Argv[1:]...)
	cmd.Args[0] = spec.Argv[0]
	cmd.Dir = spec.Dir
	cmd.Env = exactEnv(spec.Env)
	cmd.Stdin = spec.Stdin
	cmd.Stdout = spec.Stdout
	cmd.Stderr = spec.Stderr
	cmd.WaitDelay = killGrace
	if p.Signals == nil {
		isolateGroup(cmd)
		return exitStatus(ctx, cmd.Run())
	}
	return p.Signals.run(ctx, cmd)
}

// exactEnv returns env as a non-nil slice: a nil Cmd.Env would inherit the
// parent environment, but ProcessSpec.Env is exact. Pure.
func exactEnv(env []string) []string {
	if env == nil {
		return []string{}
	}
	return env
}

// exitStatus maps the error from Cmd.Run to the Process.Run contract. Pure.
func exitStatus(ctx context.Context, err error) (int, error) {
	if err == nil {
		return 0, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return -1, ctxErr
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitCode(exitErr)
	}
	return -1, err
}

// exitCode is the status of a child that did not exit 0: its exit code, or
// -1 and a *SignalError when a signal terminated it. Pure.
func exitCode(exitErr *exec.ExitError) (int, error) {
	if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return -1, &SignalError{Signal: int(ws.Signal())}
	}
	return exitErr.ExitCode(), nil
}

// OSClock is the Clock backed by the system clock and the local time zone.
type OSClock struct{}

// Now returns the current local time. Impure: it reads the system clock.
func (OSClock) Now() Time {
	return fromTime(time.Now())
}

// fromTime breaks t down into a Time in t's own zone. Pure.
func fromTime(t time.Time) Time {
	_, offset := t.Zone()
	return Time{Unix: t.Unix(), Nano: t.Nanosecond(), Offset: offset}
}
