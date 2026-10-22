package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	just "gitea.com/wimwian/just-go"
	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/sys"
	"gitea.com/wimwian/just-go/internal/sys/sysfake"
)

// outcome is what one call of run produced.
type outcome struct {
	code   int
	stdout string
	stderr string
	proc   *sysfake.FakeProcess
}

// runFake calls run in directory /w over an in-memory filesystem holding
// files (keyed by path) and a fake process runner that answers with codes.
func runFake(t testing.TB, files map[string]string, codes []int, args ...string) outcome {
	t.Helper()
	var stdout, stderr bytes.Buffer
	proc := &sysfake.FakeProcess{Codes: codes}
	code := run(args, []string{"A=1"}, "/w", strings.NewReader(""), &stdout, &stderr, sysfake.FakeFS(files), proc)
	return outcome{code: code, stdout: stdout.String(), stderr: stderr.String(), proc: proc}
}

func TestRunReturnsExitCodeFromRecipe(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "justfile"), []byte("x:\n  exit 4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run(nil, nil, dir, strings.NewReader(""), &stdout, &stderr, sys.OSFS{}, sys.OSProcess{})
	if code != 4 {
		t.Errorf("run = %d, want 4 (stderr %q)", code, stderr.String())
	}
	want := "exit 4\nerror: recipe `x` failed on line 2 with exit code 4\n"
	if got := stderr.String(); got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
}

func TestRunListMatchesUpstreamFormat(t *testing.T) {
	// Layout from the corpus cases misc::list_alignment#1 and
	// misc::list_alignment_long#1: sorted by name, one "#" column, private
	// recipes hidden, and a wide signature carrying its doc above it.
	src := "\n# this does a thing\nhello a b c:\n  echo hi\n\n# something else\na Z:\n\n# this recipe will not appear\n_private-recipe:\n\nplain:\n"
	got := runFake(t, map[string]string{"/w/justfile": src}, nil, "--list")
	want := "Available recipes:\n    a Z         # something else\n    hello a b c # this does a thing\n    plain\n"
	if got.code != 0 || got.stdout != want || got.stderr != "" {
		t.Errorf("run --list = %d, %q, %q; want 0, %q, empty", got.code, got.stdout, got.stderr, want)
	}
	if len(got.proc.Specs) != 0 {
		t.Errorf("--list ran %d commands, want 0", len(got.proc.Specs))
	}
}

func TestRunNoJustfileExitsOne(t *testing.T) {
	got := runFake(t, map[string]string{}, nil)
	if got.code != 1 || got.stderr != "error: no justfile found\n" || got.stdout != "" {
		t.Errorf("run = %d, %q, %q; want 1, error: no justfile found", got.code, got.stdout, got.stderr)
	}
}

func TestRunUnknownRecipeExitsOne(t *testing.T) {
	got := runFake(t, map[string]string{"/w/justfile": "hello:\n"}, nil, "foo", "bar")
	want := "error: justfile does not contain recipe `foo`\n"
	if got.code != 1 || got.stderr != want {
		t.Errorf("run = %d, %q; want 1, %q", got.code, got.stderr, want)
	}
	if len(got.proc.Specs) != 0 {
		t.Errorf("ran %d commands for an unknown recipe, want 0", len(got.proc.Specs))
	}
}

func TestRunDefaultsToFirstRecipe(t *testing.T) {
	got := runFake(t, map[string]string{"/w/justfile": "b:\n  echo b\na:\n  echo a\n"}, nil)
	if got.code != 0 || len(got.proc.Specs) != 1 {
		t.Fatalf("run = %d with %d commands, want 0 with 1", got.code, len(got.proc.Specs))
	}
	spec := got.proc.Specs[0]
	if spec.Dir != "/w" || !reflect.DeepEqual(spec.Env, []string{"A=1"}) {
		t.Errorf("spec dir %q env %q; want /w and the given environment", spec.Dir, spec.Env)
	}
	if got.stderr != "echo b\n" {
		t.Errorf("stderr = %q, want the echoed command", got.stderr)
	}
}

func TestRunQuietSuppressesEcho(t *testing.T) {
	got := runFake(t, map[string]string{"/w/justfile": "a:\n  echo a\n"}, nil, "-q")
	if got.code != 0 || got.stderr != "" || len(got.proc.Specs) != 1 {
		t.Errorf("run -q = %d, %q, %d commands; want 0, empty, 1", got.code, got.stderr, len(got.proc.Specs))
	}
}

func TestRunPassesArgumentsToTheRecipe(t *testing.T) {
	got := runFake(t, map[string]string{"/w/justfile": "a x:\n  echo {{x}}\n"}, nil, "a", "hi")
	if got.code != 0 || len(got.proc.Specs) != 1 {
		t.Fatalf("run = %d with %d commands, stderr %q", got.code, len(got.proc.Specs), got.stderr)
	}
	if line := got.proc.Specs[0].Argv[len(got.proc.Specs[0].Argv)-1]; line != "echo hi" {
		t.Errorf("command line = %q, want %q", line, "echo hi")
	}
}

func TestRunExplicitJustfileResolvesAgainstWorkDir(t *testing.T) {
	files := map[string]string{"/w/sub/other.just": "a:\n  echo a\n", "/abs/x.just": "b:\n  echo b\n"}
	rel := runFake(t, files, nil, "-f", "sub/other.just")
	if rel.code != 0 || rel.proc.Specs[0].Dir != "/w/sub" {
		t.Errorf("relative -f = %d, dir %v; want 0 in /w/sub (stderr %q)", rel.code, rel.proc.Specs, rel.stderr)
	}
	abs := runFake(t, files, nil, "--justfile=/abs/x.just")
	if abs.code != 0 || abs.proc.Specs[0].Dir != "/abs" {
		t.Errorf("absolute --justfile = %d, specs %v; want 0 in /abs (stderr %q)", abs.code, abs.proc.Specs, abs.stderr)
	}
}

