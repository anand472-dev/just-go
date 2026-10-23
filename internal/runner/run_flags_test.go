package runner

import (
	"strings"
	"testing"

	"gitea.com/wimwian/just-go/internal/sys"
	"gitea.com/wimwian/just-go/internal/sys/sysfake"
)

// --dry-run writes each line as it would run, with backticks and shell calls
// as text, and runs nothing.
func TestRunDryRunWritesTheLinesAndRunsNothing(t *testing.T) {
	src := "var := `echo x`\n\nfoo: bar\n  @echo {{var}} {{shell('exit 1', '\"')}}\nbar:\n  echo bar\n"
	fake := &sysfake.FakeProcess{}
	plan, _, stderr := newPlan(t, src, fake)
	plan.Calls = []Call{{Recipe: "foo"}}
	plan.DryRun = true
	mustRun(t, plan)
	want := "echo bar\necho `echo x` shell(\"exit 1\", \"\\\"\")\n"
	if got := stderr.String(); got != want {
		t.Errorf("stderr = %q; want %q", got, want)
	}
	if len(fake.Specs) != 0 {
		t.Errorf("a dry run ran %d commands", len(fake.Specs))
	}
}

// --time writes how long each recipe took, after it runs; a failed recipe is
// timed too.
func TestRunTimeReportsEachRecipe(t *testing.T) {
	plan, _, stderr := newPlan(t, "bar:\n  @echo bar\nfoo: bar\n  @echo foo\n", &sysfake.FakeProcess{})
	plan.Calls = []Call{{Recipe: "foo"}}
	plan.Time = true
	plan.Clock = sysfake.FakeClock{At: sys.Time{Unix: 1}}
	mustRun(t, plan)
	if want := "---> bar completed in 0.000s\n---> foo completed in 0.000s\n"; stderr.String() != want {
		t.Errorf("stderr = %q; want %q", stderr.String(), want)
	}
}

func TestRunTimeReportsAFailedRecipe(t *testing.T) {
	plan, _, stderr := newPlan(t, "foo:\n  @false\n", &sysfake.FakeProcess{Codes: []int{1}})
	plan.Calls = []Call{{Recipe: "foo"}}
	plan.Time = true
	plan.Clock = sysfake.FakeClock{At: sys.Time{Unix: 1}}
	res, err := Run(t.Context(), plan)
	if err != nil || res.ExitCode != 1 || !strings.HasPrefix(stderr.String(), "---> foo completed in 0.000s\n") {
		t.Errorf("Run = %+v, %v; stderr %q", res, err, stderr.String())
	}
}

// --explain writes the doc comment of each recipe that has one, before it runs,
// and nothing for a recipe without one.
func TestRunExplainWritesTheDocComment(t *testing.T) {
	src := "# List fruits\nfruits:\n  @echo apple\nplain:\n  @echo x\n"
	plan, _, stderr := newPlan(t, src, &sysfake.FakeProcess{})
	plan.Calls = []Call{{Recipe: "fruits"}, {Recipe: "plain"}}
	plan.Explain = true
	mustRun(t, plan)
	if got := stderr.String(); got != "#### List fruits\n" {
		t.Errorf("stderr = %q; want the doc comment alone", got)
	}
}

// --timestamp stamps every recipe that has no [timestamp] attribute, with the
// format it gives, and the default one when it gives none.
func TestRunTimestampFlagStampsEveryRecipe(t *testing.T) {
	for _, tc := range []struct {
		name, format, want string
	}{
		{"the default format", "", "[15:04:59] echo foo\n"},
		{"a format of its own", "%S", "[59] echo foo\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, _, stderr := newPlan(t, "recipe:\n  echo foo\n", &sysfake.FakeProcess{})
			plan.Clock = stampClock
			plan.Timestamp = true
			plan.TimestampFormat = tc.format
			mustRun(t, plan)
			if got := stderr.String(); got != tc.want {
				t.Errorf("stderr = %q; want %q", got, tc.want)
			}
		})
	}
}

// A dry run renders the other builtins as they run, skips a comment under
// ignore-comments and a line that renders empty, and reports an error the
// builtin gives.
func TestRunDryRunRendersTheRest(t *testing.T) {
	src := "set ignore-comments\nfoo:\n  # hidden\n  echo {{ trim(' a ') }}\n  {{ '' }}\n"
	plan, _, stderr := newPlan(t, src, &sysfake.FakeProcess{})
	plan.Calls = []Call{{Recipe: "foo"}}
	plan.DryRun = true
	mustRun(t, plan)
	if got := stderr.String(); got != "echo a\n" {
		t.Errorf("stderr = %q; want the rendered line alone", got)
	}
}

func TestRunDryRunReportsAnErrorFromABuiltin(t *testing.T) {
	plan, _, _ := newPlan(t, "foo:\n  echo {{ read('no-such-file.txt') }}\n", &sysfake.FakeProcess{})
	plan.Calls = []Call{{Recipe: "foo"}}
	plan.DryRun = true
	if _, err := Run(t.Context(), plan); err == nil {
		t.Errorf("a dry run with a failing read gave no error")
	}
}

// The dry shell function quotes its arguments, and only that function is
// rendered by the commander itself.
func TestDryRunnerRendersOnlyTheShellCall(t *testing.T) {
	d := dryRunner{}
	if text, ok := d.Dry("shell", []string{"a\"b\\c", "d\te"}); !ok || text != `shell("a\"b\\c", "d\te")` {
		t.Errorf("Dry(shell) = %q, %v", text, ok)
	}
	if _, ok := d.Dry("trim", []string{" a "}); ok {
		t.Errorf("Dry(trim) rendered the call")
	}
	if text, err := d.Backtick("date"); err != nil || text != "`date`" {
		t.Errorf("Backtick = %q, %v", text, err)
	}
}

func TestSecondsBetweenCountsWholeAndPartialSeconds(t *testing.T) {
	start := sys.Time{Unix: 10, Nano: 750_000_000}
	end := sys.Time{Unix: 12, Nano: 250_000_000}
	if got := secondsBetween(start, end); got != 1.5 {
		t.Errorf("secondsBetween = %v; want 1.5", got)
	}
}

// A dry run does not run the dotenv command, so a command that would fail does
// not fail the run.
func TestRunDryRunDoesNotRunTheDotenvCommand(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	plan, _, stderr := newPlan(t, "set dotenv-command := 'exit 1'\nfoo:\n  echo bar\n", fake)
	plan.Calls = []Call{{Recipe: "foo"}}
	plan.DryRun = true
	mustRun(t, plan)
	if got := stderr.String(); got != "echo bar\n" || len(fake.Specs) != 0 {
		t.Errorf("stderr %q, ran %d; want the line and nothing run", got, len(fake.Specs))
	}
}
