package runner

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"reflect"
	"strings"
	"testing"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/eval"
	"gitea.com/wimwian/just-go/internal/sys"
	"gitea.com/wimwian/just-go/internal/sys/sysfake"
)

// backtickPlan is newPlan for a justfile that has backticks: the plan keeps the
// text, as the loader does, so that errors can quote their line.
func backtickPlan(t testing.TB, src string, fake *sysfake.FakeProcess) (Plan, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	plan, stdout, stderr := newPlan(t, src, fake)
	plan.Justfile.Source = []byte(src)
	return plan, stdout, stderr
}

// failure runs the plan and returns the *diag.Error it fails with.
func failure(t *testing.T, plan Plan) *diag.Error {
	t.Helper()
	_, err := Run(t.Context(), plan)
	var de *diag.Error
	if !errors.As(err, &de) {
		t.Fatalf("Run error = %v, want a *diag.Error", err)
	}
	return de
}

// The command of a backtick runs through the shell, in the base directory,
// and its output, without its last line break, is the value.
func TestRunBacktickInALine(t *testing.T) {
	fake := &sysfake.FakeProcess{Outputs: []string{"hello\n"}}
	plan, _, _ := backtickPlan(t, "foo:\n  echo {{`greet --loud`}}\n", fake)
	plan.Env = []string{"A=1"}
	mustRun(t, plan)
	if want := []string{"greet --loud", "echo hello"}; !reflect.DeepEqual(commands(fake), want) {
		t.Fatalf("commands = %q, want %q", commands(fake), want)
	}
	spec := fake.Specs[0]
	if want := []string{"sh", "-cu", "greet --loud"}; !reflect.DeepEqual(spec.Argv, want) || spec.Dir != plan.WorkDir ||
		!reflect.DeepEqual(spec.Env, []string{"A=1"}) {
		t.Errorf("spec = %+v, want the default shell in %s with the plan's environment", spec, plan.WorkDir)
	}
}

func TestRunBacktickUsesTheShellOfTheJustfileAndThePlan(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	plan, _, _ := backtickPlan(t, "set shell := ['bash', '-c']\nx := `bar`\nfoo:\n", fake)
	mustRun(t, plan)
	if want := []string{"bash", "-c", "bar"}; !reflect.DeepEqual(fake.Specs[0].Argv, want) {
		t.Errorf("argv = %q, want %q", fake.Specs[0].Argv, want)
	}
	fake = &sysfake.FakeProcess{}
	plan, _, _ = backtickPlan(t, "set shell := ['bash', '-c']\nx := `bar`\nfoo:\n", fake)
	plan.Shell = "zsh"
	mustRun(t, plan)
	if want := []string{"zsh", "-cu", "bar"}; !reflect.DeepEqual(fake.Specs[0].Argv, want) {
		t.Errorf("argv = %q, want %q", fake.Specs[0].Argv, want)
	}
}

// Only one line break is removed from the end of the output.
func TestRunBacktickTrimsOneLineBreak(t *testing.T) {
	for output, want := range map[string]string{
		"a": "a", "a\n": "a", "a\n\n": "a\n", "a\r\n": "a", "a\r\n\r\n": "a\r\n", "\n": "", "a\rb": "a\rb", "": "",
	} {
		fake := &sysfake.FakeProcess{Outputs: []string{output}}
		plan, _, _ := backtickPlan(t, "x := `c`\nfoo:\n  @echo '{{x}}'\n", fake)
		mustRun(t, plan)
		if want := "echo '" + want + "'"; commands(fake)[1] != want {
			t.Errorf("output %q gave %q, want %q", output, commands(fake)[1], want)
		}
	}
}

func TestTrimLineBreak(t *testing.T) {
	for in, want := range map[string]string{"": "", "\n": "", "\r\n": "", "a\n": "a", "a\r\n": "a", "a\n\n": "a\n", "a\r": "a\r"} {
		if got := trimLineBreak(in); got != want {
			t.Errorf("trimLineBreak(%q) = %q, want %q", in, got, want)
		}
	}
}

