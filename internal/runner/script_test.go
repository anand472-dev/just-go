package runner

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/parser"
	"gitea.com/wimwian/just-go/internal/sys"
	"gitea.com/wimwian/just-go/internal/sys/sysfake"
)

// write is one file a script recipe asked the filesystem to write.
type write struct {
	path, data string
	perm       fs.FileMode
}

// made is one directory a script recipe asked the filesystem to make.
type made struct{ dir, prefix string }

// recordingFS is a FakeFS that remembers what it was asked to make, write and
// remove, and can be made to fail.
type recordingFS struct {
	sysfake.FakeFS
	writes   []write
	made     []made
	removed  []string
	mkdirErr error
	writeErr error
}

func (r *recordingFS) WriteFile(path string, data []byte, perm fs.FileMode) error {
	if r.writeErr != nil {
		return r.writeErr
	}
	r.writes = append(r.writes, write{path, string(data), perm})
	return r.FakeFS.WriteFile(path, data, perm)
}

func (r *recordingFS) MkdirTemp(dir, prefix string) (string, error) {
	r.made = append(r.made, made{dir, prefix})
	if r.mkdirErr != nil {
		return "", r.mkdirErr
	}
	return r.FakeFS.MkdirTemp(dir, prefix)
}

func (r *recordingFS) RemoveAll(path string) error {
	r.removed = append(r.removed, path)
	return r.FakeFS.RemoveAll(path)
}

// scriptDir is where the fake filesystem makes the directory of a script when
// the justfile does not say where.
var scriptDir = filepath.Join("/tmp", "just-fake")

// scriptPlan parses src and returns a plan that runs it against a fake
// filesystem and a fake process.
func scriptPlan(t testing.TB, src string) (Plan, *recordingFS, *sysfake.FakeProcess, *bytes.Buffer) {
	t.Helper()
	fake := &sysfake.FakeProcess{}
	plan, _, stderr := newPlan(t, src, fake)
	rec := &recordingFS{FakeFS: sysfake.FakeFS{}}
	plan.FS = rec
	return plan, rec, fake, stderr
}

// scriptOf runs src and returns the text of the one script it wrote.
func scriptOf(t *testing.T, src string) string {
	t.Helper()
	plan, rec, _, _ := scriptPlan(t, src)
	mustRun(t, plan)
	if len(rec.writes) != 1 {
		t.Fatalf("wrote %d files, want 1: %+v", len(rec.writes), rec.writes)
	}
	return rec.writes[0].data
}

