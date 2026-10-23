package runner

import (
	"strings"
	"testing"

	"gitea.com/wimwian/just-go/internal/ast"
)

// modTree is a root with the modules, and recipes, named.
func modTree(modules []string, recipes ...ast.Recipe) *ast.Justfile {
	jf := &ast.Justfile{Recipes: recipes}
	for _, m := range modules {
		jf.Modules = append(jf.Modules, ast.Module{Name: m})
	}
	return jf
}

// usageText is the message of a usage error, or "" for none.
func usageText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func TestResolveRecipeNamesAnExactRecipeAsItIs(t *testing.T) {
	jf := modTree(nil, ast.Recipe{Name: "a"})
	if got, err := resolveRecipe(jf, "a", nil); err != nil || got != "a" {
		t.Errorf("resolveRecipe(a) = %q, %v", got, err)
	}
}

// A recipe a path goes through is not a module.
func TestResolveRecipeWithAPathThroughARecipeIsAnError(t *testing.T) {
	jf := modTree(nil, ast.Recipe{Name: "foo"})
	if err := usageText(errOf(resolveRecipe(jf, "foo::bar", nil))); !strings.Contains(err, "expected submodule at `foo` but found recipe") {
		t.Errorf("error = %q", err)
	}
}

func TestResolveRecipeInAnAbsentModuleSaysSo(t *testing.T) {
	jf := &ast.Justfile{AbsentModules: []string{"a::b"}}
	if err := usageText(errOf(resolveRecipe(jf, "a::b::c", nil))); !strings.Contains(err, "optional module `a::b` is absent") {
		t.Errorf("error = %q", err)
	}
}

// A module name runs the module's default recipe, which is the [default] one of
// its own recipes, else the first; a trailing separator asks for the same.
func TestResolveRecipeOfAModuleRunsItsDefault(t *testing.T) {
	jf := modTree([]string{"foo"},
		ast.Recipe{Name: "foo::a"},
		ast.Recipe{Name: "foo::b", Attributes: []ast.Attribute{{Name: ast.AttrDefault}}},
		ast.Recipe{Name: "foo::bar::c"},
	)
	for _, name := range []string{"foo", "foo::"} {
		if got, err := resolveRecipe(jf, name, nil); err != nil || got != "foo::b" {
			t.Errorf("resolveRecipe(%q) = %q, %v; want foo::b", name, got, err)
		}
	}
	if got, err := resolveRecipe(modTree([]string{"foo"}, ast.Recipe{Name: "foo::a"}), "foo", nil); err != nil || got != "foo::a" {
		t.Errorf("without [default]: %q, %v; want foo::a", got, err)
	}
}

// The default of a module must take no argument, unless one is given.
func TestResolveRecipeOfAModuleNeedingArgumentsIsAnError(t *testing.T) {
	jf := modTree([]string{"foo"}, ast.Recipe{Name: "foo::bar", Params: []ast.Param{{Name: "x"}}})
	if err := usageText(errOf(resolveRecipe(jf, "foo", nil))); !strings.Contains(err, "recipe `foo` cannot be used as default recipe since it requires at least 1 argument") {
		t.Errorf("error = %q", err)
	}
	if got, err := resolveRecipe(jf, "foo", []string{"v"}); err != nil || got != "foo::bar" {
		t.Errorf("with an argument: %q, %v", got, err)
	}
}

func TestResolveRecipeOfNothingIsUnknown(t *testing.T) {
	jf := modTree(nil, ast.Recipe{Name: "a"})
	if err := usageText(errOf(resolveRecipe(jf, "zzz", nil))); !strings.Contains(err, "justfile does not contain recipe `zzz`") {
		t.Errorf("error = %q", err)
	}
	if err := usageText(errOf(resolveRecipe(modTree([]string{"m"}), "m", nil))); !strings.Contains(err, "does not contain recipe `m`") {
		t.Errorf("a module with no recipes: %q", err)
	}
}

