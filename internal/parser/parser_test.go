package parser_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/parser"
)

func lit(s string) ast.Expr { return ast.StringLit{Value: s} }
func ref(s string) ast.Expr { return ast.Var{Name: s} }

func mustParse(t *testing.T, src string) *ast.Justfile {
	t.Helper()
	jf, err := parser.Parse("justfile", []byte(src))
	if err != nil {
		t.Fatalf("Parse(%q) error: %v", src, err)
	}
	return jf
}

// wantError parses src and checks for a KindParse *diag.Error at line:col
// whose message is exactly msg.
func wantError(t *testing.T, src string, line, col int, msg string) {
	t.Helper()
	jf, err := parser.Parse("justfile", []byte(src))
	if err == nil {
		t.Fatalf("Parse(%q) = %+v, want an error", src, jf)
	}
	var de *diag.Error
	if !errors.As(err, &de) {
		t.Fatalf("Parse(%q) error %T is not a *diag.Error", src, err)
	}
	want := diag.Error{Kind: diag.KindParse, File: "justfile", Line: line, Col: col, Msg: msg}
	if *de != want {
		t.Errorf("Parse(%q) error =\n %+v\nwant\n %+v", src, *de, want)
	}
	if jf != nil {
		t.Errorf("Parse(%q) returned a tree alongside an error", src)
	}
}

func wantRecipe(t *testing.T, src string, want ast.Recipe) {
	t.Helper()
	jf := mustParse(t, src)
	if len(jf.Recipes) != 1 {
		t.Fatalf("Parse(%q) found %d recipes, want 1: %+v", src, len(jf.Recipes), jf)
	}
	if got := jf.Recipes[0]; !reflect.DeepEqual(got, want) {
		t.Errorf("Parse(%q) recipe =\n %+v\nwant\n %+v", src, got, want)
	}
}

func TestParseRecipeWithParamsAndDeps(t *testing.T) {
	jf := mustParse(t, "build target=\"debug\": test\n  cargo build {{target}}\n")
	want := &ast.Justfile{
		File: "justfile",
		Recipes: []ast.Recipe{{
			Name:   "build",
			Params: []ast.Param{{Name: "target", Default: lit("debug")}},
			Deps:   []string{"test"},
			Lines: []ast.Line{{
				Prefix: ast.PrefixNone,
				Parts:  []ast.Expr{lit("cargo build "), ref("target")},
				Line:   2,
			}},
			Line: 1,
		}},
	}
	if !reflect.DeepEqual(jf, want) {
		t.Errorf("Parse =\n %+v\nwant\n %+v", jf, want)
	}
}

func TestParseLinePrefixes(t *testing.T) {
	cases := []struct {
		line   string
		prefix ast.Prefix
		text   string
	}{
		{"echo a", ast.PrefixNone, "echo a"},
		{"@echo a", ast.PrefixAt, "echo a"},
		{"-false", ast.PrefixDash, "false"},
		{"@-false", ast.PrefixAtDash, "false"},
		{"-@false", ast.PrefixAtDash, "false"},
		{"@@x", ast.PrefixAt, "@x"},
		{"--x", ast.PrefixDash, "-x"},
		{"@-@x", ast.PrefixAtDash, "@x"},
		{"echo @-", ast.PrefixNone, "echo @-"},
	}
	for _, c := range cases {
		jf := mustParse(t, "r:\n  "+c.line+"\n")
		got := jf.Recipes[0].Lines
		want := []ast.Line{{Prefix: c.prefix, Parts: []ast.Expr{lit(c.text)}, Line: 2}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("line %q = %+v, want %+v", c.line, got, want)
		}
	}
}

func TestParsePrefixAloneAndBeforeInterpolation(t *testing.T) {
	jf := mustParse(t, "r:\n  @\n  -{{x}}\n  {{x}}\n")
	want := []ast.Line{
		{Prefix: ast.PrefixAt, Parts: nil, Line: 2},
		{Prefix: ast.PrefixDash, Parts: []ast.Expr{ref("x")}, Line: 3},
		{Prefix: ast.PrefixNone, Parts: []ast.Expr{ref("x")}, Line: 4},
	}
	if got := jf.Recipes[0].Lines; !reflect.DeepEqual(got, want) {
		t.Errorf("lines = %+v, want %+v", got, want)
	}
}

