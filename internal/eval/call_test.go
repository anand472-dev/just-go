package eval

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/eval/builtins"
	"gitea.com/wimwian/just-go/internal/sys/sysfake"
)

// lit is a string literal.
func lit(s string) ast.Expr { return ast.StringLit{Value: s} }

// callOf is a call of the function name at 2:7 with args.
func call(name string, args ...ast.Expr) ast.Call {
	return ast.Call{Name: name, Args: args, Line: 2, Col: 7}
}

func TestEvaluateCallsABuiltinWithTheValuesOfItsArguments(t *testing.T) {
	scope := Scope{"x": "bar"}
	for _, tc := range []struct {
		name string
		e    ast.Expr
		want string
	}{
		{"one argument", call("uppercase", lit("bar")), "BAR"},
		{"a variable", call("uppercase", ast.Var{Name: "x"}), "BAR"},
		{"a concatenation", call("uppercase", ast.Concat{Left: ast.Var{Name: "x"}, Right: lit("!")}), "BAR!"},
		{"three arguments", call("replace", lit("barbar"), lit("bar"), lit("foo")), "foofoo"},
		{"a call as an argument", call("quote", call("trim", lit("  it's  "))), `'it'\''s'`},
		{"variable arguments", call("join", lit("a"), lit("b"), ast.Var{Name: "x"}), "a/b/bar"},
		{"a call in a call of one argument less", call("append", lit("-"), call("lowercase", lit("A B"))), "a- b-"},
	} {
		got, err := Evaluate(tc.e, scope)
		if err != nil || got != tc.want {
			t.Errorf("%s: got %q, %v; want %q", tc.name, got, err, tc.want)
		}
	}
}

// functions::broken_extension_function#1: the error of a function is located
// at its name.
func TestEvaluateLocatesTheErrorOfABuiltinAtItsName(t *testing.T) {
	_, err := Evaluate(call("extension", lit("")), nil)
	requireRuntime(t, err, "could not extract extension from ``")
	var de *diag.Error
	if !errors.As(err, &de) || de.Line != 2 || de.Col != 7 || de.Width != len("extension") || de.File != "" {
		t.Errorf("error = %+v; want it at 2:7 with width 9 and no file", de)
	}
}

// functions::invalid_replace_regex#1
func TestEvaluateWordsABadPatternAsTheRegexCrateDoes(t *testing.T) {
	_, err := Evaluate(call("replace_regex", lit("barbarbar"), lit("foo\\"), lit("foo")), nil)
	requireRuntime(t, err, "regex parse error:\n    foo\\\n       ^\nerror: incomplete escape sequence, reached end of pattern prematurely")
}

func TestEvaluateStopsAtTheFirstArgumentThatFails(t *testing.T) {
	e := call("replace", ast.Var{Name: "first", Line: 5, Col: 3}, ast.Var{Name: "second"}, lit(""))
	_, err := Evaluate(e, nil)
	requireRuntime(t, err, "variable `first` not defined")
}

// The arguments of a call are evaluated with the commander of the caller.
func TestEvaluateWithRunsTheBackticksOfTheArguments(t *testing.T) {
	cmd := &scripted{out: []string{" out "}}
	got, err := EvaluateWith(call("trim", tick), nil, cmd)
	if err != nil || got != "out" {
		t.Errorf("got %q, %v; want %q", got, err, "out")
	}
}

func TestEvaluateReadsDirAsDirectory(t *testing.T) {
	_, err := Evaluate(call("cache_dir"), nil)
	requireRuntime(t, err, "function cache_dir is not implemented yet")
}

// A function that has a name and no implementation yet says so, located at
// its name.
func TestEvaluateRefusesAFunctionThatIsNotImplementedYet(t *testing.T) {
	_, err := Evaluate(call("uuid"), nil)
	requireRuntime(t, err, "function uuid is not implemented yet")
	var de *diag.Error
	if !errors.As(err, &de) || de.Line != 2 || de.Col != 7 || de.Width != len("uuid") {
		t.Errorf("location = %+v; want line 2, column 7, width 4", de)
	}
}

// The analyzer refuses these before the evaluator sees them; a caller that
// skips the analyzer gets an error, not a panic.
func TestEvaluateRefusesACallTheAnalyzerWouldRefuse(t *testing.T) {
	_, err := Evaluate(call("nope", lit("a")), nil)
	requireRuntime(t, err, "call to undefined function `nope`")
	_, err = Evaluate(call("uppercase"), nil)
	requireRuntime(t, err, "function `uppercase` called with 0 arguments")
	_, err = Evaluate(call("uppercase", lit("a"), lit("b")), nil)
	requireRuntime(t, err, "function `uppercase` called with 2 arguments")
}

