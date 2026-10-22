package runner

import (
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/sys"
	"gitea.com/wimwian/just-go/internal/sys/sysfake"
)

// The cases below are the corpus's default group.
func TestRunDefaultAttributeSelectsTheDefaultRecipe(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		want      []string
	}{
		{"default::default_attribute_overrides_first_recipe#1", "foo:\n  @echo FOO\n\n[default]\nbar:\n  @echo BAR\n", []string{"echo BAR"}},
		{"the first recipe without one", "foo:\n  @echo FOO\n\nbar:\n  @echo BAR\n", []string{"echo FOO"}},
		{"with other attributes", "foo:\n  @echo FOO\n\n[private, default]\nbar:\n  @echo BAR\n", []string{"echo BAR"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &sysfake.FakeProcess{}
			plan, _, _ := newPlan(t, tc.src, fake)
			mustRun(t, plan)
			if got := commands(fake); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("commands = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRunNamedRecipeOverridesTheDefaultAttribute(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	plan, _, _ := newPlan(t, "[default]\nfoo:\n  echo FOO\nbar:\n  echo BAR\n", fake)
	plan.Recipes = []string{"bar"}
	mustRun(t, plan)
	if got := commands(fake); !reflect.DeepEqual(got, []string{"echo BAR"}) {
		t.Errorf("commands = %q", got)
	}
}

// confirmRun runs src with stdin, and returns what ran, what was written to
// stderr, the error, and what is left of stdin.
func confirmRun(t *testing.T, src, stdin string, calls ...Call) (cmds []string, stderr string, err error, rest string) {
	t.Helper()
	fake := &sysfake.FakeProcess{}
	plan, _, errBuf := newPlan(t, src, fake)
	in := strings.NewReader(stdin)
	plan.Stdin, plan.Calls = in, calls
	_, err = Run(t.Context(), plan)
	return commands(fake), errBuf.String(), err, stdin[len(stdin)-in.Len():]
}

// The cases below are the corpus's confirm group. stderr is what the run
// writes to standard error; refused is the message of the error of a recipe
// that was not confirmed, which the command line prints after it.
func TestRunConfirm(t *testing.T) {
	refused := func(recipe string) string { return "recipe `" + recipe + "` was not confirmed" }
	for _, tc := range []struct {
		name, src, stdin string
		calls            []Call
		cmds             []string
		stderr, refused  string
	}{
		{"confirm::confirm_recipe#1", "[confirm]\n@foo:\n  echo FOO\n", "y\n", nil, []string{"echo FOO"}, "Run recipe `foo`? ", ""},
		{"confirm::confirm_recipe_with_prompt#1", "[confirm('Sure?')]\n@foo:\n    echo FOO\n", "y\n", nil,
			[]string{"echo FOO"}, "Sure? ", ""},
		{"confirm::confirm_with_expression#1",
			"target := 'production'\n\n[confirm('Deploy to ' + target + '?')]\ndeploy:\n    echo deployed\n", "y\n", nil,
			[]string{"echo deployed"}, "Deploy to production? echo deployed\n", ""},
		{"confirm::confirm_with_recipe_parameter#1",
			"[confirm('Deploy to ' + target + '?')]\ndeploy target:\n    echo 'deployed to {{target}}'\n", "y\n",
			[]Call{{Recipe: "deploy", Args: []string{"staging"}}}, []string{"echo 'deployed to staging'"},
			"Deploy to staging? echo 'deployed to staging'\n", ""},
		{"confirm::do_not_confirm_recipe#1", "[confirm]\n@foo:\n  echo FOO\n", "", nil, nil, "Run recipe `foo`? ", refused("foo")},
		{"confirm::recipe_with_confirm_recipe_dependency#1", "@bar: foo\n  echo BAR\n\n[confirm]\n@foo:\n  echo FOO\n", "y\n", nil,
			[]string{"echo FOO", "echo BAR"}, "Run recipe `foo`? ", ""},
		{"confirm::do_not_confirm_recipe_with_confirm_recipe_dependency#1", "bar: foo\n  echo BAR\n\n[confirm]\n@foo:\n  echo FOO\n",
			"", nil, nil, "Run recipe `foo`? ", refused("foo")},
		// The prompt for a recipe comes before its dependencies run.
		{"the prompt precedes the dependencies", "[confirm]\n@bar: foo\n  echo BAR\n\n@foo:\n  echo FOO\n", "n\n", nil, nil,
			"Run recipe `bar`? ", refused("bar")},
		{"the prompt precedes the dependencies, confirmed", "[confirm]\n@bar: foo\n  echo BAR\n\n@foo:\n  echo FOO\n", "yes\n", nil,
			[]string{"echo FOO", "echo BAR"}, "Run recipe `bar`? ", ""},
		{"a recipe named twice is asked once", "@bar: foo foo\n\n[confirm]\n@foo:\n  echo FOO\n", "y\n", nil,
			[]string{"echo FOO"}, "Run recipe `foo`? ", ""},
		{"each recipe is asked", "[confirm]\n@bar: foo\n  echo BAR\n\n[confirm('Foo?')]\n@foo:\n  echo FOO\n", "y\ny\n", nil,
			[]string{"echo FOO", "echo BAR"}, "Run recipe `bar`? Foo? ", ""},
		{"a refusal stops what comes after", "@bar: foo\n  echo BAR\n\n[confirm]\n@foo:\n  echo FOO\n", "no\n", nil, nil,
			"Run recipe `foo`? ", refused("foo")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmds, stderr, err, _ := confirmRun(t, tc.src, tc.stdin, tc.calls...)
			if !reflect.DeepEqual(cmds, tc.cmds) || stderr != tc.stderr {
				t.Errorf("commands %q, stderr %q; want %q, %q", cmds, stderr, tc.cmds, tc.stderr)
			}
			wantRefusal(t, err, tc.refused)
		})
	}
}

// wantRefusal checks that err is a runtime error with message refused, or nil
// when refused is empty.
func wantRefusal(t *testing.T, err error, refused string) {
	t.Helper()
	var de *diag.Error
	switch {
	case refused == "" && err != nil:
		t.Errorf("err = %v", err)
	case refused != "" && (!errors.As(err, &de) || de.Kind != diag.KindRuntime || de.Msg != refused):
		t.Errorf("err = %v, want a runtime error %q", err, refused)
	}
}

func TestRunConfirmAnswers(t *testing.T) {
	for stdin, yes := range map[string]bool{
		"y\n": true, "Y\n": true, "yes\n": true, "YeS\n": true, " yes \n": true, "\ty\r\n": true, "y": true, "yes\nno\n": true,
		"n\n": false, "no\n": false, "\n": false, "": false, "yess\n": false, "ye\n": false, "yes please\n": false, "\ny\n": false,
	} {
		cmds, _, err, _ := confirmRun(t, "[confirm]\nfoo:\n  echo FOO\n", stdin)
		if ran := len(cmds) == 1; ran != yes || (err == nil) != yes {
			t.Errorf("stdin %q: ran %v, err %v; want confirmed = %v", stdin, cmds, err, yes)
		}
	}
}

// A recipe's own commands, which may read standard input, get what follows
// the line of the answer.
func TestRunConfirmReadsOnlyTheAnswer(t *testing.T) {
	_, _, err, rest := confirmRun(t, "[confirm]\nfoo:\n  cat\n", "y\nthe input of cat\n")
	if err != nil || rest != "the input of cat\n" {
		t.Errorf("err = %v, rest of stdin = %q", err, rest)
	}
}

func TestRunConfirmYesAsksNothing(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	plan, _, stderr := newPlan(t, "[confirm]\n@foo:\n  echo FOO\n", fake)
	plan.Yes = true
	mustRun(t, plan)
	if stderr.Len() != 0 || len(fake.Specs) != 1 {
		t.Errorf("stderr %q, ran %d; want nothing written and one command", stderr, len(fake.Specs))
	}
}

func TestRunConfirmWithoutStdinRefuses(t *testing.T) {
	plan, _, _ := newPlan(t, "[confirm]\nfoo:\n  echo FOO\n", &sysfake.FakeProcess{})
	plan.Stdin = nil
	if _, err := Run(t.Context(), plan); err == nil {
		t.Error("Run succeeded without an answer")
	}
}

// A reader that fails refuses the recipe, as an end of input does.
type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("broken") }

func TestRunConfirmReadFailureRefuses(t *testing.T) {
	plan, _, _ := newPlan(t, "[confirm]\nfoo:\n  echo FOO\n", &sysfake.FakeProcess{})
	plan.Stdin = failingReader{}
	if _, err := Run(t.Context(), plan); err == nil {
		t.Error("Run succeeded after a failed read")
	}
}

func TestAnswerIsYes(t *testing.T) {
	for line, want := range map[string]bool{"y": true, " Y ": true, "yes": true, "YES\n": true, "": false, "n": false, "yesn": false} {
		if got := isYes(line); got != want {
			t.Errorf("isYes(%q) = %v, want %v", line, got, want)
		}
	}
}

func TestReadLineStopsAtTheLineBreak(t *testing.T) {
	r := strings.NewReader("one\ntwo")
	for _, want := range []string{"one\n", "two", ""} {
		if got, err := readLine(r); got != want || err != nil {
			t.Errorf("readLine = %q, %v; want %q", got, err, want)
		}
	}
	if got, err := readLine(nil); got != "" || err != nil {
		t.Errorf("readLine(nil) = %q, %v", got, err)
	}
}

// emptyReader returns (0, nil) for empties reads, then reads from rest.
type emptyReader struct {
	empties int
	rest    io.Reader
	reads   int
}

func (e *emptyReader) Read(p []byte) (int, error) {
	e.reads++
	if e.empties > 0 {
		e.empties--
		return 0, nil
	}
	return e.rest.Read(p)
}

func TestReadLineGivesUpOnAReaderThatMakesNoProgress(t *testing.T) {
	r := &emptyReader{empties: 1 << 30, rest: strings.NewReader("never")}
	got, err := readLine(r)
	if got != "" || !errors.Is(err, io.ErrNoProgress) {
		t.Errorf("readLine = %q, %v; want \"\", io.ErrNoProgress", got, err)
	}
	if r.reads != maxEmptyReads {
		t.Errorf("reads = %d, want %d", r.reads, maxEmptyReads)
	}
}

func TestReadLineToleratesFewEmptyReads(t *testing.T) {
	r := &emptyReader{empties: maxEmptyReads - 1, rest: strings.NewReader("ok\n")}
	if got, err := readLine(r); got != "ok\n" || err != nil {
		t.Errorf("readLine = %q, %v; want \"ok\\n\", nil", got, err)
	}
}

func TestRunConfirmIsNotGivenByAReaderThatMakesNoProgress(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	plan, _, _ := newPlan(t, "[confirm]\nfoo:\n  echo\n", fake)
	plan.Stdin = &emptyReader{empties: 1 << 30, rest: strings.NewReader("y\n")}
	if _, err := Run(t.Context(), plan); err == nil || !strings.Contains(err.Error(), "was not confirmed") {
		t.Errorf("Run error = %v, want not confirmed", err)
	}
}

func TestRunReportsAFailingConfirmExpression(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	plan, _, _ := newPlan(t, "[confirm(x)]\nfoo:\n", fake)
	_, err := Run(t.Context(), plan)
	var de *diag.Error
	if !errors.As(err, &de) || de.Msg != "variable `x` not defined" || de.Line != 1 || de.File != "justfile" {
		t.Errorf("err = %+v", err)
	}
	if len(fake.Specs) != 0 {
		t.Errorf("%d commands ran, want none", len(fake.Specs))
	}
}

// Upstream asks nothing under --yes, and so does not evaluate the prompt.
func TestRunConfirmYesDoesNotEvaluateThePrompt(t *testing.T) {
	plan, _, _ := newPlan(t, "[confirm(x)]\nfoo:\n", &sysfake.FakeProcess{})
	plan.Yes = true
	mustRun(t, plan)
}

// The cases below are the corpus's no_exit_message group.
func TestRunExitMessageAttributes(t *testing.T) {
	failure := "error: recipe `hello` failed on line 4 with exit code 100\n"
	for _, tc := range []struct {
		name, src string
		stderr    string
	}{
		{"no_exit_message::no_exit_message#1", "[no-exit-message]\n@hello:\n  echo 'Hello, World!'\n  exit 100\n", ""},
		{"no_exit_message::exit_message#1", "[exit-message]\n@hello:\n  echo 'Hello, World!'\n  exit 100\n", failure},
		{"no_exit_message::exit_message_override_no_exit_setting#1",
			"set no-exit-message\n\n[exit-message]\n@hello:\n  echo 'Hello, World!'\n  exit 100\n",
			"error: recipe `hello` failed on line 6 with exit code 100\n"},
		{"neither", "@hello:\n  echo 'Hello, World!'\n  exit 100\n", "error: recipe `hello` failed on line 3 with exit code 100\n"},
		{"the setting alone", "set no-exit-message\n@hello:\n  echo 'Hello, World!'\n  exit 100\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &sysfake.FakeProcess{Codes: []int{0, 100}}
			plan, _, stderr := newPlan(t, tc.src, fake)
			if res := mustRun(t, plan); res.ExitCode != 100 || stderr.String() != tc.stderr {
				t.Errorf("exit %d, stderr %q; want 100, %q", res.ExitCode, stderr, tc.stderr)
			}
		})
	}
}

