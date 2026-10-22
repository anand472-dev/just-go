// Package sys is the impure boundary of just-go. It defines the interfaces
// through which the rest of the module touches the operating system, so that
// every other package stays pure and testable with the fakes in sysfake.
// Only this package (os.go and its platform files) performs OS calls.
package sys

import (
	"context"
	"io"
)

// FS is read access to the filesystem.
type FS interface {
	ReadFile(path string) ([]byte, error)
	// IsFile reports whether path exists and is a regular file.
	IsFile(path string) (bool, error)
	// ReadDir returns the names of the entries directly inside the directory
	// at path, sorted by name. An unreadable or missing directory is an
	// error.
	ReadDir(path string) ([]string, error)
}

// Process runs external programs.
type Process interface {
	// Run starts spec.Argv, waits for it, and returns its exit code. A
	// non-zero exit code is not an error; err is reserved for failures to
	// start the program and for context cancellation.
	Run(ctx context.Context, spec ProcessSpec) (exitCode int, err error)
}

// ProcessSpec describes one program invocation.
type ProcessSpec struct {
	// Argv[0] is the program. A bare name (no path separator) is searched
	// for in the PATH of Env, or in a fixed default when Env has no PATH;
	// the parent process's PATH is never consulted.
	Argv   []string
	Dir    string    // working directory; empty means the current directory
	Env    []string  // exact environment, "KEY=VALUE"; nil means empty
	Stdin  io.Reader // nil means no input
	Stdout io.Writer // nil discards
	Stderr io.Writer // nil discards
}