// A leading `@` on the recipe toggles each line's `@` (upstream `quiet ^ self.quiet`).
func TestParseQuietRecipeTogglesLinePrefix(t *testing.T) {
	jf := mustParse(t, "@r:\n  a\n  -b\n  @c\n  @-d\n")
	var got []ast.Prefix
	for _, l := range jf.Recipes[0].Lines {
		got = append(got, l.Prefix)
	}
	want := []ast.Prefix{ast.PrefixAt, ast.PrefixAtDash, ast.PrefixNone, ast.PrefixDash}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("prefixes = %v, want %v", got, want)
	}
}

func TestParseSetShell(t *testing.T) {
	for src, want := range map[string][]string{
		`set shell := ["bash", "-cu"]`:    {"bash", "-cu"},
		"set shell := ['sh']\n":           {"sh"},
		`set shell := ["a", "b",]`:        {"a", "b"},
		`set shell := ["a"] # comment`:    {"a"},
		"set shell := [\"bash\"]\r\n\r\n": {"bash"},
	} {
		jf := mustParse(t, src)
		if !reflect.DeepEqual(jf.Settings.Shell, want) {
			t.Errorf("Parse(%q) Shell = %q, want %q", src, jf.Settings.Shell, want)
		}
		if jf.Settings.Quiet {
			t.Errorf("Parse(%q) set Quiet", src)
		}
	}
}

func TestParseSetQuiet(t *testing.T) {
	for _, src := range []string{"set quiet", "set quiet\n", "set quiet # why\n"} {
		jf := mustParse(t, src)
		if !jf.Settings.Quiet {
			t.Errorf("Parse(%q) did not set Quiet", src)
		}
		if jf.Settings.Shell != nil {
			t.Errorf("Parse(%q) set Shell = %q", src, jf.Settings.Shell)
		}
	}
}

func TestParseSettingErrors(t *testing.T) {
	wantError(t, "set nope\n", 1, 5, "Unknown setting `nope`")
	wantError(t, "set shell\n", 1, 10, "Expected ':=', but found end of line")
	wantError(t, "set shell :=\n", 1, 13, "Expected '[', but found end of line")
	wantError(t, "set shell := []\n", 1, 15, "Expected string, but found ']'")
	wantError(t, "set shell := [,]\n", 1, 15, "Expected string, but found ','")
	wantError(t, "set shell := \"a\"\n", 1, 14, "Expected '[', but found string")
	wantError(t, "set shell := [\"a\" \"b\"]\n", 1, 19, "Expected string or ']', but found string")
	wantError(t, "set shell := [\"a\"\n", 1, 18, "Expected string or ']', but found end of line")
	wantError(t, "set quiet := true\n", 1, 11, "Expected end of line, but found ':='")
}

func TestParseSetCanStillNameARecipeOrVariable(t *testing.T) {
	jf := mustParse(t, "set := \"x\"\nset quiet:\n  y\nset a b:\n  z\n")
	if !reflect.DeepEqual(jf.Assignments, []ast.Assignment{{Name: "set", Value: lit("x"), Line: 1}}) {
		t.Errorf("Assignments = %+v", jf.Assignments)
	}
	if jf.Settings.Quiet {
		t.Error("`set quiet:` was parsed as a setting")
	}
	if len(jf.Recipes) != 2 || jf.Recipes[0].Name != "set" || len(jf.Recipes[0].Params) != 1 ||
		len(jf.Recipes[1].Params) != 2 {
		t.Errorf("Recipes = %+v", jf.Recipes)
	}
}

func TestParseDocComment(t *testing.T) {
	wantRecipe(t, "# build it\nbuild:\n  x", ast.Recipe{
		Name: "build", Doc: "build it", Line: 2,
		Lines: []ast.Line{{Parts: []ast.Expr{lit("x")}, Line: 3}},
	})
}