func TestRunNoExitMessageAttributeSilencesTheSignalMessage(t *testing.T) {
	for src, want := range map[string]string{
		"[no-exit-message]\ndefault:\n  @kill -TERM $$\n": "",
		"default:\n  @kill -TERM $$\n":                    "error: recipe `default` was terminated on line 2 by signal 15\n",
	} {
		fake := &sysfake.FakeProcess{Codes: []int{-1}, Errs: []error{&sys.SignalError{Signal: 15}}}
		plan, _, stderr := newPlan(t, src, fake)
		if res := mustRun(t, plan); res.ExitCode != 143 || stderr.String() != want {
			t.Errorf("%q: exit %d, stderr %q; want 143, %q", src, res.ExitCode, stderr, want)
		}
	}
}

// The cases below are the corpus's quiet group.
func TestRunNoQuietAttribute(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		quiet     bool
		stderr    string
	}{
		{"quiet::quiet_setting_with_no_quiet_attribute#1", "set quiet\n\n[no-quiet]\nfoo:\n  echo FOO\n", false, "echo FOO\n"},
		{"quiet::quiet_setting_with_no_quiet_attribute_and_quiet_line#1", "set quiet\n\n[no-quiet]\nfoo:\n  @echo FOO\n", false, ""},
		{"quiet::quiet_setting_with_no_quiet_attribute_and_quiet_recipe#1", "set quiet\n\n[no-quiet]\n@foo:\n  echo FOO\n", false, ""},
		{"the setting without the attribute", "set quiet\nfoo:\n  echo FOO\n", false, ""},
		{"the attribute without the setting", "[no-quiet]\nfoo:\n  echo FOO\n", false, "echo FOO\n"},
		{"the quiet flag wins", "set quiet\n[no-quiet]\nfoo:\n  echo FOO\n", true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, _, stderr := newPlan(t, tc.src, &sysfake.FakeProcess{})
			plan.Quiet = tc.quiet
			mustRun(t, plan)
			if stderr.String() != tc.stderr {
				t.Errorf("stderr = %q, want %q", stderr, tc.stderr)
			}
		})
	}
}

