package runner

import (
	"testing"

	"gitea.com/wimwian/just-go/internal/sys/sysfake"
)

// With color on, an echoed line is bold, and a failure message is an error line
// in bold red and bold text, as upstream prints them.
func TestRunColorStylesEchoAndFailures(t *testing.T) {
	plan, _, stderr := newPlan(t, "recipe:\n  echo hi\n", &sysfake.FakeProcess{})
	plan.Calls = []Call{{Recipe: "recipe"}}
	plan.Color = true
	mustRun(t, plan)
	if got := stderr.String(); got != "\x1b[1mecho hi\x1b[0m\n" {
		t.Errorf("echo = %q", got)
	}

	plan, _, stderr = newPlan(t, "recipe:\n  @exit 100\n", &sysfake.FakeProcess{Codes: []int{100}})
	plan.Calls = []Call{{Recipe: "recipe"}}
	plan.Color = true
	mustRun(t, plan)
	want := "\x1b[1;31merror\x1b[0m: \x1b[1mrecipe `recipe` failed on line 2 with exit code 100\x1b[0m\n"
	if got := stderr.String(); got != want {
		t.Errorf("failure = %q; want %q", got, want)
	}
}

// --command-color colors the echoed lines, and --no-highlight leaves them plain.
func TestRunCommandColorAndNoHighlight(t *testing.T) {
	for _, c := range []struct {
		name        string
		noHighlight bool
		code, want  string
	}{
		{"in the command color", false, "36", "\x1b[1;36mecho hi\x1b[0m\n"},
		{"plain under no-highlight", true, "36", "echo hi\n"},
	} {
		plan, _, stderr := newPlan(t, "recipe:\n  echo hi\n", &sysfake.FakeProcess{})
		plan.Calls = []Call{{Recipe: "recipe"}}
		plan.Color, plan.NoHighlight, plan.CommandColor = true, c.noHighlight, c.code
		mustRun(t, plan)
		if got := stderr.String(); got != c.want {
			t.Errorf("%s: echo = %q; want %q", c.name, got, c.want)
		}
	}
}

// The --verbose announcement is bold cyan with color on, whatever --no-highlight says.
func TestRunColorStylesTheVerboseAnnouncement(t *testing.T) {
	plan, _, stderr := newPlan(t, "a:\n  @echo hi\n", &sysfake.FakeProcess{})
	plan.Calls = []Call{{Recipe: "a"}}
	plan.Color, plan.Verbose, plan.NoHighlight = true, true, true
	mustRun(t, plan)
	// --verbose echoes the @ line too, plainly under --no-highlight.
	if got := stderr.String(); got != "\x1b[1;36m===> running recipe `a`...\x1b[0m\necho hi\n" {
		t.Errorf("announcement = %q", got)
	}
}
