package eval

import (
	"fmt"

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
	case !b.Implemented():
		return "", locateCall(fail("function %s is not implemented yet", c.Name), c)
	}
	args, err := EvaluateAllWith(c.Args, scope, cmd)
	if err != nil {
		return "", err
	}
	out, err := callBuiltin(c.Name, b, args, cmd)
	if err != nil {
		return "", locateCall(fail("%s", err), c)
	}
	return out, nil
}

// Hosted is implemented by a Commander that can say where the justfile it
// belongs to is and which filesystem it reads. The builtins that look at the
// world, such as read and path_exists, are called only through one.
type Hosted interface {
	Host() builtins.Host
}

// callBuiltin calls b with its evaluated arguments. A builtin that reads a
// Host is given that of cmd, and is an error when cmd has none. Whatever the
// builtin does is its own: it is pure for a pure one. name is the name the
// function was called by.
func callBuiltin(name string, b builtins.Builtin, args []string, cmd Commander) (string, error) {
	if b.Pure != nil {
		return b.Pure(args)
	}
	hosted, ok := cmd.(Hosted)
	if !ok {
		return "", fmt.Errorf("function `%s` cannot be called without a justfile", name)
	}
	return b.Impure(hosted.Host(), args)
}

// locateCall puts err at the name of the function that c calls. It is pure
// apart from updating err.
func locateCall(err *diag.Error, c ast.Call) *diag.Error {
	err.Line, err.Col, err.Width = c.Line, c.Col, len(c.Name)
	return err
}
