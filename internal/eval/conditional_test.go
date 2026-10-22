package eval

import (
	"errors"
	"strings"
	"testing"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
)

func str(s string) ast.Expr { return ast.StringLit{Value: s} }

func compare(op string, left, right ast.Expr) ast.Comparison {
	return ast.Comparison{Op: op, Left: left, Right: right, Line: 3, Col: 17}
}

func TestEvaluateConditionalPicksTheBranch(t *testing.T) {
	for _, tc := range []struct {
		op          string
		left, right string
		want        string
	}{
		{"==", "a", "a", "then"}, {"==", "a", "b", "otherwise"},
		{"!=", "a", "b", "then"}, {"!=", "a", "a", "otherwise"},
		{"=~", "abbbc", "ab+c", "then"}, {"=~", "abbbc", "ab{4}c", "otherwise"},
		{"!~", "Foo", "^ab+c", "then"}, {"!~", "Foo", "Foo", "otherwise"},
		{"=~", "", "", "then"},
	} {
		e := ast.Conditional{Cond: compare(tc.op, str(tc.left), str(tc.right)), Then: str("then"), Else: str("otherwise")}
		got, err := Evaluate(e, nil)
		if err != nil || got != tc.want {
			t.Errorf("%q %s %q = %q, %v; want %q", tc.left, tc.op, tc.right, got, err, tc.want)
		}
	}
}

// The branch that is not taken is not evaluated: conditional::then_branch_unevaluated#1
// and its siblings run `exit 1` in it.
func TestEvaluateConditionalEvaluatesOnlyTheBranchItTakes(t *testing.T) {
	boom := ast.Backtick{Command: "exit 1"}
	cmd := &scripted{out: []string{"ran"}}
	e := ast.Conditional{Cond: compare("==", str("a"), str("b")), Then: boom, Else: ast.Backtick{Command: "ok"}}
	got, err := EvaluateWith(e, nil, cmd)
	if err != nil || got != "ran" || len(cmd.ran) != 1 || cmd.ran[0] != "ok" {
		t.Errorf("got %q, %v after %q; want the else branch only", got, err, cmd.ran)
	}
	cmd = &scripted{out: []string{"ran"}}
	e = ast.Conditional{Cond: compare("==", str("a"), str("a")), Then: ast.Backtick{Command: "ok"}, Else: boom}
	got, err = EvaluateWith(e, nil, cmd)
	if err != nil || got != "ran" || len(cmd.ran) != 1 || cmd.ran[0] != "ok" {
		t.Errorf("got %q, %v after %q; want the then branch only", got, err, cmd.ran)
	}
}

// The operands of the comparison are evaluated left to right, and the
// condition before either branch.
func TestEvaluateComparisonEvaluatesBothSidesInOrder(t *testing.T) {
	cmd := &scripted{out: []string{"l", "r"}}
	e := ast.Conditional{
		Cond: compare("==", ast.Backtick{Command: "left"}, ast.Backtick{Command: "right"}),
		Then: str("t"), Else: str("e"),
	}
	if got, err := EvaluateWith(e, nil, cmd); err != nil || got != "e" || len(cmd.ran) != 2 || cmd.ran[0] != "left" {
		t.Errorf("got %q, %v after %q", got, err, cmd.ran)
	}
}

func TestEvaluateConditionalErrorsComeFromTheOperandsAndBranches(t *testing.T) {
	bad := ast.Var{Name: "b", Line: 1, Col: 9}
	for name, e := range map[string]ast.Expr{
		"left":  ast.Conditional{Cond: compare("==", bad, str("")), Then: str(""), Else: str("")},
		"right": ast.Conditional{Cond: compare("==", str(""), bad), Then: str(""), Else: str("")},
		"then":  ast.Conditional{Cond: compare("==", str(""), str("")), Then: bad, Else: str("")},
		"else":  ast.Conditional{Cond: compare("!=", str(""), str("")), Then: str(""), Else: bad},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Evaluate(e, Scope{})
			requireRuntime(t, err, "variable `b` not defined")
		})
	}
}

// A comparison outside a condition, an `if` without an `else`, and a
// condition that is not a comparison belong to `set lists`, which is not
// implemented (R15).
func TestEvaluateRefusesWhatNeedsLists(t *testing.T) {
	for name, e := range map[string]ast.Expr{
		"comparison":      compare("==", str("a"), str("a")),
		"without else":    ast.Conditional{Cond: compare("==", str("a"), str("a")), Then: str("t")},
		"plain condition": ast.Conditional{Cond: str("a"), Then: str("t"), Else: str("e")},
		"assert plain":    ast.Assert{Cond: str("a")},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Evaluate(e, nil)
			requireRuntime(t, err, "list values are not supported")
		})
	}
}

func TestEvaluateAssertPassesToTheEmptyString(t *testing.T) {
	e := ast.Assert{Cond: compare("==", str("a"), str("a")), Message: str("fail"), Text: "'a' == 'a'"}
	if got, err := Evaluate(e, nil); err != nil || got != "" {
		t.Errorf("got %q, %v; want the empty string", got, err)
	}
	// A message that would fail is not evaluated when the condition holds.
	e.Message = ast.Var{Name: "nope"}
	if got, err := Evaluate(e, Scope{}); err != nil || got != "" {
		t.Errorf("got %q, %v; want the empty string", got, err)
	}
}

