package parser_test

import (
	"reflect"
	"testing"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/parser"
)

// rawOf parses src and returns the raw body of its first recipe.
func rawOf(t *testing.T, src string) []ast.RawLine {
	t.Helper()
	return mustParse(t, src).Recipes[0].Raw
}

func TestParseRawKeepsEveryPhysicalLine(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		want      []ast.RawLine
	}{
		{"a shebang and its script", "foo:\n  #!/bin/sh\n  echo a\n", []ast.RawLine{
			{Line: 2, Parts: []ast.Expr{txt("#!/bin/sh")}}, {Line: 3, Parts: []ast.Expr{txt("echo a")}}}},
		{"a blank line between", "foo:\n  echo a\n\n  echo b\n", []ast.RawLine{
			{Line: 2, Parts: []ast.Expr{txt("echo a")}}, {Line: 3}, {Line: 4, Parts: []ast.Expr{txt("echo b")}}}},
		{"a line of only spaces is blank", "foo:\n  echo a\n   \n  echo b\n", []ast.RawLine{
			{Line: 2, Parts: []ast.Expr{txt("echo a")}}, {Line: 3}, {Line: 4, Parts: []ast.Expr{txt("echo b")}}}},
		{"blank lines at the end go", "foo:\n  echo a\n\n\n", []ast.RawLine{
			{Line: 2, Parts: []ast.Expr{txt("echo a")}}}},
		{"a continuation stays two lines", "foo:\n  echo a \\\n    b\n", []ast.RawLine{
			{Line: 2, Parts: []ast.Expr{txt("echo a \\")}}, {Line: 3, Parts: []ast.Expr{txt("  b")}}}},
		{"the sigils stay", "foo:\n  @-echo a\n", []ast.RawLine{
			{Line: 2, Parts: []ast.Expr{txt("@-echo a")}}}},
		{"an interpolation that spans lines is one line", "foo:\n  echo {{\n    x\n  }}\n  echo b\n", []ast.RawLine{
			{Line: 2, Parts: []ast.Expr{txt("echo "), ref("x")}}, {Line: 5, Parts: []ast.Expr{txt("echo b")}}}},
		{"a doubled brace is text", "foo:\n  echo {{{{x}}\n", []ast.RawLine{
			{Line: 2, Parts: []ast.Expr{txt("echo {{x}}")}}}},
		{"a comment", "foo:\n  # c\n  echo b\n", []ast.RawLine{
			{Line: 2, Parts: []ast.Expr{txt("# c")}}, {Line: 3, Parts: []ast.Expr{txt("echo b")}}}},
		{"no body", "foo:\n", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := rawOf(t, tc.src); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Raw = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// The shell recipe's lines do not change: a continuation is joined and the
// sigils are cut off.
func TestParseRawLeavesLinesAlone(t *testing.T) {
	r := mustParse(t, "foo:\n  @-echo a \\\n    b\n").Recipes[0]
	want := []ast.Line{{Prefix: ast.PrefixAtDash, Parts: []ast.Expr{txt("echo a "), txt("b")}, Line: 3}}
	if len(r.Lines) != 1 || r.Lines[0].Prefix != want[0].Prefix || r.Lines[0].Line != 3 {
		t.Errorf("Lines = %+v, want %+v", r.Lines, want)
	}
	if got := rawOf(t, "foo:\n  @-echo a\n")[0].Parts[0]; got != txt("@-echo a") {
		t.Errorf("a line with sigils was changed in place: %v", got)
	}
}

func TestParseQuietRecipeIsRecorded(t *testing.T) {
	if r := mustParse(t, "@foo:\n  echo a\n").Recipes[0]; !r.Quiet {
		t.Error("@foo is not Quiet")
	}
	if r := mustParse(t, "foo:\n  echo a\n").Recipes[0]; r.Quiet {
		t.Error("foo is Quiet")
	}
}

func BenchmarkParseRawBody(b *testing.B) {
	src := []byte("foo:\n  #!/bin/sh\n  echo a\n\n  @echo b \\\n    c\n  echo {{ 'x' }}\n")
	b.ReportAllocs()
	for b.Loop() {
		if _, err := parser.Parse("justfile", src); err != nil {
			b.Fatal(err)
		}
	}
}
