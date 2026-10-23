package runner

import (
	"context"
	"errors"
	"io/fs"
	"reflect"
	"slices"
	"testing"

	"gitea.com/wimwian/just-go/internal/eval"
	"gitea.com/wimwian/just-go/internal/sys"
	"gitea.com/wimwian/just-go/internal/sys/sysfake"
)

// functionPlan is a plan for a justfile that calls the environment, system and
// runtime functions, on a fake machine in the directory /proj.
func functionPlan(t testing.TB, src string) (Plan, *sysfake.FakeProcess) {
	t.Helper()
	fake := &sysfake.FakeProcess{}
	plan, _, _ := backtickPlan(t, src, fake)
	plan.WorkDir = "/proj"
	plan.Platform = sysfake.FakePlatform{
		GOOS: "darwin", GOARCH: "arm64", CPUs: 10, Process: 4321, Path: "/bin/just-go",
		Executables: map[string]bool{"/opt/bin/tool": true, "/proj/local/tool": true},
	}
	return plan, fake
}

// misc::env_function_as_env_var#1 and its relatives: env reads the environment
// of the plan, and nothing else.
func TestRunEnvFunctionsReadTheEnvironmentOfThePlan(t *testing.T) {
	plan, fake := functionPlan(t, `
a := env_var('X')
b := env_var_or_default('NOPE', 'dflt')
c := env('NOPE', 'two')
foo: (bar env('X') env('Y', 'y'))
bar p q:
  echo {{a}} {{b}} {{c}} {{p}} {{q}}
`)
	plan.Env = []string{"X=z"}
	plan.Recipes = []string{"foo"}
	mustRun(t, plan)
	if got, want := commands(fake), []string{"echo z dflt two z y"}; !slices.Equal(got, want) {
		t.Errorf("ran %q, want %q", got, want)
	}
}

// functions::env_var_failure#1: a missing variable fails the run at the call,
// even though the process the test runs in has one of that name.
func TestRunEnvVarOfAMissingVariableIsARuntimeError(t *testing.T) {
	t.Setenv("ZADDY", "set in the process")
	plan, _ := functionPlan(t, "a:\n  echo {{env_var('ZADDY')}}\n")
	de := failure(t, plan)
	if de.Msg != "environment variable `ZADDY` not present" || de.Line != 2 || de.Col != 10 || de.Width != 7 {
		t.Errorf("error = %+v, want ZADDY missing at 2:10", *de)
	}
}

// functions::test_os_arch_functions_in_default#1 and the other two forms, on a
// machine of known kind.
func TestRunSystemFunctionsAskThePlansPlatform(t *testing.T) {
	plan, fake := functionPlan(t, `
a := arch()
foo o=os() f=os_family() n=num_cpus():
  echo {{a}} {{o}} {{f}} {{n}} {{just_pid()}} {{just_executable()}} {{just_version()}}
`)
	mustRun(t, plan)
	if got, want := commands(fake), []string{"echo aarch64 macos unix 10 4321 /bin/just-go 1.59.0"}; !slices.Equal(got, want) {
		t.Errorf("ran %q, want %q", got, want)
	}
}

// Without a platform in the plan, the system the program runs on answers, under
// the name of Plan.OS when it has one.
func TestRunSystemFunctionsDefaultToTheSystem(t *testing.T) {
	plan, fake := functionPlan(t, "foo:\n  echo {{os()}} {{os_family()}}\n")
	plan.Platform, plan.OS = nil, "windows"
	mustRun(t, plan)
	if got, want := commands(fake), []string{"echo windows windows"}; !slices.Equal(got, want) {
		t.Errorf("ran %q, want %q", got, want)
	}
	plan, fake = functionPlan(t, "foo:\n  echo {{num_cpus()}}\n")
	plan.Platform = nil
	mustRun(t, plan)
	if got := commands(fake); len(got) != 1 || got[0] == "echo 0" || got[0] == "echo " {
		t.Errorf("ran %q, want the number of processors", got)
	}
}

