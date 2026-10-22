package analyzer

import (
	"fmt"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
)

// recipeResolver walks the recipes depth first, as upstream does. Its state
// is local to one call of resolveRecipes.
type recipeResolver struct {
	jf         *ast.Justfile
	globals    map[string]struct{}
	byName     map[string]*ast.Recipe
	unresolved map[string]*ast.Recipe
	resolved   map[string]bool
	stack      []string
}

// resolveRecipes checks every recipe, in name order, each after the recipes
// it depends on. For one recipe the dependencies come first, then the
// parameter defaults, then the body, then the argument counts of the
// dependencies. It reports an unknown or circular dependency, an undefined
// variable, and a dependency that needs arguments, at the dependency or
// variable concerned. It is pure.
func resolveRecipes(jf *ast.Justfile) *diag.Error {
	r := &recipeResolver{
		jf:         jf,
		globals:    make(map[string]struct{}, len(jf.Assignments)),
		byName:     make(map[string]*ast.Recipe, len(jf.Recipes)),
		unresolved: make(map[string]*ast.Recipe, len(jf.Recipes)),
		resolved:   make(map[string]bool, len(jf.Recipes)),
	}
	for _, a := range jf.Assignments {
		r.globals[a.Name] = struct{}{}
	}
	// The maps hold pointers: a Recipe is too large to be stored in a map
	// without an allocation per entry.
	for i := range jf.Recipes {
		rec := &jf.Recipes[i]
		r.unresolved[rec.Name] = rec
		r.byName[rec.Name] = rec
	}
	for _, name := range sortedNames(r.unresolved) {
		rec, ok := r.unresolved[name]
		if !ok {
			continue
		}
		delete(r.unresolved, name)
		if err := r.resolve(rec); err != nil {
			return err
		}
	}
	return nil
}

// resolve checks rec after its dependencies. It is pure.
func (r *recipeResolver) resolve(rec *ast.Recipe) *diag.Error {
	r.stack = append(r.stack, rec.Name)
	for _, deps := range dependencyLists(rec) {
		for _, dep := range deps {
			if err := r.dependency(rec, dep); err != nil {
				return err
			}
		}
	}
	r.stack = r.stack[:len(r.stack)-1]
	if err := undefinedInRecipe(r.jf, rec, r.globals); err != nil {
		return err
	}
	if err := r.argumentCounts(rec); err != nil {
		return err
	}
	r.resolved[rec.Name] = true
	return nil
}

// argumentCounts reports the first dependency of rec that is given too few or
// too many arguments for the parameters of its recipe. Every dependency is
// resolved by now. It is pure.
func (r *recipeResolver) argumentCounts(rec *ast.Recipe) *diag.Error {
	for _, deps := range dependencyLists(rec) {
		for _, dep := range deps {
			callee := r.byName[dep.Name]
			found, lo, hi := len(dep.Args), required(callee), len(callee.Params)
			if found < lo || found > hi {
				return fail(r.jf, name(dep.Line, dep.Col, dep.Name), "dependency `%s` got %s but takes %s",
					dep.Name, count(found, "argument"), takes(found, lo, hi))
			}
		}
	}
	return nil
}

// dependencyLists returns the dependencies of rec before its body and those
// after it, in the order upstream resolves them. It returns an array, so that
// ranging over it allocates nothing. It is pure.
func dependencyLists(rec *ast.Recipe) [2][]ast.Dependency {
	return [2][]ast.Dependency{rec.Deps, rec.Subsequents}
}

// takes words the number of arguments a recipe accepts, as upstream's
// DependencyArgumentCountMismatch does: exactly lo when lo is hi, else at
// least lo when found is below it, else at most hi. It is pure.
func takes(found, lo, hi int) string {
	switch {
	case lo == hi:
		return count(lo, "argument")
	case found < lo:
		return "at least " + count(lo, "argument")
	}
	return "at most " + count(hi, "argument")
}

// required counts the parameters of rec that have no default. It is pure.
func required(rec *ast.Recipe) int {
	n := 0
	for _, p := range rec.Params {
		if p.Default == nil {
			n++
		}
	}
	return n
}

// count formats n and noun, adding "s" unless n is 1, as upstream's
// Count::numbered does. It is pure.
func count(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// dependency checks that dep, named by rec, is a recipe that can be resolved
// before rec. It is pure.
func (r *recipeResolver) dependency(rec *ast.Recipe, dep ast.Dependency) *diag.Error {
	if r.resolved[dep.Name] {
		return nil
	}
	at := name(dep.Line, dep.Col, dep.Name)
	if contains(r.stack, dep.Name) {
		return r.circular(rec, dep.Name, at)
	}
	next, ok := r.unresolved[dep.Name]
	if !ok {
		return fail(r.jf, at, "recipe `%s` has unknown dependency `%s`", rec.Name, dep.Name)
	}
	delete(r.unresolved, dep.Name)
	return r.resolve(next)
}

// circular builds the error for rec depending on dep, which is being
// resolved, at the dependency. It is pure.
func (r *recipeResolver) circular(rec *ast.Recipe, dep string, at ast.Span) *diag.Error {
	cycle := cycleFrom(r.stack, dep)
	if len(cycle) == 2 {
		return fail(r.jf, at, "recipe `%s` depends on itself", rec.Name)
	}
	return fail(r.jf, at, "recipe `%s` has circular dependency `%s`", rec.Name, circle(cycle))
}
