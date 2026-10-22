package just

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/loader"
	"gitea.com/wimwian/just-go/internal/sys"
	"gitea.com/wimwian/just-go/internal/sys/sysfake"
)

// mustParse parses src from memory and fails the test on any error.
func mustParse(t testing.TB, src string) *Justfile {
	t.Helper()
	j, err := Parse("justfile", []byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return j
}

func TestEmbedCapturesStdoutWithoutOSStdout(t *testing.T) {
	j := mustParse(t, "r:\n\t@echo hi\n")
	var stdout, stderr bytes.Buffer
	res, err := j.Run(context.Background(), Options{WorkDir: t.TempDir(), Stdout: &stdout, Stderr: &stderr})
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("Run = %+v, %v", res, err)
	}
	if got := stdout.String(); got != "hi\n" {
		t.Errorf("stdout = %q, want %q", got, "hi\n")
	}
	if got := stderr.String(); got != "" {
		t.Errorf("stderr = %q, want empty", got)
	}
}

func TestEmbedDoesNotInheritEnv(t *testing.T) {
	t.Setenv("JUST_GO_PROBE", "1")
	j := mustParse(t, "r:\n\t@echo ${JUST_GO_PROBE-unset}\n")
	var stdout bytes.Buffer
	if _, err := j.Run(context.Background(), Options{WorkDir: t.TempDir(), Stdout: &stdout}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := stdout.String(); got != "unset\n" {
		t.Errorf("stdout = %q, want %q", got, "unset\n")
	}
}

func TestEmbedContextCancelStopsChild(t *testing.T) {
	j := mustParse(t, "r:\n\t@sleep 30\n")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := j.Run(ctx, Options{WorkDir: t.TempDir()})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run error = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("Run took %v after cancellation, want prompt return", elapsed)
	}
}

func TestEmbedTwoRunsDoNotInterfere(t *testing.T) {
	j := mustParse(t, "r word:\n\t@echo {{word}}\n\t@echo {{word}}\n")
	dir := t.TempDir()
	words := []string{"alpha", "beta"}
	outs := make([]bytes.Buffer, len(words))
	errs := make([]error, len(words))
	var wg sync.WaitGroup
	for i, w := range words {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = j.Run(context.Background(), Options{WorkDir: dir, Args: []string{w}, Stdout: &outs[i]})
		}()
	}
	wg.Wait()
	for i, w := range words {
		if errs[i] != nil {
			t.Fatalf("run %d: %v", i, errs[i])
		}
		want := w + "\n" + w + "\n"
		if got := outs[i].String(); got != want {
			t.Errorf("run %d stdout = %q, want %q", i, got, want)
		}
	}
}

// forbidden are the process-state accesses the library must never make.
func forbidden() []string {
	return []string{"os.Exit", "os.Args", "os.Environ(", "os.Stdout", "os.Stderr"}
}

