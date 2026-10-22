// Package analyzer checks a parsed justfile for semantic errors that upstream
// reports before anything runs: duplicate recipe and variable names, uses of
// undefined variables, circular variable definitions, and dependencies that
// name no recipe or that form a cycle.
//
// The package is pure: every function reads the syntax tree it is given and
// returns a value. None performs I/O, none mutates its input, and there is no
// package-level state.
package analyzer

import (
	"fmt"
	"sort"
	"strings"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
)

// Analyze checks jf and returns the first error it finds as a *diag.Error of
// KindAnalysis, or nil. It is pure.
//
// The checks run in upstream's order, so that a file with several errors
// reports the one upstream reports: duplicate assignment names; then the
// assignments, resolved in name order, each after the assignments it uses
// (undefined and circular variables); then duplicate recipe names; then the
// recipes, resolved in name order, each after the recipes it depends on (its
// dependencies, then its parameter defaults, then its body). The first error
// decides the result. Each error carries the line of the offending item. The
// syntax tree records no columns, so Col is 0, meaning unknown.
func Analyze(jf *ast.Justfile) error {
	if err := duplicateAssignment(jf); err != nil {
		return err
	}
	if err := resolveAssignments(jf); err != nil {
		return err
	}
	if err := duplicateRecipe(jf); err != nil {
		return err
	}
	if err := resolveRecipes(jf); err != nil {
		return err
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
