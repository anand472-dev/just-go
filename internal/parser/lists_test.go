package parser_test

import (
	"reflect"
	"strings"
	"testing"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/parser"
)

// The dotenv settings take expressions, as upstream's do, and `dotenv-load`,
// `dotenv-override` and `dotenv-required` are booleans (see boolSettings).
func TestParseDotenvSettings(t *testing.T) {
	jf := mustParse(t, "d := '.env'\n"+
		"set dotenv-command := 'echo A=' + d\n"+
		"set dotenv-filename := d\n"+
		"set dotenv-path := '.' + '/' + d # here\n")
	s := jf.Settings
	if s.DotenvCommand != (ast.Concat{Left: lit("echo A="), Right: ast.Var{Name: "d", Line: 2, Col: 35}}) {
		t.Errorf("DotenvCommand = %+v", s.DotenvCommand)
	}
	if v, _ := s.DotenvFilename.(ast.Var); v != (ast.Var{Name: "d", Line: 3, Col: 24}) {
		t.Errorf("DotenvFilename = %+v", s.DotenvFilename)
	}
	if _, ok := s.DotenvPath.(ast.Concat); !ok {
		t.Errorf("DotenvPath = %+v", s.DotenvPath)
	}
	if got := mustParse(t, "foo:\n").Settings; got.DotenvCommand != nil || got.DotenvFilename != nil || got.DotenvPath != nil {
		t.Errorf("unset dotenv settings are not nil: %+v", got)
	}
}

// settings::undefined_variable_in_dotenv_command#1 and its filename and path
// siblings are the analyzer's; these are the parser's, with the oracle's
// positions.
func TestParseDotenvSettingErrors(t *testing.T) {
	wantError(t, "set dotenv-command\n", 1, 19, "expected ':=', but found end of line")
	wantError(t, "set dotenv-filename :=\n", 1, 23, "expected "+value+", but found end of line")
	wantError(t, "set dotenv-path := 'a' 'b'\n", 1, 24, "expected "+afterTempdirValue+", but found string")
	wantError(t, "set dotenv-load := 'x'\n", 1, 20, "expected identifier, but found string")
	wantError(t, "set dotenv-required := maybe\n", 1, 24,
		"expected keyword `true` or `false` but found identifier `maybe`")
	wantError(t, "set dotenv-override := 1\n", 1, 24, "unknown start of token '1'")
}

// `set lists` is unstable in any form, even `set lists := false`; `set
// unstable` is not.
func TestParseRecordsTheUnstableFeaturesAFileUses(t *testing.T) {
	for _, tc := range []struct {
		src  string
		want []ast.UnstableFeature
	}{
		{"foo:\n", nil},
		{"set unstable\n", nil},
		{"set unstable := false\nset quiet\n", nil},
		{"set lists\n", []ast.UnstableFeature{ast.ListsSetting}},
		{"set lists := true\n", []ast.UnstableFeature{ast.ListsSetting}},
		{"set lists := false\n", []ast.UnstableFeature{ast.ListsSetting}},
		{"set unstable\nset lists\n", []ast.UnstableFeature{ast.ListsSetting}},
	} {
		if got := mustParse(t, tc.src).Unstable; !reflect.DeepEqual(got, tc.want) {
			t.Errorf("Parse(%q).Unstable = %v, want %v", tc.src, got, tc.want)
		}
	}
}

func TestParseListLiteral(t *testing.T) {
	jf := mustParse(t, "x := ['a', y, ['b'], ]\nz := []\n")
	want := ast.List{
		Line: 1, Col: 6,
		Elements: []ast.Expr{
			lit("a"),
			ast.Var{Name: "y", Line: 1, Col: 12},
			ast.List{Line: 1, Col: 15, Elements: []ast.Expr{lit("b")}},
		},
	}
	if got := jf.Assignments[0].Value; !reflect.DeepEqual(got, want) {
		t.Errorf("x = %+v, want %+v", got, want)
	}
	if got, want := jf.Assignments[1].Value, (ast.List{Line: 2, Col: 6}); !reflect.DeepEqual(got, want) {
		t.Errorf("z = %+v, want %+v", got, want)
	}
}