// A command reads the standard input of the run and writes to its standard
// error, which --quiet silences.
func TestRunBacktickSharesStandardInputAndError(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	plan, _, stderr := backtickPlan(t, "x := `c`\nfoo:\n", fake)
	in := strings.NewReader("input")
	plan.Stdin = in
	mustRun(t, plan)
	spec := fake.Specs[0]
	if spec.Stdin != io.Reader(in) || spec.Stderr != io.Writer(stderr) || spec.Stdout == nil {
		t.Errorf("spec = %+v, want the plan's input and error, and a stream to capture the output", spec)
	}
	fake = &sysfake.FakeProcess{}
	plan, _, _ = backtickPlan(t, "x := `c`\nfoo:\n", fake)
	plan.Quiet = true
	mustRun(t, plan)
	if fake.Specs[0].Stderr != io.Discard {
		t.Errorf("a quiet run gave the command %v for its error stream, want io.Discard", fake.Specs[0].Stderr)
	}
}

// A backtick runs where upstream's evaluator does: in the directory of the
// justfile with set working-directory applied, wherever the recipes run.
func TestRunBacktickDirectory(t *testing.T) {
	for _, tc := range []struct{ name, src, sub string }{
		{"plain", "x := `c`\nfoo:\n", ""},
		{"working-directory", "set working-directory := 'sub'\nx := `c`\nfoo:\n", "sub"},
		{"no-cd", "set no-cd\nx := `c`\nfoo:\n", ""},
		{"a recipe's working directory", "foo:\n  @echo {{`c`}}\n\n[working-directory('w')]\nbar:\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &sysfake.FakeProcess{}
			plan, _, _ := backtickPlan(t, tc.src, fake)
			plan.InvocationDir = "/elsewhere"
			mustRun(t, plan)
			want := plan.WorkDir
			if tc.sub != "" {
				want += "/" + tc.sub
			}
			if fake.Specs[0].Dir != want {
				t.Errorf("Dir = %q, want %q", fake.Specs[0].Dir, want)
			}
		})
	}
}