// The text of a script keeps the line numbers of the justfile, so that an
// error in the script points at the line it came from.
func TestRunScriptTextKeepsLineNumbers(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"script::shebang_line_numbers#1", "foo:\n  #!/usr/bin/env cat\n\n  a\n\n  b\n\n\n  c\n\n",
			"#!/usr/bin/env cat\n\n\na\n\nb\n\n\nc\n"},
		{"script::multiline_shebang_line_numbers#1",
			"foo:\n  #!/usr/bin/env cat\n  #!shebang\n  #!shebang\n\n  a\n\n  b\n\n\n  c\n\n",
			"#!/usr/bin/env cat\n#!shebang\n#!shebang\n\n\na\n\nb\n\n\nc\n"},
		{"script::shebang_line_numbers_with_multiline_constructs#1",
			"foo b='b'\\\n        :\n  #!/usr/bin/env cat\n\n  a\n\n  {{ \\\n     b \\\n  }}\n\n\n  c\n\n",
			"#!/usr/bin/env cat\n\n\n\na\n\nb\n\n\n\n\nc\n"},
		{"interpolation::shebang_line_numbers_are_correct_with_multi_line_interpolations#1",
			"foo:\n  #!/usr/bin/env cat\n  echo {{\n    'a'\n+ 'b'\n       + 'c'\n  }}\n  echo foo\n",
			"#!/usr/bin/env cat\n\necho abc\n\n\n\n\necho foo\n"},
		{"script::script_line_numbers#1", "[script('cat')]\nfoo:\n  FOO\n\n  BAR\n", "\n\nFOO\n\nBAR\n"},
		{"script::script_line_numbers_with_multi_line_recipe_signature#1",
			"[script('cat')]\nfoo bar='baz' \\\n  :\n  FOO\n\n  BAR\n\n  {{ \\\n     bar \\\n  }}\n\n  BAZ\n",
			"\n\n\nFOO\n\nBAR\n\nbaz\n\n\n\nBAZ\n"},
		{"script::allowed_with_shebang#1", "[script('cat')]\nfoo:\n  #!/bin/sh\n", "\n\n#!/bin/sh\n"},
		{"script::default_script_recipe_with_shebang_uses_shebang#1",
			"set default-script\n\nfoo:\n  #!/usr/bin/env cat\n  bar\n", "#!/usr/bin/env cat\n\n\n\nbar\n"},
		{"script::default_script_allows_extra_leading_whitespace#1",
			"set default-script\n\nfoo:\n  echo foo\n    echo bar\n", "\n\n\necho foo\n  echo bar\n"},
		{"tempdir::setting#1", "set tempdir := '.'\nfoo:\n    #!/usr/bin/env bash\n    cat just*/foo\n",
			"#!/usr/bin/env bash\n\n\ncat just*/foo\n"},
		{"a continuation is not joined", "foo:\n  #!/usr/bin/env cat\n  echo a \\\n    b\n", "#!/usr/bin/env cat\n\necho a \\\n  b\n"},
		{"the sigils stay and a line of spaces is blank", "foo:\n  #!/usr/bin/env cat\n  @echo c\n   \n  -x\n",
			"#!/usr/bin/env cat\n\n@echo c\n\n-x\n"},
		{"a tab is kept", "foo:\n\t#!/usr/bin/env cat\n\techo a\n\t\techo b\n", "#!/usr/bin/env cat\n\necho a\n\techo b\n"},
		{"no body", "[script('cat')]\nfoo:\n", ""},
		{"a doubled brace", "foo:\n  #!/usr/bin/env cat\n  echo {{{{x}}\n", "#!/usr/bin/env cat\n\necho {{x}}\n"},
		{"a comment is part of the script", "set ignore-comments\nfoo:\n  #!/usr/bin/env cat\n  # c\n  echo b\n",
			"#!/usr/bin/env cat\n\n\n# c\necho b\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := scriptOf(t, tc.src); got != tc.want {
				t.Errorf("script = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRunShebangRunsTheScriptFile(t *testing.T) {
	plan, rec, fake, _ := scriptPlan(t, "foo:\n  #!/bin/sh\n  echo bar\n")
	mustRun(t, plan)
	path := filepath.Join(scriptDir, "foo")
	if want := []made{{"", "just-"}}; !reflect.DeepEqual(rec.made, want) {
		t.Errorf("made %+v, want %+v", rec.made, want)
	}
	if want := []write{{path, "#!/bin/sh\n\necho bar\n", 0o700}}; !reflect.DeepEqual(rec.writes, want) {
		t.Errorf("wrote %+v, want %+v", rec.writes, want)
	}
	if len(fake.Specs) != 1 || !reflect.DeepEqual(fake.Specs[0].Argv, []string{path}) || fake.Specs[0].Dir != plan.WorkDir {
		t.Errorf("ran %+v, want the script, in %s", fake.Specs, plan.WorkDir)
	}
	if want := []string{scriptDir}; !reflect.DeepEqual(rec.removed, want) {
		t.Errorf("removed %v, want %v", rec.removed, want)
	}
}

func TestRunScriptRecipeRunsItsInterpreter(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		want      []string
	}{
		{"script::no_arguments#1", "[script('sh')]\nfoo:\n  echo foo\n", []string{"sh", "@"}},
		{"script::with_arguments#1", "[script('sh', '-x')]\nfoo:\n  echo foo\n", []string{"sh", "-x", "@"}},
		{"script::no_arguments_with_default_script_interpreter#1", "[script]\nfoo:\n  echo foo\n", []string{"sh", "-eu", "@"}},
		{"script::no_arguments_with_non_default_script_interpreter#1",
			"set script-interpreter := ['bash', '-e']\n\n[script]\nfoo:\n  echo foo\n", []string{"bash", "-e", "@"}},
		{"script::default_script_uses_script_interpreter#1",
			"set default-script\nset script-interpreter := ['sh', '-x']\n\nfoo:\n  echo foo\n", []string{"sh", "-x", "@"}},
		{"script::default_script_makes_recipes_scripts#1", "set default-script\n\nfoo:\n  bar=baz\n", []string{"sh", "-eu", "@"}},
		{"the attribute beats the setting", "set script-interpreter := ['bash']\n[script('zsh')]\nfoo:\n  echo foo\n", []string{"zsh", "@"}},
		{"the attribute beats the shebang", "[script('cat')]\nfoo:\n  #!/bin/sh\n", []string{"cat", "@"}},
		{"the setting beats the default for a plain recipe", "set default-script\nset script-interpreter := ['bash']\nfoo:\n  echo foo\n",
			[]string{"bash", "@"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, rec, fake, _ := scriptPlan(t, tc.src)
			mustRun(t, plan)
			want := append([]string(nil), tc.want...)
			want[len(want)-1] = filepath.Join(scriptDir, "foo")
			if len(fake.Specs) != 1 || !reflect.DeepEqual(fake.Specs[0].Argv, want) {
				t.Errorf("ran %+v, want %q", fake.Specs, want)
			}
			if len(rec.writes) != 1 || rec.writes[0].path != want[len(want)-1] {
				t.Errorf("wrote %+v, want the script %s", rec.writes, want[len(want)-1])
			}
		})
	}
}