// The cases below are the corpus's positional_arguments group.
func TestRunPositionalArgumentsAttribute(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		args      []string
		want      []string
	}{
		{"positional_arguments::shell_with_attribute#1", "[positional-arguments]\nfoo bar baz:\n  echo $1\n",
			[]string{"hello", "goodbye"}, []string{"sh", "-cu", "echo $1", "foo", "hello", "goodbye"}},
		{"only the recipe with the attribute", "[positional-arguments]\nfoo a:\n  echo $1\nbar a:\n  echo $1\n",
			[]string{"x"}, []string{"sh", "-cu", "echo $1", "foo", "x"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &sysfake.FakeProcess{}
			plan, _, _ := newPlan(t, tc.src, fake)
			plan.Recipes, plan.Args = []string{"foo"}, tc.args
			mustRun(t, plan)
			if got := fake.Specs[0].Argv; !reflect.DeepEqual(got, tc.want) {
				t.Errorf("argv = %q, want %q", got, tc.want)
			}
		})
	}
	fake := &sysfake.FakeProcess{}
	plan, _, _ := newPlan(t, "[positional-arguments]\nfoo a:\n  echo $1\nbar a:\n  echo $1\n", fake)
	plan.Calls = []Call{{Recipe: "bar", Args: []string{"y"}}}
	mustRun(t, plan)
	if got, want := fake.Specs[0].Argv, []string{"sh", "-cu", "echo $1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("a recipe without the attribute got argv %q, want %q", got, want)
	}
}

