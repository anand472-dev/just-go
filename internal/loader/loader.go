// Package loader finds a justfile on disk and turns it into a checked syntax
// tree. Find and Load are impure: they touch the filesystem only through the
// sys.FS they are given, so tests run against sysfake. The name list and the
// path logic are pure helpers.
package loader

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"gitea.com/wimwian/just-go/internal/analyzer"
	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/parser"
	"gitea.com/wimwian/just-go/internal/sys"
)

// ErrNoJustfile is returned by Find when no directory from the start up to
// the filesystem root holds a justfile. The command line reports it as
// "error: no justfile found".
var ErrNoJustfile = errors.New("no justfile found")

// ErrMultipleCandidates matches, through errors.Is, the error Find returns
// when one directory holds more than one justfile. The error itself is a
// *MultipleCandidatesError that names the directory and the files.
var ErrMultipleCandidates = errors.New("multiple candidate justfiles found")

// MultipleCandidatesError reports that Dir holds several files whose names
// match a justfile name, so the choice between them would be a guess.
// Upstream refuses in the same situation. The command line prints it as
// "error: " followed by Error().
type MultipleCandidatesError struct {
	Dir   string   // the directory that holds the candidates
	Names []string // their base names, sorted by byte order; at least two
}

// Error renders the message upstream prints, for example
// "multiple candidate justfiles found in `/p`: `.justfile` and `justfile`".
// Pure.
func (e *MultipleCandidatesError) Error() string {
	return fmt.Sprintf("multiple candidate justfiles found in `%s`: %s", e.Dir, andTicked(e.Names))
}

// Is reports whether target is ErrMultipleCandidates. Pure.
func (e *MultipleCandidatesError) Is(target error) bool {
	return target == ErrMultipleCandidates
}

// andTicked joins names in backticks the way upstream's List::and_ticked
// does: "`a`", "`a` and `b`", "`a`, `b`, and `c`". Pure.
func andTicked(names []string) string {
	ticked := make([]string, len(names))
	for i, n := range names {
		ticked[i] = "`" + n + "`"
	}
	switch len(ticked) {
	case 0:
		return ""
	case 1:
		return ticked[0]
	case 2:
		return ticked[0] + " and " + ticked[1]
	}
	last := len(ticked) - 1
	return strings.Join(ticked[:last], ", ") + ", and " + ticked[last]
}

// names returns the file names Find looks for in each directory. Names are
// matched ignoring ASCII case. Pure: it returns a fresh slice, so callers
// cannot disturb later calls.
func defaultNames() []string {
	return []string{"justfile", ".justfile"}
}

// names are the justfile names a module's directory may hold, which are the
// default names. Pure.
func names() []string {
	return defaultNames()
}

// parent returns the directory above dir, and false when dir is already the
// filesystem root (or ".", the root of a relative path). Pure.
func parent(dir string) (string, bool) {
	up := filepath.Dir(dir)
	return up, up != dir
}

// equalFoldASCII reports whether a and b are equal when only the ASCII
// letters are case-folded, as upstream's eq_ignore_ascii_case does. Unicode
// folding would match more names than upstream does. Pure.
func equalFoldASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		if lowerASCII(a[i]) != lowerASCII(b[i]) {
			return false
		}
	}
	return true
}

// lowerASCII maps A-Z to a-z and leaves every other byte alone. Pure.
func lowerASCII(c byte) byte {
	if 'A' <= c && c <= 'Z' {
		return c + ('a' - 'A')
	}
	return c
}

// matching returns the entries whose name equals one of the names ignoring
// ASCII case, in the order given. Pure: it returns a fresh slice.
func matching(entries, names []string) []string {
	var found []string
	for _, entry := range entries {
		for _, name := range names {
			if equalFoldASCII(entry, name) {
				found = append(found, entry)
				break
			}
		}
	}
	return found
}

// Find searches startDir, then each directory above it up to the filesystem
// root, for a justfile: a regular file named "justfile" or ".justfile",
// ignoring ASCII case. It returns the path of the first one it finds, or
// ErrNoJustfile. When a directory holds more than one such file, Find
// returns a *MultipleCandidatesError (matching ErrMultipleCandidates) rather
// than choosing. A filesystem failure is returned wrapped, and is not
// ErrNoJustfile. Impure: it reads fsys.
func Find(fsys sys.FS, startDir string) (string, error) {
	return FindNamed(fsys, startDir, "", nil)
}

