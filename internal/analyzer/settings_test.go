package analyzer_test

import (
	"strings"
	"testing"

	"gitea.com/wimwian/just-go/internal/analyzer"
)

// Each row is a case of the corpus, or the same rule at another setting, with
// the message, line, column and width the oracle reports.
func TestAnalyzeSettingErrorsMatchUpstream(t *testing.T) {
	for _, tc := range []struct {
		name, src        string
		line, col, width int
		msg              string
	}{
		{"settings::no_cd_setting_conflicts_with_working_directory_setting#1",
			"set no-cd := true\nset working-directory := 'bar'\n", 2, 5, 17,
			"`no-cd` set on line 1 is incompatible with `working-directory`"},
		{"working_directory::working_directory_setting_conflicts_with_no_cd_setting#1",
			"set working-directory := 'bar'\nset no-cd := true\n", 2, 5, 5,
			"`working-directory` set on line 1 is incompatible with `no-cd`"},
		{"a setting set twice", "set quiet\n\nset quiet := false\n", 3, 5, 5,
			"setting `quiet` first set on line 1 is redefined on line 3"},
		{"a setting set twice with another between", "set lazy\nset export\nset lazy\n", 3, 5, 4,
			"setting `lazy` first set on line 1 is redefined on line 3"},
		{"settings::undefined_variable_in_tempdir#1", "set tempdir := foo\n", 1, 16, 3,
			"variable `foo` not defined"},
		{"settings::undefined_variable_in_shell_argument#1", "set shell := ['foo', bar]\n", 1, 22, 3,
			"variable `bar` not defined"},
		{"function_definitions::undefined_in_setting", "set tempdir := foo()\n", 1, 16, 3,
			"call to undefined function `foo`"},
		{"a function given the wrong number of arguments", "set working-directory := uppercase()\n", 1, 26, 9,
			"function `uppercase` called with 0 arguments but takes 1"},
		{"settings::function_call#1", "set working-directory := arch()\n", 1, 26, 4,
			"cannot call functions in const context"},
		{"settings::non_const_variable", "foo := arch()\n\nset working-directory := foo\n", 3, 26, 3,
			"cannot access non-const variable `foo` in const context"},
		{"settings::backtick#1", "set working-directory := `pwd`\n", 1, 26, 5, "cannot call backticks in const context"},
		{"settings::non_const_variable with a backtick", "foo := `pwd`\n\nset working-directory := foo\n", 3, 26, 3,
			"cannot access non-const variable `foo` in const context"},
		{"dotenv::command_conflicts_with_dotenv_path_setting#1",
			"set dotenv-command := 'true'\nset dotenv-path := 'foo'\n\nfoo:\n", 2, 5, 11,
			"`dotenv-command` set on line 1 is incompatible with `dotenv-path`"},
		{"dotenv::command_conflicts_with_dotenv_required_setting#1",
			"set dotenv-required\nset dotenv-command := 'true'\n\nfoo:\n", 2, 5, 14,
			"`dotenv-required` set on line 1 is incompatible with `dotenv-command`"},
		{"dotenv-load on, then dotenv-command", "set dotenv-load := true\nset dotenv-command := 'x'\n", 2, 5, 14,
			"`dotenv-load` set on line 1 is incompatible with `dotenv-command`"},
		{"dotenv-command, then dotenv-load", "set dotenv-command := 'x'\nset dotenv-load\n", 2, 5, 11,
			"`dotenv-command` set on line 1 is incompatible with `dotenv-load`"},
		{"dotenv-command, then dotenv-filename", "set dotenv-command := 'x'\nset dotenv-filename := 'y'\n", 2, 5, 15,
			"`dotenv-command` set on line 1 is incompatible with `dotenv-filename`"},
		{"dotenv-filename, then dotenv-command", "set dotenv-filename := 'x'\nset dotenv-command := 'y'\n", 2, 5, 14,
			"`dotenv-filename` set on line 1 is incompatible with `dotenv-command`"},
		{"dotenv-path, then dotenv-command", "set dotenv-path := 'x'\nset dotenv-command := 'y'\n", 2, 5, 14,
			"`dotenv-path` set on line 1 is incompatible with `dotenv-command`"},
		{"dotenv-required twice", "set dotenv-required\nset dotenv-required := false\n", 2, 5, 15,
			"setting `dotenv-required` first set on line 1 is redefined on line 2"},
		{"settings::undefined_variable_in_dotenv_command#1", "set dotenv-command := foo\n", 1, 23, 3,
			"variable `foo` not defined"},
		{"settings::undefined_variable_in_dotenv_filename#1", "set dotenv-filename := foo\n", 1, 24, 3,
			"variable `foo` not defined"},
		{"settings::undefined_variable_in_dotenv_path#1", "set dotenv-path := foo\n", 1, 20, 3,
			"variable `foo` not defined"},
		{"a function in a dotenv setting", "set dotenv-path := arch()\n", 1, 20, 4,
			"cannot call functions in const context"},
		{"guards::guard_sigil_may_not_be_used_with_infallible_sigil#1",
			"set guards\n\n@foo:\n  -?exit 2\n", 4, 3, 8,
			"the guard `?` and infallible `-` sigils may not be used together"},
		{"guards::sigils_are_recognized_after_ignored_comments#1",
			"set guards\nset ignore-comments\n\nfoo:\n  # comment \\\n  -?bar\n", 6, 3, 5,
			"the guard `?` and infallible `-` sigils may not be used together"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := analyze(t, tc.src)
			if got.Line != tc.line || got.Col != tc.col || got.Width != tc.width || got.Msg != tc.msg {
				t.Errorf("Analyze(%q) = %d:%d width %d %q, want %d:%d width %d %q",
					tc.src, got.Line, got.Col, got.Width, got.Msg, tc.line, tc.col, tc.width, tc.msg)
			}
		})
	}
}

