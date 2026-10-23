package completion

import (
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

// catalog is the catalog of a justfile with recipes foo (documented), bar, a
// private _hid, and a grouped baz; with variables x and y and files in the
// directory.
func catalog() Catalog {
	return Catalog{
		Recipes: []Recipe{
			{Name: "foo", Doc: "doc"},
			{Name: "bar"},
			{Name: "_hid", Private: true},
			{Name: "baz", Groups: []string{"tools", "ci"}},
		},
		Variables: []string{"y", "x"},
		Files:     []string{"foo.just", "justfile"},
		Flags:     []string{"dump", "list"},
	}
}

func TestCandidatesInAPositionListRecipesVariablesDotFilesAndFlags(t *testing.T) {
	got := Candidates([]string{"just", ""}, catalog())
	want := []string{"bar", "baz", "foo\tdoc", "x=", "y=", ".", "foo.just", "justfile", "--dump", "--list"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Candidates = %q; want %q", got, want)
	}
}

func TestCandidatesFilterByThePrefixTyped(t *testing.T) {
	if got := Candidates([]string{"just", "b"}, catalog()); !reflect.DeepEqual(got, []string{"bar", "baz"}) {
		t.Errorf("Candidates(b) = %q; want bar and baz only", got)
	}
	if got := Candidates([]string{"just", "--d"}, catalog()); !reflect.DeepEqual(got, []string{"--dump"}) {
		t.Errorf("Candidates(--d) = %q; want --dump", got)
	}
}

func TestPrivateRecipesAreNotOffered(t *testing.T) {
	if got := Candidates([]string{"just", "_"}, catalog()); len(got) != 0 {
		t.Errorf("Candidates(_) = %q; want none", got)
	}
}

func TestFlagValueNamesGiveTheirOwnCandidates(t *testing.T) {
	c := catalog()
	for _, tc := range []struct {
		words []string
		want  []string
	}{
		{[]string{"just", "--set", ""}, []string{"x", "y"}},
		{[]string{"just", "--set", "x"}, []string{"x"}},
		{[]string{"just", "--show", ""}, []string{"bar", "baz", "foo\tdoc"}},
		{[]string{"just", "--group", ""}, []string{"ci", "tools"}},
		{[]string{"just", "--group", "t"}, []string{"tools"}},
	} {
		if got := Candidates(tc.words, c); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("Candidates(%q) = %q; want %q", tc.words, got, tc.want)
		}
	}
}

// --usage takes recipes and then any positional word, so both lists are offered.
func TestUsageOffersRecipesThenPositionalCandidates(t *testing.T) {
	got := Candidates([]string{"just", "--usage", ""}, catalog())
	want := []string{"bar", "baz", "foo\tdoc", "bar", "baz", "foo\tdoc", "x=", "y=", ".", "foo.just", "justfile", "--dump", "--list"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Candidates(--usage) = %q; want %q", got, want)
	}
}

func TestCandidatesOfNoWordsIsEmptyAndDoesNotPanic(t *testing.T) {
	if got := Candidates(nil, Catalog{}); len(got) == 0 {
		t.Errorf("Candidates(nil) = %q; want the dot at least", got)
	}
}

func TestScriptsExistForTheSpecShells(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "PowerShell"} {
		script, ok := Script(shell)
		if !ok || !strings.Contains(script, "JUST_COMPLETE") {
			t.Errorf("Script(%q) = %q, %v; want a script that sets JUST_COMPLETE", shell, script, ok)
		}
	}
}

func TestScriptsAreRefusedForOtherShells(t *testing.T) {
	for _, shell := range []string{"elvish", "nushell", "fish", ""} {
		if script, ok := Script(shell); ok || script != "" {
			t.Errorf("Script(%q) = %q, %v; want none", shell, script, ok)
		}
	}
}

// The scripts must be accepted by their shells' syntax checks, where the shell
// is installed.
func TestScriptsPassTheirShellsSyntaxCheck(t *testing.T) {
	for _, tc := range []struct {
		shell string
		argv  []string
	}{
		{"bash", []string{"bash", "-n"}},
		{"zsh", []string{"zsh", "-n"}},
	} {
		bin, err := exec.LookPath(tc.argv[0])
		if err != nil {
			t.Skipf("%s is not installed", tc.argv[0])
		}
		script, _ := Script(tc.shell)
		cmd := exec.Command(bin, tc.argv[1:]...)
		cmd.Stdin = strings.NewReader(script)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("%s -n rejected the %s script: %v: %s", tc.argv[0], tc.shell, err, out)
		}
	}
}

func TestFlagValueIsTheLastOccurrence(t *testing.T) {
	words := []string{"just", "--set", "a", "--set", "b", "x"}
	if v, ok := FlagValue(words, "set"); !ok || v != "b" {
		t.Errorf("FlagValue(set) = %q, %v; want b", v, ok)
	}
	if _, ok := FlagValue([]string{"just", "--set"}, "set"); ok {
		t.Error("FlagValue of a flag with no word after it reports one")
	}
	if _, ok := FlagValue(words, "justfile"); ok {
		t.Error("FlagValue of an absent flag reports one")
	}
}
