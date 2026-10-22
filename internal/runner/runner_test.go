package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"reflect"
	"strings"
	"testing"
	"time"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/eval"
	"gitea.com/wimwian/just-go/internal/parser"
	"gitea.com/wimwian/just-go/internal/sys"
	"gitea.com/wimwian/just-go/internal/sys/sysfake"
)

// newPlan parses src and returns a Plan that runs it in a fresh temporary
// directory, capturing both output streams.
func newPlan(t testing.TB, src string, exec sys.Process) (Plan, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	jf, err := parser.Parse("justfile", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var stdout, stderr bytes.Buffer
	return Plan{
		Justfile: jf,
		WorkDir:  t.TempDir(),
		Stdout:   &stdout,
		Stderr:   &stderr,
		Exec:     exec,
	}, &stdout, &stderr
}

// commands returns the shell command of each recorded spec.
func commands(p *sysfake.FakeProcess) []string {
	var out []string
	for _, s := range p.Specs {
		out = append(out, s.Argv[len(s.Argv)-1])
	}
	return out
}

func mustRun(t *testing.T, plan Plan) Result {
	t.Helper()
	res, err := Run(context.Background(), plan)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return res
}

func TestRunDependenciesRunOnce(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	plan, _, _ := newPlan(t, "a: b c\n\techo a\nb: c\n\techo b\nc:\n\techo c\n", fake)
	plan.Recipes = []string{"a"}
	if res := mustRun(t, plan); res.ExitCode != 0 {
		t.Fatalf("exit = %d; want 0", res.ExitCode)
	}
	want := []string{"echo c", "echo b", "echo a"}
	if got := commands(fake); !reflect.DeepEqual(got, want) {
		t.Fatalf("commands = %q; want %q", got, want)
	}
}

func TestRunEchoesCommandsToStderr(t *testing.T) {
	plan, stdout, stderr := newPlan(t, "r:\n\techo hi\n", sys.OSProcess{})
	mustRun(t, plan)
	if stderr.String() != "echo hi\n" || stdout.String() != "hi\n" {
		t.Fatalf("stdout %q, stderr %q; want %q, %q", stdout, stderr, "hi\n", "echo hi\n")
	}
}

func TestRunAtPrefixSuppressesEcho(t *testing.T) {
	plan, stdout, stderr := newPlan(t, "r:\n\t@echo hi\n", sys.OSProcess{})
	mustRun(t, plan)
	if stderr.String() != "" || stdout.String() != "hi\n" {
		t.Fatalf("stdout %q, stderr %q; want %q, %q", stdout, stderr, "hi\n", "")
	}
}

func TestRunQuietSuppressesEcho(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	plan, _, stderr := newPlan(t, "r: d\n\techo r\n\t-echo s\nd:\n\techo d\n", fake)
	plan.Quiet = true
	mustRun(t, plan)
	if stderr.String() != "" {
		t.Fatalf("stderr = %q; want nothing", stderr)
	}
	if len(fake.Specs) != 3 {
		t.Fatalf("ran %d lines; want 3", len(fake.Specs))
	}
}

func TestRunSetQuietSuppressesEcho(t *testing.T) {
	plan, _, stderr := newPlan(t, "set quiet\nr:\n\techo r\n", &sysfake.FakeProcess{})
	mustRun(t, plan)
	if stderr.String() != "" {
		t.Fatalf("stderr = %q; want nothing", stderr)
	}
}

// TestRunQuietSuppressesFailureMessage pins upstream's -q: the command still
// fails with its status, but nothing is written, not even the failure line.
func TestRunQuietSuppressesFailureMessage(t *testing.T) {
	fake := &sysfake.FakeProcess{Codes: []int{3}}
	plan, _, stderr := newPlan(t, "r:\n\texit 3\n", fake)
	plan.Quiet = true
	if res := mustRun(t, plan); res.ExitCode != 3 {
		t.Fatalf("exit = %d; want 3", res.ExitCode)
	}
	if stderr.String() != "" {
		t.Fatalf("stderr = %q; want nothing", stderr)
	}
}

// TestRunSetQuietKeepsFailureMessage checks that `set quiet` only silences
// the echo; upstream still reports the failure.
func TestRunSetQuietKeepsFailureMessage(t *testing.T) {
	fake := &sysfake.FakeProcess{Codes: []int{3}}
	plan, _, stderr := newPlan(t, "set quiet\nr:\n\texit 3\n", fake)
	mustRun(t, plan)
	if want := "error: recipe `r` failed on line 3 with exit code 3\n"; stderr.String() != want {
		t.Fatalf("stderr = %q; want %q", stderr, want)
	}
}

func TestRunDashPrefixIgnoresFailure(t *testing.T) {
	fake := &sysfake.FakeProcess{Codes: []int{1, 0}}
	plan, _, stderr := newPlan(t, "r:\n\t-false\n\techo after\n", fake)
	res := mustRun(t, plan)
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d; want 0", res.ExitCode)
	}
	if want := []string{"false", "echo after"}; !reflect.DeepEqual(commands(fake), want) {
		t.Fatalf("commands = %q; want %q", commands(fake), want)
	}
	// The echo shows the command without its prefix, as upstream does.
	if want := "false\necho after\n"; stderr.String() != want {
		t.Fatalf("stderr = %q; want %q", stderr, want)
	}
}

