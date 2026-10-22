// Package settings evaluates the expression settings of a justfile.
//
// Upstream evaluates `set dotenv-command`, `set dotenv-filename`,
// `set dotenv-path`, `set shell`, `set windows-shell`,
// `set script-interpreter`, `set tempdir` and `set working-directory` in a
// const context, before anything runs: they may name variables and
// concatenate strings, but they may not call functions or backticks, and a
// variable they name must itself be const. Resolve does the same.
//
// A list literal is part of `set lists`, which is unstable and not
// implemented (R15). Evaluating one reports its elements' faults, as upstream
// would, and then a ListError, which the analyzer turns into upstream's
// refusal of a file that does not set `set lists`.
//
// The package is pure: it reads the syntax tree it is given and returns
// values. It performs no I/O and keeps no package-level state.
package settings

import (
	"errors"
	"fmt"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/eval"
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
	// DotenvCommand, DotenvFilename and DotenvPath are the values of the
	// dotenv settings. A setting that is set to an empty list is an empty,
	// non-nil slice, which is not the same as one that is not set.
	DotenvCommand  []string
	DotenvFilename []string
	DotenvPath     []string
}

// Expressions returns the expressions of the expression settings of jf, in the
// order Resolve evaluates them: by setting name, and for an interpreter its
// command and then its arguments. It is pure.
func Expressions(jf *ast.Justfile) []ast.Expr {
	set := jf.Settings
	var out []ast.Expr
	for _, e := range []ast.Expr{set.DotenvCommand, set.DotenvFilename, set.DotenvPath} {
		if e != nil {
			out = append(out, e)
		}
	}
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
	set := jf.Settings
	steps := []func() error{
		func() (err error) { out.DotenvCommand, err = c.many(set.DotenvCommand); return },
		func() (err error) { out.DotenvFilename, err = c.many(set.DotenvFilename); return },
		func() (err error) { out.DotenvPath, err = c.many(set.DotenvPath); return },
		func() (err error) { out.ScriptInterpreter, err = c.list(set.ScriptInterpreter); return },
		func() (err error) { out.Shell, err = c.list(set.Shell); return },
		func() (err error) { out.Tempdir, err = c.one(set.Tempdir); return },
		func() (err error) { out.WindowsShell, err = c.list(set.WindowsShell); return },
		func() (err error) { out.WorkingDirectory, err = c.one(set.WorkingDirectory); return },
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return Resolved{}, unfault(err)
		}
	}
	return out, nil
}

// ConstValue evaluates e in a const context of jf, as upstream evaluates the
// `[doc]` attribute of a recipe: variables must be assignments that are
// themselves const, and functions cannot be called. A failure is a
// *diag.Error of KindAnalysis, located at the variable or call concerned,
// or a *ListError. ConstValue is pure.
func ConstValue(jf *ast.Justfile, e ast.Expr) (string, error) {
	v, err := newConst(jf).expr(e)
	return v, unfault(err)
}

// consts evaluates expressions in a const context. memo holds the assignments
// evaluated so far, by name.
type consts struct {
	jf     *ast.Justfile
	byName map[string]ast.Assignment
	memo   map[string]constValue
}

// constValue is the result of evaluating an assignment in a const context:
// its value, or the fact that it is not const, or that it is a list.
type constValue struct {
	value    string
	notConst bool
	listed   bool
	fault    *fault
}

// ListError is the error of a list in a setting. Lists belong to the unstable
// `lists` setting, whose behavior is not implemented (R15); the error is for
// the caller to tell apart, so that it can refuse the list as upstream does
// without `set lists` first.
type ListError struct{ Diag *diag.Error }

// Error returns the message of the diagnostic. It is pure.
func (e *ListError) Error() string { return e.Diag.Error() }

func newConst(jf *ast.Justfile) *consts {
	byName := make(map[string]ast.Assignment, len(jf.Assignments))
	for _, a := range jf.Assignments {
		byName[a.Name] = a
	}
	return &consts{jf: jf, byName: byName, memo: make(map[string]constValue, len(byName))}
}

