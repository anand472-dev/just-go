package analyzer

import (
	"errors"
	"sort"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/settings"
)

// expressionAttributes are the attributes of a recipe that take expressions
// and have an effect, in the order upstream resolves them: it keeps a
// recipe's attributes sorted by name.
var expressionAttributes = [...]string{ast.AttrConfirm, ast.AttrDoc, ast.AttrWorkingDirectory}

// attributeExpressions checks the expressions in the attributes of r, after
// its parameters and dependency arguments and before its body, as upstream
// does. A `[confirm]` or `[working-directory]` expression sees the globals
// and every parameter, since it is evaluated when the recipe runs. A `[doc]`
// sees only the globals, and is evaluated now, in a const context: the
// variables it names must be const. It is pure.
func attributeExpressions(jf *ast.Justfile, r *ast.Recipe, globals, params map[string]struct{}) *diag.Error {
	for _, name := range expressionAttributes {
		a := r.Attribute(name)
		if a == nil {
			continue
		}
		locals := params
		if name == ast.AttrDoc {
			locals = nil
		}
		check := func(ref ast.Expr) *diag.Error { return checkReference(jf, ref, globals, locals) }
		for _, arg := range a.Args {
			if err := references(arg, check); err != nil {
				return err
			}
		}
		if name == ast.AttrDoc && len(a.Args) == 1 {
			if err := constError(settings.ConstValue(jf, a.Args[0])); err != nil {
				return err
			}
		}
	}
	return nil
}

// constError is the diagnostic of the error of a const evaluation, or nil
// for none. It is pure.
func constError(_ string, err error) *diag.Error {
	var listed *settings.ListError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &listed):
		return listed.Diag
	}
	return err.(*diag.Error)
}

// duplicateDefault reports the second recipe, in the order of their names,
// that has `[default]`, as upstream does: the one of the two that is later in
// the file. It is pure.
func duplicateDefault(jf *ast.Justfile) *diag.Error {
	var defaults []*ast.Recipe
	unique := jf.UniqueRecipes()
	for i := range unique {
		if unique[i].Attribute(ast.AttrDefault) != nil {
			defaults = append(defaults, &unique[i])
		}
	}
	if len(defaults) < 2 {
		return nil
	}
	sort.Slice(defaults, func(i, j int) bool { return defaults[i].Name < defaults[j].Name })
	r := defaults[1]
	if defaults[0].Line > r.Line {
		r = defaults[0]
	}
	return fail(jf, ast.Span{Line: r.Line, Col: r.Col, Width: len(r.Name)},
		"recipe `%s` has duplicate `[default]` attribute, which may only appear once per module", r.Name)
}
