package eval

import (
	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/rustre"
)

// errLists is the error of what needs `set lists`, which is unstable and not
// implemented (R15): a comparison outside a condition, an `if` without an
// `else`, and a condition that is not a comparison. The parser records these
// uses and the analyzer refuses them unless the file sets `lists`, so only a
// file that does reaches this. Pure.
func errLists() *diag.Error { return fail("list values are not supported") }

// conditional evaluates the condition of c, and then only the branch it
// selects. It is pure apart from what cmd does.
func conditional(c ast.Conditional, scope Scope, cmd Commander) (string, error) {
	cmp, ok := c.Cond.(ast.Comparison)
	if !ok || c.Else == nil {
		return "", errLists()
	}
	taken, err := holds(cmp, scope, cmd)
	if err != nil {
		return "", err
	}
	if taken {
		return EvaluateWith(c.Then, scope, cmd)
	}
	return EvaluateWith(c.Else, scope, cmd)
}

// holds evaluates both sides of cmp, left first, and compares them. It is pure
// apart from what cmd does.
func holds(cmp ast.Comparison, scope Scope, cmd Commander) (bool, error) {
	left, err := EvaluateWith(cmp.Left, scope, cmd)
	if err != nil {
		return false, err
	}
	right, err := EvaluateWith(cmp.Right, scope, cmd)
	if err != nil {
		return false, err
	}
	taken, failure := Compare(cmp, left, right)
	if failure != nil {
		return false, failure
	}
	return taken, nil
}

// assertion evaluates the condition of a, which is the empty string when it
// holds, and an error when it does not. The message is evaluated only then.
// It is pure apart from what cmd does.
func assertion(a ast.Assert, scope Scope, cmd Commander) (string, error) {
	cmp, ok := a.Cond.(ast.Comparison)
	if !ok {
		return "", errLists()
	}
	ok, err := holds(cmp, scope, cmd)
	if err != nil || ok {
		return "", err
	}
	message := "`" + a.Text + "`"
	if a.Message != nil {
		if message, err = EvaluateWith(a.Message, scope, cmd); err != nil {
			return "", err
		}
	}
	return "", AssertFailure(a, message)
}

// AssertFailure is the error of a failed assert whose message, or whose
// condition quoted in backticks when it has none, is message: located at the
// name `assert`, without a file, which the caller fills in. It is pure.
func AssertFailure(a ast.Assert, message string) *diag.Error {
	err := fail("assert failed: %s", message)
	err.Line, err.Col, err.Width = a.Line, a.Col, len("assert")
	return err
}

// Compare reports whether the comparison cmp holds between the values left and
// right: equal or unequal for `==` and `!=`, a match of the pattern right in
// left for `=~` and `!~`. A pattern that does not compile is a *diag.Error of
// KindRuntime located at the operator and worded as Rust's regex crate words
// it, without a file, which the caller fills in. Pure.
func Compare(cmp ast.Comparison, left, right string) (bool, *diag.Error) {
	switch cmp.Op {
	case ast.OpEqual:
		return left == right, nil
	case ast.OpNotEqual:
		return left != right, nil
	case ast.OpMatch, ast.OpMismatch:
		return match(cmp, left, right)
	}
	return false, fail("unknown comparison operator `%s`", cmp.Op)
}

// match is Compare for the regular-expression operators. Pure.
func match(cmp ast.Comparison, text, pattern string) (bool, *diag.Error) {
	re, err := rustre.Compile(pattern)
	if err != nil {
		bad := fail("%s", err)
		bad.Line, bad.Col, bad.Width = cmp.Line, cmp.Col, len(cmp.Op)
		return false, bad
	}
	return re.MatchString(text) == (cmp.Op == ast.OpMatch), nil
}
