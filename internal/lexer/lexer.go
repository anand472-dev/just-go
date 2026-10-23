// Package lexer turns justfile source into tokens.
//
// Lex is pure: it reads only its arguments and performs no I/O.
//
// Line structure drives the lexer. A line that starts with a space or tab and
// has other content is a candidate recipe body line: the lexer emits Indent
// and then Text, with {{ ... }} interpolations lexed as expressions. In body
// text "{{{{" is an escaped "{{", as upstream's is. The parser decides whether
// such a line really belongs to a recipe. Every other line is lexed as code. A
// line holding only whitespace yields just a Newline.
//
// Code is lexed into every token of upstream's language, even those the
// parser does not accept, so that errors name what upstream names. As in
// upstream, the lexer tracks the delimiters (, [ and { that code opens: a
// line break inside one is whitespace, and a closing delimiter must match the
// innermost open one. A backslash before a line break in code joins the
// lines.
//
// A leading UTF-8 byte order mark is dropped, and "\r\n" is treated as "\n"
// for line structure: it ends a line, and the Newline token's Text is "\n".
// Inside string literals the line break bytes are kept verbatim, so a value
// spanning lines carries the file's own line endings, as upstream does.
// StringValue turns a String token into its value: it removes an indented
// string's common indentation and processes a double-quoted string's escapes,
// where a backslash before a line break is a continuation and yields nothing.
// Positions are 1-based and count runes, so a BOM or CRLF never shifts a
// column. Source that does not end in a newline gets a synthetic Newline (with
// empty Text) before EOF.
package lexer

import (
	"bytes"
	"fmt"
	"strings"
	"unicode/utf8"

	"gitea.com/wimwian/just-go/internal/diag"
)

// TokenKind identifies a kind of token. The kinds are declared in the order
// of upstream's TokenKind, so sorting kinds sorts them as upstream lists the
// tokens it expected.
type TokenKind int

// Token kinds.
const (
	AmpAmp        TokenKind = iota // &&
	Asterisk                       // *
	At                             // @
	Backtick                       // Text is the source between the delimiters; Raw is the source text with its delimiters
	Bang                           // !
	BangEquals                     // !=
	BangTilde                      // !~
	BarBar                         // ||
	LBrace                         // {
	RBrace                         // }
	LBracket                       // [
	RBracket                       // ]
	ByteOrderMark                  // a U+FEFF that does not start the file
	Colon                          // :
	ColonColon                     // ::
	Assign                         // :=
	Comma                          // ,
	Comment                        // Text is the comment body without '#'
	Dollar                         // $
	EOF
	Newline      // "\n"; empty Text when synthesized at the end of the input
	Equals       // =
	EqualsEquals // ==
	EqualsTilde  // =~
	Ident
	Indent       // leading whitespace of a recipe body line; Text is the exact whitespace
	Close        // }}
	Interp       // {{
	LParen       // (
	RParen       // )
	Plus         // +
	PlusPlus     // ++
	QuestionMark // ?
	Slash        // /
	String       // Text is the source between the delimiters; Raw is the source text with its delimiters
	Text         // literal text inside a recipe body, with "{{{{" unescaped; Raw is the source text
)

// String names the kind as upstream does in an error message. Pure.
func (k TokenKind) String() string { return kindNames[k] }

var kindNames = [...]string{
	AmpAmp: "'&&'", Asterisk: "'*'", At: "'@'", Backtick: "backtick", Bang: "'!'",
	BangEquals: "'!='", BangTilde: "'!~'", BarBar: "'||'", LBrace: "'{'", RBrace: "'}'",
	LBracket: "'['", RBracket: "']'", ByteOrderMark: "byte order mark", Colon: "':'",
	ColonColon: "'::'", Assign: "':='", Comma: "','", Comment: "comment", Dollar: "'$'",
	EOF: "end of file", Newline: "end of line", Equals: "'='", EqualsEquals: "'=='",
	EqualsTilde: "'=~'", Ident: "identifier", Indent: "indent", Close: "'}}'",
	Interp: "'{{'", LParen: "'('", RParen: "')'", Plus: "'+'", PlusPlus: "'++'",
	QuestionMark: "?", Slash: "'/'", String: "string", Text: "command text",
}