func TestRunFailureExitStatusPropagates(t *testing.T) {
	fake := &sysfake.FakeProcess{Codes: []int{0, 3}}
	plan, _, stderr := newPlan(t, "\nfoo:\n\techo a\n\texit 3\n\techo never\n", fake)
	res := mustRun(t, plan)
	if res.ExitCode != 3 {
		t.Fatalf("exit = %d; want 3", res.ExitCode)
	}
	// Wording from corpus case stderr "echo a\nerror: recipe `foo` failed on
	// line 4 with exit code 7\n": the line number is the justfile line.
	want := "echo a\nexit 3\nerror: recipe `foo` failed on line 4 with exit code 3\n"
	if stderr.String() != want {
		t.Fatalf("stderr = %q; want %q", stderr, want)
	}
	if len(fake.Specs) != 2 {
		t.Fatalf("ran %d lines; want 2", len(fake.Specs))
	}
}

func TestRunFailureStopsLaterRecipes(t *testing.T) {
	fake := &sysfake.FakeProcess{Codes: []int{5}}
	plan, _, _ := newPlan(t, "a: b\n\techo a\nb:\n\texit 5\n", fake)
	if res := mustRun(t, plan); res.ExitCode != 5 {
		t.Fatalf("exit = %d; want 5", res.ExitCode)
	}
	if len(fake.Specs) != 1 {
		t.Fatalf("ran %d lines; want 1", len(fake.Specs))
	}
}

func TestRunSignalExitIsUnknownFailure(t *testing.T) {
	fake := &sysfake.FakeProcess{Codes: []int{-1}}
	plan, _, stderr := newPlan(t, "r:\n\t@kill -9 $$\n", fake)
	if res := mustRun(t, plan); res.ExitCode != 1 {
		t.Fatalf("exit = %d; want 1", res.ExitCode)
	}
	if want := "error: recipe `r` failed on line 2 for an unknown reason\n"; stderr.String() != want {
		t.Fatalf("stderr = %q; want %q", stderr, want)
	}
}

// TestRunSignals follows upstream's run_shell: a failing status comes first,
// then a signal that terminated the command, then a fatal signal caught
// while it ran; `-` ignores them all; the status is 128 plus the signal.
func TestRunSignals(t *testing.T) {
	cases := []struct {
		name, src string
		codes     []int
		errs      []error
		quiet     bool
		ran, exit int
		stderr    string
	}{
		{"terminated", "r:\n\t@kill -TERM $$\n\t@echo no\n", []int{-1}, []error{&sys.SignalError{Signal: 15}}, false, 1, 143,
			"error: recipe `r` was terminated on line 2 by signal 15\n"},
		{"terminated and caught", "r:\n\t@sleep 9\n", []int{-1}, []error{&sys.SignalError{Signal: 2, Caught: 2}}, false, 1, 130,
			"error: recipe `r` was terminated on line 2 by signal 2\n"},
		{"caught after success", "r:\n\t@true\n\t@echo no\n", []int{0}, []error{&sys.SignalError{Caught: 2}}, false, 1, 130,
			"error: interrupted by SIGINT\n"},
		{"caught after failure", "r:\n\t@false\n", []int{3}, []error{&sys.SignalError{Caught: 15}}, false, 1, 3,
			"error: recipe `r` failed on line 2 with exit code 3\n"},
		{"infallible goes on", "r:\n\t-@sleep 9\n\t@echo yes\n", []int{-1}, []error{&sys.SignalError{Signal: 2, Caught: 2}}, false, 2, 0, ""},
		{"quiet", "r:\n\t@sleep 9\n", []int{-1}, []error{&sys.SignalError{Signal: 9}}, true, 1, 137, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fake := &sysfake.FakeProcess{Codes: c.codes, Errs: c.errs}
			plan, _, stderr := newPlan(t, c.src, fake)
			plan.Quiet = c.quiet
			if res := mustRun(t, plan); res.ExitCode != c.exit || len(fake.Specs) != c.ran || stderr.String() != c.stderr {
				t.Fatalf("exit %d, ran %d, stderr %q; want %d, %d, %q", res.ExitCode, len(fake.Specs), stderr, c.exit, c.ran, c.stderr)
			}
		})
	}
}

