// Package analyzer checks a parsed justfile for semantic errors that upstream
// reports before anything runs: settings set twice or together that cannot be,
// settings that cannot be evaluated, misordered parameters, duplicate
// parameter, recipe and variable names, uses of the features of `set lists` in
// a file that does not set it, recipe lines with extra leading whitespace,
// uses of undefined variables, calls of undefined functions or with the wrong
// number of arguments, circular variable definitions, and dependencies that
// name no recipe, that form a cycle, or that are given too few or too many
// arguments, expressions in attributes (`[confirm]`, `[doc]`, `[env]`,
// `[timestamp]` and `[working-directory]`) that cannot be evaluated, and a
// second recipe with `[default]`.
//
// The package is pure: every function reads the syntax tree it is given and
// returns a value. None performs I/O, none mutates its input, and there is no
// package-level state.
package analyzer

import (
	"fmt"
	"gitea.com/wimwian/just-go/internal/argspec"
	"sort"
	"strings"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
)

// Analyze checks jf and returns the first error it finds as a *diag.Error of
// KindAnalysis, or nil. It is pure.
//
// The checks run in upstream's order, so that a file with several errors
// reports the one upstream reports: in source order, settings set twice or
// set together that cannot be, and duplicate parameters and a parameter
// without a default after one with a default, recipe by recipe;
// then duplicate assignment names (unless allow-duplicate-variables is set);
// then the assignments, resolved in name
// order, each after the assignments it uses (undefined and circular
// variables, undefined functions and argument counts, each call before its
// arguments); then the expression settings, evaluated in a const context;
// then the first use of a feature of `set lists`, unless the file sets it;
// then, recipe by recipe in source order, a duplicate name (unless
// allow-duplicate-recipes is set), and, for a recipe that runs line by line
// through the shell rather than as a script, guard and infallible sigils
// together, lines with extra leading whitespace, and an attribute only a
// script can have; then the recipes, resolved in name
// order, each after the recipes it depends on (its dependencies, then its
// parameter defaults, then the arguments of its dependencies, then its body,
// then the argument counts of its dependencies). The first error decides the
// result. Each error is located where upstream locates it: at the name,
// dependency, variable or text it is about, with Width covering it.
func Analyze(jf *ast.Justfile) error {
	return AnalyzeWith(jf, nil)
}

// AnalyzeWith is Analyze with the --set overrides given, which the settings
// see in place of the variables they name.
func AnalyzeWith(jf *ast.Justfile, overrides map[string]string) error {
	if err := checkItems(jf); err != nil {
		return err
	}
	if err := duplicateAssignment(jf); err != nil {
		return err
	}
	if err := resolveAssignments(jf); err != nil {
		return err
	}
	if err := resolveSettings(jf, overrides); err != nil {
		return err
	}
	if err := checkListFeatures(jf); err != nil {
		return err
	}
	if err := checkRecipeItems(jf); err != nil {
		return err
	}
	if err := resolveRecipes(jf); err != nil {
		return err
	}
	if err := duplicateDefault(jf); err != nil {
		return err
	}
	return nil
}

// fail builds an analysis error for jf at span. It is pure.
func fail(jf *ast.Justfile, span ast.Span, format string, args ...any) *diag.Error {
	return &diag.Error{
		Kind:  diag.KindAnalysis,
		File:  jf.File,
		Line:  span.Line,
		Col:   span.Col,
		Width: span.Width,
		Msg:   fmt.Sprintf(format, args...),
	}
}

// name is the span of a name that starts at line and col. Names are ASCII,
// so their width is their length. It is pure.
func name(line, col int, name string) ast.Span {
	return ast.Span{Line: line, Col: col, Width: len(name)}
}