// TestRunCommandLineErrorsExitTwo pins upstream: its argument parser exits
// with 2, where errors in the justfile or the request exit with 1.
func TestRunCommandLineErrorsExitTwo(t *testing.T) {
	files := map[string]string{"/w/justfile": "a:\n"}
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"unknown flag", []string{"--bogus"}, "error: unexpected argument '--bogus' found\n"},
		{"missing flag value", []string{"-f"}, "error: a value is required for '--justfile <JUSTFILE>' but none was supplied\n"},
		{"-q does not silence them", []string{"-q", "--bogus"}, "error: unexpected argument '--bogus' found\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := runFake(t, files, nil, tc.args...)
			if got.code != 2 || got.stderr != tc.want {
				t.Errorf("run = %d, %q; want 2, %q", got.code, got.stderr, tc.want)
			}
		})
	}
}

func TestRunParseErrorKeepsLocation(t *testing.T) {
	got := runFake(t, map[string]string{"/w/justfile": "a: nope\n"}, nil)
	if got.code != 1 || !strings.HasPrefix(got.stderr, "error: ") || !strings.Contains(got.stderr, "nope") {
		t.Errorf("run = %d, %q; want 1 and an error naming the dependency", got.code, got.stderr)
	}
	if strings.Contains(got.stderr, ":0:0") {
		t.Errorf("stderr = %q, must not carry an empty location", got.stderr)
	}
}

func TestRunAnalysisErrorExitsOne(t *testing.T) {
	got := runFake(t, map[string]string{"/w/justfile": "a:\n  echo {{nope}}\n"}, nil)
	if got.code != 1 || !strings.HasPrefix(got.stderr, "error: ") || !strings.Contains(got.stderr, "nope") {
		t.Errorf("run = %d, %q; want 1 and an error naming the variable", got.code, got.stderr)
	}
}

// TestRunGroupsArgumentsPerRecipe is the case from upstream: with `d:` and
// `c x:`, `d c 1` runs d and then c with x=1, `c 1 c 2 c 1` runs c twice.
func TestRunGroupsArgumentsPerRecipe(t *testing.T) {
	files := map[string]string{"/w/justfile": "d:\n  echo d\nc x:\n  echo c{{x}}\n"}
	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"d", "c", "1"}, []string{"echo d", "echo c1"}},
		{[]string{"c", "1", "d", "c", "2"}, []string{"echo c1", "echo d", "echo c2"}},
		{[]string{"c", "1", "c", "2", "c", "1"}, []string{"echo c1", "echo c2"}},
	} {
		got := runFake(t, files, nil, tc.args...)
		var cmds []string
		for _, s := range got.proc.Specs {
			cmds = append(cmds, s.Argv[len(s.Argv)-1])
		}
		if got.code != 0 || !reflect.DeepEqual(cmds, tc.want) {
			t.Errorf("run %q = %d, %q (stderr %q); want 0, %q", tc.args, got.code, cmds, got.stderr, tc.want)
		}
	}
}

func TestRunArgumentCountMismatchExitsOne(t *testing.T) {
	got := runFake(t, map[string]string{"/w/justfile": "c x:\n  echo\n"}, nil, "c")
	if want := "error: recipe `c` got 0 positional arguments but takes 1\nusage:\n    just c x\n"; got.code != 1 || got.stderr != want || len(got.proc.Specs) != 0 {
		t.Errorf("run = %d, %q with %d commands; want 1, %q, none", got.code, got.stderr, len(got.proc.Specs), want)
	}
}

// TestRunQuietSuppressesErrors pins upstream's -q: the exit status is the
// same and nothing is written, whether the error is a failing recipe, an
// unknown recipe, a missing justfile or a wrong number of arguments.
func TestRunQuietSuppressesErrors(t *testing.T) {
	files := map[string]string{"/w/justfile": "a:\n  exit 3\nc x:\n  echo\n"}
	for _, tc := range []struct {
		name  string
		files map[string]string
		codes []int
		args  []string
		want  int
	}{
		{"recipe failure", files, []int{3}, []string{"-q", "a"}, 3},
		{"unknown recipe", files, nil, []string{"-q", "nope"}, 1},
		{"no justfile", nil, nil, []string{"-q"}, 1},
		{"argument count mismatch", files, nil, []string{"-q", "c"}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := runFake(t, tc.files, tc.codes, tc.args...)
			if got.code != tc.want || got.stderr != "" || got.stdout != "" {
				t.Errorf("run %q = %d, %q, %q; want %d and no output", tc.args, got.code, got.stdout, got.stderr, tc.want)
			}
		})
	}
}

func TestRunListShowsParameterDefaults(t *testing.T) {
	// Defaults are shown as written (corpus: "foo PARAM='foo'", "a Z=\"\\t z\"").
	src := "a b='B' c=\"C d\":\n  echo\nz q:\n"
	got := runFake(t, map[string]string{"/w/justfile": src}, nil, "--list")
	want := "Available recipes:\n    a b='B' c=\"C d\"\n    z q\n"
	if got.code != 0 || got.stdout != want {
		t.Errorf("run --list = %d, %q; want 0, %q", got.code, got.stdout, want)
	}
}