// The cases below are the corpus's working_directory and no_cd groups.
func TestRunWorkingDirectoryAttributes(t *testing.T) {
	base := t.TempDir()
	invocation := filepath.Join(base, "inv")
	abs := filepath.Join(base, "elsewhere")
	for _, tc := range []struct {
		name, src string
		args      []string
		want      string
	}{
		{"working_directory::attribute#1", "[working-directory('foo')]\n@qux:\n  echo baz > bar\n", nil, filepath.Join(base, "foo")},
		{"working_directory::attribute_with_expression#1",
			"dir := 'foo'\n\n[working-directory(dir + '-bar')]\n@baz:\n  echo bob > fred\n", nil, filepath.Join(base, "foo-bar")},
		{"working_directory::attribute_with_recipe_parameter#1", "[working-directory(target)]\n@baz target:\n  echo bob > fred\n",
			[]string{"foo"}, filepath.Join(base, "foo")},
		{"working_directory::setting_and_attribute#1",
			"set working-directory := 'foo'\n\n[working-directory('bar')]\n@baz:\n  echo bob > fred\n", nil, filepath.Join(base, "foo", "bar")},
		{"no_cd::working_directory_attribute_overrides_setting#1",
			"set no-cd := true\n\n[working-directory('workspace')]\nfoo:\n  cat data.txt\n", nil, filepath.Join(base, "workspace")},
		{"an absolute path replaces the directory", "[working-directory('" + abs + "')]\nfoo:\n  echo\n", nil, abs},
		{"no_cd::shell#1", "[no-cd]\nfoo:\n  cat bar\n", nil, invocation},
		{"working_directory::no_cd_overrides_setting#1", "set working-directory := 'bar'\n\n[no-cd]\nfoo:\n  cat bar\n", nil, invocation},
		{"no-cd with the setting off", "set no-cd := false\n[no-cd]\nfoo:\n  echo\n", nil, invocation},
		{"neither", "foo:\n  echo\n", nil, base},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &sysfake.FakeProcess{}
			plan, _, _ := newPlan(t, tc.src, fake)
			plan.WorkDir, plan.InvocationDir = base, invocation
			plan.Calls = []Call{{Recipe: firstRecipe(plan), Args: tc.args}}
			mustRun(t, plan)
			if got := fake.Specs[0].Dir; got != tc.want {
				t.Errorf("Dir = %q, want %q", got, tc.want)
			}
		})
	}
}

