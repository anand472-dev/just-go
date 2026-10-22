package lexer_test

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/lexer"
)

func tok(kind lexer.TokenKind, text string, line, col int) lexer.Token {
	return lexer.Token{Kind: kind, Text: text, Line: line, Col: col}
}

// wantTokens lexes src and compares the full token slice, kind by kind and
// text by text, with positions.
func wantTokens(t *testing.T, src string, want []lexer.Token) {
	t.Helper()
	got, err := lexer.Lex("justfile", []byte(src))
	if err != nil {
		t.Fatalf("Lex(%q) error: %v", src, err)
	}
	if len(got) != len(want) {
		t.Fatalf("Lex(%q) returned %d tokens, want %d:\n got %+v\nwant %+v", src, len(got), len(want), got, want)
	}
	for i := range want {
		got[i].Raw = "" // Raw has its own test
		if got[i] != want[i] {
			t.Errorf("Lex(%q) token %d = %+v, want %+v", src, i, got[i], want[i])
		}
	}
}

// wantParseError lexes src and checks for a KindParse diag.Error at the position.
func wantParseError(t *testing.T, src string, line, col int, msg string) {
	t.Helper()
	wantParseErrorWidth(t, src, line, col, 1, msg)
}

// wantParseErrorWidth is wantParseError for an error that covers width runes.
func wantParseErrorWidth(t *testing.T, src string, line, col, width int, msg string) {
	t.Helper()
	toks, err := lexer.Lex("justfile", []byte(src))
	if err == nil {
		t.Fatalf("Lex(%q) = %+v, want an error", src, toks)
	}
	var de *diag.Error
	if !errors.As(err, &de) {
		t.Fatalf("Lex(%q) error %T is not a *diag.Error", src, err)
	}
	want := diag.Error{Kind: diag.KindParse, File: "justfile", Line: line, Col: col, Width: width, Msg: msg}
	if *de != want {
		t.Errorf("Lex(%q) error = %+v, want %+v", src, *de, want)
	}
	if toks != nil {
		t.Errorf("Lex(%q) returned tokens alongside an error: %+v", src, toks)
	}
}

// stringToken lexes src and returns its one String token.
func stringToken(t *testing.T, src string) lexer.Token {
	t.Helper()
	toks, err := lexer.Lex("justfile", []byte(src))
	if err != nil {
		t.Fatalf("Lex(%q) error: %v", src, err)
	}
	var strs []lexer.Token
	for _, tk := range toks {
		if tk.Kind == lexer.String {
			strs = append(strs, tk)
		}
	}
	if len(strs) != 1 {
		t.Fatalf("Lex(%q) has %d String tokens, want 1", src, len(strs))
	}
	return strs[0]
}

// wantValue checks the value of the one string literal in src.
func wantValue(t *testing.T, src, want string) {
	t.Helper()
	got, err := lexer.StringValue("justfile", stringToken(t, src))
	if err != nil || got != want {
		t.Errorf("StringValue(%q) = %q, %v; want %q", src, got, err, want)
	}
}

// wantValueError checks the error StringValue reports for the one string
// literal in src.
func wantValueError(t *testing.T, src string, line, col, width int, msg string) {
	t.Helper()
	got, err := lexer.StringValue("justfile", stringToken(t, src))
	var de *diag.Error
	if !errors.As(err, &de) {
		t.Fatalf("StringValue(%q) = %q, %v; want a *diag.Error", src, got, err)
	}
	want := diag.Error{Kind: diag.KindParse, File: "justfile", Line: line, Col: col, Width: width, Msg: msg}
	if *de != want || got != "" {
		t.Errorf("StringValue(%q) = %q, %+v; want %+v", src, got, *de, want)
	}
}

func TestLexSimpleAssignment(t *testing.T) {
	wantTokens(t, `x := "a"`, []lexer.Token{
		tok(lexer.Ident, "x", 1, 1),
		tok(lexer.Assign, ":=", 1, 3),
		tok(lexer.String, "a", 1, 6),
		tok(lexer.Newline, "", 1, 9),
		tok(lexer.EOF, "", 1, 9),
	})
}

func TestLexComment(t *testing.T) {
	wantTokens(t, "# hi", []lexer.Token{
		tok(lexer.Comment, " hi", 1, 1),
		tok(lexer.Newline, "", 1, 5),
		tok(lexer.EOF, "", 1, 5),
	})
}

