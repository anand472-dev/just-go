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
		if got[i] != want[i] {
			t.Errorf("Lex(%q) token %d = %+v, want %+v", src, i, got[i], want[i])
		}
	}
}

// wantParseError lexes src and checks for a KindParse diag.Error at the position.
func wantParseError(t *testing.T, src string, line, col int, msg string) {
	t.Helper()
	toks, err := lexer.Lex("justfile", []byte(src))
	if err == nil {
		t.Fatalf("Lex(%q) = %+v, want an error", src, toks)
	}
	var de *diag.Error
	if !errors.As(err, &de) {
		t.Fatalf("Lex(%q) error %T is not a *diag.Error", src, err)
	}
	want := diag.Error{Kind: diag.KindParse, File: "justfile", Line: line, Col: col, Msg: msg}
	if *de != want {
		t.Errorf("Lex(%q) error = %+v, want %+v", src, *de, want)
	}
	if toks != nil {
		t.Errorf("Lex(%q) returned tokens alongside an error: %+v", src, toks)
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
	wantTokens(t, "@-a: b, +c = (d)\n", []lexer.Token{
		tok(lexer.At, "@", 1, 1),
		tok(lexer.Dash, "-", 1, 2),
		tok(lexer.Ident, "a", 1, 3),
		tok(lexer.Colon, ":", 1, 4),
		tok(lexer.Ident, "b", 1, 6),
		tok(lexer.Comma, ",", 1, 7),
		tok(lexer.Plus, "+", 1, 9),
		tok(lexer.Ident, "c", 1, 10),
		tok(lexer.Equals, "=", 1, 12),
		tok(lexer.LParen, "(", 1, 14),
		tok(lexer.Ident, "d", 1, 15),
		tok(lexer.RParen, ")", 1, 16),
		tok(lexer.Newline, "\n", 1, 17),
		tok(lexer.EOF, "", 2, 1),
	})
}

func TestLexIdentifiersMayContainDashesDigitsAndUnderscores(t *testing.T) {
	wantTokens(t, "_a-b_9\n", []lexer.Token{
		tok(lexer.Ident, "_a-b_9", 1, 1),
		tok(lexer.Newline, "\n", 1, 7),
		tok(lexer.EOF, "", 2, 1),
	})
}

func TestLexCookedStringEscapes(t *testing.T) {
	wantTokens(t, `"a\n\r\t\\\"b"`, []lexer.Token{
		tok(lexer.String, "a\n\r\t\\\"b", 1, 1),
		tok(lexer.Newline, "", 1, 15),
		tok(lexer.EOF, "", 1, 15),
	})
}

func TestLexCookedStringLineContinuation(t *testing.T) {
	wantTokens(t, "\"a\\\nb\" x", []lexer.Token{
		tok(lexer.String, "ab", 1, 1),
		tok(lexer.Ident, "x", 2, 4),
		tok(lexer.Newline, "", 2, 5),
		tok(lexer.EOF, "", 2, 5),
	})
	wantTokens(t, "\"a\\\r\nb\"", []lexer.Token{
		tok(lexer.String, "ab", 1, 1),
		tok(lexer.Newline, "", 2, 3),
		tok(lexer.EOF, "", 2, 3),
	})
}

func TestLexRawStringKeepsBackslashes(t *testing.T) {
	wantTokens(t, `'a\n"b'`, []lexer.Token{
		tok(lexer.String, `a\n"b`, 1, 1),
		tok(lexer.Newline, "", 1, 8),
		tok(lexer.EOF, "", 1, 8),
	})
}

func TestLexMultiLineStringNormalizesCRLFAndTracksLines(t *testing.T) {
	wantTokens(t, "'a\r\nb' c\n", []lexer.Token{
		tok(lexer.String, "a\nb", 1, 1),
		tok(lexer.Ident, "c", 2, 4),
		tok(lexer.Newline, "\n", 2, 5),
		tok(lexer.EOF, "", 3, 1),
	})
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

func TestLexInterpolationInAnAssignment(t *testing.T) {
	wantTokens(t, "x := {{y}}\n", []lexer.Token{
		tok(lexer.Ident, "x", 1, 1),
		tok(lexer.Assign, ":=", 1, 3),
		tok(lexer.Interp, "{{", 1, 6),
		tok(lexer.Ident, "y", 1, 8),
		tok(lexer.Close, "}}", 1, 9),
		tok(lexer.Newline, "\n", 1, 11),
		tok(lexer.EOF, "", 2, 1),
	})
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
}

func TestLexInvalidEscape(t *testing.T) {
	wantParseError(t, `x := "ab\q"`, 1, 9, "`\\q` is not a valid escape sequence")
}

func TestLexUnknownStartOfToken(t *testing.T) {
	wantParseError(t, "x := $", 1, 6, "unknown start of token '$'")
	wantParseError(t, "a\n\u00e9", 2, 1, "unknown start of token 'é'")
	wantParseError(t, "x\r", 1, 2, "unknown start of token '\\r'")
	wantParseError(t, "{ x", 1, 1, "unknown start of token '{'")
	wantParseError(t, "9", 1, 1, "unknown start of token '9'")
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
	_, err := lexer.Lex("sub/justfile", []byte("$"))
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
