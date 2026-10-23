package runner

import (
	"context"
	"errors"
	"strings"
	"testing"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/sys"
	"gitea.com/wimwian/just-go/internal/sys/sysfake"
)

func TestRunCommandGivesItTheExportedVariablesOnly(t *testing.T) {
	proc := &sysfake.FakeProcess{Codes: []int{0}}
	plan, _, _ := newPlan(t, "export FOO := 'bar'\nplain := 'x'\ny := `exit 1`\n", proc)
	code, err := RunCommand(context.Background(), plan, []string{"sh", "-c", "printf $FOO"})
	if err != nil || code != 0 {
		t.Fatalf("RunCommand = %d, %v", code, err)
	}
	if len(proc.Specs) != 1 {
		t.Fatalf("ran %d processes; want only the command, since y is not needed", len(proc.Specs))
	}
	if !containsEnv(proc.Specs[0].Env, "FOO=bar") || containsEnv(proc.Specs[0].Env, "plain=x") {
		t.Errorf("env = %q; want FOO only", proc.Specs[0].Env)
	}
}

func TestRunCommandPassesItsStatusAsAFailure(t *testing.T) {
	proc := &sysfake.FakeProcess{Codes: []int{42}}
	plan, _, _ := newPlan(t, "x:\n", proc)
	code, err := RunCommand(context.Background(), plan, []string{"sh", "-c", "exit 42"})
	var de *diag.Error
	if code != 42 || !errors.As(err, &de) || de.Msg != "command `sh` `-c` `exit 42` failed: exit status: 42" || de.ExitCode != 42 {
		t.Errorf("RunCommand = %d, %v; want status 42 and the failure message", code, err)
	}
}

func TestRunCommandReportsACommandThatCannotStart(t *testing.T) {
	proc := &sysfake.FakeProcess{Err: errors.New("no such file")}
	plan, _, _ := newPlan(t, "x:\n", proc)
	code, err := RunCommand(context.Background(), plan, []string{"nope", "bar"})
	var de *diag.Error
	if code != 1 || !errors.As(err, &de) || !strings.HasPrefix(de.Msg, "failed to invoke `nope` `bar`") {
		t.Errorf("RunCommand = %d, %v; want status 1 and the invoke error", code, err)
	}
}

func TestRunCommandRefusesAnUnknownOverride(t *testing.T) {
	plan, _, _ := newPlan(t, "x:\n", &sysfake.FakeProcess{})
	plan.Overrides = map[string]string{"nope": "y"}
	if code, err := RunCommand(context.Background(), plan, []string{"true"}); code != 1 || err == nil {
		t.Errorf("RunCommand = %d, %v; want the override error", code, err)
	}
}

func TestRunPlainCommandRunsTheChildAsGiven(t *testing.T) {
	proc := &sysfake.FakeProcess{Codes: []int{3}}
	code, err := RunPlainCommand(context.Background(), proc, sys.ProcessSpec{Argv: []string{"x"}})
	if code != 3 || err == nil {
		t.Errorf("RunPlainCommand = %d, %v; want status 3 and a failure", code, err)
	}
	if _, err := RunPlainCommand(context.Background(), nil, sys.ProcessSpec{Argv: []string{"/no/such/program/here"}}); err == nil {
		t.Log("the default process runner ran a missing program")
	}
}

func TestOnlyNamesOfNoExportIsEmptyNotAll(t *testing.T) {
	jf := &ast.Justfile{}
	if got := onlyNames(jf, nil); got == nil || len(got) != 0 {
		t.Errorf("onlyNames with no names = %v; want an empty set", got)
	}
}

func TestBacktickedAndExitStatusOfAnError(t *testing.T) {
	if got := backticked([]string{"a", "b c"}); got != "`a` `b c`" {
		t.Errorf("backticked = %q", got)
	}
	if exitStatus(errors.New("plain")) != 1 || exitStatus(&diag.Error{ExitCode: 7}) != 7 {
		t.Error("exitStatus does not give the diagnostic's code, or 1 for a plain error")
	}
}

func containsEnv(env []string, want string) bool {
	for _, e := range env {
		if e == want {
			return true
		}
	}
	return false
}

func TestRunCommandReportsASettingThatIsNotConstant(t *testing.T) {
	jf := fileJustfile(t, "set dotenv-filename := shell('x')\n")
	if code, err := RunCommand(context.Background(), Plan{Justfile: jf, WorkDir: "/w"}, []string{"true"}); code != 1 || err == nil {
		t.Errorf("RunCommand = %d, %v; want the setting's error", code, err)
	}
}

func TestRunCommandReportsAnExportThatFails(t *testing.T) {
	plan, _, _ := newPlan(t, "export x := `exit 1`\n", &sysfake.FakeProcess{Codes: []int{1}})
	if code, err := RunCommand(context.Background(), plan, []string{"true"}); code != 1 || err == nil {
		t.Errorf("RunCommand = %d, %v; want the export's backtick error", code, err)
	}
}

func TestRunCommandReportsADotenvFileThatIsNotUTF8(t *testing.T) {
	jf := fileJustfile(t, "set dotenv-load\nexport a := 'x'\n")
	plan := Plan{Justfile: jf, WorkDir: "/w", FS: sysfake.FakeFS{"/w/.env": "\xff"}, Exec: &sysfake.FakeProcess{}}
	code, err := RunCommand(context.Background(), plan, []string{"true"})
	if code != 1 || err == nil {
		t.Errorf("RunCommand = %d, %v; want the dotenv error", code, err)
	}
}

// With no process runner in the plan, the operating system's runs the command;
// a program that does not exist fails to start, so nothing runs.
func TestRunCommandUsesTheOperatingSystemWhenThePlanHasNoRunner(t *testing.T) {
	plan, _, _ := newPlan(t, "x:\n", nil)
	plan.Exec = nil
	if code, err := RunCommand(context.Background(), plan, []string{"/no/such/just-go-program"}); code != 1 || err == nil {
		t.Errorf("RunCommand = %d, %v; want the start failure", code, err)
	}
}
