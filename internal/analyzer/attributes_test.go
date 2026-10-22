package analyzer_test

import (
	"testing"

	"gitea.com/wimwian/just-go/internal/analyzer"
)

// Each row was produced by oracle/bin/just on the same source: the location
// from its "——▶ justfile:L:C" line, the width from its carets.
func TestAnalyzeAttributeErrors(t *testing.T) {
	cases := []struct {
		id, src          string
		line, col, width int
		msg              string
	}{
		{"undefined_variables::undefined_variable_in_confirm_expression#1", "[confirm(x)]\nfoo:\n", 1, 10, 1,
			"variable `x` not defined"},
		{"working_directory::attribute_undefined_variable#1", "[working-directory(x)]\nfoo:\n", 1, 20, 1,
			"variable `x` not defined"},
		{"attributes::doc_attribute_cannot_reference_undefined_variable#1", "[doc(undefined)]\nfoo:\n", 1, 6, 9,
			"variable `undefined` not defined"},
		{"function_definitions::undefined_in_confirm_attribute#1", "[confirm(foo())]\nbar:\n", 1, 10, 3,
			"call to undefined function `foo`"},
		{"a doc cannot see the parameters", "[doc(x)]\nfoo x:\n", 1, 6, 1, "variable `x` not defined"},
		{"a doc is const", "bar := uppercase('a')\n[doc(bar)]\nfoo:\n", 2, 6, 3,
			"cannot access non-const variable `bar` in const context"},
		{"a doc cannot call a function", "[doc(uppercase('a'))]\nfoo:\n", 1, 6, 9,
			"cannot call functions in const context"},
		{"a list is not a doc", "set lists\n[doc(['a'])]\nfoo:\n", 0, 0, 0, "list values are not supported"},
		{"a function takes its arguments", "[confirm(uppercase())]\nfoo:\n", 1, 10, 9,
			"function `uppercase` called with 0 arguments but takes 1"},
		{"a variable is looked up inside a sum", "a := 'x'\n[working-directory(a + b)]\nfoo:\n", 2, 24, 1,
			"variable `b` not defined"},
		{"parameter defaults come before attributes", "[doc(a)]\nfoo x=y:\n", 2, 7, 1, "variable `y` not defined"},
		{"dependency arguments come before attributes", "[doc(a)]\nfoo: (bar y)\nbar x:\n", 2, 11, 1, "variable `y` not defined"},
		{"attributes come before the body", "[confirm(a)]\nfoo:\n  echo {{b}}\n", 1, 10, 1, "variable `a` not defined"},
		{"attributes are met in upstream's order, confirm first", "[doc(a)]\n[confirm(b)]\nfoo:\n", 2, 10, 1,
			"variable `b` not defined"},
		{"the other recipe of a pair is checked too", "[default]\nfoo:\n[confirm(b)]\nbar:\n", 3, 10, 1,
			"variable `b` not defined"},
		{"attributes::env_attribute_name_cannot_reference_parameter#1", "[env(name, 'value')]\nfoo name:\n", 1, 6, 4,
			"variable `name` not defined"},
		{"attributes::env_attribute_value_cannot_reference_parameter#1", "[env('TARGET', target)]\ndeploy target:\n", 1, 16, 6,
			"variable `target` not defined"},
		{"an env value is looked up in the globals", "[env('A', x)]\nfoo:\n", 1, 11, 1, "variable `x` not defined"},
		{"every env is checked, in source order", "[env(z, '1')]\n[env(a, '2')]\nfoo:\n", 1, 6, 1, "variable `z` not defined"},
		{"the second env is checked too", "[env('A', 'b')]\n[env(c, 'd')]\nfoo:\n", 2, 6, 1, "variable `c` not defined"},
		{"a timestamp sees the parameters but not what is undefined", "[timestamp(y)]\nfoo:\n", 1, 12, 1,
			"variable `y` not defined"},
		{"env comes before working-directory", "[working-directory(a)]\n[env(b, 'x')]\nfoo:\n", 2, 6, 1,
			"variable `b` not defined"},
		{"env comes before timestamp", "[timestamp(a)]\n[env(b, 'x')]\nfoo:\n", 2, 6, 1, "variable `b` not defined"},
		{"timestamp comes after confirm", "[timestamp(b)]\n[confirm(a)]\nfoo:\n", 2, 10, 1, "variable `a` not defined"},
		{"attributes::extension_on_shell_error#1", "[extension: '.txt']\nbaz:\n", 2, 1, 3,
			"shell recipe `baz` has script recipe attribute `extension`"},
		{"a shell attribute makes a shebang recipe a shell recipe", "[extension: '.txt']\n[shell]\nbaz:\n  #!/bin/sh\n", 3, 1, 3,
			"shell recipe `baz` has script recipe attribute `extension`"},
		{"default::default_attribute_may_only_appear_once_per_justfile#1", "[default]\nfoo:\n\n[default]\nbar:\n", 5, 1, 3,
			"recipe `bar` has duplicate `[default]` attribute, which may only appear once per module"},
		{"default::duplicate_default_attribute_blames_source_later_recipe#1", "[default]\nbar:\n\n[default]\nfoo:\n", 5, 1, 3,
			"recipe `foo` has duplicate `[default]` attribute, which may only appear once per module"},
		{"the later recipe is blamed whatever the names", "[default]\nzed:\n\n[default]\nabc:\n", 5, 1, 3,
			"recipe `abc` has duplicate `[default]` attribute, which may only appear once per module"},
		{"only the first two by name matter", "[default]\nb:\n[default]\na:\n[default]\nc:\n", 4, 1, 1,
			"recipe `a` has duplicate `[default]` attribute, which may only appear once per module"},
	}
	for _, c := range cases {
		t.Run(c.id, func(t *testing.T) {
			got := analyze(t, c.src)
			if got.Line != c.line || got.Col != c.col || got.Width != c.width || got.Msg != c.msg {
				t.Errorf("Analyze(%q) = %d:%d width %d %q, want %d:%d width %d %q",
					c.src, got.Line, got.Col, got.Width, got.Msg, c.line, c.col, c.width, c.msg)
			}
		})
	}
}