func TestMainExitsWithRunStatus(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "justfile")
	if err := os.WriteFile(path, []byte("x:\n  exit 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	errFile, err := os.Create(filepath.Join(dir, "stderr"))
	if err != nil {
		t.Fatal(err)
	}
	defer errFile.Close()

	oldExit, oldArgs, oldStderr := exit, os.Args, os.Stderr
	t.Cleanup(func() { exit, os.Args, os.Stderr = oldExit, oldArgs, oldStderr })
	got := -1
	exit = func(code int) { got = code }
	os.Args = []string{"just-go", "-f", path, "x"}
	os.Stderr = errFile

	main()

	if got != 3 {
		t.Errorf("main exited with %d, want 3", got)
	}
	data, err := os.ReadFile(errFile.Name())
	if err != nil || !strings.Contains(string(data), "exit code 3") {
		t.Errorf("stderr file = %q, %v; want the failure message", data, err)
	}
}

func TestWorkingDir(t *testing.T) {
	if got := workingDir(func() (string, error) { return "/here", nil }); got != "/here" {
		t.Errorf("workingDir = %q, want /here", got)
	}
	if got := workingDir(func() (string, error) { return "", errors.New("gone") }); got != "." {
		t.Errorf("workingDir on error = %q, want %q", got, ".")
	}
}

func TestParseArgs(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want config
	}{
		{"empty", nil, config{}},
		{"words", []string{"a", "b"}, config{words: []string{"a", "b"}}},
		{"long flags", []string{"--list", "--quiet", "--justfile", "j"}, config{list: true, quiet: true, justfile: "j"}},
		{"attached long value", []string{"--justfile=j"}, config{justfile: "j"}},
		{"short flags", []string{"-l", "-q", "-f", "j"}, config{list: true, quiet: true, justfile: "j"}},
		{"clustered", []string{"-lq"}, config{list: true, quiet: true}},
		{"attached short value", []string{"-fj"}, config{justfile: "j"}},
		{"cluster ending in value", []string{"-qf", "j"}, config{quiet: true, justfile: "j"}},
		// As upstream's: after the first word every argument is a word.
		{"dashes after a word are words", []string{"a", "-q", "--verbose", "b"}, config{words: []string{"a", "-q", "--verbose", "b"}}},
		{"double dash after a word is a word", []string{"a", "--", "-l"}, config{words: []string{"a", "--", "-l"}}},
		{"double dash before words ends flags", []string{"-q", "--", "-l", "--list"}, config{quiet: true, words: []string{"-l", "--list"}}},
		{"lone dash is a word", []string{"-"}, config{words: []string{"-"}}},
		{"flags after a lone dash are words", []string{"-", "-q"}, config{words: []string{"-", "-q"}}},
		{"shell", []string{"--shell", "bash"}, config{shell: "bash", shellGiven: true}},
		{"attached shell", []string{"--shell=bash"}, config{shell: "bash", shellGiven: true}},
		{"shell arg", []string{"--shell-arg", "-c"}, config{shellArgs: []string{"-c"}}},
		{"shell args repeat in order", []string{"--shell-arg", "-e", "--shell-arg=-c", "a"}, config{shellArgs: []string{"-e", "-c"}, words: []string{"a"}}},
		{"shell with args", []string{"--shell-arg", "-c", "--shell", "zsh"}, config{shell: "zsh", shellGiven: true, shellArgs: []string{"-c"}}},
		{"unstable", []string{"--unstable", "a"}, config{unstable: true, words: []string{"a"}}},
		{"yes", []string{"--yes", "a"}, config{yes: true, words: []string{"a"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseArgs(tc.args)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Errorf("parseArgs(%q) = %+v, %v; want %+v", tc.args, got, err, tc.want)
			}
		})
	}
}

func TestParseArgsErrors(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--nope"}, "unexpected argument '--nope' found"},
		{[]string{"-x"}, "unexpected argument '-x' found"},
		{[]string{"-lx"}, "unexpected argument '-x' found"},
		{[]string{"--list=1"}, "unexpected value for '--list'"},
		{[]string{"--justfile"}, "a value is required for '--justfile <JUSTFILE>' but none was supplied"},
		{[]string{"-f"}, "a value is required for '--justfile <JUSTFILE>' but none was supplied"},
		{[]string{"-lf"}, "a value is required for '--justfile <JUSTFILE>' but none was supplied"},
		{[]string{"--shell"}, "a value is required for '--shell <SHELL>' but none was supplied"},
		{[]string{"--shell-arg"}, "a value is required for '--shell-arg <SHELL_ARG>' but none was supplied"},
		{[]string{"--shell", "a", "--shell=b"}, "the argument '--shell <SHELL>' cannot be used multiple times"},
	} {
		_, err := parseArgs(tc.args)
		var de *diag.Error
		if !errors.As(err, &de) || de.Kind != diag.KindUsage || de.Msg != tc.want {
			t.Errorf("parseArgs(%q) error = %v, want usage error %q", tc.args, err, tc.want)
		}
	}
}

// listsJustfile needs unstable features.
const listsJustfile = "set lists\nfoo:\n  @echo hi\n"

// TestRunRefusesUnstableFeaturesUnlessEnabled is upstream's gate: --unstable,
// JUST_UNSTABLE (unless it is empty or false-like) and `set unstable` each
// enable them. The environment values were checked against the oracle.
func TestRunRefusesUnstableFeaturesUnlessEnabled(t *testing.T) {
	const refused = "error: the `lists` setting is currently unstable, invoke `just` with `--unstable`, " +
		"set the `JUST_UNSTABLE` environment variable, or add `set unstable` to your `justfile` to enable unstable features\n"
	for _, tc := range []struct {
		name string
		src  string
		args []string
		env  []string
		ok   bool
	}{
		{"nothing", listsJustfile, nil, nil, false},
		{"the flag", listsJustfile, []string{"--unstable"}, nil, true},
		{"the flag with a recipe", listsJustfile, []string{"--unstable", "foo"}, nil, true},
		{"the setting", "set unstable\n" + listsJustfile, nil, nil, true},
		{"the setting off", "set unstable := false\n" + listsJustfile, nil, nil, false},
		{"the environment", listsJustfile, nil, []string{"JUST_UNSTABLE=1"}, true},
		{"true", listsJustfile, nil, []string{"JUST_UNSTABLE=true"}, true},
		{"yes", listsJustfile, nil, []string{"JUST_UNSTABLE=yes"}, true},
		{"anything", listsJustfile, nil, []string{"JUST_UNSTABLE=anything"}, true},
		{"the last of two", listsJustfile, nil, []string{"JUST_UNSTABLE=0", "JUST_UNSTABLE=1"}, true},
		{"the last of two, off", listsJustfile, nil, []string{"JUST_UNSTABLE=1", "JUST_UNSTABLE=off"}, false},
		{"empty", listsJustfile, nil, []string{"JUST_UNSTABLE="}, false},
		{"0", listsJustfile, nil, []string{"JUST_UNSTABLE=0"}, false},
		{"false", listsJustfile, nil, []string{"JUST_UNSTABLE=false"}, false},
		{"FALSE", listsJustfile, nil, []string{"JUST_UNSTABLE=FALSE"}, false},
		{"no", listsJustfile, nil, []string{"JUST_UNSTABLE=no"}, false},
		{"off", listsJustfile, nil, []string{"JUST_UNSTABLE=off"}, false},
		{"n", listsJustfile, nil, []string{"JUST_UNSTABLE=n"}, false},
		{"f", listsJustfile, nil, []string{"JUST_UNSTABLE=f"}, false},
		{"another variable", listsJustfile, nil, []string{"JUST_UNSTABLE_X=1", "XJUST_UNSTABLE=1"}, false},
		{"the flag beats a false environment", listsJustfile, []string{"--unstable"}, []string{"JUST_UNSTABLE=0"}, true},
		{"listing is refused too", listsJustfile, []string{"--list"}, nil, false},
		{"listing with the flag", listsJustfile, []string{"--list", "--unstable"}, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			proc := &sysfake.FakeProcess{}
			code := run(tc.args, tc.env, "/w", strings.NewReader(""), &stdout, &stderr,
				sysfake.FakeFS(map[string]string{"/w/justfile": tc.src}), proc)
			if tc.ok && (code != 0 || stderr.Len() != 0) {
				t.Errorf("run = %d, %q; want success", code, stderr.String())
			}
			if !tc.ok && (code != 1 || stderr.String() != refused || len(proc.Specs) != 0) {
				t.Errorf("run = %d, %q; want the unstable error", code, stderr.String())
			}
		})
	}
}

