package main

import (
	"bytes"
	"errors"
	"io/fs"
	"reflect"
	"strings"
	"testing"

	just "gitea.com/wimwian/just-go"
	"gitea.com/wimwian/just-go/internal/sys"
	"gitea.com/wimwian/just-go/internal/sys/sysfake"
)

func TestChoosableIsThePublicArgumentlessRecipesSorted(t *testing.T) {
	infos := []just.RecipeInfo{
		{Name: "zed", Groups: []string{"g"}},
		{Name: "_hidden", Private: true},
		{Name: "needs", Params: []string{"x"}, Kinds: []string{""}, Defaults: []string{""}},
		{Name: "opt", Params: []string{"x", "rest"}, Kinds: []string{"", "*"}, Defaults: []string{"'d'", ""}},
		{Name: "sub::inner"},
		{Name: "abc"},
	}
	if got := choosable(infos, nil); !reflect.DeepEqual(got, []string{"abc", "opt", "sub inner", "zed"}) {
		t.Errorf("choosable = %q", got)
	}
	if got := choosable(infos, []string{"g"}); !reflect.DeepEqual(got, []string{"zed"}) {
		t.Errorf("choosable limited to group g = %q", got)
	}
}

func TestNeedsArgumentAcceptsDefaultsAndStars(t *testing.T) {
	cases := map[string]just.RecipeInfo{
		"none":           {Name: "a"},
		"defaulted":      {Name: "a", Params: []string{"x"}, Kinds: []string{""}, Defaults: []string{"'x'"}},
		"star":           {Name: "a", Params: []string{"x"}, Kinds: []string{"*"}, Defaults: []string{""}},
		"plus required":  {Name: "a", Params: []string{"x"}, Kinds: []string{"+"}, Defaults: []string{""}},
		"single missing": {Name: "a", Params: []string{"x"}, Kinds: []string{""}, Defaults: []string{""}},
	}
	want := map[string]bool{"none": false, "defaulted": false, "star": false, "plus required": true, "single missing": true}
	for name, info := range cases {
		if got := needsArgument(info); got != want[name] {
			t.Errorf("needsArgument(%s) = %v; want %v", name, got, want[name])
		}
	}
}

func TestInAnyGroupMatchesOneOfEither(t *testing.T) {
	if !inAnyGroup([]string{"a", "b"}, []string{"c", "b"}) || inAnyGroup([]string{"a"}, []string{"c"}) {
		t.Error("inAnyGroup does not match one name in common")
	}
}

