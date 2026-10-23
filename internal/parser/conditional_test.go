package parser_test

import (
	"reflect"
	"strings"
	"testing"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/parser"
)

func cmp(op string, left, right ast.Expr) ast.Expr {
	return ast.Comparison{Op: op, Left: left, Right: right}
}

func cond(c, then, otherwise ast.Expr) ast.Expr {
	return ast.Conditional{Cond: c, Then: then, Else: otherwise}
}

// assignmentValue parses src and returns the value of its first assignment,
// without positions.
func assignmentValue(t *testing.T, src string) ast.Expr {
	t.Helper()
	jf := mustParse(t, src)
	if len(jf.Assignments) == 0 {
		t.Fatalf("Parse(%q) found no assignment", src)
	}
	return jf.Assignments[0].Value
}

// The rows are cases of the oracle's conditional group, conditional::if_else#1
// among them, and the four comparison operators of regexes::*.
func TestParseConditional(t *testing.T) {
	for _, tc := range []struct {
		src  string
		want ast.Expr
	}{
		{"x := if 'a' == 'b' { 'c' } else { 'd' }\n",
			cond(cmp("==", lit("a"), lit("b")), lit("c"), lit("d"))},
		{"x := if 'a' != 'b' { 'c' } else { 'd' }\n",
			cond(cmp("!=", lit("a"), lit("b")), lit("c"), lit("d"))},
		{"x := if 'a' =~ 'b' { 'c' } else { 'd' }\n",
			cond(cmp("=~", lit("a"), lit("b")), lit("c"), lit("d"))},
		{"x := if 'a' !~ 'b' { 'c' } else { 'd' }\n",
			cond(cmp("!~", lit("a"), lit("b")), lit("c"), lit("d"))},
		// conditional::if_else#1: an `else if` chain.
		{"x := if '0' == '1' { 'a' } else if '0' == '0' { 'b' } else { 'c' }\n",
			cond(cmp("==", lit("0"), lit("1")), lit("a"),
				cond(cmp("==", lit("0"), lit("0")), lit("b"), lit("c")))},
		// conditional::complex_expressions#1: sums on every side.
		{"x := if 'a' + 'b' == `echo ab` { 'c' + 'd' } else { 'e' + 'f' }\n",
			cond(cmp("==", ast.Concat{Left: lit("a"), Right: lit("b")}, ast.Backtick{Command: "echo ab", Line: 1, Col: 24, Width: 9}),
				ast.Concat{Left: lit("c"), Right: lit("d")}, ast.Concat{Left: lit("e"), Right: lit("f")})},
		{"x := if y == z { y } else { z }\n",
			cond(cmp("==", ref("y"), ref("z")), ref("y"), ref("z"))},
		// A conditional nests in either branch and in a comparison.
		{"x := if 'a' == (if 'b' == 'b' { 'a' } else { 'z' }) { 'c' } else { 'd' }\n",
			cond(cmp("==", lit("a"), cond(cmp("==", lit("b"), lit("b")), lit("a"), lit("z"))), lit("c"), lit("d"))},
		{"x := if 'a' == 'a' { if 'b' == 'c' { 'x' } else { 'y' } } else { 'd' }\n",
			cond(cmp("==", lit("a"), lit("a")), cond(cmp("==", lit("b"), lit("c")), lit("x"), lit("y")), lit("d"))},
		// delimiters::brace_continuation#1: line breaks inside braces are whitespace.
		{"x := if '' == '' {\n  'a'\n} else {\n  'b'\n}\n\nfoo:\n  echo {{x}}\n",
			cond(cmp("==", lit(""), lit("")), lit("a"), lit("b"))},
	} {
		got := assignmentValue(t, tc.src)
		if !reflect.DeepEqual(stripPositions(got), stripPositions(tc.want)) {
			t.Errorf("Parse(%q) value =\n %+v\nwant\n %+v", tc.src, got, tc.want)
		}
	}
}

// stripPositions zeroes the positions of the nodes that carry them, so that a
// row need not count columns; the tests of positions are separate.
func stripPositions(e ast.Expr) ast.Expr {
	switch x := e.(type) {
	case ast.Comparison:
		return ast.Comparison{Op: x.Op, Left: stripPositions(x.Left), Right: stripPositions(x.Right)}
	case ast.Conditional:
		return ast.Conditional{Cond: stripPositions(x.Cond), Then: stripPositions(x.Then), Else: stripPositions(x.Else)}
	case ast.Assert:
		return ast.Assert{Cond: stripPositions(x.Cond), Message: stripPositions(x.Message), Text: x.Text}
	case ast.Concat:
		return ast.Concat{Left: stripPositions(x.Left), Right: stripPositions(x.Right)}
	case ast.Backtick:
		x.Line, x.Col = 0, 0
		return x
	case ast.Var:
		return ast.Var{Name: x.Name}
	}
	return e
}