// assertions::assert_fail#1 and assertions::assert_without_message_reports_condition#1.
func TestEvaluateAssertFails(t *testing.T) {
	failing := compare("!=", str("a"), str("a"))
	for _, tc := range []struct {
		name string
		e    ast.Assert
		msg  string
	}{
		{"message", ast.Assert{Cond: failing, Message: str("error message"), Text: "'a' != 'a'", Line: 2, Col: 6}, "assert failed: error message"},
		{"computed message", ast.Assert{Cond: failing, Message: ast.Concat{Left: str("a"), Right: str("b")}, Line: 2, Col: 6}, "assert failed: ab"},
		{"no message", ast.Assert{Cond: failing, Text: "'a' != 'a'", Line: 2, Col: 6}, "assert failed: `'a' != 'a'`"},
	} {
		_, err := Evaluate(tc.e, nil)
		requireRuntime(t, err, tc.msg)
		var de *diag.Error
		if !errors.As(err, &de) || de.Line != 2 || de.Col != 6 || de.Width != 6 || de.File != "" {
			t.Errorf("%s: error = %+v, want located at 2:6 with width 6 and no file", tc.name, de)
		}
	}
}

func TestEvaluateAssertMessageErrorComesFirst(t *testing.T) {
	e := ast.Assert{Cond: compare("!=", str("a"), str("a")), Message: ast.Var{Name: "m"}}
	_, err := Evaluate(e, Scope{})
	requireRuntime(t, err, "variable `m` not defined")
}

// regexes::bad_regex_fails_at_runtime#1 and settings::bad_regex#1: the
// operator is the location, and the message is the one the regex crate gives.
func TestEvaluateBadRegexIsLocatedAtTheOperator(t *testing.T) {
	e := ast.Conditional{Cond: compare("=~", str(""), str("(")), Then: str("a"), Else: str("b")}
	_, err := Evaluate(e, nil)
	requireRuntime(t, err, "regex parse error:\n    (\n    ^\nerror: unclosed group")
	var de *diag.Error
	if !errors.As(err, &de) || de.Line != 3 || de.Col != 17 || de.Width != 2 {
		t.Errorf("error = %+v, want located at 3:17 with width 2", de)
	}
}

// Each row was produced by oracle/bin/just on `x := if ” =~ '<pattern>'`: the
// caret is under the character at the index, counted in characters.
func TestRegexErrorsAreWordedAsTheRegexCrateWordsThem(t *testing.T) {
	const (
		group    = "unclosed group"
		unopened = "unopened group"
		repeat   = "repetition operator missing expression"
		class    = "unclosed character class"
	)
	for _, tc := range []struct {
		pattern string
		at      int
		fault   string
	}{
		{"(", 0, group}, {"(a", 0, group}, {"a(", 1, group}, {"x(y(z", 3, group}, {"(?P<n>a", 0, group},
		{`\((`, 2, group}, {"((a)", 0, group}, {"(a|", 0, group}, {`\[(`, 2, group}, {"é(", 1, group},
		{"(?i)(", 4, group}, {"(?P<n>x)(", 8, group}, {`[\]](`, 4, group}, {"[[:alpha:]](", 11, group},
		{"[^]a](", 5, group}, {"(?:a)(", 5, group},
		{")", 0, unopened}, {"a)", 1, unopened}, {"(a))", 3, unopened}, {"a(b)c)", 5, unopened},
		{"*", 0, repeat}, {"+a", 0, repeat}, {"a|*", 2, repeat}, {"(?:*)", 3, repeat}, {"(*)", 1, repeat},
		{"a|+", 2, repeat}, {"**", 0, repeat},
		{"[", 0, class}, {"[a", 0, class}, {"a[", 1, class}, {"[[:alpha:]", 0, class},
	} {
		want := "regex parse error:\n    " + tc.pattern + "\n    " + strings.Repeat(" ", tc.at) + "^\nerror: " + tc.fault
		_, err := Compare(compare("=~", str(""), str("")), "", tc.pattern)
		if err == nil || err.Msg != want {
			t.Errorf("pattern %q: error = %v, want %q", tc.pattern, err, want)
		}
	}
}

// Patterns the crate and Go agree are fine.
func TestRegexCompilesWhatBothEnginesAccept(t *testing.T) {
	for _, pattern := range []string{"[(]", "a.c", `\d+`, "(?i)A", "a{1,3}", "[^a-z]", "(?s).", "x|y|z", "", `[\]]`, "[]a]"} {
		if _, err := Compare(compare("=~", str(""), str("")), "", pattern); err != nil {
			t.Errorf("pattern %q: error %v", pattern, err)
		}
	}
}

// A pattern the scan cannot place is reported at its start with the message
// of Go's regexp.
func TestRegexErrorsWithoutAPositionStartAtTheFirstColumn(t *testing.T) {
	_, err := Compare(compare("=~", str(""), str("")), "", "[z-a]")
	var de *diag.Error
	if !errors.As(err, &de) || de.Msg != "regex parse error:\n    [z-a]\n    ^\nerror: invalid character class range: `z-a`" {
		t.Errorf("error = %v", err)
	}
}

func TestCompareUnknownOperatorIsInternal(t *testing.T) {
	_, err := Compare(ast.Comparison{Op: "<"}, "a", "b")
	requireRuntime(t, err, "unknown comparison operator `<`")
}

func BenchmarkEvaluateConditional(b *testing.B) {
	e := ast.Conditional{
		Cond: compare("=~", ast.Concat{Left: ast.Var{Name: "x"}, Right: str("c")}, str("^ab+c$")),
		Then: str("yes"), Else: str("no"),
	}
	scope := Scope{"x": "abb"}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Evaluate(e, scope); err != nil {
			b.Fatal(err)
		}
	}
}
