package runner

import (
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/eval"
	"gitea.com/wimwian/just-go/internal/parser"
	"gitea.com/wimwian/just-go/internal/settings"
	"gitea.com/wimwian/just-go/internal/sys"
	"gitea.com/wimwian/just-go/internal/sys/sysfake"
)

// envOf returns the variables of a spec's environment as a map.
func envOf(spec sys.ProcessSpec) map[string]string {
	out := map[string]string{}
	for _, kv := range spec.Env {
		k, v, _ := strings.Cut(kv, "=")
		out[k] = v
	}
	return out
}

// The cases below are the corpus's export group: set export, the export
// keyword, and `$` parameters.
func TestRunSetExportExportsGlobalsAndParameters(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	plan, _, _ := newPlan(t, "set export\nA := 'hello'\n\nfoo B C='c':\n  echo $A\n", fake)
	plan.Recipes, plan.Args = []string{"foo"}, []string{"goodbye"}
	mustRun(t, plan)
	want := map[string]string{"A": "hello", "B": "goodbye", "C": "c"}
	if got := envOf(fake.Specs[0]); !reflect.DeepEqual(got, want) {
		t.Errorf("env = %v, want %v", got, want)
	}
}

func TestRunSetExportFalseExportsNothing(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	plan, _, _ := newPlan(t, "set export := false\nA := 'hello'\n\nfoo:\n  echo\n", fake)
	mustRun(t, plan)
	if got := envOf(fake.Specs[0]); len(got) != 0 {
		t.Errorf("env = %v, want none", got)
	}
}

func TestRunExportKeywordAndDollarParameters(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	src := "export FOO := 'a'\nbaz := 'c'\nexport BAR := 'b'\nexport ABC := FOO + '-' + BAR + '-' + baz\n\n" +
		"wut $P='p' Q='q':\n  echo\n"
	plan, _, _ := newPlan(t, src, fake)
	mustRun(t, plan)
	want := map[string]string{"FOO": "a", "BAR": "b", "ABC": "a-b-c", "P": "p"}
	if got := envOf(fake.Specs[0]); !reflect.DeepEqual(got, want) {
		t.Errorf("env = %v, want %v", got, want)
	}
}

// A parameter that shadows an exported global does not unexport it, as in
// upstream's Environment.
func TestRunNonExportedParameterKeepsTheExportedGlobal(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	plan, _, _ := newPlan(t, "export A := 'g'\nfoo A='p':\n  echo {{A}}\n", fake)
	mustRun(t, plan)
	if got := envOf(fake.Specs[0])["A"]; got != "g" {
		t.Errorf("A = %q, want g", got)
	}
	if got := commands(fake)[0]; got != "echo p" {
		t.Errorf("command = %q, want the parameter in the text", got)
	}
}

func TestRunExportedEnvironmentReplacesThePlansAndIsSorted(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	plan, _, _ := newPlan(t, "export Z := 'z'\nexport A := 'a'\nfoo:\n  echo\n", fake)
	plan.Env = []string{"KEEP=1", "A=old", "NOEQUALS"}
	mustRun(t, plan)
	want := []string{"KEEP=1", "NOEQUALS", "A=a", "Z=z"}
	if got := fake.Specs[0].Env; !reflect.DeepEqual(got, want) {
		t.Errorf("env = %q, want %q", got, want)
	}
}

func TestRunWithoutExportsLeavesTheEnvironmentAsGiven(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	plan, _, _ := newPlan(t, "foo:\n  echo\n", fake)
	plan.Env = []string{"A=1"}
	mustRun(t, plan)
	if got := fake.Specs[0].Env; !reflect.DeepEqual(got, []string{"A=1"}) {
		t.Errorf("env = %q", got)
	}
}

func TestMergeEnv(t *testing.T) {
	base := []string{"A=1", "B=2", "C"}
	for _, tc := range []struct {
		name  string
		extra map[string]string
		want  []string
	}{
		{"none", nil, base},
		{"replaces and appends in name order", map[string]string{"B": "x", "Z": "z", "D": "d"}, []string{"A=1", "C", "B=x", "D=d", "Z=z"}},
		{"an empty value", map[string]string{"A": ""}, []string{"B=2", "C", "A="}},
	} {
		if got := mergeEnv(base, tc.extra); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: mergeEnv = %q, want %q", tc.name, got, tc.want)
		}
	}
	if !hasKey(map[string]string{"k": ""}, "k") || hasKey(nil, "k") {
		t.Error("hasKey is wrong")
	}
}

