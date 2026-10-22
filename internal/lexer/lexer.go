// Package lexer turns justfile source into tokens for the core subset.
//
// Lex is pure: it reads only its arguments and performs no I/O.
//
// Line structure drives the lexer. A line that starts with a space or tab and
// has other content is a candidate recipe body line: the lexer emits Indent
// and then raw Text, with {{ ... }} interpolations lexed as expressions. The
// parser decides whether such a line really belongs to a recipe. Every other
// line is lexed as code. A line holding only whitespace yields just a Newline.
//
// A leading UTF-8 byte order mark is dropped, and "\r\n" is treated as "\n"
// for line structure: it ends a line, and the Newline token's Text is "\n".
// Inside string literals the line break bytes are kept verbatim, so a value
// spanning lines carries the file's own line endings, as upstream does; only a
// backslash before a line break is a continuation and yields nothing.
// Positions are 1-based and count runes, so a BOM or CRLF never shifts a
// column. Source that does not end in a newline gets a synthetic Newline (with
// empty Text) before EOF.
package lexer

import (
	"bytes"
	"fmt"
	"unicode/utf8"

	"gitea.com/wimwian/just-go/internal/diag"
)

// TokenKind identifies a kind of token.
type TokenKind int

// Token kinds.
const (
	Ident    TokenKind = iota
	String             // Text is the unescaped contents; Raw is the source text with its quotes
	Assign             // :=
	Equals             // =
	Colon              // :
	Plus               // +
	LParen             // (
	RParen             // )
	Comma              // ,
	At                 // @ prefix
	Dash               // - prefix
	Interp             // {{
	Close              // }}
	Indent             // leading whitespace of a recipe body line; Text is the exact whitespace
	Text               // literal text inside a recipe body; Text is the raw text
	Newline            // "\n"; empty Text when synthesized at the end of the input
	Comment            // Text is the comment body without '#'
	LBracket           // [
	RBracket           // ]
	EOF
)

// Token is a lexeme with the 1-based line and rune column where it starts.
type Token struct {
	Kind TokenKind
	Text string
	Raw  string // for String, the source text including quotes and escapes; empty otherwise
	Line int
	Col  int
}

var bom = []byte{0xEF, 0xBB, 0xBF}

// escapes maps the character after a backslash in a cooked string to the byte
// it stands for.
var escapes = map[byte]byte{'n': '\n', 'r': '\r', 't': '\t', '\\': '\\', '"': '"'}

var puncts = map[byte]TokenKind{
	':': Colon, '=': Equals, '+': Plus, '(': LParen,
	')': RParen, ',': Comma, '@': At, '-': Dash, '[': LBracket, ']': RBracket,
}

// Lex tokenizes src. Errors are *diag.Error with KindParse; on error no tokens
// are returned.
func Lex(file string, src []byte) ([]Token, error) {
	l := &lexer{
		file:      file,
		src:       src,
		line:      1,
		col:       1,
		lineStart: true,
		toks:      make([]Token, 0, len(src)/3+2),
	}
	if bytes.HasPrefix(src, bom) {
		l.pos = len(bom)
	}
	for l.pos < len(l.src) {
		if err := l.step(); err != nil {
			return nil, l.unterminated(err)
		}
	}
	if l.interp {
		return nil, l.interpError()
	}
	l.finish()
	return l.toks, nil
}

// unterminated returns err, unless it is an unknown character inside an
// interpolation that nothing later in the source closes. Upstream lexes more
// characters than the core subset knows, and reaches the end of the input
// still inside the interpolation, so it reports the interpolation instead.
func (l *lexer) unterminated(err error) error {
	if l.interp && !bytes.Contains(l.src[l.pos:], []byte("}}")) && l.unknownAt(err) {
		return l.interpError()
	}
	return err
}

// unknownAt reports whether err is the lexer's unknown-character error.
func (l *lexer) unknownAt(err error) bool {
	de, ok := err.(*diag.Error)
	return ok && de.Line == l.line && de.Col == l.col && bytes.HasPrefix([]byte(de.Msg), []byte("unknown start of token"))
}

// interpError reports the interpolation that the input leaves open, at its
// opening braces, as upstream does.
func (l *lexer) interpError() error {
	return l.errWidth(l.open, 2, "unterminated interpolation")
}

type mark struct{ line, col int }

type lexer struct {
	file      string
	src       []byte
	pos       int
	line, col int
	lineStart bool // the next byte begins a line
	body      bool // the current line is a candidate recipe body line
	interp    bool // inside {{ ... }}
	open      mark // where the current interpolation's {{ is
	toks      []Token
}