// What the backticks of each kind of expression see in their environment, as
// upstream's evaluators show it. envs[i] is the environment of the i-th
// command to run.
func TestRunBacktickEnvironment(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		args      []string
		// which command to look at, and what it has and lacks.
		at        int
		has, lack map[string]string
	}{
		{"a global sees nothing exported", "export A := 'a'\nx := `c`\nfoo:\n", nil, 0, nil, map[string]string{"A": "a"}},
		{"a line sees the exported globals", "export A := 'a'\nB := 'b'\nfoo:\n  @echo {{`c`}}\n", nil, 0,
			map[string]string{"A": "a"}, map[string]string{"B": "b"}},
		{"a line sees every global under set export", "set export\nA := 'a'\nfoo:\n  @echo {{`c`}}\n", nil, 0,
			map[string]string{"A": "a"}, nil},
		{"a line sees the exported parameters", "foo $P Q:\n  @echo {{`c`}}\n", []string{"p", "q"}, 0,
			map[string]string{"P": "p"}, map[string]string{"Q": "q"}},
		{"a default does not see the exported parameters", "foo $P Q=`c`:\n", []string{"p"}, 0,
			nil, map[string]string{"P": "p"}},
		{"a default sees the exported globals", "export A := 'a'\nfoo Q=`c`:\n", nil, 0, map[string]string{"A": "a"}, nil},
		{"a default sees the env attribute", "[env('E', 'e')]\nfoo Q=`c`:\n", nil, 0, map[string]string{"E": "e"}, nil},
		{"a line sees the env attribute", "[env('E', 'e')]\nfoo:\n  @echo {{`c`}}\n", nil, 0, map[string]string{"E": "e"}, nil},
		{"the env attribute replaces an exported global", "export E := 'old'\n[env('E', 'new')]\nfoo:\n  @echo {{`c`}}\n", nil, 0,
			map[string]string{"E": "new"}, nil},
		{"an env attribute sees the ones before it", "[env('A', 'a'), env('B', `c`)]\nfoo:\n", nil, 0, map[string]string{"A": "a"}, nil},
		{"a dependency argument sees the exported parameters", "foo $P: (bar `c`)\nbar x:\n", []string{"p"}, 0,
			map[string]string{"P": "p"}, nil},
		{"a dependency argument does not see the env attribute", "[env('E', 'e')]\nfoo: (bar `c`)\nbar x:\n", nil, 0,
			nil, map[string]string{"E": "e"}},
		{"a confirm prompt does not see the env attribute", "[env('E', 'e'), confirm(`c`)]\nfoo:\n", nil, 0,
			nil, map[string]string{"E": "e"}},
		{"a working directory sees the env attribute", "[env('E', 'e'), working-directory(`c`)]\nfoo:\n", nil, 0,
			map[string]string{"E": "e"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &sysfake.FakeProcess{}
			plan, _, _ := backtickPlan(t, tc.src, fake)
			plan.Env = []string{"HOME=/h"}
			plan.Stdin = strings.NewReader("y\n")
			plan.Calls = []Call{{Recipe: "foo", Args: tc.args}}
			mustRun(t, plan)
			got := environmentOf(fake.Specs[tc.at])
			if got["HOME"] != "/h" {
				t.Errorf("env = %v, want the plan's HOME", got)
			}
			for k, v := range tc.has {
				if got[k] != v {
					t.Errorf("env[%s] = %q, want %q (env %v)", k, got[k], v, got)
				}
			}
			for k := range tc.lack {
				if _, ok := got[k]; ok {
					t.Errorf("env has %s=%q, want it absent", k, got[k])
				}
			}
		})
	}
}

// The commands of a recipe see what its backticks see, plus its parameters.
func TestRunBacktickEnvironmentAgreesWithTheCommands(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	plan, _, _ := backtickPlan(t, "export A := 'a'\n[env('E', 'e')]\nfoo:\n  @echo {{`c`}}\n", fake)
	mustRun(t, plan)
	if !reflect.DeepEqual(environmentOf(fake.Specs[0]), environmentOf(fake.Specs[1])) {
		t.Errorf("backtick env %v, command env %v; want the same", environmentOf(fake.Specs[0]), environmentOf(fake.Specs[1]))
	}
}

// Failures are located at the backtick and quote its line; the run ends with the
// status of the command. A line that comes before the failing one has run, and
// has echoed.
func TestRunBacktickFailure(t *testing.T) {
	fake := &sysfake.FakeProcess{Codes: []int{0, 222}}
	plan, _, stderr := backtickPlan(t, "foo:\n echo hello\n echo {{`exit 222`}}\n", fake)
	de := failure(t, plan)
	want := diag.Error{
		Kind: diag.KindRuntime, File: "justfile", Line: 3, Col: 9, Width: 10, Msg: "backtick failed with exit code 222",
		Source: " echo {{`exit 222`}}", ExitCode: 222,
	}
	if *de != want {
		t.Errorf("error = %+v, want %+v", *de, want)
	}
	if stderr.String() != "echo hello\n" || !reflect.DeepEqual(commands(fake), []string{"echo hello", "exit 222"}) {
		t.Errorf("stderr %q, commands %q; want the first line to have run and echoed", stderr, commands(fake))
	}
}

