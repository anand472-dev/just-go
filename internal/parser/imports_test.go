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