func TestLexRecipeBodyIndent(t *testing.T) {
	wantTokens(t, "build:\n\tcargo build\n", []lexer.Token{
		tok(lexer.Ident, "build", 1, 1),
		tok(lexer.Colon, ":", 1, 6),
		tok(lexer.Newline, "\n", 1, 7),
		tok(lexer.Indent, "\t", 2, 1),
		tok(lexer.Text, "cargo build", 2, 2),
		tok(lexer.Newline, "\n", 2, 13),
		tok(lexer.EOF, "", 3, 1),
	})
}

func TestLexStripsBOMAndNormalizesCRLF(t *testing.T) {
	want := []lexer.Token{
		tok(lexer.Ident, "x", 1, 1),
		tok(lexer.Assign, ":=", 1, 3),
		tok(lexer.String, "a", 1, 6),
		tok(lexer.Newline, "\n", 1, 9),
		tok(lexer.EOF, "", 2, 1),
	}
	wantTokens(t, "\xEF\xBB\xBFx := \"a\"\r\n", want)
	wantTokens(t, "x := \"a\"\n", want)
}

func TestLexEmptySourceIsJustEOF(t *testing.T) {
	wantTokens(t, "", []lexer.Token{tok(lexer.EOF, "", 1, 1)})
	wantTokens(t, "\xEF\xBB\xBF", []lexer.Token{tok(lexer.EOF, "", 1, 1)})
}

func TestLexPunctuation(t *testing.T) {
	wantTokens(t, "@a: b, +c = (d)\n", []lexer.Token{
		tok(lexer.At, "@", 1, 1),
		tok(lexer.Ident, "a", 1, 2),
		tok(lexer.Colon, ":", 1, 3),
		tok(lexer.Ident, "b", 1, 5),
		tok(lexer.Comma, ",", 1, 6),
		tok(lexer.Plus, "+", 1, 8),
		tok(lexer.Ident, "c", 1, 9),
		tok(lexer.Equals, "=", 1, 11),
		tok(lexer.LParen, "(", 1, 13),
		tok(lexer.Ident, "d", 1, 14),
		tok(lexer.RParen, ")", 1, 15),
		tok(lexer.Newline, "\n", 1, 16),
		tok(lexer.EOF, "", 2, 1),
	})
}

// TestLexOperatorsOfTheFullLanguage checks that the lexer knows every
// operator upstream's lexer does, the longest first, so that a parse error
// names the token upstream names. A non-leading byte order mark is a token.
func TestLexOperatorsOfTheFullLanguage(t *testing.T) {
	wantTokens(t, "&& * ! != !~ || :: == =~ ++ $ / ? \uFEFF", []lexer.Token{
		tok(lexer.AmpAmp, "&&", 1, 1),
		tok(lexer.Asterisk, "*", 1, 4),
		tok(lexer.Bang, "!", 1, 6),
		tok(lexer.BangEquals, "!=", 1, 8),
		tok(lexer.BangTilde, "!~", 1, 11),
		tok(lexer.BarBar, "||", 1, 14),
		tok(lexer.ColonColon, "::", 1, 17),
		tok(lexer.EqualsEquals, "==", 1, 20),
		tok(lexer.EqualsTilde, "=~", 1, 23),
		tok(lexer.PlusPlus, "++", 1, 26),
		tok(lexer.Dollar, "$", 1, 29),
		tok(lexer.Slash, "/", 1, 31),
		tok(lexer.QuestionMark, "?", 1, 33),
		tok(lexer.ByteOrderMark, "\uFEFF", 1, 35),
		tok(lexer.Newline, "", 1, 36),
		tok(lexer.EOF, "", 1, 36),
	})
}

// TestTokenKindNamesAreUpstreams checks the names error messages use.
func TestTokenKindNamesAreUpstreams(t *testing.T) {
	want := map[lexer.TokenKind]string{
		lexer.AmpAmp: "'&&'", lexer.Backtick: "backtick", lexer.ByteOrderMark: "byte order mark",
		lexer.Assign: "':='", lexer.EOF: "end of file", lexer.Newline: "end of line",
		lexer.Ident: "identifier", lexer.Indent: "indent", lexer.Close: "'}}'", lexer.Interp: "'{{'",
		lexer.QuestionMark: "?", lexer.String: "string", lexer.Text: "command text",
	}
	for kind, name := range want {
		if got := kind.String(); got != name {
			t.Errorf("TokenKind(%d).String() = %q, want %q", int(kind), got, name)
		}
	}
}

func TestLexIdentifiersMayContainDashesDigitsAndUnderscores(t *testing.T) {
	wantTokens(t, "_a-b_9\n", []lexer.Token{
		tok(lexer.Ident, "_a-b_9", 1, 1),
		tok(lexer.Newline, "\n", 1, 7),
		tok(lexer.EOF, "", 2, 1),
	})
}

