// Package sys is the impure boundary of just-go. It defines the interfaces
// through which the rest of the module touches the operating system, so that
// every other package stays pure and testable with the fakes in sysfake.
// Only this package (os.go, signals.go and the platform files) performs OS
// calls.
package sys

import (
	"context"
	"fmt"
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
	// start the program, for context cancellation, and for a *SignalError
	// when a signal ended the child or reached the runner while the child
	// ran. A child ended by a signal has no exit code, so exitCode is -1.
	Run(ctx context.Context, spec ProcessSpec) (exitCode int, err error)
}

// SignalError reports the signals that affected one child run, by their
// numbers. At least one field is set.
type SignalError struct {
	// Signal is the signal that terminated the child, or 0 when it exited.
	Signal int
	// Caught is a fatal signal the runner caught while the child ran, or 0.
	// Only an OSProcess with Signals catches them.
	Caught int
}

// Error describes the signals. Pure.
func (e *SignalError) Error() string {
	if e.Signal == 0 {
		return fmt.Sprintf("interrupted by %s", SignalName(e.Caught))
	}
	return fmt.Sprintf("terminated by signal %d", e.Signal)
}

// SignalName is upstream's name for a fatal signal, such as SIGINT for 2;
// any other signal is "signal N". Pure.
func SignalName(sig int) string {
	switch sig {
	case 1:
		return "SIGHUP"
	case 2:
		return "SIGINT"
	case 3:
		return "SIGQUIT"
	case 15:
		return "SIGTERM"
	}
	return fmt.Sprintf("signal %d", sig)
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
