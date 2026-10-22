// Package diag defines the diagnostic type shared by every stage of just-go.
// It is pure: it holds data and formats it, and performs no I/O.
package diag

import "fmt"

// Kind classifies a diagnostic by the stage that produced it.
type Kind int

const (
	// KindParse is a lexing or parsing failure.
	KindParse Kind = iota
	// KindAnalysis is a semantic failure found before anything runs.
	KindAnalysis
	// KindRuntime is a failure while evaluating or running a recipe.
	KindRuntime
	// KindUsage is a command-line usage failure.
	KindUsage
)

// Error is a located diagnostic. Line and Col are 1-based; Col counts runes.
type Error struct {
	Kind Kind
	File string
	Line int
	Col  int
	Msg  string
}

// Error formats the diagnostic as "<file>:<line>:<col>: <msg>".
func (e *Error) Error() string {
	return fmt.Sprintf("%s:%d:%d: %s", e.File, e.Line, e.Col, e.Msg)
}