// A String token's Text is the source between its delimiters. StringValue
// processes escapes later, as upstream's parser does.
func TestLexCookedStringKeepsEscapesForStringValue(t *testing.T) {
	wantTokens(t, `"a\n\r\t\\\"b"`, []lexer.Token{
		tok(lexer.String, `a\n\r\t\\\"b`, 1, 1),
		tok(lexer.Newline, "", 1, 15),
		tok(lexer.EOF, "", 1, 15),
	})
	wantValue(t, `"a\n\r\t\\\"b"`, "a\n\r\t\\\"b")
}

func TestLexCookedStringLineContinuation(t *testing.T) {
	wantTokens(t, "\"a\\\nb\" x", []lexer.Token{
		tok(lexer.String, "a\\\nb", 1, 1),
		tok(lexer.Ident, "x", 2, 4),
		tok(lexer.Newline, "", 2, 5),
		tok(lexer.EOF, "", 2, 5),
	})
	wantValue(t, "\"a\\\nb\"", "ab")
	wantValue(t, "\"a\\\r\nb\"", "ab")
}

func TestLexRawStringKeepsBackslashes(t *testing.T) {
	wantTokens(t, `'a\n"b'`, []lexer.Token{
		tok(lexer.String, `a\n"b`, 1, 1),
		tok(lexer.Newline, "", 1, 8),
		tok(lexer.EOF, "", 1, 8),
	})
	wantValue(t, `'a\n"b'`, `a\n"b`)
	wantValue(t, `'a\'`, `a\`)
}

func TestLexMultiLineStringKeepsCRLFAndTracksLines(t *testing.T) {
	wantTokens(t, "'a\r\nb' c\n", []lexer.Token{
		tok(lexer.String, "a\r\nb", 1, 1),
		tok(lexer.Ident, "c", 2, 4),
		tok(lexer.Newline, "\n", 2, 5),
		tok(lexer.EOF, "", 3, 1),
	})
	wantTokens(t, "\"a\r\nb\" c\n", []lexer.Token{
		tok(lexer.String, "a\r\nb", 1, 1),
		tok(lexer.Ident, "c", 2, 4),
		tok(lexer.Newline, "\n", 2, 5),
		tok(lexer.EOF, "", 3, 1),
	})
	wantTokens(t, "'a\nb'\n", []lexer.Token{
		tok(lexer.String, "a\nb", 1, 1),
		tok(lexer.Newline, "\n", 2, 3),
		tok(lexer.EOF, "", 3, 1),
	})
	wantValue(t, "\"a\r\nb\"", "a\r\nb")
}

// Review Focus 3: `{{{{` in a recipe body is an escaped `{{`, even inside
// quotes, which are plain text there. In upstream's misc::brace_escape the
// body line `echo '{{{{'` runs as `echo '{{'`.
func TestLexBraceEscapeInsideString(t *testing.T) {
	wantTokens(t, "foo:\n  echo '{{{{'\n", []lexer.Token{
		tok(lexer.Ident, "foo", 1, 1),
		tok(lexer.Colon, ":", 1, 4),
		tok(lexer.Newline, "\n", 1, 5),
		tok(lexer.Indent, "  ", 2, 1),
		tok(lexer.Text, "echo '{{'", 2, 3),
		tok(lexer.Newline, "\n", 2, 14),
		tok(lexer.EOF, "", 3, 1),
	})
	// misc::brace_escape_extra: the fifth brace is plain text.
	wantTokens(t, "\techo '{{{{{'", []lexer.Token{
		tok(lexer.Indent, "\t", 1, 1),
		tok(lexer.Text, "echo '{{{'", 1, 2),
		tok(lexer.Newline, "", 1, 14),
		tok(lexer.EOF, "", 1, 14),
	})
	// Six braces are an escape and then an interpolation.
	wantTokens(t, "\t{{{{{{x}}", []lexer.Token{
		tok(lexer.Indent, "\t", 1, 1),
		tok(lexer.Text, "{{", 1, 2),
		tok(lexer.Interp, "{{", 1, 6),
		tok(lexer.Ident, "x", 1, 8),
		tok(lexer.Close, "}}", 1, 9),
		tok(lexer.Newline, "", 1, 11),
		tok(lexer.EOF, "", 1, 11),
	})
	// In a string literal `{{{{` is not an escape.
	wantValue(t, `"{{{{"`, "{{{{")
	wantValue(t, `'{{{{'`, "{{{{")
}

// TestLexBraceEscapeTextKeepsSource checks that a Text token's Raw is its
// source text, escape included, which error widths need.
func TestLexBraceEscapeTextKeepsSource(t *testing.T) {
	toks, err := lexer.Lex("justfile", []byte("\t{{{{ x"))
	if err != nil {
		t.Fatal(err)
	}
	if got := toks[1]; got.Kind != lexer.Text || got.Text != "{{ x" || got.Raw != "{{{{ x" {
		t.Errorf("Text token = %+v, want Text %q and Raw %q", got, "{{ x", "{{{{ x")
	}
}

// Indented strings, opened by three quotes, single or double, lose their
// common indentation and their blank first and last lines. A cooked one's
// escapes are processed after that, as upstream does (the string::indented_*
// corpus cases).
func TestLexIndentedStringRemovesCommonIndent(t *testing.T) {
	wantTokens(t, "x := '''\n  foo\n  bar\n'''\n", []lexer.Token{
		tok(lexer.Ident, "x", 1, 1),
		tok(lexer.Assign, ":=", 1, 3),
		tok(lexer.String, "\n  foo\n  bar\n", 1, 6),
		tok(lexer.Newline, "\n", 4, 4),
		tok(lexer.EOF, "", 5, 1),
	})
	cases := []struct{ src, want string }{
		{"'''\n  foo\n  bar\n'''", "foo\nbar\n"},
		{"\"\"\"\n  foo\n  bar\n\"\"\"", "foo\nbar\n"},
		{"'''\n  foo\\n\n  bar\n'''", "foo\\n\nbar\n"},
		{"\"\"\"\n  foo\\n\n  bar\n\"\"\"", "foo\n\nbar\n"},
		{"\"\"\"\n  foo\\\n  bar\n\"\"\"", "foobar\n"},
		{"'''\r\nfoo\r\n\r\nbar\r\n'''", "foo\r\n\r\nbar\r\n"},
		{"'''\n\tfoo\n\t  bar\n   '''", "foo\n  bar\n"},
		{"'''\n  a\n\n b\n  '''", " a\n\nb\n"},
		{"'''  a'''", "a"},
		{"''' \t '''", ""},
		{"''''''", ""},
		{"''", ""},
		{"'''a ' b'''", "a ' b"},
		{"\"\"\"a \\\"\"\" b\"\"\"", "a \"\"\" b"},
	}
	for _, c := range cases {
		wantValue(t, c.src, c.want)
	}
}

// The plan names misc::brace_escape#1 here, but that case has no error. The
// message and location come from the corpus cases string::unterminated_string,
// string::unterminated_raw_string, string::unterminated_indented_string and
// string::unterminated_indented_raw_string: upstream reports the string's
// start and underlines its opening delimiter.
func TestLexUnterminatedStringMatchesUpstream(t *testing.T) {
	wantParseErrorWidth(t, "a b= \":\n", 1, 6, 1, "unterminated string")
	wantParseErrorWidth(t, "a b= ':\n", 1, 6, 1, "unterminated string")
	wantParseErrorWidth(t, "a b= \"\"\":\n", 1, 6, 3, "unterminated string")
	wantParseErrorWidth(t, "a b= ''':\n", 1, 6, 3, "unterminated string")
	wantParseErrorWidth(t, "x := '''\na\n''\n", 1, 6, 3, "unterminated string")
	wantParseErrorWidth(t, "x := \"a\\\"", 1, 6, 1, "unterminated string")
}

// StringValue processes escapes as upstream's cook_string does, unicode
// escapes included (the string::*unicode_escape* corpus cases).
func TestStringValueEscapes(t *testing.T) {
	cases := []struct{ src, want string }{
		{`"\u{1f916}\u{1F916}"`, "\U0001F916\U0001F916"},
		{`"\u{10FFFF}"`, "\U0010FFFF"},
		{`"\u{012345}\u{6789a}\u{bcdef}\u{ABCDE}\u{F}"`, "\U00012345\U0006789a\U000bcdef\U000ABCDE\u000F"},
		{`"\""`, `"`},
		{"\"\xff\"", "\xff"},
	}
	for _, c := range cases {
		wantValue(t, c.src, c.want)
	}
}

// Upstream reports a bad escape at the string, underlining all of it.
func TestStringValueEscapeErrors(t *testing.T) {
	cases := []struct {
		src   string
		width int
		msg   string
	}{
		{`"ab\q"`, 6, "`\\q` is not a valid escape sequence"},
		{`"\q\"x"`, 7, "`\\q` is not a valid escape sequence"},
		{`"é\q"`, 5, "`\\q` is not a valid escape sequence"},
		{"\"\\`\"", 4, "`\\\\`` is not a valid escape sequence"},
		{`"\é"`, 4, "`\\\\u{e9}` is not a valid escape sequence"},
		{"\"a\\\r\"", 5, "`\\\\r` is not a valid escape sequence"},
		{"\"a\\\rb\"", 6, "`\\\\r` is not a valid escape sequence"},
		{"\"a\\\t\"", 5, "`\\\\t` is not a valid escape sequence"},
		{`"\u{}"`, 6, "unicode escape sequences must not be empty"},
		{`"\u{BadBad}"`, 12, "unicode escape sequence value `BadBad` greater than maximum valid code point `10FFFF`"},
		{`"\u{D800}"`, 10, "unicode escape sequence value `D800` greater than maximum valid code point `10FFFF`"},
		{`"\u1234"`, 8, "expected unicode escape sequence delimiter `{` but found `1`"},
		{`"\u{foo}"`, 9, "expected hex digit [0-9A-Fa-f] but found `o`"},
		{`"\u {1f916}"`, 12, "expected unicode escape sequence delimiter `{` but found ` `"},
		{`"\u{FFFFFFFFFF}"`, 16, "unicode escape sequence starting with `\\u{FFFFFFF` longer than six hex digits"},
		{`"\u{1f917"`, 10, "unterminated unicode escape sequence"},
		{`"\u"`, 4, "unterminated unicode escape sequence"},
	}
	for _, c := range cases {
		wantValueError(t, "x := "+c.src, 1, 6, c.width, c.msg)
	}
}

func BenchmarkStringValue(b *testing.B) {
	src := []byte("x := \"\"\"\n    first \\u{1f916} line\\t\n    second line\\n\n      third\n    \"\"\"\n")
	toks, err := lexer.Lex("justfile", src)
	if err != nil {
		b.Fatal(err)
	}
	str := toks[2]
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := lexer.StringValue("justfile", str); err != nil {
			b.Fatal(err)
		}
	}
}

func TestLexColumnsCountRunes(t *testing.T) {
	wantTokens(t, "x := \"héllo✓\" y\n", []lexer.Token{
		tok(lexer.Ident, "x", 1, 1),
		tok(lexer.Assign, ":=", 1, 3),
		tok(lexer.String, "héllo✓", 1, 6),
		tok(lexer.Ident, "y", 1, 15),
		tok(lexer.Newline, "\n", 1, 16),
		tok(lexer.EOF, "", 2, 1),
	})
}

func TestLexCommentBodyStopsAtCRLF(t *testing.T) {
	wantTokens(t, "# a b\r\nx\n", []lexer.Token{
		tok(lexer.Comment, " a b", 1, 1),
		tok(lexer.Newline, "\n", 1, 6),
		tok(lexer.Ident, "x", 2, 1),
		tok(lexer.Newline, "\n", 2, 2),
		tok(lexer.EOF, "", 3, 1),
	})
}

func TestLexTrailingComment(t *testing.T) {
	wantTokens(t, "x := 'a' # c\n", []lexer.Token{
		tok(lexer.Ident, "x", 1, 1),
		tok(lexer.Assign, ":=", 1, 3),
		tok(lexer.String, "a", 1, 6),
		tok(lexer.Comment, " c", 1, 10),
		tok(lexer.Newline, "\n", 1, 13),
		tok(lexer.EOF, "", 2, 1),
	})
}

func TestLexBodyInterpolation(t *testing.T) {
	wantTokens(t, "r:\n  echo {{ x }}!\n", []lexer.Token{
		tok(lexer.Ident, "r", 1, 1),
		tok(lexer.Colon, ":", 1, 2),
		tok(lexer.Newline, "\n", 1, 3),
		tok(lexer.Indent, "  ", 2, 1),
		tok(lexer.Text, "echo ", 2, 3),
		tok(lexer.Interp, "{{", 2, 8),
		tok(lexer.Ident, "x", 2, 11),
		tok(lexer.Close, "}}", 2, 13),
		tok(lexer.Text, "!", 2, 15),
		tok(lexer.Newline, "\n", 2, 16),
		tok(lexer.EOF, "", 3, 1),
	})
}

func TestLexBodyTextIsRaw(t *testing.T) {
	// '#', quotes and ':=' are ordinary text inside a body line.
	wantTokens(t, "\t# not \"a\" := comment", []lexer.Token{
		tok(lexer.Indent, "\t", 1, 1),
		tok(lexer.Text, "# not \"a\" := comment", 1, 2),
		tok(lexer.Newline, "", 1, 22),
		tok(lexer.EOF, "", 1, 22),
	})
}

func TestLexInterpolationMayStartTheBodyLineAndSpanLines(t *testing.T) {
	wantTokens(t, "\t{{ a\r\n  + 'b' }}", []lexer.Token{
		tok(lexer.Indent, "\t", 1, 1),
		tok(lexer.Interp, "{{", 1, 2),
		tok(lexer.Ident, "a", 1, 5),
		tok(lexer.Plus, "+", 2, 3),
		tok(lexer.String, "b", 2, 5),
		tok(lexer.Close, "}}", 2, 9),
		tok(lexer.Newline, "", 2, 11),
		tok(lexer.EOF, "", 2, 11),
	})
}

// Only body text has interpolations. In code, as in upstream, braces are
// delimiters, one at a time.
func TestLexBracesInCodeAreDelimiters(t *testing.T) {
	wantTokens(t, "x := {{y}}\n", []lexer.Token{
		tok(lexer.Ident, "x", 1, 1),
		tok(lexer.Assign, ":=", 1, 3),
		tok(lexer.LBrace, "{", 1, 6),
		tok(lexer.LBrace, "{", 1, 7),
		tok(lexer.Ident, "y", 1, 8),
		tok(lexer.RBrace, "}", 1, 9),
		tok(lexer.RBrace, "}", 1, 10),
		tok(lexer.Newline, "\n", 1, 11),
		tok(lexer.EOF, "", 2, 1),
	})
}

// A line break inside an open delimiter is whitespace, as is the indentation
// after it (delimiters::paren_continuation#1). When the input ends inside
// one, the added line break is where upstream puts the end of the file: just
// past the last line break, on the line it ends.
func TestLexLineBreakInsideDelimiterIsWhitespace(t *testing.T) {
	wantTokens(t, "x := (\n  'a'\n)\n", []lexer.Token{
		tok(lexer.Ident, "x", 1, 1),
		tok(lexer.Assign, ":=", 1, 3),
		tok(lexer.LParen, "(", 1, 6),
		tok(lexer.String, "a", 2, 3),
		tok(lexer.RParen, ")", 3, 1),
		tok(lexer.Newline, "\n", 3, 2),
		tok(lexer.EOF, "", 4, 1),
	})
	wantTokens(t, "x := [\r\n", []lexer.Token{
		tok(lexer.Ident, "x", 1, 1),
		tok(lexer.Assign, ":=", 1, 3),
		tok(lexer.LBracket, "[", 1, 6),
		tok(lexer.Newline, "", 1, 9),
		tok(lexer.EOF, "", 2, 1),
	})
}

// The delimiters::mismatched_delimiter#1 and delimiters::unexpected_delimiter#1
// corpus cases, and a mismatch across lines that the oracle reports the same
// way.
func TestLexClosingDelimiterErrors(t *testing.T) {
	wantParseError(t, "(]", 1, 2, "mismatched closing delimiter `]`, did you mean to close the `(` on line 1?")
	wantParseError(t, "]", 1, 1, "unexpected closing delimiter `]`")
	wantParseError(t, "(\n[\n}", 3, 1, "mismatched closing delimiter `}`, did you mean to close the `[` on line 2?")
	wantParseError(t, "x := a }}", 1, 8, "unexpected closing delimiter `}`")
}

// Inside an interpolation `}}` closes it only when no delimiter is open
// there; otherwise it is a closing brace.
func TestLexInterpolationEndNeedsNoOpenDelimiter(t *testing.T) {
	wantParseError(t, "r:\n  {{ ( }}\n", 2, 8, "mismatched closing delimiter `}`, did you mean to close the `(` on line 2?")
	wantTokens(t, "r:\n  {{ (a) }}", []lexer.Token{
		tok(lexer.Ident, "r", 1, 1),
		tok(lexer.Colon, ":", 1, 2),
		tok(lexer.Newline, "\n", 1, 3),
		tok(lexer.Indent, "  ", 2, 1),
		tok(lexer.Interp, "{{", 2, 3),
		tok(lexer.LParen, "(", 2, 6),
		tok(lexer.Ident, "a", 2, 7),
		tok(lexer.RParen, ")", 2, 8),
		tok(lexer.Close, "}}", 2, 10),
		tok(lexer.Newline, "", 2, 12),
		tok(lexer.EOF, "", 2, 12),
	})
}

// error_messages::unexpected_character#1: a lone `&` or `|` is reported at
// the character after it.
func TestLexDigraphErrors(t *testing.T) {
	wantParseError(t, "&~", 1, 2, "expected character `&`")
	wantParseError(t, "&", 1, 2, "expected character `&` but found end-of-file")
	wantParseError(t, "x := a |b", 1, 9, "expected character `|`")
}

// A backslash in code joins the next line, and is an error before anything
// else (parser::backslash_eof#1 for the end of the input).
func TestLexEscapes(t *testing.T) {
	wantTokens(t, "x := 'a' \\\n  + 'b'", []lexer.Token{
		tok(lexer.Ident, "x", 1, 1),
		tok(lexer.Assign, ":=", 1, 3),
		tok(lexer.String, "a", 1, 6),
		tok(lexer.Plus, "+", 2, 3),
		tok(lexer.String, "b", 2, 5),
		tok(lexer.Newline, "", 2, 8),
		tok(lexer.EOF, "", 2, 8),
	})
	wantParseError(t, "foo:\n\\", 2, 1, "expected escape sequence but found end-of-file")
	wantParseError(t, "x := \\a", 1, 6, "`\\a` is not a valid escape sequence")
	wantParseErrorWidth(t, "x := \\\r", 1, 6, 2, "unpaired carriage return")
}

// Backticks are lexed like strings, so an unterminated one is reported as
// upstream reports it.
func TestLexBackticks(t *testing.T) {
	toks, err := lexer.Lex("justfile", []byte("x := `ls`"))
	if err != nil || toks[2] != (lexer.Token{Kind: lexer.Backtick, Text: "ls", Raw: "`ls`", Line: 1, Col: 6}) {
		t.Errorf("Lex = %+v, %v", toks, err)
	}
	wantParseError(t, "x := `a", 1, 6, "unterminated backtick")
	wantParseErrorWidth(t, "x := ```a", 1, 6, 3, "unterminated backtick")
}

func TestLexBlankAndWhitespaceOnlyLinesEmitOnlyNewline(t *testing.T) {
	wantTokens(t, "a:\n\n \t\n\tb\n  ", []lexer.Token{
		tok(lexer.Ident, "a", 1, 1),
		tok(lexer.Colon, ":", 1, 2),
		tok(lexer.Newline, "\n", 1, 3),
		tok(lexer.Newline, "\n", 2, 1),
		tok(lexer.Newline, "\n", 3, 3),
		tok(lexer.Indent, "\t", 4, 1),
		tok(lexer.Text, "b", 4, 2),
		tok(lexer.Newline, "\n", 4, 3),
		tok(lexer.EOF, "", 5, 3),
	})
}

func TestLexMixedIndentIsReportedExactly(t *testing.T) {
	wantTokens(t, " \t x", []lexer.Token{
		tok(lexer.Indent, " \t ", 1, 1),
		tok(lexer.Text, "x", 1, 4),
		tok(lexer.Newline, "", 1, 5),
		tok(lexer.EOF, "", 1, 5),
	})
}

func TestLexUnterminatedStrings(t *testing.T) {
	wantParseError(t, "x := \"abc", 1, 6, "unterminated string")
	wantParseError(t, "\n\nx := 'abc\ndef", 3, 6, "unterminated string")
}

func TestLexBackslashAtEndOfInputUnterminatesString(t *testing.T) {
	wantParseError(t, `"abc\`, 1, 1, "unterminated string")
	// The lexer only finds the string's end; a bad escape is StringValue's.
	wantParseError(t, `x := "é\q`, 1, 6, "unterminated string")
	wantParseError(t, `x := "\q\`, 1, 6, "unterminated string")
}

// An interpolation the input never closes is reported at its opening braces,
// as upstream does, even with a delimiter open inside it. A character that
// starts no token is reported first, wherever it is.
func TestLexUnterminatedInterpolation(t *testing.T) {
	wantParseErrorWidth(t, "r:\n\techo {{ x\n\techo $HOME\n", 2, 7, 2, "unterminated interpolation")
	wantParseErrorWidth(t, "r:\n  x {{ a\n", 2, 5, 2, "unterminated interpolation")
	wantParseErrorWidth(t, "r:\n  x {{", 2, 5, 2, "unterminated interpolation")
	wantParseErrorWidth(t, "r:\n  {{ (\n", 2, 3, 2, "unterminated interpolation")
	wantParseError(t, "r:\n  x {{ % }}\n", 2, 8, "unknown start of token '%'")
	wantParseError(t, "r:\n  x {{ %\n", 2, 8, "unknown start of token '%'")
	wantParseError(t, "r:\n  {{ 'a }}\n", 2, 6, "unterminated string")
}

// Upstream names a character that starts no token, and gives its code point
// unless it is printable ASCII (misc::unknown_start_of_token_* corpus cases).
func TestLexUnknownStartOfToken(t *testing.T) {
	wantParseError(t, "x := %", 1, 6, "unknown start of token '%'")
	wantParseError(t, "a\n\u00e9", 2, 1, "unknown start of token 'é' (U+00E9)")
	wantParseError(t, "x := \x00", 1, 6, "unknown start of token '\x00' (U+0000)")
	wantParseError(t, "\u200b", 1, 1, "unknown start of token '\u200b' (U+200B)")
	wantParseError(t, "-", 1, 1, "unknown start of token '-'")
	wantParseError(t, "9", 1, 1, "unknown start of token '9'")
}

// A carriage return in code must start a CRLF line break.
func TestLexUnpairedCarriageReturn(t *testing.T) {
	wantParseError(t, "x\r", 1, 2, "unpaired carriage return")
	wantParseError(t, "x\ry", 1, 2, "unpaired carriage return")
}

func TestLexInvalidUTF8InsideTextAndStringsIsKept(t *testing.T) {
	wantTokens(t, "\t\xff\n", []lexer.Token{
		tok(lexer.Indent, "\t", 1, 1),
		tok(lexer.Text, "\xff", 1, 2),
		tok(lexer.Newline, "\n", 1, 3),
		tok(lexer.EOF, "", 2, 1),
	})
	wantTokens(t, "'\xff'", []lexer.Token{
		tok(lexer.String, "\xff", 1, 1),
		tok(lexer.Newline, "", 1, 4),
		tok(lexer.EOF, "", 1, 4),
	})
}

func TestLexIsDeterministicAndDoesNotModifyInput(t *testing.T) {
	src := []byte("x := \"a\"\r\nr:\n\techo {{x}}\n")
	orig := append([]byte(nil), src...)
	first, err1 := lexer.Lex("f", src)
	second, err2 := lexer.Lex("f", src)
	if err1 != nil || err2 != nil {
		t.Fatalf("errors: %v, %v", err1, err2)
	}
	if !reflect.DeepEqual(first, second) {
		t.Errorf("two lexes of the same source differ")
	}
	if string(src) != string(orig) {
		t.Errorf("Lex modified its input")
	}
}

func TestLexErrorCarriesFileName(t *testing.T) {
	_, err := lexer.Lex("sub/justfile", []byte("%"))
	if err == nil || !strings.HasPrefix(err.Error(), "sub/justfile:1:1: ") {
		t.Fatalf("error = %v, want prefix sub/justfile:1:1: ", err)
	}
}

// generatedJustfile builds a justfile of n lines: variables, comments and
// recipes with interpolated body lines.
func generatedJustfile(n int) []byte {
	var sb strings.Builder
	for i := 0; sb.Len() == 0 || strings.Count(sb.String(), "\n") < n; i++ {
		switch i % 4 {
		case 0:
			fmt.Fprintf(&sb, "var%d := \"value %d\" + 'x'\n", i, i)
		case 1:
			fmt.Fprintf(&sb, "# comment %d\n", i)
		case 2:
			fmt.Fprintf(&sb, "@recipe-%d arg=\"d\": dep\n", i)
		default:
			fmt.Fprintf(&sb, "\techo {{ var%d }} done %d\n", i-3, i)
		}
	}
	return []byte(sb.String())
}

func BenchmarkLex(b *testing.B) {
	src := generatedJustfile(1000)
	if _, err := lexer.Lex("justfile", src); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(src)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := lexer.Lex("justfile", src); err != nil {
			b.Fatal(err)
		}
	}
}

func TestLexBrackets(t *testing.T) {
	wantTokens(t, `["a", "b"]`, []lexer.Token{
		tok(lexer.LBracket, "[", 1, 1),
		tok(lexer.String, "a", 1, 2),
		tok(lexer.Comma, ",", 1, 5),
		tok(lexer.String, "b", 1, 7),
		tok(lexer.RBracket, "]", 1, 10),
		tok(lexer.Newline, "", 1, 11),
		tok(lexer.EOF, "", 1, 11),
	})
}

// TestLexStringRawKeepsSourceText checks that a String token keeps its quotes
// and escapes, which a consumer needs to show the string as it was written.
func TestLexStringRawKeepsSourceText(t *testing.T) {
	cases := map[string]string{
		`"a\tb"`:   `"a\tb"`,
		`'a\tb'`:   `'a\tb'`,
		"'B\t'":    "'B\t'",
		`x := "y"`: `"y"`,
	}
	for src, want := range cases {
		toks, err := lexer.Lex("justfile", []byte(src))
		if err != nil {
			t.Fatalf("Lex(%q) error: %v", src, err)
		}
		var raws []string
		for _, tk := range toks {
			if tk.Kind == lexer.String {
				raws = append(raws, tk.Raw)
			} else if tk.Raw != "" {
				t.Errorf("Lex(%q): token %+v has Raw set but is not a String", src, tk)
			}
		}
		if len(raws) != 1 || raws[0] != want {
			t.Errorf("Lex(%q) String Raw = %q, want [%q]", src, raws, want)
		}
	}
}
