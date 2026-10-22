// Package sys is the impure boundary of just-go. It defines the interfaces
// through which the rest of the module touches the operating system, so that
// every other package stays pure and testable with the fakes in sysfake.
// Only this package (os.go, signals.go and the platform files) performs OS
// calls.
package sys

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
)

// FS is access to the filesystem: reading justfiles and the files they name,
// and the directory and files that script recipes run from.
type FS interface {
	ReadFile(path string) ([]byte, error)
	// IsFile reports whether path exists and is a regular file.
	IsFile(path string) (bool, error)
	// ReadDir returns the names of the entries directly inside the directory
	// at path, sorted by name. An unreadable or missing directory is an
	// error.
	ReadDir(path string) ([]string, error)
	// WriteFile writes data to the file at path, creating it with permission
	// perm if it does not exist and replacing its content if it does.
	WriteFile(path string, data []byte, perm fs.FileMode) error
	// MkdirTemp creates a new, empty directory whose name starts with prefix,
	// inside the directory dir, and returns its path. An empty dir means the
	// place for temporary scripts: the system's runtime directory, where it
	// has one, else the temporary directory.
	MkdirTemp(dir, prefix string) (string, error)
	// RemoveAll removes path and everything below it. A path that does not
	// exist is no error.
	RemoveAll(path string) error
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

// Numbers of the fatal signals `[continue]` can name, the same on every Unix
// system.
const (
	SIGHUP  = 1
	SIGINT  = 2
	SIGQUIT = 3
)

// SignalNumber is the number of the signal upstream calls name, one of
// SIGHUP, SIGINT and SIGQUIT, and whether it is one of them. Pure.
func SignalNumber(name string) (int, bool) {
	switch name {
	case "SIGHUP":
		return SIGHUP, true
	case "SIGINT":
		return SIGINT, true
	case "SIGQUIT":
		return SIGQUIT, true
	}
	return 0, false
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

// Time is an instant of wall-clock time, broken down so that pure code can
// format it without importing the time package: the seconds since the Unix
// epoch, the nanoseconds past the second, and the offset of the local time
// zone from UTC at that instant.
type Time struct {
	// Unix is the whole seconds since 1970-01-01 00:00:00 UTC.
	Unix int64
	// Nano is the nanoseconds past the second, from 0 to 999999999.
	Nano int
	// Offset is the seconds the local time is east of UTC.
	Offset int
	// UTC is true when the time is expressed in UTC, whose zone name is
	// "UTC"; otherwise the zone is named by its offset, as upstream's
	// formatter names a local time zone.
	UTC bool
}

// Clock reads the time of day.
type Clock interface {
	// Now returns the current time in the local time zone.
	Now() Time
}

// Captured is how a program that Capture ran ended, and what it wrote to its
// standard output.
type Captured struct {
	// Stdout is everything the program wrote to its standard output.
	Stdout []byte
	// ExitCode is the program's exit status, or -1 when it ended without one.
	ExitCode int
	// Signal is the signal that terminated the program, or 0 when it exited.
	Signal int
	// Caught is a fatal signal the runner caught while the program ran, or 0.
	Caught int
}

// Capture runs spec through p with its Stdout replaced by a buffer, and
// returns what the program wrote and how it ended. A signal that ended the
// program, or that the runner caught while it ran, is not an error but a
// field of the result; an error is a failure to start the program or the end
// of ctx. Impure: it runs a program through p.
func Capture(ctx context.Context, p Process, spec ProcessSpec) (Captured, error) {
	var out bytes.Buffer
	spec.Stdout = &out
	code, err := p.Run(ctx, spec)
	got := Captured{Stdout: out.Bytes(), ExitCode: code}
	var sig *SignalError
	if errors.As(err, &sig) {
		got.Signal, got.Caught = sig.Signal, sig.Caught
		return got, nil
	}
	return got, err
}
