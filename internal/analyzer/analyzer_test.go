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
// conformance corpus records, without the "error: " prefix. Columns are
// checked by TestAnalyzeErrorLocations.
func wantError(t *testing.T, src string, line int, msg string) {
	t.Helper()
	got := analyze(t, src)
	got.Col, got.Width = 0, 0
	want := diag.Error{Kind: diag.KindAnalysis, File: "justfile", Line: line, Msg: msg}
	if got != want {
		t.Errorf("Analyze(%q) error =\n %+v\nwant\n %+v", src, got, want)
	}
}

// analyze analyzes src and returns the *diag.Error it must report.
func analyze(t *testing.T, src string) diag.Error {
	t.Helper()
	err := analyzer.Analyze(mustParse(t, src))
	if err == nil {
		t.Fatalf("Analyze(%q) = nil, want an error", src)
	}
	var de *diag.Error
	if !errors.As(err, &de) {
		t.Fatalf("Analyze(%q) error %T is not a *diag.Error", src, err)
	}
	return *de
}

// TestAnalyzeErrorLocations pins each error's line, column and width. Each
// row was produced by oracle/bin/just on the same source: the location from
// its "——▶ justfile:L:C" line, the width from its carets.
func TestAnalyzeErrorLocations(t *testing.T) {
	cases := []struct {
		src              string
		line, col, width int
		msg              string
	}{
		{"build:\n  a\nbuild:\n  b\n", 3, 1, 5, "recipe `build` first defined on line 1 is redefined on line 3"},
		{"x := \"a\"\nx := \"b\"\n", 2, 1, 1, "variable `x` has multiple definitions"},
		{"r:\n  echo {{nope}}\n", 2, 10, 4, "variable `nope` not defined"},
		{"x := 'a' + ('b' + nope)\n", 1, 19, 4, "variable `nope` not defined"},
		{"r a=nope:\n  echo\n", 1, 5, 4, "variable `nope` not defined"},
		{"@a: x yy\nx:\n", 1, 7, 2, "recipe `a` has unknown dependency `yy`"},
		{"a: a\n", 1, 4, 1, "recipe `a` depends on itself"},
		{"a: b\nb: a\n", 2, 4, 1, "recipe `b` has circular dependency `a -> b -> a`"},
		{"z := y\ny := z\n", 2, 1, 1, "variable `y` depends on its own value: `y -> z -> y`"},
		{"a: b\nb x:\n  echo {{zz}}\n", 3, 10, 2, "variable `zz` not defined"},
	}
	for _, c := range cases {
		got := analyze(t, c.src)
		if got.Line != c.line || got.Col != c.col || got.Width != c.width || got.Msg != c.msg {
			t.Errorf("Analyze(%q) = %d:%d width %d %q, want %d:%d width %d %q",
				c.src, got.Line, got.Col, got.Width, got.Msg, c.line, c.col, c.width, c.msg)
		}
	}
}

// TestAnalyzeRejectsWhatUpstreamRejects covers the checks of the final
// review (FINAL-4): each row was produced by oracle/bin/just.
func TestAnalyzeRejectsWhatUpstreamRejects(t *testing.T) {
	cases := []struct {
		src              string
		line, col, width int
		msg              string
	}{
		{"a x x:\n", 1, 5, 1, "recipe `a` has duplicate parameter `x`"},
		{"a x y z y:\n", 1, 9, 1, "recipe `a` has duplicate parameter `y`"},
		{"a b c x=\"1\" y:\n", 1, 13, 1, "non-default parameter `y` follows default parameter"},
		{"bar:\n\t\techo hello\n\t\t\techo goodbye", 3, 3, 13, "recipe line has extra leading whitespace"},
		{"bar:\n  a\n    b {{x}}\n", 3, 3, 4, "recipe line has extra leading whitespace"},
		{"bar:\n  a\n    {{x}}\n", 3, 3, 2, "recipe line has extra leading whitespace"},
		{"a: b\nb x:\n", 1, 4, 1, "dependency `b` got 0 arguments but takes 1 argument"},
		{"a: b\nb x y='1':\n", 1, 4, 1, "dependency `b` got 0 arguments but takes at least 1 argument"},
		{"a: b\nb x y:\n", 1, 4, 1, "dependency `b` got 0 arguments but takes 2 arguments"},
		// The order of the checks.
		{"x := y\na x x:\n", 2, 5, 1, "recipe `a` has duplicate parameter `x`"},
		{"x:\n   y\na x x:\n", 3, 5, 1, "recipe `a` has duplicate parameter `x`"},
		{"r:\nr:\n  x\n   y\n", 2, 1, 1, "recipe `r` first defined on line 1 is redefined on line 2"},
		{"r:\n  x\n   y\nr:\n", 3, 3, 2, "recipe line has extra leading whitespace"},
		{"a: b\nb x:\nc: nope\n", 1, 4, 1, "dependency `b` got 0 arguments but takes 1 argument"},
	}
	for _, c := range cases {
		got := analyze(t, c.src)
		if got.Line != c.line || got.Col != c.col || got.Width != c.width || got.Msg != c.msg {
			t.Errorf("Analyze(%q) = %d:%d width %d %q, want %d:%d width %d %q",
				c.src, got.Line, got.Col, got.Width, got.Msg, c.line, c.col, c.width, c.msg)
		}
	}
}