func TestSelectRecipes(t *testing.T) {
	infos := []just.RecipeInfo{
		{Name: "a", Params: []string{"x", "y"}},
		{Name: "b"},
		{Name: "c", Params: []string{"z"}},
	}
	call := func(recipe string, args ...string) just.Call {
		return just.Call{Recipe: recipe, Args: append([]string(nil), args...)}
	}
	for _, tc := range []struct {
		name  string
		words []string
		want  []just.Call
	}{
		{"none", nil, nil},
		{"one recipe", []string{"b"}, []just.Call{call("b")}},
		{"arguments fill parameters", []string{"a", "1", "2"}, []just.Call{call("a", "1", "2")}},
		{"fewer words than parameters", []string{"a", "1"}, []just.Call{call("a", "1")}},
		{"later words are recipes", []string{"a", "1", "2", "b"}, []just.Call{call("a", "1", "2"), call("b")}},
		{"a later recipe takes its own arguments", []string{"b", "c", "1"}, []just.Call{call("b"), call("c", "1")}},
		{"every recipe takes its own arguments", []string{"c", "1", "a", "2", "3", "c", "4"},
			[]just.Call{call("c", "1"), call("a", "2", "3"), call("c", "4")}},
		{"a recipe name can be an argument", []string{"c", "b", "b"}, []just.Call{call("c", "b"), call("b")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := selectRecipes(infos, tc.words)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Errorf("selectRecipes = %+v, %v; want %+v", got, err, tc.want)
			}
		})
	}
}

func TestSelectRecipesUnknownName(t *testing.T) {
	infos := []just.RecipeInfo{{Name: "a", Params: []string{"x", "y"}}, {Name: "b"}}
	for _, tc := range []struct {
		name    string
		words   []string
		missing string
		suffix  string
	}{
		{"unknown first", []string{"nope"}, "nope", ""},
		// The oracle suggests `a` here, one edit from `3`.
		{"unknown later (corpus misc::argument_mismatch_more)", []string{"a", "1", "2", "3"}, "3", "\nDid you mean `a`?"},
		{"unknown stops at the first", []string{"b", "nope", "worse"}, "nope", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := selectRecipes(infos, tc.words)
			want := "justfile does not contain recipe `" + tc.missing + "`" + tc.suffix
			var de *diag.Error
			if !errors.As(err, &de) || de.Kind != diag.KindUsage || de.Msg != want {
				t.Errorf("error = %v, want usage error %q", err, want)
			}
		})
	}
}

func TestSelectRecipesCopiesArguments(t *testing.T) {
	words := []string{"a", "1"}
	calls, err := selectRecipes([]just.RecipeInfo{{Name: "a", Params: []string{"x"}}}, words)
	if err != nil {
		t.Fatal(err)
	}
	calls[0].Args[0] = "changed"
	if words[1] != "1" {
		t.Errorf("selectRecipes aliased its input: words = %q", words)
	}
}

func TestFormatList(t *testing.T) {
	wide := "this-recipe-is-very-very-very-very-very-very-very-very-important"
	for _, tc := range []struct {
		name  string
		infos []just.RecipeInfo
		want  string
	}{
		{"empty", nil, "Available recipes:\n"},
		{
			"defaults as written (corpus: misc::list_alignment#1)",
			[]just.RecipeInfo{
				{Name: "hello", Doc: "this does a thing", Params: []string{"a", "b", "c"}, Defaults: []string{"", "'B\t'", "'C'"}},
				{Name: "a", Doc: "something else", Params: []string{"Z"}, Defaults: []string{`"\t z"`}},
			},
			"Available recipes:\n    a Z=\"\\t z\"           # something else\n    hello a b='B\t' c='C' # this does a thing\n",
		},
		{"sorted by name", []just.RecipeInfo{{Name: "b"}, {Name: "a"}}, "Available recipes:\n    a\n    b\n"},
		{"private hidden", []just.RecipeInfo{{Name: "_p", Private: true}, {Name: "a"}}, "Available recipes:\n    a\n"},
		{
			"doc column aligned (misc::list_alignment#1)",
			[]just.RecipeInfo{
				{Name: "hello", Doc: "this does a thing", Params: []string{"a", "b", "c"}},
				{Name: "a", Doc: "something else", Params: []string{"Z"}},
			},
			"Available recipes:\n    a Z         # something else\n    hello a b c # this does a thing\n",
		},
		{
			"wide signature carries its doc above (misc::list_alignment_long#1)",
			[]just.RecipeInfo{
				{Name: "hello", Doc: "this does a thing", Params: []string{"a"}},
				{Name: "x", Doc: "this does another thing", Params: []string{"a"}},
				{Name: wide, Doc: "something else"},
			},
			"Available recipes:\n    hello a # this does a thing\n    # something else\n    " + wide + "\n    x a     # this does another thing\n",
		},
		{
			"multi-line doc goes above",
			[]just.RecipeInfo{{Name: "a", Doc: "one\ntwo"}},
			"Available recipes:\n    # one\n    # two\n    a\n",
		},
		{
			"undocumented recipe is not padded",
			[]just.RecipeInfo{{Name: "a"}, {Name: "bbbb", Doc: "d"}},
			"Available recipes:\n    a\n    bbbb # d\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatList(tc.infos); got != tc.want {
				t.Errorf("formatList =\n%q\nwant\n%q", got, tc.want)
			}
		})
	}
}

