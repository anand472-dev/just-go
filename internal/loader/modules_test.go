package loader

import (
	"errors"
	"strings"
	"testing"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/sys/sysfake"
)

// moduleFS is a root justfile that declares a module, with its file.
func moduleFS(root, file string) sysfake.FakeFS {
	return sysfake.FakeFS{"/proj/justfile": root, "/proj/foo.just": file}
}

// recipeList lists the recipe names of a loaded tree.
func recipeList(jf *ast.Justfile) string {
	var out []string
	for _, r := range jf.Recipes {
		out = append(out, r.Name)
	}
	return strings.Join(out, " ")
}

func TestModuleIsMergedUnderItsNameAndItsRecipesRenamed(t *testing.T) {
	jf, err := Load(moduleFS("mod foo\n\nmain: foo::bar\n", "bar:\n  echo\n"), "/proj/justfile")
	if err != nil || recipeList(jf) != "main foo::bar" {
		t.Fatalf("Load = %q, %v; want main and foo::bar", recipeList(jf), err)
	}
	if !jf.Recipes[1].Unit.IsModule || jf.Recipes[0].Unit != nil {
		t.Errorf("origins: the module recipe must point at its module, the root recipe at nothing")
	}
}

// References inside a module are renamed, except to parameters, so the module's
// variables resolve and a variable it does not define is undefined there. Every
// expression form is walked, so each rename is checked by the analyzer.
func TestModuleReferencesAreRenamedInEveryExpressionForm(t *testing.T) {
	file := strings.Join([]string{
		"v := 'x'",
		"w := v + 'y'",
		"c := quote(v)",
		"l := [v, 'z']",
		"k := if v == 'x' { 'a' } else { w }",
		"a := assert(v != '', 'empty')",
		"r p=v: (sub v) && (sub p)",
		"sub arg:",
		"  echo {{ arg }} {{ v }}",
		"",
	}, "\n")
	if _, err := Load(moduleFS("mod foo\n\nset lists\n", file), "/proj/justfile"); err != nil {
		t.Errorf("Load with module expressions = %v; want none", err)
	}
}

// A module that uses a root variable it does not define is undefined there.
func TestModuleCannotSeeTheRootsVariables(t *testing.T) {
	_, err := Load(moduleFS("g := 'a'\n\nmod foo\n", "bar:\n  echo {{ g }}\n"), "/proj/justfile")
	if err == nil || !strings.Contains(err.Error(), "variable `foo::g` not defined") {
		t.Errorf("Load = %v; want the root variable undefined inside the module", err)
	}
}

func TestOptionalAbsentModuleIsRecorded(t *testing.T) {
	jf, err := Load(sysfake.FakeFS{"/proj/justfile": "mod? foo\n\na:\n  echo\n"}, "/proj/justfile")
	if err != nil || len(jf.AbsentModules) != 1 || jf.AbsentModules[0] != "foo" {
		t.Errorf("AbsentModules = %v, %v; want foo", jf.AbsentModules, err)
	}
}

func TestRequiredMissingModuleIsAnErrorAtItsName(t *testing.T) {
	_, err := Load(sysfake.FakeFS{"/proj/justfile": "mod foo\n"}, "/proj/justfile")
	var de *diag.Error
	if !asDiag(err, &de) || de.Msg != "could not find source file for module `foo`" || de.Line != 1 || de.Col != 5 {
		t.Errorf("error = %+v; want the missing-module error at 1:5", err)
	}
}

func TestModuleFileCanBeInAModuleDirectoryOrNamedInAnyCase(t *testing.T) {
	cases := map[string]string{
		"/proj/foo/mod.just":  "mod.just",
		"/proj/foo/JUSTFILE":  "JUSTFILE",
		"/proj/foo/.justfile": ".justfile",
	}
	for path, label := range cases {
		fsys := sysfake.FakeFS{"/proj/justfile": "mod foo\n", path: "bar:\n  echo\n"}
		jf, err := Load(fsys, "/proj/justfile")
		if err != nil || recipeList(jf) != "foo::bar" {
			t.Errorf("%s: Load = %q, %v; want foo::bar", label, recipeList(jf), err)
		}
	}
}