// interpolation::closing_curly_brace_can_abut_interpolation_close#1 and
// format_string::conditional_no_whitespace#1: the braces of the branches and
// the `}}` that closes the interpolation may touch.
func TestParseConditionalInAnInterpolation(t *testing.T) {
	for _, src := range []string{
		"foo:\n  echo {{if 'a' == 'b' { 'c' } else { 'd' }}}\n",
		"foo:\n  echo {{ if 'a' == 'b' { 'c' } else { 'd' } }}\n",
		"foo:\n  echo {{ if 'a' == 'b' {\n 'c' } else { 'd' } }}\n",
	} {
		jf := mustParse(t, src)
		parts := jf.Recipes[0].Lines[0].Parts
		if len(parts) != 2 {
			t.Fatalf("Parse(%q) parts = %+v, want text and a conditional", src, parts)
		}
		if _, ok := parts[1].(ast.Conditional); !ok {
			t.Errorf("Parse(%q) part = %+v, want a conditional", src, parts[1])
		}
	}
}

// A conditional in a dependency's arguments, as json::dependency_argument#1
// has one, ends where its closing brace does.
func TestParseConditionalAsDependencyArgument(t *testing.T) {
	jf := mustParse(t, "foo a b c:\nbar: (foo\n  x\n  if 'a' == 'b' { 'c' } else { 'd' }\n  'e'\n)\n")
	args := jf.Recipes[1].Deps[0].Args
	if len(args) != 3 {
		t.Fatalf("args = %+v, want three", args)
	}
	if _, ok := args[1].(ast.Conditional); !ok {
		t.Errorf("args[1] = %+v, want a conditional", args[1])
	}
}

func TestParseConditionalPositions(t *testing.T) {
	jf, err := parser.Parse("justfile", []byte("x := if 'a' =~ 'b' { 'c' } else { 'd' }\ny := if 'a' != 'b' { 'c' } else if y == 'z' { 'd' } else { 'e' }\n"))
	if err != nil {
		t.Fatal(err)
	}
	c := jf.Assignments[0].Value.(ast.Conditional).Cond.(ast.Comparison)
	if c.Line != 1 || c.Col != 13 {
		t.Errorf("operator at %d:%d, want 1:13", c.Line, c.Col)
	}
	inner := jf.Assignments[1].Value.(ast.Conditional).Else.(ast.Conditional).Cond.(ast.Comparison)
	if inner.Line != 2 || inner.Col != 38 {
		t.Errorf("operator at %d:%d, want 2:38", inner.Line, inner.Col)
	}
}

// assertions::assert_message_may_be_omitted#1 and assertions::assert_fail#1.
func TestParseAssert(t *testing.T) {
	for _, tc := range []struct {
		src  string
		want ast.Expr
	}{
		{"x := assert('a' == 'a', 'fail')\n",
			ast.Assert{Cond: cmp("==", lit("a"), lit("a")), Message: lit("fail"), Text: "'a' == 'a'"}},
		{"x := assert('a' == 'a')\n",
			ast.Assert{Cond: cmp("==", lit("a"), lit("a")), Text: "'a' == 'a'"}},
		{"x := assert('a' != 'a', 'b' + c)\n",
			ast.Assert{Cond: cmp("!=", lit("a"), lit("a")), Message: ast.Concat{Left: lit("b"), Right: ref("c")}, Text: "'a' != 'a'"}},
		{"x := assert(y=~'a' , 'm')\n",
			ast.Assert{Cond: cmp("=~", ref("y"), lit("a")), Message: lit("m"), Text: "y =~ 'a'"}},
		{"x := 'p' + assert('a' == 'a')\n",
			ast.Concat{Left: lit("p"), Right: ast.Assert{Cond: cmp("==", lit("a"), lit("a")), Text: "'a' == 'a'"}}},
	} {
		got := assignmentValue(t, tc.src)
		if !reflect.DeepEqual(stripAssert(got), tc.want) {
			t.Errorf("Parse(%q) value =\n %+v\nwant\n %+v", tc.src, got, tc.want)
		}
	}
}