func TestRunScriptFileName(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"attributes::extension#1", "[extension: '.txt']\nbaz:\n  #!/bin/sh\n  echo $0\n", "baz.txt"},
		{"a script recipe takes it too", "[script('sh')]\n[extension: '.sh']\nbaz:\n  echo $0\n", "baz.sh"},
		{"script::use_final_path_component_of_script_interpreter_to_determine_shell_kind#1",
			"[script('./pwsh.exe')]\nfoo:\n", "foo.ps1"},
		{"cmd", "[script('cmd')]\nfoo:\n", "foo.bat"},
		{"a shebang for powershell", "foo:\n  #!/usr/bin/pwsh\n", "foo.ps1"},
		{"the extension beats the kind", "[extension: '.sh']\n[script('pwsh.exe')]\nfoo:\n", "foo.sh"},
		{"an empty extension is one", "[extension: '']\n[script('pwsh.exe')]\nfoo:\n", "foo"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, rec, _, _ := scriptPlan(t, tc.src)
			mustRun(t, plan)
			if got := filepath.Base(rec.writes[0].path); got != tc.want {
				t.Errorf("script file = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRunScriptWithoutBOMOnUnix(t *testing.T) {
	// shebang::powershell_shebang_on_unix_has_no_bom#1
	if got := scriptOf(t, "foo:\n  #!/usr/bin/pwsh\n  echo bar\n"); strings.HasPrefix(got, "\uFEFF") {
		t.Errorf("script = %q, want no byte order mark", got)
	}
}

func TestRunScriptTempdirSetting(t *testing.T) {
	plan, rec, _, _ := scriptPlan(t, "set tempdir := 'tmp'\nfoo:\n  #!/bin/sh\n")
	mustRun(t, plan)
	if want := []made{{filepath.Join(plan.WorkDir, "tmp"), "just-"}}; !reflect.DeepEqual(rec.made, want) {
		t.Errorf("made %+v, want %+v", rec.made, want)
	}
	plan, rec, _, _ = scriptPlan(t, "set tempdir := '/abs'\nfoo:\n  #!/bin/sh\n")
	mustRun(t, plan)
	if want := []made{{"/abs", "just-"}}; !reflect.DeepEqual(rec.made, want) {
		t.Errorf("made %+v, want %+v", rec.made, want)
	}
}

func TestRunScriptDirectory(t *testing.T) {
	for _, tc := range []struct {
		name, src, want string
	}{
		{"the directory of the plan", "foo:\n  #!/bin/sh\n", "@work"},
		{"no-cd is the directory of the invocation", "[no-cd]\nfoo:\n  #!/bin/sh\n", "/invocation"},
		{"set no-cd is too", "set no-cd\nfoo:\n  #!/bin/sh\n", "/invocation"},
		{"working-directory", "[working-directory: 'sub']\nfoo:\n  #!/bin/sh\n", "@work/sub"},
		{"set working-directory", "set working-directory := 'sub'\nfoo:\n  #!/bin/sh\n", "@work/sub"},
		{"script recipes too", "[script('sh')]\n[working-directory: 'sub']\nfoo:\n  echo\n", "@work/sub"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, _, fake, _ := scriptPlan(t, tc.src)
			plan.InvocationDir = "/invocation"
			mustRun(t, plan)
			want := strings.Replace(tc.want, "@work", plan.WorkDir, 1)
			if got := fake.Specs[0].Dir; got != want {
				t.Errorf("ran in %q, want %q", got, want)
			}
		})
	}
}

func TestRunScriptEnvironment(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		want      map[string]string
	}{
		{"export::shebang#1", "export FOO := 'a'\nbaz := 'c'\nexport BAR := 'b'\nexport ABC := FOO + BAR + baz\n\nwut:\n  #!/bin/sh\n  echo $FOO\n",
			map[string]string{"FOO": "a", "BAR": "b", "ABC": "abc", "PLAN": "p"}},
		{"export::setting_shebang#1", "set export\n\nA := 'hello'\n\nfoo B='goodbye':\n  #!/bin/sh\n  echo $A\n",
			map[string]string{"A": "hello", "B": "goodbye", "PLAN": "p"}},
		{"attributes::env_attribute_overrides_export_in_script#1",
			"export FOO := 'export'\n\n[env('FOO', 'attribute')]\nbar:\n  #!/bin/sh\n  echo $FOO\n",
			map[string]string{"FOO": "attribute", "PLAN": "p"}},
		{"attributes::env_attribute_with_expression_in_script#1",
			"suffix := 'world'\n\n[env('GREETING', 'hello ' + suffix)]\nfoo:\n  #!/bin/sh\n",
			map[string]string{"GREETING": "hello world", "PLAN": "p"}},
		{"a script recipe", "export A := 'a'\n[script('sh')]\nfoo:\n  echo\n", map[string]string{"A": "a", "PLAN": "p"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, _, fake, _ := scriptPlan(t, tc.src)
			plan.Env = []string{"PLAN=p"}
			mustRun(t, plan)
			if got := environmentOf(fake.Specs[0]); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("environment = %v, want %v", got, tc.want)
			}
		})
	}
}

// A script gets the positional arguments after the path of the file, and not
// the name of the recipe a command gets.
func TestRunScriptPositionalArguments(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		args      []string
		want      []string
	}{
		{"positional_arguments::shebang#1", "set positional-arguments\n\nfoo bar:\n  #!/bin/sh\n  echo $1\n", []string{"hello"},
			[]string{"@", "hello"}},
		{"positional_arguments::shebang_with_attribute#1", "[positional-arguments]\nfoo bar:\n  #!/bin/sh\n  echo $1\n", []string{"hello"},
			[]string{"@", "hello"}},
		{"a script recipe", "set positional-arguments\n[script('sh')]\nfoo bar:\n  echo $1\n", []string{"hello"},
			[]string{"sh", "@", "hello"}},
		{"without the setting", "foo bar:\n  #!/bin/sh\n  echo $1\n", []string{"hello"}, []string{"@"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, _, fake, _ := scriptPlan(t, tc.src)
			plan.Calls = []Call{{Recipe: "foo", Args: tc.args}}
			mustRun(t, plan)
			want := strings.Split(strings.Replace(strings.Join(tc.want, "\x00"), "@", filepath.Join(scriptDir, "foo"), 1), "\x00")
			if got := fake.Specs[0].Argv; !reflect.DeepEqual(got, want) {
				t.Errorf("argv = %q, want %q", got, want)
			}
		})
	}
}

func TestRunScriptRunsOnTheStreamsOfThePlan(t *testing.T) {
	plan, _, fake, _ := scriptPlan(t, "foo:\n  #!/bin/sh\n")
	var stdout bytes.Buffer
	plan.Stdin, plan.Stdout = strings.NewReader("in"), &stdout
	mustRun(t, plan)
	spec := fake.Specs[0]
	if spec.Stdin != plan.Stdin || spec.Stdout != plan.Stdout || spec.Stderr != plan.Stderr {
		t.Errorf("spec streams = %v %v %v, want those of the plan", spec.Stdin, spec.Stdout, spec.Stderr)
	}
}