func BenchmarkMergeEnv(b *testing.B) {
	base := make([]string, 40)
	for i := range base {
		base[i] = fmt.Sprintf("VAR%d=value", i)
	}
	extra := map[string]string{"A": "a", "VAR3": "x", "Z": "z"}
	b.ReportAllocs()
	for b.Loop() {
		_ = mergeEnv(base, extra)
	}
}

// The cases below are the corpus's positional_arguments group.
func TestRunPositionalArgumentsFollowTheCommand(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		args      []string
		want      []string
	}{
		{"positional_arguments::shell#1", "set positional-arguments\n\nfoo bar baz:\n  echo $1\n",
			[]string{"hello", "goodbye"}, []string{"sh", "-cu", "echo $1", "foo", "hello", "goodbye"}},
		{"positional_arguments::default_arguments#1", "set positional-arguments\n\nfoo bar='baz':\n  echo $1\n",
			nil, []string{"sh", "-cu", "echo $1", "foo", "baz"}},
		{"no parameters", "set positional-arguments\nfoo:\n  echo $0\n", nil, []string{"sh", "-cu", "echo $0", "foo"}},
		{"setting off", "set positional-arguments := false\nfoo a:\n  echo $1\n",
			[]string{"x"}, []string{"sh", "-cu", "echo $1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &sysfake.FakeProcess{}
			plan, _, _ := newPlan(t, tc.src, fake)
			plan.Recipes, plan.Args = []string{"foo"}, tc.args
			mustRun(t, plan)
			if got := fake.Specs[0].Argv; !reflect.DeepEqual(got, tc.want) {
				t.Errorf("argv = %q, want %q", got, tc.want)
			}
		})
	}
}

