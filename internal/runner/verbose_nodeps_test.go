package runner

import (
	"testing"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/sys/sysfake"
)

// --verbose echoes a line that `@` or `set quiet` hides, and --quiet echoes
// nothing, as upstream does.
func TestEchoesFollowsQuietVerboseAndPrefixes(t *testing.T) {
	for _, c := range []struct {
		name                        string
		p                           ast.Prefix
		quiet, setQuiet, loud, want bool
	}{
		{"a plain line", ast.PrefixNone, false, false, false, true},
		{"an @ line is hidden", ast.PrefixAt, false, false, false, false},
		{"an @- line is hidden", ast.PrefixAtDash, false, false, false, false},
		{"a dash line is echoed", ast.PrefixDash, false, false, false, true},
		{"set quiet hides a plain line", ast.PrefixNone, false, true, false, false},
		{"--verbose shows an @ line", ast.PrefixAt, false, false, true, true},
		{"--verbose shows a set quiet line", ast.PrefixNone, false, true, true, true},
		{"--quiet hides even under --verbose", ast.PrefixNone, true, false, true, false},
	} {
		if got := echoes(c.p, c.quiet, c.setQuiet, c.loud); got != c.want {
			t.Errorf("%s: echoes = %v; want %v", c.name, got, c.want)
		}
	}
}

// --verbose writes the announcement of each recipe after its dependencies, and
// before its own command echo.
func TestRunVerboseAnnouncesEachRecipe(t *testing.T) {
	plan, _, stderr := newPlan(t, "a: b\n\t@echo a\nb:\n\t@echo b\n", &sysfake.FakeProcess{})
	plan.Calls = []Call{{Recipe: "a"}}
	plan.Verbose = true
	mustRun(t, plan)
	want := "===> running recipe `b`...\necho b\n===> running recipe `a`...\necho a\n"
	if got := stderr.String(); got != want {
		t.Errorf("stderr = %q; want %q", got, want)
	}
}

// --quiet silences the --verbose announcement along with the echoes.
func TestRunQuietSilencesTheVerboseAnnouncement(t *testing.T) {
	plan, _, stderr := newPlan(t, "a:\n\t@echo a\n", &sysfake.FakeProcess{})
	plan.Calls = []Call{{Recipe: "a"}}
	plan.Verbose = true
	plan.Quiet = true
	mustRun(t, plan)
	if got := stderr.String(); got != "" {
		t.Errorf("stderr = %q; want nothing", got)
	}
}

// --no-deps leaves out the dependencies that come before a recipe.
func TestRunNoDepsSkipsNormalDependencies(t *testing.T) {
	proc := &sysfake.FakeProcess{}
	plan, _, _ := newPlan(t, "a: b\n\techo a\nb:\n\techo b\n", proc)
	plan.Calls = []Call{{Recipe: "a"}}
	plan.NoDeps = true
	mustRun(t, plan)
	if got := commands(proc); len(got) != 1 || got[0] != "echo a" {
		t.Errorf("commands = %q; want only echo a", got)
	}
}

// --no-deps also leaves out the subsequent dependencies that follow `&&`.
func TestRunNoDepsSkipsSubsequents(t *testing.T) {
	proc := &sysfake.FakeProcess{}
	plan, _, _ := newPlan(t, "a: && b\n\techo a\nb:\n\techo b\n", proc)
	plan.Calls = []Call{{Recipe: "a"}}
	plan.NoDeps = true
	mustRun(t, plan)
	if got := commands(proc); len(got) != 1 || got[0] != "echo a" {
		t.Errorf("commands = %q; want only echo a", got)
	}
}
