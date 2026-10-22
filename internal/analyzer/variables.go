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

// walk checks the calls and variables in e in upstream's order. A nil
// expression has none. It is pure.
func (r *assignmentResolver) walk(e ast.Expr) *diag.Error {
	return references(e, func(ref ast.Expr) *diag.Error {
		if c, ok := ref.(ast.Call); ok {
			return checkCall(r.jf, c)
		}
		return r.variable(ref.(ast.Var))
	})
}

// references calls visit on each call and each variable in e, in upstream's
// order: a call before its arguments, and the operands of a concatenation
// left to right, and the parts of a comparison, a conditional and an assert as
// they are written. It stops at the first error visit returns. A nil
// expression has none. It is pure.
func references(e ast.Expr, visit func(ast.Expr) *diag.Error) *diag.Error {
	switch e := e.(type) {
	case ast.Var:
		return visit(e)
	case ast.Call:
		if err := visit(e); err != nil {
			return err
		}
		return referencesAll(e.Args, visit)
	case ast.Concat:
		return referencesAll([]ast.Expr{e.Left, e.Right}, visit)
	case ast.List:
		return referencesAll(e.Elements, visit)
	}
	return referencesAll(ast.Operands(e), visit)
}

// referencesAll is references for each of es in turn, stopping at the first
// error. It is pure.
func referencesAll(es []ast.Expr, visit func(ast.Expr) *diag.Error) *diag.Error {
	for _, e := range es {
		if err := references(e, visit); err != nil {
			return err
		}
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

// undefinedInRecipe checks one recipe's parameter defaults, the arguments of
// its dependencies, its attributes and its body against the globals. A
// parameter default sees the globals and the parameters before it. A dependency argument and a body
// line see the globals and every parameter. A call of an
// unknown function, or with the wrong number of arguments, and a variable
// that is not defined are each reported at the name concerned. It is pure.
func undefinedInRecipe(jf *ast.Justfile, r *ast.Recipe, globals map[string]struct{}) *diag.Error {
	params := make(map[string]struct{}, len(r.Params))
	check := func(ref ast.Expr) *diag.Error { return checkReference(jf, ref, globals, params) }
	for _, p := range r.Params {
		if err := references(p.Default, check); err != nil {
			return err
		}
		params[p.Name] = struct{}{}
	}
	if err := dependencyArguments(r, check); err != nil {
		return err
	}
	if err := attributeExpressions(jf, r, globals, params); err != nil {
		return err
	}
	return bodyReferences(jf, r, check)
}

// bodyReferences checks the calls and variables in the interpolations of the
// lines of r with check, skipping the comments that set ignore-comments
// ignores. A script has no comments to ignore: it is evaluated whole. It is
// pure.
func bodyReferences(jf *ast.Justfile, r *ast.Recipe, check func(ast.Expr) *diag.Error) *diag.Error {
	skipComments := jf.Settings.IgnoreComments && !r.IsScript(jf.Settings)
	for _, l := range r.Lines {
		if skipComments && l.Comment {
			continue
		}
		for _, part := range l.Parts {
			if err := references(part, check); err != nil {
				return err
			}
		}
	}
	return nil
}

// dependencyArguments checks the calls and variables in the arguments of the
// dependencies of r, those before the body first, with check. It is pure.
func dependencyArguments(r *ast.Recipe, check func(ast.Expr) *diag.Error) *diag.Error {
	for _, deps := range dependencyLists(r) {
		for _, d := range deps {
			for _, arg := range d.Args {
				if err := references(arg, check); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// checkReference checks one call or variable of a recipe: a call must name a
// function that takes that many arguments, and a variable must be in globals
// or locals. It is pure.
func checkReference(jf *ast.Justfile, ref ast.Expr, globals, locals map[string]struct{}) *diag.Error {
	if c, ok := ref.(ast.Call); ok {
		return checkCall(jf, c)
	}
	v := ref.(ast.Var)
	_, inGlobals := globals[v.Name]
	_, inLocals := locals[v.Name]
	if inGlobals || inLocals {
		return nil
	}
	return undefinedVar(jf, v)
}

// undefinedVar is the error for a reference to a name that is not defined.
// It is pure.
func undefinedVar(jf *ast.Justfile, v ast.Var) *diag.Error {
	return fail(jf, name(v.Line, v.Col, v.Name), "variable `%s` not defined", v.Name)
}
