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

func TestAnalyzeCheckOrder(t *testing.T) {
	// Checks run in the order duplicate recipe, duplicate assignment,
	// undefined variable, unknown dependency; the first failing check wins
	// even when a later check's error is earlier in the file.
	wantError(t, "a: nope\nx := z\nx := 'a'\nr:\nr:\n", 5,
		"recipe `r` first defined on line 4 is redefined on line 5")
	wantError(t, "a: nope\nx := 'b'\nx := 'a'\n", 3, "variable `x` has multiple definitions")
	wantError(t, "a: nope\nx := z\n", 2, "variable `z` not defined")
	// Within one check the earliest error in source order wins.
	wantError(t, "x := p\ny := q\n", 1, "variable `p` not defined")
	wantError(t, "a: n1\nb: n2\n", 1, "recipe `a` has unknown dependency `n1`")
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
