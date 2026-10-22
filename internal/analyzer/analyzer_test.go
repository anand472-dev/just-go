package analyzer_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"gitea.com/wimwian/just-go/internal/analyzer"
	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/parser"
)

func mustParse(t testing.TB, src string) *ast.Justfile {
	t.Helper()
	jf, err := parser.Parse("justfile", []byte(src))
	if err != nil {
		t.Fatalf("Parse(%q) error: %v", src, err)
	}
	return jf
}

// wantError analyzes src and checks for a KindAnalysis *diag.Error on the
// given line whose message is exactly msg. Messages are the ones the
// conformance corpus records, without the "error: " prefix.
func wantError(t *testing.T, src string, line int, msg string) {
	t.Helper()
	err := analyzer.Analyze(mustParse(t, src))
	if err == nil {
		t.Fatalf("Analyze(%q) = nil, want an error", src)
	}
	var de *diag.Error
	if !errors.As(err, &de) {
		t.Fatalf("Analyze(%q) error %T is not a *diag.Error", src, err)
	}
	want := diag.Error{Kind: diag.KindAnalysis, File: "justfile", Line: line, Msg: msg}
	if *de != want {
		t.Errorf("Analyze(%q) error =\n %+v\nwant\n %+v", src, *de, want)
	}
}

func TestAnalyzeDuplicateRecipe(t *testing.T) {
	wantError(t, "build:\n  a\nbuild:\n  b\n", 3,
		"recipe `build` first defined on line 1 is redefined on line 3")
	wantError(t, "a:\nb:\nc:\nb:\n", 4,
		"recipe `b` first defined on line 2 is redefined on line 4")
}

func TestAnalyzeDuplicateAssignment(t *testing.T) {
	wantError(t, "x := \"a\"\nx := \"b\"\n", 2, "variable `x` has multiple definitions")
	wantError(t, "a := 'hi'\nb := 'x'\nb := 'y'\na := 'z'\n", 3, "variable `b` has multiple definitions")
}

func TestAnalyzeUndefinedVariable(t *testing.T) {
	wantError(t, "r:\n  echo {{nope}}\n", 2, "variable `nope` not defined")
	wantError(t, "x := nope\n", 1, "variable `nope` not defined")
	wantError(t, "x := nope + 'a'\n", 1, "variable `nope` not defined")
	wantError(t, "x := 'a' + ('b' + nope)\n", 1, "variable `nope` not defined")
	wantError(t, "r:\n  echo {{ nope + 'a' }}\n", 2, "variable `nope` not defined")
	wantError(t, "r:\n  echo {{ 'a' + ('b' + nope) }}\n", 2, "variable `nope` not defined")
	wantError(t, "r a=nope:\n  echo\n", 1, "variable `nope` not defined")
	wantError(t, "r:\n  echo hi\n  echo {{ 'a' + nope }}\n", 3, "variable `nope` not defined")
}

func TestAnalyzeUnknownDependency(t *testing.T) {
	wantError(t, "a: x y\nx:\n", 1, "recipe `a` has unknown dependency `y`")
	wantError(t, "a:\nb: missing\n", 2, "recipe `b` has unknown dependency `missing`")
}

func TestAnalyzeAcceptsValidJustfile(t *testing.T) {
	src := `x := "a"
y := x + "b"
later := tail
tail := "t"

build target=y: test
  cargo build {{target}} {{x}} {{later}}

test: build2
  echo ok

build2 p q=p:
  echo {{p}} {{q}} {{ 'lit' + y }}
`
	if err := analyzer.Analyze(mustParse(t, src)); err != nil {
		t.Errorf("Analyze(valid) = %v, want nil", err)
	}
	if err := analyzer.Analyze(mustParse(t, "")); err != nil {
		t.Errorf("Analyze(empty) = %v, want nil", err)
	}
}

func TestAnalyzeParameterScope(t *testing.T) {
	// A parameter is visible in later defaults and in the body, but not in
	// its own default or in an earlier one, and not in other recipes.
	wantError(t, "r a=b b='':\n  echo\n", 1, "variable `b` not defined")
	wantError(t, "r a=a:\n  echo\n", 1, "variable `a` not defined")
	wantError(t, "r a:\n  echo\ns:\n  echo {{a}}\n", 4, "variable `a` not defined")
}

// TestAnalyzeCheckOrder pins upstream's order. Each want was produced by
// oracle/bin/just on the same source.
func TestAnalyzeCheckOrder(t *testing.T) {
	// A duplicate assignment comes before a duplicate recipe and an unknown
	// dependency, wherever they appear.
	wantError(t, "a: nope\nx := z\nx := 'a'\nr:\nr:\n", 3, "variable `x` has multiple definitions")
	wantError(t, "a: nope\nx := 'b'\nx := 'a'\n", 3, "variable `x` has multiple definitions")
	// An undefined variable in an assignment comes before a duplicate recipe
	// and an unknown dependency.
	wantError(t, "a: nope\nx := z\n", 2, "variable `z` not defined")
	wantError(t, "r:\nr:\nx := z\n", 3, "variable `z` not defined")
	// A duplicate recipe comes before an unknown dependency.
	wantError(t, "a: nope\nr:\nr:\n", 3, "recipe `r` first defined on line 2 is redefined on line 3")
	// A recipe's unknown dependency comes before the undefined variable in
	// its own body, even though the body is later in the file.
	wantError(t, "a: nope\nb:\n  echo {{zz}}\n", 1, "recipe `a` has unknown dependency `nope`")
	// Within the assignments, resolution is in name order.
	wantError(t, "z := q\na := w\n", 2, "variable `w` not defined")
	wantError(t, "x := p\ny := q\n", 1, "variable `p` not defined")
	// Within the recipes, resolution is in name order, not source order.
	wantError(t, "b: n2\na: n1\n", 2, "recipe `a` has unknown dependency `n1`")
	wantError(t, "a: n1\nb: n2\n", 1, "recipe `a` has unknown dependency `n1`")
}