// FindNamed is Find with the names a justfile may have given: names, or the
// default names when it is nil. A ceiling, when not "", is the directory the search
// does not go above (see FindWithin). Impure: it reads fsys.
func FindNamed(fsys sys.FS, startDir, ceiling string, names []string) (string, error) {
	if names == nil {
		names = defaultNames()
	}
	dir := filepath.Clean(startDir)
	limit := filepath.Clean(ceiling)
	for {
		if path, err := findIn(fsys, dir, names); err != nil || path != "" {
			return path, err
		}
		if ceiling != "" && dir == limit {
			return "", ErrNoJustfile
		}
		up, ok := parent(dir)
		if !ok {
			return "", ErrNoJustfile
		}
		dir = up
	}
}

// FindWithin is Find, but it does not look above ceiling: the search stops once
// ceiling has been examined. A ceiling that is not above startDir is no limit
// beyond the filesystem root. Impure: it reads fsys.
func FindWithin(fsys sys.FS, startDir, ceiling string) (string, error) {
	return FindNamed(fsys, startDir, ceiling, nil)
}

// findIn returns the path of the justfile in dir, or "" when there is none,
// or a *MultipleCandidatesError when there are several. Impure: it reads
// fsys.
func findIn(fsys sys.FS, dir string, names []string) (string, error) {
	entries, err := fsys.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("search for justfile: %w", err)
	}
	var files []string
	for _, entry := range matching(entries, names) {
		ok, err := fsys.IsFile(filepath.Join(dir, entry))
		if err != nil {
			return "", fmt.Errorf("search for justfile: %w", err)
		}
		if ok {
			files = append(files, entry)
		}
	}
	switch len(files) {
	case 0:
		return "", nil
	case 1:
		return filepath.Join(dir, files[0]), nil
	}
	sort.Strings(files)
	return "", &MultipleCandidatesError{Dir: dir, Names: files}
}

// Load reads the justfile at path, parses it with path as the file name, and
// analyzes it. It returns the first error: a read failure (wrapped), or a
// *diag.Error from parsing or analysis whose File is path and whose Source is
// the line it is on. The tree keeps the text it was read from, so that a
// failure while a recipe runs can quote its line. Impure: it reads fsys.
func Load(fsys sys.FS, path string) (*ast.Justfile, error) {
	return LoadWith(fsys, path, Options{})
}

// Options are what loading a justfile takes beyond the file system and the path.
type Options struct {
	// Home is the directory an import beginning with `~/` starts from. With
	// Home empty, such an import is not found.
	Home string
	// AllowOutsidePaths lifts the strict path rule: an import, module or dotenv
	// file may lie outside the root justfile's directory.
	AllowOutsidePaths bool
}

// LoadWith is Load with the options given.
func LoadWith(fsys sys.FS, path string, opts Options) (*ast.Justfile, error) {
	e := &loadEnv{fsys: fsys, home: opts.Home, root: filepath.Dir(path), allow: opts.AllowOutsidePaths}
	src, err := fsys.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read justfile: %w", err)
	}
	jf, err := parser.Parse(path, src)
	if err != nil {
		return nil, diag.WithSource(err, src)
	}
	if err := expandImports(e, jf, src); err != nil {
		return nil, err
	}
	if err := checkModuleNames(jf); err != nil {
		return nil, diag.WithSource(err, src)
	}
	if err := expandModules(e, jf, src, nil); err != nil {
		return nil, err
	}
	if err := analyzer.Analyze(jf); err != nil {
		return nil, diag.WithSource(err, src)
	}
	jf.Source = src
	return jf, nil
}

// pendingImport is an imported file that is waiting to be merged: the file, its
// text, its tree, and the files that import it, from the root down.
type pendingImport struct {
	file   string
	src    []byte
	jf     *ast.Justfile
	parent []string
}

// expandImports merges the files that root imports into root, which is the file
// at src's text. It follows upstream's order: the root's own items come first,
// then each import is taken from a stack, so the last one an item names is
// merged first and the imports of an import come before its siblings. A file
// is merged once. An import that names a file which imports it back, directly
// or through others, is a circular import. An optional import that is missing
// is skipped; a required one that is missing is an error at its string.
func expandImports(e *loadEnv, root *ast.Justfile, src []byte) error {
	seen := map[string]bool{}
	stack := []pendingImport{{file: root.File, src: src, jf: root}}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if cur.jf != root {
			mergeUnit(root, cur.jf)
		}
		// Pushed last to first, so that the imports are merged in the order
		// they are written: upstream lists them so with --unsorted.
		for i := len(cur.jf.Imports) - 1; i >= 0; i-- {
			next, found, err := nextImport(e, seen, cur, cur.jf.Imports[i])
			if err != nil {
				return err
			}
			if found {
				stack = append(stack, next)
			}
		}
	}
	return nil
}

