package lexer

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"gitea.com/wimwian/just-go/internal/diag"
)

// StringValue returns the value of the String token t, as upstream's parser
// computes it: an indented string (three quotes, single or double) loses its
// common indentation first, and then a double-quoted string has its escapes
// processed. Errors are *diag.Error with KindParse at t, covering all of it.
// file names the justfile for errors. It is pure.
func StringValue(file string, t Token) (string, error) {
	text := t.Text
	if strings.HasPrefix(t.Raw, `'''`) || strings.HasPrefix(t.Raw, `"""`) {
		text = Unindent(text)
	}
	// Without a backslash, a double-quoted string has no escapes to process.
	if !strings.HasPrefix(t.Raw, `"`) || !strings.Contains(text, `\`) {
		return text, nil
	}
	value, msg := cook(text)
	if msg != "" {
		return "", &diag.Error{Kind: diag.KindParse, File: file, Line: t.Line, Col: t.Col,
			Width: utf8.RuneCountInString(t.Raw), Msg: msg}
	}
	return value, nil
}

// Unindent removes the indentation that every non-blank line of s shares. A
// blank line becomes just its line break, and a blank first or last line goes
// entirely. A line is blank when it holds only spaces, tabs and line breaks.
// It is upstream's unindent. Pure.
func Unindent(s string) string {
	lines := strings.SplitAfter(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	common := commonIndent(lines)
	var b strings.Builder
	b.Grow(len(s))
	for i, line := range lines {
		b.WriteString(unindentLine(line, common, i == 0 || i == len(lines)-1))
	}
	return b.String()
}

// commonIndent returns the longest run of leading spaces and tabs that every
// non-blank line starts with. Pure.
func commonIndent(lines []string) string {
	common, found := "", false
	for _, line := range lines {
		if blankLine(line) {
			continue
		}
		indent := line[:blankPrefix([]byte(line))]
		if !found {
			common, found = indent, true
			continue
		}
		common = common[:commonPrefix(common, indent)]
	}
	return common
}

// unindentLine returns line without the common indentation. A blank line
// keeps only its line break, or nothing when it is at an end. Pure.
func unindentLine(line, common string, end bool) string {
	switch {
	case !blankLine(line):
		return line[len(common):]
	case end:
		return ""
	case strings.HasSuffix(line, "\r\n"):
		return "\r\n"
	}
	return "\n"
}

func blankLine(line string) bool {
	return strings.Trim(line, " \t\r\n") == ""
}

// commonPrefix returns the length of the longest common prefix of a and b.
func commonPrefix(a, b string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

// cookState is where cook is in an escape sequence.
type cookState int

const (
	plain       cookState = iota
	backslash             // after `\`
	backslashCR           // after `\` and a carriage return
	unicodeOpen           // after `\u`
	unicodeHex            // inside `\u{...}`
)

// simpleEscapes maps the character after a backslash to what it stands for.
// A line feed after a backslash is a continuation and stands for nothing.
var simpleEscapes = map[rune]string{
	'n': "\n", 'r': "\r", 't': "\t", '\\': "\\", '"': "\"", '\n': "",
}

// cooker processes the escape sequences of a double-quoted string, one rune
// at a time, as upstream's cook_string does.
type cooker struct {
	out   strings.Builder
	state cookState
	hex   string
}

// cook returns the value of the double-quoted string contents s, or an error
// message. Bytes that are not valid UTF-8 are kept as they are. Pure.
func cook(s string) (string, string) {
	c := &cooker{}
	c.out.Grow(len(s))
	for i := 0; i < len(s); {
		r, w := utf8.DecodeRuneInString(s[i:])
		if msg := c.step(r, s[i:i+w]); msg != "" {
			return "", msg
		}
		i += w
	}
	return c.finish()
}

// step consumes the rune r, whose source bytes are src.
func (c *cooker) step(r rune, src string) string {
	switch c.state {
	case plain:
		if r == '\\' {
			c.state = backslash
		} else {
			c.out.WriteString(src)
		}
		return ""
	case backslash:
		return c.escape(r)
	case backslashCR:
		return c.afterCR(r)
	case unicodeOpen:
		return c.open(r)
	}
	return c.hexDigit(r)
}

// escape handles the rune after a backslash.
func (c *cooker) escape(r rune) string {
	c.state = plain
	if out, ok := simpleEscapes[r]; ok {
		c.out.WriteString(out)
		return ""
	}
	switch r {
	case 'u':
		c.state = unicodeOpen
	case '\r':
		c.state = backslashCR
	default:
		return invalidEscape(r)
	}
	return ""
}

// afterCR handles the rune after `\` and a carriage return: only a line feed
// completes the continuation.
func (c *cooker) afterCR(r rune) string {
	if r != '\n' {
		return invalidEscape('\r')
	}
	c.state = plain
	return ""
}

// open handles the rune after `\u`, which must be `{`.
func (c *cooker) open(r rune) string {
	if r != '{' {
		return fmt.Sprintf("expected unicode escape sequence delimiter `{` but found `%c`", r)
	}
	c.state, c.hex = unicodeHex, ""
	return ""
}

// hexDigit handles a rune inside `\u{...}`: a hex digit or the closing brace.
func (c *cooker) hexDigit(r rune) string {
	if r == '}' {
		return c.closeUnicode()
	}
	if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
		return fmt.Sprintf("expected hex digit [0-9A-Fa-f] but found `%c`", r)
	}
	c.hex += string(r)
	if len(c.hex) > 6 {
		return fmt.Sprintf("unicode escape sequence starting with `\\u{%s` longer than six hex digits", c.hex)
	}
	return ""
}

// closeUnicode writes the code point that `\u{...}` names.
func (c *cooker) closeUnicode() string {
	if c.hex == "" {
		return "unicode escape sequences must not be empty"
	}
	n, _ := strconv.ParseUint(c.hex, 16, 32)
	if n > utf8.MaxRune || n >= 0xD800 && n <= 0xDFFF {
		return fmt.Sprintf("unicode escape sequence value `%s` greater than maximum valid code point `10FFFF`", c.hex)
	}
	c.out.WriteRune(rune(n))
	c.state = plain
	return ""
}

// finish returns the value, or an error for an escape the string ends inside.
func (c *cooker) finish() (string, string) {
	switch c.state {
	case plain:
		return c.out.String(), ""
	case backslashCR:
		return "", invalidEscape('\r')
	}
	return "", "unterminated unicode escape sequence"
}

// shownEscapes overrides how invalidEscape shows some characters.
var shownEscapes = map[rune]string{
	'`': "\\`", '\\': "\\", '\'': "'", '"': "\"", '\t': `\t`, '\r': `\r`, '\n': `\n`,
}

// invalidEscape is upstream's message for `\` before r. Like upstream, it
// shows r as Rust's escape_default does, with a few exceptions.
func invalidEscape(r rune) string {
	shown, ok := shownEscapes[r]
	switch {
	case ok:
	case r >= 0x20 && r < 0x7f:
		shown = string(r)
	default:
		shown = fmt.Sprintf(`\u{%x}`, r)
	}
	return fmt.Sprintf("`\\%s` is not a valid escape sequence", shown)
}
