package main

import (
	"testing"

	just "gitea.com/wimwian/just-go"
	"gitea.com/wimwian/just-go/internal/argspec"
)

// The help a recipe prints lists its arguments and options, each with its help, its
// default and its patterns, as upstream lays them out.
func TestHelpTextLayout(t *testing.T) {
	for _, c := range []struct {
		name string
		info just.RecipeInfo
		want string
	}{
		{"an option with help, beside an argument",
			just.RecipeInfo{Name: "foo", Params: []string{"bar", "baz"}, Kinds: []string{"", ""}, Defaults: []string{"", ""},
				Specs: []argspec.Spec{{Param: "bar", Long: "bar", Help: "hello"}}},
			"Usage: just foo [OPTIONS] baz\n\nArguments:\n  baz\n\nOptions:\n      --bar bar hello\n"},
		{"a short option that takes a fixed value",
			just.RecipeInfo{Name: "foo", Params: []string{"bar"}, Kinds: []string{""}, Defaults: []string{""},
				Specs: []argspec.Spec{{Param: "bar", Short: "b", HasValue: true}}},
			"Usage: just foo [OPTIONS]\n\nOptions:\n  -b\n"},
		{"a long option with a default, and a short and a long name",
			just.RecipeInfo{Name: "foo", Params: []string{"bar"}, Kinds: []string{""}, Defaults: []string{"'baz'"},
				Specs: []argspec.Spec{{Param: "bar", Long: "bar", Short: "b", Patterns: []string{"B.R", "x"}}}},
			"Usage: just foo [OPTIONS]\n\nOptions:\n  -b, --bar bar [default: 'baz'] [pattern: 'B.R' | 'x']\n"},
		{"two arguments, one to a line",
			just.RecipeInfo{Name: "foo", Params: []string{"bar", "baz"}, Kinds: []string{"", ""}, Defaults: []string{"", ""}},
			"Usage: just foo bar baz\n\nArguments:\n  bar\n  baz\n"},
		{"an argument with a default",
			just.RecipeInfo{Name: "foo", Params: []string{"bar"}, Kinds: []string{""}, Defaults: []string{"'x'"}},
			"Usage: just foo [bar]\n\nArguments:\n  [bar] [default: 'x']\n"},
		{"a variadic argument",
			just.RecipeInfo{Name: "foo", Params: []string{"bar"}, Kinds: []string{"+"}, Defaults: []string{""}},
			"Usage: just foo bar...\n\nArguments:\n  bar...\n"},
		{"a recipe with neither",
			just.RecipeInfo{Name: "foo", Params: nil, Kinds: nil, Defaults: nil},
			"Usage: just foo\n"},
	} {
		if got := helpText(c.info); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}

// --help prints the help of the recipe it names, and runs nothing.
func TestRunPrintsTheHelpOfTheRecipe(t *testing.T) {
	out := runFake(t, map[string]string{"/w/justfile": "[arg('bar', long, help='hello')]\nfoo bar baz:\n  echo {{baz}}\n"}, nil, "foo", "--help")
	want := "Usage: just foo [OPTIONS] baz\n\nArguments:\n  baz\n\nOptions:\n      --bar bar hello\n"
	if out.code != 0 || out.stdout != want || len(out.proc.Specs) != 0 {
		t.Errorf("run = %d, %q, ran %d, stderr %q; want 0, %q, nothing run", out.code, out.stdout, len(out.proc.Specs), out.stderr, want)
	}
}

// The first recipe that asks for help ends the words: the words after it are not
// read at all, not even as recipes.
func TestSelectRecipesStopsAtHelp(t *testing.T) {
	infos := []just.RecipeInfo{
		{Name: "foo", Params: []string{"bar"}, Kinds: []string{""}, Defaults: []string{"'baz'"},
			Specs: []argspec.Spec{{Param: "bar", Long: "bar"}}},
		{Name: "qux"},
	}
	calls, err := selectRecipes(infos, []string{"foo", "-h", "qux", "--bogus"})
	if err != nil || len(calls) != 1 || !calls[0].Help || calls[0].Recipe != "foo" {
		t.Errorf("selectRecipes = %+v, %v", calls, err)
	}
	info, ok := helpRequested(infos, calls)
	if !ok || info.Name != "foo" {
		t.Errorf("helpRequested = %+v, %v", info, ok)
	}
}

// A recipe's signature in the list shows [OPTIONS] for its options, and leaves the
// options out of the positional arguments it shows.
func TestSignatureCollapsesOptions(t *testing.T) {
	info := just.RecipeInfo{Name: "recipe", Params: []string{"foo", "qux", "bar", "baz"},
		Kinds: []string{"", "", "", ""}, Defaults: []string{"'x'", "", "'y'", "'z'"},
		Specs: []argspec.Spec{{Param: "foo", Long: "foo"}, {Param: "bar", Short: "b"}}}
	if got, want := signature(info), "recipe [OPTIONS] qux baz='z'"; got != want {
		t.Errorf("signature = %q; want %q", got, want)
	}
}
