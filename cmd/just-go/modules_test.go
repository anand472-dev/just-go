package main

import (
	"strings"
	"testing"

	just "gitea.com/wimwian/just-go"
)

// A word that names a module takes the arguments of the module's first recipe
// for grouping; a trailing separator names the same module.
func TestSelectRecipesGroupsArgumentsOfAModuleWord(t *testing.T) {
	infos := []just.RecipeInfo{{Name: "foo::bar", Params: []string{"x"}}, {Name: "b"}}
	for _, word := range []string{"foo", "foo::"} {
		got, err := selectRecipes(infos, []string{word, "1", "b"})
		if err != nil || len(got) != 2 || got[0].Recipe != word || len(got[0].Args) != 1 || got[0].Args[0] != "1" || got[1].Recipe != "b" {
			t.Errorf("selectRecipes(%q, 1, b) = %+v, %v", word, got, err)
		}
	}
}

// A path through a recipe is the submodule error, which names that recipe.
func TestSelectRecipesPathThroughARecipeIsSubmoduleError(t *testing.T) {
	infos := []just.RecipeInfo{{Name: "foo"}}
	_, err := selectRecipes(infos, []string{"foo::baz"})
	if err == nil || !strings.Contains(err.Error(), "expected submodule at `foo` but found recipe") {
		t.Errorf("error = %v", err)
	}
}

// A path through a name that is neither a module nor a recipe is the unknown
// recipe error, as before.
func TestSelectRecipesPathThroughNothingIsUnknown(t *testing.T) {
	_, err := selectRecipes([]just.RecipeInfo{{Name: "a"}}, []string{"zz::q"})
	if err == nil || !strings.Contains(err.Error(), "does not contain recipe `zz::q`") {
		t.Errorf("error = %v", err)
	}
}

func TestHasRecipeAnswersForNames(t *testing.T) {
	if !hasRecipe(map[string]int{"a": 0}, "a") || hasRecipe(map[string]int{"a": 0}, "b") {
		t.Error("hasRecipe did not answer for the names in params")
	}
}

// --allow-outside-project-paths is a switch that lifts the strict path rule.
func TestAllowOutsideProjectPathsIsASwitch(t *testing.T) {
	cfg, err := parseArgs([]string{"--allow-outside-project-paths", "a"})
	if err != nil || !cfg.allowOutside || len(cfg.words) != 1 {
		t.Errorf("parseArgs = %+v, %v; want the switch set and a as the word", cfg, err)
	}
	if cfg, _ := parseArgs([]string{"a"}); cfg.allowOutside {
		t.Error("the switch is set without the flag")
	}
}
