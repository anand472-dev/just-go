package analyzer

import (
	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
)

// assignmentResolver walks the assignments depth first, as upstream does. Its
// state is local to one call of resolveAssignments.
type assignmentResolver struct {
	jf     *ast.Justfile
	byName map[string]ast.Assignment
	done   map[string]bool
	stack  []string
}

// resolveAssignments checks every assignment, in name order, each after the
// assignments it uses. It reports a reference to a name that is not an
// assignment, and a cycle of assignments. It is pure.
func resolveAssignments(jf *ast.Justfile) *diag.Error {
	r := &assignmentResolver{
		jf:     jf,
		byName: make(map[string]ast.Assignment, len(jf.Assignments)),
		done:   make(map[string]bool, len(jf.Assignments)),
	}
	for _, a := range jf.Assignments {
		r.byName[a.Name] = a
	}
	for _, name := range sortedNames(r.byName) {
		if err := r.resolve(name); err != nil {
			return err
		}
	}
	return nil
}

// resolve checks the assignment called name unless it is already checked. It
// is pure.
func (r *assignmentResolver) resolve(name string) *diag.Error {
	if r.done[name] {
		return nil
	}
	a := r.byName[name]
	r.stack = append(r.stack, name)
	if err := r.walk(a.Value); err != nil {
		return err
	}
	r.stack = r.stack[:len(r.stack)-1]
	r.done[name] = true
	return nil
}

// walk checks the variables in e, reading left to right. A nil expression
// has no variables. It is pure.
func (r *assignmentResolver) walk(e ast.Expr) *diag.Error {
	switch e := e.(type) {
	case ast.Var:
		return r.variable(e)
	case ast.Concat:
		if err := r.walk(e.Left); err != nil {
			return err
		}
		return r.walk(e.Right)
	}
	return nil
}

// variable checks one reference, reported at the reference. It is pure.
func (r *assignmentResolver) variable(v ast.Var) *diag.Error {
	if r.done[v.Name] {
		return nil
	}
	a, ok := r.byName[v.Name]
	if !ok {
		return fail(r.jf, name(v.Line, v.Col, v.Name), "variable `%s` not defined", v.Name)
	}
	if contains(r.stack, v.Name) {
		return r.circular(a)
	}
	return r.resolve(v.Name)
}

// circular builds the error for a cycle that returns to a, reported at the
// name of a, as upstream does. It is pure.
func (r *assignmentResolver) circular(a ast.Assignment) *diag.Error {
	cycle := cycleFrom(r.stack, a.Name)
	at := name(a.Line, a.Col, a.Name)
	if len(cycle) == 2 {
		return fail(r.jf, at, "variable `%s` is defined in terms of itself", a.Name)
	}
	return fail(r.jf, at, "variable `%s` depends on its own value: `%s`", a.Name, circle(cycle))
}

// undefinedInRecipe checks one recipe's parameter defaults and body against
// the globals. A parameter default sees the globals and the parameters
// before it. A body line sees the globals and every parameter. Each is
// reported at the variable. It is pure.
func undefinedInRecipe(jf *ast.Justfile, r ast.Recipe, globals map[string]struct{}) *diag.Error {
	params := make(map[string]struct{}, len(r.Params))
	for _, p := range r.Params {
		if v, ok := undefinedIn(p.Default, globals, params); ok {
			return undefinedVar(jf, v)
		}
		params[p.Name] = struct{}{}
	}
	for _, l := range r.Lines {
		for _, part := range l.Parts {
			if v, ok := undefinedIn(part, globals, params); ok {
				return undefinedVar(jf, v)
			}
		}
	}
	return nil
}

// undefinedVar is the error for a reference to a name that is not defined.
// It is pure.
func undefinedVar(jf *ast.Justfile, v ast.Var) *diag.Error {
	return fail(jf, name(v.Line, v.Col, v.Name), "variable `%s` not defined", v.Name)
}

// undefinedIn returns the first variable in e, reading left to right, whose
// name is in neither globals nor locals. A nil expression has none. It is
// pure.
func undefinedIn(e ast.Expr, globals, locals map[string]struct{}) (ast.Var, bool) {
	switch e := e.(type) {
	case ast.Var:
		_, inGlobals := globals[e.Name]
		_, inLocals := locals[e.Name]
		return e, !inGlobals && !inLocals
	case ast.Concat:
		if v, ok := undefinedIn(e.Left, globals, locals); ok {
			return v, true
		}
		return undefinedIn(e.Right, globals, locals)
	}
	return ast.Var{}, false
}