func TestParseDocCommentRules(t *testing.T) {
	cases := []struct{ src, doc string }{
		{"# a\n# b\nbuild:\n", "b"},
		{"# a\n\nbuild:\n", ""},
		{"# a\nx := \"1\"\nbuild:\n", ""},
		{"#tight\n@build:\n", "tight"},
		{"build: # trailing\n", ""},
		{"build:\n", ""},
	}
	for _, c := range cases {
		jf := mustParse(t, c.src)
		if got := jf.Recipes[0].Doc; got != c.doc {
			t.Errorf("Parse(%q) Doc = %q, want %q", c.src, got, c.doc)
		}
	}
}

func TestParseAssignments(t *testing.T) {
	jf := mustParse(t, "a := \"x\"\nb := a + 'y' + (a + \"z\")\nc := (a)\n")
	want := []ast.Assignment{
		{Name: "a", Value: lit("x"), Line: 1},
		{Name: "b", Line: 2, Value: ast.Concat{
			Left:  ast.Concat{Left: ref("a"), Right: lit("y")},
			Right: ast.Concat{Left: ref("a"), Right: lit("z")},
		}},
		{Name: "c", Value: ref("a"), Line: 3},
	}
	if !reflect.DeepEqual(jf.Assignments, want) {
		t.Errorf("Assignments =\n %+v\nwant\n %+v", jf.Assignments, want)
	}
}

func TestParseSourceOrder(t *testing.T) {
	jf := mustParse(t, "b:\na := \"1\"\nz:\nc := \"2\"\nm:\n")
	var recipes, assigns []string
	for _, r := range jf.Recipes {
		recipes = append(recipes, r.Name)
	}
	for _, a := range jf.Assignments {
		assigns = append(assigns, a.Name)
	}
	if !reflect.DeepEqual(recipes, []string{"b", "z", "m"}) || !reflect.DeepEqual(assigns, []string{"a", "c"}) {
		t.Errorf("recipes %v, assignments %v", recipes, assigns)
	}
}

func TestParseAssignmentErrors(t *testing.T) {
	wantError(t, "x := \n", 1, 6, "Expected an expression, but found end of line")
	wantError(t, "x := a +\n", 1, 9, "Expected an expression, but found end of line")
	wantError(t, "x := (a\n", 1, 8, "Expected '+' or ')', but found end of line")
	wantError(t, "x := (\n", 1, 7, "Expected an expression, but found end of line")
	wantError(t, "x := a b\n", 1, 8, "Expected end of line, but found identifier")
}

func TestParseParams(t *testing.T) {
	wantRecipe(t, "r a b=\"1\" c=d e=(f + 'g'):\n", ast.Recipe{
		Name: "r", Line: 1,
		Params: []ast.Param{
			{Name: "a"},
			{Name: "b", Default: lit("1")},
			{Name: "c", Default: ref("d")},
			{Name: "e", Default: ast.Concat{Left: ref("f"), Right: lit("g")}},
		},
	})
}

func TestParseRecipeHeaderErrors(t *testing.T) {
	wantError(t, "@\n", 1, 2, "Expected a recipe name, but found end of line")
	wantError(t, "r x\n", 1, 4, "Expected ':', ':=', or a parameter, but found end of line")
	wantError(t, "r (x):\n", 1, 3, "Expected ':', ':=', or a parameter, but found '('")
	wantError(t, "r x=:\n", 1, 5, "Expected an expression, but found ':'")
	wantError(t, "r: a (b)\n", 1, 6, "Expected end of line, but found '('")
	wantError(t, "r: a := b\n", 1, 6, "Expected end of line, but found ':='")
}

func TestParseTopLevelErrors(t *testing.T) {
	wantError(t, ": x\n", 1, 1, "Expected a recipe, an assignment, or a setting, but found ':'")
	wantError(t, "  x\n", 1, 1, "Expected a recipe, an assignment, or a setting, but found indentation")
	wantError(t, "r:\n  x\n\"s\"\n", 3, 1, "Expected a recipe, an assignment, or a setting, but found string")
}

func TestParseLexErrorsPassThrough(t *testing.T) {
	wantError(t, "x := \"abc\n", 1, 6, "unterminated string")
	wantError(t, "x := $\n", 1, 6, "unknown start of token '$'")
}