// A script does not echo its lines: only a quiet recipe does, as upstream does
// when it is not itself quiet.
func TestRunScriptEcho(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		quiet     bool
		want      string
	}{
		{"misc::quiet_shebang_recipe#1", "@quiet:\n  #!/bin/sh\n  echo hello\n", false, "#!/bin/sh\necho hello\n"},
		{"shebang::echo#1", "@baz:\n  #!/bin/sh\n  echo fizz\n", false, "#!/bin/sh\necho fizz\n"},
		{"a loud recipe", "foo:\n  #!/bin/sh\n  echo fizz\n", false, ""},
		{"-q", "@foo:\n  #!/bin/sh\n  echo fizz\n", true, ""},
		{"set quiet does nothing", "set quiet\nfoo:\n  #!/bin/sh\n  echo fizz\n", false, ""},
		{"blank lines are echoed", "@foo:\n  #!/bin/sh\n\n  echo fizz\n", false, "#!/bin/sh\n\necho fizz\n"},
		{"a script recipe", "[script('sh')]\n@foo:\n  echo fizz\n", false, "echo fizz\n"},
		{"the evaluated text", "x := 'y'\n@foo:\n  #!/bin/sh\n  echo {{x}}\n", false, "#!/bin/sh\necho y\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, _, _, stderr := scriptPlan(t, tc.src)
			plan.Quiet = tc.quiet
			mustRun(t, plan)
			if got := stderr.String(); got != tc.want {
				t.Errorf("stderr = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRunScriptTimestamp(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"timestamps::attribute_script#1", "[timestamp]\nrecipe:\n  #!/bin/sh\n  echo foo\n", "[15:04:59] recipe\n"},
		{"a format", "[timestamp('%S')]\nrecipe:\n  #!/bin/sh\n", "[59] recipe\n"},
		{"a script recipe", "[timestamp]\n[script('sh')]\nrecipe:\n  echo foo\n", "[15:04:59] recipe\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, _, _, stderr := scriptPlan(t, tc.src)
			var prompt bytes.Buffer
			plan.Clock, plan.Prompt, plan.Quiet = stampClock, &prompt, true
			mustRun(t, plan)
			if stderr.Len() != 0 || prompt.String() != tc.want {
				t.Errorf("stderr = %q, prompt = %q; want %q on the prompt stream only", stderr.String(), prompt.String(), tc.want)
			}
		})
	}
}

func TestRunScriptTimestampWithAnInvalidFormat(t *testing.T) {
	plan, rec, fake, _ := scriptPlan(t, "[timestamp('%Q')]\nrecipe:\n  #!/bin/sh\n")
	plan.Clock = stampClock
	_, err := Run(context.Background(), plan)
	var de *diag.Error
	if !errors.As(err, &de) || de.Kind != diag.KindRuntime || len(rec.made) != 0 || len(fake.Specs) != 0 {
		t.Errorf("err = %v, made %v, ran %v; want a runtime error before anything is made or run", err, rec.made, fake.Specs)
	}
}

func TestRunInvalidShebang(t *testing.T) {
	// shebang::invalid_shebang_error#1
	plan, rec, fake, _ := scriptPlan(t, "foo:\n  #!{{ '' }}\n  echo bar\n")
	_, err := Run(context.Background(), plan)
	var de *diag.Error
	if !errors.As(err, &de) || de.Kind != diag.KindRuntime || de.Msg != "recipe `foo` has invalid shebang `#!`" || de.File != "" {
		t.Errorf("err = %#v, want the invalid shebang error, with no location", err)
	}
	if len(rec.made) != 0 || len(fake.Specs) != 0 {
		t.Errorf("made %v, ran %v; want nothing", rec.made, fake.Specs)
	}
}

// Comments are evaluated in a script, even under set ignore-comments.
func TestRunScriptEvaluatesComments(t *testing.T) {
	// ignore_comments::ignore_comments_evaluates_comments_in_scripts#1
	plan, _, fake, _ := scriptPlan(t, "set ignore-comments\n\nfoo:\n  #!/bin/sh\n  # {{ `fail` }}\n  echo ok\n")
	fake.Codes = []int{5}
	_, err := Run(context.Background(), plan)
	var de *diag.Error
	if !errors.As(err, &de) || de.ExitCode != 5 || de.Line != 5 {
		t.Errorf("err = %#v, want the backtick in the comment to fail, on line 5", err)
	}
}

// The working directory of a script is evaluated after its lines, so a
// failure of the lines comes first.
func TestRunScriptWorkingDirectoryFailure(t *testing.T) {
	plan, rec, fake, _ := scriptPlan(t, "[working-directory: `fail`]\nfoo:\n  #!/bin/sh\n")
	fake.Codes = []int{5}
	_, err := Run(context.Background(), plan)
	var de *diag.Error
	if !errors.As(err, &de) || de.ExitCode != 5 || len(rec.made) != 0 {
		t.Errorf("err = %#v, made %v; want the backtick to fail with status 5 before anything is made", err, rec.made)
	}
}

func TestRunScriptBacktickFailure(t *testing.T) {
	// misc::shebang_backtick_failure#1
	plan, rec, fake, _ := scriptPlan(t, "foo:\n #!/bin/sh\n echo hello\n echo {{`exit 123`}}")
	fake.Codes = []int{123}
	_, err := Run(context.Background(), plan)
	var de *diag.Error
	if !errors.As(err, &de) || de.ExitCode != 123 || de.Line != 4 || de.Col != 9 {
		t.Errorf("err = %#v, want a failure with status 123 at 4:9", err)
	}
	if len(rec.made) != 0 || len(fake.Specs) != 1 {
		t.Errorf("made %v, ran %d; want only the backtick run", rec.made, len(fake.Specs))
	}
}

// What a script recipe reports when it ends badly has no line number, since
// the script is one command.
func TestRunScriptFailures(t *testing.T) {
	interrupted := func(signal int) error { return &sys.SignalError{Caught: signal} }
	for _, tc := range []struct {
		name, src string
		code      int
		err       error
		wantExit  int
		wantMsg   string
	}{
		{"shebang::run_shebang#1", "a:\n  #!/usr/bin/env sh\n  x\n", 200, nil, 200, "error: recipe `a` failed with exit code 200\n"},
		{"a script recipe", "[script('sh')]\na:\n  x\n", 3, nil, 3, "error: recipe `a` failed with exit code 3\n"},
		{"no_exit_message::shebang_exit_message_suppressed#1", "[no-exit-message]\nhello:\n  #!/bin/sh\n", 100, nil, 100, ""},
		{"no_exit_message::shebang_exit_message_setting_suppressed#1", "set no-exit-message\nhello:\n  #!/bin/sh\n", 100, nil, 100, ""},
		{"exit-message beats the setting", "set no-exit-message\n[exit-message]\nhello:\n  #!/bin/sh\n", 100, nil, 100,
			"error: recipe `hello` failed with exit code 100\n"},
		{"no_exit_message::signal_exit_message_not_suppressed#1", "default:\n  #!/usr/bin/env sh\n", -1, &sys.SignalError{Signal: 15}, 143,
			"error: recipe `default` was terminated by signal 15\n"},
		{"no_exit_message::signal_exit_message_suppressed#1", "[no-exit-message]\ndefault:\n  #!/bin/sh\n", -1, &sys.SignalError{Signal: 15}, 143, ""},
		{"no status and no signal", "a:\n  #!/bin/sh\n", -1, nil, 1, "error: recipe `a` failed for an unknown reason\n"},
		{"interrupted", "a:\n  #!/bin/sh\n", 0, interrupted(2), 130, "error: interrupted by SIGINT\n"},
		{"interrupted and continued", "[continue]\na:\n  #!/bin/sh\n", 0, interrupted(2), 0, ""},
		{"interrupted by another signal", "[continue]\na:\n  #!/bin/sh\n", 0, interrupted(1), 129, "error: interrupted by SIGHUP\n"},
		{"a failure beats an interruption", "a:\n  #!/bin/sh\n", 3, interrupted(2), 3, "error: recipe `a` failed with exit code 3\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, _, fake, stderr := scriptPlan(t, tc.src)
			fake.Codes = []int{tc.code}
			fake.Errs = []error{tc.err}
			res, err := Run(context.Background(), plan)
			if err != nil || res.ExitCode != tc.wantExit || stderr.String() != tc.wantMsg {
				t.Errorf("exit %d, error %v, stderr %q; want exit %d, stderr %q", res.ExitCode, err, stderr.String(), tc.wantExit, tc.wantMsg)
			}
		})
	}
}

