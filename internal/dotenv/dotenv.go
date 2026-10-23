// Package dotenv parses dotenv files, the .env files that upstream just 1.59.0
// loads through the dotenvy 0.15.7 crate.
//
// The package does no I/O. Parse takes the file contents; ParseWithEnv also
// takes a lookup function for the process environment, which $NAME and ${NAME}
// substitutions consult before the file's own earlier assignments. Whether a
// parsed key overrides an existing environment variable is the caller's
// decision.
package dotenv

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ErrInvalidUTF8 is returned for input that is not valid UTF-8. Upstream reads
// dotenv files as text, so such input fails the whole load.
var ErrInvalidUTF8 = errors.New("stream did not contain valid UTF-8")

// LineError reports input that upstream rejects as dotenv syntax.
//
// Line is the text being parsed when the error was found. It is a logical line,
// which may span several physical lines, and it keeps its final newline when the
// input ended inside a quoted value or after a backslash. Index is the offset
// upstream reports: a byte offset into Line for errors in a key or between
// tokens, a character offset into the value for errors in a value, and the byte
// length of Line for unterminated input.
type LineError struct {
	Line  string
	Index int
}

// Error formats the error with upstream's dotenvy message.
func (e LineError) Error() string {
	return fmt.Sprintf("Error parsing line: '%s', error at line index: %d", e.Line, e.Index)
}

// Parse parses src as a dotenv file with no environment: $NAME and ${NAME}
// substitutions see only the keys assigned earlier in src. For a key assigned
// more than once, the last assignment wins.
func Parse(src []byte) (map[string]string, error) {
	return ParseWithEnv(src, nil)
}

// ParseWithEnv is Parse with an environment for substitutions. As upstream
// does, a variable that lookup finds takes precedence over one assigned earlier
// in src. A nil lookup means an empty environment.
func ParseWithEnv(src []byte, lookup func(name string) (string, bool)) (map[string]string, error) {
	p := &parser{lookup: lookup, values: map[string]string{}}
	lines := &lineSplitter{src: src}
	out := map[string]string{}
	for {
		line, more, err := lines.next()
		if err != nil {
			return nil, err
		}
		if !more {
			return out, nil
		}
		key, value, assigned, err := p.parseLine(line)
		if err != nil {
			return nil, err
		}
		if assigned {
			out[key] = value
		}
	}
}

// lexState is the quoting state of upstream's line splitter.
type lexState int

const (
	stateComplete lexState = iota
	stateEscape
	stateStrongOpen
	stateStrongEscape
	stateWeakOpen
	stateWeakEscape
	stateWhitespace
	stateComment
)

// lineSplitter yields logical lines. A physical line that leaves a quote open,
// or ends in a backslash, continues onto the next physical line.
type lineSplitter struct {
	src []byte
	pos int
}

// next returns the next logical line without its terminator. A line that
// starts with a comment is returned empty. more is false at the end of input.
func (s *lineSplitter) next() (string, bool, error) {
	var buf string
	state := stateComplete
	for {
		piece, found := s.readLine()
		if !found {
			if state == stateComplete {
				return "", false, nil
			}
			return "", false, LineError{Line: buf, Index: len(buf)}
		}
		text := string(piece)
		if !utf8.ValidString(text) {
			return "", false, ErrInvalidUTF8
		}
		start := len(buf)
		buf += text
		if strings.HasPrefix(strings.TrimLeftFunc(buf, unicode.IsSpace), "#") {
			return "", true, nil
		}
		var end int
		end, state = scanPiece(state, text)
		switch state {
		case stateComplete:
			return trimTerminator(buf), true, nil
		case stateComment:
			return buf[:start+end], true, nil
		}
	}
}

// readLine returns the next physical line, including its newline if it has one.
func (s *lineSplitter) readLine() ([]byte, bool) {
	if s.pos >= len(s.src) {
		return nil, false
	}
	end := len(s.src)
	if i := bytes.IndexByte(s.src[s.pos:], '\n'); i >= 0 {
		end = s.pos + i + 1
	}
	piece := s.src[s.pos:end]
	s.pos = end
	return piece, true
}

// trimTerminator removes one trailing "\n" or "\r\n".
func trimTerminator(s string) string {
	if !strings.HasSuffix(s, "\n") {
		return s
	}
	return strings.TrimSuffix(s[:len(s)-1], "\r")
}

// scanPiece advances the splitter over piece, which starts in state prev. When
// the returned state is stateComment, the int is the byte offset in piece of
// the '#' that starts the comment.
func scanPiece(prev lexState, piece string) (int, lexState) {
	state := prev
	for i, c := range piece {
		if state == stateWhitespace && c == '#' {
			return i, stateComment
		}
		state = nextState(state, c)
	}
	return 0, state
}