func TestAnalyzeAcceptsSettingsThatDoNotConflict(t *testing.T) {
	for _, src := range []string{
		// working_directory::false_no_cd_does_not_conflict_with_working_directory#1
		"set no-cd := false\nset working-directory := 'foo'\n\nbar:\n  @echo baz\n",
		"set working-directory := 'foo'\nset no-cd := false\n",
		"set quiet\nset export\nset lazy\nset shell := ['sh', '-c']\nset tempdir := '.'\n",
		"dir := 'a'\nset working-directory := dir + '-b'\nset windows-shell := [dir]\nset script-interpreter := [dir, '-x']\n",
		// The conflict is with dotenv-command turned on, not with the name.
		"set dotenv-load := false\nset dotenv-required := false\nset dotenv-command := 'x'\n",
		"set dotenv-command := 'x'\nset dotenv-load := false\nset dotenv-required := false\nset dotenv-override\n",
		"set dotenv-load\nset dotenv-override\nset dotenv-required\nset dotenv-filename := 'x'\nset dotenv-path := 'y'\n",
		// dotenv::command_composes_with_dotenv_override_setting#1
		"set dotenv-override := true\nset dotenv-command := 'echo KEY=command'\n\n@foo:\n  echo $KEY\n",
	} {
		if err := analyzer.Analyze(mustParse(t, src)); err != nil {
			t.Errorf("Analyze(%q) = %v", src, err)
		}
	}
}

// allow_duplicate_recipes::allow_duplicate_recipes#1 and
// allow_duplicate_variables::allow_duplicate_variables#1.
func TestAnalyzeAllowsDuplicatesWhenTheSettingSaysSo(t *testing.T) {
	for _, src := range []string{
		"b:\n  echo foo\nb:\n  echo bar\n\nset allow-duplicate-recipes\n",
		"b a:\n  echo foo\nb c d:\n  echo bar {{c}} {{d}}\n\nset allow-duplicate-recipes\n",
		"a := 'foo'\na := 'bar'\n\nset allow-duplicate-variables\n\nb:\n  echo {{a}}\n",
	} {
		if err := analyzer.Analyze(mustParse(t, src)); err != nil {
			t.Errorf("Analyze(%q) = %v", src, err)
		}
	}
	// With the setting off, and without it, they are errors.
	wantError(t, "b:\n  echo foo\nb:\n  echo bar\nset allow-duplicate-recipes := false\n", 3,
		"recipe `b` first defined on line 1 is redefined on line 3")
	wantError(t, "a := 'foo'\na := 'bar'\nset allow-duplicate-variables := false\n", 2, "variable `a` has multiple definitions")
}

// A recipe the duplicate replaced is not checked, as upstream's is not.
func TestAnalyzeChecksOnlyTheLastOfDuplicateRecipes(t *testing.T) {
	src := "set allow-duplicate-recipes\nb:\n  echo {{nope}}\nb:\n  echo ok\n"
	if err := analyzer.Analyze(mustParse(t, src)); err != nil {
		t.Errorf("Analyze = %v", err)
	}
	wantError(t, "set allow-duplicate-recipes\nb:\n  echo ok\nb:\n  echo {{nope}}\n", 5, "variable `nope` not defined")
}