func TestParseRecipeBodies(t *testing.T) {
	cases := []struct {
		name, src string
		lines     []ast.Line
	}{
		{"blank lines inside are dropped", "r:\n  a\n\n  b\n", []ast.Line{
			{Parts: []ast.Expr{lit("a")}, Line: 2}, {Parts: []ast.Expr{lit("b")}, Line: 4}}},
		{"whitespace-only lines are blank", "r:\n  a\n   \n\t\n  b\n", []ast.Line{
			{Parts: []ast.Expr{lit("a")}, Line: 2}, {Parts: []ast.Expr{lit("b")}, Line: 5}}},
		{"blank line before the first line", "r:\n\n  a\n", []ast.Line{
			{Parts: []ast.Expr{lit("a")}, Line: 3}}},
		{"a blank line then a top-level line ends the body", "r:\n  a\n\nx := \"1\"\n", []ast.Line{
			{Parts: []ast.Expr{lit("a")}, Line: 2}}},
		{"a top-level comment ends the body", "r:\n  a\n# c\nx := \"1\"\n", []ast.Line{
			{Parts: []ast.Expr{lit("a")}, Line: 2}}},
		{"extra indentation stays in the text", "r:\n  a\n    b\n", []ast.Line{
			{Parts: []ast.Expr{lit("a")}, Line: 2}, {Parts: []ast.Expr{lit("  b")}, Line: 3}}},
		{"extra indentation before an interpolation", "r:\n  a\n    {{x}}\n", []ast.Line{
			{Parts: []ast.Expr{lit("a")}, Line: 2}, {Parts: []ast.Expr{lit("  "), ref("x")}, Line: 3}}},
		{"extra indentation hides the prefix", "r:\n  a\n   @b\n", []ast.Line{
			{Parts: []ast.Expr{lit("a")}, Line: 2}, {Parts: []ast.Expr{lit(" @b")}, Line: 3}}},
		{"tabs", "r:\n\ta\n\t\tb\n", []ast.Line{
			{Parts: []ast.Expr{lit("a")}, Line: 2}, {Parts: []ast.Expr{lit("\tb")}, Line: 3}}},
		{"comments in the body are text", "r:\n  # c\n  echo # d\n", []ast.Line{
			{Parts: []ast.Expr{lit("# c")}, Line: 2}, {Parts: []ast.Expr{lit("echo # d")}, Line: 3}}},
		{"CRLF", "r:\r\n  a\r\n  b\r\n", []ast.Line{
			{Parts: []ast.Expr{lit("a")}, Line: 2}, {Parts: []ast.Expr{lit("b")}, Line: 3}}},
		{"no final newline", "r:\n  a", []ast.Line{{Parts: []ast.Expr{lit("a")}, Line: 2}}},
		{"interpolations", "r:\n  {{a}}-{{ b + 'c' }}{{(d)}}\n", []ast.Line{
			{Parts: []ast.Expr{ref("a"), lit("-"), ast.Concat{Left: ref("b"), Right: lit("c")}, ref("d")}, Line: 2}}},
		{"an interpolation across lines", "r:\n  x {{ a +\n  b }} y\n", []ast.Line{
			{Parts: []ast.Expr{lit("x "), ast.Concat{Left: ref("a"), Right: ref("b")}, lit(" y")}, Line: 2}}},
		{"multibyte text", "r:\n  echo héllo ✓ 日本\n", []ast.Line{
			{Parts: []ast.Expr{lit("echo héllo ✓ 日本")}, Line: 2}}},
	}
	for _, c := range cases {
		jf := mustParse(t, c.src)
		if got := jf.Recipes[0].Lines; !reflect.DeepEqual(got, c.lines) {
			t.Errorf("%s: Lines =\n %+v\nwant\n %+v", c.name, got, c.lines)
		}
	}
}