func TestFormatListDoesNotReorderInput(t *testing.T) {
	infos := []just.RecipeInfo{{Name: "b"}, {Name: "a"}}
	formatList(infos)
	if infos[0].Name != "b" || infos[1].Name != "a" {
		t.Errorf("formatList reordered its input: %+v", infos)
	}
}

// TestDescribe pins upstream's rendering of a located error (Token's
// ColorDisplay): the base name of the file, the line quoted with tabs as four
// spaces, and carets under the token. Each located want is the oracle's.
func TestDescribe(t *testing.T) {
	located := func(file string, line, col, width int, source, msg string) *diag.Error {
		return &diag.Error{Kind: diag.KindAnalysis, File: file, Line: line, Col: col, Width: width, Source: source, Msg: msg}
	}
	for _, tc := range []struct {
		err  error
		want string
	}{
		{usage("no good"), "no good"},
		{errors.New("plain"), "plain"},
		{located("/w/justfile", 2, 9, 1, "\techo {{y}}", "variable `y` not defined"),
			"variable `y` not defined\n ——▶ justfile:2:9\n  │\n2 │     echo {{y}}\n  │            ^"},
		{located("/w/justfile", 3, 3, 13, "\t\t\techo goodbye", "recipe line has extra leading whitespace"),
			"recipe line has extra leading whitespace\n ——▶ justfile:3:3\n  │\n3 │             echo goodbye\n  │         ^^^^^^^^^^^^^^^^"},
		{located("justfile", 1, 6, 0, "x := ", "expected"),
			"expected\n ——▶ justfile:1:6\n  │\n1 │ x := \n  │      ^"},
		{located("j", 12, 1, 3, "ab", "past the end"),
			"past the end\n  ——▶ j:12:1\n   │\n12 │ ab\n   │ ^^"},
		{located("j", 1, 1, 1, "", "empty"),
			"empty\n ——▶ j:1:1\n  │\n1 │ \n  │ ^"},
		// After multibyte text the header counts bytes and the carets
		// display columns (oracle).
		{located("justfile", 2, 12, 4, "  echo é {{nope}}", "variable `nope` not defined"),
			"variable `nope` not defined\n ——▶ justfile:2:13\n  │\n2 │   echo é {{nope}}\n  │            ^^^^"},
		{located("justfile", 1, 13, 1, `x := "日本" + y`, "variable `y` not defined"),
			"variable `y` not defined\n ——▶ justfile:1:17\n  │\n1 │ x := \"日本\" + y\n  │               ^"},
		{located("justfile", 1, 6, 4, `x := "日本"`, "wide"),
			"wide\n ——▶ justfile:1:6\n  │\n1 │ x := \"日本\"\n  │      ^^^^^^"},
	} {
		if got := describe(tc.err); got != tc.want {
			t.Errorf("describe(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}

func TestResolve(t *testing.T) {
	for _, tc := range []struct{ dir, path, want string }{
		{"/w", "", ""},
		{"/w", "/abs/j", "/abs/j"},
		{"/w", "sub/j", "/w/sub/j"},
	} {
		if got := resolve(tc.dir, tc.path); got != tc.want {
			t.Errorf("resolve(%q, %q) = %q, want %q", tc.dir, tc.path, got, tc.want)
		}
	}
}

func BenchmarkFormatList(b *testing.B) {
	infos := make([]just.RecipeInfo, 200)
	for i := range infos {
		infos[i] = just.RecipeInfo{
			Name:   "recipe-" + strings.Repeat("x", i%40),
			Doc:    "does a thing",
			Params: []string{"a", "b"},
		}
	}
	b.ReportAllocs()
	for b.Loop() {
		formatList(infos)
	}
}

func BenchmarkFormatListGroups(b *testing.B) {
	infos := make([]just.RecipeInfo, 200)
	for i := range infos {
		infos[i] = just.RecipeInfo{
			Name:   "recipe-" + strings.Repeat("x", i%40),
			Doc:    "does a thing",
			Params: []string{"a", "b"},
			Groups: []string{fmt.Sprintf("group-%d", i%9)},
		}
	}
	b.ReportAllocs()
	for b.Loop() {
		formatList(infos)
	}
}

func BenchmarkParseArgs(b *testing.B) {
	args := []string{"-q", "-f", "justfile", "build", "--", "-x", "y"}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := parseArgs(args); err != nil {
			b.Fatal(err)
		}
	}
}

func TestRunShellFlagsOverrideSetShell(t *testing.T) {
	files := map[string]string{"/w/justfile": "set shell := [\"bash\", \"-c\"]\nr:\n\techo hi\n"}
	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{"set shell without flags", []string{"r"}, []string{"bash", "-c", "echo hi"}},
		{"shell", []string{"--shell", "zsh", "r"}, []string{"zsh", "-cu", "echo hi"}},
		{"shell arg", []string{"--shell-arg", "-c", "r"}, []string{"sh", "-c", "echo hi"}},
		{"repeated shell args", []string{"--shell-arg", "-e", "--shell-arg", "-c", "--shell", "zsh", "r"}, []string{"zsh", "-e", "-c", "echo hi"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := runFake(t, files, nil, tc.args...)
			if o.code != 0 || len(o.proc.Specs) != 1 {
				t.Fatalf("run = %d, specs %d (stderr %q)", o.code, len(o.proc.Specs), o.stderr)
			}
			if got := o.proc.Specs[0].Argv; !reflect.DeepEqual(got, tc.want) {
				t.Errorf("argv = %q; want %q", got, tc.want)
			}
		})
	}
}

func TestRunRepeatedShellExitsTwo(t *testing.T) {
	o := runFake(t, map[string]string{"/w/justfile": "r:\n\techo hi\n"}, nil, "--shell", "a", "--shell", "b", "r")
	want := "error: the argument '--shell <SHELL>' cannot be used multiple times\n"
	if o.code != 2 || o.stderr != want || len(o.proc.Specs) != 0 {
		t.Errorf("run = %d, stderr %q, specs %d; want 2, %q, 0", o.code, o.stderr, len(o.proc.Specs), want)
	}
}

// TestRunQuietDiscardsRecipeOutput pins upstream's --quiet, which sends the
// recipes' stdout and stderr to /dev/null (quiet::no_stdout#1,
// quiet::stderr#1).
func TestRunQuietDiscardsRecipeOutput(t *testing.T) {
	files := map[string]string{"/w/justfile": "default:\n  @echo hello\n  echo err >&2\n"}
	quiet := runFake(t, files, nil, "--quiet")
	for _, spec := range quiet.proc.Specs {
		if spec.Stdout != io.Discard || spec.Stderr != io.Discard {
			t.Errorf("-q child streams = %v, %v; want io.Discard", spec.Stdout, spec.Stderr)
		}
	}
	loud := runFake(t, files, nil)
	if spec := loud.proc.Specs[0]; spec.Stdout == io.Discard || spec.Stderr == io.Discard {
		t.Errorf("child streams without -q are discarded")
	}
	if len(quiet.proc.Specs) != 2 || quiet.stdout != "" || quiet.stderr != "" {
		t.Errorf("run -q = %d commands, %q, %q; want 2 and no output", len(quiet.proc.Specs), quiet.stdout, quiet.stderr)
	}
}

// TestRunPassesDashedArgumentsToTheRecipe pins upstream: `just a -x` gives
// -x to recipe a, and after a word "--" is a word too.
func TestRunPassesDashedArgumentsToTheRecipe(t *testing.T) {
	files := map[string]string{"/w/justfile": "a x:\n  echo {{x}}\nl:\n  echo l\n"}
	for _, arg := range []string{"-x", "--verbose", "--"} {
		got := runFake(t, files, nil, "a", arg)
		if got.code != 0 || len(got.proc.Specs) != 1 || got.proc.Specs[0].Argv[2] != "echo "+arg {
			t.Errorf("run a %s = %d, %v (stderr %q); want echo %s", arg, got.code, got.proc.Specs, got.stderr, arg)
		}
	}
	got := runFake(t, files, nil, "a", "--", "-l")
	// The oracle suggests `l`, one edit from `-l`.
	if want := "error: justfile does not contain recipe `-l`\nDid you mean `l`?\n"; got.code != 1 || got.stderr != want {
		t.Errorf("run a -- -l = %d, %q; want 1, %q", got.code, got.stderr, want)
	}
}

// TestRunRendersErrorsAsUpstream checks a whole error as the corpus records
// it (misc::unknown_dependency#1).
func TestRunRendersErrorsAsUpstream(t *testing.T) {
	got := runFake(t, map[string]string{"/w/justfile": "bar:\nhello:\nfoo: bar baaaaaaaz hello"}, nil)
	want := "error: recipe `foo` has unknown dependency `baaaaaaaz`\n ——▶ justfile:3:10\n  │\n3 │ foo: bar baaaaaaaz hello\n  │          ^^^^^^^^^\n"
	if got.code != 1 || got.stderr != want {
		t.Errorf("run = %d, %q; want 1, %q", got.code, got.stderr, want)
	}
}

// TestErrorColumnCountsRunesThroughRun runs a justfile whose error follows
// multibyte text and compares stderr with the oracle's, byte for byte.
func TestErrorColumnCountsRunesThroughRun(t *testing.T) {
	got := runFake(t, map[string]string{"/w/justfile": "a:\n  echo é {{nope}}\n"}, nil)
	want := "error: variable `nope` not defined\n ——▶ justfile:2:13\n  │\n2 │   echo é {{nope}}\n  │            ^^^^\n"
	if got.code != 1 || got.stderr != want {
		t.Errorf("run = %d, %q; want 1, %q", got.code, got.stderr, want)
	}
}

// TestDescribeSkipsSnippetWithoutSource pins upstream's one-line rendering
// of a runtime error that has a line but no column, such as a shell that
// cannot be found (shell::recipe_shell_not_found_error_message).
func TestDescribeSkipsSnippetWithoutSource(t *testing.T) {
	de := &diag.Error{Kind: diag.KindRuntime, File: "/w/justfile", Line: 2, Msg: "recipe `a` could not be run"}
	if got := describe(de); got != de.Msg {
		t.Errorf("describe = %q, want %q", got, de.Msg)
	}
	missing := fmt.Errorf("exec: %q: %w", "nosuchshell", fs.ErrNotExist)
	proc := &sysfake.FakeProcess{Err: missing}
	var stdout, stderr bytes.Buffer
	files := sysfake.FakeFS(map[string]string{"/w/justfile": "a:\n  @echo hi\n"})
	code := run([]string{"--shell", "nosuchshell"}, nil, "/w", strings.NewReader(""), &stdout, &stderr, files, proc)
	want := "error: recipe `a` could not be run because just could not find the shell `nosuchshell`: " +
		missing.Error() + "\n"
	if code != 1 || stderr.String() != want {
		t.Errorf("run = %d, %q; want 1, %q", code, stderr.String(), want)
	}
}

// TestRunArgumentCountMismatchPrintsUsage pins the usage line upstream adds
// to a wrong number of arguments (misc::argument_mismatch_fewer_with_default).
func TestRunArgumentCountMismatchPrintsUsage(t *testing.T) {
	got := runFake(t, map[string]string{"/w/justfile": "foo A B C='C':\n  echo\n"}, nil, "foo", "bar")
	want := "error: recipe `foo` got 1 positional argument but takes at least 2\nusage:\n    just foo A B [C]\n"
	if got.code != 1 || got.stderr != want {
		t.Errorf("run = %d, %q; want 1, %q", got.code, got.stderr, want)
	}
}

// TestRunUnknownRecipeSuggestsAName pins upstream's suggestion: the public
// recipe with the smallest edit distance below three, the first by name on
// a tie, and none when every name is too far (misc::run_suggestion).
func TestRunUnknownRecipeSuggestsAName(t *testing.T) {
	for _, tc := range []struct {
		src, word, want string
	}{
		{"hello:\n", "hell", "error: justfile does not contain recipe `hell`\nDid you mean `hello`?\n"},
		{"abd:\nabc:\n", "ab", "error: justfile does not contain recipe `ab`\nDid you mean `abc`?\n"},
		{"_hello:\nb:\n", "hello", "error: justfile does not contain recipe `hello`\n"},
		{"hello:\n", "xyz", "error: justfile does not contain recipe `xyz`\n"},
	} {
		got := runFake(t, map[string]string{"/w/justfile": tc.src}, nil, tc.word)
		if got.code != 1 || got.stderr != tc.want {
			t.Errorf("%q with %q: run = %d, %q; want 1, %q", tc.src, tc.word, got.code, got.stderr, tc.want)
		}
	}
}

// The cases below are the corpus's default group: default_list_lists_recipes,
// default_list_does_not_override_explicit_recipe and
// default_list_false_runs_default_recipe.
func TestRunDefaultListListsRecipesWhenNoneIsNamed(t *testing.T) {
	src := "set default-list := true\n\nfoo:\n  @echo foo\n\nbar:\n"
	got := runFake(t, map[string]string{"/w/justfile": src}, nil)
	if want := "Available recipes:\n    bar\n    foo\n"; got.code != 0 || got.stdout != want || len(got.proc.Specs) != 0 {
		t.Errorf("run = %d, stdout %q, ran %d; want 0, %q, none", got.code, got.stdout, len(got.proc.Specs), want)
	}
}

func TestRunDefaultListDoesNotOverrideAnExplicitRecipe(t *testing.T) {
	src := "set default-list\n\nfoo:\n  @echo foo\n\nbar:\n  @echo bar\n"
	got := runFake(t, map[string]string{"/w/justfile": src}, nil, "bar")
	if got.code != 0 || got.stdout != "" || len(got.proc.Specs) != 1 {
		t.Errorf("run = %d, stdout %q, ran %d", got.code, got.stdout, len(got.proc.Specs))
	}
}

func TestRunDefaultListFalseRunsTheDefaultRecipe(t *testing.T) {
	src := "set default-list := false\n\nfoo:\n  @echo foo\n\nbar:\n"
	got := runFake(t, map[string]string{"/w/justfile": src}, nil)
	if got.code != 0 || got.stdout != "" || len(got.proc.Specs) != 1 {
		t.Errorf("run = %d, stdout %q, ran %d", got.code, got.stdout, len(got.proc.Specs))
	}
}

func TestRunNoCdRunsInTheDirectoryItWasRunFrom(t *testing.T) {
	// The justfile is found in the parent of the working directory.
	got := runFake(t, map[string]string{"/justfile": "set no-cd\nfoo:\n  @echo\n"}, nil)
	if got.code != 0 || len(got.proc.Specs) != 1 || got.proc.Specs[0].Dir != "/w" {
		t.Errorf("run = %d, specs %+v; want one in /w", got.code, got.proc.Specs)
	}
}

// The cases below are the corpus's groups, private and attributes groups.
func TestFormatListGroups(t *testing.T) {
	for _, tc := range []struct {
		name  string
		infos []just.RecipeInfo
		want  string
	}{
		{
			"groups::list_with_groups#1",
			[]just.RecipeInfo{
				{Name: "a", Groups: []string{"alpha"}},
				{Name: "b", Doc: "Doc comment", Groups: []string{"alpha", "beta"}},
				{Name: "c"},
				{Name: "d", Groups: []string{"multi word group"}},
				{Name: "e", Groups: []string{"alpha"}},
				{Name: "f", Groups: []string{"alpha", "beta"}},
			},
			"Available recipes:\n    c\n\n    [alpha]\n    a\n    b # Doc comment\n    e\n    f\n\n" +
				"    [beta]\n    b # Doc comment\n    f\n\n    [multi word group]\n    d\n",
		},
		{"only a group has a heading", []just.RecipeInfo{{Name: "a", Groups: []string{"x"}}}, "Available recipes:\n    [x]\n    a\n"},
		{"no groups, no headings", []just.RecipeInfo{{Name: "b"}, {Name: "a"}}, "Available recipes:\n    a\n    b\n"},
		{
			"a private recipe's group is not listed",
			[]just.RecipeInfo{{Name: "p", Private: true, Groups: []string{"hidden"}}, {Name: "a", Groups: []string{"shown"}}},
			"Available recipes:\n    [shown]\n    a\n",
		},
		{
			"the docs of every group line up",
			[]just.RecipeInfo{
				{Name: "a", Doc: "first", Groups: []string{"x"}},
				{Name: "bbbbbb", Doc: "second", Groups: []string{"y"}},
			},
			"Available recipes:\n    [x]\n    a      # first\n\n    [y]\n    bbbbbb # second\n",
		},
		{"an empty group name", []just.RecipeInfo{{Name: "a", Groups: []string{""}}}, "Available recipes:\n    []\n    a\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatList(tc.infos); got != tc.want {
				t.Errorf("formatList =\n%q\nwant\n%q", got, tc.want)
			}
		})
	}
}