func (l *lexer) mark() mark { return mark{l.line, l.col} }

func (l *lexer) emit(kind TokenKind, text string, at mark) {
	l.toks = append(l.toks, Token{Kind: kind, Text: text, Line: at.line, Col: at.col})
}

// emitString emits a String token whose source text began at byte offset
// start and ends at the current position.
func (l *lexer) emitString(text string, start int, at mark) {
	l.toks = append(l.toks, Token{Kind: String, Text: text, Raw: string(l.src[start:l.pos]), Line: at.line, Col: at.col})
}

func (l *lexer) errAt(at mark, msg string) error {
	return l.errWidth(at, 1, msg)
}

// errWidth is errAt for an error that covers width runes.
func (l *lexer) errWidth(at mark, width int, msg string) error {
	return &diag.Error{Kind: diag.KindParse, File: l.file, Line: at.line, Col: at.col, Width: width, Msg: msg}
}

// peek returns the next byte. Every caller has checked that pos is in range.
func (l *lexer) peek() byte { return l.src[l.pos] }

// newlineWidth returns the byte width of the line break at pos: 1 for "\n",
// 2 for "\r\n", and 0 if there is none.
func (l *lexer) newlineWidth() int {
	rest := l.src[l.pos:]
	switch {
	case len(rest) > 0 && rest[0] == '\n':
		return 1
	case len(rest) > 1 && rest[0] == '\r' && rest[1] == '\n':
		return 2
	}
	return 0
}

func (l *lexer) atInterp() bool {
	return bytes.HasPrefix(l.src[l.pos:], []byte("{{"))
}

// bump consumes one rune, or one line break, and returns the source bytes it
// covers. A line break is returned verbatim ("\n" or "\r\n"), so string
// values keep the file's line endings as upstream does. Invalid UTF-8 is
// consumed one byte at a time, so no input is lost.
func (l *lexer) bump() []byte {
	if w := l.newlineWidth(); w > 0 {
		b := l.src[l.pos : l.pos+w]
		l.pos += w
		l.line++
		l.col = 1
		return b
	}
	_, w := utf8.DecodeRune(l.src[l.pos:])
	b := l.src[l.pos : l.pos+w]
	l.pos += w
	l.col++
	return b
}

// skip consumes n ASCII bytes that contain no line break.
func (l *lexer) skip(n int) {
	l.pos += n
	l.col += n
}

// finish ends the token stream: a Newline unless the last token is one, then
// EOF.
func (l *lexer) finish() {
	at := l.mark()
	if n := len(l.toks); n > 0 && l.toks[n-1].Kind != Newline {
		l.emit(Newline, "", at)
	}
	l.emit(EOF, "", at)
}

func (l *lexer) step() error {
	switch {
	case l.lineStart && !l.interp:
		l.startLine()
		return nil
	case l.body && !l.interp:
		l.lexText()
		return nil
	}
	return l.lexCode()
}

// startLine classifies the line that begins at pos. Leading whitespace on a
// line with content becomes Indent and makes the line a body line.
func (l *lexer) startLine() {
	l.lineStart = false
	l.body = false
	n := blankPrefix(l.src[l.pos:])
	if n == 0 {
		return
	}
	at := l.mark()
	ws := string(l.src[l.pos : l.pos+n])
	l.skip(n)
	if l.pos < len(l.src) && l.newlineWidth() == 0 {
		l.emit(Indent, ws, at)
		l.body = true
	}
}

// blankPrefix returns the length of the run of spaces and tabs at the start of b.
func blankPrefix(b []byte) int {
	n := 0
	for n < len(b) && (b[n] == ' ' || b[n] == '\t') {
		n++
	}
	return n
}

// lexText lexes one run of body text and what ends it: an interpolation
// opener or the line break.
func (l *lexer) lexText() {
	at := l.mark()
	start := l.pos
	for l.pos < len(l.src) && l.newlineWidth() == 0 && !l.atInterp() {
		l.bump()
	}
	if l.pos > start {
		l.emit(Text, string(l.src[start:l.pos]), at)
	}
	switch {
	case l.atInterp():
		l.lexInterp()
	case l.pos < len(l.src):
		l.lexNewline()
	}
}

func (l *lexer) lexInterp() {
	l.open = l.mark()
	l.emit(Interp, "{{", l.open)
	l.skip(2)
	l.interp = true
}