// The cases below are the corpus's working_directory and no_cd groups.
func TestRunWorkingDirectory(t *testing.T) {
	base := t.TempDir()
	invocation := filepath.Join(base, "inv")
	abs := filepath.Join(base, "elsewhere")
	for _, tc := range []struct {
		name, src, invocation, want string
	}{
		{"none", "foo:\n  echo\n", "", base},
		{"working_directory::setting", "set working-directory := 'bar'\nfoo:\n  echo\n", "", filepath.Join(base, "bar")},
		{"nested", "set working-directory := 'a/b'\nfoo:\n  echo\n", "", filepath.Join(base, "a", "b")},
		{"absolute", "set working-directory := '" + abs + "'\nfoo:\n  echo\n", "", abs},
		{"expression", "d := 'x'\nset working-directory := d + '-y'\nfoo:\n  echo\n", "", filepath.Join(base, "x-y")},
		{"no_cd::setting_applies_to_recipes", "set no-cd := true\nfoo:\n  echo\n", invocation, invocation},
		{"no-cd without an invocation directory", "set no-cd\nfoo:\n  echo\n", "", base},
		{"working_directory::false_no_cd_does_not_conflict_with_working_directory#1",
			"set no-cd := false\nset working-directory := 'foo'\nfoo:\n  echo\n", invocation, filepath.Join(base, "foo")},
		{"no-cd false", "set no-cd := false\nfoo:\n  echo\n", invocation, base},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &sysfake.FakeProcess{}
			plan, _, _ := newPlan(t, tc.src, fake)
			plan.WorkDir, plan.InvocationDir = base, tc.invocation
			mustRun(t, plan)
			if got := fake.Specs[0].Dir; got != tc.want {
				t.Errorf("Dir = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRunWorkingDirectorySettingErrorsBeforeAnythingRuns(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	plan, _, _ := newPlan(t, "set working-directory := nope\nfoo:\n  echo\n", fake)
	_, err := Run(t.Context(), plan)
	var de *diag.Error
	if !errors.As(err, &de) || de.Msg != "variable `nope` not defined" || len(fake.Specs) != 0 {
		t.Errorf("err = %v, ran %d", err, len(fake.Specs))
	}
}

func TestWorkingDirectoryIsPure(t *testing.T) {
	jf := &ast.Justfile{}
	dir := "sub"
	plan := Plan{Justfile: jf, WorkDir: "/w"}
	if got := workingDirectory(plan, settings.Resolved{WorkingDirectory: &dir}); got != filepath.Join("/w", "sub") {
		t.Errorf("workingDirectory = %q", got)
	}
}

func TestJustfileShell(t *testing.T) {
	res := settings.Resolved{
		Shell:        []string{"bash", "-c"},
		WindowsShell: []string{"cmd.exe", "/c"},
	}
	for _, tc := range []struct {
		name string
		set  ast.Settings
		res  settings.Resolved
		goos string
		want []string
	}{
		{"nothing", ast.Settings{}, settings.Resolved{}, "linux", nil},
		{"set shell", ast.Settings{}, res, "linux", res.Shell},
		{"windows-shell is ignored off Windows", ast.Settings{}, res, "darwin", res.Shell},
		{"windows-shell wins on Windows", ast.Settings{}, res, "windows", res.WindowsShell},
		{"powershell on Windows", ast.Settings{WindowsPowerShell: true}, settings.Resolved{Shell: res.Shell}, "windows",
			[]string{"powershell.exe", "-NoLogo", "-Command"}},
		{"windows-shell beats powershell", ast.Settings{WindowsPowerShell: true}, res, "windows", res.WindowsShell},
		{"powershell is ignored off Windows", ast.Settings{WindowsPowerShell: true}, res, "linux", res.Shell},
		{"set shell on Windows", ast.Settings{}, settings.Resolved{Shell: res.Shell}, "windows", res.Shell},
	} {
		if got := justfileShell(tc.set, tc.res, tc.goos); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: justfileShell = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestRunPicksTheWindowsShellOnWindows(t *testing.T) {
	src := "set shell := ['bash', '-c']\nset windows-shell := ['pwsh', '-Command']\nfoo:\n  echo\n"
	for goos, want := range map[string][]string{
		"windows": {"pwsh", "-Command", "echo"},
		"linux":   {"bash", "-c", "echo"},
		"":        nil, // the platform the test runs on: either of the two shells
	} {
		fake := &sysfake.FakeProcess{}
		plan, _, _ := newPlan(t, src, fake)
		plan.OS = goos
		mustRun(t, plan)
		argv := fake.Specs[0].Argv
		if want != nil && !reflect.DeepEqual(argv, want) {
			t.Errorf("OS %q: argv = %q, want %q", goos, argv, want)
		}
		if len(argv) < 2 || argv[0] == "" || argv[len(argv)-1] != "echo" {
			t.Errorf("OS %q: argv = %q, want a non-empty shell followed by the command", goos, argv)
		}
	}
}

// The cases below are the corpus's no_exit_message group.
func TestRunNoExitMessageSilencesTheFailureMessage(t *testing.T) {
	src := "set no-exit-message\n\n# This is a doc comment\nhello:\n  @echo 'Hello, World!'\n  @exit 100\n"
	fake := &sysfake.FakeProcess{Codes: []int{0, 100}}
	plan, _, stderr := newPlan(t, src, fake)
	if res := mustRun(t, plan); res.ExitCode != 100 || stderr.Len() != 0 {
		t.Errorf("exit %d, stderr %q; want 100 and nothing", res.ExitCode, stderr)
	}
}

func TestRunNoExitMessageSilencesTheSignalMessageOnly(t *testing.T) {
	for _, tc := range []struct {
		name   string
		sig    *sys.SignalError
		exit   int
		stderr string
	}{
		{"terminated", &sys.SignalError{Signal: 15}, 143, ""},
		{"interrupted", &sys.SignalError{Caught: 2}, 130, "error: interrupted by SIGINT\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code := -1
			if tc.sig.Signal == 0 {
				code = 0 // a fatal signal was caught while the command ran to the end
			}
			fake := &sysfake.FakeProcess{Codes: []int{code}, Errs: []error{tc.sig}}
			plan, _, stderr := newPlan(t, "set no-exit-message\ndefault:\n  @kill -TERM $$\n", fake)
			if res := mustRun(t, plan); res.ExitCode != tc.exit || stderr.String() != tc.stderr {
				t.Errorf("exit %d, stderr %q; want %d, %q", res.ExitCode, stderr, tc.exit, tc.stderr)
			}
		})
	}
}

func TestRunNoExitMessageOffKeepsTheMessage(t *testing.T) {
	fake := &sysfake.FakeProcess{Codes: []int{100}}
	plan, _, stderr := newPlan(t, "set no-exit-message := false\nhello:\n  @exit 100\n", fake)
	mustRun(t, plan)
	if want := "error: recipe `hello` failed on line 3 with exit code 100\n"; stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}
}

// The cases below are the corpus's guards group.
func TestRunGuardLines(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		codes     []int
		ran       int
		exit      int
		err       string
	}{
		{"guard_lines_halt_execution", "set guards\n\n@foo:\n  ?[[ 'foo' == 'bar' ]]\n  echo baz\n", []int{1}, 1, 0, ""},
		{"guard_lines_have_no_effect_if_successful", "set guards\n\n@foo:\n  ?[[ 'foo' == 'foo' ]]\n  echo baz\n", []int{0, 0}, 2, 0, ""},
		{"exit_codes_above_one_are_reserved", "set guards\n\n@foo:\n  ?exit 2\n",
			[]int{2}, 1, 1, "guard line in recipe `foo` on line 4 returned reserved exit code 2"},
		{"guard_lines_are_ignored_without_setting", "@foo:\n  ?() { echo bar; }; ?\n", []int{0}, 1, 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &sysfake.FakeProcess{Codes: tc.codes}
			plan, _, _ := newPlan(t, tc.src, fake)
			res, err := Run(t.Context(), plan)
			if res.ExitCode != tc.exit || len(fake.Specs) != tc.ran {
				t.Errorf("exit %d, ran %d; want %d, %d", res.ExitCode, len(fake.Specs), tc.exit, tc.ran)
			}
			var de *diag.Error
			switch {
			case tc.err == "" && err != nil:
				t.Errorf("err = %v", err)
			case tc.err != "" && (!errors.As(err, &de) || de.Msg != tc.err):
				t.Errorf("err = %v, want %q", err, tc.err)
			}
		})
	}
}

// A guard that fails ends only its recipe: the recipes after it still run.
func TestRunFailedGuardEndsOnlyItsRecipe(t *testing.T) {
	fake := &sysfake.FakeProcess{Codes: []int{0, 1, 0}}
	plan, _, _ := newPlan(t, "set guards\nfoo: bar\n  ?false\n  echo no\nbar:\n  @echo bar\nbaz: foo\n  @echo baz\n", fake)
	plan.Recipes = []string{"baz"}
	mustRun(t, plan)
	if got, want := commands(fake), []string{"echo bar", "false", "echo baz"}; !reflect.DeepEqual(got, want) {
		t.Errorf("commands = %q, want %q", got, want)
	}
}

func TestRunGuardWithSignalIsAnOrdinaryFailure(t *testing.T) {
	fake := &sysfake.FakeProcess{Codes: []int{-1}, Errs: []error{&sys.SignalError{Signal: 9}}}
	plan, _, stderr := newPlan(t, "set guards\nfoo:\n  ?sleep 9\n", fake)
	if res := mustRun(t, plan); res.ExitCode != 137 || !strings.Contains(stderr.String(), "terminated on line 3 by signal 9") {
		t.Errorf("exit %d, stderr %q", res.ExitCode, stderr)
	}
}

// The cases below are the corpus's ignore_comments group.
func TestRunIgnoreCommentsSkipsCommentLines(t *testing.T) {
	src := "set ignore-comments\n\nsome_recipe:\n  # A recipe-internal comment\n  echo something-useful\n"
	fake := &sysfake.FakeProcess{}
	plan, _, stderr := newPlan(t, src, fake)
	mustRun(t, plan)
	if got, want := commands(fake), []string{"echo something-useful"}; !reflect.DeepEqual(got, want) {
		t.Errorf("commands = %q, want %q", got, want)
	}
	if want := "echo something-useful\n"; stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}
}