func TestRunScriptQuietPlanSilencesFailure(t *testing.T) {
	plan, _, fake, stderr := scriptPlan(t, "a:\n  #!/bin/sh\n")
	fake.Codes = []int{3}
	plan.Quiet = true
	if res := mustRun(t, plan); res.ExitCode != 3 || stderr.Len() != 0 {
		t.Errorf("exit %d, stderr %q; want exit 3 and nothing written", res.ExitCode, stderr.String())
	}
}

func TestRunScriptCannotStart(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"a shebang", "foo:\n  #!/bin/sh -e\n", "recipe `foo` with shebang `#!/bin/sh -e` execution error: file does not exist"},
		{"a shebang without an argument", "foo:\n  #!/bin/sh\n", "recipe `foo` with shebang `#!/bin/sh` execution error: file does not exist"},
		{"a script", "[script('sh', '-x')]\nfoo:\n", "recipe `foo` with command `sh -x` execution error: file does not exist"},
		{"the default script", "[script]\nfoo:\n", "recipe `foo` with command `sh -eu` execution error: file does not exist"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, rec, fake, _ := scriptPlan(t, tc.src)
			fake.Err = fs.ErrNotExist
			res, err := Run(context.Background(), plan)
			var de *diag.Error
			if !errors.As(err, &de) || de.Kind != diag.KindRuntime || de.Msg != tc.want || res.ExitCode != 1 {
				t.Errorf("exit %d, err %#v; want exit 1 and %q", res.ExitCode, err, tc.want)
			}
			if len(rec.removed) != 1 {
				t.Errorf("removed %v, want the script directory", rec.removed)
			}
		})
	}
}

// cancelling is a process that ends the run of its context, as a cancelled
// context ends a real one.
type cancelling struct{ cancel context.CancelFunc }

func (c cancelling) Run(context.Context, sys.ProcessSpec) (int, error) {
	c.cancel()
	return -1, context.Canceled
}

func TestRunScriptCancelled(t *testing.T) {
	plan, rec, fake, _ := scriptPlan(t, "foo:\n  #!/bin/sh\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Run(ctx, plan); !errors.Is(err, context.Canceled) || len(rec.made) != 0 || len(fake.Specs) != 0 {
		t.Errorf("err = %v, made %v, ran %v; want context.Canceled before anything is made", err, rec.made, fake.Specs)
	}
	ctx, cancel = context.WithCancel(context.Background())
	plan.Exec = cancelling{cancel}
	if _, err := Run(ctx, plan); !errors.Is(err, context.Canceled) || len(rec.removed) != 1 {
		t.Errorf("err = %v, removed %v; want context.Canceled and the directory removed", err, rec.removed)
	}
}