// ignore_comments::dont_analyze_comments#1 and
// ignore_comments::comments_still_must_be_parsable_when_ignored#1 (the second
// is the parser's).
func TestAnalyzeSkipsCommentsUnderIgnoreComments(t *testing.T) {
	ignored := "set ignore-comments\n\nsome_recipe:\n  # {{ bar }}\n"
	if err := analyzer.Analyze(mustParse(t, ignored)); err != nil {
		t.Errorf("Analyze(%q) = %v", ignored, err)
	}
	wantError(t, "set ignore-comments := false\n\nsome_recipe:\n  # {{ bar }}\n", 4, "variable `bar` not defined")
	wantError(t, "set ignore-comments\n\nsome_recipe:\n  @echo a \\\n  # {{ bar }}\n", 5, "variable `bar` not defined")
	wantError(t, "set ignore-comments\n\nsome_recipe:\n  echo # {{ bar }}\n", 4, "variable `bar` not defined")
}

// A script is evaluated whole, so a comment in it is analyzed even under set
// ignore-comments: ignore_comments::ignore_comments_evaluates_comments_in_scripts#1
// and ..._in_default_script_recipes#1.
func TestAnalyzeChecksCommentsOfScripts(t *testing.T) {
	wantError(t, "set ignore-comments\n\nfoo:\n  #!/bin/sh\n  # {{ undefined }}\n  echo ok\n", 5, "variable `undefined` not defined")
	wantError(t, "set default-script\nset ignore-comments\n\nfoo:\n  # {{ undefined }}\n  echo ok\n", 5, "variable `undefined` not defined")
	wantError(t, "set ignore-comments\n\n[script('sh')]\nfoo:\n  # {{ undefined }}\n", 5, "variable `undefined` not defined")
	ignored := "set ignore-comments\n\n[shell]\nfoo:\n  # {{ undefined }}\n"
	if err := analyzer.Analyze(mustParse(t, ignored)); err != nil {
		t.Errorf("Analyze(%q) = %v", ignored, err)
	}
}

func TestAnalyzeSettingsCheckOrder(t *testing.T) {
	// Settings and recipe parameters are checked in the order of the file,
	// as upstream checks its items, and an undefined variable in a setting
	// before a duplicate recipe.
	wantError(t, "a x x:\nset no-cd\nset working-directory := 'w'\n", 1, "recipe `a` has duplicate parameter `x`")
	wantError(t, "set no-cd\nset working-directory := 'w'\na x x:\n", 2,
		"`no-cd` set on line 1 is incompatible with `working-directory`")
	wantError(t, "set quiet\na x x:\nset quiet\n", 2, "recipe `a` has duplicate parameter `x`")
	wantError(t, "set tempdir := nope\na:\na:\n", 1, "variable `nope` not defined")
	// Settings are checked after the assignments they use.
	wantError(t, "x := y\ny := x\nset tempdir := x\n", 1, "variable `x` depends on its own value: `x -> y -> x`")
}

func TestAnalyzeReportsAnInvalidSettingBeforeUsesOfIt(t *testing.T) {
	if err := analyzer.Analyze(mustParse(t, "set shell := [x]\nx := 'bash'\n")); err != nil {
		t.Errorf("a setting may name a variable defined after it: %v", err)
	}
	err := analyzer.Analyze(mustParse(t, "set shell := [x]\n"))
	if err == nil || !strings.Contains(err.Error(), "variable `x` not defined") {
		t.Errorf("Analyze = %v", err)
	}
}

func BenchmarkAnalyzeSettings(b *testing.B) {
	var sb strings.Builder
	sb.WriteString("dir := 'a'\nset working-directory := dir + '-b'\nset shell := ['bash', '-c']\nset tempdir := dir\nset guards\nset ignore-comments\n")
	for i := 0; i < 200; i++ {
		sb.WriteString("r")
		sb.WriteString(strings.Repeat("x", i%5+1))
		sb.WriteString(strings.Repeat("y", i))
		sb.WriteString(":\n  # {{dir}}\n  ?echo {{dir}}\n")
	}
	jf := mustParse(b, sb.String())
	b.ReportAllocs()
	for b.Loop() {
		if err := analyzer.Analyze(jf); err != nil {
			b.Fatal(err)
		}
	}
}

