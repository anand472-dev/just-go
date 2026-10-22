package eval

import (
	"errors"
	"regexp/syntax"
	"strings"
	"unicode/utf8"
)

// regexMessage words the failure err to compile pattern as Rust's regex crate
// words a parse error: the pattern, a caret under the place the fault is, and
// the fault. Go's regexp finds the same four faults as the crate does, and the
// scan places each as the crate does; for any other fault the caret is at the
// start and the fault is Go's own words. Pure.
func regexMessage(pattern string, err error) string {
	at, fault := 0, err.Error()
	var syntaxErr *syntax.Error
	if errors.As(err, &syntaxErr) {
		fault = syntaxErr.Code.String() + ": `" + syntaxErr.Expr + "`"
		at, fault = place(pattern, syntaxErr.Code, fault)
	}
	return "regex parse error:\n    " + pattern + "\n    " + strings.Repeat(" ", at) + "^\nerror: " + fault
}

// place says where in pattern, counted in characters, the crate puts the
// caret for a fault of the given code, and how the crate words the fault. For
// a code it does not know it returns 0 and fault; when the scan does not find
// a fault it knows is there, the caret is at 0. Pure.
func place(pattern string, code syntax.ErrorCode, fault string) (int, string) {
	s := scan(pattern)
	at, words := 0, fault
	switch code {
	case syntax.ErrMissingParen:
		at, words = s.open, "unclosed group"
	case syntax.ErrUnexpectedParen:
		at, words = s.unopened, "unopened group"
	case syntax.ErrMissingRepeatArgument:
		at, words = s.repeat, "repetition operator missing expression"
	case syntax.ErrMissingBracket:
		at, words = s.class, "unclosed character class"
	}
	return max(at, 0), words
}

// scanned holds the places in a pattern, in characters, of the first fault of
// each of the four kinds that the scan can find; -1 where there is none.
type scanned struct {
	// open is the last group still open at the end of the pattern.
	open int
	// unopened is the first `)` with no group to close.
	unopened int
	// repeat is the first repetition operator with nothing to repeat.
	repeat int
	// class is the first character class that is not closed.
	class int
}

// scanner is the position of a scan in a pattern. It follows the syntax only
// as far as the four faults need: escapes, character classes with their nested
// bracket expressions, groups, and the places a repetition operator may not
// stand.
type scanner struct {
	runes []rune
	i     int
	// operand is whether what lies before i is something that a repetition
	// operator may repeat.
	operand bool
	// groups are the indexes of the groups still open.
	groups []int
	found  scanned
}

// scan finds the faults of pattern. Pure.
func scan(pattern string) scanned {
	c := &scanner{runes: []rune(pattern), found: scanned{open: -1, unopened: -1, repeat: -1, class: -1}}
	for c.i < len(c.runes) {
		c.step()
	}
	if len(c.groups) > 0 {
		c.found.open = c.groups[len(c.groups)-1]
	}
	return c.found
}

// step moves the scan past the next piece of the pattern. Pure.
func (c *scanner) step() {
	switch c.runes[c.i] {
	case '\\':
		c.i += 2
		c.operand = true
	case '[':
		c.class()
	case '(':
		c.groups = append(c.groups, c.i)
		c.i += c.groupOpener()
		c.operand = false
	case ')':
		c.closeGroup()
	case '|':
		c.i++
		c.operand = false
	case '*', '+', '?':
		c.repeat()
	default:
		c.i++
		c.operand = true
	}
}

// closeGroup moves the scan past a `)`, which closes the last open group, or
// is the first fault of its kind if there is none. Pure.
func (c *scanner) closeGroup() {
	if n := len(c.groups); n > 0 {
		c.groups = c.groups[:n-1]
	} else if c.found.unopened < 0 {
		c.found.unopened = c.i
	}
	c.i++
	c.operand = true
}

// repeat moves the scan past a repetition operator, which is the first fault
// of its kind if nothing comes before it to repeat. Pure.
func (c *scanner) repeat() {
	if !c.operand && c.found.repeat < 0 {
		c.found.repeat = c.i
	}
	c.i++
	c.operand = true
}

// groupOpener returns how many characters the opening of the group at the
// scan take: `(`, or `(?:` and the flags of `(?i:`, `(?P<name>` and
// `(?<name>`, up to the `:` or `>` that ends them, or up to a `)`, which is
// left to close the group. Pure.
func (c *scanner) groupOpener() int {
	if c.i+1 >= len(c.runes) || c.runes[c.i+1] != '?' {
		return 1
	}
	// The sentinel `)` ends an opener that nothing else ends.
	tail := string(c.runes[c.i+2:]) + ")"
	end := strings.IndexAny(tail, ":>)")
	width := 2 + utf8.RuneCountInString(tail[:end])
	if tail[end] == ')' {
		return width
	}
	return width + 1
}

// class moves the scan past the character class that starts at it, or to the
// end of the pattern, and notes the first class that is not closed. Nested
// classes are not followed: a `]` ends the class. Pure.
func (c *scanner) class() {
	end := c.classEnd()
	if end < 0 && c.found.class < 0 {
		c.found.class = c.i
	}
	c.i, c.operand = len(c.runes), true
	if end >= 0 {
		c.i = end
	}
}

// classEnd returns the index after the `]` that closes the character class
// that starts at the scan, or -1 if it is not closed. A `]` first in the
// class, after any `^`, is part of it, and a backslash escapes the character
// after it. Pure.
func (c *scanner) classEnd() int {
	i := c.i + 1
	if i < len(c.runes) && c.runes[i] == '^' {
		i++
	}
	for first := true; i < len(c.runes); first = false {
		switch {
		case c.runes[i] == ']' && !first:
			return i + 1
		case c.runes[i] == '\\':
			i++
		}
		i++
	}
	return -1
}
