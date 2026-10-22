package main

import (
	"bytes"
	"errors"
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

func TestRunUsageErrorsExitOne(t *testing.T) {
	files := map[string]string{"/w/justfile": "a:\n"}
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"unknown flag", []string{"--bogus"}, "error: unexpected argument '--bogus' found\n"},
		{"missing flag value", []string{"-f"}, "error: a value is required for '--justfile <JUSTFILE>' but none was supplied\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := runFake(t, files, nil, tc.args...)
			if got.code != 1 || got.stderr != tc.want {
				t.Errorf("run = %d, %q; want 1, %q", got.code, got.stderr, tc.want)
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

func TestRunRuntimeErrorExitsOne(t *testing.T) {
	// Two requested recipes cannot take positional arguments (the runner
	// reports a usage error), so run must print it and return 1.
	files := map[string]string{"/w/justfile": "a x:\n  echo\nb:\n  echo\n"}
	got := runFake(t, files, nil, "a", "1", "b")
	if got.code != 1 || !strings.HasPrefix(got.stderr, "error: positional arguments need exactly one") {
		t.Errorf("run = %d, %q; want 1 and the runner's usage error", got.code, got.stderr)
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
		{"flags after words", []string{"a", "-q", "b"}, config{quiet: true, words: []string{"a", "b"}}},
		{"double dash ends flags", []string{"a", "--", "-q", "--list"}, config{words: []string{"a", "-q", "--list"}}},
		{"lone dash is a word", []string{"-"}, config{words: []string{"-"}}},
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
		{[]string{"a", "-lf"}, "a value is required for '--justfile <JUSTFILE>' but none was supplied"},
	} {
		_, err := parseArgs(tc.args)
		var de *diag.Error
		if !errors.As(err, &de) || de.Kind != diag.KindUsage || de.Msg != tc.want {
			t.Errorf("parseArgs(%q) error = %v, want usage error %q", tc.args, err, tc.want)
		}
	}
}

func TestSelectRecipes(t *testing.T) {
	infos := []just.RecipeInfo{
		{Name: "a", Params: []string{"x", "y"}},
		{Name: "b"},
		{Name: "c", Params: []string{"z"}},
	}
	for _, tc := range []struct {
		name        string
		words       []string
		wantRecipes []string
		wantArgs    []string
	}{
		{"none", nil, nil, nil},
		{"one recipe", []string{"b"}, []string{"b"}, []string{}},
		{"arguments fill parameters", []string{"a", "1", "2"}, []string{"a"}, []string{"1", "2"}},
		{"fewer words than parameters", []string{"a", "1"}, []string{"a"}, []string{"1"}},
		{"later words are recipes", []string{"a", "1", "2", "b"}, []string{"a", "b"}, []string{"1", "2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recipes, args, err := selectRecipes(infos, tc.words)
			if err != nil || !reflect.DeepEqual(recipes, tc.wantRecipes) || len(args) != len(tc.wantArgs) {
				t.Errorf("selectRecipes = %q, %q, %v; want %q, %q", recipes, args, err, tc.wantRecipes, tc.wantArgs)
			}
			for i := range tc.wantArgs {
				if args[i] != tc.wantArgs[i] {
					t.Errorf("args = %q, want %q", args, tc.wantArgs)
				}
			}
		})
	}
}

func TestSelectRecipesUnknownName(t *testing.T) {
	infos := []just.RecipeInfo{{Name: "a", Params: []string{"x", "y"}}}
	for _, tc := range []struct {
		name    string
		words   []string
		missing string
	}{
		{"unknown first", []string{"nope"}, "nope"},
		{"unknown later (corpus misc::argument_mismatch_more)", []string{"a", "1", "2", "3"}, "3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := selectRecipes(infos, tc.words)
			want := "justfile does not contain recipe `" + tc.missing + "`"
			var de *diag.Error
			if !errors.As(err, &de) || de.Kind != diag.KindUsage || de.Msg != want {
				t.Errorf("error = %v, want usage error %q", err, want)
			}
		})
	}
}

func TestSelectRecipesCopiesArguments(t *testing.T) {
	words := []string{"a", "1"}
	_, args, err := selectRecipes([]just.RecipeInfo{{Name: "a", Params: []string{"x"}}}, words)
	if err != nil {
		t.Fatal(err)
	}
	args[0] = "changed"
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
		{"sorted by name", []just.RecipeInfo{{Name: "b"}, {Name: "a"}}, "Available recipes:\n    a\n    b\n"},
		{"private hidden", []just.RecipeInfo{{Name: "_p"}, {Name: "a"}}, "Available recipes:\n    a\n"},
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

func TestDescribe(t *testing.T) {
	located := &diag.Error{Kind: diag.KindParse, File: "justfile", Line: 2, Col: 3, Msg: "bad"}
	for _, tc := range []struct {
		err  error
		want string
	}{
		{usage("no good"), "no good"},
		{located, "justfile:2:3: bad"},
		{errors.New("plain"), "plain"},
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

func BenchmarkParseArgs(b *testing.B) {
	args := []string{"-q", "-f", "justfile", "build", "--", "-x", "y"}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := parseArgs(args); err != nil {
			b.Fatal(err)
		}
	}
}