// stripAssert is stripPositions, and zeroes the position of an assert too.
func stripAssert(e ast.Expr) ast.Expr {
	switch x := stripPositions(e).(type) {
	case ast.Assert:
		return x
	case ast.Concat:
		return ast.Concat{Left: stripAssert(x.Left), Right: stripAssert(x.Right)}
	default:
		return x
	}
}

func TestParseAssertLocatesTheName(t *testing.T) {
	jf, err := parser.Parse("justfile", []byte("foo:\n  {{ assert('a' != 'a', 'error message') }}\n"))
	if err != nil {
		t.Fatal(err)
	}
	a := jf.Recipes[0].Lines[0].Parts[0].(ast.Assert)
	if a.Line != 2 || a.Col != 6 {
		t.Errorf("assert at %d:%d, want 2:6", a.Line, a.Col)
	}
}

// The condition of an assert without a message is quoted in its failure as
// upstream displays an expression: one space around each binary operator, one
// after each comma, strings and backticks as they were written.
func TestParseAssertTextIsDisplayedAsUpstreamDisplaysIt(t *testing.T) {
	for _, tc := range []struct{ cond, want string }{
		{"'a'!='a'", "'a' != 'a'"},
		{`"a" +   'b'==c`, `"a" + 'b' == c`},
		{"f ( a ,b )=='x'", "f(a, b) == 'x'"},
		{"f()=='x'", "f() == 'x'"},
		{"( a )=='x'", "(a) == 'x'"},
		{"`echo hi` =~ '^h'", "`echo hi` =~ '^h'"},
		{"x\n  !~\n  'y'", "x !~ 'y'"},
		{"if a=='b'{'c'}else{'d'}=='c'", "if a == 'b' { 'c' } else { 'd' } == 'c'"},
		{"'''i''' == \"\"\"j\"\"\"", "'''i''' == \"\"\"j\"\"\""},
		{"[ 'a' ,'b' ]=='c'", "['a', 'b'] == 'c'"},
	} {
		src := "x := assert(" + tc.cond + ")\n"
		jf, err := parser.Parse("justfile", []byte(src))
		if err != nil {
			if strings.Contains(tc.cond, "[") {
				continue // list literals parse; the analyzer refuses them
			}
			t.Errorf("Parse(%q) error: %v", src, err)
			continue
		}
		if got := jf.Assignments[0].Value.(ast.Assert).Text; got != tc.want {
			t.Errorf("Parse(%q) text = %q, want %q", src, got, tc.want)
		}
	}
}

// The rows are errors of the oracle, with its positions and its expected
// sets: conditional::incorrect_else_identifier#1 and
// conditional::unexpected_op#1 are the first two.
func TestParseConditionalErrors(t *testing.T) {
	const afterBranch = "'&&', '!=', '!~', '||', comment, end of file, end of line, '==', or '=~'"
	const afterOperand = "'&&', '!=', '!~', '||', '{', '==', '=~', '+', '++', or '/'"
	const afterComparison = "'&&', '||', '{', '+', '++', or '/'"
	const afterOperandInBlock = "'&&', '!=', '!~', '||', '}', '==', '=~', '+', '++', or '/'"
	const value = "backtick, '!', '[', identifier, '(', '/', or string"
	wantError(t, "TEST := if path_exists('/bin/bash') == 'true' {'yes'} els {'no'}\n", 1, 55,
		"expected "+afterBranch+", but found identifier")
	wantError(t, "a := if '' a '' { '' } else { b }\n", 1, 12, "expected "+afterOperand+", but found identifier")
	wantError(t, "a := if 'a' == 'b' 'c'\n", 1, 20, "expected "+afterComparison+", but found string")
	wantError(t, "a := if\n", 1, 8, "expected "+value+", but found end of line")
	wantError(t, "a := if 'a' == 'b' { 'c' } else 'd'\n", 1, 33, "expected '{', but found string")
	wantError(t, "a := if 'a' == 'b' { 'c' } else {\n", 1, 35, "expected "+value+", but found end of file")
	wantError(t, "a := if 'a' == 'b' { 'c' } else { 'd'\n", 1, 39, "expected "+afterOperandInBlock+", but found end of file")
	wantError(t, "a := if 'a' == 'b' { } else { 'd' }\n", 1, 22, "expected "+value+", but found '}'")
	wantError(t, "a := if 'a' ==\n", 1, 15, "expected "+value+", but found end of line")
	wantError(t, "a := 'a' == \n", 1, 13, "expected "+value+", but found end of line")
}

