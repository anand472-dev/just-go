package sys

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"time"
)

// killGrace bounds how long Run waits for output pipes to drain after a
// cancelled child has been killed.
const killGrace = 2 * time.Second

// OSFS is the FS backed by the real filesystem.
type OSFS struct{}

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

// OSProcess is the Process backed by os/exec.
type OSProcess struct{}

// Run executes spec. The child runs in its own process group, and cancelling
// ctx kills the whole group, not just the direct child. Impure: it spawns an
// operating-system process.
func (OSProcess) Run(ctx context.Context, spec ProcessSpec) (int, error) {
	if len(spec.Argv) == 0 {
		return -1, errors.New("sys: empty argv")
	}
	cmd := exec.CommandContext(ctx, spec.Argv[0], spec.Argv[1:]...)
	cmd.Dir = spec.Dir
	cmd.Env = exactEnv(spec.Env)
	cmd.Stdin = spec.Stdin
	cmd.Stdout = spec.Stdout
	cmd.Stderr = spec.Stderr
	cmd.WaitDelay = killGrace
	isolateGroup(cmd)
	return exitStatus(ctx, cmd.Run())
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
		return exitErr.ExitCode(), nil
	}
	return -1, err
}
