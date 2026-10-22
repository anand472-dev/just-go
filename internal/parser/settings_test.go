package parser_test

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/parser"
)

// boolSettings maps each boolean setting to the field it sets.
var boolSettings = map[string]func(ast.Settings) bool{
	"allow-duplicate-recipes":   func(s ast.Settings) bool { return s.AllowDuplicateRecipes },
	"allow-duplicate-variables": func(s ast.Settings) bool { return s.AllowDuplicateVariables },
	"default-list":              func(s ast.Settings) bool { return s.DefaultList },
	"default-script":            func(s ast.Settings) bool { return s.DefaultScript },
	"export":                    func(s ast.Settings) bool { return s.Export },
	"fallback":                  func(s ast.Settings) bool { return s.Fallback },
	"guards":                    func(s ast.Settings) bool { return s.Guards },
	"ignore-comments":           func(s ast.Settings) bool { return s.IgnoreComments },
	"lazy":                      func(s ast.Settings) bool { return s.Lazy },
	"no-cd":                     func(s ast.Settings) bool { return s.NoCd },
	"no-exit-message":           func(s ast.Settings) bool { return s.NoExitMessage },
	"positional-arguments":      func(s ast.Settings) bool { return s.PositionalArguments },
	"quiet":                     func(s ast.Settings) bool { return s.Quiet },
	"windows-powershell":        func(s ast.Settings) bool { return s.WindowsPowerShell },
}

func TestParseBooleanSettings(t *testing.T) {
	for name, field := range boolSettings {
		for _, tc := range []struct {
			src  string
			want bool
		}{
			{"set " + name + "\n", true},
			{"set " + name + " := true\n", true},
			{"set " + name + " := false\n", false},
			{"set " + name + " := false # why\n", false},
		} {
			jf := mustParse(t, tc.src)
			if got := field(jf.Settings); got != tc.want {
				t.Errorf("Parse(%q): %s = %v, want %v", tc.src, name, got, tc.want)
			}
			// Only the one setting is touched.
			for other, otherField := range boolSettings {
				if other != name && otherField(jf.Settings) {
					t.Errorf("Parse(%q) also set %s", tc.src, other)
				}
			}
		}
	}
}