// A requested recipe that depends, at any depth, on an absent module is an error
// that names the module, one or several.
func TestRequestedRecipeNeedingAbsentModulesIsAnError(t *testing.T) {
	jf := &ast.Justfile{
		AbsentModules: []string{"foo", "bar"},
		Recipes: []ast.Recipe{
			{Name: "build", Deps: []ast.Dependency{{Name: "foo::setup"}, {Name: "bar::ship"}}},
			{Name: "deploy", Deps: []ast.Dependency{{Name: "build"}}},
		},
	}
	got := absentDependencies(jf, "deploy", map[string]bool{})
	if strings.Join(got, ",") != "bar,foo" {
		t.Errorf("absentDependencies(deploy) = %v; want bar and foo, sorted", got)
	}
	if list := absentList([]string{"foo"}); list != "module `foo`" {
		t.Errorf("absentList one = %q", list)
	}
	if list := absentList([]string{"bar", "foo"}); list != "modules `bar` and `foo`" {
		t.Errorf("absentList two = %q", list)
	}
}

func TestDeclaresModuleFindsDeclaredAndNestedModules(t *testing.T) {
	jf := modTree([]string{"a"}, ast.Recipe{Name: "b::c"})
	if !declaresModule(jf, "a") || !declaresModule(jf, "b") || declaresModule(jf, "c") {
		t.Error("declaresModule did not find a declared module and a module with recipes only")
	}
}

// errOf is the error of a resolveRecipe call, so a test can read it.
func errOf(_ string, err error) error { return err }

// A module recipe runs in the directory of its file, unless it is no-cd; an
// imported recipe runs in the root's.
func TestDirectoryOfAModuleRecipeIsItsFileDirectory(t *testing.T) {
	inv := &invocation{dir: "/root", here: "/here"}
	module := &ast.Justfile{File: "/root/sub/mod.just", IsModule: true}
	inModule := step{recipe: &ast.Recipe{Name: "foo::a", Unit: module}}
	if got := inv.directory(inModule); got != "/root/sub" {
		t.Errorf("module recipe directory = %q; want /root/sub", got)
	}
	noCd := step{recipe: &ast.Recipe{Name: "foo::a", Unit: module, Attributes: []ast.Attribute{{Name: ast.AttrNoCd}}}}
	if got := inv.directory(noCd); got != "/here" {
		t.Errorf("no-cd module recipe directory = %q; want /here", got)
	}
	imported := step{recipe: &ast.Recipe{Name: "a", Unit: &ast.Justfile{File: "/root/imp.just"}}}
	if got := inv.directory(imported); got != "/root" {
		t.Errorf("imported recipe directory = %q; want /root", got)
	}
}

// requested refuses an unknown name, and a requested recipe that needs an absent
// module, before anything runs.
func TestRequestedRefusesUnknownAndAbsentRecipes(t *testing.T) {
	jf := &ast.Justfile{
		AbsentModules: []string{"foo"},
		Recipes: []ast.Recipe{
			{Name: "a"},
			{Name: "build", Deps: []ast.Dependency{{Name: "foo::setup"}}},
		},
	}
	if _, err := requested(Plan{Justfile: jf, Recipes: []string{"zzz"}}); err == nil || !strings.Contains(err.Error(), "does not contain recipe `zzz`") {
		t.Errorf("unknown name: error = %v", err)
	}
	if _, err := requested(Plan{Justfile: jf, Recipes: []string{"build"}}); err == nil || !strings.Contains(err.Error(), "recipe `build` depends on absent module `foo`") {
		t.Errorf("absent module: error = %v", err)
	}
	if calls, err := requested(Plan{Justfile: jf, Recipes: []string{"a"}}); err != nil || len(calls) != 1 || calls[0].Recipe != "a" {
		t.Errorf("a present recipe: %v, %v", calls, err)
	}
}