// TestAnalyzeRecipeResolutionOrder pins the depth-first walk: a recipe's
// dependencies are fully checked before the recipe itself.
func TestAnalyzeRecipeResolutionOrder(t *testing.T) {
	// b is resolved first, being first by name; a's dependency comes before
	// a's own parameter default.
	wantError(t, "b: a\na p=zz:\n  echo {{yy}}\n", 2, "variable `zz` not defined")
	// Parameter defaults come before the body.
	wantError(t, "a p=zz:\n  echo {{yy}}\n", 1, "variable `zz` not defined")
	// a depends on x, x on y, and y has an undefined variable: that is found
	// while resolving a, before b's unknown dependency.
	wantError(t, "a: x\nx: y\ny:\n  echo {{q}}\nb: nope\n", 4, "variable `q` not defined")
	// A recipe's dependencies come before its own parameters.
	wantError(t, "b p=z: nope\n  echo {{y}}\n", 1, "recipe `b` has unknown dependency `nope`")
	// A dependency resolved earlier is not checked again.
	if err := analyzer.Analyze(mustParse(t, "a: c\nb: c\nc:\n")); err != nil {
		t.Errorf("Analyze(shared dependency) = %v, want nil", err)
	}
}

func TestAnalyzeCircularVariables(t *testing.T) {
	wantError(t, "x := x\n", 1, "variable `x` is defined in terms of itself")
	wantError(t, "x := y\ny := x\n", 1, "variable `x` depends on its own value: `x -> y -> x`")
	wantError(t, "a := b\nb := c\nc := a\n", 1, "variable `a` depends on its own value: `a -> b -> c -> a`")
	// The cycle is reported at the variable that closes it, which is the one
	// resolved first, by name.
	wantError(t, "z := y\ny := z\n", 2, "variable `y` depends on its own value: `y -> z -> y`")
	// A cycle entered through a variable outside it.
	wantError(t, "a := b\nb := c + 'x'\nc := b\n", 2, "variable `b` depends on its own value: `b -> c -> b`")
	// A variable used twice is not a cycle.
	if err := analyzer.Analyze(mustParse(t, "a := 'x'\nb := a + a\nc := b + a\n")); err != nil {
		t.Errorf("Analyze(reused variable) = %v, want nil", err)
	}
}

func TestAnalyzeCircularRecipes(t *testing.T) {
	wantError(t, "a: a\n", 1, "recipe `a` depends on itself")
	wantError(t, "a: b\nb: a\n", 2, "recipe `b` has circular dependency `a -> b -> a`")
	wantError(t, "a: b\nb: c\nc: a\n", 3, "recipe `c` has circular dependency `a -> b -> c -> a`")
	// A cycle entered through a recipe outside it.
	wantError(t, "a: b\nb: c\nc: b\n", 3, "recipe `c` has circular dependency `b -> c -> b`")
}

func TestAnalyzeToleratesAbsentExpressions(t *testing.T) {
	jf := &ast.Justfile{
		File:        "f",
		Assignments: []ast.Assignment{{Name: "x", Value: nil, Line: 1}, {Name: "y", Value: ast.Concat{}, Line: 2}},
		Recipes: []ast.Recipe{{
			Name:   "r",
			Params: []ast.Param{{Name: "p"}},
			Lines:  []ast.Line{{Parts: []ast.Expr{nil, ast.StringLit{Value: "t"}}, Line: 3}},
			Line:   3,
		}},
	}
	if err := analyzer.Analyze(jf); err != nil {
		t.Errorf("Analyze = %v, want nil", err)
	}
}

func TestAnalyzeUsesTheFileName(t *testing.T) {
	jf, err := parser.Parse("sub/justfile", []byte("r: nope\n"))
	if err != nil {
		t.Fatal(err)
	}
	err = analyzer.Analyze(jf)
	var de *diag.Error
	if !errors.As(err, &de) || de.File != "sub/justfile" {
		t.Errorf("Analyze error = %#v, want File sub/justfile", err)
	}
	if got := err.Error(); !strings.HasPrefix(got, "sub/justfile:1:") {
		t.Errorf("Error() = %q, want a sub/justfile:1: prefix", got)
	}
}

func TestAnalyzeDoesNotMutateItsInput(t *testing.T) {
	src := "x := 'a'\nr: s\n  echo {{x}}\ns:\n"
	jf := mustParse(t, src)
	before := fmt.Sprintf("%#v", *jf)
	if err := analyzer.Analyze(jf); err != nil {
		t.Fatal(err)
	}
	if after := fmt.Sprintf("%#v", *jf); after != before {
		t.Errorf("Analyze mutated its input:\n%s\n%s", before, after)
	}
}

func TestAnalyzeIsDeterministic(t *testing.T) {
	jf := mustParse(t, "a: x\nb: y\nr:\nr:\n")
	first := analyzer.Analyze(jf)
	for i := 0; i < 20; i++ {
		if got := analyzer.Analyze(jf); got.Error() != first.Error() {
			t.Fatalf("run %d: %v, want %v", i, got, first)
		}
	}
}

func BenchmarkAnalyze(b *testing.B) {
	var sb strings.Builder
	sb.WriteString("base := 'x'\n")
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&sb, "r%d p=base:\n  echo {{p}} {{base + 'y'}}\n", i)
	}
	sb.WriteString("all: r0 r199\n")
	jf := mustParse(b, sb.String())
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := analyzer.Analyze(jf); err != nil {
			b.Fatal(err)
		}
	}
}