func TestParseBodyEndsAtNextRecipe(t *testing.T) {
	jf := mustParse(t, "a:\n  x\nb:\n  y\n")
	if len(jf.Recipes) != 2 || len(jf.Recipes[0].Lines) != 1 || len(jf.Recipes[1].Lines) != 1 {
		t.Errorf("Recipes = %+v", jf.Recipes)
	}
	if jf.Recipes[1].Line != 3 {
		t.Errorf("second recipe Line = %d, want 3", jf.Recipes[1].Line)
	}
}

func TestParseInterpolationErrors(t *testing.T) {
	wantError(t, "r:\n  {{\n", 3, 1, "Expected an expression, but found end of line")
	wantError(t, "r:\n  x {{ a\n", 3, 1, "Expected '+' or '}}', but found end of line")
	wantError(t, "r:\n  {{ a b }}\n", 2, 8, "Expected '+' or '}}', but found identifier")
}

func TestParseMixedIndentationIsError(t *testing.T) {
	// Messages are upstream's, taken from the corpus cases named in each row.
	cases := []struct {
		name, src string
		line      int
		msg       string
	}{
		{"brief: a tab line then a space line", "bar:\n\tone\n  two\n", 3,
			"recipe line has inconsistent leading whitespace, started with `␉` but found line with `␠␠`"},
		{"misc::inconsistent_leading_whitespace#1", "bar:\n\t\techo hello\n\techo goodbye", 3,
			"recipe line has inconsistent leading whitespace, started with `␉␉` but found line with `␉`"},
		{"misc::mixed_whitespace#1", "bar:\n\t echo hello", 2,
			"found a mix of tabs and spaces in leading whitespace: `␉␠`\n" +
				"leading whitespace may consist of tabs or spaces, but not both"},
		{"misc::mixed_whitespace_in_recipe_body#1", "bar:\n  echo a\n \techo b", 3,
			"found a mix of tabs and spaces in leading whitespace: `␠␉`\n" +
				"leading whitespace may consist of tabs or spaces, but not both"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wantError(t, c.src, c.line, 1, c.msg)
		})
	}
}

func TestParseIndentationRules(t *testing.T) {
	const mixed = "leading whitespace may consist of tabs or spaces, but not both"
	jf := mustParse(t, "r:\n\t\ta\n\t\t\tb\n\t\t\t\tc\n")
	if n := len(jf.Recipes[0].Lines); n != 3 {
		t.Errorf("deeper lines with the same characters: %d lines, want 3", n)
	}
	wantError(t, "r:\n \ta\n", 2, 1, "found a mix of tabs and spaces in leading whitespace: `␠␉`\n"+mixed)
	wantError(t, "r:\n    a\n  b\n", 3, 1,
		"recipe line has inconsistent leading whitespace, started with `␠␠␠␠` but found line with `␠␠`")
	wantError(t, "r:\n  a\n\t\tb\n", 3, 1,
		"recipe line has inconsistent leading whitespace, started with `␠␠` but found line with `␉␉`")
	wantError(t, "r:\n  a\n \t b\n", 3, 1,
		"found a mix of tabs and spaces in leading whitespace: `␠␉`\n"+mixed)
}

func TestParseFileName(t *testing.T) {
	jf, err := parser.Parse("path/to/justfile", nil)
	if err != nil {
		t.Fatal(err)
	}
	if jf.File != "path/to/justfile" || len(jf.Recipes)+len(jf.Assignments) != 0 {
		t.Errorf("Parse(nil) = %+v", jf)
	}
	_, err = parser.Parse("other", []byte("x := $"))
	var de *diag.Error
	if !errors.As(err, &de) || de.File != "other" || !strings.HasPrefix(err.Error(), "other:1:6:") {
		t.Errorf("error = %v", err)
	}
}

func TestParseIsDeterministicAndDoesNotModifyInput(t *testing.T) {
	src := []byte("# d\nx := \"1\"\n@r a=x: dep\n  @echo {{a}}\n")
	orig := string(src)
	a, errA := parser.Parse("justfile", src)
	b, errB := parser.Parse("justfile", src)
	if errA != nil || errB != nil || !reflect.DeepEqual(a, b) {
		t.Errorf("Parse is not deterministic: %+v %v / %+v %v", a, errA, b, errB)
	}
	if string(src) != orig {
		t.Error("Parse modified its input")
	}
}