// functions::recipe_name#1, recipe_name_in_dependency_argument#1 and recipe_name_in_parameter_default#1.
func TestRunRecipeNameIsTheNameOfTheRecipeTheCallIsIn(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		recipes   []string
		want      []string
	}{
		{"body", "foo: bar\n  @echo 'foo {{recipe_name()}}'\n\nbar:\n  @echo 'bar {{recipe_name()}}'\n", []string{"foo"},
			[]string{"echo 'bar bar'", "echo 'foo foo'"}},
		{"dependency argument", "foo: (bar recipe_name())\n\nbar x:\n  @echo {{x}}\n", []string{"foo"}, []string{"echo foo"}},
		{"parameter default", "foo x=recipe_name():\n  @echo {{x}}\n", nil, []string{"echo foo"}},
		{"env attribute", "[env('E', recipe_name())]\nfoo:\n  @echo {{`c`}}\n", nil, []string{"c", "echo "}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, fake := functionPlan(t, tc.src)
			plan.Recipes = tc.recipes
			mustRun(t, plan)
			if got := commands(fake); !slices.Equal(got, tc.want) {
				t.Errorf("ran %q, want %q", got, tc.want)
			}
		})
	}
}

// functions::recipe_name_in_assignment_is_an_error#1.
func TestRunRecipeNameInAnAssignmentIsAnError(t *testing.T) {
	plan, _ := functionPlan(t, "x := recipe_name()\nfoo:\n  echo {{x}}\n")
	de := failure(t, plan)
	if de.Msg != "`recipe_name()` can only be used within a recipe" || de.Line != 1 || de.Col != 6 || de.Width != 11 {
		t.Errorf("error = %+v", *de)
	}
}

// functions::is_dependency#1 and #2.
func TestRunIsDependencyTellsWhetherARecipeRunsForAnother(t *testing.T) {
	const src = "alpha: beta\n  @echo 'alpha {{is_dependency()}}'\nbeta: && gamma\n  @echo 'beta {{is_dependency()}}'\n" +
		"gamma:\n  @echo 'gamma {{is_dependency()}}'\n"
	for recipe, want := range map[string][]string{
		"alpha": {"echo 'beta true'", "echo 'gamma true'", "echo 'alpha false'"},
		"beta":  {"echo 'beta false'", "echo 'gamma true'"},
	} {
		plan, fake := functionPlan(t, src)
		plan.Recipes = []string{recipe}
		mustRun(t, plan)
		if got := commands(fake); !slices.Equal(got, want) {
			t.Errorf("%s ran %q, want %q", recipe, got, want)
		}
	}
}

// A dependency that runs in parallel with the others is a dependency too.
func TestRunIsDependencyHoldsInParallelDependencies(t *testing.T) {
	plan, fake := functionPlan(t, "[parallel]\nfoo: a b\n  @echo {{is_dependency()}}\na:\n  @echo a {{is_dependency()}}\nb:\n  @echo b {{is_dependency()}}\n")
	mustRun(t, plan)
	got := commands(fake)
	slices.Sort(got)
	if want := []string{"echo a true", "echo b true", "echo false"}; !slices.Equal(got, want) {
		t.Errorf("ran %q, want %q", got, want)
	}
}

// functions::num_jobs_returns_jobs#1 needs `set lists`, which is unstable; the
// value is the plan's.
func TestRunNumJobsIsThePlansJobs(t *testing.T) {
	plan, fake := functionPlan(t, "foo:\n  echo [{{num_jobs()}}]\n")
	mustRun(t, plan)
	plan.Jobs = 9999
	plan2, fake2 := functionPlan(t, "foo:\n  echo [{{num_jobs()}}]\n")
	plan2.Jobs = 9999
	mustRun(t, plan2)
	if got, want := append(commands(fake), commands(fake2)...), []string{"echo []", "echo [9999]"}; !slices.Equal(got, want) {
		t.Errorf("ran %q, want %q", got, want)
	}
}

