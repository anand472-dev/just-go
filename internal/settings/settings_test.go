package settings

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/parser"
)

// parse parses src, which must be valid.
func parse(t testing.TB, src string) *ast.Justfile {
	t.Helper()
	jf, err := parser.Parse("justfile", []byte(src))
	if err != nil {
		t.Fatalf("Parse(%q): %v", src, err)
	}
	return jf
}

func str(s string) *string { return &s }

func TestResolveEvaluatesExpressionSettings(t *testing.T) {
	src := "dir := 'bar'\n" +
		"set working-directory := dir + '-bob'\n" +
		"set tempdir := '.'\n" +
		"set shell := [sh, '-c']\n" +
		"sh := 'bash'\n" +
		"set windows-shell := ['powershell', '-Command']\n" +
		"set script-interpreter := ['sh', '-eu']\n"
	got, err := Resolve(parse(t, src))
	if err != nil {
		t.Fatal(err)
	}
	want := Resolved{
		Shell:             []string{"bash", "-c"},
		WindowsShell:      []string{"powershell", "-Command"},
		ScriptInterpreter: []string{"sh", "-eu"},
		Tempdir:           str("."),
		WorkingDirectory:  str("bar-bob"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Resolve = %+v, want %+v", got, want)
	}
}

// The dotenv settings take a value, which may be a list; a list is flattened,
// as upstream's value is. They are not set when absent.
func TestResolveEvaluatesDotenvSettings(t *testing.T) {
	src := "d := '.env'\n" +
		"set dotenv-command := 'echo ' + d\n" +
		"set dotenv-filename := d\n" +
		"set dotenv-path := ['a', d, ['b', []], ]\n"
	got, err := Resolve(parse(t, src))
	if err != nil {
		t.Fatal(err)
	}
	want := Resolved{
		DotenvCommand:  []string{"echo .env"},
		DotenvFilename: []string{".env"},
		DotenvPath:     []string{"a", ".env", "b"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Resolve = %+v, want %+v", got, want)
	}
}

// An empty list is a value: set, with nothing in it
// (dotenv::empty_filename_list_is_unset#1 relies on telling it from unset).
func TestResolveKeepsAnEmptyListApartFromUnset(t *testing.T) {
	got, err := Resolve(parse(t, "set dotenv-filename := []\n"))
	if err != nil || got.DotenvFilename == nil || len(got.DotenvFilename) != 0 {
		t.Errorf("DotenvFilename = %#v, %v; want an empty, non-nil list", got.DotenvFilename, err)
	}
	if got.DotenvPath != nil || got.DotenvCommand != nil {
		t.Errorf("unset settings are not nil: %+v", got)
	}
}

// A setting that holds one string takes a list of one element as that string
// and refuses any other list, in upstream's words.
func TestResolveAStringSettingTakesAListOfOne(t *testing.T) {
	got, err := Resolve(parse(t, "x := ['t']\nset tempdir := x\nset working-directory := ['w']\n"))
	if err != nil || *got.Tempdir != "t" || *got.WorkingDirectory != "w" {
		t.Errorf("Resolve = %+v, %v", got, err)
	}
}

func TestResolveRefusesAListInAStringSetting(t *testing.T) {
	for _, tc := range []struct {
		src, msg         string
		line, col, width int
	}{
		{"set tempdir := ['a', 'b']\n", `list value ["a", "b"] assigned to ` + "`tempdir`" + ` setting`, 1, 5, 7},
		{"set tempdir := []\n", "list value [] assigned to `tempdir` setting", 1, 5, 7},
		{"x := ['a', 'b']\nset tempdir := x\n", `list value ["a", "b"] assigned to ` + "`tempdir`" + ` setting`, 2, 5, 7},
		{"\n\nset working-directory := ['a\"\\\n\t', 'b']\n", `list value ["a\"\\\n\t", "b"] assigned to ` + "`working-directory`" + ` setting`, 3, 5, 17},
	} {
		_, err := Resolve(parse(t, tc.src))
		var de *diag.Error
		if !errors.As(err, &de) {
			t.Fatalf("Resolve(%q) = %v, want a *diag.Error", tc.src, err)
		}
		if de.Msg != tc.msg || de.Line != tc.line || de.Col != tc.col || de.Width != tc.width || de.Kind != diag.KindAnalysis {
			t.Errorf("Resolve(%q) = %+v, want %q at %d:%d width %d", tc.src, de, tc.msg, tc.line, tc.col, tc.width)
		}
	}
}

// Upstream's element display escapes five characters and writes the rest as
// they are (oracle/src/src/element.rs; checked against the oracle).
func TestDebugListEscapesAsUpstreamDoes(t *testing.T) {
	for _, tc := range []struct {
		values []string
		want   string
	}{
		{nil, "[]"},
		{[]string{""}, `[""]`},
		{[]string{"a\x00b", "\x01", "\x7f"}, "[\"a\x00b\", \"\x01\", \"\x7f\"]"},
		{[]string{"é✓ 日本", "it's"}, `["é✓ 日本", "it's"]`},
		{[]string{"\t\r\n\"\\"}, `["\t\r\n\"\\"]`},
	} {
		if got := debugList(tc.values); got != tc.want {
			t.Errorf("debugList(%q) = %s, want %s", tc.values, got, tc.want)
		}
	}
}

// A hand-built tree may hold a list in a setting that no `set` statement
// records; the error is then without a location.
func TestResolveRefusesAListInATreeWithoutSettingSites(t *testing.T) {
	jf := &ast.Justfile{File: "justfile", Settings: ast.Settings{Tempdir: ast.List{}}}
	_, err := Resolve(jf)
	var de *diag.Error
	if !errors.As(err, &de) || de.Msg != "list value [] assigned to `tempdir` setting" || de.Line != 0 {
		t.Errorf("Resolve = %v", err)
	}
}

// `+` joins a string to every element of a list, and lists of one length
// pairwise, as upstream's Value::apply does; any other pair is refused.
func TestResolveAddsListsAsUpstreamDoes(t *testing.T) {
	for _, tc := range []struct {
		src  string
		want []string
	}{
		{"set dotenv-path := 'a' + 'b'\n", []string{"ab"}},
		{"set dotenv-path := ['a', 'b'] + 'c'\n", []string{"ac", "bc"}},
		{"set dotenv-path := 'c' + ['a', 'b']\n", []string{"ca", "cb"}},
		{"set dotenv-path := ['a', 'b'] + ['c', 'd']\n", []string{"ac", "bd"}},
		{"set dotenv-path := ['a'] + ['c', 'd']\n", []string{"ac", "ad"}},
		{"set dotenv-path := [] + []\n", []string{}},
	} {
		got, err := Resolve(parse(t, tc.src))
		if err != nil || !reflect.DeepEqual(got.DotenvPath, tc.want) {
			t.Errorf("Resolve(%q) = %#v, %v; want %#v", tc.src, got.DotenvPath, err, tc.want)
		}
	}
	for _, src := range []string{
		"set dotenv-path := 'c' + []\n",
		"set dotenv-path := [] + 'c'\n",
		"set dotenv-path := ['a', 'b'] + ['c', 'd', 'e']\n",
		"set dotenv-path := ['a', 'b'] + []\n",
	} {
		_, err := Resolve(parse(t, src))
		if err == nil || !strings.Contains(err.Error(), "cannot concatenate list values") {
			t.Errorf("Resolve(%q) = %v", src, err)
		}
	}
}

func TestResolveOfNoSettingsIsEmpty(t *testing.T) {
	got, err := Resolve(parse(t, "foo:\n  echo\n"))
	if err != nil || !reflect.DeepEqual(got, Resolved{}) {
		t.Errorf("Resolve = %+v, %v; want the zero value", got, err)
	}
}

// Each row is a case of the settings corpus group, or the same rule at
// another setting. The message, line and column are the oracle's.
func TestResolveErrorsMatchUpstream(t *testing.T) {
	for _, tc := range []struct {
		name, src, msg string
		line, col, w   int
	}{
		{"settings::undefined_variable_in_working_directory", "set working-directory := foo\n",
			"variable `foo` not defined", 1, 26, 3},
		{"settings::undefined_variable_in_tempdir", "set tempdir := foo\n",
			"variable `foo` not defined", 1, 16, 3},
		{"settings::undefined_variable_in_shell_command", "set shell := [foo]\n",
			"variable `foo` not defined", 1, 15, 3},
		{"settings::undefined_variable_in_shell_argument", "set shell := ['foo', bar]\n",
			"variable `bar` not defined", 1, 22, 3},
		{"settings::undefined_variable_in_windows_shell_command", "set windows-shell := [foo]\n",
			"variable `foo` not defined", 1, 23, 3},
		{"settings::undefined_variable_in_script_interpreter_argument", "set script-interpreter := ['foo', bar]\n",
			"variable `bar` not defined", 1, 35, 3},
		{"settings::function_call", "set working-directory := arch()\n",
			"cannot call functions in const context", 1, 26, 4},
		{"settings::non_const_variable", "foo := arch()\n\nset working-directory := foo\n",
			"cannot access non-const variable `foo` in const context", 3, 26, 3},
		{"a variable that names a non-const one", "a := arch()\nb := a\nset tempdir := b\n",
			"cannot access non-const variable `b` in const context", 3, 16, 1},
		{"a call on the left of a concatenation", "set tempdir := arch() + 'a'\n",
			"cannot call functions in const context", 1, 16, 4},
		{"a call inside a concatenation", "set tempdir := 'a' + arch()\n",
			"cannot call functions in const context", 1, 22, 4},
		{"settings::undefined_variable_in_dotenv_command", "set dotenv-command := foo\n",
			"variable `foo` not defined", 1, 23, 3},
		{"settings::undefined_variable_in_dotenv_filename", "set dotenv-filename := foo\n",
			"variable `foo` not defined", 1, 24, 3},
		{"settings::undefined_variable_in_dotenv_path", "set dotenv-path := foo\n",
			"variable `foo` not defined", 1, 20, 3},
		{"a variable inside a list", "set dotenv-path := ['a', foo]\n",
			"variable `foo` not defined", 1, 26, 3},
		{"a call inside a list", "set shell := ['a', arch()]\n",
			"cannot call functions in const context", 1, 20, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The parser accepts these; the analyzer would reject some earlier
			// for other reasons, which is why Resolve is called directly.
			_, err := Resolve(parse(t, tc.src))
			var de *diag.Error
			if !errors.As(err, &de) {
				t.Fatalf("Resolve = %v, want a *diag.Error", err)
			}
			if de.Msg != tc.msg || de.Line != tc.line || de.Col != tc.col || de.Width != tc.w || de.File != "justfile" {
				t.Errorf("error = %+v, want %q at %d:%d width %d", de, tc.msg, tc.line, tc.col, tc.w)
			}
			if de.Kind != diag.KindAnalysis {
				t.Errorf("Kind = %v, want KindAnalysis", de.Kind)
			}
		})
	}
}

// Settings are evaluated in the order of their names, so the first failing
// one by name is the one reported, whatever its place in the file.
func TestResolveReportsFailuresInNameOrder(t *testing.T) {
	_, err := Resolve(parse(t, "set working-directory := a\nset tempdir := b\n"))
	var de *diag.Error
	if !errors.As(err, &de) || de.Msg != "variable `b` not defined" {
		t.Errorf("Resolve = %v; want the failure of tempdir, which sorts first", err)
	}
}

// A hand-built tree can hold an expression the parser never makes in a
// setting. It is refused rather than evaluated wrongly.
func TestResolveRefusesAnExpressionItDoesNotKnow(t *testing.T) {
	jf := &ast.Justfile{File: "justfile", Settings: ast.Settings{Tempdir: ast.Text{Value: "x"}}}
	_, err := Resolve(jf)
	if err == nil || !strings.Contains(err.Error(), "cannot evaluate expression of type ast.Text") {
		t.Errorf("Resolve = %v", err)
	}
}

// A cycle of assignments is the analyzer's to report; Resolve must still
// finish on a tree that has one.
func TestResolveFinishesOnACycle(t *testing.T) {
	_, err := Resolve(parse(t, "a := b\nb := a\nset tempdir := a\n"))
	if err == nil || !strings.Contains(err.Error(), "non-const variable `a`") {
		t.Errorf("Resolve = %v", err)
	}
}

func TestExpressionsAreInEvaluationOrder(t *testing.T) {
	jf := parse(t, "set working-directory := 'w'\nset windows-shell := ['ws', 'wa']\nset tempdir := 't'\n"+
		"set shell := ['s']\nset script-interpreter := ['i', 'j']\n"+
		"set dotenv-path := 'dp'\nset dotenv-filename := 'df'\nset dotenv-command := 'dc'\n")
	var got []string
	for _, e := range Expressions(jf) {
		got = append(got, e.(ast.StringLit).Value)
	}
	if want := []string{"dc", "df", "dp", "i", "j", "s", "t", "ws", "wa", "w"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Expressions = %q, want %q", got, want)
	}
	if got := Expressions(parse(t, "foo:\n")); len(got) != 0 {
		t.Errorf("Expressions of no settings = %v", got)
	}
}

func BenchmarkResolve(b *testing.B) {
	jf := parse(b, "dir := 'bar'\nset working-directory := dir + '-bob'\nset shell := ['bash', '-c']\nset tempdir := '.'\n")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Resolve(jf); err != nil {
			b.Fatal(err)
		}
	}
}