func TestRunIgnoreCommentsDoesNotEvaluateComments(t *testing.T) {
	// error() is a function the evaluator cannot call, so evaluating the
	// comment would fail the run.
	fake := &sysfake.FakeProcess{}
	plan, _, _ := newPlan(t, "set ignore-comments\nsome_recipe:\n  # {{ error('foo') }}\n", fake)
	mustRun(t, plan)
	if len(fake.Specs) != 0 {
		t.Errorf("ran %d commands", len(fake.Specs))
	}
}

func TestRunIgnoreCommentsOffRunsComments(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	plan, _, stderr := newPlan(t, "set ignore-comments := false\nsome_recipe:\n  # comment \\\n  echo something-useful\n", fake)
	mustRun(t, plan)
	if want := "# comment echo something-useful\n"; stderr.String() != want || len(fake.Specs) != 1 {
		t.Errorf("stderr = %q, ran %d", stderr, len(fake.Specs))
	}
}

// The cases below are the corpus's allow_duplicate_recipes and
// allow_duplicate_variables groups.
func TestRunDuplicateRecipeRunsTheLastDefinition(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	plan, _, _ := newPlan(t, "b:\n  echo foo\nb:\n  echo bar\n\nset allow-duplicate-recipes\n", fake)
	mustRun(t, plan)
	if got, want := commands(fake), []string{"echo bar"}; !reflect.DeepEqual(got, want) {
		t.Errorf("commands = %q, want %q", got, want)
	}
	fake = &sysfake.FakeProcess{}
	plan, _, _ = newPlan(t, "b a:\n  echo foo\nb c d:\n  echo bar {{c}} {{d}}\nset allow-duplicate-recipes\n", fake)
	plan.Recipes, plan.Args = []string{"b"}, []string{"one", "two"}
	mustRun(t, plan)
	if got, want := commands(fake), []string{"echo bar one two"}; !reflect.DeepEqual(got, want) {
		t.Errorf("commands = %q, want %q", got, want)
	}
}