func TestRunListShowsAttributes(t *testing.T) {
	for _, tc := range []struct {
		id, src, want string
	}{
		{"attributes::doc_attribute#1", "# Non-document comment\n[doc('The real docstring')]\nfoo:\n  echo foo\n",
			"Available recipes:\n    foo # The real docstring\n"},
		{"attributes::doc_attribute_suppress#1", "# Non-document comment\n[doc]\nfoo:\n  echo foo\n", "Available recipes:\n    foo\n"},
		{"attributes::doc_attribute_may_be_expression#1", "prefix := 'hello '\n[doc(prefix + 'world')]\nfoo:\n",
			"Available recipes:\n    foo # hello world\n"},
		{"attributes::doc_attribute_may_reference_variable_transitively#1", "a := b\nb := 'bar'\n[doc(a)]\nfoo:\n",
			"Available recipes:\n    foo # bar\n"},
		{"attributes::doc_multiline#1", "[doc('multiline\ncomment')]\nfoo:\n", "Available recipes:\n    # multiline\n    # comment\n    foo\n"},
		{"list::empty_doc_attributes_are_ignored#1", "[doc(' ')]\nfoo bar:\n", "Available recipes:\n    foo bar\n"},
		{"no_exit_message::recipe_has_doc_comment#1", "# This is a doc comment\n[no-exit-message]\nhello:\n  @exit 100\n",
			"Available recipes:\n    hello # This is a doc comment\n"},
		{"private::private_attribute_for_recipe#1", "[private]\nfoo:\n\nbar:\n", "Available recipes:\n    bar\n"},
		{"groups::list_with_groups#1",
			"[group('alpha')]\na:\n# Doc comment\n[group('alpha')]\n[group('beta')]\nb:\nc:\n[group('multi word group')]\nd:\n" +
				"[group('alpha')]\ne:\n[group('beta')]\n[group('alpha')]\nf:\n",
			"Available recipes:\n    c\n\n    [alpha]\n    a\n    b # Doc comment\n    e\n    f\n\n" +
				"    [beta]\n    b # Doc comment\n    f\n\n    [multi word group]\n    d\n"},
	} {
		t.Run(tc.id, func(t *testing.T) {
			got := runFake(t, map[string]string{"/w/justfile": tc.src}, nil, "--list")
			if got.code != 0 || got.stdout != tc.want || got.stderr != "" {
				t.Errorf("run --list = %d, %q, %q; want 0, %q, empty", got.code, got.stdout, got.stderr, tc.want)
			}
		})
	}
}