// list evaluates the expressions of an interpreter setting, one string each.
// A nil list is a setting that is not set. It is pure.
func (c *consts) list(exprs []ast.Expr) ([]string, error) {
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

// many evaluates a setting made of a single expression and returns its value
// as a one-element list, which is what the list form of the setting would
// give. A nil expression is a setting that is not set. It is pure.
func (c *consts) many(e ast.Expr) ([]string, error) {
	v, err := c.one(e)
	if v == nil {
		return nil, err
	}
	return []string{*v}, nil
}

// one evaluates a setting made of a single expression. A nil expression is a
// setting that is not set. It is pure.
func (c *consts) one(e ast.Expr) (*string, error) {
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
func (c *consts) expr(e ast.Expr) (string, error) {
	switch e := e.(type) {
	case ast.StringLit:
		return e.Value, nil
	case ast.Var:
		return c.variable(e)
	case ast.Concat:
		return c.concat(e)
	case ast.List:
		return c.elements(e)
	case ast.Conditional, ast.Assert, ast.Comparison:
		return c.branching(e)
	case ast.Call:
		return "", c.fail(e.Line, e.Col, e.Name, "cannot call functions in const context")
	case ast.Backtick:
		return "", c.failWidth(e.Line, e.Col, e.Width, "cannot call backticks in const context")
	}
	return "", c.fail(0, 0, "", "cannot evaluate expression of type %T in a setting", e)
}

// elements evaluates the elements of l, so that the first fault in them is
// reported as upstream reports it, and then refuses the list: its behavior is
// not implemented. It is pure.
func (c *consts) elements(l ast.List) (string, error) {
	for _, element := range l.Elements {
		if _, err := c.expr(element); err != nil {
			return "", err
		}
	}
	return "", c.listed()
}

// listed is the error of a list. It is pure.
func (c *consts) listed() error {
	return &ListError{Diag: c.fail(0, 0, "", "list values are not supported")}
}

// branching evaluates a conditional, an assert or a comparison. A comparison
// outside a condition needs `set lists`, so it is refused as a list is. It is
// pure.
func (c *consts) branching(e ast.Expr) (string, error) {
	switch e := e.(type) {
	case ast.Conditional:
		return c.conditional(e)
	case ast.Assert:
		return c.assertion(e)
	}
	return "", c.listed()
}

// conditional evaluates the condition of cond, and only the branch it picks.
// A conditional that needs `set lists` is refused as a list is. It is pure.
func (c *consts) conditional(cond ast.Conditional) (string, error) {
	holds, err := c.holds(cond.Cond)
	if err != nil {
		return "", err
	}
	switch {
	case cond.Else == nil:
		return "", c.listed()
	case holds:
		return c.expr(cond.Then)
	}
	return c.expr(cond.Else)
}

// assertion evaluates the condition of a, which gives the empty string when it
// holds. It is pure.
func (c *consts) assertion(a ast.Assert) (string, error) {
	holds, err := c.holds(a.Cond)
	if err != nil || holds {
		return "", err
	}
	message := "`" + a.Text + "`"
	if a.Message != nil {
		if message, err = c.expr(a.Message); err != nil {
			return "", err
		}
	}
	return "", c.fault(eval.AssertFailure(a, message))
}

// holds evaluates a comparison, left side first. Anything but a comparison is
// a condition that needs `set lists`: its faults are reported, and then it is
// refused as a list is. It is pure.
func (c *consts) holds(condition ast.Expr) (bool, error) {
	cmp, ok := condition.(ast.Comparison)
	if !ok {
		if _, err := c.expr(condition); err != nil {
			return false, err
		}
		return false, c.listed()
	}
	left, err := c.expr(cmp.Left)
	if err != nil {
		return false, err
	}
	right, err := c.expr(cmp.Right)
	if err != nil {
		return false, err
	}
	holds, failure := eval.Compare(cmp, left, right)
	if failure != nil {
		return false, c.fault(failure)
	}
	return holds, nil
}

// fault is the error of a failed assert or a bad pattern, which fails the
// evaluation even of an assignment that something refers to: unlike a call,
// it does not make the assignment non-const. It is pure apart from updating
// failure.
func (c *consts) fault(failure *diag.Error) error {
	failure.Kind, failure.File = diag.KindAnalysis, c.jf.File
	return &fault{failure}
}

// fault is a failure of the code a const context evaluates. Resolve and
// ConstValue return the error it holds.
type fault struct{ error }

// unfault returns the error that a fault holds, or err if it is not one.
// Pure.
func unfault(err error) error {
	var f *fault
	if errors.As(err, &f) {
		return f.error
	}
	return err
}

// concat evaluates both sides of cat and joins them. It is pure.
func (c *consts) concat(cat ast.Concat) (string, error) {
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
func (c *consts) variable(v ast.Var) (string, error) {
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
	if got.listed {
		return "", c.listed()
	}
	if got.fault != nil {
		return "", got.fault
	}
	if got.notConst {
		return "", c.fail(v.Line, v.Col, v.Name, "cannot access non-const variable `%s` in const context", v.Name)
	}
	return got.value, nil
}

// assignment evaluates the value of a. Whatever stops it makes the assignment
// non-const: the analyzer has already ruled out undefined variables, so what
// is left is a call, or a list. It is pure.
func (c *consts) assignment(a ast.Assignment) constValue {
	v, err := c.expr(a.Value)
	if err == nil {
		return constValue{value: v}
	}
	var le *ListError
	var f *fault
	switch {
	case errors.As(err, &le):
		return constValue{listed: true}
	case errors.As(err, &f):
		return constValue{fault: f}
	}
	return constValue{notConst: true}
}

// fail builds an analysis error located at the name that starts at line and
// col. It is pure.
func (c *consts) fail(line, col int, name, format string, args ...any) *diag.Error {
	return c.failWidth(line, col, len(name), format, args...)
}

// failWidth builds an analysis error located at the width runes that start at
// line and col. It is pure.
func (c *consts) failWidth(line, col, width int, format string, args ...any) *diag.Error {
	return &diag.Error{
		Kind: diag.KindAnalysis, File: c.jf.File, Line: line, Col: col, Width: width,
		Msg: fmt.Sprintf(format, args...),
	}
}
