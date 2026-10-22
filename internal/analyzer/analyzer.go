// Package analyzer checks a parsed justfile for semantic errors that upstream
// reports before anything runs: duplicate recipe and variable names, uses of
// undefined variables, and dependencies that name no recipe.
//
// The package is pure: every function reads the syntax tree it is given and
// returns a value. None performs I/O, none mutates its input, and there is no
// package-level state.
package analyzer

import (
	"fmt"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
)

// Analyze checks jf and returns the first error it finds as a *diag.Error of
// KindAnalysis, or nil. It is pure.
//
// The checks run in this order: duplicate recipe names, duplicate assignment
// names, undefined variables, unknown dependencies. The first check that
// fails decides the result, and within a check the earliest error in source
// order wins. Each error carries the line of the offending item. The syntax
// tree records no columns, so Col is 0, meaning unknown.
func Analyze(jf *ast.Justfile) error {
	checks := [...]func(*ast.Justfile) *diag.Error{
		duplicateRecipe,
		duplicateAssignment,
		undefinedVariable,
		unknownDependency,
	}
	for _, check := range checks {
		if err := check(jf); err != nil {
			return err
		}
	}
	return nil
}

// fail builds an analysis error for jf at line. It is pure.
func fail(jf *ast.Justfile, line int, format string, args ...any) *diag.Error {
	return &diag.Error{
		Kind: diag.KindAnalysis,
		File: jf.File,
		Line: line,
		Msg:  fmt.Sprintf(format, args...),
	}
}

// duplicateRecipe reports the first recipe whose name an earlier recipe
// already uses. It is pure.
func duplicateRecipe(jf *ast.Justfile) *diag.Error {
	first := make(map[string]int, len(jf.Recipes))
	for _, r := range jf.Recipes {
		if line, ok := first[r.Name]; ok {
			return fail(jf, r.Line, "recipe `%s` first defined on line %d is redefined on line %d", r.Name, line, r.Line)
		}
		first[r.Name] = r.Line
	}
	return nil
}

// duplicateAssignment reports the first assignment whose name an earlier
// assignment already uses. It is pure.
func duplicateAssignment(jf *ast.Justfile) *diag.Error {
	seen := make(map[string]struct{}, len(jf.Assignments))
	for _, a := range jf.Assignments {
		if _, ok := seen[a.Name]; ok {
			return fail(jf, a.Line, "variable `%s` has multiple definitions", a.Name)
		}
		seen[a.Name] = struct{}{}
	}
	return nil
}

// undefinedVariable reports the first reference to a name that is neither an
// assignment nor a visible parameter. It is pure.
//
// An assignment may use any assignment, wherever it appears, so the globals
// are collected first. A parameter default sees the globals and the
// parameters before it. A body line sees the globals and every parameter.
// A parameter default has no line of its own, so it is reported at the line
// of its recipe.
func undefinedVariable(jf *ast.Justfile) *diag.Error {
	globals := make(map[string]struct{}, len(jf.Assignments))
	for _, a := range jf.Assignments {
		globals[a.Name] = struct{}{}
	}
	for _, a := range jf.Assignments {
		if name, ok := undefinedIn(a.Value, globals, nil); ok {
			return fail(jf, a.Line, "variable `%s` not defined", name)
		}
	}
	for _, r := range jf.Recipes {
		if err := undefinedInRecipe(jf, r, globals); err != nil {
			return err
		}
	}
	return nil
}

// undefinedInRecipe applies undefinedVariable's scoping rules to one recipe.
// It is pure.
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

// unknownDependency reports the first dependency that names no recipe. A
// recipe may depend on one defined later in the file. A dependency has no
// line of its own, so it is reported at the line of its recipe. It is pure.
func unknownDependency(jf *ast.Justfile) *diag.Error {
	known := make(map[string]struct{}, len(jf.Recipes))
	for _, r := range jf.Recipes {
		known[r.Name] = struct{}{}
	}
	for _, r := range jf.Recipes {
		for _, dep := range r.Deps {
			if _, ok := known[dep]; !ok {
				return fail(jf, r.Line, "recipe `%s` has unknown dependency `%s`", r.Name, dep)
			}
		}
	}
	return nil
}