func TestRunBacktickFailureKinds(t *testing.T) {
	for _, tc := range []struct {
		name string
		fake *sysfake.FakeProcess
		msg  string
		code int
	}{
		{"exit status", &sysfake.FakeProcess{Codes: []int{7}}, "backtick failed with exit code 7", 7},
		{"signal", &sysfake.FakeProcess{Err: &sys.SignalError{Signal: 9}}, "backtick was terminated by signal 9", 137},
		{"no status", &sysfake.FakeProcess{Codes: []int{-1}}, "backtick failed for an unknown reason", 1},
		{"not utf-8", &sysfake.FakeProcess{Outputs: []string{"a\xffb"}}, "backtick succeeded but stdout was not utf8: invalid utf-8 sequence of 1 bytes from index 1", 1},
		{"interrupted", &sysfake.FakeProcess{Errs: []error{&sys.SignalError{Caught: 2}}, Codes: []int{0}},
			"backtick succeeded but `just` was interrupted by signal SIGINT", 130},
		{"no shell", &sysfake.FakeProcess{Err: fs.ErrNotExist}, "backtick could not be run because just could not find the shell:\nfile does not exist", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, _, _ := backtickPlan(t, "x := `c`\nfoo:\n", tc.fake)
			de := failure(t, plan)
			if de.Msg != tc.msg || de.ExitCode != tc.code || de.Line != 1 || de.Col != 6 || de.Width != 3 || de.File != "justfile" {
				t.Errorf("error = %+v, want %q ending with %d at 1:6", *de, tc.msg, tc.code)
			}
		})
	}
}

// The status wins over the output, and the output over the signal caught.
func TestCommandFailureOrder(t *testing.T) {
	for _, tc := range []struct {
		name string
		got  sys.Captured
		want eval.FailureKind
	}{
		{"fine", sys.Captured{Stdout: []byte("ok")}, 0},
		{"signal over status", sys.Captured{ExitCode: -1, Signal: 9, Caught: 2}, eval.Signaled},
		{"status over output", sys.Captured{ExitCode: 3, Stdout: []byte("\xff"), Caught: 2}, eval.ExitStatus},
		{"no status", sys.Captured{ExitCode: -1}, eval.NoStatus},
		{"output over caught", sys.Captured{Stdout: []byte("\xff"), Caught: 2}, eval.NotUTF8},
		{"caught", sys.Captured{Caught: 15}, eval.Interrupted},
	} {
		got := commandFailure(tc.got)
		switch {
		case tc.want == 0 && got != nil, tc.want != 0 && (got == nil || got.Kind != tc.want):
			t.Errorf("%s: commandFailure = %+v, want kind %v", tc.name, got, tc.want)
		}
	}
	if got := commandFailure(sys.Captured{Caught: 15}); got.Detail != "SIGTERM" || got.N != 15 {
		t.Errorf("caught signal = %+v, want SIGTERM", got)
	}
}

// The end of the run is not a failure of the command.
func TestRunBacktickCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	plan, _, _ := backtickPlan(t, "x := `c`\nfoo:\n", &sysfake.FakeProcess{Err: errors.New("killed")})
	if _, err := Run(ctx, plan); !errors.Is(err, context.Canceled) {
		t.Errorf("Run error = %v, want context.Canceled", err)
	}
}

// The assignments are evaluated first, by name and each after the ones it
// uses, and the first failure ends everything.
func TestRunEvaluatesAssignmentsBeforeAnythingElse(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	plan, _, _ := backtickPlan(t, "foo:\n  @echo {{`line`}}\nz := `z`\nb := a + `b`\na := `a`\nc := `c`\n", fake)
	mustRun(t, plan)
	if want := []string{"a", "b", "c", "z", "line", "echo "}; !reflect.DeepEqual(commands(fake), want) {
		t.Errorf("commands = %q, want %q", commands(fake), want)
	}
}

func TestRunFailingAssignmentStopsBeforeAnyRecipe(t *testing.T) {
	fake := &sysfake.FakeProcess{Codes: []int{100}}
	plan, _, stderr := backtickPlan(t, "b := a\na := `exit 100`\nbar:\n echo '{{`exit 200`}}'\n", fake)
	de := failure(t, plan)
	if de.ExitCode != 100 || de.Line != 2 || de.Col != 6 || de.Source != "a := `exit 100`" || stderr.Len() != 0 || len(fake.Specs) != 1 {
		t.Errorf("error = %+v, %d commands, stderr %q; want the assignment's failure and nothing else", *de, len(fake.Specs), stderr)
	}
}

