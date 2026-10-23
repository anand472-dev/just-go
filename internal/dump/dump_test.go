package dump

import (
	"testing"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/parser"
)

// dumpOf parses src and returns its dump with four-space indentation.
func dumpOf(t *testing.T, src string) string {
	t.Helper()
	jf, err := parser.Parse("justfile", []byte(src))
	if err != nil {
		t.Fatalf("parse %q: %v", src, err)
	}
	jf.Source = []byte(src) // as the loader sets it
	return Justfile(jf, "    ")
}

// The expected texts in these tests are the oracle's dumps of the same sources.
func TestDumpWritesTheCanonicalForm(t *testing.T) {
	for _, tc := range []struct {
		name, src, want string
	}{
		{"assignment", "a := 'x'\n", "a := 'x'\n"},
		{"export and eager", "export a := 'x'\neager b := 'y'\n", "export a := 'x'\neager b := 'y'\n"},
		{"private attribute", "[private]\na := 'x'\n", "[private]\na := 'x'\n"},
		{"quiet recipe and prefixes", "@foo:\n  echo a\n  @echo b\n  -echo c\n  @-echo d\n", "@foo:\n    echo a\n    @echo b\n    -echo c\n    @-echo d\n"},
		{"doc comment", "# hello\nfoo:\n  echo\n", "# hello\nfoo:\n    echo\n"},
		{"attributes in name order", "[group('x')]\n[doc('y')]\nfoo:\n  echo\n", "[doc('y')]\n[group('x')]\nfoo:\n    echo\n"},
		{"attributes three", "[private]\n[doc('d')]\n[confirm]\nfoo:\n  echo\n", "[confirm]\n[doc('d')]\n[private]\nfoo:\n    echo\n"},
		{"interpolations", "foo x:\n  echo {{ x + 'a' }} {{'b'}}\n", "foo x:\n    echo {{ x + 'a' }} {{ 'b' }}\n"},
		{"else if", "a := if 'x' == 'y' { 'p' } else if 'x' != 'y' { 'q' } else { 'r' }\n", "a := if 'x' == 'y' { 'p' } else if 'x' != 'y' { 'q' } else { 'r' }\n"},
		{"if without else", "a := if 'x' == 'y' { 'p' }\n", "a := if 'x' == 'y' { 'p' }\n"},
		{"match", "a := if 'x' =~ 'y' { 'p' } else { 'q' }\n", "a := if 'x' =~ 'y' { 'p' } else { 'q' }\n"},
		{"calls and backticks", "a := env('X', 'd') + join('a', 'b')\nb := `echo hi`\n", "a := env('X', 'd') + join('a', 'b')\nb := `echo hi`\n"},
		{"assert with and without message", "a := assert('x' == 'x', 'msg')\nb := assert('x' == 'x')\n", "a := assert('x' == 'x', 'msg')\nb := assert('x' == 'x')\n"},
		{"double quotes with a single quote inside", "a := \"it's\"\n", "a := \"it's\"\n"},
		{"escapes in double quotes", "a := \"it's\\n\\t\\\\\"\n", "a := \"it's\\n\\t\\\\\"\n"},
		{"blank lines collapse to one", "a := 'x'\n\n\nfoo:\n  echo\nbar:\n  echo\n\nbaz:\n  echo\n", "a := 'x'\n\nfoo:\n    echo\nbar:\n    echo\n\nbaz:\n    echo\n"},
		{"blank line in a body", "foo:\n  echo a\n\n  echo b\n", "foo:\n    echo a\n\n    echo b\n"},
		{"parameters", "foo a $b='d' c='e':\n  echo {{a}}\n", "foo a $b='d' c='e':\n    echo {{ a }}\n"},
		{"dependencies with arguments", "bar:\n  echo\nbaz x y:\n  echo\nqux:\n  echo\nfoo: bar (baz 'x' 'y') && qux\n  echo\n", "bar:\n    echo\nbaz x y:\n    echo\nqux:\n    echo\nfoo: bar (baz 'x' 'y') && qux\n    echo\n"},
		{"dependency with empty parentheses", "bar:\n  echo\nfoo: (bar)\n  echo\n", "bar:\n    echo\nfoo: bar\n    echo\n"},
		{"subsequent dependency alone", "bar:\n  echo\nfoo: && bar\n  echo\n", "bar:\n    echo\nfoo: && bar\n    echo\n"},
		{"list", "a := ['x', 'y']\n", "a := ['x', 'y']\n"},
		{"blank line before a doc comment", "a := 'x'\n\n# hello\nfoo:\n  echo\n", "a := 'x'\n\n# hello\nfoo:\n    echo\n"},
		{"no items", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := dumpOf(t, tc.src); got != tc.want {
				t.Errorf("dump of %q = %q; want %q", tc.src, got, tc.want)
			}
		})
	}
}

// Without the source text, the blank lines of the justfile are not known, so
// none are written between items or in a body.
func TestDumpWithoutSourceWritesNoBlankLines(t *testing.T) {
	jf, err := parser.Parse("justfile", []byte("a := 'x'\n\n\nfoo:\n  echo a\n\n  echo b\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := Justfile(jf, "    "); got != "a := 'x'\nfoo:\n    echo a\n    echo b\n" {
		t.Errorf("Justfile = %q", got)
	}
}

func TestDumpIndentsBodiesWithTheGivenIndent(t *testing.T) {
	jf, err := parser.Parse("justfile", []byte("foo:\n  echo\n"))
	if err != nil {
		t.Fatal(err)
	}
	jf.Source = []byte("foo:\n  echo\n")
	if got := Justfile(jf, "\t"); got != "foo:\n\techo\n" {
		t.Errorf("Justfile with a tab = %q", got)
	}
}

// An item from an import or a module is not the root file's, so it is left out.
func TestDumpLeavesOutItemsOfOtherFiles(t *testing.T) {
	other := &ast.Justfile{}
	jf := &ast.Justfile{
		Assignments: []ast.Assignment{{Name: "a", Value: ast.StringLit{Value: "x"}, Unit: other}, {Name: "b", Value: ast.StringLit{Value: "y"}}},
		Recipes:     []ast.Recipe{{Name: "r", Unit: other}},
	}
	if got := Justfile(jf, "    "); got != "b := 'y'\n" {
		t.Errorf("Justfile = %q; want only b", got)
	}
}

// An assignment's value that is not an expression this printer knows writes
// nothing rather than failing.
func TestExprOfAnUnknownNodeIsEmpty(t *testing.T) {
	if got := expr(nil); got != "" {
		t.Errorf("expr(nil) = %q; want empty", got)
	}
}

func TestPrefixOfNoMarkIsEmpty(t *testing.T) {
	if got := prefix(ast.PrefixNone); got != "" {
		t.Errorf("prefix(none) = %q", got)
	}
}
