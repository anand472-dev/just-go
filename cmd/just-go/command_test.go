package main

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"gitea.com/wimwian/just-go/internal/sys/sysfake"
)

func TestParseArgsCommandTakesEveryWordAfterIt(t *testing.T) {
	got, err := parseArgs([]string{"-q", "--command", "sh", "-c", "--quiet"})
	want := config{quiet: true, command: []string{"sh", "-c", "--quiet"}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("parseArgs = %+v, %v; want %+v", got, err, want)
	}
	short, err := parseArgs([]string{"-c", "printf", "foo"})
	if err != nil || !reflect.DeepEqual(short.command, []string{"printf", "foo"}) {
		t.Errorf("parseArgs(-c) = %+v, %v", short, err)
	}
}

func TestParseArgsCommandNeedsAProgram(t *testing.T) {
	if _, err := parseArgs([]string{"--command"}); err == nil {
		t.Error("--command with nothing after it was accepted")
	}
}

func TestRunCommandWithNoJustfileRunsTheProgramAlone(t *testing.T) {
	proc := &sysfake.FakeProcess{Codes: []int{0}}
	var stdout, stderr bytes.Buffer
	code := run([]string{"--command", "printf", "foo"}, []string{"A=1"}, "/w", strings.NewReader(""), &stdout, &stderr, sysfake.FakeFS{}, proc)
	if code != 0 || len(proc.Specs) != 1 || proc.Specs[0].Argv[0] != "printf" {
		t.Errorf("run = %d, %q; want the program run", code, stderr.String())
	}
}

func TestRunCommandWithAJustfileGivesItTheExports(t *testing.T) {
	proc := &sysfake.FakeProcess{Codes: []int{0}}
	var stdout, stderr bytes.Buffer
	code := run([]string{"--command", "sh", "-c", "printf $FOO"}, nil, "/w", strings.NewReader(""), &stdout, &stderr, sysfake.FakeFS{"/w/justfile": "export FOO := 'bar'\nx:\n"}, proc)
	if code != 0 || len(proc.Specs) != 1 || !containsString(proc.Specs[0].Env, "FOO=bar") {
		t.Errorf("run = %d with env %q", code, proc.Specs)
	}
}

func TestRunCommandPassesTheChildsFailure(t *testing.T) {
	proc := &sysfake.FakeProcess{Codes: []int{5}}
	var stdout, stderr bytes.Buffer
	code := run([]string{"--command", "false"}, nil, "/w", strings.NewReader(""), &stdout, &stderr, sysfake.FakeFS{}, proc)
	if code != 5 || !strings.Contains(stderr.String(), "command `false` failed: exit status: 5") {
		t.Errorf("run = %d, %q; want status 5 and the failure", code, stderr.String())
	}
}

func TestRunCommandReportsAProgramThatCannotStartWithNoJustfile(t *testing.T) {
	proc := &sysfake.FakeProcess{Err: errors.New("no such file")}
	var stdout, stderr bytes.Buffer
	code := run([]string{"--command", "nope"}, nil, "/w", strings.NewReader(""), &stdout, &stderr, sysfake.FakeFS{}, proc)
	if code != 1 || !strings.Contains(stderr.String(), "failed to invoke `nope`") {
		t.Errorf("run = %d, %q; want the invoke error", code, stderr.String())
	}
}

func TestRunCommandWithAJustfileReportsAProgramThatCannotStart(t *testing.T) {
	proc := &sysfake.FakeProcess{Err: errors.New("no such file")}
	var stdout, stderr bytes.Buffer
	code := run([]string{"--command", "nope"}, nil, "/w", strings.NewReader(""), &stdout, &stderr, sysfake.FakeFS{"/w/justfile": "x:\n"}, proc)
	if code != 1 || !strings.Contains(stderr.String(), "failed to invoke") {
		t.Errorf("run = %d, %q; want the invoke error", code, stderr.String())
	}
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
