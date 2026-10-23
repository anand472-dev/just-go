package main

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"gitea.com/wimwian/just-go/internal/sys/sysfake"
)

func TestParseArgsDotenvFlagsRepeatAndNoDotenvSwitches(t *testing.T) {
	got, err := parseArgs([]string{"--dotenv-path", "a", "--dotenv-path", "b", "--dotenv-filename", "f", "--dotenv-command", "c", "--no-dotenv"})
	want := config{dotenvPath: []string{"a", "b"}, dotenvFilename: []string{"f"}, dotenvCommand: []string{"c"}, noDotenv: true}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("parseArgs = %+v, %v; want %+v", got, err, want)
	}
}

func TestDotenvOfCarriesTheFlagsAsGiven(t *testing.T) {
	got := dotenvOf(config{dotenvFilename: []string{"f"}, noDotenv: true})
	if !reflect.DeepEqual(got.Filename, []string{"f"}) || got.Path != nil || !got.NoDotenv {
		t.Errorf("dotenvOf = %+v", got)
	}
}

func TestDotenvErrorNeedsListsForRepeatedFilenameOrPath(t *testing.T) {
	if err := dotenvError(config{dotenvPath: []string{"a"}}); err != nil {
		t.Errorf("one path = %v; want none", err)
	}
	if err := dotenvError(config{dotenvFilename: []string{"a", "b"}}); err == nil {
		t.Error("two filenames were accepted without set lists")
	}
	if err := dotenvError(config{dotenvPath: []string{"a", "b"}}); err == nil {
		t.Error("two paths were accepted without set lists")
	}
}

func TestRunNoDotenvLoadsNothing(t *testing.T) {
	var stdout, stderr bytes.Buffer
	files := sysfake.FakeFS{"/w/justfile": "x := env_var_or_default('K', 'd')\nfoo:\n  @echo {{x}}\n", "/w/.env": "K=loaded\n"}
	proc := &sysfake.FakeProcess{}
	if code := run([]string{"--no-dotenv", "--evaluate", "x"}, nil, "/w", strings.NewReader(""), &stdout, &stderr, files, proc); code != 0 || stdout.String() != "d" {
		t.Errorf("run = %d, %q, %q; want the default, not the .env value", code, stdout.String(), stderr.String())
	}
}

func TestRunDotenvCommandsAreEachRunAndMerged(t *testing.T) {
	var stdout, stderr bytes.Buffer
	proc := &sysfake.FakeProcess{Outputs: []string{"FOO=bar\n", "BAZ=qux\n"}}
	files := sysfake.FakeFS{"/w/justfile": "foo:\n  @echo done\n"}
	if code := run([]string{"--dotenv-command", "one", "--dotenv-command", "two", "foo"}, nil, "/w", strings.NewReader(""), &stdout, &stderr, files, proc); code != 0 {
		t.Errorf("run = %d, %q", code, stderr.String())
	}
	if len(proc.Specs) < 2 || !strings.Contains(strings.Join(proc.Specs[0].Argv, " "), "one") || !strings.Contains(strings.Join(proc.Specs[1].Argv, " "), "two") {
		t.Errorf("dotenv commands ran as %+v; want one then two", proc.Specs)
	}
}

func TestApplyDotenvFlagsReplacesOnlyTheGivenSettings(t *testing.T) {
	// The flags are applied through the runner; this checks the command line side.
	f := dotenvOf(config{dotenvPath: []string{"p"}})
	if f.Filename != nil || f.Command != nil || !reflect.DeepEqual(f.Path, []string{"p"}) {
		t.Errorf("flags = %+v", f)
	}
}
