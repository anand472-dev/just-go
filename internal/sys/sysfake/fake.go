// Package sysfake provides in-memory fakes of the sys interfaces for tests.
package sysfake

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"

	"gitea.com/wimwian/just-go/internal/sys"
)

// FakeFS is an in-memory sys.FS mapping path to file content.
type FakeFS map[string]string

// ReadFile returns the content at path, or an error wrapping fs.ErrNotExist.
// Pure: it reads only the in-memory map.
func (f FakeFS) ReadFile(path string) ([]byte, error) {
	content, ok := f[path]
	if !ok {
		return nil, fmt.Errorf("read %s: %w", path, fs.ErrNotExist)
	}
	return []byte(content), nil
}

// IsFile reports whether path is present in the map. Pure: it reads only the
// in-memory map.
func (f FakeFS) IsFile(path string) (bool, error) {
	_, ok := f[path]
	return ok, nil
}

// ReadDir returns the sorted base names of the files whose parent directory
// is path. The fake has no directory entries of its own, so a directory that
// holds no files lists as empty rather than as an error. Pure: it reads only
// the in-memory map.
func (f FakeFS) ReadDir(path string) ([]string, error) {
	dir := filepath.Clean(path)
	var names []string
	for p := range f {
		if filepath.Dir(p) == dir {
			names = append(names, filepath.Base(p))
		}
	}
	sort.Strings(names)
	return names, nil
}

// FakeProcess is a sys.Process that runs nothing. It records every spec and
// answers with scripted results.
type FakeProcess struct {
	// Codes are the exit codes returned by successive calls; once exhausted,
	// calls return 0.
	Codes []int
	// Err, when non-nil, is returned by every call (with exit code -1).
	Err error
	// Specs records each ProcessSpec passed to Run, in order.
	Specs []sys.ProcessSpec
}

// Run records spec and returns the next scripted result. Pure: it runs no
// process and mutates only the fake's own records.
func (p *FakeProcess) Run(_ context.Context, spec sys.ProcessSpec) (int, error) {
	p.Specs = append(p.Specs, spec)
	if p.Err != nil {
		return -1, p.Err
	}
	call := len(p.Specs) - 1
	if call < len(p.Codes) {
		return p.Codes[call], nil
	}
	return 0, nil
}