// A row named by a case id is that case of the corpus; the others are the same
// rule in another place. The message and position are the oracle's. A use of a list feature is an error unless the
// file says `set lists`, and the first one in the file is the one reported.
func TestAnalyzeListFeaturesNeedTheListsSetting(t *testing.T) {
	for _, tc := range []struct {
		name, src        string
		line, col, width int
		msg              string
	}{
		{"booleans::bool_requires_lists_setting#1", "x := bool('true')", 1, 6, 4,
			"the `bool()` function requires `set lists`"},
		{"functions::num_jobs_requires_lists#1", "foo:\n  @echo {{ num_jobs() }}\n", 2, 12, 8,
			"the `num_jobs()` function requires `set lists`"},
		{"functions::show_requires_lists_setting#1", "x := show(\"foo\")", 1, 6, 4,
			"the `show()` function requires `set lists`"},
		{"lists::join_list_requires_lists_setting#1", "x := join_list(\"foo\")", 1, 6, 9,
			"the `join_list()` function requires `set lists`"},
		{"lists::split_requires_lists_setting#1", "x := split(\"foo\", \",\")", 1, 6, 5,
			"the `split()` function requires `set lists`"},
		{"which_function::requires_lists_setting#1", "p := which('hello.exe')", 1, 6, 5,
			"the `which()` function requires `set lists`"},
		{"dotenv::filename_list_requires_lists_setting#1", "set dotenv-filename := ['foo', 'bar']\n\nfoo:\n", 1, 24, 1,
			"list literals require `set lists`"},
		{"list_literals::list_literals_requires_lists_setting#1", "set lists := false\n\nx := []\n\nfoo:\n  @echo hi\n", 3, 6, 1,
			"list literals require `set lists`"},
		{"a list in a shell setting", "set shell := ['bash', ['-c']]\nfoo:\n", 1, 23, 1,
			"list literals require `set lists`"},
		{"a list in a parameter default", "foo x=['a']:\n", 1, 7, 1,
			"list literals require `set lists`"},
		{"a list in a recipe line", "foo:\n  @echo {{ ['a'] }}\n", 2, 12, 1,
			"list literals require `set lists`"},
		{"a call after its arguments", "x := show(bool('t'))\n", 1, 11, 4,
			"the `bool()` function requires `set lists`"},
		{"a list before its elements", "x := ['a', ['b']]\n", 1, 6, 1,
			"list literals require `set lists`"},
		{"the first of two", "x := bool('t') + ['a']\n", 1, 6, 4,
			"the `bool()` function requires `set lists`"},
		{"a list in a setting that is not a string", "set tempdir := ['a']\n", 1, 16, 1,
			"list literals require `set lists`"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := analyze(t, tc.src)
			if got.Line != tc.line || got.Col != tc.col || got.Width != tc.width || got.Msg != tc.msg {
				t.Errorf("Analyze(%q) = %d:%d width %d %q, want %d:%d width %d %q",
					tc.src, got.Line, got.Col, got.Width, got.Msg, tc.line, tc.col, tc.width, tc.msg)
			}
		})
	}
}

func TestAnalyzeAcceptsListFeaturesUnderTheListsSetting(t *testing.T) {
	for _, src := range []string{
		"set lists\nx := ['a', 'b']\nfoo y=['c']:\n  @echo {{ y }} {{ x }}\n",
		"set lists := true\nx := show(bool('t'))\nset shell := ['bash', '-c']\n",
		"set unstable\nset lists\nx := which('a') + join_list('b') + split('c', 'd') + num_jobs()\n",
	} {
		if err := analyzer.Analyze(mustParse(t, src)); err != nil {
			t.Errorf("Analyze(%q) = %v", src, err)
		}
	}
}

// A list in a setting is refused even under `set lists`: its behavior is
// not implemented (R15, unstable). The refusal is the setting's list error,
// after the faults in its elements.
func TestAnalyzeRefusesAListInASettingUnderTheListsSetting(t *testing.T) {
	for _, src := range []string{
		"set lists\nset dotenv-filename := ['foo', 'bar']\nfoo:\n",
		"set lists := true\nset shell := ['bash', ['-c']]\n",
		"set lists\nx := ['a']\nset tempdir := x\n",
	} {
		err := analyzer.Analyze(mustParse(t, src))
		if err == nil || !strings.Contains(err.Error(), "list values are not supported") {
			t.Errorf("Analyze(%q) = %v", src, err)
		}
	}
}

// What upstream finds before it looks at list features is reported first, and
// what it finds after is reported after.
func TestAnalyzeListFeatureCheckOrder(t *testing.T) {
	// Settings and assignments are resolved before the list features.
	wantError(t, "x := ['a'] + y\n", 1, "variable `y` not defined")
	wantError(t, "x := bool()\n", 1, "function `bool` called with 0 arguments but takes 1")
	wantError(t, "set tempdir := ['a', 'b']\nx := ['a']\n", 1, "list literals require `set lists`")
	wantError(t, "set lists\nx := ['a', nope]\n", 2, "variable `nope` not defined")
	// Recipes are checked after the list features.
	wantError(t, "x := ['a']\nfoo:\n  echo {{y}}\n", 1, "list literals require `set lists`")
	wantError(t, "foo:\nfoo:\n  echo {{ show('a') }}\n", 3, "the `show()` function requires `set lists`")
	wantError(t, "x := 'a'\nfoo: (bar [])\nbar a:\n", 2, "list literals require `set lists`")
	// Items are checked first.
	wantError(t, "set lists\nset lists\nx := ['a']\n", 2, "setting `lists` first set on line 1 is redefined on line 2")
}