// lexNewline handles a line break. Inside an interpolation it is whitespace.
func (l *lexer) lexNewline() {
	if l.interp {
		l.bump()
		return
	}
	at := l.mark()
	l.bump()
	l.emit(Newline, "\n", at)
	l.lineStart = true
	l.body = false
}

func (l *lexer) lexCode() error {
	c := l.peek()
	switch {
	case c == ' ' || c == '\t':
		l.skip(1)
	case l.newlineWidth() > 0:
		l.lexNewline()
	case isIdentStart(c):
		l.lexIdent()
	case c == '"':
		return l.lexCooked()
	case c == '\'':
		return l.lexRaw()
	case c == '#':
		l.lexComment()
	default:
		return l.lexPunct()
	}
	return nil
}

func isIdentStart(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || c == '-' || c >= '0' && c <= '9'
}

func (l *lexer) lexIdent() {
	at := l.mark()
	start := l.pos
	for l.pos < len(l.src) && isIdentPart(l.src[l.pos]) {
		l.skip(1)
	}
	l.emit(Ident, string(l.src[start:l.pos]), at)
}

func (l *lexer) lexComment() {
	at := l.mark()
	l.skip(1)
	start := l.pos
	for l.pos < len(l.src) && l.newlineWidth() == 0 {
		l.bump()
	}
	l.emit(Comment, string(l.src[start:l.pos]), at)
}

// lexRaw lexes a single-quoted string: no escapes, line breaks allowed.
func (l *lexer) lexRaw() error {
	at, start := l.mark(), l.pos
	l.skip(1)
	var text []byte
	for l.pos < len(l.src) {
		if l.peek() == '\'' {
			l.skip(1)
			l.emitString(string(text), start, at)
			return nil
		}
		text = append(text, l.bump()...)
	}
	return l.errAt(at, "unterminated string")
}

// lexCooked lexes a double-quoted string with backslash escapes.
func (l *lexer) lexCooked() error {
	at, start := l.mark(), l.pos
	l.skip(1)
	var text []byte
	for l.pos < len(l.src) {
		switch l.peek() {
		case '"':
			l.skip(1)
			l.emitString(string(text), start, at)
			return nil
		case '\\':
			out, err := l.lexEscape(at, start)
			if err != nil {
				return err
			}
			text = append(text, out...)
		default:
			text = append(text, l.bump()...)
		}
	}
	return l.errAt(at, "unterminated string")
}

// lexEscape consumes a backslash escape and returns the bytes it produces. A
// backslash before a line break produces nothing. str is the start of the
// enclosing string, reported if the input ends inside the escape, and start
// is its byte offset.
func (l *lexer) lexEscape(str mark, start int) ([]byte, error) {
	l.skip(1)
	if l.pos >= len(l.src) {
		return nil, l.errAt(str, "unterminated string")
	}
	if l.newlineWidth() > 0 {
		l.bump()
		return nil, nil
	}
	out, ok := escapes[l.peek()]
	if !ok {
		r, _ := utf8.DecodeRune(l.src[l.pos:])
		return nil, l.errWidth(str, l.stringWidth(start), fmt.Sprintf("`\\%c` is not a valid escape sequence", r))
	}
	l.skip(1)
	return []byte{out}, nil
}

// stringWidth is the width in runes of the cooked string that starts at byte
// offset start: through its closing quote, or to the end of the input. The
// lexer is inside the string, past an invalid escape. Upstream underlines the
// whole string for that error. It does not move the lexer.
func (l *lexer) stringWidth(start int) int {
	end := l.pos
	for end < len(l.src) && l.src[end] != '"' {
		if l.src[end] == '\\' {
			end++
		}
		end++
	}
	return utf8.RuneCount(l.src[start:min(end+1, len(l.src))])
}

// lexPunct lexes operators and delimiters, or reports an unknown character.
func (l *lexer) lexPunct() error {
	at := l.mark()
	rest := l.src[l.pos:]
	switch {
	case bytes.HasPrefix(rest, []byte(":=")):
		l.emit(Assign, ":=", at)
		l.skip(2)
	case l.atInterp():
		l.lexInterp()
	case bytes.HasPrefix(rest, []byte("}}")):
		l.emit(Close, "}}", at)
		l.skip(2)
		l.interp = false
	default:
		kind, ok := puncts[rest[0]]
		if !ok {
			r, _ := utf8.DecodeRune(rest)
			return l.errAt(at, fmt.Sprintf("unknown start of token %q", r))
		}
		l.emit(kind, string(rest[:1]), at)
		l.skip(1)
	}
	return nil
}
