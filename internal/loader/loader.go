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
func names() []string {
	return []string{"justfile", ".justfile"}
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

// matching returns the entries whose name equals a justfile name ignoring
// ASCII case, in the order given. Pure: it returns a fresh slice.
func matching(entries []string) []string {
	var found []string
	for _, entry := range entries {
		for _, name := range names() {
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
	dir := filepath.Clean(startDir)
	for {
		if path, err := findIn(fsys, dir); err != nil || path != "" {
			return path, err
		}
		up, ok := parent(dir)
		if !ok {
			return "", ErrNoJustfile
		}
		dir = up
	}
}

// findIn returns the path of the justfile in dir, or "" when there is none,
// or a *MultipleCandidatesError when there are several. Impure: it reads
// fsys.
func findIn(fsys sys.FS, dir string) (string, error) {
	entries, err := fsys.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("search for justfile: %w", err)
	}
	var files []string
	for _, entry := range matching(entries) {
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
	src, err := fsys.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read justfile: %w", err)
	}
	jf, err := parser.Parse(path, src)
	if err != nil {
		return nil, diag.WithSource(err, src)
	}
	if err := analyzer.Analyze(jf); err != nil {
		return nil, diag.WithSource(err, src)
	}
	jf.Source = src
	return jf, nil
}