// A real child that a signal ends reports it as upstream does
// (FINAL-7): `kill -TERM $$` gives status 143.
func TestRunSignalTerminatedChild(t *testing.T) {
	plan, _, stderr := newPlan(t, "a:\n\tkill -TERM $$\n", sys.OSProcess{})
	plan.Env = []string{"PATH=/usr/bin:/bin"}
	if res := mustRun(t, plan); res.ExitCode != 143 {
		t.Fatalf("exit = %d; want 143", res.ExitCode)
	}
	if want := "kill -TERM $$\nerror: recipe `a` was terminated on line 2 by signal 15\n"; stderr.String() != want {
		t.Fatalf("stderr = %q; want %q", stderr, want)
	}
}

func TestRunCancelKillsChild(t *testing.T) {
	plan, _, _ := newPlan(t, "r:\n\t@sleep 30\n", sys.OSProcess{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	time.AfterFunc(100*time.Millisecond, cancel)
	start := time.Now()
	_, err := Run(ctx, plan)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v; want context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Run took %v after cancel; want under 2s", elapsed)
	}
}

func TestRunCancelledBeforeLineRunsNothing(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	plan, _, stderr := newPlan(t, "r:\n\techo hi\n", fake)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Run(ctx, plan); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v; want context.Canceled", err)
	}
	if len(fake.Specs) != 0 || stderr.Len() != 0 {
		t.Fatalf("ran %d lines, stderr %q; want none", len(fake.Specs), stderr)
	}
}

func TestRunEnvIsExactlyPlanEnv(t *testing.T) {
	src := "r:\n\t@echo ${A-unset} ${HOME-unset}\n"
	plan, stdout, _ := newPlan(t, src, sys.OSProcess{})
	plan.Env = []string{"A=1"}
	mustRun(t, plan)
	if stdout.String() != "1 unset\n" {
		t.Fatalf("stdout = %q; want %q", stdout, "1 unset\n")
	}

	fake := &sysfake.FakeProcess{}
	plan, _, _ = newPlan(t, src, fake)
	plan.Env = []string{"A=1"}
	mustRun(t, plan)
	if got := fake.Specs[0].Env; !reflect.DeepEqual(got, []string{"A=1"}) {
		t.Fatalf("spec env = %q; want [A=1]", got)
	}
}

func TestRunProcessSpec(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	plan, stdout, stderr := newPlan(t, "r:\n\techo hi\n", fake)
	plan.Stdin = strings.NewReader("in")
	mustRun(t, plan)
	s := fake.Specs[0]
	if want := []string{"sh", "-cu", "echo hi"}; !reflect.DeepEqual(s.Argv, want) {
		t.Errorf("argv = %q; want %q", s.Argv, want)
	}
	if s.Dir != plan.WorkDir || s.Stdin != plan.Stdin || s.Stdout != io.Writer(stdout) || s.Stderr != io.Writer(stderr) {
		t.Errorf("spec = %+v; want the plan's directory and streams", s)
	}
	if s.Env == nil || len(s.Env) != 0 {
		t.Errorf("env = %#v; want an empty, non-nil slice for a nil Plan.Env", s.Env)
	}
}

func TestRunSettingsShell(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	plan, _, _ := newPlan(t, "set shell := [\"bash\", \"-c\"]\nr:\n\techo hi\n", fake)
	mustRun(t, plan)
	if want := []string{"bash", "-c", "echo hi"}; !reflect.DeepEqual(fake.Specs[0].Argv, want) {
		t.Fatalf("argv = %q; want %q", fake.Specs[0].Argv, want)
	}
}

func TestRunDefaultsToFirstRecipe(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	plan, _, _ := newPlan(t, "x := \"1\"\nfirst:\n\techo first\nsecond:\n\techo second\n", fake)
	mustRun(t, plan)
	if want := []string{"echo first"}; !reflect.DeepEqual(commands(fake), want) {
		t.Fatalf("commands = %q; want %q", commands(fake), want)
	}
}