// A private recipe is never suggested, as upstream's (misc::private_recipes_are_not_suggested#1).
func TestRunUnknownRecipeDoesNotSuggestAPrivateRecipe(t *testing.T) {
	got := runFake(t, map[string]string{"/w/justfile": "[private]\nhello:\n"}, nil, "hell")
	if want := "error: justfile does not contain recipe `hell`\n"; got.code != 1 || got.stderr != want {
		t.Errorf("run = %d, %q; want 1, %q", got.code, got.stderr, want)
	}
}

func TestRunDefaultAttributeRunsInsteadOfTheFirstRecipe(t *testing.T) {
	got := runFake(t, map[string]string{"/w/justfile": "foo:\n  @echo FOO\n\n[default]\nbar:\n  @echo BAR\n"}, nil)
	if got.code != 0 || len(got.proc.Specs) != 1 || got.proc.Specs[0].Argv[len(got.proc.Specs[0].Argv)-1] != "echo BAR" {
		t.Errorf("run = %d, %+v", got.code, got.proc.Specs)
	}
}

// A quiet run still shows the prompt, as upstream's does, and the refusal is
// not reported.
func TestRunQuietConfirmShowsThePrompt(t *testing.T) {
	code, stderr, proc := confirmRun("[confirm]\n@foo:\n  echo FOO\n", "n\n", "--quiet")
	if code != 1 || stderr != "Run recipe `foo`? " || len(proc.Specs) != 0 {
		t.Errorf("run = %d, %q, ran %d", code, stderr, len(proc.Specs))
	}
}

