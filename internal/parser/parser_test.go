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
func txt(s string) ast.Expr { return ast.Text{Value: s} }
func ref(s string) ast.Expr { return ast.Var{Name: s} }

// mustParse parses src and returns the tree without its columns and the
// positions of names, which TestParsePositions checks on their own; the
// line numbers of recipes, assignments and lines stay.
func mustParse(t *testing.T, src string) *ast.Justfile {
	t.Helper()
	jf, err := parser.Parse("justfile", []byte(src))
	if err != nil {
		t.Fatalf("Parse(%q) error: %v", src, err)
	}
	return strip(jf)
}

func strip(jf *ast.Justfile) *ast.Justfile {
	for i := range jf.Assignments {
		a := &jf.Assignments[i]
		a.Col, a.Value = 0, stripExpr(a.Value)
	}
	for i := range jf.Recipes {
		r := &jf.Recipes[i]
		r.Col = 0
		for k := range r.Deps {
			r.Deps[k].Line, r.Deps[k].Col = 0, 0
		}
		for k := range r.Params {
			p := &r.Params[k]
			p.Line, p.Col, p.Default = 0, 0, stripExpr(p.Default)
		}
		for k := range r.Lines {
			l := &r.Lines[k]
			l.ExtraIndent = ast.Span{}
			for n := range l.Parts {
				l.Parts[n] = stripExpr(l.Parts[n])
			}
		}
	}
	return jf
}