func TestRunScriptTempdirFailures(t *testing.T) {
	boom := errors.New("boom")
	want := "recipe `foo` could not be run because of an IO error while trying to create a temporary directory or write a file to that directory: boom"
	for _, tc := range []struct {
		name        string
		mkdir, file error
		removed     int
	}{
		{"no directory", boom, nil, 0},
		{"no file", nil, boom, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, rec, fake, _ := scriptPlan(t, "foo:\n  #!/bin/sh\n")
			rec.mkdirErr, rec.writeErr = tc.mkdir, tc.file
			res, err := Run(context.Background(), plan)
			var de *diag.Error
			if !errors.As(err, &de) || de.Kind != diag.KindRuntime || de.Msg != want || res.ExitCode != 1 {
				t.Errorf("exit %d, err %#v; want exit 1 and %q", res.ExitCode, err, want)
			}
			if len(rec.removed) != tc.removed || len(fake.Specs) != 0 {
				t.Errorf("removed %v, ran %v; want %d removed and nothing run", rec.removed, fake.Specs, tc.removed)
			}
		})
	}
}

// A script recipe is one command to the dependencies around it.
func TestRunScriptWithDependencies(t *testing.T) {
	plan, _, fake, _ := scriptPlan(t, "a: b\n  #!/bin/sh\n  echo a\nb:\n  echo b\n")
	mustRun(t, plan)
	if len(fake.Specs) != 2 || fake.Specs[0].Argv[len(fake.Specs[0].Argv)-1] != "echo b" ||
		fake.Specs[1].Argv[0] != filepath.Join(scriptDir, "a") {
		t.Errorf("ran %+v, want b, then the script of a", fake.Specs)
	}
}

func TestRunScriptOnWindows(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		outputs   []string
		wantArgv  [][]string // the commands run, with @ for the script
		wantText  string
	}{
		{"a bare interpreter", "foo:\n  #!pwsh\n  echo bar\n", nil, [][]string{{"pwsh", "@"}}, "\uFEFF\n\necho bar\n"},
		{"an argument", "foo:\n  #!cmd.exe /C\n  echo bar\n", nil, [][]string{{"cmd.exe", "/C", "@"}}, "\n\necho bar\n"},
		{"a path is translated", "foo:\n  #!/bin/sh -e\n  echo bar\n", []string{"C:\\msys\\bin\\sh.exe\r\n"},
			[][]string{{"cygpath", "--windows", "/bin/sh"}, {"C:\\msys\\bin\\sh.exe", "-e", "@"}}, "\n\necho bar\n"},
		{"a script recipe", "[script('powershell')]\nfoo:\n  echo bar\n", nil, [][]string{{"powershell", "@"}}, "\uFEFF\n\necho bar\n"},
		{"cmd takes no byte order mark", "[script('cmd')]\nfoo:\n  echo bar\n", nil, [][]string{{"cmd", "@"}}, "\n\necho bar\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, rec, fake, _ := scriptPlan(t, tc.src)
			plan.OS = "windows"
			fake.Outputs = tc.outputs
			mustRun(t, plan)
			path := rec.writes[0].path
			var got [][]string
			for _, s := range fake.Specs {
				got = append(got, s.Argv)
			}
			want := make([][]string, len(tc.wantArgv))
			for i, argv := range tc.wantArgv {
				want[i] = append([]string(nil), argv...)
				for j, a := range want[i] {
					if a == "@" {
						want[i][j] = path
					}
				}
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("ran %q, want %q", got, want)
			}
			if rec.writes[0].data != tc.wantText {
				t.Errorf("script = %q, want %q", rec.writes[0].data, tc.wantText)
			}
		})
	}
}

func TestRunScriptOnWindowsTranslatesInTheWorkingDirectory(t *testing.T) {
	plan, _, fake, _ := scriptPlan(t, "set working-directory := 'sub'\nfoo:\n  #!/bin/sh\n")
	plan.OS, plan.Cygpath = "windows", "mycygpath"
	fake.Outputs = []string{"sh.exe"}
	mustRun(t, plan)
	want := filepath.Join(plan.WorkDir, "sub")
	if fake.Specs[0].Argv[0] != "mycygpath" || fake.Specs[0].Dir != want || fake.Specs[1].Dir != want {
		t.Errorf("ran %+v, want mycygpath and the script in %s", fake.Specs, want)
	}
}

func TestRunScriptOnWindowsCygpathFailures(t *testing.T) {
	for _, tc := range []struct {
		name string
		code int
		err  error
		want string
	}{
		{"a status", 3, nil, "cygpath failed with exit code 3 while translating recipe `foo` shebang interpreter path"},
		{"a signal", -1, &sys.SignalError{Signal: 9}, "cygpath terminated by signal 9 while translating recipe `foo` shebang interpreter path"},
		{"no status", -1, nil, "cygpath experienced an unknown failure while translating recipe `foo` shebang interpreter path"},
		{"not found", -1, fs.ErrNotExist,
			"could not find `cygpath` executable to translate recipe `foo` shebang interpreter path:\nfile does not exist"},
		{"not allowed", -1, fs.ErrPermission,
			"could not run `cygpath` executable to translate recipe `foo` shebang interpreter path:\npermission denied"},
		{"another failure", -1, errors.New("boom"), "could not run `cygpath` executable:\nboom"},
		{"interrupted", 0, &sys.SignalError{Caught: 2}, "cygpath succeeded but `just` was interrupted by SIGINT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, rec, fake, _ := scriptPlan(t, "foo:\n  #!/bin/sh\n")
			plan.OS = "windows"
			fake.Codes, fake.Errs = []int{tc.code}, []error{tc.err}
			res, err := Run(context.Background(), plan)
			var de *diag.Error
			if !errors.As(err, &de) || de.Kind != diag.KindRuntime || de.Msg != tc.want || res.ExitCode != 1 {
				t.Errorf("exit %d, err %#v; want exit 1 and %q", res.ExitCode, err, tc.want)
			}
			if len(fake.Specs) != 1 || len(rec.removed) != 1 {
				t.Errorf("ran %d commands, removed %v; want only cygpath, and the directory removed", len(fake.Specs), rec.removed)
			}
		})
	}
}