// A continuation line may start with extra whitespace, and a dependency
// whose parameters all have defaults takes no arguments.
func TestAnalyzeAcceptsContinuationsAndDefaultedDependencies(t *testing.T) {
	src := "a: b\n  echo one \\\n      two\nb x='1':\n  echo {{x}}\n"
	if err := analyzer.Analyze(mustParse(t, src)); err != nil {
		t.Errorf("Analyze = %v, want nil", err)
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

build2 p="x" q=p:
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
			Lines:  []ast.Line{{Parts: []ast.Expr{nil, ast.Text{Value: "t"}}, Line: 3}},
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
		fmt.Fprintf(&sb, "r%d p=base:\n  echo {{p}} {{base + 'y'}} {{uppercase(p)}}\n", i)
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

// wantAt analyzes src and checks the error's line, column, width and message.
func wantAt(t *testing.T, src string, line, col, width int, msg string) {
	t.Helper()
	got := analyze(t, src)
	want := diag.Error{Kind: diag.KindAnalysis, File: "justfile", Line: line, Col: col, Width: width, Msg: msg}
	if got != want {
		t.Errorf("Analyze(%q) error =\n %+v\nwant\n %+v", src, got, want)
	}
}

// TestAnalyzeCircularDependencyMatchesUpstream pins the category, the
// location and the cycle text of each circular-dependency case of the misc
// and subsequents corpus groups. The cycle is reported at the dependency that
// closes it, in the recipe that names it.
func TestAnalyzeCircularDependencyMatchesUpstream(t *testing.T) {
	cases := []struct {
		id, src          string
		line, col, width int
		msg              string
	}{
		{"misc::self_dependency#1", "a: a", 1, 4, 1, "recipe `a` depends on itself"},
		{"misc::circular_recipe_dependency_entered_from_outside#1", "a: b\nb: c\nc: b\n", 3, 4, 1,
			"recipe `c` has circular dependency `b -> c -> b`"},
		{"misc::long_circular_recipe_dependency#1", "a: b\nb: c\nc: d\nd: a", 4, 4, 1,
			"recipe `d` has circular dependency `a -> b -> c -> d -> a`"},
		{"misc::variable_self_dependency#1", "z := z\na:", 1, 1, 1, "variable `z` is defined in terms of itself"},
		// A cycle through a recipe that other recipes depend on is reported
		// once, from the first recipe by name.
		{"by name", "b: c\nc: b\na: b\n", 2, 4, 1, "recipe `c` has circular dependency `b -> c -> b`"},
		// A cycle among variables, entered from a variable outside it.
		{"variables", "a := b\nb := c + 'x'\nc := b\n", 2, 1, 1, "variable `b` depends on its own value: `b -> c -> b`"},
	}
	for _, c := range cases {
		t.Run(c.id, func(t *testing.T) { wantAt(t, c.src, c.line, c.col, c.width, c.msg) })
	}
}

// TestAnalyzeDuplicateParameter pins misc::duplicate_parameter#1 and the
// position rule: the error is at the second use of the name, and a duplicate
// is found before a required parameter after a default.
func TestAnalyzeDuplicateParameter(t *testing.T) {
	wantAt(t, "a foo foo:", 1, 7, 3, "recipe `a` has duplicate parameter `foo`")
	wantAt(t, "a b c b:", 1, 7, 1, "recipe `a` has duplicate parameter `b`")
	wantAt(t, "a x:\nb y y:\nc z z:", 2, 5, 1, "recipe `b` has duplicate parameter `y`")
	// A duplicate parameter is found before a duplicate assignment, a
	// duplicate recipe, an undefined function and an unknown dependency.
	wantAt(t, "x := foo()\nx := 'a'\na b b: nope\n", 3, 5, 1, "recipe `a` has duplicate parameter `b`")
}

// TestAnalyzeUndefinedVariableInDefault pins misc::unknown_variable_in_default#1
// and the scope of a default: the globals and the parameters before it.
func TestAnalyzeUndefinedVariableInDefault(t *testing.T) {
	wantAt(t, "foo x=bar:\n", 1, 7, 3, "variable `bar` not defined")
	wantAt(t, "foo x=x:\n", 1, 7, 1, "variable `x` not defined")
	wantAt(t, "foo x='a' y=z:\n", 1, 13, 1, "variable `z` not defined")
	wantAt(t, "foo x=uppercase(q):\n", 1, 17, 1, "variable `q` not defined")
	if err := analyzer.Analyze(mustParse(t, "g := 'a'\nfoo x=g y=x:\n")); err != nil {
		t.Errorf("Analyze = %v, want nil", err)
	}
}

// TestAnalyzeCorpusDiagnostics pins the analysis errors of the misc and
// subsequents corpus groups, and of function_definitions, that the analyzer
// reports with no other feature: the id names the case, and the location and
// message are the corpus's.
func TestAnalyzeCorpusDiagnostics(t *testing.T) {
	cases := []struct {
		id, src          string
		line, col, width int
		msg              string
	}{
		{"misc::duplicate_recipe#1", "b:\nb:", 2, 1, 1, "recipe `b` first defined on line 1 is redefined on line 2"},
		{"misc::duplicate_variable#1", "a := 'hello'\na := 'hello'\nfoo:", 2, 1, 1, "variable `a` has multiple definitions"},
		{"misc::missing_second_dependency#1", "x:\n\na: x y\n", 3, 6, 1, "recipe `a` has unknown dependency `y`"},
		{"misc::unknown_dependency#1", "bar:\nhello:\nfoo: bar baaaaaaaz hello", 3, 10, 9,
			"recipe `foo` has unknown dependency `baaaaaaaz`"},
		{"misc::required_after_default#1", "bar:\nhello baz arg='foo' bar:", 2, 21, 3,
			"non-default parameter `bar` follows default parameter"},
		{"misc::dependency_takes_arguments_exact#1", "a FOO:\nb: a\n", 2, 4, 1,
			"dependency `a` got 0 arguments but takes 1 argument"},
		{"misc::dependency_takes_arguments_at_least#1", "a FOO LUZ='hello':\nb: a\n", 2, 4, 1,
			"dependency `a` got 0 arguments but takes at least 1 argument"},
		{"misc::undefined_function_in_assignment#1", "foo := foo() + 'hello'\nbar:", 1, 8, 3,
			"call to undefined function `foo`"},
		{"misc::undefined_function_in_default#1", "foo x=bar():\n", 1, 7, 3, "call to undefined function `bar`"},
		{"function_definitions::undefined_in_assignment#1", "a := foo()", 1, 6, 3, "call to undefined function `foo`"},
		{"function_definitions::undefined_in_interpolation#1", "bar:\n  echo {{foo()}}\n", 2, 10, 3,
			"call to undefined function `foo`"},
		{"function_definitions::undefined_in_recipe_parameter_default#1", "bar x=foo():", 1, 7, 3,
			"call to undefined function `foo`"},
	}
	for _, c := range cases {
		t.Run(c.id, func(t *testing.T) { wantAt(t, c.src, c.line, c.col, c.width, c.msg) })
	}
}

// TestAnalyzeUndefinedFunction pins the order and the location of a call to
// a function that does not exist. Each row was produced by oracle/bin/just on
// the same source.
func TestAnalyzeUndefinedFunction(t *testing.T) {
	cases := []struct {
		src              string
		line, col, width int
		msg              string
	}{
		// The call is checked before its arguments.
		{"a := foo(nope)", 1, 6, 3, "call to undefined function `foo`"},
		{"a := uppercase(nope())", 1, 16, 4, "call to undefined function `nope`"},
		// Names are case sensitive.
		{"a := uuid() + UUID()", 1, 15, 4, "call to undefined function `UUID`"},
		// The `_dir` spelling is accepted for `_directory`, but the native
		// spelling only for the functions that have one.
		{"a := cache_dir_native()", 1, 6, 16, "call to undefined function `cache_dir_native`"},
		{"a := cache_directory_native()", 1, 6, 22, "call to undefined function `cache_directory_native`"},
		// A call in an interpolation or a default is found in its place in
		// the walk: the defaults, then the body.
		{"x y=nope():\n  echo {{foo()}}", 1, 5, 4, "call to undefined function `nope`"},
		{"x y='a':\n  echo {{foo()}}", 2, 10, 3, "call to undefined function `foo`"},
		// Assignments are checked before recipes, whatever the source order.
		{"x: a\n  echo\na := nope()", 3, 6, 4, "call to undefined function `nope`"},
		{"r:\n  echo {{foo()}}\nz := nope()", 3, 6, 4, "call to undefined function `nope`"},
		// And an undefined function in an assignment before a duplicate recipe.
		{"x := foo()\nr:\nr:", 1, 6, 3, "call to undefined function `foo`"},
		// A call found while resolving a dependency comes before the
		// recipe that depends on it.
		{"b: a\na:\n  echo {{foo()}}\nc: nope", 3, 10, 3, "call to undefined function `foo`"},
	}
	for _, c := range cases {
		t.Run(c.src, func(t *testing.T) { wantAt(t, c.src, c.line, c.col, c.width, c.msg) })
	}
}

// TestAnalyzeFunctionArgumentCount pins upstream's arity check: the message,
// the range wording, and the order against undefined variables in the
// arguments.
func TestAnalyzeFunctionArgumentCount(t *testing.T) {
	cases := []struct {
		src              string
		line, col, width int
		msg              string
	}{
		{"a := arch('x')", 1, 6, 4, "function `arch` called with 1 argument but takes 0"},
		{"a := uppercase()", 1, 6, 9, "function `uppercase` called with 0 arguments but takes 1"},
		{"a := uppercase('x', 'y')", 1, 6, 9, "function `uppercase` called with 2 arguments but takes 1"},
		{"a := env('a', 'b', 'c')", 1, 6, 3, "function `env` called with 3 arguments but takes 1 to 2"},
		{"a := style()", 1, 6, 5, "function `style` called with 0 arguments but takes 1 to 2"},
		{"a := join('x')", 1, 6, 4, "function `join` called with 1 argument but takes 2 or more"},
		{"a := shell()", 1, 6, 5, "function `shell` called with 0 arguments but takes 1 or more"},
		{"a := replace('x')", 1, 6, 7, "function `replace` called with 1 argument but takes 3"},
		{"a := cache_dir('x')", 1, 6, 9, "function `cache_dir` called with 1 argument but takes 0"},
		// The count is checked before the arguments are.
		{"a := uppercase(nope, 'y')", 1, 6, 9, "function `uppercase` called with 2 arguments but takes 1"},
		{"x y=uppercase('a', z):\n  echo {{foo()}}", 1, 5, 9, "function `uppercase` called with 2 arguments but takes 1"},
		{"x := env('a') + uppercase()", 1, 17, 9, "function `uppercase` called with 0 arguments but takes 1"},
		// The arguments are checked after the call, in order.
		{"a := uppercase(nope)", 1, 16, 4, "variable `nope` not defined"},
		{"x:\n  echo {{uppercase(y)}} {{foo()}}", 2, 20, 1, "variable `y` not defined"},
		{"x y=uppercase(z):", 1, 15, 1, "variable `z` not defined"},
	}
	for _, c := range cases {
		t.Run(c.src, func(t *testing.T) { wantAt(t, c.src, c.line, c.col, c.width, c.msg) })
	}
}

// TestAnalyzeAcceptsKnownFunctions checks that a call of every spelling the
// table knows passes, with the argument counts upstream accepts.
func TestAnalyzeAcceptsKnownFunctions(t *testing.T) {
	src := "a := uppercase('x') + arch() + env('A') + env('A', 'b') + join('a', 'b') + join('a', 'b', 'c')\n" +
		"b := cache_dir() + invocation_dir_native() + style('error') + shell('x') + shell('x', 'y')\n" +
		"r x=lowercase(a):\n  echo {{replace(x, 'a', b)}} {{trim(uppercase(a))}}\n"
	if err := analyzer.Analyze(mustParse(t, src)); err != nil {
		t.Errorf("Analyze = %v, want nil", err)
	}
}
