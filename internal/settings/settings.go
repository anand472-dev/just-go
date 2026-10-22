// Package settings evaluates the expression settings of a justfile.
//
// Upstream evaluates `set shell`, `set windows-shell`,
// `set script-interpreter`, `set tempdir` and `set working-directory` in a
// const context, before anything runs: they may name variables and
// concatenate strings, but they may not call functions or backticks, and a
// variable they name must itself be const. Resolve does the same.
//
// The package is pure: it reads the syntax tree it is given and returns
// values. It performs no I/O and keeps no package-level state.
package settings

import (
	"fmt"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
)

// Resolved holds the expression settings of a justfile after evaluation.
// Each is nil when the setting is not set.
type Resolved struct {
	// Shell, WindowsShell and ScriptInterpreter are the command followed by
	// its arguments.
	Shell             []string
	WindowsShell      []string
	ScriptInterpreter []string
	Tempdir           *string
	WorkingDirectory  *string
}

// Expressions returns the expressions of the expression settings of jf, in the
// order Resolve evaluates them: by setting name, and for an interpreter its
// command and then its arguments. It is pure.
func Expressions(jf *ast.Justfile) []ast.Expr {
	set := jf.Settings
	var out []ast.Expr
	out = append(out, set.ScriptInterpreter...)
	out = append(out, set.Shell...)
	if set.Tempdir != nil {
		out = append(out, set.Tempdir)
	}
	out = append(out, set.WindowsShell...)
	if set.WorkingDirectory != nil {
		out = append(out, set.WorkingDirectory)
	}
	return out
}

// Resolve evaluates the expression settings of jf. They are evaluated in the
// order of their names, as upstream's table of settings is, so that a justfile
// with several faulty settings reports the same one. It returns the first
// failure as a *diag.Error of KindAnalysis, located at the variable or call
// concerned. Resolve is pure.
func Resolve(jf *ast.Justfile) (Resolved, error) {
	c := newConst(jf)
	var out Resolved
	steps := []func() *diag.Error{
		func() (err *diag.Error) { out.ScriptInterpreter, err = c.list(jf.Settings.ScriptInterpreter); return },
		func() (err *diag.Error) { out.Shell, err = c.list(jf.Settings.Shell); return },
		func() (err *diag.Error) { out.Tempdir, err = c.one(jf.Settings.Tempdir); return },
		func() (err *diag.Error) { out.WindowsShell, err = c.list(jf.Settings.WindowsShell); return },
		func() (err *diag.Error) { out.WorkingDirectory, err = c.one(jf.Settings.WorkingDirectory); return },
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return Resolved{}, err
		}
	}
	return out, nil
}

// consts evaluates expressions in a const context. memo holds the assignments
// evaluated so far, by name.
type consts struct {
	jf     *ast.Justfile
	byName map[string]ast.Assignment
	memo   map[string]constValue
}

// constValue is the result of evaluating an assignment in a const context:
// its value, or the fact that it is not const.
type constValue struct {
	value    string
	notConst bool
}

func newConst(jf *ast.Justfile) *consts {
	byName := make(map[string]ast.Assignment, len(jf.Assignments))
	for _, a := range jf.Assignments {
		byName[a.Name] = a
	}
	return &consts{jf: jf, byName: byName, memo: make(map[string]constValue, len(byName))}
}

// list evaluates the expressions of an interpreter setting, one string each.
// A nil list is a setting that is not set. It is pure.
func (c *consts) list(exprs []ast.Expr) ([]string, *diag.Error) {
	if exprs == nil {
		return nil, nil
	}
	out := make([]string, len(exprs))
	for i, e := range exprs {
		v, err := c.expr(e)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

// one evaluates a setting made of a single expression. A nil expression is a
// setting that is not set. It is pure.
func (c *consts) one(e ast.Expr) (*string, *diag.Error) {
	if e == nil {
		return nil, nil
	}
	v, err := c.expr(e)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// expr evaluates e in a const context. It is pure.
func (c *consts) expr(e ast.Expr) (string, *diag.Error) {
	switch e := e.(type) {
	case ast.StringLit:
		return e.Value, nil
	case ast.Var:
		return c.variable(e)
	case ast.Concat:
		return c.concat(e)
	case ast.Call:
		return "", c.fail(e.Line, e.Col, e.Name, "cannot call functions in const context")
	}
	return "", c.fail(0, 0, "", "cannot evaluate expression of type %T in a setting", e)
}

// concat evaluates both sides of cat and joins them. It is pure.
func (c *consts) concat(cat ast.Concat) (string, *diag.Error) {
	left, err := c.expr(cat.Left)
	if err != nil {
		return "", err
	}
	right, err := c.expr(cat.Right)
	if err != nil {
		return "", err
	}
	return left + right, nil
}

// variable evaluates a reference to an assignment. An assignment that is not
// const makes the reference an error at the reference. It is pure.
func (c *consts) variable(v ast.Var) (string, *diag.Error) {
	a, ok := c.byName[v.Name]
	if !ok {
		return "", c.fail(v.Line, v.Col, v.Name, "variable `%s` not defined", v.Name)
	}
	got, ok := c.memo[v.Name]
	if !ok {
		// A cycle would come back here; the analyzer reports cycles before
		// settings are evaluated, so this only keeps a hand-built tree finite.
		c.memo[v.Name] = constValue{notConst: true}
		got = c.assignment(a)
		c.memo[v.Name] = got
	}
	if got.notConst {
		return "", c.fail(v.Line, v.Col, v.Name, "cannot access non-const variable `%s` in const context", v.Name)
	}
	return got.value, nil
}

// assignment evaluates the value of a. Whatever stops it makes the assignment
// non-const: the analyzer has already ruled out undefined variables, so what
// is left is a call. It is pure.
func (c *consts) assignment(a ast.Assignment) constValue {
	v, err := c.expr(a.Value)
	if err != nil {
		return constValue{notConst: true}
	}
	return constValue{value: v}
}

// fail builds an analysis error located at the name that starts at line and
// col. It is pure.
func (c *consts) fail(line, col int, name, format string, args ...any) *diag.Error {
	return &diag.Error{
		Kind: diag.KindAnalysis, File: c.jf.File, Line: line, Col: col, Width: len(name),
		Msg: fmt.Sprintf(format, args...),
	}
}
