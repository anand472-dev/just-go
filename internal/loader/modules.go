package loader

import (
	"fmt"
	"path/filepath"
	"sort"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/sys"
)

// A module is merged into the justfile that declares it under its name: its
// recipes and assignments become `name::recipe` and `name::variable`, and every
// reference inside it is renamed to match. The analyzer and runner then see one
// namespace, and a path like `foo::bar` is an ordinary name. A variable that a
// module does not define is still renamed, so it is undefined there, as it is
// upstream.

// expandModules loads and merges the modules that jf declares. file is the file
// jf was parsed from, the directory of which the modules are found in, and chain
// the files that import or declare it, from the root down.
func expandModules(e *loadEnv, jf *ast.Justfile, src []byte, chain []string) error {
	for _, m := range jf.Modules {
		if err := expandModule(e, jf, src, chain, m); err != nil {
			return err
		}
	}
	return nil
}

// expandModule loads the module m that jf declares, merges it under its name,
// and returns the error if it cannot be loaded. chain is the files above jf.
func expandModule(e *loadEnv, jf *ast.Justfile, src []byte, chain []string, m ast.Module) error {
	fsys := e.fsys
	path, found, err := findModule(e, jf.File, m)
	if err != nil {
		return err
	}
	if !found {
		if m.Optional {
			jf.AbsentModules = append(jf.AbsentModules, m.Name)
			return nil
		}
		return diag.WithSource(missingModule(jf.File, m), src)
	}
	if err := e.check(path); err != nil {
		return diag.WithSource(moduleOutside(jf.File, m, err), src)
	}
	next := append(append([]string(nil), chain...), jf.File)
	if contains(next, path) {
		return diag.WithSource(circularImport(jf.File, path), src)
	}
	sub, err := readImport(fsys, path)
	if err != nil {
		return err
	}
	if err := expandImports(e, sub.jf, sub.src); err != nil {
		return err
	}
	if err := expandModules(e, sub.jf, sub.src, next); err != nil {
		return err
	}
	mergeModule(jf, m, sub.jf)
	return nil
}

// mergeModule merges the module mod, which m declares, into jf under its name, with
// the module's own privacy applied to its recipes. It is pure apart from updating jf.
func mergeModule(jf *ast.Justfile, m ast.Module, mod *ast.Justfile) {
	if m.Private {
		hidePrivate(mod, m)
	}
	flattenModule(jf, m.Name+"::", mod)
}

// hidePrivate gives each recipe of mod the [private] attribute, as the module's
// own attribute makes them: a private module's recipes are not listed or chosen.
// It is pure apart from updating mod.
func hidePrivate(mod *ast.Justfile, m ast.Module) {
	for i := range mod.Recipes {
		mod.Recipes[i].Attributes = append(mod.Recipes[i].Attributes, ast.Attribute{Name: ast.AttrPrivate, Line: m.Line, Col: m.Col})
	}
}

// findModule is the file a module names, as upstream searches for it. With a
// path, the file at that path, else the candidates beneath it: `mod.just` and
// the justfile names in that directory. Without one, `name.just`, `name/mod.just`
// and the justfile names in `name/`. The justfile names match in any case. More
// than one file is an error. found is false when none is there.
func findModule(e *loadEnv, file string, m ast.Module) (string, bool, error) {
	fsys, home := e.fsys, e.home
	dir := filepath.Dir(file)
	var exact []string
	var dirs []string
	if m.Path != "" {
		path := importPath(file, m.Path, home)
		ok, err := fsys.IsFile(path)
		if err != nil || ok {
			return path, ok, err
		}
		dirs = []string{path}
		exact = []string{filepath.Join(path, "mod.just")}
	} else {
		exact = []string{filepath.Join(dir, m.Name+".just"), filepath.Join(dir, m.Name, "mod.just")}
		dirs = []string{filepath.Join(dir, m.Name)}
	}
	found, err := moduleCandidates(fsys, dir, exact, dirs)
	if err != nil {
		return "", false, err
	}
	switch len(found) {
	case 0:
		return "", false, nil
	case 1:
		return found[0], true, nil
	}
	return "", false, &diag.Error{
		Kind: diag.KindAnalysis, File: file, Line: m.Line, Col: m.Col, Width: m.Width,
		Msg: fmt.Sprintf("found multiple source files for module `%s`: %s", m.Name, andTicked(relativeTo(dir, found))),
	}
}

