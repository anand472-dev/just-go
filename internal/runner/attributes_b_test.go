package runner

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/sys"
	"gitea.com/wimwian/just-go/internal/sys/sysfake"
)

// environmentOf is the variables of a recorded spec as a map.
func environmentOf(spec sys.ProcessSpec) map[string]string {
	env := map[string]string{}
	for _, kv := range spec.Env {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v
	}
	return env
}

func TestRunEnvAttribute(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		planEnv   []string
		want      map[string]string
	}{
		{"attributes::env_attribute_single#1", "[env('FOO', 'bar')]\nfoo:\n  @echo $FOO\n", nil,
			map[string]string{"FOO": "bar"}},
		{"attributes::env_attribute_multiple#1", "[env('VAR1', 'value1')]\n[env('VAR2', 'value 2')]\nfoo:\n  @echo\n", nil,
			map[string]string{"VAR1": "value1", "VAR2": "value 2"}},
		{"attributes::env_attribute_duplicate_last_wins#1", "[env('VAR1', 'value1')]\n[env('VAR1', 'value 2')]\nfoo:\n  @echo\n", nil,
			map[string]string{"VAR1": "value 2"}},
		{"attributes::env_attribute_with_expression#1", "suffix := 'world'\n[env('GREETING', 'hello ' + suffix)]\nfoo:\n  @echo\n", nil,
			map[string]string{"GREETING": "hello world"}},
		{"attributes::env_attribute_name_with_expression#1", "prefix := 'MY_'\n[env(prefix + 'VAR', 'value')]\nfoo:\n  @echo\n", nil,
			map[string]string{"MY_VAR": "value"}},
		{"attributes::env_attribute_overrides_export#1", "export FOO := 'export'\n[env('FOO', 'attribute')]\nbar:\n  @echo $FOO\n", nil,
			map[string]string{"FOO": "attribute"}},
		{"it overrides the environment of the plan", "[env('A', 'attribute')]\nfoo:\n  @echo\n", []string{"A=plan", "B=plan"},
			map[string]string{"A": "attribute", "B": "plan"}},
		{"an empty value sets the variable", "[env('A', '')]\nfoo:\n  @echo\n", nil, map[string]string{"A": ""}},
		{"it sees a global defined later", "[env('A', x)]\nfoo:\n  @echo\nx := 'late'\n", nil, map[string]string{"A": "late"}},
		{"it sits beside an exported parameter", "[env('A', 'a')]\nfoo $b:\n  @echo\n", nil, map[string]string{"A": "a", "b": ""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &sysfake.FakeProcess{}
			plan, _, _ := newPlan(t, tc.src, fake)
			plan.Env = tc.planEnv
			if strings.Contains(tc.src, "foo $b") {
				plan.Calls = []Call{{Recipe: "foo", Args: []string{""}}}
			}
			mustRun(t, plan)
			if got := environmentOf(fake.Specs[0]); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("environment = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRunEnvAttributeBelongsToItsRecipe(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	plan, _, _ := newPlan(t, "[env('A', 'a')]\nfoo: bar\n  @echo foo\nbar:\n  @echo bar\n", fake)
	mustRun(t, plan)
	if got := environmentOf(fake.Specs[0]); len(got) != 0 {
		t.Errorf("bar sees %v; want the environment of the plan only", got)
	}
	if got := environmentOf(fake.Specs[1]); got["A"] != "a" {
		t.Errorf("foo sees %v; want A=a", got)
	}
}

func TestRunEnvAttributeFailsBeforeAnythingRuns(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	plan, _, _ := newPlan(t, "[env('A', nope())]\nfoo:\n  @echo foo\n", fake)
	_, err := Run(t.Context(), plan)
	var de *diag.Error
	if !errors.As(err, &de) || de.File != "justfile" || de.Line != 1 {
		t.Errorf("Run error = %v, want a located *diag.Error on line 1", err)
	}
	if len(fake.Specs) != 0 {
		t.Errorf("%d commands ran, want none", len(fake.Specs))
	}
}

// The clock of the timestamp tests: 2001-07-07 15:04:59.5 UTC.
var stampClock = sysfake.FakeClock{At: sys.Time{Unix: 994518299, Nano: 500_000_000, UTC: true}}

func TestRunTimestamp(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		quiet     bool
		want      string
	}{
		{"timestamps::attribute#1", "[timestamp]\nrecipe:\n  echo foo\n", false, "[15:04:59] echo foo\n"},
		{"timestamps::attribute_format#1", "[timestamp('%H:%M:%S%.3f')]\nrecipe:\n  echo foo\n", false, "[15:04:59.500] echo foo\n"},
		{"timestamps::attribute_format_expression#1",
			"format := '%H:%M:%S' + '%.3f'\n\n[timestamp(format)]\nrecipe:\n  echo foo\n", false, "[15:04:59.500] echo foo\n"},
		{"a quiet line is echoed", "[timestamp]\nrecipe:\n  @echo foo\n", false, "[15:04:59] echo foo\n"},
		{"set quiet is overruled", "set quiet\n[timestamp]\nrecipe:\n  echo foo\n", false, "[15:04:59] echo foo\n"},
		{"so is a quiet plan", "[timestamp]\nrecipe:\n  echo foo\n", true, "[15:04:59] echo foo\n"},
		{"every command has one", "[timestamp('%S')]\nrecipe:\n  echo one\n  echo two\n", false, "[59] echo one\n[59] echo two\n"},
		{"it sees the parameters", "[timestamp(format)]\nrecipe format='%M':\n  echo foo\n", false, "[04] echo foo\n"},
		{"a recipe without one echoes as usual", "recipe:\n  echo foo\n", false, "echo foo\n"},
		{"a quiet line of it stays quiet", "recipe:\n  @echo foo\n", false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &sysfake.FakeProcess{}
			plan, _, stderr := newPlan(t, tc.src, fake)
			plan.Clock, plan.Quiet = stampClock, tc.quiet
			mustRun(t, plan)
			if got := stderr.String(); got != tc.want {
				t.Errorf("stderr = %q, want %q", got, tc.want)
			}
		})
	}
}

// A quiet host silences Stderr and gives the prompt stream the real one, where
// the timestamped commands go.
func TestRunTimestampWritesToThePromptStream(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	plan, _, stderr := newPlan(t, "[timestamp]\nrecipe:\n  echo foo\n", fake)
	var prompt bytes.Buffer
	plan.Clock, plan.Prompt, plan.Quiet = stampClock, &prompt, true
	mustRun(t, plan)
	if stderr.Len() != 0 || prompt.String() != "[15:04:59] echo foo\n" {
		t.Errorf("stderr = %q, prompt = %q; want the command on the prompt stream only", stderr.String(), prompt.String())
	}
}

func TestRunTimestampUsesTheSystemClockByDefault(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	plan, _, stderr := newPlan(t, "[timestamp]\nrecipe:\n  echo foo\n", fake)
	mustRun(t, plan)
	if !regexp.MustCompile(`^\[\d\d:\d\d:\d\d\] echo foo\n$`).MatchString(stderr.String()) {
		t.Errorf("stderr = %q, want a time in square brackets, then the command", stderr.String())
	}
}

func TestRunTimestampWithAnInvalidFormat(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	plan, _, stderr := newPlan(t, "[timestamp('%Q')]\nrecipe:\n  echo foo\n", fake)
	plan.Clock = stampClock
	_, err := Run(t.Context(), plan)
	var de *diag.Error
	if !errors.As(err, &de) || de.Kind != diag.KindRuntime || de.Line != 0 ||
		de.Msg != "failed to parse time format string `%Q`: bad or unsupported format string" {
		t.Errorf("Run error = %#v, want the parse error, without a location", err)
	}
	if len(fake.Specs) != 0 || stderr.Len() != 0 {
		t.Errorf("ran %d commands and wrote %q; want neither", len(fake.Specs), stderr.String())
	}
}

func TestRunTimestampFormatThatFailsToEvaluate(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	plan, _, _ := newPlan(t, "[timestamp(nope())]\nrecipe:\n  echo foo\n", fake)
	_, err := Run(t.Context(), plan)
	var de *diag.Error
	if !errors.As(err, &de) || de.Line != 1 {
		t.Errorf("Run error = %v, want a located *diag.Error on line 1", err)
	}
}

func TestRunContinue(t *testing.T) {
	interrupted := func(signal int) error { return &sys.SignalError{Caught: signal} }
	for _, tc := range []struct {
		name, attribute string
		caught          int
		goes            bool
	}{
		{"nothing to continue", "", 2, false},
		{"continue alone goes on after SIGINT", "[continue]", 2, true},
		{"continue alone stops for SIGHUP", "[continue]", 1, false},
		{"continue alone stops for SIGQUIT", "[continue]", 3, false},
		{"continue alone stops for SIGTERM", "[continue]", 15, false},
		{"a named signal", "[continue('SIGHUP')]", 1, true},
		{"a named signal, not the other", "[continue('SIGHUP')]", 2, false},
		{"several signals", "[continue('SIGINT', 'SIGQUIT')]", 3, true},
		{"several signals, the first", "[continue('SIGINT', 'SIGQUIT')]", 2, true},
		{"several signals, not the other", "[continue('SIGINT', 'SIGQUIT')]", 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &sysfake.FakeProcess{Errs: []error{interrupted(tc.caught)}}
			plan, _, stderr := newPlan(t, tc.attribute+"\nfoo:\n  @echo one\n  @echo two\n", fake)
			res := mustRun(t, plan)
			if tc.goes {
				if res.ExitCode != 0 || len(fake.Specs) != 2 || stderr.Len() != 0 {
					t.Errorf("exit %d after %d commands, stderr %q; want 0 after both, silent",
						res.ExitCode, len(fake.Specs), stderr.String())
				}
				return
			}
			want := "error: interrupted by " + sys.SignalName(tc.caught) + "\n"
			if res.ExitCode != 128+tc.caught || len(fake.Specs) != 1 || stderr.String() != want {
				t.Errorf("exit %d after %d commands, stderr %q; want %d after one, %q",
					res.ExitCode, len(fake.Specs), stderr.String(), 128+tc.caught, want)
			}
		})
	}
}