func TestRunDefaultRecipeWithDuplicatesIsTheFirstByPosition(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	plan, _, _ := newPlan(t, "a:\n  echo a1\nb:\n  echo b\na:\n  echo a2\nset allow-duplicate-recipes\n", fake)
	mustRun(t, plan)
	// The surviving `a` is the one on line 5, so `b`, on line 3, comes first.
	if got, want := commands(fake), []string{"echo b"}; !reflect.DeepEqual(got, want) {
		t.Errorf("commands = %q, want %q", got, want)
	}
}

func TestRunDuplicateVariableTakesTheLastValue(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	plan, _, _ := newPlan(t, "a := 'foo'\na := 'bar'\nset allow-duplicate-variables\nb:\n  echo {{a}}\n", fake)
	mustRun(t, plan)
	if got, want := commands(fake), []string{"echo bar"}; !reflect.DeepEqual(got, want) {
		t.Errorf("commands = %q, want %q", got, want)
	}
}

// The cases below are the corpus's lazy group. A call of extension on a name
// with no extension is what fails to evaluate, so an assignment that uses one
// fails when it is evaluated.
func TestRunLazyEvaluatesOnlyWhatIsUsed(t *testing.T) {
	const bad = "x := extension('a')\n"
	for _, tc := range []struct {
		name, src string
		fails     bool
	}{
		{"lazy::unused_assignment_not_evaluated", "set lazy\n" + bad + "foo:\n  @echo foo\n", false},
		{"lazy::used_assignment_evaluated", "set lazy\n" + bad + "foo:\n  @echo {{x}}\n", true},
		{"lazy::transitively_used_assignment_evaluated", "set lazy\n" + bad + "y := x\nfoo:\n  @echo {{y}}\n", true},
		{"lazy::assignment_used_in_parameter_default_evaluated", "set lazy\n" + bad + "foo val=x:\n", true},
		{"lazy::assignment_used_in_dependency_evaluated", "set lazy\n" + bad + "foo: bar\nbar:\n  @echo {{x}}\n", true},
		{"lazy::assignment_used_in_transitive_dependency_evaluated", "set lazy\n" + bad + "foo: bar\nbar: baz\nbaz:\n  @echo {{x}}\n", true},
		{"lazy::assignment_used_in_dependency_argument_evaluated", "set lazy\n" + bad + "foo: (bar x)\nbar val:\n", true},
		{"after a subsequent", "set lazy\n" + bad + "foo: && bar\nbar:\n  @echo {{x}}\n", true},
		{"lazy::eager_assignments_are_evaluated", "set lazy\neager " + bad + "foo:\n", true},
		{"an exported assignment", "set lazy\nexport " + bad + "foo:\n", true},
		{"set export evaluates all", "set lazy\nset export\n" + bad + "foo:\n", true},
		{"lazy::multiple_invocations_evaluate_union", "set lazy\nx := 'foo'\ny := 'bar'\nz := extension('a')\na:\n  @echo {{x}}\nb:\n  @echo {{y}}\n", false},
		{"a parameter shadows the assignment", "set lazy\n" + bad + "foo x='q':\n  @echo {{x}}\n", false},
		{"a later parameter does not shadow an earlier default", "set lazy\n" + bad + "foo a=x x='q':\n", true},
		{"a comment is not read under ignore-comments", "set lazy\nset ignore-comments\n" + bad + "foo:\n  # {{x}}\n", false},
		{"a comment is read without it", "set lazy\n" + bad + "foo:\n  # {{x}}\n  @echo\n", true},
		{"without lazy everything is evaluated", bad + "foo:\n  @echo foo\n", true},
		{"lazy := false", "set lazy := false\n" + bad + "foo:\n  @echo foo\n", true},
		{"an unused assignment of another recipe", "set lazy\n" + bad + "foo:\n  @echo foo\nbar:\n  @echo {{x}}\n", false},
		{"used in an env value", "set lazy\n" + bad + "[env('A', x)]\nfoo:\n", true},
		{"used in an env name", "set lazy\n" + bad + "[env(x, 'a')]\nfoo:\n", true},
		{"used in a timestamp format", "set lazy\n" + bad + "[timestamp(x)]\nfoo:\n", true},
		{"used in a confirm prompt", "set lazy\n" + bad + "[confirm(x)]\nfoo:\n", true},
		{"used in a working directory", "set lazy\n" + bad + "[working-directory(x)]\nfoo:\n", true},
		{"used in a doc", "set lazy\n" + bad + "[doc(x)]\nfoo:\n", true},
		{"a parameter shadows it in a working directory", "set lazy\n" + bad + "[working-directory(x)]\nfoo x='q':\n", false},
		{"a parameter does not shadow it in an env", "set lazy\n" + bad + "[env('A', x)]\nfoo x='q':\n", true},
		{"literal attributes use nothing", "set lazy\n" + bad + "[group('x'), env('A', 'b')]\nfoo:\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, _, _ := newPlan(t, tc.src, &sysfake.FakeProcess{})
			_, err := Run(t.Context(), plan)
			if (err != nil) != tc.fails {
				t.Errorf("Run error = %v, want failure: %v", err, tc.fails)
			}
		})
	}
}