// moduleCandidates lists the files a module may be in: each exact path that is a
// file, and each justfile name, in any case, inside each of dirs. The result is
// sorted.
func moduleCandidates(fsys sys.FS, dir string, exact, dirs []string) ([]string, error) {
	var found []string
	for _, p := range exact {
		ok, err := fsys.IsFile(p)
		if err != nil {
			return nil, err
		}
		if ok {
			found = append(found, p)
		}
	}
	for _, d := range dirs {
		entries, err := fsys.ReadDir(d)
		if err != nil {
			continue
		}
		for _, e := range entries {
			for _, n := range names() {
				if equalFoldASCII(e, n) {
					found = append(found, filepath.Join(d, e))
				}
			}
		}
	}
	sort.Strings(found)
	return found, nil
}

// relativeTo writes each path relative to dir, where it is inside it. It is pure.
func relativeTo(dir string, paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		if rel, err := filepath.Rel(dir, p); err == nil {
			out[i] = rel
		} else {
			out[i] = p
		}
	}
	return out
}

// moduleOutside is the strict path rule's error for a module, at its name.
func moduleOutside(file string, m ast.Module, err error) *diag.Error {
	return &diag.Error{
		Kind: diag.KindAnalysis, File: file, Line: m.Line, Col: m.Col, Width: m.Width,
		Msg: err.Error(),
	}
}

// missingModule is the error for a required module whose file is not there, at
// its name.
func missingModule(file string, m ast.Module) *diag.Error {
	return &diag.Error{
		Kind: diag.KindAnalysis, File: file, Line: m.Line, Col: m.Col, Width: m.Width,
		Msg: fmt.Sprintf("could not find source file for module `%s`", m.Name),
	}
}

// flattenModule merges the items of mod into root, each name given the prefix:
// the module's own recipes and assignments, renamed, with their references.
// Settings are not merged. It is pure apart from updating root.
func flattenModule(root *ast.Justfile, prefix string, mod *ast.Justfile) {
	mod.IsModule = true
	for _, a := range mod.Assignments {
		a.Name = prefix + a.Name
		a.Value = renameExpr(a.Value, prefix, nil)
		a.Unit = mod
		root.Assignments = append(root.Assignments, a)
	}
	for _, r := range mod.Recipes {
		root.Recipes = append(root.Recipes, renameRecipe(r, prefix, mod))
	}
	for _, absent := range mod.AbsentModules {
		root.AbsentModules = append(root.AbsentModules, prefix+absent)
	}
	root.Unstable = append(root.Unstable, mod.Unstable...)
	root.ListUses = append(root.ListUses, mod.ListUses...)
}

// renameRecipe is r with its name, dependencies, parameter defaults and body
// renamed into the module's namespace. Parameters are local, so references to
// them are left as they are.
func renameRecipe(r ast.Recipe, prefix string, mod *ast.Justfile) ast.Recipe {
	params := map[string]bool{}
	for _, p := range r.Params {
		params[p.Name] = true
	}
	r.Name = prefix + r.Name
	r.Unit = mod
	r.Deps = renameDeps(r.Deps, prefix, params)
	r.Subsequents = renameDeps(r.Subsequents, prefix, params)
	for i := range r.Params {
		if r.Params[i].Default != nil {
			r.Params[i].Default = renameExpr(r.Params[i].Default, prefix, params)
		}
	}
	lines := make([]ast.Line, len(r.Lines))
	for i, l := range r.Lines {
		parts := make([]ast.Expr, len(l.Parts))
		for j, e := range l.Parts {
			parts[j] = renameExpr(e, prefix, params)
		}
		l.Parts = parts
		lines[i] = l
	}
	r.Lines = lines
	return r
}

