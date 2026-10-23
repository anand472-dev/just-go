package analyzer_test

import (
	"testing"

	"gitea.com/wimwian/just-go/internal/analyzer"
)

// The variable of every part of a conditional is checked, in the order the
// parts are written, and the branch that is not taken is checked too. The
// rows are conditional::undefined_*#1 and undefined_variables::
// conditional_reports_first_undefined_variable#1, with the oracle's positions.
func TestAnalyzeUndefinedVariablesInConditionals(t *testing.T) {
	for _, tc := range []struct {
		name, src        string
		line, col, width int
		msg              string
	}{
		{"conditional::undefined_lhs#1", "a := if b == '' { '' } else { '' }\n\nfoo:\n  echo {{ a }}\n", 1, 9, 1, "variable `b` not defined"},
		{"conditional::undefined_rhs#1", "a := if '' == b { '' } else { '' }\n\nfoo:\n  echo {{ a }}\n", 1, 15, 1, "variable `b` not defined"},
		{"conditional::undefined_then#1", "a := if '' == '' { b } else { '' }\n\nfoo:\n  echo {{ a }}\n", 1, 20, 1, "variable `b` not defined"},
		{"conditional::undefined_otherwise#1", "a := if '' == '' { '' } else { b }\n\nfoo:\n  echo {{ a }}\n", 1, 32, 1, "variable `b` not defined"},
		{"undefined_variables::conditional_reports_first_undefined_variable#1", "x := if foo == bar { baz } else { qux }", 1, 9, 3, "variable `foo` not defined"},
		{"an else if", "x := if 'a' == 'b' { 'c' } else if y =~ 'a' { 'd' } else { 'e' }\n", 1, 36, 1, "variable `y` not defined"},
		{"an assert condition", "x := assert(y == 'a', 'm')\n", 1, 13, 1, "variable `y` not defined"},
		{"an assert message", "x := assert('a' == 'a', y)\n", 1, 25, 1, "variable `y` not defined"},
		{"in a recipe line", "foo:\n  echo {{ if 'a' == 'b' { 'c' } else { y } }}\n", 2, 40, 1, "variable `y` not defined"},
		{"in a recipe line comparison", "foo:\n  echo {{ if y == 'b' { 'c' } else { 'd' } }}\n", 2, 14, 1, "variable `y` not defined"},
		{"a parameter is not visible to a global", "x := if p == 'a' { 'b' } else { 'c' }\nfoo p:\n", 1, 9, 1, "variable `p` not defined"},
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

// A recipe's parameters and the global variables are in scope in a
// conditional, and a call in it is checked as any call is.
func TestAnalyzeAcceptsConditionals(t *testing.T) {
	for _, src := range []string{
		"x := 'a'\nfoo p:\n  echo {{ if p == x { assert(p =~ 'a') + x } else { p + 'b' } }}\n",
		"x := if 'a' == 'a' { 'b' } else if 'c' != 'd' { 'e' } else { 'f' }\n",
	} {
		if err := analyzer.Analyze(mustParse(t, src)); err != nil {
			t.Errorf("Analyze(%q) = %v", src, err)
		}
	}
}

func TestAnalyzeChecksTheArityOfACallInAConditional(t *testing.T) {
	wantError(t, "x := if 'a' == 'a' { uppercase() } else { 'b' }\n", 1,
		"function `uppercase` called with 0 arguments but takes 1")
	wantError(t, "x := assert(uppercase('a', 'b') == 'a')\n", 1,
		"function `uppercase` called with 2 arguments but takes 1")
}

// The features of `set lists` that conditionals use are errors without it.
// conditional::if_without_else_requires_lists_setting#1 and the comparison
// group give the first and last rows; the positions are the oracle's.
func TestAnalyzeConditionalListFeaturesNeedTheListsSetting(t *testing.T) {
	for _, tc := range []struct {
		name, src        string
		line, col, width int
		msg              string
	}{
		{"conditional::if_without_else_requires_lists_setting#1", "TEST := if path_exists('/bin/bash') == 'true' {'yes'}\n", 1, 9, 2,
			"`if` without `else` requires `set lists`"},
		{"comparison::non_comparison_condition_requires_lists_setting#1", "x := if \"foo\" { \"t\" } else { \"f\" }\n\nfoo:\n  @echo hi\n", 1, 9, 5,
			"`if` and `assert` conditions other than comparisons require `set lists`"},
		{"a comparison outside a condition", "x := 'a' == 'b'\n", 1, 10, 2, "comparison operators require `set lists`"},
		{"an assert condition that is not a comparison", "x := assert('a')\n", 1, 13, 3,
			"`if` and `assert` conditions other than comparisons require `set lists`"},
		{"the first of two", "x := if 'a' == 'b' { 'c' }\ny := 'a' == 'b'\n", 1, 6, 2, "`if` without `else` requires `set lists`"},
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

func BenchmarkAnalyzeConditionals(b *testing.B) {
	src := "x := 'a'\n"
	for i := 0; i < 100; i++ {
		src += "foo" + string(rune('a'+i%26)) + string(rune('a'+i/26)) + " p:\n  echo {{ if p == x { assert(p =~ 'a') + x } else { p + 'b' } }}\n"
	}
	jf := mustParse(b, src)
	b.ReportAllocs()
	for b.Loop() {
		if err := analyzer.Analyze(jf); err != nil {
			b.Fatal(err)
		}
	}
}