func TestRunScriptOnWindowsCygpathOutputThatIsNotText(t *testing.T) {
	plan, _, fake, _ := scriptPlan(t, "foo:\n  #!/bin/sh\n")
	plan.OS = "windows"
	fake.Outputs = []string{"\xff"}
	_, err := Run(context.Background(), plan)
	want := "cygpath successfully translated recipe `foo` shebang interpreter path, but output was not utf8: " +
		"invalid utf-8 sequence of 1 bytes from index 0"
	var de *diag.Error
	if !errors.As(err, &de) || de.Msg != want {
		t.Errorf("err = %#v, want %q", err, want)
	}
}

func TestParseShebang(t *testing.T) {
	// The cases of split_shebang in upstream's src/shebang.rs.
	for _, tc := range []struct {
		line, interpreter, argument string
		hasArgument, ok             bool
	}{
		{"#!    ", "", "", false, false},
		{"#!", "", "", false, false},
		{"#!/bin/bash", "/bin/bash", "", false, true},
		{"#!/bin/bash    ", "/bin/bash", "", false, true},
		{"#!/usr/bin/env python", "/usr/bin/env", "python", true, true},
		{"#!/usr/bin/env python   ", "/usr/bin/env", "python", true, true},
		{"#!/usr/bin/env python -x", "/usr/bin/env", "python -x", true, true},
		{"#!/usr/bin/env python   -x", "/usr/bin/env", "python   -x", true, true},
		{"#!/usr/bin/env python \t-x\t", "/usr/bin/env", "python \t-x", true, true},
		{"#/usr/bin/env python \t-x\t", "", "", false, false},
		{"#!  /bin/bash", "/bin/bash", "", false, true},
		{"#!\t\t/bin/bash    ", "/bin/bash", "", false, true},
		{"#!  \t\t/usr/bin/env python", "/usr/bin/env", "python", true, true},
		{"#!  /usr/bin/env python \t-x\t", "/usr/bin/env", "python \t-x", true, true},
		{"#  /usr/bin/env python \t-x\t", "", "", false, false},
		// Only the first line counts, as a line of text from an interpolation can have many.
		{"#!/bin/sh\necho", "/bin/sh", "", false, true},
		{"#!/bin/sh -e\r\necho", "/bin/sh", "-e", true, true},
		{"#!\n/bin/sh", "", "", false, false},
	} {
		got, ok := parseShebang(tc.line)
		want := shebang{interpreter: tc.interpreter, argument: tc.argument, hasArgument: tc.hasArgument}
		if ok != tc.ok || (ok && got != want) {
			t.Errorf("parseShebang(%q) = %+v, %v; want %+v, %v", tc.line, got, ok, want, tc.ok)
		}
	}
}

func TestShebangText(t *testing.T) {
	if got := (shebang{interpreter: "/bin/sh"}).text(); got != "#!/bin/sh" {
		t.Errorf("text() = %q", got)
	}
	if got := (shebang{interpreter: "/usr/bin/env", argument: "python -x", hasArgument: true}).text(); got != "#!/usr/bin/env python -x" {
		t.Errorf("text() = %q", got)
	}
}