// Token is a lexeme with the 1-based line and rune column where it starts.
type Token struct {
	Kind TokenKind
	Text string
	Raw  string // for String, Backtick and Text, the source text, delimiters and escapes included; empty otherwise
	Line int
	Col  int
}

var bom = []byte{0xEF, 0xBB, 0xBF}

// operator is a token that is always the same text.
type operator struct {
	text string
	kind TokenKind
}

// operators lists the operator tokens by their first byte, longest first, as
// upstream lexes them.
var operators = map[byte][]operator{
	':':  {{":=", Assign}, {"::", ColonColon}, {":", Colon}},
	'!':  {{"!=", BangEquals}, {"!~", BangTilde}, {"!", Bang}},
	'=':  {{"==", EqualsEquals}, {"=~", EqualsTilde}, {"=", Equals}},
	'+':  {{"++", PlusPlus}, {"+", Plus}},
	'@':  {{"@", At}},
	',':  {{",", Comma}},
	'*':  {{"*", Asterisk}},
	'$':  {{"$", Dollar}},
	'/':  {{"/", Slash}},
	'?':  {{"?", QuestionMark}},
	0xEF: {{"\uFEFF", ByteOrderMark}},
}

// delimiters maps each closing delimiter to its opening one, and each opening
// delimiter to itself.
var delimiters = map[byte]byte{'(': '(', '[': '[', '{': '{', ')': '(', ']': '[', '}': '{'}

var delimiterKinds = map[byte]TokenKind{
	'(': LParen, '[': LBracket, '{': LBrace, ')': RParen, ']': RBracket, '}': RBrace,
}

// codeLexers lexes the tokens of code that start with a given byte, apart
// from whitespace, identifiers and operators.
var codeLexers = map[byte]func(*lexer) error{
	'\n': (*lexer).lexLineBreak, '\r': (*lexer).lexLineBreak,
	'"': (*lexer).lexString, '\'': (*lexer).lexString, '`': (*lexer).lexString,
	'#': (*lexer).lexComment, '\\': (*lexer).lexEscape,
	'&': (*lexer).lexDigraph, '|': (*lexer).lexDigraph,
	'(': (*lexer).lexOpen, '[': (*lexer).lexOpen, '{': (*lexer).lexOpen,
	')': (*lexer).lexClose, ']': (*lexer).lexClose, '}': (*lexer).lexRBrace,
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
			return nil, err
		}
	}
	if l.interp {
		return nil, l.interpError()
	}
	l.finish()
	return l.toks, nil
}

// interpError reports the interpolation that the input leaves open, at its
// opening braces, as upstream does.
func (l *lexer) interpError() error {
	return l.errWidth(l.open, 2, "unterminated interpolation")
}

type mark struct{ line, col int }

// delim is an open delimiter and the line it is on.
type delim struct {
	open byte
	line int
}

type lexer struct {
	file      string
	src       []byte
	pos       int
	line, col int
	lineStart bool    // the next byte begins a line
	body      bool    // the current line is a candidate recipe body line
	interp    bool    // inside {{ ... }}
	open      mark    // where the current interpolation's {{ is
	delims    []delim // the delimiters open in code, innermost last
	brk       mark    // just past the last line break, on the line the break ends
	toks      []Token
}

func (l *lexer) mark() mark { return mark{l.line, l.col} }

func (l *lexer) emit(kind TokenKind, text string, at mark) {
	l.toks = append(l.toks, Token{Kind: kind, Text: text, Line: at.line, Col: at.col})
}