func TestParseRecordsEachSetStatement(t *testing.T) {
	jf, err := parser.Parse("justfile", []byte("set quiet\n\nset export := false\nset shell := ['sh']\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := []ast.SettingSite{
		{Name: "quiet", Active: true, Line: 1, Col: 5},
		{Name: "export", Active: false, Line: 3, Col: 5},
		{Name: "shell", Active: true, Line: 4, Col: 5},
	}
	if !reflect.DeepEqual(jf.Settings.Sites, want) {
		t.Errorf("Sites = %+v, want %+v", jf.Settings.Sites, want)
	}
}

// isVar reports whether e is a reference to the variable name.
func isVar(e ast.Expr, name string) bool {
	v, ok := e.(ast.Var)
	return ok && v.Name == name
}

func TestParseExpressionSettings(t *testing.T) {
	jf := mustParse(t, "d := 'x'\n"+
		"set tempdir := d\n"+
		"set working-directory := 'a' + 'b'\n"+
		"set windows-shell := ['powershell.exe', '-NoLogo', ]\n"+
		"set script-interpreter := [d, '-eu']\n"+
		"set shell := [\"bash\"]\n")
	s := jf.Settings
	if v, _ := s.Tempdir.(ast.Var); v != (ast.Var{Name: "d", Line: 2, Col: 16}) {
		t.Errorf("Tempdir = %+v", s.Tempdir)
	}
	if s.WorkingDirectory != (ast.Concat{Left: lit("a"), Right: lit("b")}) {
		t.Errorf("WorkingDirectory = %+v", s.WorkingDirectory)
	}
	if want := lits("powershell.exe", "-NoLogo"); !reflect.DeepEqual(s.WindowsShell, want) {
		t.Errorf("WindowsShell = %+v, want %+v", s.WindowsShell, want)
	}
	if want := lits("bash"); !reflect.DeepEqual(s.Shell, want) {
		t.Errorf("Shell = %+v, want %+v", s.Shell, want)
	}
	if got := s.ScriptInterpreter; len(got) != 2 || !isVar(got[0], "d") || got[1] != lit("-eu") {
		t.Errorf("ScriptInterpreter = %+v", got)
	}
}

func TestParseInterpreterWithTrailingCommaAndExpressions(t *testing.T) {
	jf := mustParse(t, "set shell := [\n  'a' + 'b',\n  x(),\n]\n")
	want := []ast.Expr{
		ast.Concat{Left: lit("a"), Right: lit("b")},
		ast.Call{Name: "x"},
	}
	got := jf.Settings.Shell
	if len(got) != 2 || got[0] != want[0] || got[1].(ast.Call).Name != "x" {
		t.Errorf("Shell = %+v, want %+v", got, want)
	}
}

// The values are those of the corpus's format and settings groups.
func TestParseIndentationAndMinimumVersion(t *testing.T) {
	jf := mustParse(t, "set indentation := ' '\nset minimum-version := '0.0.0'\n")
	if jf.Settings.Indentation != " " || jf.Settings.MinimumVersion != "0.0.0" {
		t.Errorf("Settings = %+v", jf.Settings)
	}
	for _, src := range []string{"set indentation := \"\\t\\t\"\n", "set indentation := '\t'\n"} {
		if got := mustParse(t, src).Settings.Indentation; strings.Trim(got, "\t") != "" || got == "" {
			t.Errorf("Parse(%q): Indentation = %q", src, got)
		}
	}
	for _, v := range []string{"1.59.0", "1.0.0", "0.9.9", "0.0.0"} {
		src := fmt.Sprintf("set minimum-version := %q\n", v)
		if got := mustParse(t, src).Settings.MinimumVersion; got != v {
			t.Errorf("Parse(%q): MinimumVersion = %q", src, got)
		}
	}
}

// wantErrorWidth is wantError that also checks the width of the error.
func wantErrorWidth(t *testing.T, src string, line, col, width int, msg string) {
	t.Helper()
	_, err := parser.Parse("justfile", []byte(src))
	var de *diag.Error
	if !errors.As(err, &de) {
		t.Fatalf("Parse(%q) error = %v, want a *diag.Error", src, err)
	}
	want := diag.Error{Kind: diag.KindParse, File: "justfile", Line: line, Col: col, Width: width, Msg: msg}
	if *de != want {
		t.Errorf("Parse(%q) error =\n %+v\nwant\n %+v", src, *de, want)
	}
}

// Every row but the last two is a case of the corpus: the group and case are
// named, and the message, position and width are the oracle's.
func TestParseSettingErrorsMatchUpstream(t *testing.T) {
	const plain = "`%s` setting must be a plain string literal"
	for _, tc := range []struct {
		name, src      string
		line, col, wid int
		msg            string
	}{
		{"assignment::set_export_parse_error#1", "set export := fals", 1, 15, 4,
			"expected keyword `true` or `false` but found identifier `fals`"},
		{"assignment::set_export_parse_error_eol#1", "set export :=\n", 1, 14, 1,
			"expected identifier, but found end of line"},
		{"format::indentation_setting_invalid_values#1", "set indentation := ''\n", 1, 20, 2,
			"indentation must not be empty"},
		{"format::indentation_setting_invalid_values#2", "set indentation := 'x'\n", 1, 20, 3,
			"indentation must be spaces or tabs"},
		{"format::indentation_setting_invalid_values#3", "set indentation := \" \\t\"\n", 1, 20, 5,
			"indentation may not be mixed"},
		{"format::indentation_setting_must_be_string_literal#1", "set indentation := ('  ' + '  ')\n", 1, 5, 11,
			fmt.Sprintf(plain, "indentation")},
		{"minimum_version::minimum_version_invalid#1", "set minimum-version := 'foo'\n", 1, 24, 5,
			"`minimum-version` setting has invalid version `foo`: expected `MAJOR.MINOR.PATCH`"},
		{"minimum_version::minimum_version_may_not_be_expression#1", "set minimum-version := ('1.' + '0')\n", 1, 5, 15,
			fmt.Sprintf(plain, "minimum-version")},
		{"minimum_version::minimum_version_may_not_be_indented_string#1", "set minimum-version := '''1.0.0'''\n", 1, 5, 15,
			fmt.Sprintf(plain, "minimum-version")},
		{"minimum_version::minimum_version_too_low#1", "set minimum-version := '999.0.0'\n", 1, 24, 9,
			"justfile requires just 999.0.0 or later, but using 1.59.0"},
		{"a group around a string", "set indentation := (' ')\n", 1, 5, 11,
			fmt.Sprintf(plain, "indentation")},
		{"an indented string with double quotes", "set indentation := \"\"\"  \"\"\"\n", 1, 5, 11,
			fmt.Sprintf(plain, "indentation")},
		{"a variable", "x := ' '\nset indentation := x\n", 2, 5, 11,
			fmt.Sprintf(plain, "indentation")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wantErrorWidth(t, tc.src, tc.line, tc.col, tc.wid, tc.msg)
		})
	}
}