// functions::choose#1: the characters come from the plan's source of chance.
func TestRunChooseDrawsFromThePlansRandom(t *testing.T) {
	plan, fake := functionPlan(t, "foo:\n  echo {{choose('3', 'xyz')}}\n")
	plan.Random = &sysfake.FakeRandom{Bytes: []byte{0, 0, 0, 0, 0, 0, 0, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}}
	mustRun(t, plan)
	if got, want := commands(fake), []string{"echo zxy"}; !slices.Equal(got, want) {
		t.Errorf("ran %q, want %q", got, want)
	}
	plan, fake = functionPlan(t, "foo:\n  echo {{choose('8', 'ab')}}\n")
	mustRun(t, plan)
	if got := commands(fake); len(got) != 1 || len(got[0]) != len("echo ")+8 {
		t.Errorf("ran %q, want eight characters", got)
	}
}

// which_function::require_success#1 and require_error#1, with the PATH of the
// plan and a relative entry that starts from the working directory.
func TestRunWhichAndRequireSearchThePlansPATH(t *testing.T) {
	plan, fake := functionPlan(t, "set lists\nfoo:\n  echo {{require('tool')}} [{{which('nope')}}] {{require('./local/tool')}}\n")
	plan.Env = []string{"PATH=/opt/bin:local"}
	mustRun(t, plan)
	if got, want := commands(fake), []string{"echo /opt/bin/tool [] /proj/local/tool"}; !slices.Equal(got, want) {
		t.Errorf("ran %q, want %q", got, want)
	}
	plan, _ = functionPlan(t, "x := require('tool')\nfoo:\n")
	plan.Env = []string{"PATH=/elsewhere"}
	if de := failure(t, plan); de.Msg != "could not find executable `tool`" {
		t.Errorf("error = %q", de.Msg)
	}
}

// functions::shell_args#1 and shell_minimal#1: the command is the shell's
// script, then its own name for $0, then the arguments; the result loses one
// line break.
func TestRunShellFunctionRunsTheCommandWithItsArguments(t *testing.T) {
	plan, fake := functionPlan(t, "x := shell('echo $1 $2', 'justice', 'legs')\nfoo:\n  echo {{x}}\n")
	fake.Outputs = []string{"justice legs\n"}
	plan.Env = []string{"A=1"}
	mustRun(t, plan)
	spec := fake.Specs[0]
	if want := []string{"sh", "-cu", "echo $1 $2", "echo $1 $2", "justice", "legs"}; !slices.Equal(spec.Argv, want) {
		t.Errorf("argv = %q, want %q", spec.Argv, want)
	}
	if spec.Dir != "/proj" || !slices.Equal(spec.Env, []string{"A=1"}) {
		t.Errorf("spec = %+v, want the base directory and the plan's environment", spec)
	}
	if got, want := commands(fake)[1], "echo justice legs"; got != want {
		t.Errorf("ran %q, want %q", got, want)
	}
}

// functions::shell_first_arg#1: with no arguments the shell still gets its
// name; cmd and the PowerShells do not take one.
func TestRunShellFunctionGivesTheShellItsNameUnlessItTakesNone(t *testing.T) {
	for _, tc := range []struct {
		shell string
		want  []string
	}{
		{"sh", []string{"sh", "-cu", "echo $0", "echo $0"}},
		{"/usr/bin/zsh", []string{"/usr/bin/zsh", "-cu", "echo $0", "echo $0"}},
		{"cmd", []string{"cmd", "-cu", "echo $0"}},
		{"powershell.exe", []string{"powershell.exe", "-cu", "echo $0"}},
		{"pwsh", []string{"pwsh", "-cu", "echo $0"}},
	} {
		plan, fake := functionPlan(t, "x := shell('echo $0')\nfoo:\n")
		plan.Shell = tc.shell
		mustRun(t, plan)
		if !slices.Equal(fake.Specs[0].Argv, tc.want) {
			t.Errorf("shell %s: argv = %q, want %q", tc.shell, fake.Specs[0].Argv, tc.want)
		}
	}
}