func TestProgramName(t *testing.T) {
	for path, want := range map[string]string{"/foo/bar/baz": "baz", `\foo\bar\baz`: "baz", "baz": "baz", `C:\x/y.exe`: "y.exe", "": ""} {
		if got := programName(path); got != want {
			t.Errorf("programName(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestShellKindFileName(t *testing.T) {
	// The cases of shebang_script_filename in upstream's src/executor.rs.
	for _, tc := range []struct{ program, extension, want string }{
		{"bar", ".sh", "foo.sh"},
		{"pwsh.exe", ".sh", "foo.sh"},
		{"cmd.exe", ".sh", "foo.sh"},
		{"powershell", "", "foo.ps1"},
		{"pwsh", "", "foo.ps1"},
		{"powershell.exe", "", "foo.ps1"},
		{"pwsh.exe", "", "foo.ps1"},
		{"cmd", "", "foo.bat"},
		{"cmd.exe", "", "foo.bat"},
		{"bar", "", "foo"},
		{"/usr/bin/pwsh", "", "foo.ps1"},
	} {
		for _, e := range []executor{
			{command: []string{tc.program}},
			{shebang: &shebang{interpreter: tc.program}},
		} {
			var ext *string
			if tc.extension != "" {
				ext = &tc.extension
			}
			if got := e.filename("foo", ext); got != tc.want {
				t.Errorf("%+v.filename(foo, %q) = %q, want %q", e, tc.extension, got, tc.want)
			}
		}
	}
}

func TestExecutorNeedsBOM(t *testing.T) {
	// The cases of needs_bom in upstream's src/executor.rs.
	for _, tc := range []struct {
		program string
		windows bool
	}{
		{"powershell", true}, {"powershell.exe", true}, {"pwsh", true}, {"pwsh.exe", true},
		{"cmd", false}, {"cmd.exe", false}, {"bash", false}, {"/bin/sh", false}, {"python", false},
	} {
		for _, goos := range []string{"linux", "windows"} {
			e := executor{command: []string{tc.program}}
			if got, want := e.needsBOM(goos), tc.windows && goos == "windows"; got != want {
				t.Errorf("%s.needsBOM(%s) = %v, want %v", tc.program, goos, got, want)
			}
		}
	}
}

func TestExecutorIncludesShebangLine(t *testing.T) {
	// The cases of the tests of include_shebang_line in upstream's src/shebang.rs.
	for _, tc := range []struct {
		interpreter, goos string
		want              bool
	}{
		{"cmd", "linux", false}, {"cmd.exe", "linux", false}, {"foo", "linux", true}, {"foo", "windows", false},
	} {
		e := executor{shebang: &shebang{interpreter: tc.interpreter}}
		if got := e.includesShebangLine(tc.goos); got != tc.want {
			t.Errorf("%s on %s: includesShebangLine() = %v, want %v", tc.interpreter, tc.goos, got, tc.want)
		}
	}
}

func TestFailureMessagesWithoutALine(t *testing.T) {
	for _, tc := range []struct{ got, want string }{
		{failureMessage("r", 0, 3), "error: recipe `r` failed with exit code 3\n"},
		{failureMessage("r", 2, 3), "error: recipe `r` failed on line 2 with exit code 3\n"},
		{failureMessage("r", 0, -1), "error: recipe `r` failed for an unknown reason\n"},
		{failureMessage("r", 2, -1), "error: recipe `r` failed on line 2 for an unknown reason\n"},
		{terminatedMessage("r", 0, 15), "error: recipe `r` was terminated by signal 15\n"},
		{terminatedMessage("r", 2, 15), "error: recipe `r` was terminated on line 2 by signal 15\n"},
	} {
		if tc.got != tc.want {
			t.Errorf("got %q, want %q", tc.got, tc.want)
		}
	}
}

// The script is written and run for real, by the shell of the machine, to see
// that the pieces fit: the file is executable and its line numbers are right.
func TestRunScriptEndToEnd(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		args      []string
		want      string
	}{
		{"shebang::simple#1", "foo:\n  #!/bin/sh\n  echo bar\n", nil, "bar\n"},
		{"script::runs_with_command#1", "[script('cat')]\nfoo:\n  FOO\n", nil, "\n\nFOO\n"},
		{"script::shebang_line_numbers#1", "foo:\n  #!/usr/bin/env cat\n\n  a\n\n  b\n\n\n  c\n\n", nil,
			"#!/usr/bin/env cat\n\n\na\n\nb\n\n\nc\n"},
		{"attributes::extension#1", "[extension: '.txt']\nbaz:\n  #!/bin/sh\n  echo $0\n", nil, "baz.txt\n"},
		{"positional_arguments::variadic_shebang#1", "set positional-arguments\n\nfoo bar:\n  #!/bin/sh\n  echo $1\n", []string{"hello"},
			"hello\n"},
		{"shebang::run_shebang#1", "a:\n  #!/usr/bin/env sh\n  code=200\n  x() { return $code; }\n  x\n  x\n", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			jf, err := parser.Parse("justfile", []byte(tc.src))
			if err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			plan := Plan{
				Justfile: jf, WorkDir: t.TempDir(), Stdout: &stdout, Stderr: &stderr, Exec: sys.OSProcess{},
				Env: []string{"PATH=/usr/bin:/bin"}, Args: tc.args,
			}
			res := mustRun(t, plan)
			got := stdout.String()
			if tc.name == "attributes::extension#1" {
				got = filepath.Base(strings.TrimSpace(got)) + "\n"
			}
			if got != tc.want {
				t.Errorf("stdout = %q, want %q (stderr %q, exit %d)", got, tc.want, stderr.String(), res.ExitCode)
			}
		})
	}
}

func TestRunScriptEndToEndExitStatus(t *testing.T) {
	jf, err := parser.Parse("justfile", []byte("a:\n  #!/usr/bin/env sh\n  code=200\n  x() { return $code; }\n  x\n  x\n"))
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	res := mustRun(t, Plan{Justfile: jf, WorkDir: t.TempDir(), Stderr: &stderr, Exec: sys.OSProcess{}, Env: []string{"PATH=/usr/bin:/bin"}})
	if res.ExitCode != 200 || stderr.String() != "error: recipe `a` failed with exit code 200\n" {
		t.Errorf("exit %d, stderr %q; want 200 and the failure of the recipe", res.ExitCode, stderr.String())
	}
}

func BenchmarkRunScript(b *testing.B) {
	plan, _, _, _ := scriptPlan(b, "foo x='1':\n  #!/bin/sh\n  echo {{x}}\n\n  echo done\n")
	plan.Stdout, plan.Stderr, plan.FS = nil, nil, sysfake.FakeFS{}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Run(context.Background(), plan); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkScriptText(b *testing.B) {
	jf, err := parser.Parse("justfile", []byte("foo:\n  #!/bin/sh\n  echo a\n\n  echo b\n\n\n  echo c\n"))
	if err != nil {
		b.Fatal(err)
	}
	r := &jf.Recipes[0]
	lines := []string{"#!/bin/sh", "echo a", "", "echo b", "", "", "echo c"}
	e := executor{shebang: &shebang{interpreter: "/bin/sh"}}
	b.ReportAllocs()
	for b.Loop() {
		scriptText(r.Raw, lines, e, "linux")
	}
}
