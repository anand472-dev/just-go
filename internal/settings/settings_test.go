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

// The dotenv settings take a value, held as a list of one element until lists
// are implemented. They are not set when absent.
func TestResolveEvaluatesDotenvSettings(t *testing.T) {
	src := "d := '.env'\n" +
		"set dotenv-command := 'echo ' + d\n" +
		"set dotenv-filename := d\n" +
		"set dotenv-path := 'a'\n"
	got, err := Resolve(parse(t, src))
	if err != nil {
		t.Fatal(err)
	}
	want := Resolved{
		DotenvCommand:  []string{"echo .env"},
		DotenvFilename: []string{".env"},
		DotenvPath:     []string{"a"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Resolve = %+v, want %+v", got, want)
	}
	got, err = Resolve(parse(t, "set dotenv-path := 'a'\n"))
	if err != nil || got.DotenvFilename != nil || got.DotenvCommand != nil {
		t.Errorf("unset settings are not nil: %+v, %v", got, err)
	}
}

// A list is not implemented (R15): it is a ListError, after the faults in its
// elements, which are reported as upstream reports them. A variable that holds
// a list is a ListError at its use, not a non-const variable.
func TestResolveRefusesAList(t *testing.T) {
	for _, src := range []string{
		"set tempdir := ['a', 'b']\n",
		"set tempdir := []\n",
		"set working-directory := ['a']\n",
		"set dotenv-path := ['a', ['b']]\n",
		"set shell := ['echo', ['foo', 'bar']]\n",
		"x := ['a']\nset tempdir := x\n",
		"x := ['a']\nset dotenv-filename := 'f' + x\n",
	} {
		_, err := Resolve(parse(t, src))
		var le *ListError
		if !errors.As(err, &le) {
			t.Fatalf("Resolve(%q) = %v, want a *ListError", src, err)
		}
		if !strings.Contains(le.Error(), "list values are not supported") {
			t.Errorf("Resolve(%q) = %q", src, le.Error())
		}
		if le.Diag.Kind != diag.KindAnalysis || le.Diag.File != "justfile" {
			t.Errorf("Resolve(%q) diag = %+v", src, le.Diag)
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

// ConstValue is the evaluation upstream applies to a `[doc]` attribute.
func TestConstValue(t *testing.T) {
	jf := parse(t, "a := b + 'x'\nb := 'y'\nc := uppercase('z')\nd := c\n")
	for _, tc := range []struct {
		src  string
		want string
	}{{"a", "yx"}, {"'p' + a", "pyx"}, {"''", ""}} {
		e := parse(t, "x := "+tc.src+"\n"+"a := b + 'x'\nb := 'y'\n").Assignments[0].Value
		got, err := ConstValue(jf, e)
		if err != nil || got != tc.want {
			t.Errorf("ConstValue(%s) = %q, %v, want %q", tc.src, got, err, tc.want)
		}
	}
	for _, tc := range []struct{ src, msg string }{
		{"c", "cannot access non-const variable `c` in const context"},
		{"d", "cannot access non-const variable `d` in const context"},
		{"uppercase('a')", "cannot call functions in const context"},
		{"nope", "variable `nope` not defined"},
	} {
		e := parse(t, "x := "+tc.src+"\n").Assignments[0].Value
		_, err := ConstValue(jf, e)
		var de *diag.Error
		if !errors.As(err, &de) || de.Msg != tc.msg {
			t.Errorf("ConstValue(%s) error = %v, want %q", tc.src, err, tc.msg)
		}
	}
}
