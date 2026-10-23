package loader

import (
	"errors"
	"strings"
	"testing"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/sys/sysfake"
)

// asDiag reports whether err holds a *diag.Error, which it stores in de.
func asDiag(err error, de **diag.Error) bool { return errors.As(err, de) }

// recipeNames returns the names of the recipes of the tree, in order.
func recipeNames(recipes []string) string { return strings.Join(recipes, " ") }

func loadNames(t *testing.T, fsys sysfake.FakeFS, path string) ([]string, error) {
	t.Helper()
	jf, err := Load(fsys, path)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, r := range jf.Recipes {
		out = append(out, r.Name)
	}
	return out, nil
}

// The root's own items come first, then each import in turn, so an import's
// recipes never become the default recipe (see import_recipes_are_not_default).
func TestImportMergesRootItemsFirstThenImports(t *testing.T) {
	fsys := sysfake.FakeFS{
		"/proj/justfile": "import 'b'\n\nr1:\n  echo\n",
		"/proj/b":        "b1:\n  echo\n",
	}
	got, err := loadNames(t, fsys, "/proj/justfile")
	if err != nil || recipeNames(got) != "r1 b1" {
		t.Fatalf("recipes = %v, %v; want r1 b1", got, err)
	}
	jf, _ := Load(fsys, "/proj/justfile")
	if jf.Recipes[0].Unit != nil || jf.Recipes[1].Unit == nil {
		t.Errorf("the root recipe has an origin, or the imported one has none")
	}
}

func TestRequiredMissingImportIsAnErrorAtItsString(t *testing.T) {
	_, err := loadNames(t, sysfake.FakeFS{"/proj/justfile": "import './missing'\n\na:\n  echo\n"}, "/proj/justfile")
	var de *diag.Error
	if !asDiag(err, &de) || de.Msg != "could not find source file for import" || de.Line != 1 || de.Col != 8 || de.Width != len("'./missing'") {
		t.Errorf("error = %+v; want the missing-import error at 1:8", err)
	}
}

func TestOptionalMissingImportIsSkipped(t *testing.T) {
	got, err := loadNames(t, sysfake.FakeFS{"/proj/justfile": "import? './missing'\n\na:\n  echo\n"}, "/proj/justfile")
	if err != nil || recipeNames(got) != "a" {
		t.Errorf("recipes = %v, %v; want a", got, err)
	}
}

// A file that imports the file importing it is circular. Upstream gives the
// error without a location.
func TestCircularImportIsAnErrorWithoutALocation(t *testing.T) {
	fsys := sysfake.FakeFS{
		"/proj/justfile": "import 'a'\n",
		"/proj/a":        "import 'justfile'\n",
	}
	_, err := loadNames(t, fsys, "/proj/justfile")
	var de *diag.Error
	if !asDiag(err, &de) || de.Line != 0 || de.Msg != "import `/proj/justfile` in `/proj/a` is circular" {
		t.Errorf("error = %+v; want the circular-import error with no location", err)
	}
}

// `~/` in an import names a file under the home directory the caller gives.
func TestTildeImportUsesTheHomeDirectoryGiven(t *testing.T) {
	fsys := sysfake.FakeFS{
		"/proj/justfile":   "import '~/mod.just'\n",
		"/home/u/mod.just": "foo:\n  echo\n",
	}
	jf, err := LoadHome(fsys, "/proj/justfile", "/home/u")
	if err != nil || len(jf.Recipes) != 1 || jf.Recipes[0].Name != "foo" {
		t.Errorf("LoadHome = %v, %v; want the recipe foo from the home directory", jf, err)
	}
	if _, err := LoadHome(fsys, "/proj/justfile", ""); err == nil {
		t.Error("an import under ~/ with no home directory was found")
	}
}

// With allow-duplicate-recipes the root's recipe wins over the imported one of
// the same name, which is dropped.
func TestRootRecipeOverridesImportWhenDuplicatesAreAllowed(t *testing.T) {
	fsys := sysfake.FakeFS{
		"/proj/justfile": "a:\n  echo ROOT\n\nimport 'b'\n\nset allow-duplicate-recipes\n",
		"/proj/b":        "a:\n  echo IMPORT\n",
	}
	jf, err := Load(fsys, "/proj/justfile")
	if err != nil || len(jf.Recipes) != 1 || jf.Recipes[0].Unit != nil {
		t.Errorf("Load = %+v, %v; want only the root's recipe a", jf.Recipes, err)
	}
}

// Without allow-duplicate-recipes the same name in an import is a redefinition.
func TestImportedDuplicateIsAnErrorWithoutTheSetting(t *testing.T) {
	fsys := sysfake.FakeFS{
		"/proj/justfile": "a:\n  echo ROOT\n\nimport 'b'\n",
		"/proj/b":        "a:\n  echo IMPORT\n",
	}
	if _, err := Load(fsys, "/proj/justfile"); err == nil {
		t.Error("a duplicate recipe from an import was accepted without allow-duplicate-recipes")
	}
}

