// Package diag defines the diagnostic type shared by every stage of just-go.
// It is pure: it holds data and formats it, and performs no I/O.
package diag

import (
	"bytes"
	"errors"
	"fmt"
)

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
// A Line of 0 means the diagnostic has no location.
type Error struct {
	Kind Kind
	File string
	Line int
	Col  int
	Msg  string
	// Width is how many runes of the source the diagnostic covers, starting
	// at Col; 0 means one. A renderer underlines them.
	Width int
	// Source is the text of line Line of File, without its line break, for a
	// renderer to quote; empty when unknown. WithSource fills it in.
	Source string
}

// Error formats the diagnostic as "<file>:<line>:<col>: <msg>". Pure.
func (e *Error) Error() string {
	return fmt.Sprintf("%s:%d:%d: %s", e.File, e.Line, e.Col, e.Msg)
}

// WithSource fills in Source of a located *Error in err's chain from src, the
// contents of the file it refers to, and returns err. Any other error is
// returned unchanged. Pure apart from updating the *Error.
func WithSource(err error, src []byte) error {
	var de *Error
	if errors.As(err, &de) && de.Line > 0 {
		de.Source = sourceLine(src, de.Line)
	}
	return err
}

// sourceLine returns line n (1-based) of src without its line break, a
// leading byte order mark or a trailing carriage return; "" past the end.
// Pure.
func sourceLine(src []byte, n int) string {
	src = bytes.TrimPrefix(src, []byte("\xEF\xBB\xBF"))
	lines := bytes.Split(src, []byte("\n"))
	if n > len(lines) {
		return ""
	}
	return string(bytes.TrimSuffix(lines[n-1], []byte("\r")))
}
