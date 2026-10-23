package dotenv

import (
	"errors"
	"reflect"
	"testing"
)

// parseCases are checked against upstream just 1.59.0 (dotenvy 0.15.7). Each
// row was run through the oracle binary with a justfile that prints the value
// of every key, and the expected value is the one it printed. Error text is
// upstream's; the oracle's path prefix is left out.
var parseCases = []struct {
	name string
	src  string
	want map[string]string
	err  string
}{
	{"plain", "A=1\n", map[string]string{"A": "1"}, ""},
	{"no_trailing_newline", "A=1", map[string]string{"A": "1"}, ""},
	{"empty_file", "", map[string]string{}, ""},
	{"only_newline", "\n", map[string]string{}, ""},
	{"crlf", "A=1\r\nB=2\r\n", map[string]string{"A": "1", "B": "2"}, ""},
	{"lone_cr_value", "A=1\r", map[string]string{"A": "1"}, ""},
	{"ws_around_key_eq", "  A  =  1  \n", map[string]string{"A": "1"}, ""},
	{"leading_ws_comment", "   # c\nA=1\n", map[string]string{"A": "1"}, ""},
	{"comment_after_ws", "A=1 # c\n", map[string]string{"A": "1"}, ""},
	{"hash_no_ws", "A=1#c\n", map[string]string{"A": "1#c"}, ""},
	{"hash_after_eq", "A=#c\n", map[string]string{"A": ""}, ""},
	{"hash_after_eq_ws", "A= #c\n", map[string]string{"A": ""}, ""},
	{"empty_value", "A=\n", map[string]string{"A": ""}, ""},
	{"empty_value_ws", "A=   \n", map[string]string{"A": ""}, ""},
	{"empty_value_eof_ws", "A=   ", nil, "Error parsing line: 'A=   ', error at line index: 5"},
	{"export_prefix", "export A=1\n", map[string]string{"A": "1"}, ""},
	{"export_eq_key", "export=1\n", map[string]string{"export": "1"}, ""},
	{"export_space_eq", "export = 1\n", map[string]string{"export": "1"}, ""},
	{"export_no_eq", "export A\n", nil, "Error parsing line: 'export A', error at line index: 8"},
	{"export_bad_key", "export 1A=1\n", nil, "Error parsing line: 'export 1A=1', error at line index: 7"},
	{"export_only", "export\n", nil, "Error parsing line: 'export', error at line index: 6"},
	{"key_underscore_start", "_A1=1\n", map[string]string{"_A1": "1"}, ""},
	{"key_digit_start", "1A=1\n", nil, "Error parsing line: '1A=1', error at line index: 0"},
	{"key_dot_start", ".A=1\n", nil, "Error parsing line: '.A=1', error at line index: 0"},
	{"key_hyphen", "A-B=1\n", nil, "Error parsing line: 'A-B=1', error at line index: 1"},
	{"key_unicode", "é=1\n", nil, "Error parsing line: 'é=1', error at line index: 0"},
	{"key_no_eq", "A\n", nil, "Error parsing line: 'A', error at line index: 1"},
	{"bom", "\ufeffA=1\n", nil, "Error parsing line: '\ufeffA=1', error at line index: 0"},
	{"dq_basic", "A=\"x y\"\n", map[string]string{"A": "x y"}, ""},
	{"dq_escapes", "A=\"\\\\ \\' \\\" \\$ \\  \\n\"\n", map[string]string{"A": "\\ ' \" $   \n"}, ""},
	{"dq_bad_escape", "A=\"\\t\"\n", nil, "Error parsing line: '\"\\t\"', error at line index: 2"},
	{"dq_hash_inside", "A=\"a # b\"\n", map[string]string{"A": "a # b"}, ""},
	{"dq_then_hash", "A=\"a\"#b\n", map[string]string{"A": "a#b"}, ""},
	{"dq_then_comment", "A=\"a\" #b\n", map[string]string{"A": "a"}, ""},
	{"dq_then_text", "A=\"a\" b\n", nil, "Error parsing line: '\"a\" b', error at line index: 4"},
	{"dq_unterminated", "A=\"abc\n", nil, "Error parsing line: 'A=\"abc\n', error at line index: 7"},
	{"dq_multiline", "A=\"a\nb\"\nC=2\n", map[string]string{"A": "a\nb", "C": "2"}, ""},
	{"dq_multiline_comment_line", "A=\"a\n# b\"\n", map[string]string{"A": "a\n# b"}, ""},
	{"dq_multiline_crlf", "A=\"a\r\nb\"\r\n", map[string]string{"A": "a\r\nb"}, ""},
	{"sq_basic", "A='x y'\n", map[string]string{"A": "x y"}, ""},
	{"sq_no_escape", "A='a\\nb\\'\n", nil, "Error parsing line: 'A='a\\nb\\'\n', error at line index: 10"},
	{"sq_dollar", "A='$HOME'\n", map[string]string{"A": "$HOME"}, ""},
	{"sq_multiline", "A='a\nb'\nC=2\n", map[string]string{"A": "a\nb", "C": "2"}, ""},
	{"sq_unterminated", "A='abc\n", nil, "Error parsing line: 'A='abc\n', error at line index: 7"},
	{"sq_then_text", "A='a' b\n", nil, "Error parsing line: ''a' b', error at line index: 4"},
	{"sq_escape_quote_concat", "A='sweet $\\fgs'\\''fds'\n", map[string]string{"A": "sweet $\\fgs'fds"}, ""},
	{"unq_escapes", "A=my\\ cool\\ value\nB=\\$sweet\nC=s\\\\x\nD=a\\\"b\nE=a\\'b\nF=a\\nb\n", map[string]string{"A": "my cool value", "B": "$sweet", "C": "s\\x", "D": "a\"b", "E": "a'b", "F": "a\nb"}, ""},
	{"unq_bad_escape", "A=\\f\n", nil, "Error parsing line: '\\f', error at line index: 1"},
	{"unq_escape_t_char", "A=\\t\n", nil, "Error parsing line: '\\t', error at line index: 1"},
	{"unq_space_then_text", "A=x y\n", nil, "Error parsing line: 'x y', error at line index: 2"},
	{"unq_tab_then_text", "A=x\ty\n", nil, "Error parsing line: 'x\ty', error at line index: 2"},
	{"unq_tab_trailing", "A=x\t\n", map[string]string{"A": "x"}, ""},
	{"unq_quote_mid", "A=a\"b c\"d\n", map[string]string{"A": "ab cd"}, ""},
	{"unq_backslash_eol", "A=a\\\nB=b\n", map[string]string{"A": "a", "B": "b"}, ""},
	{"unq_backslash_eof", "A=a\\", nil, "Error parsing line: 'A=a\\', error at line index: 4"},
	{"unq_trailing_space_eof", "A=a ", nil, "Error parsing line: 'A=a ', error at line index: 4"},
	{"unq_trailing_space_eol", "A=a \n", map[string]string{"A": "a"}, ""},
	{"unq_trailing_tab_eol", "A=a\t\n", map[string]string{"A": "a"}, ""},
	{"unq_eq_in_value", "A=b=c\n", map[string]string{"A": "b=c"}, ""},
	{"unq_utf8_value", "A=héllo\n", map[string]string{"A": "héllo"}, ""},
	{"dup_last_wins", "A=1\nA=2\n", map[string]string{"A": "2"}, ""},
	{"subst_simple", "A=1\nB=$A\n", map[string]string{"A": "1", "B": "1"}, ""},
	{"subst_braces", "A=1\nB=${A}x\n", map[string]string{"A": "1", "B": "1x"}, ""},
	{"subst_dq_braces", "A=1\nB=\"${A}\"\n", map[string]string{"A": "1", "B": "1"}, ""},
	{"subst_dq_bare", "A=1\nB=\"$A\"\n", nil, "Error parsing line: '\"$A\"', error at line index: 3"},
	{"subst_dq_bare_then_text", "A=1\nB=\"$A.x\"\n", map[string]string{"A": "1", "B": "1.x"}, ""},
	{"subst_dq_inner", "A=1\nB=\"x$A y\"\n", map[string]string{"A": "1", "B": "x1 y"}, ""},
	{"subst_undefined", "B=>$NOPE<>${NOPE2}<\n", map[string]string{"B": "><><"}, ""},
	{"subst_escaped_dollar", "B=\\$A\n", map[string]string{"B": "$A"}, ""},
	{"subst_escaped_dollar_braces", "B=\\${A}\n", map[string]string{"B": "${A}"}, ""},
	{"subst_escaped_dq", "B=\"\\$A\"\n", map[string]string{"B": "$A"}, ""},
	{"subst_sq_literal", "A=1\nB='$A'\n", map[string]string{"A": "1", "B": "$A"}, ""},
	{"subst_sq_braces", "A=1\nB='${A}'\n", map[string]string{"A": "1", "B": "${A}"}, ""},
	{"subst_underscore_ends_name", "A=test\nA_1=t2\nB=$A_1\n", map[string]string{"A": "test", "A_1": "t2", "B": "test_1"}, ""},
	{"subst_digits", "A1=test\nB=$A1x\n", map[string]string{"A1": "test", "B": ""}, ""},
	{"subst_dot_ends_name", "A=test\nB=$A.x\n", map[string]string{"A": "test", "B": "test.x"}, ""},
	{"subst_dollar_end", "B=x$\n", map[string]string{"B": "x"}, ""},
	{"subst_dollar_space", "B=$ x\n", map[string]string{"B": " x"}, ""},
	{"subst_dollar_hash", "A=1\nB=$A#x\n", map[string]string{"A": "1", "B": "1#x"}, ""},
	{"subst_dollar_quote_unq", "A=1\nB=$A'x'\n", nil, "Error parsing line: '$A'x'', error at line index: 4"},
	{"subst_brace_unterminated", "A=1\nB=${A\n", nil, "Error parsing line: '${A', error at line index: 2"},
	{"subst_brace_space_name", "B=${ A}\n", map[string]string{"B": ""}, ""},
	{"subst_brace_empty", "B=${}x\n", map[string]string{"B": "x"}, ""},
	{"subst_brace_in_dq_unterminated", "B=\"${A\"\n", nil, "Error parsing line: '\"${A\"', error at line index: 4"},
	{"subst_empty_key_empty_value", "A=\nB=x$A\n", map[string]string{"A": "", "B": "x"}, ""},
	{"subst_later_key_undefined", "B=$A\nA=1\n", map[string]string{"B": "", "A": "1"}, ""},
	{"subst_self_ref", "A=x\nA=$A$A\n", map[string]string{"A": "xx"}, ""},
	{"subst_unicode_name", "é=1\nB=$é\n", nil, "Error parsing line: 'é=1', error at line index: 0"},
	{"subst_unicode_adjacent", "A=1\nB=$Aé\n", map[string]string{"A": "1", "B": ""}, ""},
	{"subst_in_multiline_dq", "A=1\nB=\"x\n$A\"\n", nil, "Error parsing line: '\"x\n$A\"', error at line index: 5"},
	{"subst_dollar_double", "A=1\nB=$$A\n", map[string]string{"A": "1", "B": "1"}, ""},
	{"subst_brace_adjacent_dollar", "A=1\nB=${A}$A\n", map[string]string{"A": "1", "B": "11"}, ""},
	{"subst_escaped_backslash_dollar", "A=1\nB=\\\\$A\n", map[string]string{"A": "1", "B": "\\1"}, ""},
	{"empty_key_eq_value_sub", "A=\nB=${A}x\n", map[string]string{"A": "", "B": "x"}, ""},
	{"error_line_ws", "  invalid    \n", nil, "Error parsing line: '  invalid    ', error at line index: 9"},
	{"error_invalid_line", "very bacon = yes indeed\n", nil, "Error parsing line: 'very bacon = yes indeed', error at line index: 5"},
	{"error_eq_first", "=value\n", nil, "Error parsing line: '=value', error at line index: 0"},
	{"error_illegal", "<><><>\n", nil, "Error parsing line: '<><><>', error at line index: 0"},
	{"error_after_good", "A=1\nB c\n", nil, "Error parsing line: 'B c', error at line index: 2"},
	{"error_escape_in_ws_eof", "A=1\n\\", nil, "Error parsing line: '\\', error at line index: 1"},
	{"comment_with_quote", "# it's \"\nA=1\n", map[string]string{"A": "1"}, ""},
	{"comment_quote_after_ws", "A=1 # it's\nB=2\n", map[string]string{"A": "1", "B": "2"}, ""},
	{"comment_only_eof", "# c", map[string]string{}, ""},
	{"unq_then_quote_then_hash", "A=x\"#\"\n", map[string]string{"A": "x#"}, ""},
	{"sq_hash", "A='#x'\n", map[string]string{"A": "#x"}, ""},
	{"dq_unicode_escape_like", "A=\"\\u0041\"\n", nil, "Error parsing line: '\"\\u0041\"', error at line index: 2"},
	{"unq_escape_at_eof_after_ws", "A=a \\", nil, "Error parsing line: 'A=a \\', error at line index: 5"},
	{"sq_backslash_quote_split", "A='a\\'\nB=2\n", nil, "Error parsing line: 'A='a\\'\nB=2\n', error at line index: 11"},
	{"dq_escape_newline", "A=\"a\\\nb\"\n", nil, "Error parsing line: '\"a\\\nb\"', error at line index: 3"},
	{"dq_escape_n_only", "A=\"a\\nb\"\n", map[string]string{"A": "a\nb"}, ""},
	{"whitespace_only_line", "   \t \nA=1\n", map[string]string{"A": "1"}, ""},
	{"trailing_cr_ws", "A=1 \r\n", map[string]string{"A": "1"}, ""},
	{"value_leading_ws_dq", "A=    \"  x\"  \n", map[string]string{"A": "  x"}, ""},
	{"subst_brace_escape_inside", "A=1\nB=${A\\}\n", map[string]string{"A": "1", "B": ""}, ""},
	{"subst_brace_name_hyphen", "A-B=1\nC=${A-B}\n", nil, "Error parsing line: 'A-B=1', error at line index: 1"},
	{"unq_dollar_then_quote_dq_close", "A=1\nB=\"$A\"x\n", nil, "Error parsing line: '\"$A\"x', error at line index: 4"},
	{"rule_key_dot_allowed", "a.b=1\n", map[string]string{"a.b": "1"}, ""},
	{"rule_corpus_bare_word_after_space", "FOO=bar baz", nil, "Error parsing line: 'bar baz', error at line index: 4"},
	{"rule_corpus_plain_value", "KEY=value\n", map[string]string{"KEY": "value"}, ""},
	{"rule_corpus_two_keys", "BAR=bar\nSHARED=from-bar\n", map[string]string{"BAR": "bar", "SHARED": "from-bar"}, ""},
	{"rule_corpus_no_newline", "JUST_TEST_VARIABLE=bar", map[string]string{"JUST_TEST_VARIABLE": "bar"}, ""},
	{"rule_key_then_space_then_value_error", "A B=1\n", nil, "Error parsing line: 'A B=1', error at line index: 2"},
	{"rule_sq_open_across_lines_then_comment", "A='x\\' #c\nB=1'\n", map[string]string{"A": "x\\"}, ""},
	{"rule_sq_join_space_then_comment_stops_value", "A='x\\'  #c\nB=1'\n", map[string]string{"A": "x\\"}, ""},
	{"rule_escaped_space_is_not_comment", "A=x\\ #c\n", map[string]string{"A": "x #c"}, ""},
	{"rule_dq_escaped_quote_joins_lines", "A=\"x\\\" #c\nB=1\"\n", map[string]string{"A": "x\" #c\nB=1"}, ""},
}