func TestRunSeveralRecipesShareDependencies(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	plan, _, _ := newPlan(t, "a: c\n\techo a\nb: c\n\techo b\nc:\n\techo c\n", fake)
	plan.Recipes = []string{"a", "b", "a"}
	mustRun(t, plan)
	if want := []string{"echo c", "echo a", "echo b"}; !reflect.DeepEqual(commands(fake), want) {
		t.Fatalf("commands = %q; want %q", commands(fake), want)
	}
}

// TestRunCallsCarryTheirOwnArguments is `just d c 1 e 2`: each recipe gets
// the arguments of its own group.
func TestRunCallsCarryTheirOwnArguments(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	src := "d:\n\techo d\nc x:\n\techo c {{x}}\ne y='-':\n\techo e {{y}}\n"
	plan, _, _ := newPlan(t, src, fake)
	plan.Calls = []Call{{Recipe: "d"}, {Recipe: "c", Args: []string{"1"}}, {Recipe: "e", Args: []string{"2"}}}
	plan.Recipes, plan.Args = []string{"ignored"}, []string{"ignored"}
	mustRun(t, plan)
	if want := []string{"echo d", "echo c 1", "echo e 2"}; !reflect.DeepEqual(commands(fake), want) {
		t.Fatalf("commands = %q; want %q", commands(fake), want)
	}
}

// TestRunCallsRunOncePerArgumentList is `just c 1 c 2 c 1`: a repeated call
// with the same arguments is dropped, a different argument list is not.
func TestRunCallsRunOncePerArgumentList(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	plan, _, _ := newPlan(t, "c x='-':\n\techo c {{x}}\n", fake)
	plan.Calls = []Call{
		{Recipe: "c", Args: []string{"1"}}, {Recipe: "c", Args: []string{"2"}}, {Recipe: "c", Args: []string{"1"}},
		{Recipe: "c"}, {Recipe: "c", Args: []string{""}},
	}
	mustRun(t, plan)
	if want := []string{"echo c 1", "echo c 2", "echo c -", "echo c "}; !reflect.DeepEqual(commands(fake), want) {
		t.Fatalf("commands = %q; want %q", commands(fake), want)
	}
}

// TestRunDependencyReceivesItsArguments is `a: (c 'x')`: the dependency's
// parameter takes the argument.
func TestRunDependencyReceivesItsArguments(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	plan, _, _ := newPlan(t, "a: (c 'x')\n\techo a\nc y:\n\techo c {{y}}\n", fake)
	mustRun(t, plan)
	if want := []string{"echo c x", "echo a"}; !reflect.DeepEqual(commands(fake), want) {
		t.Fatalf("commands = %q; want %q", commands(fake), want)
	}
}

// TestRunDependencyArgumentsAreEvaluatedInTheCallersScope checks that an
// argument sees the globals and the parameters of the recipe that names the
// dependency, and that a default fills in for an argument left out.
func TestRunDependencyArgumentsAreEvaluatedInTheCallersScope(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	src := "g := 'G'\na p='P' q='Q': (c g p + q) (c 'y')\n\techo a\nc x y='d':\n\techo c {{x}} {{y}}\n"
	plan, _, _ := newPlan(t, src, fake)
	plan.Calls = []Call{{Recipe: "a", Args: []string{"1"}}}
	mustRun(t, plan)
	if want := []string{"echo c G 1Q", "echo c y d", "echo a"}; !reflect.DeepEqual(commands(fake), want) {
		t.Fatalf("commands = %q; want %q", commands(fake), want)
	}
}

// TestRunSameDependencyWithDifferentArgumentsRunsTwice checks that a
// recipe runs once per distinct argument list: the repeated list is dropped,
// and a call that leaves the argument out is a list of its own.
func TestRunSameDependencyWithDifferentArgumentsRunsTwice(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	src := "a: (c '1') (c '2') (c '1') c\n\techo a\nc x='-':\n\techo c {{x}}\n"
	plan, _, _ := newPlan(t, src, fake)
	mustRun(t, plan)
	if want := []string{"echo c 1", "echo c 2", "echo c -", "echo a"}; !reflect.DeepEqual(commands(fake), want) {
		t.Fatalf("commands = %q; want %q", commands(fake), want)
	}
}

// TestRunDependencyArgumentsReachNestedDependencies is a chain in which each
// recipe passes its parameter on.
func TestRunDependencyArgumentsReachNestedDependencies(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	src := "a: (b 'x')\nb p: (c p + '!')\n\techo b {{p}}\nc q:\n\techo c {{q}}\n"
	plan, _, _ := newPlan(t, src, fake)
	mustRun(t, plan)
	if want := []string{"echo c x!", "echo b x"}; !reflect.DeepEqual(commands(fake), want) {
		t.Fatalf("commands = %q; want %q", commands(fake), want)
	}
}