// checkItems takes the settings and the recipes in source order, as
// upstream's analyzer takes its items. For a setting it reports a second
// setting of the same name, and one that conflicts with an earlier setting.
// For a recipe it reports the first parameter whose name an earlier parameter
// of the same recipe already uses, or that has no default but follows one that
// has. It is pure.
func checkItems(jf *ast.Justfile) *diag.Error {
	sets := newSettingChecker(jf)
	sites, recipes := jf.Settings.Sites, jf.Recipes
	for len(sites) > 0 || len(recipes) > 0 {
		var err *diag.Error
		if len(sites) > 0 && (len(recipes) == 0 || sites[0].Line < recipes[0].Line) {
			err, sites = sets.check(sites[0]), sites[1:]
		} else {
			err, recipes = parameters(jf, recipes[0]), recipes[1:]
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// parameters checks the parameters of r for checkItems. An option is not a
// positional parameter, so the order of positional parameters ignores it. It is
// pure.
func parameters(jf *ast.Justfile, r ast.Recipe) *diag.Error {
	seen := make(map[string]struct{}, len(r.Params))
	passedDefault, passedVariadic := false, false
	options := optionNames(jf, r)
	for _, p := range r.Params {
		at := name(p.Line, p.Col, p.Name)
		if _, ok := seen[p.Name]; ok {
			return fail(jf, at, "recipe `%s` has duplicate parameter `%s`", r.Name, p.Name)
		}
		seen[p.Name] = struct{}{}
		if options[p.Name] {
			continue
		}
		if passedVariadic {
			return fail(jf, at, "parameter `%s` follows variadic parameter", p.Name)
		}
		if p.Default != nil {
			passedDefault = true
		} else if passedDefault && p.Kind == ast.ParamSingle {
			return fail(jf, at, "non-default parameter `%s` follows default parameter", p.Name)
		}
		passedVariadic = p.Kind != ast.ParamSingle
	}
	return nil
}

// checkRecipes takes the recipes in source order and reports the first that
// redefines an earlier recipe's name, or, if it is not a script, that has a
// line starting with more whitespace than its indentation or an attribute
// only a script can have, as upstream's analyzer does. It is pure.
func checkRecipes(jf *ast.Justfile) *diag.Error {
	first := make(map[string]int, len(jf.Recipes))
	for _, r := range jf.Recipes {
		unit := unitOf(jf, r.Unit)
		if line, ok := first[r.Name]; ok && !allowsDuplicateRecipes(jf, r.Unit) {
			return fail(unit, name(r.Line, r.Col, r.Name),
				"recipe `%s` first defined on line %d is redefined on line %d", r.Name, line, r.Line)
		}
		first[r.Name] = r.Line
		if r.IsScript(jf.Settings) {
			continue
		}
		if err := extraIndent(jf, r); err != nil {
			return err
		}
		if err := scriptAttribute(jf, r); err != nil {
			return err
		}
	}
	return nil
}

// scriptAttribute reports the attribute of r that only a script recipe can
// have, `[extension]`, when r runs line by line through the shell, at the
// recipe's name. It is pure.
func scriptAttribute(jf *ast.Justfile, r ast.Recipe) *diag.Error {
	if r.Attribute(ast.AttrExtension) == nil {
		return nil
	}
	return fail(jf, name(r.Line, r.Col, r.Name),
		"shell recipe `%s` has script recipe attribute `%s`", r.Name, ast.AttrExtension)
}

// extraIndent reports the first command of r that has the guard and the
// infallible sigil together, or whose first line starts with extra leading
// whitespace. A continuation line may. It is pure.
func extraIndent(jf *ast.Justfile, r ast.Recipe) *diag.Error {
	for _, l := range r.Lines {
		if l.GuardClash != (ast.Span{}) {
			return fail(jf, l.GuardClash, "the guard `?` and infallible `-` sigils may not be used together")
		}
		if l.ExtraIndent != (ast.Span{}) {
			return fail(jf, l.ExtraIndent, "recipe line has extra leading whitespace")
		}
	}
	return nil
}

// duplicateAssignment reports the first assignment whose name an earlier
// assignment already uses, unless the justfile allows duplicate variables.
// It is pure.
func duplicateAssignment(jf *ast.Justfile) *diag.Error {
	seen := make(map[string]struct{}, len(jf.Assignments))
	for _, a := range jf.Assignments {
		if _, ok := seen[a.Name]; ok && !allowsDuplicateVariables(jf, a.Unit) {
			return fail(unitOf(jf, a.Unit), name(a.Line, a.Col, a.Name), "variable `%s` has multiple definitions", a.Name)
		}
		seen[a.Name] = struct{}{}
	}
	return nil
}

// unitOf is the file a definition came from: its unit when it came from an
// imported or module file, else the root. It is pure.
func unitOf(root, unit *ast.Justfile) *ast.Justfile {
	if unit != nil {
		return unit
	}
	return root
}

// allowsDuplicateRecipes reports whether a recipe of the given unit may share its
// name with another: the root's setting, or the setting of the unit it is in.
// It is pure.
func allowsDuplicateRecipes(root, unit *ast.Justfile) bool {
	return root.Settings.AllowDuplicateRecipes || (unit != nil && unit.Settings.AllowDuplicateRecipes)
}

// allowsDuplicateVariables is allowsDuplicateRecipes for assignments. It is pure.
func allowsDuplicateVariables(root, unit *ast.Justfile) bool {
	return root.Settings.AllowDuplicateVariables || (unit != nil && unit.Settings.AllowDuplicateVariables)
}

// sortedNames returns the keys of m in byte order, which is the order of
// upstream's name-keyed tables. It is pure.
func sortedNames[V any](m map[string]V) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// circle formats the names of a dependency cycle for a message. It is pure.
func circle(names []string) string {
	return strings.Join(names, " -> ")
}

// cycleFrom returns the part of stack that starts at its last occurrence of
// name, followed by name again: upstream's circle. It is pure.
func cycleFrom(stack []string, name string) []string {
	start := 0
	for i, s := range stack {
		if s == name {
			start = i
			break
		}
	}
	out := append([]string(nil), stack[start:]...)
	return append(out, name)
}

// contains reports whether names has name. It is pure.
func contains(names []string, name string) bool {
	for _, s := range names {
		if s == name {
			return true
		}
	}
	return false
}

// checkRecipeItems checks the recipes' own items: their attributes, parameters and
// dependencies, then the [arg] attributes that name their parameters.
func checkRecipeItems(jf *ast.Justfile) error {
	if err := checkRecipes(jf); err != nil {
		return err
	}
	return checkArgSpecs(jf)
}

// optionNames are the names of the parameters of r that are options. The [arg]
// attributes are checked on their own, so an attribute that cannot be read gives
// none. It is pure.
func optionNames(jf *ast.Justfile, r ast.Recipe) map[string]bool {
	specs, _ := argspec.Of(jf, &r)
	out := make(map[string]bool, len(specs))
	for _, s := range specs {
		if s.IsOption() {
			out[s.Param] = true
		}
	}
	return out
}

// checkArgSpecs checks the [arg] attributes of every recipe, as upstream does once
// the recipes are resolved. The first error is returned.
func checkArgSpecs(jf *ast.Justfile) error {
	for i := range jf.Recipes {
		if _, err := argspec.Of(jf, &jf.Recipes[i]); err != nil {
			return err
		}
	}
	return nil
}