// Every row but the last is a case of the oracle, with its position.
func TestParseListLiteralErrors(t *testing.T) {
	const list = "backtick, '!', '[', ']', identifier, '(', '/', or string"
	wantError(t, "x := [\n", 1, 8, "expected "+list+", but found end of file")
	wantError(t, "x := [,]\n", 1, 7, "expected "+list+", but found ','")
	wantError(t, "x := ['a',,]\n", 1, 11, "expected "+list+", but found ','")
	wantError(t, "x := ['a' 'b']\n", 1, 11,
		"expected '&&', '!=', '!~', '||', ']', ',', '==', '=~', '+', '++', or '/', but found string")
	wantError(t, "foo:\n  echo {{ [ }}\n", 2, 13, "mismatched closing delimiter `}`, did you mean to close the `[` on line 2?")
}

// A list in a recipe body or a parameter default is parsed too.
func TestParseListLiteralInBodyAndDefault(t *testing.T) {
	jf := mustParse(t, "foo x=['a']:\n  echo {{ ['b'] }}\n")
	r := jf.Recipes[0]
	if _, ok := r.Params[0].Default.(ast.List); !ok {
		t.Errorf("Default = %+v", r.Params[0].Default)
	}
	if _, ok := r.Lines[0].Parts[1].(ast.List); !ok {
		t.Errorf("Parts = %+v", r.Lines[0].Parts)
	}
}

// The six functions that exist only for lists are recorded where they are
// called, a list literal where its bracket is. Upstream records a call after
// its arguments, so `show(bool('t'))` has bool first, and a list literal
// before its elements.
func TestParseListUsesAreInUpstreamOrder(t *testing.T) {
	jf := mustParse(t, "x := show(bool('t'))\n"+
		"y := ['a', ['b']]\n"+
		"foo z=split('a', 'b'):\n"+
		"  echo {{ which('c') }} {{ num_jobs() }} {{ join_list('d') }} {{ uppercase('e') }}\n")
	want := []ast.ListUse{
		{Feature: ast.BoolFunction, At: ast.Span{Line: 1, Col: 11, Width: 4}},
		{Feature: ast.ShowFunction, At: ast.Span{Line: 1, Col: 6, Width: 4}},
		{Feature: ast.ListLiteral, At: ast.Span{Line: 2, Col: 6, Width: 1}},
		{Feature: ast.ListLiteral, At: ast.Span{Line: 2, Col: 12, Width: 1}},
		{Feature: ast.SplitFunction, At: ast.Span{Line: 3, Col: 7, Width: 5}},
		{Feature: ast.WhichFunction, At: ast.Span{Line: 4, Col: 11, Width: 5}},
		{Feature: ast.NumJobsFunction, At: ast.Span{Line: 4, Col: 28, Width: 8}},
		{Feature: ast.JoinListFunction, At: ast.Span{Line: 4, Col: 45, Width: 9}},
	}
	if !reflect.DeepEqual(jf.ListUses, want) {
		t.Errorf("ListUses =\n %+v\nwant\n %+v", jf.ListUses, want)
	}
	if got := mustParse(t, "x := uppercase('a') + y\ny := 'b'\n").ListUses; got != nil {
		t.Errorf("ListUses = %+v, want none", got)
	}
}

// The second pass, which a setting that changes how bodies are read causes,
// does not record a use twice.
func TestParseListUsesAreRecordedOnceWhenTheFileIsReadTwice(t *testing.T) {
	jf := mustParse(t, "x := ['a']\nset guards\n")
	if len(jf.ListUses) != 1 {
		t.Errorf("ListUses = %+v, want one", jf.ListUses)
	}
}

func BenchmarkParseLists(b *testing.B) {
	var sb strings.Builder
	for i := 0; i < 200; i++ {
		sb.WriteString("x := ['a', 'b', ['c', show(bool('t'))]]\n")
	}
	sb.WriteString("set lists\nset dotenv-path := ['a', 'b']\n")
	src := []byte(sb.String())
	b.ReportAllocs()
	b.SetBytes(int64(len(src)))
	for b.Loop() {
		if _, err := parser.Parse("justfile", src); err != nil {
			b.Fatal(err)
		}
	}
}