// nextState is the splitter's transition on one character.
func nextState(s lexState, c rune) lexState {
	switch s {
	case stateEscape:
		return stateComplete
	case stateWeakEscape:
		return stateWeakOpen
	case stateStrongEscape:
		return stateStrongOpen
	case stateWeakOpen:
		return weakNext(c)
	case stateStrongOpen:
		return strongNext(c)
	case stateWhitespace:
		return startState(c)
	case stateComplete:
		return tokenNext(c)
	}
	return s
}

func weakNext(c rune) lexState {
	switch c {
	case '\\':
		return stateWeakEscape
	case '"':
		return stateComplete
	}
	return stateWeakOpen
}

func strongNext(c rune) lexState {
	switch c {
	case '\\':
		return stateStrongEscape
	case '\'':
		return stateComplete
	}
	return stateStrongOpen
}

func tokenNext(c rune) lexState {
	if isInlineSpace(c) {
		return stateWhitespace
	}
	return startState(c)
}

// startState is the state after c when c is not a comment-starting '#'.
func startState(c rune) lexState {
	switch c {
	case '\\':
		return stateEscape
	case '"':
		return stateWeakOpen
	case '\'':
		return stateStrongOpen
	}
	return stateComplete
}

// isInlineSpace reports white space other than a line terminator.
func isInlineSpace(c rune) bool {
	return unicode.IsSpace(c) && c != '\n' && c != '\r'
}

// parser holds the state that persists across lines: the values assigned so
// far, which later lines' substitutions read.
type parser struct {
	lookup func(name string) (string, bool)
	values map[string]string
}

// parseLine parses one logical line. assigned is false for blank and comment
// lines.
func (p *parser) parseLine(line string) (key, value string, assigned bool, err error) {
	lp := newLineParser(line)
	lp.skipSpace()
	if lp.blank() {
		return "", "", false, nil
	}
	key, err = lp.assignedKey()
	if err != nil {
		return "", "", false, err
	}
	lp.skipSpace()
	if lp.blank() {
		p.values[key] = ""
		return key, "", true, nil
	}
	value, err = p.parseValue(lp.rest)
	if err != nil {
		return "", "", false, err
	}
	p.values[key] = value
	return key, value, true, nil
}

// parseValue parses the value that starts input, the rest of the line after
// the '=' and the white space that follows it.
func (p *parser) parseValue(input string) (string, error) {
	v := &valueParser{p: p}
	for i, c := range []rune(input) {
		stop, bad := v.feed(c)
		if bad {
			return "", LineError{Line: input, Index: i}
		}
		if stop {
			break
		}
	}
	if v.unterminated() {
		return "", LineError{Line: input, Index: max(len(input)-1, 0)}
	}
	v.substitute()
	return v.out.String(), nil
}

// lineParser is upstream's line parser: it consumes a trimmed line from the
// left and reports errors at its position.
type lineParser struct {
	original string
	rest     string
	pos      int
}

func newLineParser(line string) *lineParser {
	return &lineParser{original: line, rest: strings.TrimRightFunc(line, unicode.IsSpace)}
}

func (lp *lineParser) err() error {
	return LineError{Line: lp.original, Index: lp.pos}
}

// blank reports whether nothing is left, or only a comment.
func (lp *lineParser) blank() bool {
	return lp.rest == "" || lp.rest[0] == '#'
}

func (lp *lineParser) advance(n int) {
	lp.pos += n
	lp.rest = lp.rest[n:]
}

func (lp *lineParser) skipSpace() {
	n := strings.IndexFunc(lp.rest, isNotSpace)
	if n < 0 {
		n = len(lp.rest)
	}
	lp.advance(n)
}

// name parses a key: an ASCII letter or '_', then ASCII letters, digits, '_'
// or '.'.
func (lp *lineParser) name() (string, error) {
	if lp.rest == "" || !isNameStart(lp.rest[0]) {
		return "", lp.err()
	}
	n := 1
	for n < len(lp.rest) && isNameByte(lp.rest[n]) {
		n++
	}
	name := lp.rest[:n]
	lp.advance(n)
	return name, nil
}

// expectEqual consumes an '=' if one is next.
func (lp *lineParser) expectEqual() bool {
	if !strings.HasPrefix(lp.rest, "=") {
		return false
	}
	lp.advance(1)
	return true
}

func (lp *lineParser) requireEqual() error {
	if !lp.expectEqual() {
		return lp.err()
	}
	return nil
}

// assignedKey parses the key of an assignment. A leading `export` is a prefix
// (`export KEY=value`) unless it is followed directly by '=' (`export=value`),
// in which case it is the key.
func (lp *lineParser) assignedKey() (string, error) {
	key, err := lp.name()
	if err != nil {
		return "", err
	}
	lp.skipSpace()
	if key != "export" {
		return key, lp.requireEqual()
	}
	if lp.expectEqual() {
		return key, nil
	}
	if key, err = lp.name(); err != nil {
		return "", err
	}
	lp.skipSpace()
	return key, lp.requireEqual()
}