// firstRecipe is the name of the first recipe of the plan's justfile.
func firstRecipe(plan Plan) string { return plan.Justfile.Recipes[0].Name }

// Each recipe has its own directory.
func TestRunWorkingDirectoryAttributeIsPerRecipe(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	plan, _, _ := newPlan(t, "a: b\n  echo a\n[working-directory('x')]\nb:\n  echo b\n", fake)
	plan.WorkDir = "/w"
	mustRun(t, plan)
	if got := []string{fake.Specs[0].Dir, fake.Specs[1].Dir}; !reflect.DeepEqual(got, []string{filepath.Join("/w", "x"), "/w"}) {
		t.Errorf("dirs = %q", got)
	}
}

func TestRunWorkingDirectoryAttributeEvaluationFailureStopsTheRecipe(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	plan, _, _ := newPlan(t, "[working-directory(x)]\nfoo:\n  echo\n", fake)
	_, err := Run(t.Context(), plan)
	var de *diag.Error
	if !errors.As(err, &de) || de.Msg != "variable `x` not defined" || de.File != "justfile" || de.Line != 1 || len(fake.Specs) != 0 {
		t.Errorf("err = %v, ran %d", err, len(fake.Specs))
	}
}

func TestAttributePredicatesArePure(t *testing.T) {
	has := func(names ...string) *ast.Recipe {
		r := &ast.Recipe{Name: "r"}
		for _, n := range names {
			r.Attributes = append(r.Attributes, ast.Attribute{Name: n})
		}
		return r
	}
	for _, tc := range []struct {
		name                  string
		set                   ast.Settings
		r                     *ast.Recipe
		exit, quiet, position bool
	}{
		{"plain", ast.Settings{}, has(), true, false, false},
		{"settings", ast.Settings{NoExitMessage: true, Quiet: true, PositionalArguments: true}, has(), false, true, true},
		{"attributes", ast.Settings{}, has("no-exit-message", "positional-arguments"), false, false, true},
		{"overrides", ast.Settings{NoExitMessage: true, Quiet: true}, has("exit-message", "no-quiet"), true, false, false},
	} {
		if got := printsExitMessage(tc.set, tc.r); got != tc.exit {
			t.Errorf("%s: printsExitMessage = %v", tc.name, got)
		}
		if got := settingQuiet(tc.set, tc.r); got != tc.quiet {
			t.Errorf("%s: settingQuiet = %v", tc.name, got)
		}
		if got := takesPositional(tc.set, tc.r); got != tc.position {
			t.Errorf("%s: takesPositional = %v", tc.name, got)
		}
	}
}