// nextImport checks one import of cur and returns the file it names, ready to
// merge, when it is a file that is not merged yet. found is false for an
// optional import that is missing and for a file already merged.
func nextImport(e *loadEnv, seen map[string]bool, cur pendingImport, imp ast.Import) (pendingImport, bool, error) {
	fsys := e.fsys
	path := importPath(cur.file, imp.Path, e.home)
	ok, err := fsys.IsFile(path)
	if err != nil {
		return pendingImport{}, false, fmt.Errorf("import %s: %w", path, err)
	}
	if !ok {
		if imp.Optional {
			return pendingImport{}, false, nil
		}
		return pendingImport{}, false, diag.WithSource(missingImport(cur.file, imp), cur.src)
	}
	if err := e.check(path); err != nil {
		return pendingImport{}, false, diag.WithSource(outsideError(cur.file, imp, err), cur.src)
	}
	chain := append(append([]string(nil), cur.parent...), cur.file)
	if contains(chain, path) {
		return pendingImport{}, false, diag.WithSource(circularImport(cur.file, path), cur.src)
	}
	if seen[path] {
		return pendingImport{}, false, nil
	}
	seen[path] = true
	next, err := readImport(fsys, path)
	if err != nil {
		return pendingImport{}, false, err
	}
	if err := expandModules(e, next.jf, next.src, chain); err != nil {
		return pendingImport{}, false, err
	}
	next.parent = chain
	return next, true, nil
}

// importPath is the file an import names: relative to the directory of the
// importing file, or, when it begins with `~/`, to the home directory. It is
// pure.
func importPath(importer, name, home string) string {
	if rest, ok := strings.CutPrefix(name, "~/"); ok {
		return filepath.Join(home, rest)
	}
	if filepath.IsAbs(name) {
		return filepath.Clean(name)
	}
	return filepath.Join(filepath.Dir(importer), name)
}

// readImport reads and parses the imported file at path.
func readImport(fsys sys.FS, path string) (pendingImport, error) {
	src, err := fsys.ReadFile(path)
	if err != nil {
		return pendingImport{}, fmt.Errorf("read import: %w", err)
	}
	jf, err := parser.Parse(path, src)
	if err != nil {
		return pendingImport{}, diag.WithSource(err, src)
	}
	return pendingImport{file: path, src: src, jf: jf}, nil
}

// mergeUnit appends the items of an imported file to root, marking each with
// the file it came from. Settings and unstable features are merged as well.
func mergeUnit(root, unit *ast.Justfile) {
	for _, a := range unit.Assignments {
		if root.Settings.AllowDuplicateVariables && hasAssignment(root, a.Name) {
			continue
		}
		a.Unit = unit
		root.Assignments = append(root.Assignments, a)
	}
	for _, r := range unit.Recipes {
		if root.Settings.AllowDuplicateRecipes && hasRecipe(root, r.Name) {
			continue
		}
		r.Unit = unit
		root.Recipes = append(root.Recipes, r)
	}
	root.Unstable = append(root.Unstable, unit.Unstable...)
	root.ListUses = append(root.ListUses, unit.ListUses...)
}

// hasAssignment reports whether root holds an assignment named name. It is
// pure.
func hasAssignment(root *ast.Justfile, name string) bool {
	for _, a := range root.Assignments {
		if a.Name == name {
			return true
		}
	}
	return false
}

// hasRecipe reports whether root holds a recipe named name. It is pure.
func hasRecipe(root *ast.Justfile, name string) bool {
	for _, r := range root.Recipes {
		if r.Name == name {
			return true
		}
	}
	return false
}

// missingImport is the error for a required import whose file is not there, at
// the string that names it.
func missingImport(file string, imp ast.Import) *diag.Error {
	return &diag.Error{
		Kind: diag.KindAnalysis, File: file, Line: imp.Line, Col: imp.Col, Width: imp.Width,
		Msg: "could not find source file for import",
	}
}

// outsideError is the strict path rule's error for an import, at its string.
func outsideError(file string, imp ast.Import, err error) *diag.Error {
	return &diag.Error{
		Kind: diag.KindAnalysis, File: file, Line: imp.Line, Col: imp.Col, Width: imp.Width,
		Msg: err.Error(),
	}
}

// circularImport is the error for an import of a file that is already being
// merged, which upstream words with the file that imports it. Upstream gives
// it no location, so it has none.
func circularImport(file, path string) *diag.Error {
	return &diag.Error{
		Kind: diag.KindAnalysis,
		Msg:  fmt.Sprintf("import `%s` in `%s` is circular", path, file),
	}
}

// contains reports whether s is one of list.
func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