// confirmRun runs src with the given standard input.
func confirmRun(src, stdin string, args ...string) (int, string, *sysfake.FakeProcess) {
	var stdout, stderr bytes.Buffer
	proc := &sysfake.FakeProcess{}
	files := sysfake.FakeFS{"/w/justfile": src}
	code := run(args, nil, "/w", strings.NewReader(stdin), &stdout, &stderr, files, proc)
	return code, stderr.String(), proc
}

// The cases below are the corpus's confirm group.
func TestRunConfirm(t *testing.T) {
	src := "[confirm]\n@foo:\n  echo FOO\n"
	for _, tc := range []struct {
		name, stdin string
		args        []string
		code, ran   int
		stderr      string
	}{
		{"confirm::confirm_recipe#1", "y\n", nil, 0, 1, "Run recipe `foo`? "},
		{"confirm::do_not_confirm_recipe#1", "", nil, 1, 0, "Run recipe `foo`? error: recipe `foo` was not confirmed\n"},
		{"confirm::confirm_recipe_arg#1", "", []string{"--yes"}, 0, 1, ""},
		{"a refusal", "n\n", nil, 1, 0, "Run recipe `foo`? error: recipe `foo` was not confirmed\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, stderr, proc := confirmRun(src, tc.stdin, tc.args...)
			if code != tc.code || len(proc.Specs) != tc.ran || stderr != tc.stderr {
				t.Errorf("run = %d, ran %d, stderr %q; want %d, %d, %q", code, len(proc.Specs), stderr, tc.code, tc.ran, tc.stderr)
			}
		})
	}
}

// A backtick that fails ends the run with the status of its command, and the
// error quotes the backtick.
func TestRunBacktickFailureEndsWithTheCommandsStatus(t *testing.T) {
	got := runFake(t, map[string]string{"/w/justfile": "foo:\n echo hello\n echo {{`exit 123`}}\n"}, []int{0, 123})
	want := "echo hello\nerror: backtick failed with exit code 123\n ——▶ justfile:3:9\n  │\n3 │  echo {{`exit 123`}}\n  │         ^^^^^^^^^^\n"
	if got.code != 123 || got.stderr != want {
		t.Errorf("run = %d, %q; want 123, %q", got.code, got.stderr, want)
	}
}

func TestRunBacktickFailureStaysQuietWhenQuiet(t *testing.T) {
	got := runFake(t, map[string]string{"/w/justfile": "a := `exit 100`\nfoo:\n"}, []int{100}, "--quiet")
	if got.code != 100 || got.stderr != "" {
		t.Errorf("run = %d, %q; want 100 and nothing written", got.code, got.stderr)
	}
}

// The error of a justfile that is run, not only one that is read, quotes the
// line it is about.
func TestRunBacktickAssignmentFailureQuotesItsLine(t *testing.T) {
	got := runFake(t, map[string]string{"/w/justfile": "foo:\n echo hello\na := `exit 222`"}, []int{222})
	want := "error: backtick failed with exit code 222\n ——▶ justfile:3:6\n  │\n3 │ a := `exit 222`\n  │      ^^^^^^^^^^\n"
	if got.code != 222 || got.stderr != want || len(got.proc.Specs) != 1 {
		t.Errorf("run = %d, %q after %d commands; want 222, %q after one", got.code, got.stderr, len(got.proc.Specs), want)
	}
}

func TestStatusOf(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want int
	}{
		{errors.New("plain"), exitFailure},
		{usage("no good"), exitFailure},
		{&diag.Error{Kind: diag.KindRuntime, Msg: "x", ExitCode: 7}, 7},
		{fmt.Errorf("wrapped: %w", &diag.Error{Kind: diag.KindRuntime, ExitCode: 130}), 130},
	} {
		if got := statusOf(tc.err); got != tc.want {
			t.Errorf("statusOf(%v) = %d, want %d", tc.err, got, tc.want)
		}
	}
}