// A command that fails, or is ended by a signal, fails whatever `[continue]`
// says: it only forgives a signal the runner caught while a command ended on
// its own.
func TestRunContinueDoesNotForgiveFailure(t *testing.T) {
	fake := &sysfake.FakeProcess{Codes: []int{3}, Errs: []error{&sys.SignalError{Caught: 2}}}
	plan, _, stderr := newPlan(t, "[continue]\nfoo:\n  @exit 3\n  @echo two\n", fake)
	res := mustRun(t, plan)
	if res.ExitCode != 3 || len(fake.Specs) != 1 || stderr.String() != "error: recipe `foo` failed on line 3 with exit code 3\n" {
		t.Errorf("exit %d after %d commands, stderr %q", res.ExitCode, len(fake.Specs), stderr.String())
	}
	fake = &sysfake.FakeProcess{Codes: []int{-1}, Errs: []error{&sys.SignalError{Signal: 2, Caught: 2}}}
	plan, _, stderr = newPlan(t, "[continue]\nfoo:\n  @sleep\n  @echo two\n", fake)
	res = mustRun(t, plan)
	if res.ExitCode != 130 || len(fake.Specs) != 1 || !strings.Contains(stderr.String(), "was terminated on line 3 by signal 2") {
		t.Errorf("exit %d after %d commands, stderr %q", res.ExitCode, len(fake.Specs), stderr.String())
	}
}

