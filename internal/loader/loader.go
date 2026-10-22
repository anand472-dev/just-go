// Package loader finds a justfile on disk and turns it into a checked syntax
// tree. Find and Load are impure: they touch the filesystem only through the
// sys.FS they are given, so tests run against sysfake. The name list and the
// path logic are pure helpers.
package loader

import (
	"errors"
	"fmt"
	"path/filepath"

	"gitea.com/wimwian/just-go/internal/analyzer"
	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/parser"
	"gitea.com/wimwian/just-go/internal/sys"
)

// ErrNoJustfile is returned by Find when no directory from the start up to
// the filesystem root holds a justfile. The command line reports it as
// "error: no justfile found".
var ErrNoJustfile = errors.New("no justfile found")

// names returns the file names Find looks for in each directory, in
// priority order. Pure: it returns a fresh slice, so callers cannot disturb
// later calls.
func names() []string {
	return []string{"justfile", ".justfile"}
}

// parent returns the directory above dir, and false when dir is already the
// filesystem root (or ".", the root of a relative path). Pure.
func parent(dir string) (string, bool) {
	up := filepath.Dir(dir)
	return up, up != dir
}

// candidates returns the paths to try in dir, in priority order. Pure.
func candidates(dir string) []string {
	ns := names()
	paths := make([]string, len(ns))
	for i, n := range ns {
		paths[i] = filepath.Join(dir, n)
	}
	return paths
}

// Find searches startDir, then each directory above it up to the filesystem
// root, for a file named "justfile" and then ".justfile". It returns the path
// of the first regular file it finds, or ErrNoJustfile. A filesystem failure
// is returned wrapped, and is not ErrNoJustfile. Impure: it reads fsys.
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

// findIn returns the path of the first candidate in dir that is a regular
// file, or "" when there is none. Impure: it reads fsys.
func findIn(fsys sys.FS, dir string) (string, error) {
	for _, path := range candidates(dir) {
		ok, err := fsys.IsFile(path)
		if err != nil {
			return "", fmt.Errorf("search for justfile: %w", err)
		}
		if ok {
			return path, nil
		}
	}
	return "", nil
}

// Load reads the justfile at path, parses it with path as the file name, and
// analyzes it. It returns the first error: a read failure (wrapped), or a
// *diag.Error from parsing or analysis whose File is path. Impure: it reads
// fsys.
func Load(fsys sys.FS, path string) (*ast.Justfile, error) {
	src, err := fsys.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read justfile: %w", err)
	}
	jf, err := parser.Parse(path, src)
	if err != nil {
		return nil, err
	}
	if err := analyzer.Analyze(jf); err != nil {
		return nil, err
	}
	return jf, nil
}