func TestEvaluationOrder(t *testing.T) {
	jf := parse(t, "d := 'd'\nb := a + c\nc := a\na := 'a'\ne := e2\ne2 := 'x'\nf := nope\n")
	var got []string
	for _, a := range evaluationOrder(jf.UniqueAssignments()) {
		got = append(got, a.Name)
	}
	if want := []string{"a", "c", "b", "d", "e2", "e", "f"}; !reflect.DeepEqual(got, want) {
		t.Errorf("evaluationOrder = %v, want %v", got, want)
	}
}

// A cycle the analyzer missed does not loop.
func TestEvaluationOrderFinishesOnACycle(t *testing.T) {
	jf := parse(t, "a := b\nb := a\n")
	if got := evaluationOrder(jf.UniqueAssignments()); len(got) != 2 {
		t.Errorf("evaluationOrder = %+v, want both assignments once", got)
	}
}

// With set lazy only the assignments that are used are evaluated.
func TestRunLazyRunsOnlyTheBackticksOfWhatIsUsed(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	plan, _, _ := backtickPlan(t, "set lazy\nx := `x`\ny := `y`\nz := `z`\nfoo:\n  @echo {{y}}\n", fake)
	mustRun(t, plan)
	if want := []string{"y", "echo "}; !reflect.DeepEqual(commands(fake), want) {
		t.Errorf("commands = %q, want %q", commands(fake), want)
	}
}

// Dependency arguments, parameter defaults and the attributes are evaluated
// when the recipe is reached, so what ran before them has run, and has
// written, when they fail (Task 5 F4).
func TestRunDependencyArgumentIsEvaluatedWhenTheDependencyIsReached(t *testing.T) {
	fake := &sysfake.FakeProcess{Codes: []int{0, 1}}
	plan, _, stderr := backtickPlan(t, "first:\n  echo first\nsecond: (third `exit 1`)\nthird x:\n  echo {{x}}\n", fake)
	plan.Calls = []Call{{Recipe: "first"}, {Recipe: "second"}}
	de := failure(t, plan)
	if want := []string{"echo first", "exit 1"}; !reflect.DeepEqual(commands(fake), want) {
		t.Errorf("commands = %q, want %q", commands(fake), want)
	}
	if stderr.String() != "echo first\n" || de.Msg != "backtick failed with exit code 1" || de.Line != 3 || de.Col != 16 {
		t.Errorf("stderr %q, error %+v; want first to have run before the argument failed", stderr, *de)
	}
}

// The same holds without a backtick: a failing argument is reported after the
// recipes that come before it have run.
func TestRunLaterArgumentFailureComesAfterEarlierRecipes(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	plan, _, _ := backtickPlan(t, "first:\n  @echo first\nsecond: (third nope())\nthird x:\n", fake)
	plan.Calls = []Call{{Recipe: "first"}, {Recipe: "second"}}
	if _, err := Run(t.Context(), plan); err == nil {
		t.Fatal("Run succeeded with an argument that cannot be evaluated")
	}
	if want := []string{"echo first"}; !reflect.DeepEqual(commands(fake), want) {
		t.Errorf("commands = %q, want %q", commands(fake), want)
	}
}

// All the arguments of a list are evaluated before the first of its
// dependencies runs.
func TestRunEvaluatesAllArgumentsOfAListFirst(t *testing.T) {
	fake := &sysfake.FakeProcess{Outputs: []string{"A", "B"}}
	plan, _, _ := backtickPlan(t, "top: (one `a`) (two `b`)\none x:\n  @echo {{x}}\ntwo x:\n  @echo {{x}}\n", fake)
	mustRun(t, plan)
	if want := []string{"a", "b", "echo A", "echo B"}; !reflect.DeepEqual(commands(fake), want) {
		t.Errorf("commands = %q, want %q", commands(fake), want)
	}
}