// libraryFiles returns the non-test .go files of the module root (not its
// subdirectories) and of everything under internal/.
func libraryFiles(t *testing.T) []string {
	t.Helper()
	root, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	files := root
	err = fs.WalkDir(os.DirFS("."), "internal", func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(path, ".go") {
			files = append(files, path)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	var lib []string
	for _, f := range files {
		if !strings.HasSuffix(f, "_test.go") {
			lib = append(lib, f)
		}
	}
	return lib
}

func TestLibraryNeverExitsOrReadsProcessState(t *testing.T) {
	files := libraryFiles(t)
	if len(files) == 0 {
		t.Fatal("no library files found")
	}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for n, line := range strings.Split(string(src), "\n") {
			for _, bad := range forbidden() {
				if strings.Contains(line, bad) {
					t.Errorf("%s:%d: uses %s", f, n+1, bad)
				}
			}
		}
	}
}

// The tests below cover the rest of the API: Load, Parse errors, Recipes,
// and the mapping of Options onto the runner's plan.

func TestLoadExplicitJustfile(t *testing.T) {
	fsys := sysfake.FakeFS{"/p/build.just": "r:\n\techo hi\n"}
	j, err := Load(Options{Justfile: "/p/build.just", FS: fsys})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := j.Recipes(); len(got) != 1 || got[0].Name != "r" {
		t.Errorf("Recipes = %+v", got)
	}
}

func TestLoadSearchesUpward(t *testing.T) {
	fsys := sysfake.FakeFS{"/p/justfile": "r:\n\techo hi\n"}
	j, err := Load(Options{SearchFrom: "/p/a/b", FS: fsys})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	proc := &sysfake.FakeProcess{}
	if _, err := j.Run(context.Background(), Options{Exec: proc}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(proc.Specs) != 1 || proc.Specs[0].Dir != "/p" {
		t.Errorf("specs = %+v, want one run in /p", proc.Specs)
	}
}

func TestLoadNoJustfile(t *testing.T) {
	_, err := Load(Options{SearchFrom: "/p", FS: sysfake.FakeFS{}})
	if !errors.Is(err, loader.ErrNoJustfile) {
		t.Errorf("err = %v, want ErrNoJustfile", err)
	}
}

func TestLoadNeedsJustfileOrSearchFrom(t *testing.T) {
	_, err := Load(Options{FS: sysfake.FakeFS{}})
	var d *diag.Error
	if !errors.As(err, &d) || d.Kind != diag.KindUsage {
		t.Errorf("err = %v, want a usage *diag.Error", err)
	}
}

func TestLoadReturnsParseError(t *testing.T) {
	_, err := Load(Options{Justfile: "/p/justfile", FS: sysfake.FakeFS{"/p/justfile": "r:\n\techo {{x\n"}})
	var d *diag.Error
	if !errors.As(err, &d) || d.File != "/p/justfile" {
		t.Errorf("err = %v, want a *diag.Error in /p/justfile", err)
	}
}

func TestLoadNilFSUsesOS(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "justfile")
	if err := os.WriteFile(path, []byte("r:\n\t@pwd\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	j, err := Load(Options{SearchFrom: dir})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var stdout bytes.Buffer
	if _, err := j.Run(context.Background(), Options{Stdout: &stdout}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := filepath.EvalSymlinks(strings.TrimSpace(stdout.String()))
	if err != nil {
		t.Fatalf("pwd printed %q: %v", stdout.String(), err)
	}
	if got != want {
		t.Errorf("recipe ran in %q, want the justfile's directory %q", got, want)
	}
}

func TestParseErrorsNameTheFile(t *testing.T) {
	cases := map[string]string{
		"parse":    "r:\n\techo {{x\n",
		"analysis": "r: missing\n\techo hi\n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse("mem.just", []byte(src))
			var d *diag.Error
			if !errors.As(err, &d) || d.File != "mem.just" {
				t.Errorf("err = %v, want a *diag.Error naming mem.just", err)
			}
		})
	}
}

func TestRecipesReportsNameDocAndParams(t *testing.T) {
	j := mustParse(t, "# builds it\nbuild target mode='fast':\n\techo hi\n\ntest:\n\techo t\n")
	want := []RecipeInfo{
		{Name: "build", Doc: "builds it", Params: []string{"target", "mode"}, Defaults: []string{"", "'fast'"}},
		{Name: "test", Params: []string{}, Defaults: []string{}},
	}
	if got := j.Recipes(); !reflect.DeepEqual(got, want) {
		t.Errorf("Recipes = %+v, want %+v", got, want)
	}
}

func TestRecipesReturnsFreshSlices(t *testing.T) {
	j := mustParse(t, "r a:\n\techo hi\n")
	j.Recipes()[0].Params[0] = "changed"
	if got := j.Recipes()[0].Params[0]; got != "a" {
		t.Errorf("Params[0] = %q after caller mutation, want %q", got, "a")
	}
}

func TestRecipesReportsDefaultsAsWritten(t *testing.T) {
	j := mustParse(t, "r a='x y' b=\"t\\tz\" c=a d=(a + 'e'):\n\techo hi\n")
	want := []string{"'x y'", `"t\tz"`, "a", "(a + 'e')"}
	if got := j.Recipes()[0].Defaults; !reflect.DeepEqual(got, want) {
		t.Errorf("Defaults = %q, want %q", got, want)
	}
}

// TestRunCallsCarryOwnArguments runs two calls of one recipe with different
// arguments, which Recipes and Args cannot express.
func TestRunCallsCarryOwnArguments(t *testing.T) {
	j := mustParse(t, "a:\n\techo a\nb x:\n\techo {{x}}\n")
	proc := &sysfake.FakeProcess{}
	opts := Options{WorkDir: "/w", Exec: proc, Calls: []Call{{Recipe: "a"}, {Recipe: "b", Args: []string{"1"}}, {Recipe: "b", Args: []string{"2"}}}}
	if _, err := j.Run(context.Background(), opts); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var got []string
	for _, s := range proc.Specs {
		got = append(got, s.Argv[len(s.Argv)-1])
	}
	if want := []string{"echo a", "echo 1", "echo 2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("commands = %q, want %q", got, want)
	}
}

func TestRunMapsOptionsToPlan(t *testing.T) {
	j := mustParse(t, "a:\n\techo a\nb x:\n\techo {{x}}\n")
	proc := &sysfake.FakeProcess{}
	stdin := strings.NewReader("in")
	var stdout, stderr bytes.Buffer
	opts := Options{
		WorkDir: "/w",
		Env:     []string{"K=V"},
		Stdin:   stdin,
		Stdout:  &stdout,
		Stderr:  &stderr,
		Recipes: []string{"b"},
		Args:    []string{"arg"},
		Quiet:   true,
		Exec:    proc,
	}
	if _, err := j.Run(context.Background(), opts); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(proc.Specs) != 1 {
		t.Fatalf("specs = %+v, want one", proc.Specs)
	}
	checkSpecCarries(t, proc.Specs[0], opts)
	if stderr.Len() != 0 {
		t.Errorf("Quiet run echoed %q", stderr.String())
	}
}

// checkSpecCarries asserts that spec runs "echo arg" with the directory,
// environment and streams of opts.
func checkSpecCarries(t *testing.T, spec sys.ProcessSpec, opts Options) {
	t.Helper()
	if spec.Dir != opts.WorkDir || !reflect.DeepEqual(spec.Env, opts.Env) ||
		spec.Stdin != opts.Stdin || spec.Stdout != opts.Stdout || spec.Stderr != opts.Stderr {
		t.Errorf("spec = %+v does not carry the options", spec)
	}
	if got := spec.Argv[len(spec.Argv)-1]; got != "echo arg" {
		t.Errorf("command = %q, want %q", got, "echo arg")
	}
}

func TestRunReportsExitCode(t *testing.T) {
	j := mustParse(t, "r:\n\texit 3\n")
	var stderr bytes.Buffer
	res, err := j.Run(context.Background(), Options{WorkDir: "/w", Stderr: &stderr, Exec: &sysfake.FakeProcess{Codes: []int{3}}})
	if err != nil || res.ExitCode != 3 {
		t.Errorf("Run = %+v, %v, want exit code 3", res, err)
	}
}

func TestRunParsedJustfileNeedsWorkDir(t *testing.T) {
	j := mustParse(t, "r:\n\techo hi\n")
	proc := &sysfake.FakeProcess{}
	_, err := j.Run(context.Background(), Options{Exec: proc})
	var d *diag.Error
	if !errors.As(err, &d) || d.Kind != diag.KindUsage {
		t.Errorf("err = %v, want a usage *diag.Error", err)
	}
	if len(proc.Specs) != 0 {
		t.Errorf("ran %d commands, want none", len(proc.Specs))
	}
}

func BenchmarkParse(b *testing.B) {
	var src strings.Builder
	for i := range 100 {
		fmt.Fprintf(&src, "r%d p:\n\techo {{p}}\n", i)
	}
	text := []byte(src.String())
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Parse("justfile", text); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRun(b *testing.B) {
	j := mustParse(b, "a:\n\techo a\nb: a\n\techo b\nc: b\n\techo c\n")
	opts := Options{WorkDir: "/w", Recipes: []string{"c"}, Quiet: true}
	b.ReportAllocs()
	for b.Loop() {
		opts.Exec = &sysfake.FakeProcess{}
		if _, err := j.Run(context.Background(), opts); err != nil {
			b.Fatal(err)
		}
	}
}

func TestRunShellOverridesSetShell(t *testing.T) {
	j := mustParse(t, "set shell := [\"bash\", \"-c\"]\nr:\n\techo hi\n")
	proc := &sysfake.FakeProcess{}
	if _, err := j.Run(context.Background(), Options{WorkDir: "/w", Exec: proc, Shell: "zsh"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if want := []string{"zsh", "-cu", "echo hi"}; !reflect.DeepEqual(proc.Specs[0].Argv, want) {
		t.Errorf("argv = %q; want %q", proc.Specs[0].Argv, want)
	}
}

func TestRunShellArgsOverrideSetShell(t *testing.T) {
	j := mustParse(t, "set shell := [\"bash\", \"-c\"]\nr:\n\techo hi\n")
	for _, tc := range []struct {
		name  string
		shell string
		args  []string
		want  []string
	}{
		{"args alone use sh", "", []string{"-e", "-c"}, []string{"sh", "-e", "-c", "echo hi"}},
		{"shell with args", "zsh", []string{"-c"}, []string{"zsh", "-c", "echo hi"}},
		{"empty non-nil args", "zsh", []string{}, []string{"zsh", "echo hi"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proc := &sysfake.FakeProcess{}
			opts := Options{WorkDir: "/w", Exec: proc, Shell: tc.shell, ShellArgs: tc.args}
			if _, err := j.Run(context.Background(), opts); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if !reflect.DeepEqual(proc.Specs[0].Argv, tc.want) {
				t.Errorf("argv = %q; want %q", proc.Specs[0].Argv, tc.want)
			}
		})
	}
}

// The attributes of a recipe reach the host as data.
func TestRecipesReportsPrivateGroupsAndDoc(t *testing.T) {
	src := "# comment\n[group('b'), group('a')]\nfoo:\n" +
		"_bar:\n" +
		"# hidden\n[private]\nbaz:\n" +
		"# suppressed\n[doc]\nqux:\n" +
		"prefix := 'hello '\n[doc(prefix + 'world ')]\nquux:\n" +
		"# comment\n[doc('   ')]\nblank:\n"
	var got []RecipeInfo
	for _, r := range mustParse(t, src).Recipes() {
		got = append(got, RecipeInfo{Name: r.Name, Doc: r.Doc, Private: r.Private, Groups: r.Groups})
	}
	want := []RecipeInfo{
		{Name: "foo", Doc: "comment", Groups: []string{"a", "b"}},
		{Name: "_bar", Private: true},
		{Name: "baz", Doc: "hidden", Private: true},
		{Name: "qux"},
		{Name: "quux", Doc: "hello world"},
		{Name: "blank"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Recipes =\n %+v\nwant\n %+v", got, want)
	}
}

func TestRecipesGroupsAreFresh(t *testing.T) {
	j := mustParse(t, "[group('a')]\nr:\n")
	j.Recipes()[0].Groups[0] = "changed"
	if got := j.Recipes()[0].Groups[0]; got != "a" {
		t.Errorf("Groups[0] = %q after caller mutation, want a", got)
	}
}

func TestRunDefaultAttributeIsTheDefaultRecipe(t *testing.T) {
	j := mustParse(t, "foo:\n\techo foo\n[default]\nbar:\n\techo bar\n")
	proc := &sysfake.FakeProcess{}
	if _, err := j.Run(context.Background(), Options{WorkDir: "/w", Exec: proc}); err != nil {
		t.Fatal(err)
	}
	if got := proc.Specs[0].Argv[len(proc.Specs[0].Argv)-1]; got != "echo bar" {
		t.Errorf("ran %q, want echo bar", got)
	}
}

func TestRunConfirmReadsOptionsStdinAndYesSkipsIt(t *testing.T) {
	j := mustParse(t, "[confirm('Sure?')]\nfoo:\n\techo foo\n")
	for _, tc := range []struct {
		name  string
		opts  Options
		ran   int
		stdin string
		err   string
	}{
		{"yes on stdin", Options{Stdin: strings.NewReader("yes\n")}, 1, "Sure? ", ""},
		{"no on stdin", Options{Stdin: strings.NewReader("no\n")}, 0, "Sure? ", "recipe `foo` was not confirmed"},
		{"no stdin", Options{}, 0, "Sure? ", "recipe `foo` was not confirmed"},
		{"the yes option", Options{Yes: true}, 1, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proc := &sysfake.FakeProcess{}
			var stderr bytes.Buffer
			tc.opts.WorkDir, tc.opts.Exec, tc.opts.Stderr = "/w", proc, &stderr
			_, err := j.Run(context.Background(), tc.opts)
			var d *diag.Error
			switch {
			case tc.err == "" && err != nil, tc.err != "" && (!errors.As(err, &d) || d.Msg != tc.err):
				t.Errorf("err = %v, want %q", err, tc.err)
			}
			if len(proc.Specs) != tc.ran || !strings.HasPrefix(stderr.String(), tc.stdin) {
				t.Errorf("ran %d, stderr %q; want %d, prefix %q", len(proc.Specs), &stderr, tc.ran, tc.stdin)
			}
		})
	}
}

func TestRunConfirmPromptGoesToThePromptOption(t *testing.T) {
	j := mustParse(t, "[confirm('Sure?')]\nfoo:\n\techo foo\n")
	var stderr, prompt bytes.Buffer
	opts := Options{WorkDir: "/w", Exec: &sysfake.FakeProcess{}, Stderr: &stderr, Prompt: &prompt, Stdin: strings.NewReader("y\n")}
	if _, err := j.Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if prompt.String() != "Sure? " || strings.Contains(stderr.String(), "Sure?") {
		t.Errorf("prompt %q, stderr %q", &prompt, &stderr)
	}
}

func TestParseReportsAttributeErrors(t *testing.T) {
	for src, msg := range map[string]string{
		"[private('x')]\nfoo:\n":                    "attribute `private` got 1 argument but takes 0 arguments",
		"[doc(nope)]\nfoo:\n":                       "variable `nope` not defined",
		"[default]\nfoo:\n[default]\nbar:\n":        "recipe `bar` has duplicate `[default]` attribute, which may only appear once per module",
		"[no-cd]\n[working-directory('x')]\nfoo:\n": "recipe `foo` has both `[no-cd]` and `[working-directory]` attributes",
	} {
		_, err := Parse("justfile", []byte(src))
		var d *diag.Error
		if !errors.As(err, &d) || d.Msg != msg || d.Source == "" {
			t.Errorf("Parse(%q) = %+v, want %q with its source line", src, err, msg)
		}
	}
}

func TestParseReportsGroupBAttributeErrors(t *testing.T) {
	for src, msg := range map[string]string{
		"[env('A')]\nfoo:\n":                       "attribute `env` got 1 argument but takes 2 arguments",
		"[env(x, 'a')]\nfoo x:\n":                  "variable `x` not defined",
		"[continue('SIGTERM')]\nfoo:\n":            "invalid signal `SIGTERM`: expected `SIGHUP`, `SIGINT`, or `SIGQUIT`",
		"[script, shell]\nfoo:\n":                  "recipe `foo` has both `[script]` and `[shell]` attributes",
		"[extension: '.txt']\nfoo:\n":              "shell recipe `foo` has script recipe attribute `extension`",
		"[metadata]\nfoo:\n":                       "attribute `metadata` got 0 arguments but takes at least 1 argument",
		"[timestamp(nope)]\nfoo:\n":                "variable `nope` not defined",
		"[parallel, parallel]\nfoo:\n":             "attribute `parallel` first used on line 1 is duplicated on line 1",
		"[linux]\n[windows]\nset nonsense\nfoo:\n": "unknown setting `nonsense`",
	} {
		_, err := Parse("justfile", []byte(src))
		var d *diag.Error
		if !errors.As(err, &d) || d.Msg != msg || d.Source == "" {
			t.Errorf("Parse(%q) = %+v, want %q with its source line", src, err, msg)
		}
	}
}

func TestRunTimestampUsesOptionsClock(t *testing.T) {
	j := mustParse(t, "[timestamp('%Y-%m-%d %H:%M:%S')]\nr:\n\techo hi\n")
	proc := &sysfake.FakeProcess{}
	var stderr bytes.Buffer
	opts := Options{
		WorkDir: "/w", Stderr: &stderr, Exec: proc,
		Clock: sysfake.FakeClock{At: sys.Time{Unix: 994518299, UTC: true}},
	}
	if _, err := j.Run(context.Background(), opts); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if want := "[2001-07-07 15:04:59] echo hi\n"; stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
}

func TestRunEnvAttributeReachesTheCommand(t *testing.T) {
	j := mustParse(t, "name := 'world'\n[env('GREETING', 'hello ' + name)]\nr:\n\t@echo $GREETING\n")
	proc := &sysfake.FakeProcess{}
	if _, err := j.Run(context.Background(), Options{WorkDir: "/w", Exec: proc, Env: []string{"K=V"}}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if want := []string{"K=V", "GREETING=hello world"}; !reflect.DeepEqual(proc.Specs[0].Env, want) {
		t.Errorf("environment = %q, want %q", proc.Specs[0].Env, want)
	}
}

func TestParseKeepsTheRecipesOfTheHostSystem(t *testing.T) {
	j := mustParse(t, "[windows]\nw:\n\n[unix]\nu:\n")
	var names []string
	for _, r := range j.Recipes() {
		names = append(names, r.Name)
	}
	want := []string{"u"}
	if runtime.GOOS == "windows" {
		want = []string{"w"}
	}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("recipes on %s = %v, want %v", runtime.GOOS, names, want)
	}
}
