package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/sys/sysfake"
)

// --jobs takes a positive number, and each refusal has upstream's wording.
func TestSetJobsRecordsAPositiveCount(t *testing.T) {
	var cfg config
	if err := setJobs(&cfg, "4"); err != nil || cfg.jobs != 4 {
		t.Errorf("setJobs(4) = %v, jobs %d; want jobs 4", err, cfg.jobs)
	}
}

func TestSetJobsRefusesZeroAndNonNumbers(t *testing.T) {
	for value, want := range map[string]string{
		"0": "invalid value '0' for '--jobs <N>': number would be zero for non-zero type",
		"x": "invalid value 'x' for '--jobs <N>': invalid syntax",
	} {
		var cfg config
		var de *diag.Error
		if err := setJobs(&cfg, value); !errors.As(err, &de) || de.Msg != want {
			t.Errorf("setJobs(%q) = %v; want %q", value, err, want)
		}
	}
}

// --one refuses more than one command-line recipe, and allows one.
func TestRunOneAllowsOnlyOneRecipe(t *testing.T) {
	src := "a:\n  @echo a\nb:\n  @echo b\n"
	got := runFake(t, map[string]string{"/w/justfile": src}, nil, "--one", "a", "b")
	if got.code == 0 || !strings.Contains(got.stderr, "expected 1 command-line recipe invocation but found 2") || len(got.proc.Specs) != 0 {
		t.Errorf("run = %d, stderr %q, ran %d; want a refusal and nothing run", got.code, got.stderr, len(got.proc.Specs))
	}
	one := runFake(t, map[string]string{"/w/justfile": src}, nil, "--one", "a")
	if one.code != 0 || len(one.proc.Specs) != 1 {
		t.Errorf("run --one a = %d, ran %d; want 0 and one run", one.code, len(one.proc.Specs))
	}
}

// --allow-missing with only absent recipes runs nothing and succeeds, rather
// than running the default recipe of a justfile that has none.
func TestRunAllowMissingWithOnlyAbsentRecipesSucceeds(t *testing.T) {
	got := runFake(t, map[string]string{"/w/justfile": ""}, nil, "--allow-missing", "foo")
	if got.code != 0 || got.stderr != "" || len(got.proc.Specs) != 0 {
		t.Errorf("run = %d, stderr %q, ran %d; want 0, silent, nothing run", got.code, got.stderr, len(got.proc.Specs))
	}
}

// The short forms of the new flags, and the switches they set.
func TestParseArgsShortFlagsOfTheRunModes(t *testing.T) {
	got, err := parseArgs([]string{"-n", "-V", "-v", "-u", "-e", "-d", "dir", "-s", "x"})
	if err != nil || !got.dryRun || !got.version || !got.verbose || !got.unsorted || !got.edit || got.workingDirectory != "dir" || got.show != "x" {
		t.Errorf("parseArgs = %+v, %v", got, err)
	}
}

// --dry-run and --quiet cannot be given together, as upstream refuses.
func TestDryRunConflictsWithQuiet(t *testing.T) {
	_, err := parseArgs([]string{"--dry-run", "--quiet"})
	var de *diag.Error
	if !errors.As(err, &de) || de.Msg != "the argument '--dry-run' cannot be used with '--quiet'" {
		t.Errorf("parseArgs(--dry-run --quiet) = %v", err)
	}
}

// A dry run writes the recipe's lines and runs nothing.
func TestRunDryRunWritesLinesAndRunsNothing(t *testing.T) {
	got := runFake(t, map[string]string{"/w/justfile": "foo:\n  echo {{ `date` }}\n"}, nil, "--dry-run", "foo")
	if got.code != 0 || got.stderr != "echo `date`\n" || len(got.proc.Specs) != 0 {
		t.Errorf("run = %d, stderr %q, ran %d", got.code, got.stderr, len(got.proc.Specs))
	}
}

// --justfile - reads the justfile from standard input, and the recipes run in
// the --working-directory given.
func TestRunReadsTheJustfileFromStdin(t *testing.T) {
	var stdout, stderr bytes.Buffer
	proc := &sysfake.FakeProcess{}
	fsys := sysfake.FakeFS{"/w/bar/baz": "qux"}
	code := run([]string{"--justfile", "-", "--working-directory", "bar"}, nil, "/w", strings.NewReader("@foo:\n  cat baz\n"), &stdout, &stderr, fsys, proc)
	if code != 0 || len(proc.Specs) != 1 || proc.Specs[0].Dir != "/w/bar" {
		t.Errorf("run = %d, stderr %q, specs %+v", code, stderr.String(), proc.Specs)
	}
}

// A justfile path with no parent is an error, as upstream reports it.
func TestRunRefusesAJustfilePathWithNoParent(t *testing.T) {
	got := runFake(t, nil, nil, "--justfile", "/", "--list")
	if got.code != 1 || got.stderr != "error: justfile path had no parent: /\n" {
		t.Errorf("run = %d, stderr %q", got.code, got.stderr)
	}
}

