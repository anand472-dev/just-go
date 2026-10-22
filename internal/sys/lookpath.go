package sys

import (
	"io/fs"
	"os/exec"
	"path/filepath"
	"strings"
)

// resolveProgram returns the file to execute for name. A name that contains
// a path separator is returned unchanged. A bare name is searched for in the
// PATH entry of env, the child's exact environment, and never in the parent
// process's PATH; when env has no PATH, defaultPath is searched. Relative and
// empty PATH entries are skipped, as os/exec refuses programs found through
// them. Impure: it stats candidate files.
func resolveProgram(name string, env []string) (string, error) {
	if !isBare(name) {
		return name, nil
	}
	for _, c := range candidates(name, searchPath(env), pathExts(env)) {
		if isExecutable(c) {
			return c, nil
		}
	}
	return "", &notFoundError{err: &exec.Error{Name: name, Err: exec.ErrNotFound}}
}

// notFoundError is the error for a program that is not in PATH. It is an
// *exec.Error for exec.ErrNotFound, and it also matches fs.ErrNotExist, so a
// caller that may not import os/exec can tell a missing program from other
// failures, as it can for a missing program named by a path.
type notFoundError struct{ err *exec.Error }

// Error is the *exec.Error's message. Pure.
func (e *notFoundError) Error() string { return e.err.Error() }

// Unwrap returns the *exec.Error. Pure.
func (e *notFoundError) Unwrap() error { return e.err }

// Is reports whether target is fs.ErrNotExist. Pure.
func (e *notFoundError) Is(target error) bool { return target == fs.ErrNotExist }

// isBare reports whether name has no path separator, so that it must be
// searched for in PATH. Pure.
func isBare(name string) bool {
	return !strings.ContainsAny(name, pathSeparators)
}

// searchPath returns the PATH value of env, or defaultPath when env has no
// PATH entry. Pure.
func searchPath(env []string) string {
	if v, ok := envValue(env, "PATH"); ok {
		return v
	}
	return defaultPath
}

// envValue returns the value of the last entry for key in env, matching the
// key case-insensitively where the platform does. The last entry wins, as it
// does for os/exec. Pure.
func envValue(env []string, key string) (string, bool) {
	value, found := "", false
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if ok && sameKey(k, key) {
			value, found = v, true
		}
	}
	return value, found
}

// candidates returns, in search order, the absolute paths at which name may
// be found in the PATH list, one per directory and extension. Relative and
// empty directories are skipped. Pure.
func candidates(name, list string, exts []string) []string {
	var out []string
	for _, dir := range filepath.SplitList(list) {
		if !filepath.IsAbs(dir) {
			continue
		}
		for _, ext := range exts {
			out = append(out, filepath.Join(dir, name+ext))
		}
	}
	return out
}