// An import that the importing file does not name in the same directory, but
// in a subdirectory, is found relative to the importer.
func TestNestedImportIsRelativeToItsImporter(t *testing.T) {
	fsys := sysfake.FakeFS{
		"/proj/justfile": "import 'sub/a'\n",
		"/proj/sub/a":    "import 'b'\n",
		"/proj/sub/b":    "b:\n  echo\n",
	}
	got, err := loadNames(t, fsys, "/proj/justfile")
	if err != nil || recipeNames(got) != "b" {
		t.Errorf("recipes = %v, %v; want b", got, err)
	}
}

// An import that is an absolute path is read as it is.
func TestAbsoluteImportIsReadAsGiven(t *testing.T) {
	fsys := sysfake.FakeFS{
		"/proj/justfile":   "import '/lib/shared.just'\n",
		"/lib/shared.just": "s:\n  echo\n",
	}
	got, err := loadNames(t, fsys, "/proj/justfile")
	if err != nil || recipeNames(got) != "s" {
		t.Errorf("recipes = %v, %v; want s", got, err)
	}
}

// importFS is a FakeFS whose import checks and import reads can fail.
type importFS struct {
	sysfake.FakeFS
	isErr, readErr error
}

func (f importFS) IsFile(p string) (bool, error) {
	if f.isErr != nil && p != "/proj/justfile" {
		return false, f.isErr
	}
	return f.FakeFS.IsFile(p)
}

func (f importFS) ReadFile(p string) ([]byte, error) {
	if f.readErr != nil && p != "/proj/justfile" {
		return nil, f.readErr
	}
	return f.FakeFS.ReadFile(p)
}

func TestImportCheckErrorIsReported(t *testing.T) {
	boom := errors.New("boom")
	fsys := importFS{FakeFS: sysfake.FakeFS{"/proj/justfile": "import 'b'\n", "/proj/b": ""}, isErr: boom}
	if _, err := Load(fsys, "/proj/justfile"); !errors.Is(err, boom) {
		t.Errorf("error = %v; want the check failure", err)
	}
}

func TestImportReadErrorIsReported(t *testing.T) {
	boom := errors.New("boom")
	fsys := importFS{FakeFS: sysfake.FakeFS{"/proj/justfile": "import 'b'\n", "/proj/b": ""}, readErr: boom}
	if _, err := Load(fsys, "/proj/justfile"); !errors.Is(err, boom) {
		t.Errorf("error = %v; want the read failure", err)
	}
}

// An import that does not parse is reported in its own file.
func TestImportParseErrorIsInTheImportedFile(t *testing.T) {
	fsys := sysfake.FakeFS{"/proj/justfile": "import 'b'\n", "/proj/b": "x :=\n"}
	_, err := Load(fsys, "/proj/justfile")
	var de *diag.Error
	if !asDiag(err, &de) || de.File != "/proj/b" {
		t.Errorf("error = %+v; want it in /proj/b", err)
	}
}

// Importing the same file twice merges it once.
func TestImportedFileIsMergedOnce(t *testing.T) {
	fsys := sysfake.FakeFS{"/proj/justfile": "import 'b'\nimport 'b'\n", "/proj/b": "b1:\n  echo\n"}
	got, err := loadNames(t, fsys, "/proj/justfile")
	if err != nil || recipeNames(got) != "b1" {
		t.Errorf("recipes = %v, %v; want b1 once", got, err)
	}
}

// With allow-duplicate-variables the root's assignment wins over the import's.
func TestRootAssignmentOverridesImportWhenDuplicatesAreAllowed(t *testing.T) {
	fsys := sysfake.FakeFS{
		"/proj/justfile": "f := 'bar'\n\nimport 'b'\n\nset allow-duplicate-variables\n",
		"/proj/b":        "f := 'foo'\n",
	}
	jf, err := Load(fsys, "/proj/justfile")
	if err != nil || len(jf.Assignments) != 1 || jf.Assignments[0].Value.(ast.StringLit).Value != "bar" {
		t.Errorf("Load assignments = %+v, %v; want only the root's f", jf.Assignments, err)
	}
}

// A variable an import defines is merged and visible to the root.
func TestImportedVariableIsMerged(t *testing.T) {
	fsys := sysfake.FakeFS{
		"/proj/justfile": "import 'b'\n\na:\n  echo {{ x }}\n",
		"/proj/b":        "x := 'v'\n",
	}
	jf, err := Load(fsys, "/proj/justfile")
	if err != nil || len(jf.Assignments) != 1 || jf.Assignments[0].Name != "x" || jf.Assignments[0].Unit == nil {
		t.Errorf("Load assignments = %+v, %v; want the imported x with its origin", jf.Assignments, err)
	}
}

// With the duplicate settings on, a name the root does not have is still merged.
func TestNewImportedItemsAreMergedWhenDuplicatesAreAllowed(t *testing.T) {
	fsys := sysfake.FakeFS{
		"/proj/justfile": "g := 'a'\n\nr:\n  echo\n\nimport 'b'\n\nset allow-duplicate-recipes\nset allow-duplicate-variables\n",
		"/proj/b":        "x := 'b'\n\ns:\n  echo\n",
	}
	jf, err := Load(fsys, "/proj/justfile")
	if err != nil || len(jf.Assignments) != 2 || len(jf.Recipes) != 2 {
		t.Errorf("Load = %d assignments, %d recipes, %v; want 2 and 2", len(jf.Assignments), len(jf.Recipes), err)
	}
}