// envCases exercise ParseWithEnv. Upstream checks the process environment before
// the file's earlier assignments during substitution; the oracle run used the
// same environment for each case.
var envCases = []struct {
	name string
	src  string
	env  map[string]string
	want map[string]string
}{
	{"env_over_file_bare", "A=file\nB=$A\n", map[string]string{"A": "env"}, map[string]string{"A": "file", "B": "env"}},
	{"env_over_file_braced", "A=file\nB=${A}\n", map[string]string{"A": "env"}, map[string]string{"A": "file", "B": "env"}},
	{"env_only_variable", "B=$ENVONLY\n", map[string]string{"ENVONLY": "fromenv"}, map[string]string{"B": "fromenv"}},
	{"env_does_not_change_assignment", "A=file\n", map[string]string{"A": "env"}, map[string]string{"A": "file"}},
	{"env_miss_falls_back_to_file", "A=1\nB=$A\n", map[string]string{"X": "y"}, map[string]string{"A": "1", "B": "1"}},
}

func TestParseMatchesUpstream(t *testing.T) {
	for _, tt := range parseCases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse([]byte(tt.src))
			if tt.err != "" {
				if err == nil || err.Error() != tt.err {
					t.Fatalf("Parse(%q) error = %v; want %q", tt.src, err, tt.err)
				}
				if got != nil {
					t.Fatalf("Parse(%q) returned %v with an error", tt.src, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q) error = %v", tt.src, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Parse(%q) = %q; want %q", tt.src, got, tt.want)
			}
		})
	}
}

