package main

import (
	"bytes"
	"strings"
	"testing"

	"gitea.com/wimwian/just-go/internal/sys/sysfake"
)

// completeOf runs the command as a shell completing words, with JUST_COMPLETE
// set, over files, and returns its stdout.
func completeOf(t *testing.T, files map[string]string, words ...string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	args := append([]string{"--"}, words...)
	code := run(args, []string{"JUST_COMPLETE=bash"}, "/w", strings.NewReader(""), &stdout, &stderr, sysfake.FakeFS(files), &sysfake.FakeProcess{})
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("run = %d, stderr %q; want a clean completion", code, stderr.String())
	}
	return stdout.String()
}

func TestCompletionOffersTheRecipesOfTheJustfile(t *testing.T) {
	files := map[string]string{"/w/justfile": "# does it\nfoo:\nbar:\n_hid:\n"}
	got := completeOf(t, files, "just", "")
	want := "bar\nfoo\tdoes it\n.\njustfile\n--allow-outside-project-paths\n--completions\n--dump\n--evaluate\n--indentation\n--justfile\n--list\n--quiet\n--shell\n--shell-arg\n--unstable\n--yes\n"
	if got != want {
		t.Errorf("completion = %q; want %q", got, want)
	}
}

func TestCompletionOffersVariablesForSet(t *testing.T) {
	files := map[string]string{"/w/justfile": "y := 'a'\nx := 'b'\n_p := 'c'\n"}
	if got := completeOf(t, files, "just", "--set", ""); got != "x\ny\n" {
		t.Errorf("completion of --set = %q; want x and y", got)
	}
}

// --justfile names the file the candidates come from, and the flag value is not
// a candidate.
func TestCompletionReadsTheJustfileNamedByTheFlag(t *testing.T) {
	files := map[string]string{"/w/foo.just": "bar:\n", "/w/justfile": "other:\n"}
	got := completeOf(t, files, "just", "--justfile", "foo.just", "")
	if !strings.HasPrefix(got, "bar\n.\nfoo.just\njustfile\n") {
		t.Errorf("completion = %q; want the recipe of foo.just", got)
	}
}

// --working-directory moves where the justfile and the files are read from.
func TestCompletionReadsTheDirectoryNamedByTheFlag(t *testing.T) {
	files := map[string]string{"/w/sub/justfile": "inner:\n", "/w/outer": ""}
	got := completeOf(t, files, "just", "--working-directory", "sub", "")
	if !strings.HasPrefix(got, "inner\n.\njustfile\n") {
		t.Errorf("completion = %q; want the recipe of sub/justfile", got)
	}
}

// With no justfile, completion still offers the files and the flags.
func TestCompletionWithoutAJustfileOffersFilesAndFlags(t *testing.T) {
	got := completeOf(t, map[string]string{"/w/notes.txt": ""}, "just", "")
	if !strings.HasPrefix(got, ".\nnotes.txt\n--") {
		t.Errorf("completion = %q; want the files and flags", got)
	}
}

func TestEarlyIsOnlyForCompletionAndCompletions(t *testing.T) {
	if _, done := early(config{}, nil, "/w", sysfake.FakeFS{}, nil, nil); done {
		t.Error("early handled a run that is neither a completion nor a script")
	}
}

func TestCompletionsPrintsTheScriptForTheShell(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--completions", "zsh"}, nil, "/w", strings.NewReader(""), &stdout, &stderr, sysfake.FakeFS{}, &sysfake.FakeProcess{})
	if code != 0 || !strings.Contains(stdout.String(), "compdef _just_go just-go") || stderr.Len() != 0 {
		t.Errorf("run = %d, %q, %q; want the zsh script", code, stdout.String(), stderr.String())
	}
}

func TestCompletionsRejectsAWordAfterTheShell(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--completions", "bash", "extra"}, nil, "/w", strings.NewReader(""), &stdout, &stderr, sysfake.FakeFS{}, &sysfake.FakeProcess{})
	if code != 1 || stderr.String() != "error: `--completions` used with unexpected argument: `extra`\n" {
		t.Errorf("run = %d, %q; want status 1 and the unexpected-argument error", code, stderr.String())
	}
}

func TestCompletionsRefusesAShellWithoutAScript(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--completions", "elvish"}, nil, "/w", strings.NewReader(""), &stdout, &stderr, sysfake.FakeFS{}, &sysfake.FakeProcess{})
	if code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "completions for `elvish` are not generated") {
		t.Errorf("run = %d, stdout %q, stderr %q; want status 1 and the refusal", code, stdout.String(), stderr.String())
	}
}

func TestKindOfAnUnknownFlagIsASwitch(t *testing.T) {
	if got := kindOf("no-such-flag"); got != switchFlag {
		t.Errorf("kindOf(unknown) = %v; want switchFlag", got)
	}
}

func TestFlagNamesAreTheTableOfLongFlags(t *testing.T) {
	names := flagNames()
	if len(names) != len(flags) || names[0] != flags[0].name {
		t.Errorf("flagNames = %q; want every long flag in the table's order", names)
	}
}
