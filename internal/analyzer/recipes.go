package analyzer

import (
	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
)

// recipeResolver walks the recipes depth first, as upstream does. Its state
// is local to one call of resolveRecipes.
type recipeResolver struct {
	jf         *ast.Justfile
	globals    map[string]struct{}
	unresolved map[string]ast.Recipe
	resolved   map[string]bool
	stack      []string
}

// resolveRecipes checks every recipe, in name order, each after the recipes
// it depends on. For one recipe the dependencies come first, then the
// parameter defaults, then the body. It reports an unknown or circular
// dependency and an undefined variable. A dependency has no line of its own,
// so it is reported at the line of its recipe. It is pure.
func resolveRecipes(jf *ast.Justfile) *diag.Error {
	r := &recipeResolver{
		jf:         jf,
		globals:    make(map[string]struct{}, len(jf.Assignments)),
		unresolved: make(map[string]ast.Recipe, len(jf.Recipes)),
		resolved:   make(map[string]bool, len(jf.Recipes)),
	}
	for _, a := range jf.Assignments {
		r.globals[a.Name] = struct{}{}
	}
	for _, rec := range jf.Recipes {
		r.unresolved[rec.Name] = rec
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
func (r *recipeResolver) resolve(rec ast.Recipe) *diag.Error {
	r.stack = append(r.stack, rec.Name)
	for _, dep := range rec.Deps {
		if err := r.dependency(rec, dep); err != nil {
			return err
		}
	}
	r.stack = r.stack[:len(r.stack)-1]
	if err := undefinedInRecipe(r.jf, rec, r.globals); err != nil {
		return err
	}
	r.resolved[rec.Name] = true
	return nil
}

// dependency checks that dep, named by rec, is a recipe that can be resolved
// before rec. It is pure.
func (r *recipeResolver) dependency(rec ast.Recipe, dep string) *diag.Error {
	if r.resolved[dep] {
		return nil
	}
	if contains(r.stack, dep) {
		return r.circular(rec, dep)
	}
	next, ok := r.unresolved[dep]
	if !ok {
		return fail(r.jf, rec.Line, "recipe `%s` has unknown dependency `%s`", rec.Name, dep)
	}
	delete(r.unresolved, dep)
	return r.resolve(next)
}

// circular builds the error for rec depending on dep, which is being
// resolved. It is pure.
func (r *recipeResolver) circular(rec ast.Recipe, dep string) *diag.Error {
	cycle := cycleFrom(r.stack, dep)
	if len(cycle) == 2 {
		return fail(r.jf, rec.Line, "recipe `%s` depends on itself", rec.Name)
	}
	return fail(r.jf, rec.Line, "recipe `%s` has circular dependency `%s`", rec.Name, circle(cycle))
}