func TestParseValueSettingErrors(t *testing.T) {
	wantError(t, "set tempdir\n", 1, 12, "expected ':=', but found end of line")
	wantError(t, "set tempdir :=\n", 1, 15, "expected "+value+", but found end of line")
	wantError(t, "set working-directory := 'a' 'b'\n", 1, 30,
		"expected "+afterTempdirValue+", but found string")
	wantError(t, "set minimum-version := (\n", 1, 26, "expected "+value+", but found end of file")
	wantError(t, "set indentation := 1\n", 1, 20, "unknown start of token '1'")
	wantError(t, "set windows-shell := ['a', ,]\n", 1, 28, "expected "+valueOrEnd+", but found ','")
	wantError(t, "set script-interpreter := []\n", 1, 28, "expected "+value+", but found ']'")
}

// valueOrEnd is what upstream looks for in a list where another element, or
// the end of the list, may come.
const valueOrEnd = "backtick, '!', '[', ']', identifier, '(', '/', or string"

// afterTempdirValue is what upstream looks for after the expression of a
// setting: an operator, a `+`, or the end of the line.
const afterTempdirValue = "'&&', '!=', '!~', '||', comment, end of file, end of line, '==', '=~', '+', '++', or '/'"

func TestParseMinimumVersionGrammar(t *testing.T) {
	for _, v := range []string{"foo", "1.0", "1.0.0.0", "01.0.0", "1.00.0", "1.0.x", "", "1234567890.0.0", " 1.0.0", "-1.0.0", "1..0"} {
		src := fmt.Sprintf("set minimum-version := %q\n", v)
		msg := fmt.Sprintf("`minimum-version` setting has invalid version `%s`: expected `MAJOR.MINOR.PATCH`", v)
		wantErrorWidth(t, src, 1, 24, len(v)+2, msg)
	}
	for _, v := range []string{"1.59.1", "1.60.0", "2.0.0"} {
		src := fmt.Sprintf("set minimum-version := %q\n", v)
		wantErrorWidth(t, src, 1, 24, len(v)+2, fmt.Sprintf("justfile requires just %s or later, but using %s", v, parser.UpstreamVersion))
	}
}

func TestParseExportAndEagerAssignments(t *testing.T) {
	jf := mustParse(t, "export A := 'a'\neager B := 'b'\nC := 'c'\nexport := 'd'\neager := 'e'\n")
	want := []ast.Assignment{
		{Name: "A", Value: lit("a"), Exported: true, Line: 1},
		{Name: "B", Value: lit("b"), Eager: true, Line: 2},
		{Name: "C", Value: lit("c"), Line: 3},
		{Name: "export", Value: lit("d"), Line: 4},
		{Name: "eager", Value: lit("e"), Line: 5},
	}
	if !reflect.DeepEqual(jf.Assignments, want) {
		t.Errorf("Assignments = %+v, want %+v", jf.Assignments, want)
	}
}

func TestParseExportAndEagerCanStillNameARecipe(t *testing.T) {
	jf := mustParse(t, "export foo:\n  echo\neager bar baz:\n  echo\nexport:\n  echo\n")
	if len(jf.Assignments) != 0 || len(jf.Recipes) != 3 {
		t.Fatalf("Assignments = %+v, Recipes = %+v", jf.Assignments, jf.Recipes)
	}
	if r := jf.Recipes[0]; r.Name != "export" || len(r.Params) != 1 || r.Params[0].Name != "foo" {
		t.Errorf("Recipes[0] = %+v", r)
	}
}