// timedProcess is a sys.Process that records when each command starts and
// ends and how many run at once at most. It holds commands with channels and
// not with sleeps, so that what it proves does not depend on the speed of the
// machine: together makes the commands it names wait until all of them are
// running, and after makes a command wait until another has ended. A wait
// that is not met within guard gives up and sets timedOut, so that a hang
// fails a test (see checkNoHang) and does not stall it.
type timedProcess struct {
	mu       sync.Mutex
	events   []string
	running  int
	most     int
	together []string
	after    map[string]string
	guard    time.Duration
	timedOut bool
	latches  map[string]chan struct{}
	arrived  int
	codes    map[string]int
	errs     map[string]error
	output   string
}

// defaultGuard is how long a held command waits for what it waits for.
const defaultGuard = 5 * time.Second

// latch returns the channel called name, made on first use. The caller holds
// p.mu.
func (p *timedProcess) latch(name string) chan struct{} {
	if p.latches == nil {
		p.latches = map[string]chan struct{}{}
	}
	if p.latches[name] == nil {
		p.latches[name] = make(chan struct{})
	}
	return p.latches[name]
}

// open closes the channel called name, once. The caller holds p.mu.
func (p *timedProcess) open(name string) {
	ch := p.latch(name)
	select {
	case <-ch:
	default:
		close(ch)
	}
}