func TestModuleFileWithAPathIsFoundAtThatPath(t *testing.T) {
	fsys := sysfake.FakeFS{"/proj/justfile": "mod foo 'sub/x.just'\n", "/proj/sub/x.just": "bar:\n  echo\n"}
	jf, err := Load(fsys, "/proj/justfile")
	if err != nil || recipeList(jf) != "foo::bar" {
		t.Errorf("Load = %q, %v; want foo::bar from the path", recipeList(jf), err)
	}
	fsys = sysfake.FakeFS{"/proj/justfile": "mod foo 'commands/bar'\n", "/proj/commands/bar/mod.just": "bar:\n  echo\n"}
	if jf, err := Load(fsys, "/proj/justfile"); err != nil || recipeList(jf) != "foo::bar" {
		t.Errorf("a directory path: Load = %q, %v; want foo::bar", recipeList(jf), err)
	}
}

func TestModuleWithTwoCandidatesIsAmbiguous(t *testing.T) {
	fsys := sysfake.FakeFS{"/proj/justfile": "mod foo\n", "/proj/foo.just": "", "/proj/foo/justfile": ""}
	_, err := Load(fsys, "/proj/justfile")
	if err == nil || !strings.Contains(err.Error(), "found multiple source files for module `foo`") {
		t.Errorf("Load = %v; want the ambiguity error", err)
	}
}

func TestModuleThatIncludesItselfIsCircular(t *testing.T) {
	fsys := sysfake.FakeFS{"/proj/justfile": "mod foo\n", "/proj/foo.just": "mod foo\n"}
	_, err := Load(fsys, "/proj/justfile")
	if err == nil || !strings.Contains(err.Error(), "is circular") {
		t.Errorf("Load = %v; want the circular error", err)
	}
}

func TestNestedModulesAreRenamedUnderTheirParent(t *testing.T) {
	fsys := sysfake.FakeFS{
		"/proj/justfile": "mod foo\n",
		"/proj/foo.just": "mod bar\n\nbaz: bar::inner\n",
		"/proj/bar.just": "inner:\n  echo\n",
	}
	jf, err := Load(fsys, "/proj/justfile")
	if err != nil || recipeList(jf) != "foo::baz foo::bar::inner" {
		t.Errorf("Load = %q, %v; want the module recipes then the submodule recipes, renamed", recipeList(jf), err)
	}
}

func TestAbsentNestedModuleKeepsItsPath(t *testing.T) {
	fsys := sysfake.FakeFS{"/proj/justfile": "mod foo\n", "/proj/foo.just": "mod? bar\n"}
	jf, err := Load(fsys, "/proj/justfile")
	if err != nil || len(jf.AbsentModules) != 1 || jf.AbsentModules[0] != "foo::bar" {
		t.Errorf("AbsentModules = %v, %v; want foo::bar", jf.AbsentModules, err)
	}
}

func TestModuleNameDeclaredTwiceIsARedefinition(t *testing.T) {
	_, err := Load(sysfake.FakeFS{"/proj/justfile": "mod foo\nmod foo\n", "/proj/foo.just": ""}, "/proj/justfile")
	var de *diag.Error
	if !asDiag(err, &de) || de.Line != 2 || !strings.Contains(de.Msg, "first defined on line 1 is redefined on line 2") {
		t.Errorf("error = %+v; want a redefinition at line 2", err)
	}
}

func TestModuleAndRecipeWithTheSameNameAreARedefinition(t *testing.T) {
	_, err := Load(sysfake.FakeFS{"/proj/justfile": "mod foo\n\nfoo:\n  echo\n", "/proj/foo.just": ""}, "/proj/justfile")
	var de *diag.Error
	if !asDiag(err, &de) || de.Line != 3 || !strings.Contains(de.Msg, "defined on line 1 is redefined as a recipe on line 3") {
		t.Errorf("error = %+v; want the module redefined as a recipe at line 3", err)
	}
	_, err = Load(sysfake.FakeFS{"/proj/justfile": "foo:\n  echo\n\nmod foo\n", "/proj/foo.just": ""}, "/proj/justfile")
	if !asDiag(err, &de) || de.Line != 4 || !strings.Contains(de.Msg, "is redefined as a module on line 4") {
		t.Errorf("error = %+v; want the recipe redefined as a module at line 4", err)
	}
}