// TestRunSubsequentsRunAfterTheBody covers `&&`; the expected orders are the
// ones oracle/bin/just gives, and several are cases of the subsequents corpus
// group.
func TestRunSubsequentsRunAfterTheBody(t *testing.T) {
	for _, c := range []struct {
		name, src string
		call      Call
		want      []string
	}{
		{"after the body", "foo: && bar\n\techo foo\nbar:\n\techo bar\n", Call{Recipe: "foo"},
			[]string{"echo foo", "echo bar"}},
		{"with arguments", "foo: && (bar 'hello')\n\techo foo\nbar x:\n\techo {{x}}\n", Call{Recipe: "foo"},
			[]string{"echo foo", "echo hello"}},
		{"priors, then the body, then subsequents", "a: b && c\n\techo a\nb:\n\techo b\nc:\n\techo c\n",
			Call{Recipe: "a"}, []string{"echo b", "echo a", "echo c"}},
		{"even when already run as a prior", "a: b && b\n\techo a\nb:\n\techo b\n",
			Call{Recipe: "a"}, []string{"echo b", "echo a", "echo b"}},
		{"duplicates of one list run once", "a: && b c\n\techo a\nb: d\n\techo b\nc: d\n\techo c\nd:\n\techo d\n",
			Call{Recipe: "a"}, []string{"echo a", "echo d", "echo b", "echo c"}},
		{"the subsequents of a subsequent", "a: && b\n\techo a\nb: && c\n\techo b\nc:\n\techo c\n",
			Call{Recipe: "a"}, []string{"echo a", "echo b", "echo c"}},
		{"the caller's parameters", "a x: && (b x + '?')\n\techo a {{x}}\nb y:\n\techo b {{y}}\n",
			Call{Recipe: "a", Args: []string{"1"}}, []string{"echo a 1", "echo b 1?"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			fake := &sysfake.FakeProcess{}
			plan, _, _ := newPlan(t, c.src, fake)
			plan.Calls = []Call{c.call}
			mustRun(t, plan)
			if !reflect.DeepEqual(commands(fake), c.want) {
				t.Fatalf("commands = %q; want %q", commands(fake), c.want)
			}
		})
	}
}

// TestRunSubsequentsDoNotRunAfterAFailure is subsequents::failure#1.
func TestRunSubsequentsDoNotRunAfterAFailure(t *testing.T) {
	fake := &sysfake.FakeProcess{Codes: []int{0, 1}}
	plan, _, stderr := newPlan(t, "foo: && bar\n\techo foo\n\tfalse\nbar:\n\techo bar\n", fake)
	res := mustRun(t, plan)
	if want := []string{"echo foo", "false"}; !reflect.DeepEqual(commands(fake), want) {
		t.Fatalf("commands = %q; want %q", commands(fake), want)
	}
	if res.ExitCode != 1 || !strings.HasSuffix(stderr.String(), "error: recipe `foo` failed on line 3 with exit code 1\n") {
		t.Fatalf("exit = %d, stderr = %q", res.ExitCode, stderr.String())
	}
}

// TestRunTerminatesOnACycleTheAnalyzerMissed checks that a justfile that was
// not analyzed cannot loop: `a: && a` runs the body once.
func TestRunTerminatesOnACycleTheAnalyzerMissed(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	plan, _, _ := newPlan(t, "a: && a\n\techo a\n", fake)
	mustRun(t, plan)
	if want := []string{"echo a"}; !reflect.DeepEqual(commands(fake), want) {
		t.Fatalf("commands = %q; want %q", commands(fake), want)
	}
}

// TestRunDependencyArgumentErrorsComeBeforeAnything checks that an argument
// that cannot be evaluated, or does not fit the dependency, stops the run
// before any command runs.
func TestRunDependencyArgumentErrorsComeBeforeAnything(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	plan, _, _ := newPlan(t, "a: b (c nope)\nb:\n\techo b\nc y:\n\techo c\n", fake)
	_, err := Run(context.Background(), plan)
	requireDiag(t, err, diag.KindRuntime, 1, "variable `nope` not defined")
	plan, _, _ = newPlan(t, "a: b (c 'x' 'y')\nb:\n\techo b\nc y:\n\techo c\n", fake)
	_, err = Run(context.Background(), plan)
	requireDiag(t, err, diag.KindUsage, 0, "recipe `c` got 2 positional arguments but only takes 1\nusage:\n    just c y")
	if len(fake.Specs) != 0 {
		t.Fatalf("ran %d lines; want none", len(fake.Specs))
	}
}

