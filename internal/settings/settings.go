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
// implemented. The settings that take a list (the dotenv settings and the
// interpreters) flatten one, so that a file that is refused for using it is
// refused for that; a setting that takes one string takes a list of one
// element as that element, and refuses any other, as upstream does.
//
// The package is pure: it reads the syntax tree it is given and returns
// values. It performs no I/O and keeps no package-level state.
package settings

import (
	"fmt"
	"strings"

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
	steps := []func() *diag.Error{
		func() (err *diag.Error) { out.DotenvCommand, err = c.many(set.DotenvCommand); return },
		func() (err *diag.Error) { out.DotenvFilename, err = c.many(set.DotenvFilename); return },
		func() (err *diag.Error) { out.DotenvPath, err = c.many(set.DotenvPath); return },
		func() (err *diag.Error) { out.ScriptInterpreter, err = c.list(set.ScriptInterpreter); return },
		func() (err *diag.Error) { out.Shell, err = c.list(set.Shell); return },
		func() (err *diag.Error) { out.Tempdir, err = c.one("tempdir", set.Tempdir); return },
		func() (err *diag.Error) { out.WindowsShell, err = c.list(set.WindowsShell); return },
		func() (err *diag.Error) {
			out.WorkingDirectory, err = c.one("working-directory", set.WorkingDirectory)
			return
		},
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return Resolved{}, err
		}
	}
	return out, nil
}

// consts evaluates expressions in a const context. memo holds the assignments
// evaluated so far, by name. A value is a list of strings: a string is a list
// of one.
type consts struct {
	jf     *ast.Justfile
	byName map[string]ast.Assignment
	memo   map[string]constValue
}

// constValue is the result of evaluating an assignment in a const context:
// its value, or the fact that it is not const.
type constValue struct {
	value    []string
	notConst bool
}

func newConst(jf *ast.Justfile) *consts {
	byName := make(map[string]ast.Assignment, len(jf.Assignments))
	for _, a := range jf.Assignments {
		byName[a.Name] = a
	}
	return &consts{jf: jf, byName: byName, memo: make(map[string]constValue, len(byName))}
}

// list evaluates the expressions of an interpreter setting and joins their
// values, a list contributing its elements. A nil list is a setting that is
// not set. It is pure.
func (c *consts) list(exprs []ast.Expr) ([]string, *diag.Error) {
	if exprs == nil {
		return nil, nil
	}
	out := make([]string, 0, len(exprs))
	for _, e := range exprs {
		v, err := c.expr(e)
		if err != nil {
			return nil, err
		}
		out = append(out, v...)
	}
	return out, nil
}

// many evaluates a setting made of a single expression, which may be a list.
// A nil expression is a setting that is not set; an empty list is set, to
// nothing. It is pure.
func (c *consts) many(e ast.Expr) ([]string, *diag.Error) {
	if e == nil {
		return nil, nil
	}
	v, err := c.expr(e)
	if err != nil {
		return nil, err
	}
	return append([]string{}, v...), nil
}

// one evaluates the setting called name, which is made of a single
// expression that must be a string. A nil expression is a setting that is not
// set. Any list but one of a single element is an error at the setting's
// name. It is pure.
func (c *consts) one(name string, e ast.Expr) (*string, *diag.Error) {
	if e == nil {
		return nil, nil
	}
	v, err := c.expr(e)
	if err != nil {
		return nil, err
	}
	if len(v) != 1 {
		site := c.site(name)
		return nil, c.fail(site.Line, site.Col, name, "list value %s assigned to `%s` setting", debugList(v), name)
	}
	return &v[0], nil
}

// site returns where the setting called name is set. It is pure.
func (c *consts) site(name string) ast.SettingSite {
	for _, s := range c.jf.Settings.Sites {
		if s.Name == name {
			return s
		}
	}
	return ast.SettingSite{}
}

// debugList formats values as upstream words a list in an error:
// `["a", "b"]`, each element in double quotes with the backslash, the double
// quote, the line feed, the carriage return and the tab escaped, and any
// other character as it is. Pure.
func debugList(values []string) string {
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = `"` + escapes.Replace(v) + `"`
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

// escapes writes the characters of oracle/src/src/element.rs as escapes.
var escapes = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "\t", `\t`)

// expr evaluates e in a const context. It is pure.
func (c *consts) expr(e ast.Expr) ([]string, *diag.Error) {
	switch e := e.(type) {
	case ast.StringLit:
		return []string{e.Value}, nil
	case ast.Var:
		return c.variable(e)
	case ast.Concat:
		return c.concat(e)
	case ast.List:
		return c.elements(e)
	case ast.Call:
		return nil, c.fail(e.Line, e.Col, e.Name, "cannot call functions in const context")
	}
	return nil, c.fail(0, 0, "", "cannot evaluate expression of type %T in a setting", e)
}

// elements evaluates the elements of l and joins their values. It is pure.
func (c *consts) elements(l ast.List) ([]string, *diag.Error) {
	out := []string{}
	for _, element := range l.Elements {
		v, err := c.expr(element)
		if err != nil {
			return nil, err
		}
		out = append(out, v...)
	}
	return out, nil
}

// concat evaluates both sides of cat and joins them, as upstream's `+` does
// for lists: a single string is joined to each element of a list, and two
// lists of one length are joined element by element. Any other pair is
// refused. It is pure.
func (c *consts) concat(cat ast.Concat) ([]string, *diag.Error) {
	left, err := c.expr(cat.Left)
	if err != nil {
		return nil, err
	}
	right, err := c.expr(cat.Right)
	if err != nil {
		return nil, err
	}
	joined, ok := broadcast(left, right)
	if !ok {
		return nil, c.fail(0, 0, "", "cannot concatenate list values")
	}
	return joined, nil
}

// broadcast joins a and b as upstream's Value::apply does with the operator
// `+`: a single element is joined to every element of the other, and lists of
// equal length are joined pairwise. It reports false for any other pair,
// which includes an empty list beside a non-empty one. Pure.
func broadcast(a, b []string) ([]string, bool) {
	switch {
	case len(a) == 1 && len(b) >= 1:
		return mapEach(b, func(x string) string { return a[0] + x }), true
	case len(a) >= 1 && len(b) == 1:
		return mapEach(a, func(x string) string { return x + b[0] }), true
	case len(a) == len(b):
		out := make([]string, len(a))
		for i := range a {
			out[i] = a[i] + b[i]
		}
		return out, true
	}
	return nil, false
}

// mapEach returns f of each of values. Pure.
func mapEach(values []string, f func(string) string) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = f(v)
	}
	return out
}

// variable evaluates a reference to an assignment. An assignment that is not
// const makes the reference an error at the reference. It is pure.
func (c *consts) variable(v ast.Var) ([]string, *diag.Error) {
	a, ok := c.byName[v.Name]
	if !ok {
		return nil, c.fail(v.Line, v.Col, v.Name, "variable `%s` not defined", v.Name)
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
		return nil, c.fail(v.Line, v.Col, v.Name, "cannot access non-const variable `%s` in const context", v.Name)
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
