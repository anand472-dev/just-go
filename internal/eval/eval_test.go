package eval

import (
	"errors"
	"fmt"
	"testing"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
)

func TestEvaluateLiteral(t *testing.T) {
	got, err := Evaluate(ast.StringLit{Value: "hi"}, nil)
	if err != nil || got != "hi" {
		t.Fatalf("got %q, %v; want %q", got, err, "hi")
	}
}

func TestEvaluateText(t *testing.T) {
	got, err := Evaluate(ast.Text{Value: "@echo"}, nil)
	if err != nil || got != "@echo" {
		t.Fatalf("got %q, %v; want %q", got, err, "@echo")
	}
}

func TestEvaluateUndefinedVarIsLocatedAtItsName(t *testing.T) {
	_, err := Evaluate(ast.Var{Name: "nope", Line: 2, Col: 9}, Scope{})
	var de *diag.Error
	if !errors.As(err, &de) || de.Line != 2 || de.Col != 9 || de.Width != 4 || de.File != "" {
		t.Fatalf("error = %+v, want located at 2:9 with width 4 and no file", de)
	}
}

func TestEvaluateConcat(t *testing.T) {
	e := ast.Concat{Left: ast.StringLit{Value: "a"}, Right: ast.StringLit{Value: "b"}}
	got, err := Evaluate(e, nil)
	if err != nil || got != "ab" {
		t.Fatalf("got %q, %v; want %q", got, err, "ab")
	}
}

func TestEvaluateVarFromScope(t *testing.T) {
	got, err := Evaluate(ast.Var{Name: "x"}, Scope{"x": "1"})
	if err != nil || got != "1" {
		t.Fatalf("got %q, %v; want %q", got, err, "1")
	}
}

func requireRuntime(t *testing.T, err error, msg string) {
	t.Helper()
	var de *diag.Error
	if !errors.As(err, &de) {
		t.Fatalf("error %v is not a *diag.Error", err)
	}
	if de.Kind != diag.KindRuntime {
		t.Errorf("kind = %v; want KindRuntime", de.Kind)
	}
	if de.Msg != msg {
		t.Errorf("msg = %q; want %q", de.Msg, msg)
	}
}

func TestEvaluateUndefinedVar(t *testing.T) {
	_, err := Evaluate(ast.Var{Name: "y"}, Scope{"x": "1"})
	requireRuntime(t, err, "variable `y` not defined")
}

func TestEvaluateUndefinedVarOnEitherSideOfConcat(t *testing.T) {
	scope := Scope{"x": "1"}
	ok, bad := ast.Var{Name: "x"}, ast.Var{Name: "y"}
	for name, e := range map[string]ast.Expr{
		"left":  ast.Concat{Left: bad, Right: ok},
		"right": ast.Concat{Left: ok, Right: bad},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := Evaluate(e, scope)
			requireRuntime(t, err, "variable `y` not defined")
			if got != "" {
				t.Errorf("value = %q on error; want empty", got)
			}
		})
	}
}

// foreign is an expression type the evaluator does not know. The ast.Expr
// interface has an unexported method, so a type outside package ast can only
// satisfy it by embedding a real node.
type foreign struct{ ast.StringLit }

func TestEvaluateUnknownExpression(t *testing.T) {
	_, err := Evaluate(nil, nil)
	requireRuntime(t, err, "cannot evaluate expression of type <nil>")
	_, err = Evaluate(foreign{}, nil)
	requireRuntime(t, err, "cannot evaluate expression of type eval.foreign")
}

func TestEvaluatePreservesMultibyteText(t *testing.T) {
	const s = "héllo ✓ 日本"
	got, err := Evaluate(ast.StringLit{Value: s}, nil)
	if err != nil || got != s {
		t.Fatalf("got %q, %v; want %q", got, err, s)
	}
	if len(got) != len(s) {
		t.Errorf("byte length = %d; want %d", len(got), len(s))
	}
	// Multibyte text also survives a variable and a concatenation.
	e := ast.Concat{Left: ast.Var{Name: "v"}, Right: ast.StringLit{Value: "✓"}}
	got, err = Evaluate(e, Scope{"v": "日本"})
	if err != nil || got != "日本✓" {
		t.Fatalf("got %q, %v; want %q", got, err, "日本✓")
	}
}

func TestEvaluateDoesNotMutateScope(t *testing.T) {
	scope := Scope{"x": "1"}
	if _, err := Evaluate(ast.Concat{Left: ast.Var{Name: "x"}, Right: ast.Var{Name: "x"}}, scope); err != nil {
		t.Fatal(err)
	}
	if len(scope) != 1 || scope["x"] != "1" {
		t.Errorf("scope changed: %v", scope)
	}
}

func BenchmarkEvaluate(b *testing.B) {
	scope := Scope{}
	var e ast.Expr = ast.Var{Name: "v0"}
	scope["v0"] = "value"
	for i := 1; i < 10; i++ {
		name := fmt.Sprintf("v%d", i)
		scope[name] = "value"
		e = ast.Concat{Left: e, Right: ast.Var{Name: name}}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Evaluate(e, scope); err != nil {
			b.Fatal(err)
		}
	}
}

func TestEvaluateAll(t *testing.T) {
	args := []ast.Expr{ast.StringLit{Value: "a"}, ast.Var{Name: "x"}, ast.Concat{Left: ast.Var{Name: "x"}, Right: ast.StringLit{Value: "!"}}}
	got, err := EvaluateAll(args, Scope{"x": "1"})
	if err != nil || len(got) != 3 || got[0] != "a" || got[1] != "1" || got[2] != "1!" {
		t.Fatalf("got %q, %v; want [a 1 1!]", got, err)
	}
}

func TestEvaluateAllOfNothingIsNil(t *testing.T) {
	if got, err := EvaluateAll(nil, nil); err != nil || got != nil {
		t.Fatalf("got %#v, %v; want nil", got, err)
	}
}

func TestEvaluateAllStopsAtTheFirstError(t *testing.T) {
	args := []ast.Expr{ast.Var{Name: "a", Line: 1, Col: 1}, ast.Var{Name: "b", Line: 1, Col: 3}}
	got, err := EvaluateAll(args, Scope{})
	requireRuntime(t, err, "variable `a` not defined")
	if got != nil {
		t.Errorf("got %q with an error; want nil", got)
	}
}

func BenchmarkEvaluateAll(b *testing.B) {
	args := []ast.Expr{ast.StringLit{Value: "a"}, ast.Var{Name: "x"}, ast.Concat{Left: ast.Var{Name: "x"}, Right: ast.Var{Name: "x"}}}
	scope := Scope{"x": "value"}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := EvaluateAll(args, scope); err != nil {
			b.Fatal(err)
		}
	}
}
