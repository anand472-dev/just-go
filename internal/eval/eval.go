// Package eval turns expressions into strings.
//
// It is pure: every function reads the expression and scope it is given and
// returns a value. None performs I/O, none mutates its input, and there is no
// package-level state. The evaluator knows nothing of recipes, shells, or the
// environment. Strings are Go strings, which are byte sequences, and no
// conversion is applied, so multibyte text passes through unchanged.
package eval

import (
	"fmt"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
)

// Scope holds evaluated assignment values, by name.
type Scope map[string]string

// Evaluate returns the string value of e, looking variables up in scope. A
// variable with no entry in scope, a list (which belongs to the unstable
// `lists` setting), or an expression of a type the evaluator does not know,
// is a *diag.Error of KindRuntime. An undefined variable is
// located at its name, as the syntax tree records it, but without a file,
// which the caller fills in. Evaluate is pure.
func Evaluate(e ast.Expr, scope Scope) (string, error) {
	switch x := e.(type) {
	case ast.Text:
		return x.Value, nil
	case ast.StringLit:
		return x.Value, nil
	case ast.Var:
		return lookup(x, scope)
	case ast.Concat:
		return concat(x, scope)
	case ast.List:
		return "", fail("list values are not supported")
	default:
		return "", fail("cannot evaluate expression of type %T", e)
	}
}

// EvaluateAll returns the string value of each of exprs, in order, looking
// variables up in scope. It stops at the first error Evaluate reports. With no
// expressions it returns nil. EvaluateAll is pure.
func EvaluateAll(exprs []ast.Expr, scope Scope) ([]string, error) {
	if len(exprs) == 0 {
		return nil, nil
	}
	values := make([]string, len(exprs))
	for i, e := range exprs {
		v, err := Evaluate(e, scope)
		if err != nil {
			return nil, err
		}
		values[i] = v
	}
	return values, nil
}

// lookup returns the value of v in scope, or an error if it has none. It is
// pure.
func lookup(v ast.Var, scope Scope) (string, error) {
	val, ok := scope[v.Name]
	if !ok {
		err := fail("variable `%s` not defined", v.Name)
		err.Line, err.Col, err.Width = v.Line, v.Col, len(v.Name)
		return "", err
	}
	return val, nil
}

// concat evaluates both sides of c and joins them. It is pure.
func concat(c ast.Concat, scope Scope) (string, error) {
	left, err := Evaluate(c.Left, scope)
	if err != nil {
		return "", err
	}
	right, err := Evaluate(c.Right, scope)
	if err != nil {
		return "", err
	}
	return left + right, nil
}

// fail builds a runtime error. It is pure.
func fail(format string, args ...any) *diag.Error {
	return &diag.Error{Kind: diag.KindRuntime, Msg: fmt.Sprintf(format, args...)}
}