func TestUsedAssignmentsIsNilWithoutLazy(t *testing.T) {
	jf, err := parser.Parse("justfile", []byte("x := 'a'\nfoo:\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := usedAssignments(jf, []Call{{Recipe: "foo"}}, nil); got != nil {
		t.Errorf("usedAssignments = %v, want nil", got)
	}
}

func TestUsedAssignmentsFollowsAssignmentsAndDependencies(t *testing.T) {
	src := "set lazy\na := b + 'x'\nb := c\nc := 'c'\nunused := 'u'\nd := 'd'\n" +
		"foo: bar (baz d)\n  echo {{a}}\nbar:\nbaz x:\n"
	jf, err := parser.Parse("justfile", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	got := usedAssignments(jf, []Call{{Recipe: "foo"}}, nil)
	var names []string
	for name := range got {
		names = append(names, name)
	}
	sort.Strings(names)
	if want := []string{"a", "b", "c", "d"}; !reflect.DeepEqual(names, want) {
		t.Errorf("usedAssignments = %v, want %v", names, want)
	}
	// An unknown recipe, which requested() refuses before this is called, is
	// skipped.
	if got := usedAssignments(jf, []Call{{Recipe: "missing"}}, nil); len(got) != 0 {
		t.Errorf("usedAssignments of an unknown recipe = %v", got)
	}
}

func TestWantedFiltersAssignments(t *testing.T) {
	all := []ast.Assignment{{Name: "a"}, {Name: "b"}, {Name: "c"}}
	if got := wanted(all, nil); len(got) != 3 {
		t.Errorf("wanted(nil) = %v", got)
	}
	if got := wanted(all, map[string]bool{"b": true, "z": true}); len(got) != 1 || got[0].Name != "b" {
		t.Errorf("wanted = %v", got)
	}
}

func BenchmarkUsedAssignments(b *testing.B) {
	var sb strings.Builder
	sb.WriteString("set lazy\n")
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&sb, "v%d := 'x'", i)
		if i > 0 {
			fmt.Fprintf(&sb, " + v%d", i-1)
		}
		sb.WriteString("\n")
	}
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&sb, "r%d:", i)
		if i+1 < 100 {
			fmt.Fprintf(&sb, " r%d", i+1)
		}
		fmt.Fprintf(&sb, "\n  echo {{v%d}}\n", i)
	}
	jf, err := parser.Parse("justfile", []byte(sb.String()))
	if err != nil {
		b.Fatal(err)
	}
	calls := []Call{{Recipe: "r0"}}
	b.ReportAllocs()
	for b.Loop() {
		_ = usedAssignments(jf, calls, nil)
	}
}