func TestRunCallsReportUnknownRecipeAndArgumentMismatch(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	plan, _, _ := newPlan(t, "c x:\n\techo c\n", fake)
	plan.Calls = []Call{{Recipe: "nope"}}
	_, err := Run(context.Background(), plan)
	requireDiag(t, err, diag.KindUsage, 0, "justfile does not contain recipe `nope`")
	plan.Calls = []Call{{Recipe: "c", Args: []string{"1"}}, {Recipe: "c"}}
	_, err = Run(context.Background(), plan)
	requireDiag(t, err, diag.KindUsage, 0, "recipe `c` got 0 positional arguments but takes 1\nusage:\n    just c x")
	if len(fake.Specs) != 0 {
		t.Fatalf("ran %d lines; want none", len(fake.Specs))
	}
}

func TestRunInterpolatesVariablesAndParameters(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	src := "y := x + \"!\"\nx := \"hi\"\nr a b=a:\n\techo {{a}} {{b}} {{y}}\n"
	plan, _, stderr := newPlan(t, src, fake)
	plan.Args = []string{"one"}
	mustRun(t, plan)
	if want := []string{"echo one one hi!"}; !reflect.DeepEqual(commands(fake), want) {
		t.Fatalf("commands = %q; want %q", commands(fake), want)
	}
	if want := "echo one one hi!\n"; stderr.String() != want {
		t.Fatalf("stderr = %q; want %q", stderr, want)
	}
}

func TestRunSkipsEmptyCommands(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	plan, _, stderr := newPlan(t, "e := \"\"\nr:\n\t{{e}}\n\techo x\n", fake)
	mustRun(t, plan)
	if want := []string{"echo x"}; !reflect.DeepEqual(commands(fake), want) {
		t.Fatalf("commands = %q; want %q", commands(fake), want)
	}
	if stderr.String() != "echo x\n" {
		t.Fatalf("stderr = %q; want %q", stderr, "echo x\n")
	}
}

func TestRunNilStreamsDiscard(t *testing.T) {
	fake := &sysfake.FakeProcess{Codes: []int{2}}
	plan, _, _ := newPlan(t, "r:\n\texit 2\n", fake)
	plan.Stdout, plan.Stderr = nil, nil
	if res := mustRun(t, plan); res.ExitCode != 2 {
		t.Fatalf("exit = %d; want 2", res.ExitCode)
	}
}

func TestRunNilExecUsesOS(t *testing.T) {
	plan, stdout, _ := newPlan(t, "r:\n\t@echo os\n", nil)
	mustRun(t, plan)
	if stdout.String() != "os\n" {
		t.Fatalf("stdout = %q; want %q", stdout, "os\n")
	}
}

// TestRunExecErrorIsRuntimeError pins upstream's ShellIo wording for a
// command that could not be started, by the kind of the failure, and that the
// error has a line but no column, so the command prints no snippet.
func TestRunExecErrorIsRuntimeError(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{errors.New("boom"),
			"recipe `r` could not be run because of an IO error while launching the shell `sh`: boom"},
		{fmt.Errorf("gone: %w", fs.ErrNotExist),
			"recipe `r` could not be run because just could not find the shell `sh`: gone: file does not exist"},
		{fmt.Errorf("no: %w", fs.ErrPermission),
			"recipe `r` could not be run because just could not run the shell `sh`: no: permission denied"},
	} {
		plan, _, _ := newPlan(t, "r:\n\techo hi\n", &sysfake.FakeProcess{Err: tc.err})
		res, err := Run(context.Background(), plan)
		requireDiag(t, err, diag.KindRuntime, 2, tc.want)
		var de *diag.Error
		if errors.As(err, &de); de.Col != 0 || res.ExitCode != 1 {
			t.Fatalf("col = %d, exit = %d; want 0, 1", de.Col, res.ExitCode)
		}
	}
}

func requireDiag(t *testing.T, err error, kind diag.Kind, line int, msg string) {
	t.Helper()
	var de *diag.Error
	if !errors.As(err, &de) {
		t.Fatalf("error %v is not a *diag.Error", err)
	}
	if de.Kind != kind || de.Line != line || de.Msg != msg {
		t.Fatalf("got kind %v line %d msg %q; want kind %v line %d msg %q",
			de.Kind, de.Line, de.Msg, kind, line, msg)
	}
}

