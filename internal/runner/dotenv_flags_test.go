package runner

import (
	"context"
	"reflect"
	"testing"

	"gitea.com/wimwian/just-go/internal/settings"
	"gitea.com/wimwian/just-go/internal/sys/sysfake"
)

func TestDotenvFlagsApplyReplaceOnlyTheGivenSettings(t *testing.T) {
	base := settings.Resolved{DotenvFilename: []string{"orig"}, DotenvPath: []string{"orig"}, DotenvCommand: []string{"orig"}}
	got := DotenvFlags{Filename: []string{"f"}, Command: []string{"c"}}.apply(base)
	if !reflect.DeepEqual(got.DotenvFilename, []string{"f"}) || !reflect.DeepEqual(got.DotenvPath, []string{"orig"}) || !reflect.DeepEqual(got.DotenvCommand, []string{"c"}) {
		t.Errorf("apply = %+v", got)
	}
	if none := (DotenvFlags{}).apply(base); !reflect.DeepEqual(none, base) {
		t.Errorf("apply with no flags changed the settings: %+v", none)
	}
	if path := (DotenvFlags{Path: []string{"p"}}).apply(base); !reflect.DeepEqual(path.DotenvPath, []string{"p"}) {
		t.Errorf("apply of a path = %+v", path)
	}
}

func TestDotenvCommandsThatReturnBadContentAreReported(t *testing.T) {
	proc := &sysfake.FakeProcess{Outputs: []string{"\xff\n"}}
	plan, _, _ := newPlan(t, "x:\n", proc)
	plan.Dotenv = DotenvFlags{Command: []string{"emit"}}
	if _, err := Run(context.Background(), plan); err == nil {
		t.Error("a dotenv command that wrote invalid UTF-8 was accepted")
	}
}

func TestDotenvCommandFailureIsReported(t *testing.T) {
	proc := &sysfake.FakeProcess{Err: errorString("no such shell")}
	plan, _, _ := newPlan(t, "x:\n", proc)
	plan.Dotenv = DotenvFlags{Command: []string{"emit"}}
	if _, err := Run(context.Background(), plan); err == nil {
		t.Error("a dotenv command that could not start was accepted")
	}
}

type errorString string

func (e errorString) Error() string { return string(e) }

func TestNoDotenvLeavesThePlanEnvironmentAlone(t *testing.T) {
	plan, _, _ := newPlan(t, "x:\n", &sysfake.FakeProcess{})
	plan.Env = []string{"A=1"}
	plan.Dotenv = DotenvFlags{NoDotenv: true, Command: []string{"emit"}}
	env, err := loadDotenv(context.Background(), plan, sysfake.FakeFS{}, plan.Exec, settings.Resolved{DotenvCommand: []string{"emit"}})
	if err != nil || !reflect.DeepEqual(env, []string{"A=1"}) {
		t.Errorf("loadDotenv = %q, %v; want the plan's environment", env, err)
	}
}