// emitString emits a String or Backtick token whose source text began at
// byte offset start and ends at the current position.
func (l *lexer) emitString(kind TokenKind, text string, start int, at mark) {
	l.toks = append(l.toks, Token{Kind: kind, Text: text, Raw: string(l.src[start:l.pos]), Line: at.line, Col: at.col})
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

// interpEscape is how body text writes a literal "{{".
const interpEscape = "{{{{"

func (l *lexer) atEscape() bool {
	return bytes.HasPrefix(l.src[l.pos:], []byte(interpEscape))
}

// bump consumes one rune, or one line break, and returns the source bytes it
// covers. A line break is returned verbatim ("\n" or "\r\n"), so string
// values keep the file's line endings as upstream does. Invalid UTF-8 is
// consumed one byte at a time, so no input is lost.
func (l *lexer) bump() []byte {
	if w := l.newlineWidth(); w > 0 {
		b := l.src[l.pos : l.pos+w]
		l.brk = mark{l.line, l.col + w}
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
// EOF. The added Newline stands for upstream's end of file, so it sits where
// upstream puts that: upstream never moves past the last line of the file,
// so when the source ends in a line break (inside a delimiter, where it is
// whitespace) the end is just past that break, on the line it ends.
func (l *lexer) finish() {
	at := l.mark()
	if n := len(l.toks); n > 0 && l.toks[n-1].Kind != Newline {
		end := at
		if bytes.HasSuffix(l.src, []byte("\n")) {
			end = l.brk
		}
		l.emit(Newline, "", end)
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
// opener or the line break. "{{{{" is text, and stands for "{{".
func (l *lexer) lexText() {
	at := l.mark()
	start := l.pos
	for l.pos < len(l.src) && l.newlineWidth() == 0 {
		if l.atEscape() {
			l.skip(len(interpEscape))
			continue
		}
		if l.atInterp() {
			break
		}
		l.bump()
	}
	if l.pos > start {
		raw := string(l.src[start:l.pos])
		l.toks = append(l.toks, Token{Kind: Text, Text: strings.ReplaceAll(raw, interpEscape, "{{"), Raw: raw, Line: at.line, Col: at.col})
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

// lexNewline handles a line break. Inside an interpolation or an open
// delimiter it is whitespace, as upstream's is.
func (l *lexer) lexNewline() {
	if l.interp || len(l.delims) > 0 {
		l.bump()
		return
	}
	at := l.mark()
	l.bump()
	l.emit(Newline, "\n", at)
	l.lineStart = true
	l.body = false
}

// lexCode lexes one token of code, which is everything but body text.
func (l *lexer) lexCode() error {
	c := l.peek()
	switch {
	case c == ' ' || c == '\t':
		l.skip(1)
		return nil
	case isIdentStart(c):
		l.lexIdent()
		return nil
	}
	if lex, ok := codeLexers[c]; ok {
		return lex(l)
	}
	return l.lexOperator()
}

// lexLineBreak lexes a line break in code. A carriage return must start one.
func (l *lexer) lexLineBreak() error {
	if l.newlineWidth() == 0 {
		return l.errAt(l.mark(), "unpaired carriage return")
	}
	l.lexNewline()
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

func (l *lexer) lexComment() error {
	at := l.mark()
	l.skip(1)
	start := l.pos
	for l.pos < len(l.src) && l.newlineWidth() == 0 {
		l.bump()
	}
	l.emit(Comment, string(l.src[start:l.pos]), at)
	return nil
}

// lexString lexes a string literal ('...', "...", or an indented string,
// which opens and closes with three quotes, single or double) or a backtick,
// which is delimited the same way by one or three backticks. As upstream
// does, the lexer only finds the end: in a double-quoted string a backslash
// keeps the next character from closing it. The token's Text is the source
// between the delimiters, and StringValue gives a string's value. An
// unterminated literal is reported at its start, underlining the opening
// delimiter.
func (l *lexer) lexString() error {
	at, start := l.mark(), l.pos
	quote := stringDelimiter(l.src[l.pos:])
	kind, what := String, "string"
	if quote[0] == '`' {
		kind, what = Backtick, "backtick"
	}
	l.skip(len(quote))
	body := l.pos
	escape := false
	for l.pos < len(l.src) {
		if !escape && bytes.HasPrefix(l.src[l.pos:], quote) {
			text := string(l.src[body:l.pos])
			l.skip(len(quote))
			l.emitString(kind, text, start, at)
			return nil
		}
		escape = quote[0] == '"' && !escape && l.peek() == '\\'
		l.bump()
	}
	return l.errWidth(at, len(quote), "unterminated "+what)
}

// stringDelimiter returns the delimiter of the string literal that starts
// rest: three quotes for an indented string, otherwise one. Pure.
func stringDelimiter(rest []byte) []byte {
	if len(rest) >= 3 && rest[1] == rest[0] && rest[2] == rest[0] {
		return rest[:3]
	}
	return rest[:1]
}

// lexEscape lexes a backslash in code. Before a line break it joins the next
// line, whose leading whitespace goes with it; anything else is an error, as
// in upstream. Errors underline the backslash.
func (l *lexer) lexEscape() error {
	at := l.mark()
	l.skip(1)
	switch {
	case l.pos == len(l.src):
		return l.errAt(at, "expected escape sequence but found end-of-file")
	case l.newlineWidth() > 0:
		l.bump()
		l.skip(blankPrefix(l.src[l.pos:]))
		return nil
	case l.peek() == '\r':
		return l.errWidth(at, 2, "unpaired carriage return")
	}
	r, _ := utf8.DecodeRune(l.src[l.pos:])
	return l.errAt(at, invalidEscape(r))
}

// lexDigraph lexes `&&` or `||`. A lone `&` or `|` is reported at the
// character after it, which should have been its twin.
func (l *lexer) lexDigraph() error {
	c := l.peek()
	if l.pos+1 < len(l.src) && l.src[l.pos+1] == c {
		kind := AmpAmp
		if c == '|' {
			kind = BarBar
		}
		l.emit(kind, string([]byte{c, c}), l.mark())
		l.skip(2)
		return nil
	}
	l.skip(1)
	if l.pos == len(l.src) {
		return l.errAt(l.mark(), fmt.Sprintf("expected character `%c` but found end-of-file", c))
	}
	return l.errAt(l.mark(), fmt.Sprintf("expected character `%c`", c))
}

// lexOpen lexes an opening delimiter and remembers it until it is closed.
func (l *lexer) lexOpen() error {
	c := l.peek()
	l.delims = append(l.delims, delim{open: c, line: l.line})
	l.emit(delimiterKinds[c], string(c), l.mark())
	l.skip(1)
	return nil
}

// lexClose lexes a closing delimiter, which must close the innermost open
// one.
func (l *lexer) lexClose() error {
	c, at := l.peek(), l.mark()
	n := len(l.delims)
	if n == 0 {
		return l.errAt(at, fmt.Sprintf("unexpected closing delimiter `%c`", c))
	}
	open := l.delims[n-1]
	if delimiters[c] != open.open {
		return l.errAt(at, fmt.Sprintf(
			"mismatched closing delimiter `%c`, did you mean to close the `%c` on line %d?", c, open.open, open.line))
	}
	l.delims = l.delims[:n-1]
	l.emit(delimiterKinds[c], string(c), at)
	l.skip(1)
	return nil
}

// lexRBrace lexes `}}`, which ends an interpolation that has no delimiter
// open inside it, or else a closing `}`.
func (l *lexer) lexRBrace() error {
	if !l.interp || len(l.delims) > 0 || !bytes.HasPrefix(l.src[l.pos:], []byte("}}")) {
		return l.lexClose()
	}
	l.emit(Close, "}}", l.mark())
	l.skip(2)
	l.interp = false
	return nil
}

// lexOperator lexes an operator, or reports a character that starts no token.
func (l *lexer) lexOperator() error {
	rest := l.src[l.pos:]
	for _, op := range operators[rest[0]] {
		if bytes.HasPrefix(rest, []byte(op.text)) {
			l.emit(op.kind, op.text, l.mark())
			l.pos += len(op.text)
			l.col += utf8.RuneCountInString(op.text)
			return nil
		}
	}
	r, _ := utf8.DecodeRune(rest)
	return l.errAt(l.mark(), unknownStart(r))
}

// unknownStart is upstream's message for a character that starts no token: it
// names the character, and its code point unless it is printable ASCII. Pure.
func unknownStart(r rune) string {
	msg := fmt.Sprintf("unknown start of token '%c'", r)
	if r < '!' || r > '~' {
		msg += fmt.Sprintf(" (U+%04X)", r)
	}
	return msg
}
