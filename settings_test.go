package just

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"gitea.com/wimwian/just-go/internal/sys/sysfake"
)

func TestDefaultListAndFallbackReportTheSettings(t *testing.T) {
	plain := mustParse(t, "foo:\n")
	if plain.DefaultList() || plain.Fallback() {
		t.Error("a justfile without the settings reports them")
	}
	set := mustParse(t, "set default-list\nset fallback := true\nfoo:\n")
	if !set.DefaultList() || !set.Fallback() {
		t.Error("a justfile with the settings does not report them")
	}
	off := mustParse(t, "set default-list := false\nset fallback := false\nfoo:\n")
	if off.DefaultList() || off.Fallback() {
		t.Error("a justfile with the settings off reports them")
	}
}

func TestRunNoCdUsesTheInvocationDirectory(t *testing.T) {
	j := mustParse(t, "set no-cd\nfoo:\n\techo\n")
	proc := &sysfake.FakeProcess{}
	if _, err := j.Run(context.Background(), Options{WorkDir: "/w", InvocationDir: "/w/sub", Exec: proc}); err != nil {
		t.Fatal(err)
	}
	if got := proc.Specs[0].Dir; got != "/w/sub" {
		t.Errorf("Dir = %q, want /w/sub", got)
	}
}

func TestRunWorkingDirectorySettingIsRelativeToWorkDir(t *testing.T) {
	j := mustParse(t, "set working-directory := 'bar'\nfoo:\n\techo\n")
	proc := &sysfake.FakeProcess{}
	if _, err := j.Run(context.Background(), Options{WorkDir: "/w", InvocationDir: "/w/sub", Exec: proc}); err != nil {
		t.Fatal(err)
	}
	if got := proc.Specs[0].Dir; got != "/w/bar" {
		t.Errorf("Dir = %q, want /w/bar", got)
	}
}

func TestRecipesListsTheLastOfDuplicateRecipes(t *testing.T) {
	j := mustParse(t, "set allow-duplicate-recipes\na:\n\techo 1\nb:\na x:\n\techo 2\n")
	var got [][]string
	for _, r := range j.Recipes() {
		got = append(got, append([]string{r.Name}, r.Params...))
	}
	if want := [][]string{{"b"}, {"a", "x"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("Recipes = %v, want %v", got, want)
	}
}

func TestParseReportsSettingErrors(t *testing.T) {
	for src, msg := range map[string]string{
		"set minimum-version := '999.0.0'\n":                   "justfile requires just 999.0.0 or later, but using 1.59.0",
		"set no-cd\nset working-directory := 'x'\n":            "`no-cd` set on line 1 is incompatible with `working-directory`",
		"set tempdir := nope\n":                                "variable `nope` not defined",
		"set quiet\nset quiet\n":                               "setting `quiet` first set on line 1 is redefined on line 2",
		"set guards\nfoo:\n\t-?true\n":                         "the guard `?` and infallible `-` sigils may not be used together",
		"set export := maybe\n":                                "expected keyword `true` or `false` but found identifier `maybe`",
		"set indentation := 'x'\n":                             "indentation must be spaces or tabs",
		"set windows-shell := [nope]\nfoo:\n":                  "variable `nope` not defined",
		"set script-interpreter := ['sh', arch()]\nfoo:\n":     "cannot call functions in const context",
		"set allow-duplicate-recipes := false\na:\na:\n":       "recipe `a` first defined on line 2 is redefined on line 3",
		"set allow-duplicate-variables := false\na:=''\na:=''": "variable `a` has multiple definitions",
	} {
		_, err := Parse("justfile", []byte(src))
		if err == nil || !strings.Contains(err.Error(), msg) {
			t.Errorf("Parse(%q) = %v, want %q", src, err, msg)
		}
	}
}