func TestParseAssertErrors(t *testing.T) {
	const value = "backtick, '!', '[', identifier, '(', '/', or string"
	wantError(t, "a := assert\n", 1, 12, "expected '(', but found end of line")
	wantError(t, "a := assert 'a'\n", 1, 13, "expected '(', but found string")
	wantError(t, "a := assert()\n", 1, 13, "expected "+value+", but found ')'")
	wantError(t, "a := assert('a' == 'a',)\n", 1, 24, "expected "+value+", but found ')'")
	wantError(t, "a := assert('a' == 'a' 'b')\n", 1, 24,
		"expected '&&', '||', ',', ')', '+', '++', or '/', but found string")
}

// A comparison outside an `if` or `assert` condition, an `if` without an
// `else`, and a condition that is not a comparison are features of `set
// lists`; the parser records where, in the order upstream does.
func TestParseRecordsTheListFeaturesOfConditionals(t *testing.T) {
	jf := mustParse(t, "a := 'a' == 'b'\n"+
		"b := if 'a' == 'b' { 'c' }\n"+
		"c := if 'a' { 'c' } else { 'd' }\n"+
		"d := assert(f())\n"+
		"e := if ('a' == 'b') { 'c' } else { 'd' }\n"+
		"f := if 'a' == 'b' { 'x' == 'y' } else { 'd' }\n")
	want := []ast.ListUse{
		{Feature: ast.ComparisonOperator, At: ast.Span{Line: 1, Col: 10, Width: 2}},
		{Feature: ast.IfWithoutElse, At: ast.Span{Line: 2, Col: 6, Width: 2}},
		{Feature: ast.NonComparisonCondition, At: ast.Span{Line: 3, Col: 9, Width: 3}},
		{Feature: ast.NonComparisonCondition, At: ast.Span{Line: 4, Col: 13, Width: 1}},
		{Feature: ast.ComparisonOperator, At: ast.Span{Line: 5, Col: 14, Width: 2}},
		{Feature: ast.NonComparisonCondition, At: ast.Span{Line: 5, Col: 9, Width: 1}},
		{Feature: ast.ComparisonOperator, At: ast.Span{Line: 6, Col: 26, Width: 2}},
	}
	if !reflect.DeepEqual(jf.ListUses, want) {
		t.Errorf("ListUses =\n %+v\nwant\n %+v", jf.ListUses, want)
	}
	// None of the stable forms is a use.
	stable := mustParse(t, "a := if 'a' == 'b' { 'c' } else if 'a' =~ 'a' { 'e' } else { 'd' }\nb := assert('a' !~ 'b', 'm')\n")
	if stable.ListUses != nil {
		t.Errorf("ListUses = %+v, want none", stable.ListUses)
	}
}

// The words `if`, `else` and `assert` are keywords of an expression, as they
// are upstream.
func TestParseKeywordsAreNotVariables(t *testing.T) {
	wantError(t, "a := if\n", 1, 8, "expected backtick, '!', '[', identifier, '(', '/', or string, but found end of line")
	jf := mustParse(t, "else := 'x'\nb := else\n")
	if got := jf.Assignments[1].Value; got != ast.Expr(ast.Var{Name: "else"}) {
		t.Errorf("b = %+v, want the variable else", got)
	}
}

func TestParseConditionalHonoursTheRecursionLimit(t *testing.T) {
	src := "x := " + strings.Repeat("if 'a' == 'a' { ", 1000) + "'b'" + strings.Repeat(" } else { 'c' }", 1000) + "\n"
	_, err := parser.Parse("justfile", []byte(src))
	if err == nil || !strings.Contains(err.Error(), "parsing recursion depth exceeded") {
		t.Errorf("error = %v, want the recursion limit", err)
	}
}

func BenchmarkParseConditionals(b *testing.B) {
	var sb strings.Builder
	for i := 0; i < 200; i++ {
		sb.WriteString("x := if 'a' + 'b' == 'ab' { 'c' } else if 'a' =~ 'b' { 'd' } else { assert('a' != 'b', 'm') }\n")
	}
	src := []byte(sb.String())
	b.ReportAllocs()
	b.SetBytes(int64(len(src)))
	for b.Loop() {
		if _, err := parser.Parse("justfile", src); err != nil {
			b.Fatal(err)
		}
	}
}