func TestCallsOfReadsCandidatesAndWordLists(t *testing.T) {
	got := callsOf("sub bar\n\nfoo bar\n", []string{"sub bar", "foo"})
	want := []just.Call{{Recipe: "sub::bar"}, {Recipe: "foo"}, {Recipe: "bar"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("callsOf = %+v; want %+v", got, want)
	}
}

func TestDefaultChooserUsesTheShellOrSlash(t *testing.T) {
	if got := defaultChooser([]string{"SHELL=/bin/zsh"}); !strings.HasPrefix(got, "/bin/zsh -cu fzf") {
		t.Errorf("defaultChooser with SHELL = %q", got)
	}
	if got := defaultChooser(nil); !strings.HasPrefix(got, "/ -cu fzf") {
		t.Errorf("defaultChooser without SHELL = %q", got)
	}
}

func TestChooserOfPrefersTheFlagThenTheEnvironment(t *testing.T) {
	if got := chooserOf(config{chooser: "flag"}, []string{"JUST_CHOOSER=env"}); got != "flag" {
		t.Errorf("chooserOf with the flag = %q", got)
	}
	if got := chooserOf(config{}, []string{"JUST_CHOOSER=env"}); got != "env" {
		t.Errorf("chooserOf with JUST_CHOOSER = %q", got)
	}
	if got := chooserOf(config{}, []string{"JUST_CHOOSER="}); !strings.Contains(got, "fzf") {
		t.Errorf("chooserOf with an empty JUST_CHOOSER = %q; want the default", got)
	}
}

func TestChooserFailedIsFalseForACleanChooser(t *testing.T) {
	var stderr bytes.Buffer
	if status, failed := chooserFailed("c", 0, nil, false, &stderr); failed || status != 0 {
		t.Errorf("a clean chooser = %d, %v; want no failure", status, failed)
	}
}

func TestChooserFailedReportsASignalWithItsStatus(t *testing.T) {
	var stderr bytes.Buffer
	if status, failed := chooserFailed("c", 0, &sys.SignalError{Signal: 15}, false, &stderr); !failed || status != 143 {
		t.Errorf("a signalled chooser = %d, %v; want 143", status, failed)
	}
	if !strings.Contains(stderr.String(), "signal: 15 (SIGTERM)") {
		t.Errorf("stderr = %q; want the signal's name", stderr.String())
	}
}

func TestChooserFailedReportsAnExitCode(t *testing.T) {
	var stderr bytes.Buffer
	if status, failed := chooserFailed("c", 3, nil, false, &stderr); !failed || status != 1 || !strings.Contains(stderr.String(), "exit status 3") {
		t.Errorf("a failing chooser = %d, %v, %q", status, failed, stderr.String())
	}
}

func TestChooserFailedReportsAChooserThatWillNotStart(t *testing.T) {
	var stderr bytes.Buffer
	if status, failed := chooserFailed("c", 0, errors.New("boom"), false, &stderr); !failed || status != 1 || !strings.Contains(stderr.String(), "boom") {
		t.Errorf("a chooser that will not start = %d, %v, %q", status, failed, stderr.String())
	}
}

func TestRunChooseRunsASelectedRecipeThatDoesNotExist(t *testing.T) {
	proc := &sysfake.FakeProcess{Outputs: []string{"nope\n"}}
	var stdout, stderr bytes.Buffer
	code := run([]string{"--choose"}, nil, "/w", strings.NewReader(""), &stdout, &stderr, sysfake.FakeFS{"/w/justfile": "foo:\n"}, proc)
	if code != 1 || !strings.Contains(stderr.String(), "does not contain recipe `nope`") {
		t.Errorf("run = %d, %q; want the missing-recipe error", code, stderr.String())
	}
}

// failingFS is a file system whose writes fail, for --init.
type failingFS struct{ sysfake.FakeFS }

func (failingFS) WriteFile(string, []byte, fs.FileMode) error { return errors.New("read-only") }

func TestRunInitReportsAWriteThatFails(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--init"}, nil, "/w", strings.NewReader(""), &stdout, &stderr, failingFS{sysfake.FakeFS{}}, &sysfake.FakeProcess{})
	if code != 1 || !strings.Contains(stderr.String(), "failed to write justfile") {
		t.Errorf("run = %d, %q; want the write error", code, stderr.String())
	}
}

func TestRunChooseWithNoChoosableRecipeExitsOne(t *testing.T) {
	got := runFake(t, map[string]string{"/w/justfile": "a x:\n"}, nil, "--choose")
	if got.code != 1 || got.stderr != "error: justfile contains no choosable recipes\n" {
		t.Errorf("run = %d, %q", got.code, got.stderr)
	}
}

func TestRunChooseRunsWhatTheChooserSelects(t *testing.T) {
	proc := &sysfake.FakeProcess{Outputs: []string{"bar\n"}}
	var stdout, stderr bytes.Buffer
	code := run([]string{"--choose", "--chooser", "head -n1"}, []string{"A=1"}, "/w", strings.NewReader(""), &stdout, &stderr, sysfake.FakeFS{"/w/justfile": "foo:\n  echo foo\nbar:\n  echo bar\n"}, proc)
	if code != 0 || len(proc.Specs) != 2 || proc.Specs[0].Argv[0] != "sh" {
		t.Errorf("run = %d, %q, %q with %d processes", code, stdout.String(), stderr.String(), len(proc.Specs))
	}
}

func TestRunChooseReportsAFailingChooser(t *testing.T) {
	proc := &sysfake.FakeProcess{Codes: []int{3}}
	var stdout, stderr bytes.Buffer
	code := run([]string{"--choose"}, nil, "/w", strings.NewReader(""), &stdout, &stderr, sysfake.FakeFS{"/w/justfile": "foo:\n"}, proc)
	if code != 1 || !strings.Contains(stderr.String(), "invocation failed") {
		t.Errorf("run = %d, %q; want status 1 and the invocation error", code, stderr.String())
	}
}

func TestRunEditOpensTheJustfileInTheEditor(t *testing.T) {
	proc := &sysfake.FakeProcess{}
	var stdout, stderr bytes.Buffer
	code := run([]string{"--edit"}, []string{"VISUAL=vi"}, "/w", strings.NewReader(""), &stdout, &stderr, sysfake.FakeFS{"/w/justfile": "foo:\n"}, proc)
	if code != 0 || len(proc.Specs) != 1 || !reflect.DeepEqual(proc.Specs[0].Argv, []string{"vi", "/w/justfile"}) {
		t.Errorf("run = %d with specs %+v", code, proc.Specs)
	}
}

func TestRunEditWithNoEditorExitsOne(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--edit"}, nil, "/w", strings.NewReader(""), &stdout, &stderr, sysfake.FakeFS{"/w/justfile": "foo:\n"}, &sysfake.FakeProcess{})
	if code != 1 || !strings.Contains(stderr.String(), "no editor") {
		t.Errorf("run = %d, %q", code, stderr.String())
	}
}

func TestRunEditReportsAnEditorThatWillNotStart(t *testing.T) {
	proc := &sysfake.FakeProcess{Err: errors.New("no such file")}
	var stdout, stderr bytes.Buffer
	code := run([]string{"--edit"}, []string{"EDITOR=nope"}, "/w", strings.NewReader(""), &stdout, &stderr, sysfake.FakeFS{"/w/justfile": "foo:\n"}, proc)
	if code != 1 || !strings.Contains(stderr.String(), "could not run editor") {
		t.Errorf("run = %d, %q", code, stderr.String())
	}
}

func TestRunEditWithNoJustfileExitsOne(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--edit"}, []string{"VISUAL=vi"}, "/w", strings.NewReader(""), &stdout, &stderr, sysfake.FakeFS{}, &sysfake.FakeProcess{})
	if code != 1 {
		t.Errorf("run = %d, %q; want status 1", code, stderr.String())
	}
}

func TestRunInitWritesTheStarterAndRefusesAnExistingOne(t *testing.T) {
	fsys := sysfake.FakeFS{}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--init"}, nil, "/w", strings.NewReader(""), &stdout, &stderr, fsys, &sysfake.FakeProcess{}); code != 0 || fsys["/w/justfile"] != starterJustfile {
		t.Errorf("--init = %d; file %q", code, fsys["/w/justfile"])
	}
	stderr.Reset()
	if code := run([]string{"--init"}, nil, "/w", strings.NewReader(""), &stdout, &stderr, fsys, &sysfake.FakeProcess{}); code != 1 || !strings.Contains(stderr.String(), "already exists") {
		t.Errorf("--init again = %d, %q", code, stderr.String())
	}
}

