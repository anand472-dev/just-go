package runner

import "gitea.com/wimwian/just-go/internal/ast"

// usedAssignments returns the names of the assignments that set lazy
// evaluates, or nil, meaning all of them, when the setting is off. Those are
// the assignments the requested recipes use, directly or through other
// assignments, and those that are evaluated whether or not anything uses
// them: the exported ones, all of them under set export, and the ones
// written with `eager`. It is pure.
func usedAssignments(jf *ast.Justfile, calls []Call, overrides map[string]string) map[string]bool {
	if !jf.Settings.Lazy {
		return nil
	}
	u := newInUse(jf.UniqueAssignments())
	for name := range overrides {
		u.frozen[name] = true // an override's value is not evaluated, so its references are not in use
	}
	for _, a := range u.assignments {
		if a.Exported || a.Eager || jf.Settings.Export {
			u.mark([]string{a.Name})
		}
	}
	for _, r := range reachable(jf, calls) {
		u.mark(recipeVariables(jf, r))
	}
	u.follow()
	return u.used
}

// inUse collects the assignments in use. The ones marked but not yet
// followed, whose own variables are still to be marked, are in queue.
type inUse struct {
	assignments []ast.Assignment
	byName      map[string]ast.Assignment
	used        map[string]bool
	queue       []string
	frozen      map[string]bool // names used but never followed: those a --set overrides
}

// newInUse returns an inUse over assignments, with none in use. It is pure.
func newInUse(assignments []ast.Assignment) *inUse {
	u := &inUse{
		assignments: assignments,
		byName:      make(map[string]ast.Assignment, len(assignments)),
		used:        make(map[string]bool, len(assignments)),
		frozen:      map[string]bool{},
	}
	for _, a := range assignments {
		u.byName[a.Name] = a
	}
	return u
}

// mark puts the assignments called names in use. A name that is no
// assignment, such as a parameter, is ignored. It is pure apart from updating
// u.
func (u *inUse) mark(names []string) {
	for _, name := range names {
		if _, ok := u.byName[name]; ok && !u.used[name] {
			u.used[name] = true
			if !u.frozen[name] {
				u.queue = append(u.queue, name)
			}
		}
	}
}

// follow puts in use every assignment that an assignment in use refers to. It
// is pure apart from updating u.
func (u *inUse) follow() {
	for len(u.queue) > 0 {
		name := u.queue[len(u.queue)-1]
		u.queue = u.queue[:len(u.queue)-1]
		u.mark(ast.Variables(u.byName[name].Value, nil))
	}
}

// reachable returns the recipes that running calls may run: the requested
// ones and, transitively, their dependencies and subsequents. It is pure.
func reachable(jf *ast.Justfile, calls []Call) []*ast.Recipe {
	seen := make(map[string]bool, len(jf.Recipes))
	var out []*ast.Recipe
	var visit func(name string)
	visit = func(name string) {
		r := findRecipe(jf, name)
		if r == nil || seen[name] {
			return
		}
		seen[name] = true
		out = append(out, r)
		for _, deps := range [2][]ast.Dependency{r.Deps, r.Subsequents} {
			for _, d := range deps {
				visit(d.Name)
			}
		}
	}
	for _, c := range calls {
		visit(c.Recipe)
	}
	return out
}

// recipeVariables returns the names that r's parameter defaults, dependency
// arguments, attributes and body refer to, other than its own parameters: a
// name that a parameter defines is not the assignment of that name. A default
// sees only the parameters before it. Comments under set ignore-comments are
// not read. It is pure.
func recipeVariables(jf *ast.Justfile, r *ast.Recipe) []string {
	params := make(map[string]bool, len(r.Params))
	var names []string
	for _, p := range r.Params {
		if p.Default != nil {
			names = appendFree(names, ast.Variables(p.Default, nil), params)
		}
		params[p.Name] = true
	}
	for _, e := range dependencyArguments(r) {
		names = appendFree(names, ast.Variables(e, nil), params)
	}
	for _, a := range r.Attributes {
		names = appendFree(names, attributeVariables(a), attributeParams(a, params))
	}
	for _, e := range bodyParts(jf, r) {
		names = appendFree(names, ast.Variables(e, nil), params)
	}
	return names
}

// attributeVariables returns the names that the arguments of a refer to. It is
// pure.
func attributeVariables(a ast.Attribute) []string {
	var names []string
	for _, e := range a.Args {
		names = ast.Variables(e, names)
	}
	return names
}

// attributeParams returns the parameters of the recipe, params, that the
// expressions of a see: `[doc]` and `[env]` are evaluated before the
// parameters are bound, and see none. It is pure.
func attributeParams(a ast.Attribute, params map[string]bool) map[string]bool {
	if a.Name == ast.AttrDoc || a.Name == ast.AttrEnv {
		return nil
	}
	return params
}

// dependencyArguments returns the argument expressions of the dependencies
// of r, those before the body and those after it. It is pure.
func dependencyArguments(r *ast.Recipe) []ast.Expr {
	var out []ast.Expr
	for _, deps := range [2][]ast.Dependency{r.Deps, r.Subsequents} {
		for _, d := range deps {
			out = append(out, d.Args...)
		}
	}
	return out
}

// bodyParts returns the parts of the lines of r that are evaluated: all of
// them, but for the comments that set ignore-comments skips. A script skips
// none. It is pure.
func bodyParts(jf *ast.Justfile, r *ast.Recipe) []ast.Expr {
	var out []ast.Expr
	skipComments := jf.Settings.IgnoreComments && !r.IsScript(jf.Settings)
	for _, l := range r.Lines {
		if !(l.Comment && skipComments) {
			out = append(out, l.Parts...)
		}
	}
	return out
}

// appendFree appends the names of vars that are not in bound to names. It is
// pure.
func appendFree(names, vars []string, bound map[string]bool) []string {
	for _, v := range vars {
		if !bound[v] {
			names = append(names, v)
		}
	}
	return names
}

// wanted returns the assignments of all whose names are in only, or all of
// them when only is nil. It is pure.
func wanted(all []ast.Assignment, only map[string]bool) []ast.Assignment {
	if only == nil {
		return all
	}
	out := make([]ast.Assignment, 0, len(only))
	for _, a := range all {
		if only[a.Name] {
			out = append(out, a)
		}
	}
	return out
}