// A dependency that names a module recipe is renamed, with its arguments.
func TestDependenciesOfAModuleRecipeAreRenamed(t *testing.T) {
	fsys := sysfake.FakeFS{"/proj/justfile": "mod foo\n", "/proj/foo.just": "a x:\n  echo\n\nb: (a 'y')\n  echo\n"}
	jf, err := Load(fsys, "/proj/justfile")
	if err != nil || recipeList(jf) != "foo::a foo::b" {
		t.Fatalf("Load = %q, %v", recipeList(jf), err)
	}
	if deps := jf.Recipes[1].Deps; len(deps) != 1 || deps[0].Name != "foo::a" || len(deps[0].Args) != 1 {
		t.Errorf("dependency = %+v; want foo::a with its argument", deps)
	}
}

func TestMissingModuleFileInTheRootAsAnErrorWithItsSource(t *testing.T) {
	_, err := Load(sysfake.FakeFS{"/proj/justfile": "mod foo\n"}, "/proj/justfile")
	var de *diag.Error
	if !asDiag(err, &de) || de.Source == "" {
		t.Errorf("error = %+v; want the source line attached", err)
	}
}

// A module declared by an imported file is loaded, and its own error is reported.
func TestModuleDeclaredInAnImportIsLoadedAndItsErrorReported(t *testing.T) {
	fsys := sysfake.FakeFS{"/proj/justfile": "import 'b'\n", "/proj/b": "mod foo\n"}
	_, err := Load(fsys, "/proj/justfile")
	var de *diag.Error
	if !asDiag(err, &de) || de.Msg != "could not find source file for module `foo`" {
		t.Errorf("error = %+v; want the missing module reported from the import", err)
	}
}

// A module file that cannot be read is an error.
func TestModuleReadErrorIsReported(t *testing.T) {
	boom := errors.New("boom")
	fsys := importFS{FakeFS: sysfake.FakeFS{"/proj/justfile": "mod foo\n", "/proj/foo.just": ""}, readErr: boom}
	if _, err := Load(fsys, "/proj/justfile"); !errors.Is(err, boom) {
		t.Errorf("error = %v; want the read failure", err)
	}
}

// A module file whose own import is missing is an error in that file.
func TestModuleFileImportErrorIsReported(t *testing.T) {
	fsys := sysfake.FakeFS{"/proj/justfile": "mod foo\n", "/proj/foo.just": "import 'nope'\n"}
	_, err := Load(fsys, "/proj/justfile")
	var de *diag.Error
	if !asDiag(err, &de) || de.Msg != "could not find source file for import" || de.File != "/proj/foo.just" {
		t.Errorf("error = %+v; want the missing import in foo.just", err)
	}
}

// A failure to look for a module's file is reported, not taken for absence.
func TestModuleSearchErrorIsReported(t *testing.T) {
	boom := errors.New("boom")
	fsys := importFS{FakeFS: sysfake.FakeFS{"/proj/justfile": "mod foo\n"}, isErr: boom}
	if _, err := Load(fsys, "/proj/justfile"); !errors.Is(err, boom) {
		t.Errorf("error = %v; want the search failure", err)
	}
}

// dirErrFS is a file system whose directories cannot be read.
type dirErrFS struct{ sysfake.FakeFS }

func (dirErrFS) ReadDir(string) ([]string, error) { return nil, errors.New("no listing") }

// A module found by its exact file is still found when its directory cannot be
// listed, since the listing only adds the justfile-named candidates.
func TestModuleIsFoundWhenItsDirectoryCannotBeListed(t *testing.T) {
	fsys := dirErrFS{sysfake.FakeFS{"/proj/justfile": "mod foo\n", "/proj/foo.just": "bar:\n  echo\n"}}
	jf, err := Load(fsys, "/proj/justfile")
	if err != nil || recipeList(jf) != "foo::bar" {
		t.Errorf("Load = %q, %v; want foo::bar", recipeList(jf), err)
	}
}

// A path that cannot be made relative to the directory is kept as it is.
func TestRelativeToKeepsPathsItCannotRelate(t *testing.T) {
	if got := relativeTo("rel", []string{"/abs/x"}); got[0] != "/abs/x" {
		t.Errorf("relativeTo = %v; want the path kept", got)
	}
	if got := relativeTo("/proj", []string{"/proj/foo/mod.just"}); got[0] != "foo/mod.just" {
		t.Errorf("relativeTo = %v; want foo/mod.just", got)
	}
}
