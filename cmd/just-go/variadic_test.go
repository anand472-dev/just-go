package main

import (
	"reflect"
	"testing"

	just "gitea.com/wimwian/just-go"
)

func TestArgCountOfAVariadicRecipeIsUnbounded(t *testing.T) {
	plain := just.RecipeInfo{Name: "a", Params: []string{"x", "y"}, Kinds: []string{"", ""}}
	if got := argCount(plain); got != 2 {
		t.Errorf("argCount(plain) = %d; want 2", got)
	}
	star := just.RecipeInfo{Name: "b", Params: []string{"x", "y"}, Kinds: []string{"", "*"}}
	if got := argCount(star); got != -1 {
		t.Errorf("argCount(star) = %d; want -1", got)
	}
	if got := argCount(just.RecipeInfo{Name: "c"}); got != 0 {
		t.Errorf("argCount(no parameters) = %d; want 0", got)
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