func TestParseExportErrors(t *testing.T) {
	wantError(t, "export a :=\n", 1, 12, "expected "+value+", but found end of line")
	wantError(t, "export a := 'x' b\n", 1, 17, "expected "+afterTempdirValue+", but found identifier")
}

func TestParseExportedParameters(t *testing.T) {
	jf := mustParse(t, "foo $a b $c='x' d='y':\n")
	var got []string
	for _, p := range jf.Recipes[0].Params {
		got = append(got, fmt.Sprintf("%s:%v", p.Name, p.Exported))
	}
	if want := []string{"a:true", "b:false", "c:true", "d:false"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Params = %v, want %v", got, want)
	}
	if d := jf.Recipes[0].Params[2].DefaultText; d != "'x'" {
		t.Errorf("DefaultText = %q", d)
	}
}

func TestParseExportedParameterNeedsAName(t *testing.T) {
	wantError(t, "foo $:\n", 1, 6, "expected identifier, but found ':'")
}

// A sigil is what comes first on a command; each may appear once, in any
// order, and `?` is one only under set guards.
func TestParseGuardSigils(t *testing.T) {
	for _, tc := range []struct {
		line   string
		prefix ast.Prefix
		guard  bool
		text   string
	}{
		{"?true", ast.PrefixNone, true, "true"},
		{"@?true", ast.PrefixAt, true, "true"},
		{"?@true", ast.PrefixAt, true, "true"},
		{"?-true", ast.PrefixDash, true, "true"},
		{"@-?x", ast.PrefixAtDash, true, "x"},
		{"??x", ast.PrefixNone, true, "?x"},
		{"?@?x", ast.PrefixAt, true, "?x"},
		{"?", ast.PrefixNone, true, ""},
	} {
		jf := mustParse(t, "set guards\nr:\n  "+tc.line+"\n")
		l := jf.Recipes[0].Lines[0]
		var text string
		if len(l.Parts) > 0 {
			text = l.Parts[0].(ast.Text).Value
		}
		if l.Prefix != tc.prefix || l.Guard != tc.guard || text != tc.text {
			t.Errorf("line %q: prefix %v guard %v text %q; want %v %v %q", tc.line, l.Prefix, l.Guard, text, tc.prefix, tc.guard, tc.text)
		}
	}
}

func TestParseGuardIsTextWithoutTheSetting(t *testing.T) {
	jf := mustParse(t, "r:\n  ?true\n  @?true\n")
	for i, want := range []string{"?true", "?true"} {
		l := jf.Recipes[0].Lines[i]
		if l.Guard || l.Parts[0].(ast.Text).Value != want {
			t.Errorf("line %d = %+v", i, l)
		}
	}
}

// A setting may come after the recipes it changes.
func TestParseGuardsSettingAfterTheRecipe(t *testing.T) {
	jf := mustParse(t, "r:\n  ?true\nset guards\n")
	if l := jf.Recipes[0].Lines[0]; !l.Guard {
		t.Errorf("Line = %+v, want a guard", l)
	}
}

// guards::guard_sigil_may_not_be_used_with_infallible_sigil#1 and
// guards::sigils_are_recognized_after_ignored_comments#1: the parser marks
// the clash with the text it covers, and the analyzer reports it.
func TestParseGuardAndInfallibleSigilsTogether(t *testing.T) {
	jf, err := parser.Parse("justfile", []byte("set guards\n\n@foo:\n  -?exit 2\n  ?-x {{y}} z\n"))
	if err != nil {
		t.Fatal(err)
	}
	lines := jf.Recipes[0].Lines
	if want := (ast.Span{Line: 4, Col: 3, Width: 8}); lines[0].GuardClash != want {
		t.Errorf("GuardClash = %+v, want %+v", lines[0].GuardClash, want)
	}
	if want := (ast.Span{Line: 5, Col: 3, Width: 4}); lines[1].GuardClash != want {
		t.Errorf("GuardClash = %+v, want %+v", lines[1].GuardClash, want)
	}
	lone := mustParse(t, "set guards\nr:\n  ?x\n  -x\n  @-x\n").Recipes[0].Lines
	for _, l := range lone {
		if l.GuardClash != (ast.Span{}) {
			t.Errorf("line %+v has a clash", l)
		}
	}
}

