package runner

import (
	"reflect"
	"testing"

	"gitea.com/wimwian/just-go/internal/sys/sysfake"
)

// conditional::if_else#1, and the interpolation and format cases that abut
// their braces.
func TestRunConditionalPicksItsBranch(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"x := if '0' == '1' { 'a' } else if '0' == '0' { 'b' } else { 'c' }\n\nfoo:\n  echo {{ x }}\n", "echo b"},
		{"foo:\n  echo {{if 'a' == 'b' { 'c' } else { 'd' }}}\n", "echo d"},
		{"foo := if 'abbbc' =~ 'ab+c' {\n  'yes'\n} else {\n  'no'\n}\n\ndefault:\n  echo {{ foo }}\n", "echo yes"},
		{"foo := if 'Foo' !~ '^ab+c' { 'mismatch' } else { 'match' }\n\ndefault:\n  echo {{ foo }}\n", "echo mismatch"},
		{"foo p='y':\n  echo {{ if p != 'x' { p + '!' } else { 'x' } }}\n", "echo y!"},
	} {
		fake := &sysfake.FakeProcess{}
		plan, _, _ := newPlan(t, tc.src, fake)
		mustRun(t, plan)
		if got := commands(fake); !reflect.DeepEqual(got, []string{tc.want}) {
			t.Errorf("%q ran %q, want %q", tc.src, got, tc.want)
		}
	}
}

// The branch that is not taken does not run its backtick
// (conditional::then_branch_unevaluated#1 and otherwise_branch_unevaluated#1).
func TestRunConditionalRunsOnlyTheBacktickOfTheBranchItTakes(t *testing.T) {
	for _, src := range []string{
		"foo:\n  echo {{ if 'a' == 'b' { `exit 1` } else { 'otherwise' } }}\n",
		"foo:\n  echo {{ if 'a' != 'a' { `exit 1` } else { 'otherwise' } }}\n",
	} {
		fake := &sysfake.FakeProcess{}
		plan, _, _ := newPlan(t, src, fake)
		mustRun(t, plan)
		if got := commands(fake); !reflect.DeepEqual(got, []string{"echo otherwise"}) {
			t.Errorf("%q ran %q", src, got)
		}
	}
	fake := &sysfake.FakeProcess{Outputs: []string{"out"}}
	plan, _, _ := newPlan(t, "foo:\n  echo {{ if 'a' == 'a' { `pick` } else { `exit 1` } }}\n", fake)
	mustRun(t, plan)
	if got := commands(fake); !reflect.DeepEqual(got, []string{"pick", "echo out"}) {
		t.Errorf("ran %q", got)
	}
}

// regexes::bad_regex_fails_at_runtime#1: the recipe's earlier line has run
// when the pattern is found bad, and the error is at the operator.
func TestRunBadRegexFailsAtRuntime(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	src := "default:\n  echo before\n  echo {{ if '' =~ '(' { 'a' } else { 'b' } }}\n  echo after\n"
	plan, _, _ := backtickPlan(t, src, fake)
	de := failure(t, plan)
	if de.Msg != "regex parse error:\n    (\n    ^\nerror: unclosed group" || de.Line != 3 || de.Col != 17 || de.Width != 2 || de.File != "justfile" {
		t.Errorf("error = %+v", de)
	}
	if got := commands(fake); !reflect.DeepEqual(got, []string{"echo before"}) {
		t.Errorf("ran %q, want only the first line", got)
	}
}

// assertions::assert_fail#1, assert_pass#1 and
// assert_without_message_reports_condition#1.
func TestRunAssert(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	plan, _, stderr := backtickPlan(t, "foo:\n  {{ assert('a' == 'a', 'error message') }}\n", fake)
	mustRun(t, plan)
	if len(fake.Specs) != 0 || stderr.Len() != 0 {
		t.Errorf("a passing assert ran %q and printed %q, want an empty line that does not run", commands(fake), stderr)
	}
	for _, tc := range []struct{ src, msg string }{
		{"foo:\n  {{ assert('a' != 'a', 'error message') }}\n", "assert failed: error message"},
		{"foo:\n  {{ assert('a' != 'a') }}\n", "assert failed: `'a' != 'a'`"},
	} {
		fake := &sysfake.FakeProcess{}
		plan, _, _ := backtickPlan(t, tc.src, fake)
		de := failure(t, plan)
		if de.Msg != tc.msg || de.Line != 2 || de.Col != 6 || de.Width != 6 || len(fake.Specs) != 0 {
			t.Errorf("%q: error = %+v after %d commands", tc.src, de, len(fake.Specs))
		}
	}
}

// lazy::unused_assert_not_evaluated#1: under `set lazy` an assignment nothing
// uses is not evaluated, so its failing assert does not run; without the
// setting it is.
func TestRunLazyAssignmentWithAFailingAssertIsNotEvaluated(t *testing.T) {
	const body = "x := assert('foo' == 'bar', 'fail')\n\nfoo:\n  @echo foo\n"
	fake := &sysfake.FakeProcess{}
	plan, _, _ := backtickPlan(t, "set lazy\n\n"+body, fake)
	mustRun(t, plan)
	if got := commands(fake); !reflect.DeepEqual(got, []string{"echo foo"}) {
		t.Errorf("ran %q", got)
	}
	fake = &sysfake.FakeProcess{}
	plan, _, _ = backtickPlan(t, body, fake)
	if de := failure(t, plan); de.Msg != "assert failed: fail" {
		t.Errorf("error = %+v", de)
	}
}

// A variable that only an untaken branch names is still evaluated when
// something uses the conditional: upstream finds the variables of an
// expression by looking at all of it.
func TestRunLazyEvaluatesTheVariablesOfBothBranches(t *testing.T) {
	fake := &sysfake.FakeProcess{Outputs: []string{"left", "right"}}
	plan, _, _ := backtickPlan(t, "set lazy\na := `l`\nb := `r`\nc := if 'x' == 'x' { a } else { b }\nfoo:\n  @echo {{c}}\n", fake)
	mustRun(t, plan)
	if got := commands(fake); !reflect.DeepEqual(got, []string{"l", "r", "echo left"}) {
		t.Errorf("ran %q", got)
	}
}
