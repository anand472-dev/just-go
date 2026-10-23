package parser

import (
	"testing"

	"gitea.com/wimwian/just-go/internal/ast"
)

// import and import? are keywords when a string follows them. Each test reads the
// tree the parser gives for its source.
func TestParseImportStatements(t *testing.T) {
	jf, err := Parse("justfile", []byte("import 'x.just'\nimport? \"y.just\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(jf.Imports) != 2 {
		t.Fatalf("Imports = %+v; want two", jf.Imports)
	}
	checkImport(t, jf.Imports[0], "x.just", false, 1, 8)
	checkImport(t, jf.Imports[1], "y.just", true, 2, 9)
}

// checkImport fails unless imp has the path, optional flag, and position given,
// with the width of its quoted string.
func checkImport(t *testing.T, imp ast.Import, path string, optional bool, line, col int) {
	t.Helper()
	if imp.Path != path || imp.Optional != optional || imp.Line != line || imp.Col != col || imp.Width != len(path)+2 {
		t.Errorf("import = %+v; want %q optional=%v at %d:%d", imp, path, optional, line, col)
	}
}

func TestImportWordIsARecipeOrAnAssignmentWithoutAString(t *testing.T) {
	jf, err := Parse("justfile", []byte("import := 'v'\n\nimport:\n  echo\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(jf.Imports) != 0 || len(jf.Assignments) != 1 || len(jf.Recipes) != 1 {
		t.Errorf("import, assignments, recipes = %d, %d, %d; want 0, 1, 1",
			len(jf.Imports), len(jf.Assignments), len(jf.Recipes))
	}
}

// The string of an import is a string literal; a name in its place is upstream's
// error for the keyword `x` it wanted.
func TestImportWithAnotherWordIsUpstreamsError(t *testing.T) {
	_, err := Parse("justfile", []byte("import foo\"bar.just\"\n"))
	if err == nil || err.Error() != "justfile:1:8: expected keyword `x` but found identifier `foo`" {
		t.Errorf("Parse = %v; want the expected-keyword error at 1:8", err)
	}
}

// The string of an import is processed like any other, so a bad escape in it is
// the same error as elsewhere.
func TestImportStringEscapesAreChecked(t *testing.T) {
	if _, err := Parse("justfile", []byte("import \"a\\qb\"\n")); err == nil {
		t.Error("an unknown escape in an import string was accepted")
	}
}

// mod and mod? name a module, with the file it is in when a string follows.
func TestParseModuleStatements(t *testing.T) {
	jf, err := Parse("justfile", []byte("# docs\nmod foo\nmod? bar 'sub/bar.just'\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(jf.Modules) != 2 {
		t.Fatalf("Modules = %+v; want two", jf.Modules)
	}
	checkModule(t, jf.Modules[0], ast.Module{Name: "foo", Doc: "docs", Line: 2, Col: 5, Width: 3})
	checkModule(t, jf.Modules[1], ast.Module{Name: "bar", Optional: true, Path: "sub/bar.just", Line: 3, Col: 6, Width: 3})
}

// checkModule fails unless m is want in every field the test sets.
func checkModule(t *testing.T, m, want ast.Module) {
	t.Helper()
	if m != want {
		t.Errorf("module = %+v; want %+v", m, want)
	}
}

// A module name in a dependency is a path, and a path may go deeper.
func TestParseModulePathsInDependencies(t *testing.T) {
	jf, err := Parse("justfile", []byte("a: foo::bar::baz (x::y 'q')\n  echo\n"))
	if err != nil {
		t.Fatal(err)
	}
	deps := jf.Recipes[0].Deps
	if len(deps) != 2 || deps[0].Name != "foo::bar::baz" || deps[1].Name != "x::y" || len(deps[1].Args) != 1 {
		t.Errorf("dependencies = %+v; want the two paths, with the argument", deps)
	}
}

// A path must end in a name: a `::` with nothing after it is a syntax error.
func TestParseModulePathNeedsAName(t *testing.T) {
	if _, err := Parse("justfile", []byte("a: foo::\n  echo\n")); err == nil {
		t.Error("a path that ends in :: was accepted")
	}
}

// A `mod` with a bad name, or a bad string after it, is a syntax error.
func TestParseModuleStatementErrors(t *testing.T) {
	for _, src := range []string{"mod ?\n", "mod foo 'x\n"} {
		if _, err := Parse("justfile", []byte(src)); err == nil {
			t.Errorf("%q: Parse accepted it", src)
		}
	}
}

// mod as a recipe name, or followed by a colon, is a recipe.
func TestModWordIsARecipeWhenItIsNotAModule(t *testing.T) {
	jf, err := Parse("justfile", []byte("mod:\n  echo\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(jf.Modules) != 0 || len(jf.Recipes) != 1 {
		t.Errorf("modules, recipes = %d, %d; want 0, 1", len(jf.Modules), len(jf.Recipes))
	}
}

// A bad string after `mod` is a syntax error, and so is a bad path in brackets.
func TestParseModuleWithBadStringIsAnError(t *testing.T) {
	if _, err := Parse("justfile", []byte("mod foo \"\\q\"\n")); err == nil {
		t.Error("an unknown escape in a module path was accepted")
	}
}

func TestParseParenthesisedDependencyNeedsAName(t *testing.T) {
	for _, src := range []string{"a: (1)\n  echo\n", "a: (foo::)\n  echo\n"} {
		if _, err := Parse("justfile", []byte(src)); err == nil {
			t.Errorf("%q: a malformed parenthesised dependency was accepted", src)
		}
	}
}