func stripExpr(e ast.Expr) ast.Expr {
	switch x := e.(type) {
	case ast.Var:
		return ast.Var{Name: x.Name}
	case ast.Concat:
		return ast.Concat{Left: stripExpr(x.Left), Right: stripExpr(x.Right)}
	}
	return e
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
	got := *de
	got.Width = 0
	if got != want {
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
			Params: []ast.Param{{Name: "target", Default: lit("debug"), DefaultText: `"debug"`}},
			Deps:   []ast.Dep{{Name: "test"}},
			Lines: []ast.Line{{
				Prefix: ast.PrefixNone,
				Parts:  []ast.Expr{txt("cargo build "), ref("target")},
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
		want := []ast.Line{{Prefix: c.prefix, Parts: []ast.Expr{txt(c.text)}, Line: 2}}
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

// What upstream expects at each point of the grammar, as its error messages
// list it. Every message in these tests was checked against the oracle.
const (
	value     = "backtick, '!', '[', identifier, '(', '/', or string"
	afterWord = "'&&', '!=', '!~', '||', ']', ',', '==', '=~', '+', '++', or '/'"
	afterName = "'&&', '!=', '!~', '||', comment, end of file, end of line, '==', '=~', '(', '+', '++', or '/'"
	afterDeps = "'&&', '*', '::', comment, end of file, end of line, identifier, indent, or '('"
	item      = "'@', '[', comment, end of file, end of line, or identifier"
)

func TestParseSettingErrors(t *testing.T) {
	wantError(t, "set nope\n", 1, 5, "unknown setting `nope`")
	wantError(t, "set shell\n", 1, 10, "expected ':=', but found end of line")
	wantError(t, "set shell :=\n", 1, 13, "expected '[', but found end of line")
	wantError(t, "set shell := []\n", 1, 15, "expected "+value+", but found ']'")
	wantError(t, "set shell := [,]\n", 1, 15, "expected "+value+", but found ','")
	wantError(t, "set shell := \"a\"\n", 1, 14, "expected '[', but found string")
	wantError(t, "set shell := [\"a\" \"b\"]\n", 1, 19, "expected "+afterWord+", but found string")
	wantError(t, "set shell := [\"a\"\n", 1, 19, "expected "+afterWord+", but found end of file")
	wantError(t, "set quiet := maybe\n", 1, 14, "expected keyword `true` or `false` but found identifier `maybe`")
	wantError(t, "set quiet := \"true\"\n", 1, 14, "expected identifier, but found string")
	wantError(t, "set quiet := true foo\n", 1, 19, "expected comment, end of file, or end of line, but found identifier")
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
		Lines: []ast.Line{{Parts: []ast.Expr{txt("x")}, Line: 3}},
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
	wantError(t, "x := \n", 1, 6, "expected "+value+", but found end of line")
	wantError(t, "x := a +\n", 1, 9, "expected "+value+", but found end of line")
	wantError(t, "x := (a\n", 1, 9, "expected '&&', '!=', '!~', '||', '==', '=~', '(', ')', '+', '++', or '/', but found end of file")
	wantError(t, "x := (\n", 1, 8, "expected "+value+", but found end of file")
	wantError(t, "x := a b\n", 1, 8, "expected "+afterName+", but found identifier")
}

func TestParseParams(t *testing.T) {
	wantRecipe(t, "r a b=\"1\" c=d e=(f + 'g'):\n", ast.Recipe{
		Name: "r", Line: 1,
		Params: []ast.Param{
			{Name: "a"},
			{Name: "b", Default: lit("1"), DefaultText: `"1"`},
			{Name: "c", Default: ref("d"), DefaultText: "d"},
			{Name: "e", Default: ast.Concat{Left: ref("f"), Right: lit("g")}, DefaultText: "(f + 'g')"},
		},
	})
}

func TestParseRecipeHeaderErrors(t *testing.T) {
	wantError(t, "@\n", 1, 2, "expected identifier, but found end of line")
	wantError(t, "r x\n", 1, 4, "expected '*', ':', '$', '=', identifier, or '+', but found end of line")
	wantError(t, "r 'x':\n", 1, 3, "expected '*', ':', '$', identifier, or '+', but found string")
	wantError(t, "r x=:\n", 1, 5, "expected backtick, '!', '[', identifier, '(', or string, but found ':'")
	wantError(t, "r: a 'b'\n", 1, 6, "expected "+afterDeps+", but found string")
	wantError(t, "r: a := b\n", 1, 6, "expected "+afterDeps+", but found ':='")
}

func TestParseTopLevelErrors(t *testing.T) {
	wantError(t, ": x\n", 1, 1, "expected "+item+", but found ':'")
	wantError(t, "  x\n", 1, 1, "expected "+item+", but found indent")
	wantError(t, "r:\n  x\n\"s\"\n", 3, 1, "expected "+item+", but found string")
}

func TestParseLexErrorsPassThrough(t *testing.T) {
	wantError(t, "x := \"abc\n", 1, 6, "unterminated string")
	wantError(t, "x := %\n", 1, 6, "unknown start of token '%'")
}

func TestParseRecipeBodies(t *testing.T) {
	cases := []struct {
		name, src string
		lines     []ast.Line
	}{
		{"blank lines inside are dropped", "r:\n  a\n\n  b\n", []ast.Line{
			{Parts: []ast.Expr{txt("a")}, Line: 2}, {Parts: []ast.Expr{txt("b")}, Line: 4}}},
		{"whitespace-only lines are blank", "r:\n  a\n   \n\t\n  b\n", []ast.Line{
			{Parts: []ast.Expr{txt("a")}, Line: 2}, {Parts: []ast.Expr{txt("b")}, Line: 5}}},
		{"blank line before the first line", "r:\n\n  a\n", []ast.Line{
			{Parts: []ast.Expr{txt("a")}, Line: 3}}},
		{"a blank line then a top-level line ends the body", "r:\n  a\n\nx := \"1\"\n", []ast.Line{
			{Parts: []ast.Expr{txt("a")}, Line: 2}}},
		{"a top-level comment ends the body", "r:\n  a\n# c\nx := \"1\"\n", []ast.Line{
			{Parts: []ast.Expr{txt("a")}, Line: 2}}},
		{"extra indentation stays in the text", "r:\n  a\n    b\n", []ast.Line{
			{Parts: []ast.Expr{txt("a")}, Line: 2}, {Parts: []ast.Expr{txt("  b")}, Line: 3}}},
		{"extra indentation before an interpolation", "r:\n  a\n    {{x}}\n", []ast.Line{
			{Parts: []ast.Expr{txt("a")}, Line: 2}, {Parts: []ast.Expr{txt("  "), ref("x")}, Line: 3}}},
		{"extra indentation hides the prefix", "r:\n  a\n   @b\n", []ast.Line{
			{Parts: []ast.Expr{txt("a")}, Line: 2}, {Parts: []ast.Expr{txt(" @b")}, Line: 3}}},
		{"tabs", "r:\n\ta\n\t\tb\n", []ast.Line{
			{Parts: []ast.Expr{txt("a")}, Line: 2}, {Parts: []ast.Expr{txt("\tb")}, Line: 3}}},
		{"comments in the body are text", "r:\n  # c\n  echo # d\n", []ast.Line{
			{Parts: []ast.Expr{txt("# c")}, Line: 2}, {Parts: []ast.Expr{txt("echo # d")}, Line: 3}}},
		{"CRLF", "r:\r\n  a\r\n  b\r\n", []ast.Line{
			{Parts: []ast.Expr{txt("a")}, Line: 2}, {Parts: []ast.Expr{txt("b")}, Line: 3}}},
		{"no final newline", "r:\n  a", []ast.Line{{Parts: []ast.Expr{txt("a")}, Line: 2}}},
		{"interpolations", "r:\n  {{a}}-{{ b + 'c' }}{{(d)}}\n", []ast.Line{
			{Parts: []ast.Expr{ref("a"), txt("-"), ast.Concat{Left: ref("b"), Right: lit("c")}, ref("d")}, Line: 2}}},
		{"an interpolation across lines", "r:\n  x {{ a +\n  b }} y\n", []ast.Line{
			{Parts: []ast.Expr{txt("x "), ast.Concat{Left: ref("a"), Right: ref("b")}, txt(" y")}, Line: 2}}},
		{"multibyte text", "r:\n  echo héllo ✓ 日本\n", []ast.Line{
			{Parts: []ast.Expr{txt("echo héllo ✓ 日本")}, Line: 2}}},
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
	// An interpolation the input leaves open is reported at its braces, as
	// upstream reports it (T4-5, T5-3).
	wantError(t, "r:\n  {{\n", 2, 3, "unterminated interpolation")
	wantError(t, "r:\n  x {{ a\n", 2, 5, "unterminated interpolation")
	wantError(t, "r:\n  x {{ a\n  echo $HOME\n", 2, 5, "unterminated interpolation")
	wantError(t, "r:\n  {{ }}\n", 2, 6, "expected "+value+", but found '}}'")
	wantError(t, "r:\n  {{ a b }}\n", 2, 8, "expected '&&', '!=', '!~', '||', '==', '=~', '}}', '(', '+', '++', or '/', but found identifier")
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

// TestParseParamDefaultTextKeepsQuotes checks the default as written, which
// --list shows: the quote style and escapes survive, spacing is normalized.
func TestParseParamDefaultTextKeepsQuotes(t *testing.T) {
	wantRecipe(t, "r a='x y' b=\"t\\tz\" c=(d+'e'):\n", ast.Recipe{
		Name: "r", Line: 1,
		Params: []ast.Param{
			{Name: "a", Default: lit("x y"), DefaultText: "'x y'"},
			{Name: "b", Default: lit("t\tz"), DefaultText: `"t\tz"`},
			{Name: "c", Default: ast.Concat{Left: ref("d"), Right: lit("e")}, DefaultText: "(d + 'e')"},
		},
	})
}

// TestParseLineContinuation follows upstream's run_shell and evaluate_line:
// a trailing backslash joins the next physical line, whose leading
// whitespace goes; the command reports the last physical line; a blank line
// ends the command; only the first line's prefix is a prefix.
func TestParseLineContinuation(t *testing.T) {
	cases := []struct {
		name, src string
		lines     []ast.Line
	}{
		{"misc::line_continuation_with_space#1", "foo:\n  echo a\\\n         b  \\\n             c\n", []ast.Line{
			{Parts: []ast.Expr{txt("echo a"), txt("b  "), txt("c")}, Line: 4}}},
		{"tab indentation", "a:\n\techo one \\\n\t  two\n\tthree\n", []ast.Line{
			{Parts: []ast.Expr{txt("echo one "), txt("two")}, Line: 3},
			{Parts: []ast.Expr{txt("three")}, Line: 4}}},
		{"a prefix only on the first line", "a:\n  @echo one \\\n  @two\n", []ast.Line{
			{Prefix: ast.PrefixAt, Parts: []ast.Expr{txt("echo one "), txt("@two")}, Line: 3}}},
		{"a prefix alone", "a:\n  -\\\n  false\n", []ast.Line{
			{Prefix: ast.PrefixDash, Parts: []ast.Expr{txt("false")}, Line: 3}}},
		{"interpolations on both lines", "a:\n  {{x}} \\\n    {{y}}\n", []ast.Line{
			{Parts: []ast.Expr{ref("x"), txt(" "), ref("y")}, Line: 3}}},
		{"an interpolation does not continue", "a:\n  {{'\\'}}\n  b\n", []ast.Line{
			{Parts: []ast.Expr{lit("\\")}, Line: 2}, {Parts: []ast.Expr{txt("b")}, Line: 3}}},
		{"a blank line ends the command", "a:\n  echo one \\\n\n  two\n", []ast.Line{
			{Parts: []ast.Expr{txt("echo one ")}, Line: 3}, {Parts: []ast.Expr{txt("two")}, Line: 4}}},
		{"a backslash on the last line", "a:\n  echo \\\nb:\n", []ast.Line{
			{Parts: []ast.Expr{txt("echo ")}, Line: 2}}},
		{"three lines", "a:\n  a \\\n  b \\\n  c\n", []ast.Line{
			{Parts: []ast.Expr{txt("a "), txt("b "), txt("c")}, Line: 4}}},
	}
	for _, c := range cases {
		jf := mustParse(t, c.src)
		if got := jf.Recipes[0].Lines; !reflect.DeepEqual(got, c.lines) {
			t.Errorf("%s: Lines =\n %+v\nwant\n %+v", c.name, got, c.lines)
		}
	}
}

// A string written inside an interpolation is not source text, so it never
// carries a prefix (misc::interpolation_evaluation_ignore_quiet#1).
func TestParseInterpolatedStringIsNotAPrefix(t *testing.T) {
	jf := mustParse(t, "foo:\n  {{'@echo foo 2>/dev/null'}}\n  {{'-x'}}-y\n")
	want := []ast.Line{
		{Parts: []ast.Expr{lit("@echo foo 2>/dev/null")}, Line: 2},
		{Parts: []ast.Expr{lit("-x"), txt("-y")}, Line: 3},
	}
	if got := jf.Recipes[0].Lines; !reflect.DeepEqual(got, want) {
		t.Errorf("Lines =\n %+v\nwant\n %+v", got, want)
	}
}

// Upstream's take_doc_comment refuses a `#!` comment that is the first item
// of the file, a common header; anywhere else it is a doc.
func TestParseShebangIsNotADoc(t *testing.T) {
	cases := []struct{ src, doc string }{
		{"#!/usr/bin/env just --justfile\na:\n", ""},
		{"\n#!/usr/bin/env just --justfile\na:\n", "!/usr/bin/env just --justfile"},
		{"#!x\n\n#!y\na:\n", "!y"},
		{"# !x\na:\n", "!x"},
	}
	for _, c := range cases {
		if got := mustParse(t, c.src).Recipes[0].Doc; got != c.doc {
			t.Errorf("Parse(%q) Doc = %q, want %q", c.src, got, c.doc)
		}
	}
}

// positionsSource exercises every position the parser records.
const positionsSource = "x := 'a' + yy\n@r p q=(x + zz): d1  d2\n  a\n    b {{p}}\n  {{'c'}} \\\n      e\n"

func parsePositions(t *testing.T) *ast.Justfile {
	t.Helper()
	jf, err := parser.Parse("justfile", []byte(positionsSource))
	if err != nil {
		t.Fatal(err)
	}
	return jf
}

// TestParsePositionsOfNames checks the positions of an assignment's names,
// which the analyzer reports errors at.
func TestParsePositionsOfNames(t *testing.T) {
	jf := parsePositions(t)
	a := jf.Assignments[0]
	if a.Line != 1 || a.Col != 1 || a.Value.(ast.Concat).Right != (ast.Var{Name: "yy", Line: 1, Col: 12}) {
		t.Errorf("assignment = %+v", a)
	}
}

// TestParsePositionsOfRecipeNames checks the recipe, its parameters and its
// dependencies.
func TestParsePositionsOfRecipeNames(t *testing.T) {
	r := parsePositions(t).Recipes[0]
	if r.Line != 2 || r.Col != 2 {
		t.Errorf("recipe at %d:%d, want 2:2", r.Line, r.Col)
	}
	if want := []ast.Dep{{Name: "d1", Line: 2, Col: 18}, {Name: "d2", Line: 2, Col: 22}}; !reflect.DeepEqual(r.Deps, want) {
		t.Errorf("Deps = %+v, want %+v", r.Deps, want)
	}
	if p, q := r.Params[0], r.Params[1]; p.Line != 2 || p.Col != 4 || q.Col != 6 ||
		q.Default.(ast.Concat).Right != (ast.Var{Name: "zz", Line: 2, Col: 13}) {
		t.Errorf("Params = %+v", r.Params)
	}
}

// TestParsePositionsInTheBody checks variables in the body and the extra
// indentation upstream rejects, which a continuation line may have.
func TestParsePositionsInTheBody(t *testing.T) {
	r := parsePositions(t).Recipes[0]
	wantExtra := []ast.Span{{}, {Line: 4, Col: 3, Width: 4}, {}}
	for i, l := range r.Lines {
		if l.ExtraIndent != wantExtra[i] {
			t.Errorf("line %d ExtraIndent = %+v, want %+v", i, l.ExtraIndent, wantExtra[i])
		}
	}
	if v := r.Lines[1].Parts[1]; v != (ast.Var{Name: "p", Line: 4, Col: 9}) {
		t.Errorf("body variable = %+v", v)
	}
}

// Parse errors cover the token they are about.
func TestParseErrorWidths(t *testing.T) {
	cases := []struct {
		src   string
		width int
	}{
		{"set nope\n", 4},
		{"r: a := b\n", 2},
		{"x := a 'bc'\n", 4},
		{"r:\n  a\n\t\tb\n", 2},
		{"x := \n", 1},
		{"x :=", 0},
		{"r:\n  {{ # hello\n  }}\n", 7}, // interpolation::comment_in_interopolation#1
	}
	for _, c := range cases {
		_, err := parser.Parse("justfile", []byte(c.src))
		var de *diag.Error
		if !errors.As(err, &de) || de.Width != c.width {
			t.Errorf("Parse(%q) error = %+v, want width %d", c.src, de, c.width)
		}
	}
}

// String literals get their values from lexer.StringValue: indented strings
// lose their common indentation, and cooked strings process escapes after
// that (the string::indented_* and string::cooked_string_* corpus cases).
func TestParseStringLiteralForms(t *testing.T) {
	jf := mustParse(t, "a := \"\"\"\n  foo\\\n  bar\n\"\"\"\nb := '''\n  x\\n\n  y\n'''\nc := \"\\u{1f916}\\t\"\n"+
		"set shell := [\"\\u{62}ash\", '\\c']\n")
	want := []ast.Assignment{
		{Name: "a", Value: lit("foobar\n"), Line: 1},
		{Name: "b", Value: lit("x\\n\ny\n"), Line: 5},
		{Name: "c", Value: lit("\U0001F916\t"), Line: 9},
	}
	if !reflect.DeepEqual(jf.Assignments, want) {
		t.Errorf("Assignments =\n %+v\nwant\n %+v", jf.Assignments, want)
	}
	if shell := []string{"bash", "\\c"}; !reflect.DeepEqual(jf.Settings.Shell, shell) {
		t.Errorf("Shell = %q, want %q", jf.Settings.Shell, shell)
	}
}

// Upstream cooks a string when it parses it, after the whole file has lexed,
// so a lex error later in the file and a parse error earlier in it both come
// before a bad escape.
func TestParseStringEscapeErrorsComeFromTheParser(t *testing.T) {
	wantError(t, "x := \"\\q\"\n", 1, 6, "`\\q` is not a valid escape sequence")
	wantError(t, "set shell := [\"a\", \"\\u{}\"]\n", 1, 20, "unicode escape sequences must not be empty")
	wantError(t, "set shell := [\"\\u\"]\n", 1, 15, "unterminated unicode escape sequence")
	wantError(t, "r:\n  {{ \"\\q\" }}\n", 2, 6, "`\\q` is not a valid escape sequence")
	wantError(t, "x := \"\\q\"\ny := 'abc\n", 2, 6, "unterminated string")
	wantError(t, "x := a b\ny := \"\\q\"\n", 1, 8, "expected "+afterName+", but found identifier")
	_, err := parser.Parse("justfile", []byte("x := \"a\\q\" + 'b'\n"))
	var de *diag.Error
	if !errors.As(err, &de) || de.Width != 5 {
		t.Errorf("escape error = %+v, want width 5", de)
	}
}

// misc::brace_escape and misc::brace_escape_extra: `{{{{` in a body is `{{`.
func TestParseBodyBraceEscape(t *testing.T) {
	wantRecipe(t, "foo:\n  echo '{{{{'\n  echo '{{{{{' {{{{{{x}}\n", ast.Recipe{
		Name: "foo", Line: 1,
		Lines: []ast.Line{
			{Parts: []ast.Expr{txt("echo '{{'")}, Line: 2},
			{Parts: []ast.Expr{txt("echo '{{{' {{"), ref("x")}, Line: 3},
		},
	})
}

// Upstream underlines the source of a line with extra indentation, so an
// escape there counts all four braces.
func TestParseExtraIndentWidthCountsTheEscapeSource(t *testing.T) {
	jf, err := parser.Parse("justfile", []byte("r:\n  a\n    {{{{ x {{y}}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := jf.Recipes[0].Lines[1].ExtraIndent, (ast.Span{Line: 3, Col: 3, Width: 9}); got != want {
		t.Errorf("ExtraIndent = %+v, want %+v", got, want)
	}
}

// TestParseCorpusErrors pins each parse-time case of the error_messages,
// delimiters and parser corpus groups: the category, the location and the
// message, which is copied from the corpus case named in each row.
func TestParseCorpusErrors(t *testing.T) {
	cases := []struct {
		id, src   string
		line, col int
		msg       string
	}{
		{"error_messages::unexpected_character#1", "&~", 1, 2, "expected character `&`"},
		{"error_messages::file_path_is_indented_if_justfile_is_long#1", strings.Repeat("\n", 19) + "foo", 20, 4,
			"expected '*', ':', '$', identifier, or '+', but found end of file"},
		{"delimiters::mismatched_delimiter#1", "(]", 1, 2,
			"mismatched closing delimiter `]`, did you mean to close the `(` on line 1?"},
		{"delimiters::unexpected_delimiter#1", "]", 1, 1, "unexpected closing delimiter `]`"},
		{"parser::backslash_eof#1", "foo:\n\\", 2, 1, "expected escape sequence but found end-of-file"},
	}
	for _, c := range cases {
		t.Run(c.id, func(t *testing.T) {
			wantError(t, c.src, c.line, c.col, c.msg)
		})
	}
}

// TestParseDelimiterContinuation pins delimiters::paren_continuation#1 and
// delimiters::bracket_continuation#1: a line break inside an open delimiter
// is whitespace, and a shell list may end in a comma.
func TestParseDelimiterContinuation(t *testing.T) {
	jf := mustParse(t, "x := (\n      'a'\n          +\n  'b'\n)\n\nfoo:\n  echo {{x}}\n")
	if want := (ast.Concat{Left: lit("a"), Right: lit("b")}); len(jf.Assignments) != 1 || jf.Assignments[0].Value != want {
		t.Errorf("paren_continuation: Assignments = %+v, want x := %+v", jf.Assignments, want)
	}
	jf = mustParse(t, "set shell := [\n  'sh',\n  '-cu',\n]\n\nfoo:\n  echo foo\n")
	if want := []string{"sh", "-cu"}; !reflect.DeepEqual(jf.Settings.Shell, want) {
		t.Errorf("bracket_continuation: Shell = %q, want %q", jf.Settings.Shell, want)
	}
}