func BenchmarkReadLine(b *testing.B) {
	answer := "yes, please do it\nand then some more input\n"
	b.ReportAllocs()
	for b.Loop() {
		readLine(strings.NewReader(answer))
	}
}

func BenchmarkConfirmRun(b *testing.B) {
	src := "[confirm('Go?')]\n@a: b\n  echo a\n[confirm]\n@b:\n  echo b\n"
	plan, _, _ := newPlan(b, src, &sysfake.FakeProcess{})
	b.ReportAllocs()
	for b.Loop() {
		plan.Stdin = strings.NewReader("y\ny\n")
		if _, err := Run(b.Context(), plan); err != nil {
			b.Fatal(err)
		}
	}
}

// Prompts go to Prompt when it is set, and to Stderr when it is not.
func TestRunConfirmWritesThePromptToPrompt(t *testing.T) {
	plan, _, stderr := newPlan(t, "[confirm]\n@foo:\n  echo FOO\n", &sysfake.FakeProcess{})
	var prompt strings.Builder
	plan.Prompt, plan.Stdin = &prompt, strings.NewReader("y\n")
	mustRun(t, plan)
	if prompt.String() != "Run recipe `foo`? " || stderr.Len() != 0 {
		t.Errorf("prompt %q, stderr %q", prompt.String(), stderr)
	}
}

func TestRunDefaultRecipeWithArgumentsGivesThemToIt(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	plan, _, _ := newPlan(t, "[default]\nr a:\n  echo {{a}}\n", fake)
	plan.Args = []string{"x"}
	mustRun(t, plan)
	if got := commands(fake); !reflect.DeepEqual(got, []string{"echo x"}) {
		t.Errorf("commands = %q", got)
	}
}