// attributes::env_attribute_is_visible_to_shell_function#1.
func TestRunShellFunctionSeesTheEnvironmentOfItsBacktickSiblings(t *testing.T) {
	plan, fake := functionPlan(t, "export G := 'g'\n[env('FOO', 'bar')]\nfoo:\n  @echo {{ shell('c') }}\n")
	plan.Env = []string{"HOME=/h"}
	mustRun(t, plan)
	got := environmentOf(fake.Specs[0])
	if got["FOO"] != "bar" || got["G"] != "g" || got["HOME"] != "/h" {
		t.Errorf("env = %v, want FOO, G and HOME", got)
	}
}

// The standard input and error are shared as a backtick's are.
func TestRunShellFunctionSharesStandardErrorAndIsQuietWhenTheRunIs(t *testing.T) {
	plan, fake := functionPlan(t, "x := shell('c')\nfoo:\n")
	mustRun(t, plan)
	if fake.Specs[0].Stderr == nil || fake.Specs[0].Stdout == nil {
		t.Errorf("spec = %+v, want the plan's error and a stream to capture the output", fake.Specs[0])
	}
	plan, fake = functionPlan(t, "x := shell('c')\nfoo:\n")
	plan.Quiet = true
	mustRun(t, plan)
	if fake.Specs[0].Stderr != nil && fake.Specs[0].Stderr == plan.Stderr {
		t.Error("a quiet run gave the command the plan's error stream")
	}
}

// functions::shell_error#1 and the other ways a command fails, worded as
// upstream's OutputError.
func TestRunShellFunctionFailureKinds(t *testing.T) {
	for _, tc := range []struct {
		name string
		fake *sysfake.FakeProcess
		msg  string
	}{
		{"exit status", &sysfake.FakeProcess{Codes: []int{1}}, "shell command failed: process exited with status code 1"},
		{"signal", &sysfake.FakeProcess{Err: &sys.SignalError{Signal: 9}}, "shell command failed: process terminated by signal 9"},
		{"no status", &sysfake.FakeProcess{Codes: []int{-1}}, "shell command failed: process experienced an unknown failure"},
		{"not utf-8", &sysfake.FakeProcess{Outputs: []string{"a\xffb"}},
			"shell command failed: could not convert process stdout to UTF-8: invalid utf-8 sequence of 1 bytes from index 1"},
		{"interrupted", &sysfake.FakeProcess{Errs: []error{&sys.SignalError{Caught: 2}}, Codes: []int{0}},
			"shell command failed: process succeeded but `just` was interrupted by signal SIGINT"},
		{"no shell", &sysfake.FakeProcess{Err: fs.ErrNotExist},
			"shell command failed: could not execute process: No such file or directory (os error 2)"},
		{"other", &sysfake.FakeProcess{Err: errors.New("boom")}, "shell command failed: could not execute process: boom"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, _, _ := backtickPlan(t, "var := shell('exit 1')\nfoo:\n", tc.fake)
			de := failure(t, plan)
			if de.Msg != tc.msg || de.Line != 1 || de.Col != 8 || de.Width != 5 || de.File != "justfile" {
				t.Errorf("error = %+v, want %q at 1:8", *de, tc.msg)
			}
		})
	}
}

// The end of the run is not a failure of the command.
func TestRunShellFunctionCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	plan, _, _ := backtickPlan(t, "x := shell('c')\nfoo:\n", &sysfake.FakeProcess{Err: errors.New("killed")})
	if _, err := Run(ctx, plan); !errors.Is(err, context.Canceled) {
		t.Errorf("Run error = %v, want context.Canceled", err)
	}
}

