package eval

import (
	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/eval/builtins"
)

// evaluateCall evaluates the arguments of c, left to right, and then the
// builtin it names. The error of an argument is returned as it is. The error
// of the builtin is located at the name of the function, without a file, which
// the caller fills in. A function that has no implementation yet, and a call
// the analyzer would have refused (an unknown function, or a count of
// arguments it does not take), are errors located the same way. It is pure
// apart from what cmd does.
func evaluateCall(c ast.Call, scope Scope, cmd Commander) (string, error) {
	b, ok := builtins.Lookup(c.Name)
	switch {
	case !ok:
		return "", locateCall(fail("call to undefined function `%s`", c.Name), c)
	case !b.Accepts(len(c.Args)):
		return "", locateCall(fail("function `%s` called with %d arguments", c.Name, len(c.Args)), c)
	case b.Pure == nil:
		return "", locateCall(fail("function %s is not implemented yet", c.Name), c)
	}
	args, err := EvaluateAllWith(c.Args, scope, cmd)
	if err != nil {
		return "", err
	}
	out, err := b.Pure(args)
	if err != nil {
		return "", locateCall(fail("%s", err), c)
	}
	return out, nil
}

// locateCall puts err at the name of the function that c calls. It is pure
// apart from updating err.
func locateCall(err *diag.Error, c ast.Call) *diag.Error {
	err.Line, err.Col, err.Width = c.Line, c.Col, len(c.Name)
	return err
}
