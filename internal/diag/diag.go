// Package diag defines the diagnostic type shared by every stage of just-go.
// It is pure: it holds data and formats it, and performs no I/O.
package diag

import (
	"bytes"
	"errors"
	"fmt"
	"unicode"
	"unicode/utf8"
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

// Error is a located diagnostic. Line and Col are 1-based; Col counts runes
// of the source line. A Line of 0 means the diagnostic has no location, and a
// Col of 0 means it has a line but no column, such as a recipe line that
// could not be run. Column gives the column as upstream reports it.
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

// Error formats the diagnostic as "<file>:<line>:<col>: <msg>", with the
// column upstream reports (see Column). Pure.
func (e *Error) Error() string {
	return fmt.Sprintf("%s:%d:%d: %s", e.File, e.Line, e.Column(), e.Msg)
}

// Column is the 1-based column upstream reports for the diagnostic: one more
// than the number of UTF-8 bytes of Source before rune Col. It differs from
// Col only when multibyte text precedes the location. A column past the end
// of Source counts one byte per rune beyond it. Without a Source, or without
// a column, it is Col. Pure.
func (e *Error) Column() int {
	if e.Source == "" || e.Col < 1 {
		return e.Col
	}
	column, runes := 1, 0
	for _, r := range e.Source {
		if runes == e.Col-1 {
			return column
		}
		column += utf8.RuneLen(r)
		runes++
	}
	return column + e.Col - 1 - runes
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

// DisplayWidth is the number of terminal columns r takes in a quoted source
// line, as upstream's unicode-width measures it: 0 for control characters,
// combining marks, invisible format characters, and the Hangul vowels and
// final consonants that join the syllable before them; 2 for wide and
// fullwidth East Asian characters and emoji; 1 otherwise. Pure.
func DisplayWidth(r rune) int {
	switch {
	case unicode.Is(prependedMarks, r):
		return 1
	case unicode.In(r, unicode.Cc, unicode.Mn, unicode.Me, unicode.Cf, joiningJamo):
		return 0
	case unicode.Is(wide, r):
		return 2
	}
	return 1
}

// joiningJamo are the Hangul vowels and final consonants that join the
// syllable before them, and so take no column of their own.
var joiningJamo = &unicode.RangeTable{
	R16: []unicode.Range16{{Lo: 0x1160, Hi: 0x11FF, Stride: 1}, {Lo: 0xD7B0, Hi: 0xD7FF, Stride: 1}},
}

// prependedMarks are the format characters that are visible and take one
// column (Unicode's Prepended_Concatenation_Mark).
var prependedMarks = &unicode.RangeTable{
	R16: []unicode.Range16{
		{Lo: 0x0600, Hi: 0x0605, Stride: 1}, {Lo: 0x06DD, Hi: 0x06DD, Stride: 1},
		{Lo: 0x070F, Hi: 0x070F, Stride: 1}, {Lo: 0x0890, Hi: 0x0891, Stride: 1},
		{Lo: 0x08E2, Hi: 0x08E2, Stride: 1},
	},
	R32: []unicode.Range32{{Lo: 0x110BD, Hi: 0x110BD, Stride: 1}, {Lo: 0x110CD, Hi: 0x110CD, Stride: 1}},
}
