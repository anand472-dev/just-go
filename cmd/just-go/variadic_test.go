package main

import (
	"reflect"
	"testing"

	just "gitea.com/wimwian/just-go"
	"gitea.com/wimwian/just-go/internal/argv"
)

// A plain recipe takes as many words as it has parameters, and a variadic one
// takes every word left.
func TestArgvQuotaOfAVariadicRecipeIsUnbounded(t *testing.T) {
	plain := just.RecipeInfo{Name: "a", Params: []string{"x", "y"}, Kinds: []string{"", ""}}
	if got, err := argv.Take(argvOf(plain), []string{"1", "2", "3"}); err != nil || got.Used != 2 {
		t.Errorf("plain Take used %d, %v; want 2", got.Used, err)
	}
	star := just.RecipeInfo{Name: "b", Params: []string{"x", "y"}, Kinds: []string{"", "*"}}
	if got, err := argv.Take(argvOf(star), []string{"1", "2", "3"}); err != nil || got.Used != 3 {
		t.Errorf("star Take used %d, %v; want 3", got.Used, err)
	}
	if got, err := argv.Take(argvOf(just.RecipeInfo{Name: "c"}), []string{"1"}); err != nil || got.Used != 0 {
		t.Errorf("no-parameter Take used %d, %v; want 0", got.Used, err)
	}
}

// A variadic recipe takes every word after its name, as upstream groups them.
func TestSelectRecipesGivesAVariadicRecipeEveryWordLeft(t *testing.T) {
	infos := []just.RecipeInfo{
		{Name: "bar", Params: []string{"n"}, Kinds: []string{""}},
		{Name: "baz", Params: []string{"z"}, Kinds: []string{"+"}},
	}
	calls, err := selectRecipes(infos, []string{"bar", "0", "baz", "3", "4", "5"})
	want := []just.Call{{Recipe: "bar", Args: []string{"0"}}, {Recipe: "baz", Args: []string{"3", "4", "5"}}}
	if err != nil || !reflect.DeepEqual(calls, want) {
		t.Errorf("selectRecipes = %+v, %v; want %+v", calls, err, want)
	}
}

func TestSignatureShowsTheVariadicMarkBeforeTheName(t *testing.T) {
	info := just.RecipeInfo{Name: "a", Params: []string{"B", "D"}, Kinds: []string{"", "+"}, Defaults: []string{"", "'hello'"}}
	if got := signature(info); got != "a B +D='hello'" {
		t.Errorf("signature = %q; want a B +D='hello'", got)
	}
}