func BenchmarkExportedEnv(b *testing.B) {
	jf := &ast.Justfile{Settings: ast.Settings{Export: true}}
	r := &ast.Recipe{Params: []ast.Param{{Name: "p"}, {Name: "q", Exported: true}}}
	globals := eval.Scope{}
	var exported []string
	for i := 0; i < 50; i++ {
		name := fmt.Sprintf("G%d", i)
		globals[name] = "v"
		exported = append(exported, name)
	}
	scope := eval.Scope{"p": "1", "q": "2"}
	b.ReportAllocs()
	for b.Loop() {
		_ = paramEnv(jf, r, scope, globalEnv(exported, globals, len(r.Params)))
	}
}

func TestExportedGlobalsAreTheExportedOnesOrAllUnderSetExport(t *testing.T) {
	jf := &ast.Justfile{Assignments: []ast.Assignment{{Name: "a", Exported: true}, {Name: "b"}, {Name: "a", Exported: true}}}
	if got := exportedGlobals(jf); !reflect.DeepEqual(got, []string{"a"}) {
		t.Errorf("exportedGlobals = %v", got)
	}
	jf.Settings.Export = true
	if got := exportedGlobals(jf); !reflect.DeepEqual(got, []string{"b", "a"}) {
		t.Errorf("exportedGlobals under set export = %v", got)
	}
	if got := globalEnv(nil, eval.Scope{}, 0); got != nil {
		t.Errorf("globalEnv of nothing = %v, want nil", got)
	}
	if got := paramEnv(&ast.Justfile{}, &ast.Recipe{}, eval.Scope{}, nil); got != nil {
		t.Errorf("paramEnv of nothing = %v, want nil", got)
	}
}

// set lazy evaluates the assignments a recipe uses. A comment that set
// ignore-comments skips does not use any, but a script has no such comment.
func TestUsedAssignmentsCountsTheCommentsOfScripts(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       []string
	}{
		{"a shell recipe skips them", "foo:\n  # {{a}}\n  echo {{b}}\n", []string{"b"}},
		{"a shebang does not", "foo:\n  #!/bin/sh\n  # {{a}}\n  echo {{b}}\n", []string{"a", "b"}},
		{"a script attribute does not", "[script('sh')]\nfoo:\n  # {{a}}\n  echo {{b}}\n", []string{"a", "b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			jf, err := parser.Parse("justfile", []byte("set lazy\nset ignore-comments\na := 'a'\nb := 'b'\n"+tc.body))
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			for name := range usedAssignments(jf, []Call{{Recipe: "foo"}}, nil) {
				names = append(names, name)
			}
			sort.Strings(names)
			if !reflect.DeepEqual(names, tc.want) {
				t.Errorf("usedAssignments = %v, want %v", names, tc.want)
			}
		})
	}
}

// Under set lazy, an assignment that a --set override replaces is not
// evaluated, so the assignments its value names are not in use either.
func TestUsedAssignmentsDoesNotFollowAnOverride(t *testing.T) {
	jf, err := parser.Parse("justfile", []byte("set lazy\n\nx := `exit 1`\ny := x\n\nfoo:\n  @echo {{ y }}\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := usedAssignments(jf, []Call{{Recipe: "foo"}}, map[string]string{"y": "bar"}); got["x"] || !got["y"] {
		t.Errorf("used = %v; want y in use and x not", got)
	}
}