func TestRunErrorsBeforeAnythingRuns(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		recipes []string
		args    []string
		kind    diag.Kind
		line    int
		msg     string
	}{
		{"unknown recipe", "r:\n\techo\n", []string{"nope"}, nil, diag.KindUsage, 0,
			"justfile does not contain recipe `nope`"},
		{"no recipes", "x := \"1\"\n", nil, nil, diag.KindUsage, 0,
			"justfile contains no recipes"},
		{"too few", "r a b:\n\techo\n", nil, []string{"1"}, diag.KindUsage, 0,
			"recipe `r` got 1 positional argument but takes 2\nusage:\n    just r a b"},
		{"too many", "r a:\n\techo\n", nil, []string{"1", "2"}, diag.KindUsage, 0,
			"recipe `r` got 2 positional arguments but only takes 1\nusage:\n    just r a"},
		{"at least", "r a b='x':\n\techo\n", nil, nil, diag.KindUsage, 0,
			"recipe `r` got 0 positional arguments but takes at least 1\nusage:\n    just r a [b]"},
		{"at most", "r a='x':\n\techo\n", nil, []string{"1", "2"}, diag.KindUsage, 0,
			"recipe `r` got 2 positional arguments but takes at most 1\nusage:\n    just r [a]"},
		{"dependency needs argument", "r: d\n\techo\nd a:\n\techo\n", nil, nil, diag.KindUsage, 0,
			"recipe `d` got 0 positional arguments but takes 1\nusage:\n    just d a"},
		{"args with several recipes", "a:\n\techo\nb:\n\techo\n", []string{"a", "b"}, []string{"1"}, diag.KindUsage, 0,
			"positional arguments need exactly one requested recipe, but 2 were requested"},
		{"unknown dependency", "r: d\n\techo\n", nil, nil, diag.KindAnalysis, 1,
			"recipe `r` has unknown dependency `d`"},
		{"undefined assignment variable", "x := y\nr:\n\techo\n", nil, nil, diag.KindRuntime, 1,
			"variable `y` not defined"},
		{"undefined default variable", "\nr a=z:\n\techo\n", nil, nil, diag.KindRuntime, 2,
			"variable `z` not defined"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fake := &sysfake.FakeProcess{}
			plan, _, _ := newPlan(t, c.src, fake)
			plan.Recipes, plan.Args = c.recipes, c.args
			_, err := Run(context.Background(), plan)
			requireDiag(t, err, c.kind, c.line, c.msg)
			if len(fake.Specs) != 0 {
				t.Fatalf("ran %d lines; want none", len(fake.Specs))
			}
		})
	}
}

func TestRunLineEvaluationErrorIsLocated(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	plan, _, _ := newPlan(t, "r:\n\techo ok\n\techo {{nope}}\n", fake)
	_, err := Run(context.Background(), plan)
	requireDiag(t, err, diag.KindRuntime, 3, "variable `nope` not defined")
	var de *diag.Error
	errors.As(err, &de)
	if de.File != "justfile" {
		t.Fatalf("file = %q; want %q", de.File, "justfile")
	}
	if len(fake.Specs) != 1 {
		t.Fatalf("ran %d lines; want 1", len(fake.Specs))
	}
}

func TestLocateKeepsExistingLocation(t *testing.T) {
	located := &diag.Error{Kind: diag.KindRuntime, File: "f", Line: 7, Msg: "m"}
	if got := locate(located, "justfile", 1); got != located || located.Line != 7 || located.File != "f" {
		t.Fatalf("locate changed an already located error: %+v", got)
	}
}

func TestLocateFillsInWhatIsMissing(t *testing.T) {
	unlocated := &diag.Error{Kind: diag.KindRuntime, Msg: "m"}
	if locate(unlocated, "justfile", 3); unlocated.File != "justfile" || unlocated.Line != 3 {
		t.Fatalf("locate did not fill in the location: %+v", unlocated)
	}
	column := &diag.Error{Kind: diag.KindRuntime, Line: 2, Col: 9, Msg: "m"}
	if locate(column, "justfile", 3); column.File != "justfile" || column.Line != 2 || column.Col != 9 {
		t.Fatalf("locate replaced the position of a variable: %+v", column)
	}
}

func TestLocateLeavesOtherErrors(t *testing.T) {
	plain := errors.New("plain")
	if got := locate(plain, "justfile", 1); got != plain {
		t.Fatalf("locate(%v) = %v; want it unchanged", plain, got)
	}
}

// chain returns a justfile of n recipes in which each depends on the next.
func chain(n int) *ast.Justfile {
	jf := &ast.Justfile{File: "justfile"}
	for i := 0; i < n; i++ {
		r := ast.Recipe{Name: fmt.Sprintf("r%d", i), Line: i + 1}
		if i+1 < n {
			r.Deps = []ast.Dependency{{Name: fmt.Sprintf("r%d", i+1)}}
		}
		jf.Recipes = append(jf.Recipes, r)
	}
	return jf
}