// renameDeps renames each dependency, and the arguments it is given.
func renameDeps(deps []ast.Dependency, prefix string, locals map[string]bool) []ast.Dependency {
	out := make([]ast.Dependency, len(deps))
	for i, d := range deps {
		d.Name = prefix + d.Name
		if d.Args != nil {
			args := make([]ast.Expr, len(d.Args))
			for j, a := range d.Args {
				args[j] = renameExpr(a, prefix, locals)
			}
			d.Args = args
		}
		out[i] = d
	}
	return out
}

// renameExpr is e with every variable it names given the prefix, except the
// locals, which are parameters of the recipe the expression is in. Other nodes
// are copied as they are.
func renameExpr(e ast.Expr, prefix string, locals map[string]bool) ast.Expr {
	switch x := e.(type) {
	case ast.Var:
		if !locals[x.Name] {
			x.Name = prefix + x.Name
		}
		return x
	case ast.Concat:
		return ast.Concat{Left: renameExpr(x.Left, prefix, locals), Right: renameExpr(x.Right, prefix, locals)}
	case ast.Comparison:
		x.Left = renameExpr(x.Left, prefix, locals)
		x.Right = renameExpr(x.Right, prefix, locals)
		return x
	case ast.Conditional:
		return ast.Conditional{
			Cond: renameExpr(x.Cond, prefix, locals),
			Then: renameExpr(x.Then, prefix, locals),
			Else: renameExpr(x.Else, prefix, locals),
		}
	}
	return renameCompound(e, prefix, locals)
}

// renameCompound is renameExpr for the expressions that hold a list of others: a
// call's arguments, a list's elements, and an assertion's parts.
func renameCompound(e ast.Expr, prefix string, locals map[string]bool) ast.Expr {
	switch x := e.(type) {
	case ast.Call:
		x.Args = renameAll(x.Args, prefix, locals)
		return x
	case ast.List:
		x.Elements = renameAll(x.Elements, prefix, locals)
		return x
	case ast.Assert:
		x.Cond = renameExpr(x.Cond, prefix, locals)
		if x.Message != nil {
			x.Message = renameExpr(x.Message, prefix, locals)
		}
		return x
	}
	return e
}

// renameAll renames each expression of list, into a new slice.
func renameAll(list []ast.Expr, prefix string, locals map[string]bool) []ast.Expr {
	out := make([]ast.Expr, len(list))
	for i, a := range list {
		out[i] = renameExpr(a, prefix, locals)
	}
	return out
}

// checkModuleNames reports a module name that is declared twice, or that a recipe
// of the root file also has, at whichever of the two comes second in the file.
// It is pure.
func checkModuleNames(jf *ast.Justfile) *diag.Error {
	for i, m := range jf.Modules {
		for _, earlier := range jf.Modules[:i] {
			if earlier.Name == m.Name {
				return moduleError(jf.File, m.Line, m.Col, m.Width,
					"module `%s` first defined on line %d is redefined on line %d", m.Name, earlier.Line, m.Line)
			}
		}
	}
	for _, r := range jf.Recipes {
		if r.Unit != nil {
			continue
		}
		for _, m := range jf.Modules {
			if m.Name != r.Name {
				continue
			}
			if r.Line > m.Line {
				return moduleError(jf.File, r.Line, r.Col, len(r.Name),
					"module `%s` defined on line %d is redefined as a recipe on line %d", m.Name, m.Line, r.Line)
			}
			return moduleError(jf.File, m.Line, m.Col, m.Width,
				"recipe `%s` defined on line %d is redefined as a module on line %d", r.Name, r.Line, m.Line)
		}
	}
	return nil
}

// moduleError is an analysis error at a position, with a message.
func moduleError(file string, line, col, width int, format string, args ...any) *diag.Error {
	return &diag.Error{
		Kind: diag.KindAnalysis, File: file, Line: line, Col: col, Width: width,
		Msg: fmt.Sprintf(format, args...),
	}
}