func TestParseWithEnvMatchesUpstream(t *testing.T) {
	for _, tt := range envCases {
		t.Run(tt.name, func(t *testing.T) {
			lookup := func(name string) (string, bool) {
				v, ok := tt.env[name]
				return v, ok
			}
			got, err := ParseWithEnv([]byte(tt.src), lookup)
			if err != nil {
				t.Fatalf("ParseWithEnv(%q) error = %v", tt.src, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ParseWithEnv(%q) = %q; want %q", tt.src, got, tt.want)
			}
		})
	}
}

func TestParseInvalidUTF8(t *testing.T) {
	got, err := Parse([]byte("A=\xff\n"))
	if !errors.Is(err, ErrInvalidUTF8) || got != nil {
		t.Fatalf("Parse(invalid UTF-8) = %v, %v; want nil, ErrInvalidUTF8", got, err)
	}
	if err.Error() != "stream did not contain valid UTF-8" {
		t.Fatalf("ErrInvalidUTF8 text = %q", err.Error())
	}
}

func TestParseInvalidUTF8InContinuation(t *testing.T) {
	// The bad byte is on a continuation line of a quoted value, which the
	// splitter reads as its own piece.
	if _, err := Parse([]byte("A=\"x\n\xff\"\n")); !errors.Is(err, ErrInvalidUTF8) {
		t.Fatalf("Parse = %v; want ErrInvalidUTF8", err)
	}
}

func TestParseEmptyInputIsEmptyMap(t *testing.T) {
	got, err := Parse(nil)
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("Parse(nil) = %v, %v; want empty non-nil map", got, err)
	}
}

