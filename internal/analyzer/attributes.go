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
var expressionAttributes = [...]string{
	ast.AttrConfirm, ast.AttrDoc, ast.AttrEnv, ast.AttrTimestamp, ast.AttrWorkingDirectory,
}

// attributeExpressions checks the expressions in the attributes of r, after
// its parameters and dependency arguments and before its body, as upstream
// does. A `[confirm]`, `[timestamp]` or `[working-directory]` expression sees
// the globals and every parameter, since it is evaluated when the recipe
// runs. A `[doc]` and an `[env]` see only the globals: the first is
// evaluated now, in a const context, so the variables it names must be
// const, and the second is evaluated before the parameters are bound. It is
// pure.
func attributeExpressions(jf *ast.Justfile, r *ast.Recipe, globals, params map[string]struct{}) *diag.Error {
	for _, name := range expressionAttributes {
		locals := params
		if name == ast.AttrDoc || name == ast.AttrEnv {
			locals = nil
		}
		for i := range r.Attributes {
			if r.Attributes[i].Name != name {
				continue
			}
			if err := attributeReferences(jf, &r.Attributes[i], globals, locals); err != nil {
				return err
			}
		}
	}
	return nil
}

// argumentReferences checks the variables in the keys of the arg attributes of r
// with check. They may name the parameters, since a value is evaluated when its
// option is given. It is pure.
func argumentReferences(r *ast.Recipe, check func(ast.Expr) *diag.Error) *diag.Error {
	for i := range r.Attributes {
		if r.Attributes[i].Name != ast.AttrArg {
			continue
		}
		for _, k := range r.Attributes[i].Keys {
			if k.Value == nil {
				continue
			}
			if err := references(k.Value, check); err != nil {
				return err
			}
		}
	}
	return nil
}

// attributeReferences checks that the variables in the arguments of a are
// defined, in globals or locals, and for a `[doc]` that its value is const.
// It is pure.
func attributeReferences(jf *ast.Justfile, a *ast.Attribute, globals, locals map[string]struct{}) *diag.Error {
	check := func(ref ast.Expr) *diag.Error { return checkReference(jf, ref, globals, locals) }
	for _, arg := range a.Args {
		if err := references(arg, check); err != nil {
			return err
		}
	}
	if a.Name == ast.AttrDoc && len(a.Args) == 1 {
		return constError(settings.ConstValue(jf, a.Args[0]))
	}
	return nil
}

// constError is the diagnostic of the error of a const evaluation, or nil
// for none. settings.ConstValue returns only a *diag.Error or a
// *settings.ListError; any other error is reported as an internal error, with
// its text, and not a panic. It is pure.
func constError(_ string, err error) *diag.Error {
	var listed *settings.ListError
	var located *diag.Error
	switch {
	case err == nil:
		return nil
	case errors.As(err, &listed):
		return listed.Diag
	case errors.As(err, &located):
		return located
	}
	return &diag.Error{Kind: diag.KindAnalysis, Msg: "internal error: unexpected error in a const context: " + err.Error()}
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