// ... and so are those of a list that runs at once.
func TestRunEvaluatesAllArgumentsOfAParallelListFirst(t *testing.T) {
	fake := &sysfake.FakeProcess{Codes: []int{0, 5}}
	plan, _, _ := backtickPlan(t, "[parallel]\ntop: (one `a`) (two `exit 5`)\none x:\n  @echo {{x}}\ntwo x:\n  @echo {{x}}\n", fake)
	de := failure(t, plan)
	if de.ExitCode != 5 || len(fake.Specs) != 2 {
		t.Errorf("error %+v after %d commands; want the second argument to fail before either recipe ran", *de, len(fake.Specs))
	}
}

// A parameter's default is evaluated when its recipe is reached, not before the
// recipes that come first run.
func TestRunParameterDefaultIsEvaluatedWhenTheRecipeIsReached(t *testing.T) {
	fake := &sysfake.FakeProcess{Codes: []int{0, 4}}
	plan, _, stderr := backtickPlan(t, "first:\n  echo first\nsecond x=`exit 4`:\n  echo {{x}}\n", fake)
	plan.Calls = []Call{{Recipe: "first"}, {Recipe: "second"}}
	de := failure(t, plan)
	if de.ExitCode != 4 || stderr.String() != "echo first\n" {
		t.Errorf("error %+v, stderr %q; want first to have run before the default failed", *de, stderr)
	}
}

// A default is not evaluated when the argument is given.
func TestRunParameterDefaultIsNotEvaluatedWhenGiven(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	plan, _, _ := backtickPlan(t, "foo x=`c`:\n  @echo {{x}}\n", fake)
	plan.Calls = []Call{{Recipe: "foo", Args: []string{"given"}}}
	mustRun(t, plan)
	if want := []string{"echo given"}; !reflect.DeepEqual(commands(fake), want) {
		t.Errorf("commands = %q, want %q", commands(fake), want)
	}
}

// A default may use the parameters before it, with a backtick.
func TestRunParameterDefaultsSeeEarlierParameters(t *testing.T) {
	fake := &sysfake.FakeProcess{Outputs: []string{"out"}}
	plan, _, _ := backtickPlan(t, "foo a b=(a + `c`):\n  @echo {{b}}\n", fake)
	plan.Calls = []Call{{Recipe: "foo", Args: []string{"x"}}}
	mustRun(t, plan)
	if want := []string{"c", "echo xout"}; !reflect.DeepEqual(commands(fake), want) {
		t.Errorf("commands = %q, want %q", commands(fake), want)
	}
}

// The attributes that hold expressions are evaluated when their recipe is.
func TestRunAttributeBackticksAreEvaluatedWhenTheRecipeIsReached(t *testing.T) {
	for _, tc := range []struct{ name, attr string }{
		{"working-directory", "working-directory(`exit 3`)"},
		{"timestamp", "timestamp(`exit 3`)"},
		{"env", "env('A', `exit 3`)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &sysfake.FakeProcess{Codes: []int{0, 3}}
			plan, _, stderr := backtickPlan(t, "first:\n  echo first\n["+tc.attr+"]\nsecond:\n  echo second\n", fake)
			plan.Calls = []Call{{Recipe: "first"}, {Recipe: "second"}}
			de := failure(t, plan)
			if de.ExitCode != 3 || stderr.String() != "echo first\n" || len(fake.Specs) != 2 {
				t.Errorf("error %+v, stderr %q, %d commands; want first to run before the attribute failed", *de, stderr, len(fake.Specs))
			}
		})
	}
}