func TestResolveOrderChain(t *testing.T) {
	order, err := resolveOrder(chain(200), []Call{{Recipe: "r0"}}, eval.Scope{})
	if err != nil {
		t.Fatalf("resolveOrder: %v", err)
	}
	if len(order) != 200 || order[0].recipe.Name != "r199" || order[199].recipe.Name != "r0" {
		t.Fatalf("order has %d recipes from %s to %s; want 200 from r199 to r0",
			len(order), order[0].recipe.Name, order[len(order)-1].recipe.Name)
	}
}

func BenchmarkResolveOrder(b *testing.B) {
	jf := chain(200)
	calls := []Call{{Recipe: "r0"}}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := resolveOrder(jf, calls, eval.Scope{}); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkResolveOrderArguments walks a chain in which each recipe passes
// its parameter on to the next, and has one subsequent. Every recipe has two
// different calls of the next, so the walk is 2^n steps long and n is small.
func BenchmarkResolveOrderArguments(b *testing.B) {
	const n = 10
	jf := &ast.Justfile{File: "justfile"}
	for i := 0; i < n; i++ {
		r := ast.Recipe{Name: fmt.Sprintf("r%d", i), Line: i + 1, Params: []ast.Param{{Name: "p", Default: ast.StringLit{Value: "d"}}}}
		if i+1 < n {
			next := fmt.Sprintf("r%d", i+1)
			r.Deps = []ast.Dependency{{Name: next, Args: []ast.Expr{ast.Concat{Left: ast.Var{Name: "p"}, Right: ast.StringLit{Value: "x"}}}}}
			r.Subsequents = []ast.Dependency{{Name: next, Args: []ast.Expr{ast.Var{Name: "p"}}}}
		}
		jf.Recipes = append(jf.Recipes, r)
	}
	calls := []Call{{Recipe: "r0"}}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := resolveOrder(jf, calls, eval.Scope{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRenderLine(b *testing.B) {
	line := ast.Line{Parts: []ast.Expr{
		ast.StringLit{Value: "echo "}, ast.Var{Name: "a"},
		ast.StringLit{Value: " "}, ast.Concat{Left: ast.Var{Name: "a"}, Right: ast.Var{Name: "b"}},
	}}
	scope := map[string]string{"a": "alpha", "b": "beta"}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := renderLine(line, scope); err != nil {
			b.Fatal(err)
		}
	}
}

func TestShellArgvOverrides(t *testing.T) {
	set := ast.Settings{Shell: []string{"bash", "-c"}}
	for _, tc := range []struct {
		name     string
		settings ast.Settings
		shell    string
		args     []string
		want     []string
	}{
		{"default", ast.Settings{}, "", nil, []string{"sh", "-cu", "cmd"}},
		{"set shell", set, "", nil, []string{"bash", "-c", "cmd"}},
		{"shell keeps default args", set, "zsh", nil, []string{"zsh", "-cu", "cmd"}},
		{"args use default shell", set, "", []string{"-e", "-c"}, []string{"sh", "-e", "-c", "cmd"}},
		{"shell and args", set, "zsh", []string{"-c"}, []string{"zsh", "-c", "cmd"}},
		{"empty args are no args", set, "zsh", []string{}, []string{"zsh", "cmd"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := shellArgv(tc.settings, tc.shell, tc.args, "cmd"); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("shellArgv = %q; want %q", got, tc.want)
			}
		})
	}
}

func TestRunPlanShellOverridesSetShell(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	plan, _, _ := newPlan(t, "set shell := [\"bash\", \"-c\"]\nr: d\n\techo hi\nd:\n\techo d\n", fake)
	plan.Shell = "zsh"
	plan.ShellArgs = []string{"-e", "-c"}
	mustRun(t, plan)
	for _, s := range fake.Specs {
		if want := []string{"zsh", "-e", "-c"}; !reflect.DeepEqual(s.Argv[:3], want) {
			t.Errorf("argv = %q; want prefix %q", s.Argv, want)
		}
	}
	if len(fake.Specs) != 2 {
		t.Fatalf("specs = %d; want 2", len(fake.Specs))
	}
}

func BenchmarkShellArgv(b *testing.B) {
	set := ast.Settings{Shell: []string{"bash", "-c"}}
	args := []string{"-e", "-c"}
	for i := 0; i < b.N; i++ {
		_ = shellArgv(set, "zsh", args, "echo hi")
	}
}