func TestParseWithEnvNilLookup(t *testing.T) {
	got, err := ParseWithEnv([]byte("B=$HOME\nC=${HOME}x\n"), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"B": "", "C": "x"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseWithEnv(nil lookup) = %q; want %q", got, want)
	}
}

func TestLineErrorFields(t *testing.T) {
	_, err := Parse([]byte("A=1\nB c\n"))
	var le LineError
	if !errors.As(err, &le) {
		t.Fatalf("error %v is not a LineError", err)
	}
	if le.Line != "B c" || le.Index != 2 {
		t.Fatalf("LineError = %+v; want Line %q, Index 2", le, "B c")
	}
	if got := le.Error(); got != "Error parsing line: 'B c', error at line index: 2" {
		t.Fatalf("Error() = %q", got)
	}
}

func TestLineErrorUnterminatedKeepsNewline(t *testing.T) {
	_, err := Parse([]byte("A='abc\n"))
	var le LineError
	if !errors.As(err, &le) || le.Line != "A='abc\n" || le.Index != 7 {
		t.Fatalf("error = %#v; want Line with newline, Index 7", err)
	}
}

func TestCommentOnlyInputsAreSkipped(t *testing.T) {
	for _, src := range []string{"# only\n", "   # indented\n", "# no newline", "\n\n  \t\n"} {
		got, err := Parse([]byte(src))
		if err != nil || len(got) != 0 {
			t.Errorf("Parse(%q) = %v, %v; want empty", src, got, err)
		}
	}
}

// scanPiece never continues past a comment, so nextState only sees
// stateComment if a caller feeds it directly; the state must not change.
func TestNextStateCommentIsTerminal(t *testing.T) {
	for _, c := range "#x\n\"'\\ " {
		if got := nextState(stateComment, c); got != stateComment {
			t.Fatalf("nextState(stateComment, %q) = %v; want stateComment", c, got)
		}
	}
}

// benchSrc is a typical .env file that exercises quoting, comments, export and
// substitution.
const benchSrc = `# application settings
export APP_NAME=just-go
PORT=8080
GREETING="hello, world"  # trailing comment
PATH_PREFIX='/opt/app'
DATA_DIR=${PATH_PREFIX}/data
LOG_LEVEL=info
MULTILINE="line one
line two"
ESCAPED=a\ b\n
`

func BenchmarkParse(b *testing.B) {
	src := []byte(benchSrc)
	b.ReportAllocs()
	b.SetBytes(int64(len(src)))
	for i := 0; i < b.N; i++ {
		if _, err := Parse(src); err != nil {
			b.Fatal(err)
		}
	}
}
