package just

import (
	"errors"
	"reflect"
	"testing"

	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/sys/sysfake"
)

// loadFiles loads the justfile /w/justfile from files, which may hold modules.
func loadFiles(t *testing.T, files sysfake.FakeFS) *Justfile {
	t.Helper()
	j, err := Load(Options{Justfile: "/w/justfile", FS: files})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return j
}

func TestShowPrintsTheRecipeAndJoinsSpacedPaths(t *testing.T) {
	j := loadFiles(t, sysfake.FakeFS{"/w/justfile": "# doc\nfoo:\n  echo\nmod bar\n", "/w/bar.just": "baz:\n  echo\n"})
	if got, err := j.Show("foo", "  "); err != nil || got != "# doc\nfoo:\n  echo\n" {
		t.Errorf("Show(foo) = %q, %v", got, err)
	}
	// A recipe in a module is written under its own name, and `bar baz` names it.
	if got, err := j.Show("bar baz", "  "); err != nil || got != "baz:\n  echo\n" {
		t.Errorf("Show(bar baz) = %q, %v; want the module's recipe under its own name", got, err)
	}
	if got, err := j.Show("bar::baz", "  "); err != nil || got != "baz:\n  echo\n" {
		t.Errorf("Show(bar::baz) = %q, %v", got, err)
	}
}

func TestShowRefusesAnInvalidPath(t *testing.T) {
	j := loadFiles(t, sysfake.FakeFS{"/w/justfile": "foo:\n"})
	_, err := j.Show("$hello", "  ")
	var d *diag.Error
	if err == nil || !errors.As(err, &d) || d.Msg != "invalid module path `$hello`" {
		t.Errorf("Show($hello) = %v; want the invalid-path error", err)
	}
}

func TestShowSuggestsANearRecipeForAMissingOne(t *testing.T) {
	j := loadFiles(t, sysfake.FakeFS{"/w/justfile": "hello:\na:\n"})
	_, err := j.Show("hell", "  ")
	var d *diag.Error
	if !errors.As(err, &d) || d.Msg != "justfile does not contain recipe `hell`\nDid you mean `hello`?" {
		t.Errorf("Show(hell) = %v; want the suggestion", err)
	}
}

func TestShowAndUsageRefuseAnAbsentOptionalModule(t *testing.T) {
	j := loadFiles(t, sysfake.FakeFS{"/w/justfile": "mod? foo\n"})
	for _, name := range []string{"foo", "foo::bar"} {
		_, err := j.Show(name, "  ")
		var d *diag.Error
		if !errors.As(err, &d) || d.Msg != "optional module `foo` is absent" {
			t.Errorf("Show(%s) = %v; want the absent-module error", name, err)
		}
	}
	if _, err := j.Usage("foo"); err == nil {
		t.Error("Usage of an absent module's recipe succeeded")
	}
}

func TestUsageNamesTheArgumentsOfARecipe(t *testing.T) {
	j := mustParse(t, "foo bar baz='x' +rest:\n  echo\nbare:\n  echo\n")
	got, err := j.Usage("foo")
	if err != nil || got != "Usage: just foo bar [baz] rest...\n\nArguments:\n  bar\n  baz\n  rest\n" {
		t.Errorf("Usage(foo) = %q, %v", got, err)
	}
	if got, err := j.Usage("bare"); err != nil || got != "Usage: just bare\n" {
		t.Errorf("Usage(bare) = %q, %v; want no Arguments section", got, err)
	}
}

func TestSummaryListsEachLevelsRecipesBeforeItsModules(t *testing.T) {
	j := loadFiles(t, sysfake.FakeFS{
		"/w/justfile": "top:\nmod bar\nmod foo\n",
		"/w/bar.just": "bar:\n",
		"/w/foo.just": "foo:\n",
	})
	want := []string{"top", "bar::bar", "foo::foo"}
	if got := j.Summary(false); !reflect.DeepEqual(got, want) {
		t.Errorf("Summary(unsorted) = %q; want %q", got, want)
	}
	if got := j.Summary(true); !reflect.DeepEqual(got, want) {
		t.Errorf("Summary(sorted) = %q; want %q", got, want)
	}
}

func TestSummaryOrderSortsEachLevelOrKeepsTheSource(t *testing.T) {
	names := []string{"b", "a::z", "a::y", "a::b::c", "d"}
	if got := summaryOrder(names, true); !reflect.DeepEqual(got, []string{"b", "d", "a::y", "a::z", "a::b::c"}) {
		t.Errorf("summaryOrder(sorted) = %q", got)
	}
	if got := summaryOrder(names, false); !reflect.DeepEqual(got, []string{"b", "d", "a::z", "a::y", "a::b::c"}) {
		t.Errorf("summaryOrder(unsorted) = %q", got)
	}
}

func TestIsIdentAcceptsOnlyNamesAJustfileMayGive(t *testing.T) {
	if !isIdent("a-b_1") || isIdent("1a") || isIdent("") || isIdent("a b") {
		t.Error("isIdent does not accept only names a justfile may give")
	}
}

func TestValidPathNeedsIdentifiersBetweenSeparators(t *testing.T) {
	if validPath("a::") || validPath("::a") || !validPath("a::b") {
		t.Error("validPath accepts empty segments or rejects a valid path")
	}
}

func TestLastSegmentIsTheLastPart(t *testing.T) {
	if lastSegment("a::b::c") != "c" || lastSegment("c") != "c" {
		t.Error("lastSegment does not return the last part of a path")
	}
}

func TestAbsentModuleOfNamesTheModuleItself(t *testing.T) {
	jf := mustParse(t, "foo:\n")
	jf.tree.AbsentModules = []string{"foo"}
	if got := absentModuleOf(jf.tree, "foo"); got != "foo" {
		t.Errorf("absentModuleOf(foo) = %q; want foo", got)
	}
	if got := absentModuleOf(jf.tree, "bar"); got != "" {
		t.Errorf("absentModuleOf(bar) = %q; want none", got)
	}
}