// hosted is a Commander that is also Hosted.
type hosted struct {
	scripted
	host builtins.Host
}

func (h *hosted) Host() builtins.Host { return h.host }

// A builtin that reads the world is given the Host of the commander the
// evaluation runs with, and its arguments are evaluated first.
func TestEvaluateGivesABuiltinTheHostOfTheCommander(t *testing.T) {
	cmd := &hosted{host: builtins.Host{
		FS:      sysfake.FakeFS{"/w/f": "text", "/w/d/g": ""},
		WorkDir: "/w", Justfile: "/w/justfile", SourceFile: "/w/justfile", ModuleFile: "/w/justfile",
	}}
	scope := Scope{"name": "f"}
	for _, tc := range []struct {
		name string
		e    ast.Expr
		want string
	}{
		{"read", call("read", ast.Var{Name: "name"}), "text"},
		{"path_exists", call("path_exists", lit("d")), "true"},
		{"absolute_path", call("absolute_path", ast.Concat{Left: lit("d/"), Right: lit("g")}), "/w/d/g"},
		{"justfile", call("justfile"), "/w/justfile"},
		{"abbreviated", call("justfile_dir"), "/w"},
		{"in a call", call("uppercase", call("read", lit("f"))), "TEXT"},
	} {
		got, err := EvaluateWith(tc.e, scope, cmd)
		if err != nil || got != tc.want {
			t.Errorf("%s: got %q, %v; want %q", tc.name, got, err, tc.want)
		}
	}
}

func TestEvaluateLocatesTheErrorOfAHostBuiltinAtItsName(t *testing.T) {
	cmd := &hosted{host: builtins.Host{FS: sysfake.FakeFS{}, WorkDir: "/w"}}
	_, err := EvaluateWith(call("read", lit("bar")), nil, cmd)
	requireRuntime(t, err, "I/O error reading `bar`: No such file or directory (os error 2)")
	var de *diag.Error
	if !errors.As(err, &de) || de.Line != 2 || de.Col != 7 || de.Width != len("read") {
		t.Errorf("location = %+v; want line 2, column 7, width 4", de)
	}
}

// An evaluation that has no Host, such as one with no commander, cannot call a
// builtin that needs one, and says so at its name.
func TestEvaluateRefusesAHostBuiltinWithoutAHost(t *testing.T) {
	for _, cmd := range []Commander{nil, &scripted{}} {
		_, err := EvaluateWith(call("path_exists", lit("x")), nil, cmd)
		requireRuntime(t, err, "function `path_exists` cannot be called without a justfile")
		var de *diag.Error
		if !errors.As(err, &de) || de.Line != 2 || de.Col != 7 || de.Width != len("path_exists") {
			t.Errorf("location = %+v; want line 2, column 7, width 11", de)
		}
	}
}

// A builtin that ran a command and was stopped by the end of the run returns
// that end as it is, and not as an error of the call; any other failure is
// located at the name of the function.
func TestEvaluatePassesOnTheEndOfTheRun(t *testing.T) {
	for _, end := range []error{context.Canceled, context.DeadlineExceeded, fmt.Errorf("wrapped: %w", context.Canceled)} {
		cmd := &hosted{host: builtins.Host{Shell: func(string, []string) (string, error) { return "", end }}}
		if _, err := EvaluateWith(call("shell", lit("c")), nil, cmd); err != end {
			t.Errorf("EvaluateWith error = %v, want %v as it is", err, end)
		}
	}
	cmd := &hosted{host: builtins.Host{Shell: func(string, []string) (string, error) {
		return "", errors.New("shell command failed: boom")
	}}}
	_, err := EvaluateWith(call("shell", lit("c")), nil, cmd)
	requireRuntime(t, err, "shell command failed: boom")
	var de *diag.Error
	if !errors.As(err, &de) || de.Line != 2 || de.Col != 7 || de.Width != len("shell") {
		t.Errorf("location = %+v; want line 2, column 7, width 5", de)
	}
}

func BenchmarkEvaluateHostCall(b *testing.B) {
	cmd := &hosted{host: builtins.Host{FS: sysfake.FakeFS{"/w/a/b": ""}, WorkDir: "/w"}}
	e := call("path_exists", call("clean", lit("a/./b/")))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if got, err := EvaluateWith(e, nil, cmd); err != nil || got != "true" {
			b.Fatal(got, err)
		}
	}
}

func BenchmarkEvaluateCall(b *testing.B) {
	e := call("replace", call("uppercase", ast.Var{Name: "x"}), lit("BAR"), call("trim", lit(" foo ")))
	scope := Scope{"x": "barbarbar"}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Evaluate(e, scope); err != nil {
			b.Fatal(err)
		}
	}
}