// wait blocks until ch is closed, or until the guard runs out, which it
// records. The caller does not hold p.mu.
func (p *timedProcess) wait(ch chan struct{}) {
	guard := p.guard
	if guard == 0 {
		guard = defaultGuard
	}
	select {
	case <-ch:
	case <-time.After(guard):
		p.mu.Lock()
		p.timedOut = true
		p.mu.Unlock()
	}
}

// checkNoHang fails t if a held command gave up waiting.
func (p *timedProcess) checkNoHang(t *testing.T) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.timedOut {
		t.Errorf("a command waited in vain: %v", p.events)
	}
}

func (p *timedProcess) Run(_ context.Context, spec sys.ProcessSpec) (int, error) {
	command := spec.Argv[len(spec.Argv)-1]
	p.mu.Lock()
	p.events = append(p.events, "start "+command)
	p.running++
	p.most = max(p.most, p.running)
	meet, together := p.latch("meet"), slices.Contains(p.together, command)
	if together {
		if p.arrived++; p.arrived == len(p.together) {
			p.open("meet")
		}
	}
	other, waits := p.after[command]
	var ended chan struct{}
	if waits {
		ended = p.latch("end " + other)
	}
	p.mu.Unlock()
	if together {
		p.wait(meet)
	}
	if waits {
		p.wait(ended)
	}
	if p.output != "" {
		for range 50 {
			io.WriteString(spec.Stdout, p.output)
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.running--
	p.events = append(p.events, "end "+command)
	p.open("end " + command)
	return p.codes[command], p.errs[command]
}

// index is the position of event in the events, or -1.
func (p *timedProcess) index(event string) int {
	for i, e := range p.events {
		if e == event {
			return i
		}
	}
	return -1
}

func (p *timedProcess) count(event string) int {
	n := 0
	for _, e := range p.events {
		if e == event {
			n++
		}
	}
	return n
}

func TestRunParallelRunsDependenciesAtOnce(t *testing.T) {
	proc := &timedProcess{together: []string{"echo a", "echo b", "echo c"}}
	plan, _, _ := newPlan(t, "[parallel]\nall: a b c\n  @echo all\na:\n  @echo a\nb:\n  @echo b\nc:\n  @echo c\n", proc)
	mustRun(t, plan)
	proc.checkNoHang(t)
	if proc.most != 3 {
		t.Errorf("%d commands ran at once, want 3: %v", proc.most, proc.events)
	}
	if last := proc.events[len(proc.events)-1]; last != "end echo all" {
		t.Errorf("the recipe ran before its dependencies were done: %v", proc.events)
	}
	if proc.index("start echo all") < proc.index("end echo a") {
		t.Errorf("the recipe started before a dependency ended: %v", proc.events)
	}
}

func TestRunWithoutParallelRunsDependenciesOneAfterTheOther(t *testing.T) {
	// Run one after the other, a waits in vain for b to start, so it gives up
	// at once at the short guard; run at once, they would both be running.
	proc := &timedProcess{together: []string{"echo a", "echo b"}, guard: 20 * time.Millisecond}
	plan, _, _ := newPlan(t, "all: a b\n  @echo all\na:\n  @echo a\nb:\n  @echo b\n", proc)
	mustRun(t, plan)
	if !proc.timedOut || proc.most != 1 {
		t.Errorf("%d commands ran at once, timed out %v; want 1 and a wait in vain: %v", proc.most, proc.timedOut, proc.events)
	}
}

func TestRunParallelSubsequents(t *testing.T) {
	proc := &timedProcess{together: []string{"echo a", "echo b"}}
	plan, _, _ := newPlan(t, "[parallel]\nall: && a b\n  @echo all\na:\n  @echo a\nb:\n  @echo b\n", proc)
	mustRun(t, plan)
	proc.checkNoHang(t)
	if proc.most != 2 || proc.index("end echo all") > proc.index("start echo a") {
		t.Errorf("subsequents: %d at once, events %v; want 2 at once after the recipe", proc.most, proc.events)
	}
}

func TestForksNeedsTwoDependenciesAndTheAttribute(t *testing.T) {
	for src, want := range map[string]bool{
		"[parallel]\nall: a\na:\n  @echo a\n":          false,
		"[parallel]\nall: a b\na:\n  @echo a\nb:\n":    true,
		"[parallel]\nall: && a b\na:\n  @echo a\nb:\n": true,
		"[parallel]\nall: a && b\na:\n  @echo a\nb:\n": false,
		"all: a b\na:\n  @echo a\nb:\n":                false,
		"a:\n  @echo a\n":                              false,
	} {
		if got := forks(parse(t, src)); got != want {
			t.Errorf("forks(%q) = %v, want %v", src, got, want)
		}
	}
}

// A recipe two branches both depend on runs once, and the branch that did not
// run it waits for it.
func TestRunParallelBranchesShareADependency(t *testing.T) {
	proc := &timedProcess{}
	plan, _, _ := newPlan(t, "[parallel]\nall: a b\n  @echo all\na: c\n  @echo a\nb: c\n  @echo b\nc:\n  @echo c\n", proc)
	mustRun(t, plan)
	if n := proc.count("start echo c"); n != 1 {
		t.Errorf("c ran %d times, want once: %v", n, proc.events)
	}
	done := proc.index("end echo c")
	for _, started := range []string{"start echo a", "start echo b"} {
		if proc.index(started) < done {
			t.Errorf("%s before c was done: %v", started, proc.events)
		}
	}
	if proc.index("start echo all") < proc.index("end echo a") || proc.index("start echo all") < proc.index("end echo b") {
		t.Errorf("all started before its dependencies ended: %v", proc.events)
	}
}

// A dependency made before the fork is not waited for: it is done.
func TestRunParallelDependencyAlreadyRun(t *testing.T) {
	proc := &timedProcess{}
	plan, _, _ := newPlan(t, "[parallel]\nall: c a b\n  @echo all\na: c\n  @echo a\nb: c\n  @echo b\nc:\n  @echo c\n", proc)
	mustRun(t, plan)
	if n := proc.count("start echo c"); n != 1 {
		t.Errorf("c ran %d times, want once: %v", n, proc.events)
	}
}

// Only the failure of the first branch that failed is reported, as upstream
// returns only that error; the other branches still run to the end.
func TestRunParallelReportsTheFirstFailure(t *testing.T) {
	// a ends after b, so that it is first in order and not in time.
	proc := &timedProcess{
		after: map[string]string{"exit 1": "exit 2"},
		codes: map[string]int{"exit 1": 1, "exit 2": 2},
	}
	plan, _, stderr := newPlan(t, "[parallel]\nall: a b\na:\n  @exit 1\nb:\n  @exit 2\n", proc)
	res := mustRun(t, plan)
	proc.checkNoHang(t)
	if proc.index("end exit 2") > proc.index("end exit 1") {
		t.Errorf("b did not end before a: %v", proc.events)
	}
	if res.ExitCode != 1 {
		t.Errorf("exit code = %d, want that of the first branch, 1", res.ExitCode)
	}
	if want := "error: recipe `a` failed on line 4 with exit code 1\n"; stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
	if proc.count("end exit 2") != 1 {
		t.Errorf("the second branch did not run to the end: %v", proc.events)
	}
}

func TestRunParallelReportsTheFailureOfALaterBranch(t *testing.T) {
	proc := &timedProcess{codes: map[string]int{"exit 2": 2}}
	plan, _, stderr := newPlan(t, "[parallel]\nall: a b\na:\n  @echo a\nb:\n  @exit 2\n", proc)
	res := mustRun(t, plan)
	if want := "error: recipe `b` failed on line 6 with exit code 2\n"; res.ExitCode != 2 || stderr.String() != want {
		t.Errorf("exit %d, stderr %q; want 2, %q", res.ExitCode, stderr.String(), want)
	}
}

func TestRunParallelReturnsTheErrorOfTheFirstBranchThatHasOne(t *testing.T) {
	proc := &timedProcess{errs: map[string]error{"echo a": fs.ErrNotExist, "echo b": errors.New("second")}}
	plan, _, _ := newPlan(t, "[parallel]\nall: a b\na:\n  @echo a\nb:\n  @echo b\n", proc)
	_, err := Run(t.Context(), plan)
	var de *diag.Error
	if !errors.As(err, &de) || !strings.Contains(de.Msg, "could not find the shell") {
		t.Errorf("Run error = %v, want the start failure of a", err)
	}
}

// A branch that waits for a recipe another branch fails to run does not wait
// for ever, and does not go on. The fork names c first, so its branch is the
// one that runs it, and the branch of b, which depends on c, is the one that
// waits.
func TestRunParallelWaiterOfAFailedRecipe(t *testing.T) {
	proc := &timedProcess{codes: map[string]int{"exit 1": 1}}
	plan, _, stderr := newPlan(t, "[parallel]\nall: c b\nb: c\n  @echo b\nc:\n  @exit 1\n", proc)
	res := mustRun(t, plan)
	if res.ExitCode != 1 || proc.count("start echo b") != 0 {
		t.Errorf("exit %d, events %v; want 1 and b not run", res.ExitCode, proc.events)
	}
	if want := "error: recipe `c` failed on line 6 with exit code 1\n"; stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
}

func TestRunParallelOutputIsNotTorn(t *testing.T) {
	proc := &timedProcess{output: "xxxxxxxxxxxxxxxxxxxx\n"}
	plan, _, _ := newPlan(t, "[parallel]\nall: a b c d\na:\n  @echo a\nb:\n  @echo b\nc:\n  @echo c\nd:\n  @echo d\n", proc)
	var out bytes.Buffer // not safe for concurrent writes by itself
	plan.Stdout = &out
	mustRun(t, plan)
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(lines) != 4*50 {
		t.Fatalf("%d lines, want %d", len(lines), 4*50)
	}
	for _, l := range lines {
		if l != "xxxxxxxxxxxxxxxxxxxx" {
			t.Fatalf("a torn line: %q", l)
		}
	}
}

// The arguments of every dependency are evaluated, and the dependencies are
// found, before any of them runs; but a call's own argument count is checked
// when it is made.
func TestRunParallelPlanErrors(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	plan, _, _ := newPlan(t, "[parallel]\nall: a (b)\na:\n  @echo a\nb x:\n  @echo b\n", fake)
	_, err := Run(t.Context(), plan)
	if err == nil || !strings.Contains(err.Error(), "recipe `b` got 0 positional arguments") {
		t.Errorf("Run error = %v, want the argument count of b", err)
	}
	fake = &sysfake.FakeProcess{}
	plan, _, _ = newPlan(t, "[parallel]\nall: a (b nope())\na:\n  @echo a\nb x:\n  @echo b\n", fake)
	if _, err = Run(t.Context(), plan); err == nil || len(fake.Specs) != 0 {
		t.Errorf("Run error = %v after %d commands; want an argument that cannot be evaluated to stop everything", err, len(fake.Specs))
	}
	fake = &sysfake.FakeProcess{}
	plan, _, _ = newPlan(t, "[parallel]\nall: a b\na:\n  @echo a\n", fake)
	plan.Justfile.Recipes[0].Deps[1].Name = "missing"
	if _, err = Run(t.Context(), plan); err == nil || !strings.Contains(err.Error(), "unknown dependency") || len(fake.Specs) != 0 {
		t.Errorf("Run error = %v after %d commands; want an unknown dependency", err, len(fake.Specs))
	}
}

// A recipe named twice in a list that runs at once is run by the first branch,
// and the second waits for it and does not run it again.
func TestRunParallelRecipeNamedTwiceRunsOnce(t *testing.T) {
	proc := &timedProcess{}
	plan, _, _ := newPlan(t, "[parallel]\nall: a a\n  @echo all\na:\n  @echo a\n", proc)
	mustRun(t, plan)
	if n := proc.count("start echo a"); n != 1 || proc.index("start echo all") < proc.index("end echo a") {
		t.Errorf("a ran %d times, or all started before it was done: %v", n, proc.events)
	}
}

// A dependency that names a recipe already being run is a cycle the analyzer
// would have refused; the fork leaves it out rather than wait for itself.
func TestRunParallelLeavesOutACycle(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	plan, _, _ := newPlan(t, "[parallel]\na: b a\n  @echo a\nb:\n  @echo b\n", fake)
	mustRun(t, plan)
	if got := commands(fake); len(got) != 2 || got[0] != "echo b" || got[1] != "echo a" {
		t.Errorf("commands = %q, want b and then a", got)
	}
}

func TestFailedBranchesAreFound(t *testing.T) {
	if (branchResult{}).failed() || !(branchResult{code: 1}).failed() || !(branchResult{err: errors.New("x")}).failed() {
		t.Error("failed() is wrong")
	}
}

func TestAwaitStopsWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	inv := &invocation{ctx: ctx}
	if _, err := inv.await(newGate()); !errors.Is(err, context.Canceled) {
		t.Errorf("await error = %v, want context.Canceled", err)
	}
}

func TestGateOpensOnceAndAbandonsOnlyWhenShut(t *testing.T) {
	g := newGate()
	g.open()
	g.abandon()
	inv := &invocation{ctx: t.Context()}
	if code, err := inv.await(g); code != 0 || err != nil {
		t.Errorf("await after open then abandon = %d, %v; want 0, nil", code, err)
	}
	g = newGate()
	g.abandon()
	g.open()
	if code, err := inv.await(g); code != 1 || err != nil {
		t.Errorf("await after abandon = %d, %v; want 1, nil", code, err)
	}
}

func TestLockStreams(t *testing.T) {
	if got := lockStreams(nil, nil); got[0] != nil || got[1] != nil {
		t.Errorf("lockStreams(nil, nil) = %v, want nils", got)
	}
	var a, b bytes.Buffer
	got := lockStreams(&a, nil, &b)
	io.WriteString(got[0], "out")
	io.WriteString(got[2], "err")
	if a.String() != "out" || b.String() != "err" || got[1] != nil {
		t.Errorf("streams wrote %q and %q, and the middle one is %v", a.String(), b.String(), got[1])
	}
}

func TestSettleOpensTheGateOfACallThatSucceeded(t *testing.T) {
	g := newGate()
	if code, err := settle(g, 0, nil); code != 0 || err != nil {
		t.Fatalf("settle = %d, %v", code, err)
	}
	inv := &invocation{ctx: t.Context()}
	if code, err := inv.await(g); code != 0 || err != nil {
		t.Errorf("await = %d, %v; want the gate open", code, err)
	}
}

func TestSettleAbandonsTheGateOfACallThatFailed(t *testing.T) {
	boom := errors.New("boom")
	inv := &invocation{ctx: t.Context()}
	for name, result := range map[string]struct {
		code int
		err  error
	}{"a status": {3, nil}, "an error": {0, boom}} {
		g := newGate()
		code, err := settle(g, result.code, result.err)
		if code != result.code || err != result.err {
			t.Errorf("%s: settle = %d, %v; want the call's result", name, code, err)
		}
		if code, err := inv.await(g); code != 1 || err != nil {
			t.Errorf("%s: await = %d, %v; want a gate that fails its waiters", name, code, err)
		}
	}
}

func TestRanGivesACallToTheFirstWhoClaimsIt(t *testing.T) {
	r := newRan()
	g, mine := r.claim("a")
	if !mine || g == nil {
		t.Fatal("the first claim is not mine")
	}
	if again, mine := r.claim("a"); mine || again != g {
		t.Errorf("the second claim = %p, %v; want the first one's gate, not mine", again, mine)
	}
	if other, mine := r.claim("b"); !mine || other == g {
		t.Errorf("claim of another call = %p, %v; want a gate of its own", other, mine)
	}
}

func TestCallChainFindsTheCallsOnTheWayHere(t *testing.T) {
	var none *callChain
	chain := &callChain{key: "b", up: &callChain{key: "a"}}
	for key, want := range map[string]bool{"a": true, "b": true, "c": false} {
		if got := chain.has(key); got != want {
			t.Errorf("has(%q) = %v, want %v", key, got, want)
		}
	}
	if none.has("a") {
		t.Error("the empty chain has a call")
	}
}

func parse(t testing.TB, src string) *ast.Justfile {
	t.Helper()
	plan, _, _ := newPlan(t, src, nil)
	return plan.Justfile
}

func BenchmarkRunParallel(b *testing.B) {
	var src strings.Builder
	src.WriteString("[parallel]\nall:")
	for _, name := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		src.WriteString(" " + name)
	}
	src.WriteString("\n  @echo all\n")
	for _, name := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		src.WriteString(name + ":\n  @echo " + name + "\n")
	}
	plan, _, _ := newPlan(b, src.String(), &sysfake.FakeProcess{})
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Run(context.Background(), plan); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTimestampedEcho(b *testing.B) {
	plan, _, _ := newPlan(b, "[timestamp('%H:%M:%S%.3f')]\nfoo:\n  @echo one\n  @echo two\n", &sysfake.FakeProcess{})
	plan.Clock = stampClock
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Run(context.Background(), plan); err != nil {
			b.Fatal(err)
		}
	}
}