func TestRunConfirmPromptBacktickIsEvaluatedWhenAsked(t *testing.T) {
	fake := &sysfake.FakeProcess{Outputs: []string{"Sure?"}}
	plan, _, stderr := backtickPlan(t, "[confirm(`ask`)]\nfoo:\n  @echo foo\n", fake)
	plan.Stdin = strings.NewReader("y\n")
	mustRun(t, plan)
	if stderr.String() != "Sure? " || !reflect.DeepEqual(commands(fake), []string{"ask", "echo foo"}) {
		t.Errorf("stderr %q, commands %q; want the prompt the command printed", stderr, commands(fake))
	}
}

func TestRunConfirmYesDoesNotRunThePromptBacktick(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	plan, _, _ := backtickPlan(t, "[confirm(`ask`)]\nfoo:\n  @echo foo\n", fake)
	plan.Yes = true
	mustRun(t, plan)
	if want := []string{"echo foo"}; !reflect.DeepEqual(commands(fake), want) {
		t.Errorf("commands = %q, want %q", commands(fake), want)
	}
}

// A recipe called twice evaluates its parameters twice, once for each call.
func TestRunBacktickDefaultsRunOncePerCall(t *testing.T) {
	fake := &sysfake.FakeProcess{Outputs: []string{"1", "2"}}
	plan, _, _ := backtickPlan(t, "top: one (one 'x') one\none a=`c`:\n  @echo {{a}}\n", fake)
	mustRun(t, plan)
	if want := []string{"c", "echo 1", "echo x"}; !reflect.DeepEqual(commands(fake), want) {
		t.Errorf("commands = %q, want %q", commands(fake), want)
	}
}

// The text of a failure is quoted from the justfile when the plan has it.
func TestRunWithoutSourceDoesNotQuote(t *testing.T) {
	plan, _, _ := newPlan(t, "x := `c`\nfoo:\n", &sysfake.FakeProcess{Codes: []int{1}})
	if de := failure(t, plan); de.Source != "" {
		t.Errorf("Source = %q, want none for a tree without its text", de.Source)
	}
}

func TestWithSource(t *testing.T) {
	jf := &ast.Justfile{File: "justfile", Source: []byte("a\nb\n")}
	other := errors.New("other")
	if got := withSource(jf, other); got != other {
		t.Errorf("withSource of a plain error = %v", got)
	}
	for _, tc := range []struct {
		name string
		jf   *ast.Justfile
		err  *diag.Error
		want string
	}{
		{"located", jf, &diag.Error{File: "justfile", Line: 2}, "b"},
		{"another file", jf, &diag.Error{File: "other", Line: 2}, ""},
		{"already quoted", jf, &diag.Error{File: "justfile", Line: 2, Source: "kept"}, "kept"},
		{"no source", &ast.Justfile{File: "justfile"}, &diag.Error{File: "justfile", Line: 2}, ""},
	} {
		if withSource(tc.jf, tc.err); tc.err.Source != tc.want {
			t.Errorf("%s: Source = %q, want %q", tc.name, tc.err.Source, tc.want)
		}
	}
}

func TestPrepareChecksTheArgumentsOfTheRequestedCalls(t *testing.T) {
	plan, _, _ := newPlan(t, "foo x:\n", &sysfake.FakeProcess{})
	plan.Calls = []Call{{Recipe: "foo"}}
	_, err := prepare(plan)
	requireDiag(t, err, diag.KindUsage, 0, "recipe `foo` got 0 positional arguments but takes 1\nusage:\n    just foo x")
}

func BenchmarkRunBacktick(b *testing.B) {
	plan, _, _ := backtickPlan(b, "x := `a`\nfoo y=`b`:\n  @echo {{x}} {{y}} {{`c`}}\n", &sysfake.FakeProcess{Outputs: []string{"1", "2", "3"}})
	plan.Stdout, plan.Stderr = io.Discard, io.Discard
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Run(context.Background(), plan); err != nil {
			b.Fatal(err)
		}
	}
}
