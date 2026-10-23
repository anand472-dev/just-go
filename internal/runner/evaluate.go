package runner

import (
	"context"
	"fmt"
	"runtime"
	"sort"
	"strings"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/settings"
	"gitea.com/wimwian/just-go/internal/suggest"
	"gitea.com/wimwian/just-go/internal/sys"
)

// Binding is an assignment with its value, as evaluated. A module's assignment
// is named as the module lists it, without the module's name.
type Binding struct {
	Name  string
	Value string
}

// Evaluate returns the values of the assignments named, sorted by name. A name
// is an assignment, or a module, whose public assignments are then returned.
// With no name it returns every public assignment of the justfile. A named
// assignment is evaluated with the assignments it uses, as it would be by a
// run, and backticks run as they do for a run. The environment is the plan's,
// with its dotenv variables.
func Evaluate(ctx context.Context, plan Plan, names []string) ([]Binding, error) {
	jf := plan.Justfile
	if err := CheckOverrides(jf, plan.Overrides); err != nil {
		return nil, err
	}
	if err := unknownName(jf, names); err != nil {
		return nil, err
	}
	set, err := settings.Resolve(jf)
	if err != nil {
		return nil, withSource(jf, err)
	}
	fsys, exec := plan.FS, plan.Exec
	if fsys == nil {
		fsys = sys.OSFS{}
	}
	if exec == nil {
		exec = sys.OSProcess{}
	}
	if plan.Env, err = loadDotenv(ctx, plan, fsys, exec, set); err != nil {
		return nil, err
	}
	goos := plan.OS
	if goos == "" {
		goos = runtime.GOOS
	}
	inv := newInvocation(ctx, plan, prepared{set: set}, goos)
	scope, err := inv.evaluateGlobals(evaluateOnly(jf, names))
	if err != nil {
		return nil, withSource(jf, err)
	}
	return bindings(jf, names, scope), nil
}

// unknownName is the error for the first name that Evaluate cannot find. It
// names the missing module when the name is a path into one, and otherwise
// the variable or submodule, each with the name upstream would suggest for it.
// It is pure.
func unknownName(jf *ast.Justfile, names []string) error {
	for _, n := range names {
		if known(jf, n) {
			continue
		}
		module, _, nested := strings.Cut(n, "::")
		if nested && !moduleKnown(jf, module) {
			return unknown(fmt.Sprintf("justfile does not contain submodule `%s`", module), moduleNames(jf), module)
		}
		return unknown(fmt.Sprintf("justfile does not contain variable or submodule `%s`", n), visibleNames(jf), n)
	}
	return nil
}

// unknown is msg, with the suggestion for word among candidates, if there is one.
func unknown(msg string, candidates []string, word string) *diag.Error {
	if suggestion, ok := suggest.Closest(word, candidates); ok {
		msg += fmt.Sprintf("\nDid you mean `%s`?", suggestion)
	}
	return &diag.Error{Kind: diag.KindAnalysis, Msg: msg}
}

// known reports whether n is an assignment of jf, or a module that jf declares
// or holds assignments of. It is pure.
func known(jf *ast.Justfile, n string) bool {
	if moduleKnown(jf, n) {
		return true
	}
	for _, a := range jf.UniqueAssignments() {
		if a.Name == n {
			return true
		}
	}
	return false
}

// moduleKnown reports whether jf declares a module named n, or holds an
// assignment of one. It is pure.
func moduleKnown(jf *ast.Justfile, n string) bool {
	for _, m := range jf.Modules {
		if m.Name == n {
			return true
		}
	}
	for _, a := range jf.UniqueAssignments() {
		if strings.HasPrefix(a.Name, n+"::") {
			return true
		}
	}
	return false
}

// moduleNames is the names of the modules jf declares. It is pure.
func moduleNames(jf *ast.Justfile) []string {
	out := make([]string, len(jf.Modules))
	for i, m := range jf.Modules {
		out[i] = m.Name
	}
	return out
}

// visibleNames is the public variables and modules that a name may be
// suggested from: each variable of the justfile's own level, and each module.
// It is pure.
func visibleNames(jf *ast.Justfile) []string {
	seen := make(map[string]bool)
	var out []string
	add := func(n string) {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	for _, a := range jf.UniqueAssignments() {
		if a.Private {
			continue
		}
		if module, _, nested := strings.Cut(a.Name, "::"); nested {
			add(module)
		} else {
			add(a.Name)
		}
	}
	for _, n := range moduleNames(jf) {
		add(n)
	}
	return out
}

// displayName is the name a is listed under for names, and whether it is
// listed at all. With no names, every public assignment is listed under its own
// name. A name matches a exactly, or a module that holds it: a module's public
// assignments are listed under the names the module gives them. It is pure.
func displayName(a ast.Assignment, names []string) (string, bool) {
	if len(names) == 0 {
		return a.Name, !a.Private
	}
	for _, n := range names {
		if a.Name == n {
			return a.Name, true
		}
		if rest, ok := strings.CutPrefix(a.Name, n+"::"); ok {
			return rest, !a.Private
		}
	}
	return "", false
}

// evaluateOnly is the set of assignments that evaluating names needs: those
// listed for the names, and what they use. It is nil when no name is given,
// which means every assignment. It is pure.
func evaluateOnly(jf *ast.Justfile, names []string) map[string]bool {
	if len(names) == 0 {
		return nil
	}
	var wanted []string
	for _, a := range jf.UniqueAssignments() {
		if _, ok := displayName(a, names); ok {
			wanted = append(wanted, a.Name)
		}
	}
	u := newInUse(jf.UniqueAssignments())
	u.mark(wanted)
	u.follow()
	return u.used
}

// bindings returns the assignments listed for names, sorted by their names,
// with their values from scope. It is pure.
func bindings(jf *ast.Justfile, names []string, scope map[string]string) []Binding {
	var out []Binding
	for _, a := range jf.UniqueAssignments() {
		name, ok := displayName(a, names)
		if !ok {
			continue
		}
		out = append(out, Binding{Name: name, Value: scope[a.Name]})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
