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