func TestAnalyzeAcceptsAttributes(t *testing.T) {
	for _, src := range []string{
		"[confirm('Deploy to ' + target + '?')]\ndeploy target:\n",
		"[confirm]\n[no-cd]\n[group('a')]\n[group('b')]\n[private]\nfoo:\n",
		"a := 'x'\n[doc(a + 'y')]\n[working-directory(a)]\nfoo:\n",
		"a := b\nb := 'c'\n[doc(a)]\nfoo:\n",
		"[working-directory(target)]\nfoo target:\n",
		"[default]\nfoo:\nbar:\n",
		"set allow-duplicate-recipes\n[default]\nfoo:\n[default]\nfoo:\n",
		"x := 'q'\n[env('A', x)]\n[env('B', x + 'y')]\nfoo:\n",
		"[env('A', x)]\nfoo:\nx := 'q'\n",
		"[timestamp(format)]\nfoo format:\n",
		"[timestamp]\nfoo:\n",
		"[extension: '.txt']\n[script]\nfoo:\n",
		"[extension: '.txt']\nfoo:\n  #!/bin/sh\n",
		"set default-script\n[extension: '.txt']\nfoo:\n",
		"[script]\nfoo:\n  a\n    b\n",
		"foo:\n  #!/bin/sh\n    echo hi\n",
		"set default-script\nfoo:\n  echo foo\n    echo bar\n",
		"[shell]\nfoo:\n  echo foo\n",
		"[continue]\n[parallel]\n[metadata('a')]\n[linux]\nfoo:\n",
	} {
		if err := analyzer.Analyze(mustParse(t, src)); err != nil {
			t.Errorf("Analyze(%q) = %v, want nil", src, err)
		}
	}
}