func TestRunInitQuietSaysNothingWhenTheFileExists(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--init", "--quiet"}, nil, "/w", strings.NewReader(""), &stdout, &stderr, sysfake.FakeFS{"/w/justfile": "x:\n"}, &sysfake.FakeProcess{})
	if code != 1 || stderr.Len() != 0 {
		t.Errorf("run = %d, %q; want status 1 and no message", code, stderr.String())
	}
}

func TestRunInitQuietSaysNothingWhenItWrites(t *testing.T) {
	fsys := sysfake.FakeFS{}
	var stdout, stderr bytes.Buffer
	code := run([]string{"--init", "--quiet"}, nil, "/w", strings.NewReader(""), &stdout, &stderr, fsys, &sysfake.FakeProcess{})
	if code != 0 || stderr.Len() != 0 || fsys["/w/justfile"] != starterJustfile {
		t.Errorf("run = %d, %q; want a quiet write", code, stderr.String())
	}
}

func TestRunInitUsesTheNameGiven(t *testing.T) {
	fsys := sysfake.FakeFS{}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--init", "--justfile-name", "foo"}, nil, "/w", strings.NewReader(""), &stdout, &stderr, fsys, &sysfake.FakeProcess{}); code != 0 || fsys["/w/foo"] != starterJustfile {
		t.Errorf("run = %d; want the file named foo", code)
	}
}
