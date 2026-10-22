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
	if err := r.walk(a.Value, a.Line); err != nil {
		return err
	}
	r.stack = r.stack[:len(r.stack)-1]
	r.done[name] = true
	return nil
}

// walk checks the variables in e, reading left to right. Line is where e
// appears. A nil expression has no variables. It is pure.
func (r *assignmentResolver) walk(e ast.Expr, line int) *diag.Error {
	switch e := e.(type) {
	case ast.Var:
		return r.variable(e.Name, line)
	case ast.Concat:
		if err := r.walk(e.Left, line); err != nil {
			return err
		}
		return r.walk(e.Right, line)
	}
	return nil
}

// variable checks one reference to name made on line. It is pure.
func (r *assignmentResolver) variable(name string, line int) *diag.Error {
	if r.done[name] {
		return nil
	}
	a, ok := r.byName[name]
	if !ok {
		return fail(r.jf, line, "variable `%s` not defined", name)
	}
	if contains(r.stack, name) {
		return r.circular(a, name)
	}
	return r.resolve(name)
}

// circular builds the error for a cycle that returns to a, reported at the
// line where a is defined, as upstream does. It is pure.
func (r *assignmentResolver) circular(a ast.Assignment, name string) *diag.Error {
	cycle := cycleFrom(r.stack, name)
	if len(cycle) == 2 {
		return fail(r.jf, a.Line, "variable `%s` is defined in terms of itself", name)
	}
	return fail(r.jf, a.Line, "variable `%s` depends on its own value: `%s`", name, circle(cycle))
}

// undefinedInRecipe checks one recipe's parameter defaults and body against
// the globals. A parameter default sees the globals and the parameters
// before it. A body line sees the globals and every parameter. A parameter
// default has no line of its own, so it is reported at the line of its
// recipe. It is pure.
func undefinedInRecipe(jf *ast.Justfile, r ast.Recipe, globals map[string]struct{}) *diag.Error {
	params := make(map[string]struct{}, len(r.Params))
	for _, p := range r.Params {
		if name, ok := undefinedIn(p.Default, globals, params); ok {
			return fail(jf, r.Line, "variable `%s` not defined", name)
		}
		params[p.Name] = struct{}{}
	}
	for _, l := range r.Lines {
		for _, part := range l.Parts {
			if name, ok := undefinedIn(part, globals, params); ok {
				return fail(jf, l.Line, "variable `%s` not defined", name)
			}
		}
	}
	return nil
}

// undefinedIn returns the first name in e, reading left to right, that is in
// neither globals nor locals. A nil expression has no names. It is pure.
func undefinedIn(e ast.Expr, globals, locals map[string]struct{}) (string, bool) {
	switch e := e.(type) {
	case ast.Var:
		_, inGlobals := globals[e.Name]
		_, inLocals := locals[e.Name]
		return e.Name, !inGlobals && !inLocals
	case ast.Concat:
		if name, ok := undefinedIn(e.Left, globals, locals); ok {
			return name, true
		}
		return undefinedIn(e.Right, globals, locals)
	}
	return "", false
}