func TestParseCommentLines(t *testing.T) {
	jf := mustParse(t, "r:\n  # a\n  @# b\n  echo # c\n  #!/bin/sh\n")
	var got []bool
	for _, l := range jf.Recipes[0].Lines {
		got = append(got, l.Comment)
	}
	if want := []bool{true, false, false, true}; !reflect.DeepEqual(got, want) {
		t.Errorf("Comment = %v, want %v", got, want)
	}
}

// ignore_comments::continuations_with_echo_comments_false#1 and
// ignore_comments::continuations_with_echo_comments_true#1.
func TestParseIgnoredCommentDoesNotContinue(t *testing.T) {
	ignored := mustParse(t, "set ignore-comments\n\nr:\n  # Comment lines ignore line continuations \\\n  echo something-useful\n")
	lines := ignored.Recipes[0].Lines
	if len(lines) != 2 || !lines[0].Comment || lines[1].Comment || lines[1].Parts[0].(ast.Text).Value != "echo something-useful" {
		t.Errorf("ignored: Lines = %+v", lines)
	}
	kept := mustParse(t, "set ignore-comments := false\n\nr:\n  # comment lines can be continued \\\n  echo something-useful\n")
	lines = kept.Recipes[0].Lines
	if len(lines) != 1 || lines[0].Parts[0].(ast.Text).Value != "# comment lines can be continued " {
		t.Errorf("kept: Lines = %+v", lines)
	}
}

// A comment on a continuation line belongs to the command it continues
// (ignore_comments::ignore_comments_evaluates_comments_on_continuation_lines#1).
func TestParseCommentOnContinuationLineIsPartOfTheCommand(t *testing.T) {
	jf := mustParse(t, "set ignore-comments\nr:\n  echo a \\\n  # b \\\n  echo c\n")
	lines := jf.Recipes[0].Lines
	if len(lines) != 1 || lines[0].Comment {
		t.Errorf("Lines = %+v, want one command that is not a comment", lines)
	}
}

// Sigils come after a comment that was ignored
// (guards::sigils_are_recognized_after_ignored_comments#1).
func TestParseSigilsFollowAnIgnoredComment(t *testing.T) {
	jf := mustParse(t, "set guards\nset ignore-comments\nr:\n  # comment \\\n  -?bar\n")
	lines := jf.Recipes[0].Lines
	if len(lines) != 2 || !lines[1].Guard || lines[1].Prefix != ast.PrefixDash || lines[1].GuardClash.Line != 5 {
		t.Errorf("Lines = %+v", lines)
	}
}

func TestParseSettingsAfterRecipesSetTheFlagsForAllOfThem(t *testing.T) {
	jf := mustParse(t, "r:\n  # c \\\n  echo\nset ignore-comments\n")
	if got := len(jf.Recipes[0].Lines); got != 2 {
		t.Errorf("Lines = %d, want 2: the comment does not continue", got)
	}
}

func TestParseErrorInFirstPassIsReported(t *testing.T) {
	wantError(t, "set guards\nr:\n  ?x {{\n", 3, 6, "unterminated interpolation")
}

func BenchmarkParseSettings(b *testing.B) {
	var sb strings.Builder
	for name := range boolSettings {
		fmt.Fprintf(&sb, "set %s\n", name)
	}
	sb.WriteString("set shell := ['bash', '-cu']\nset working-directory := 'a' + 'b'\nset tempdir := '.'\n")
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&sb, "r%d $a b='x':\n  # c %d \\\n  ?echo {{a}}\n  @-?echo {{b}}\n", i, i)
	}
	src := []byte(sb.String())
	b.ReportAllocs()
	b.SetBytes(int64(len(src)))
	for b.Loop() {
		if _, err := parser.Parse("justfile", src); err != nil {
			b.Fatal(err)
		}
	}
}