// A relative --justfile is found from where the command was run, and the recipes
// run in the --working-directory.
func TestRunJustfileResolvesFromTheInvocationDirectory(t *testing.T) {
	got := runFake(t, map[string]string{"/w/foo.md": "```just\n@foo:\n  cat baz\n```\n", "/w/bar/baz": "qux"}, nil,
		"--justfile", "foo.md", "--working-directory", "bar")
	if got.code != 0 || len(got.proc.Specs) != 1 || got.proc.Specs[0].Dir != "/w/bar" {
		t.Errorf("run = %d, stderr %q, specs %+v", got.code, got.stderr, got.proc.Specs)
	}
}

// --init cannot write a justfile that is read from standard input.
func TestInitRefusesAJustfileFromStandardInput(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--justfile", "-", "--init"}, nil, "/w", strings.NewReader(""), &stdout, &stderr, sysfake.FakeFS{}, &sysfake.FakeProcess{})
	if code != 1 || stderr.String() != "error: cannot use justfile from standard input with `--init`\n" {
		t.Errorf("run = %d, stderr %q", code, stderr.String())
	}
}

// errReader is a standard input that cannot be read.
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("broken stdin") }

// stdinSource reads the justfile from stdin only when --justfile is `-`; with no
// stdin it is empty, and a read that fails is an error.
func TestStdinSourceReadsOnlyForAStdinJustfile(t *testing.T) {
	if got, err := stdinSource(config{justfile: "x"}, strings.NewReader("ignored")); got != nil || err != nil {
		t.Errorf("stdinSource of a named file = %q, %v", got, err)
	}
	if got, err := stdinSource(config{justfile: "-"}, nil); got == nil || len(got) != 0 || err != nil {
		t.Errorf("stdinSource with no stdin = %q, %v", got, err)
	}
	if _, err := stdinSource(config{justfile: "-"}, errReader{}); err == nil {
		t.Errorf("stdinSource of a broken stdin gave no error")
	}
}

// A broken stdin is reported as the run's error.
func TestRunReportsABrokenStdin(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--justfile", "-"}, nil, "/w", errReader{}, &stdout, &stderr, sysfake.FakeFS{}, &sysfake.FakeProcess{})
	if code != 1 || !strings.Contains(stderr.String(), "broken stdin") {
		t.Errorf("run = %d, stderr %q", code, stderr.String())
	}
}

// --init --quiet refuses a stdin justfile silently.
func TestInitRefusesStdinQuietly(t *testing.T) {
	var stderr bytes.Buffer
	if code := writeInit(config{justfile: "-", quiet: true}, "/w", sysfake.FakeFS{}, &stderr); code != 1 || stderr.Len() != 0 {
		t.Errorf("writeInit = %d, stderr %q", code, stderr.String())
	}
}

// --init writes the --justfile if given, else the --justfile-name, else justfile.
func TestInitNameFollowsTheFlagsGiven(t *testing.T) {
	for _, c := range []struct {
		cfg  config
		want string
	}{
		{config{justfile: "a.md", justfileName: "b"}, "a.md"},
		{config{justfileName: "b"}, "b"},
		{config{}, "justfile"},
	} {
		if got := initName(c.cfg); got != c.want {
			t.Errorf("initName(%+v) = %q; want %q", c.cfg, got, c.want)
		}
	}
}

// --dump-format json writes the justfile as JSON, and --json is the same; the
// other formats are refused.
func TestDumpFormatJSON(t *testing.T) {
	for _, args := range [][]string{{"--dump", "--dump-format", "json"}, {"--json"}} {
		got := runFake(t, map[string]string{"/w/justfile": "foo:\n"}, nil, args...)
		if got.code != 0 || !strings.HasPrefix(got.stdout, `{"assignments":{},"first":"foo"`) || !strings.HasSuffix(got.stdout, "}\n") {
			t.Errorf("%v: run = %d, stdout %q, stderr %q", args, got.code, got.stdout, got.stderr)
		}
	}
	if _, err := parseArgs([]string{"--dump-format", "yaml"}); err == nil {
		t.Errorf("--dump-format yaml was accepted")
	}
}

// A justfile whose JSON form is not covered yet fails the dump, with the reason.
func TestDumpFormatJSONRefusesWhatItDoesNotEncode(t *testing.T) {
	got := runFake(t, map[string]string{"/w/justfile": "set lists\nx := [\"a\"]\n"}, nil, "--unstable", "--json")
	if got.code != 1 || !strings.Contains(got.stderr, "does not encode this expression yet") {
		t.Errorf("run = %d, stderr %q", got.code, got.stderr)
	}
}

// --groups takes no argument, as upstream refuses one.
func TestGroupsRefusesAnArgument(t *testing.T) {
	got := runFake(t, map[string]string{"/w/justfile": "[group('foo')]\nbar:\n"}, nil, "--groups", "baz")
	if got.code != 1 || got.stderr != "error: `--groups` used with unexpected argument: `baz`\n" {
		t.Errorf("run = %d, stderr %q", got.code, got.stderr)
	}
}

// --shell-command runs the --command words as one script in the justfile's shell.
func TestCommandWithShellCommandRunsItsWordsAsOneScript(t *testing.T) {
	got := runFake(t, map[string]string{"/w/justfile": "set shell := ['printf']\n"}, nil, "--shell-command", "--command", "bar", "baz")
	if got.code != 0 || len(got.proc.Specs) != 1 || strings.Join(got.proc.Specs[0].Argv, "|") != "printf|bar baz" {
		t.Errorf("run = %d, argv %v", got.code, got.proc.Specs)
	}
}