func isNotSpace(c rune) bool {
	return !unicode.IsSpace(c)
}

func isNameStart(b byte) bool {
	return b == '_' || ('a' <= b && b <= 'z') || ('A' <= b && b <= 'Z')
}

func isNameByte(b byte) bool {
	return isNameStart(b) || ('0' <= b && b <= '9') || b == '.'
}

// isNameRune reports whether c can appear in a $NAME substitution. Upstream
// uses Rust's char::is_alphanumeric, so the name may contain any Unicode letter
// or number, and an underscore ends it.
func isNameRune(c rune) bool {
	return unicode.IsLetter(c) || unicode.IsNumber(c) || unicode.Is(unicode.Other_Alphabetic, c)
}

// substMode is where a valueParser is in a $NAME or ${NAME} substitution.
type substMode int

const (
	substNone   substMode = iota
	substBare             // after '$', reading NAME
	substBraced           // after '${', reading NAME up to '}'
)

// valueParser is upstream's value state machine. Each character is handled by
// the first matching rule in feed, in upstream's order.
type valueParser struct {
	p            *parser
	out          strings.Builder
	name         strings.Builder
	strong       bool // inside '...'
	weak         bool // inside "..."
	escaped      bool // after a backslash
	expectingEnd bool // after unquoted white space: only white space or a comment may follow
	mode         substMode
}

// feed processes one character. stop reports a comment that ends the value;
// bad reports a character upstream rejects at this point.
func (v *valueParser) feed(c rune) (stop, bad bool) {
	switch {
	case v.expectingEnd:
		return v.afterValue(c)
	case v.escaped:
		return false, !v.escape(c)
	case v.strong:
		v.strongChar(c)
	case v.mode != substNone:
		v.substChar(c)
	case c == '$':
		v.mode = substBare
	default:
		v.plainChar(c)
	}
	return false, false
}

func (v *valueParser) afterValue(c rune) (stop, bad bool) {
	switch c {
	case ' ', '\t':
		return false, false
	case '#':
		return true, false
	}
	return false, true
}

// escape handles the character after a backslash. It reports false for an
// escape upstream does not know.
func (v *valueParser) escape(c rune) bool {
	v.escaped = false
	switch c {
	case '\\', '\'', '"', '$', ' ':
		v.out.WriteRune(c)
	case 'n':
		v.out.WriteByte('\n')
	default:
		return false
	}
	return true
}

func (v *valueParser) strongChar(c rune) {
	if c == '\'' {
		v.strong = false
		return
	}
	v.out.WriteRune(c)
}

// substChar handles a character while a substitution is open.
func (v *valueParser) substChar(c rune) {
	if isNameRune(c) {
		v.name.WriteRune(c)
		return
	}
	if v.mode == substBraced {
		if c == '}' {
			v.mode = substNone
			v.substitute()
		} else {
			v.name.WriteRune(c)
		}
		return
	}
	v.bareEnd(c)
}

// bareEnd handles the non-name character that follows '$'. It is not a name
// character, so it ends the substitution and is written as-is, except that '{'
// right after '$' opens a braced name, and another '$' starts a new one.
func (v *valueParser) bareEnd(c rune) {
	if c == '{' && v.name.Len() == 0 {
		v.mode = substBraced
		return
	}
	v.substitute()
	if c == '$' {
		return
	}
	v.mode = substNone
	v.out.WriteRune(c)
}

// substitute writes the value of the name read so far. The environment is
// consulted first, then the earlier assignments; an unknown name is empty.
func (v *valueParser) substitute() {
	name := v.name.String()
	v.name.Reset()
	if v.p.lookup != nil {
		if value, ok := v.p.lookup(name); ok {
			v.out.WriteString(value)
			return
		}
	}
	v.out.WriteString(v.p.values[name])
}

// plainChar handles a character outside a substitution and outside single
// quotes.
func (v *valueParser) plainChar(c rune) {
	if v.weak {
		v.weakChar(c)
		return
	}
	switch c {
	case '\'':
		v.strong = true
	case '"':
		v.weak = true
	case '\\':
		v.escaped = true
	case ' ', '\t':
		v.expectingEnd = true
	default:
		v.out.WriteRune(c)
	}
}

// weakChar handles a character inside double quotes.
func (v *valueParser) weakChar(c rune) {
	switch c {
	case '"':
		v.weak = false
	case '\\':
		v.escaped = true
	default:
		v.out.WriteRune(c)
	}
}

// unterminated reports input that ends inside a quote or a braced name.
func (v *valueParser) unterminated() bool {
	return v.mode == substBraced || v.strong || v.weak
}