func TestTakesShellNameUnlessTheShellIsCmdOrAPowerShell(t *testing.T) {
	for program, want := range map[string]bool{
		"sh": true, "bash": true, "/bin/zsh": true, "fish": true, "cmd": false, "cmd.exe": false,
		"powershell": false, "powershell.exe": false, "pwsh": false, "pwsh.exe": false, "mycmd": true,
	} {
		if got := takesShellName(program); got != want {
			t.Errorf("takesShellName(%q) = %v, want %v", program, got, want)
		}
	}
}

func TestProcessFailureWordsEachKind(t *testing.T) {
	for _, tc := range []struct {
		failure *eval.CommandError
		want    string
	}{
		{&eval.CommandError{Kind: eval.ExitStatus, N: 3}, "process exited with status code 3"},
		{&eval.CommandError{Kind: eval.Signaled, N: 9}, "process terminated by signal 9"},
		{&eval.CommandError{Kind: eval.NoStatus}, "process experienced an unknown failure"},
		{&eval.CommandError{Kind: eval.NotUTF8, Detail: "bad"}, "could not convert process stdout to UTF-8: bad"},
		{&eval.CommandError{Kind: eval.Interrupted, N: 15, Detail: "SIGTERM"}, "process succeeded but `just` was interrupted by signal SIGTERM"},
	} {
		if got := processFailure(tc.failure); got != tc.want {
			t.Errorf("processFailure(%+v) = %q, want %q", *tc.failure, got, tc.want)
		}
	}
}

// invocationOf is the invocation that Run would make of plan.
func invocationOf(t *testing.T, plan Plan) *invocation {
	t.Helper()
	pre, err := prepare(plan)
	if err != nil {
		t.Fatal(err)
	}
	return newInvocation(t.Context(), plan, pre, "linux")
}

// The host a backtick's runner hands out names the recipe and whether it is a
// dependency, and holds what the plan has.
func TestBacktickRunnerHostNamesTheSiteOfTheCall(t *testing.T) {
	plan, _ := functionPlan(t, "foo:\n")
	plan.Env = []string{"A=1"}
	h := invocationOf(t, plan).commander(nil, nil, site{recipe: "foo", dependency: true}).(eval.Hosted).Host()
	if h.Recipe != "foo" || !h.IsDependency || h.Shell == nil || h.Version != "1.59.0" || h.Random == nil {
		t.Errorf("host = %+v", h)
	}
	if !reflect.DeepEqual(h.Env, sys.EnvList{"A=1"}) || !reflect.DeepEqual(h.Platform, plan.Platform) {
		t.Errorf("env, platform = %v, %v; want the plan's", h.Env, h.Platform)
	}
}

// The assignments are evaluated outside any recipe.
func TestBacktickRunnerHostOfTheAssignmentsNamesNoRecipe(t *testing.T) {
	plan, _ := functionPlan(t, "foo:\n")
	h := invocationOf(t, plan).commander(nil, nil, site{}).(eval.Hosted).Host()
	if h.Recipe != "" || h.IsDependency {
		t.Errorf("host = %+v, want none of a recipe", h)
	}
}

// A plan with no platform and no chance gets those of the system.
func TestInvocationDefaultsToTheSystemMachine(t *testing.T) {
	plan, _ := functionPlan(t, "foo:\n")
	plan.Platform, plan.Random, plan.OS = nil, nil, "plan9"
	inv := invocationOf(t, plan)
	if inv.platform != (sys.OSPlatform{GOOS: "plan9"}) || inv.random != (sys.OSRandom{}) {
		t.Errorf("platform, random = %#v, %#v", inv.platform, inv.random)
	}
}

func BenchmarkRunSystemFunctions(b *testing.B) {
	plan, _ := functionPlan(b, "foo:\n  echo {{os()}} {{arch()}} {{env_var_or_default('X', 'x')}} {{recipe_name()}} {{is_dependency()}}\n")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Run(context.Background(), plan); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRunShellFunction(b *testing.B) {
	plan, _ := functionPlan(b, "foo:\n  echo {{shell('echo $1', 'a')}}\n")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Run(context.Background(), plan); err != nil {
			b.Fatal(err)
		}
	}
}
