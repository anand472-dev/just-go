// Package sysfake provides in-memory fakes of the sys interfaces for tests.
package sysfake

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"sync"

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

// WriteFile stores data at path. The permission is not kept. Pure: it writes
// only the in-memory map.
func (f FakeFS) WriteFile(path string, data []byte, _ fs.FileMode) error {
	f[path] = string(data)
	return nil
}

// MkdirTemp returns the path of a directory in dir, "/tmp" for an empty dir,
// called prefix and then "fake". The fake has no directory entries, so every
// call returns the same name, and a directory exists only through the files
// written below it. Pure: it reads and writes nothing.
func (f FakeFS) MkdirTemp(dir, prefix string) (string, error) {
	if dir == "" {
		dir = "/tmp"
	}
	return filepath.Join(dir, prefix+"fake"), nil
}

// RemoveAll removes the file at path and every file below it. Pure: it writes
// only the in-memory map.
func (f FakeFS) RemoveAll(path string) error {
	below := strings.TrimSuffix(path, "/") + "/"
	for p := range f {
		if p == path || strings.HasPrefix(p, below) {
			delete(f, p)
		}
	}
	return nil
}

// Exists reports whether path is a file in the map or a directory above one.
// Pure: it reads only the in-memory map.
func (f FakeFS) Exists(path string) bool {
	_, err := f.Canonicalize(path)
	return err == nil
}

// Canonicalize returns path cleaned, if it is a file in the map or a directory
// above one, and an error wrapping fs.ErrNotExist if it is not. The fake has
// no symbolic links and no working directory, so a relative path stays
// relative. Pure: it reads only the in-memory map.
func (f FakeFS) Canonicalize(path string) (string, error) {
	clean := filepath.Clean(path)
	below := strings.TrimSuffix(clean, "/") + "/"
	for p := range f {
		if filepath.Clean(p) == clean || strings.HasPrefix(p, below) {
			return clean, nil
		}
	}
	return "", fmt.Errorf("canonicalize %s: %w", path, fs.ErrNotExist)
}

// FakeProcess is a sys.Process that runs nothing. It records every spec and
// answers with scripted results.
type FakeProcess struct {
	// Codes are the exit codes returned by successive calls; once exhausted,
	// calls return 0.
	Codes []int
	// Err, when non-nil, is returned by every call (with exit code -1).
	Err error
	// Errs are errors returned by successive calls together with the matching
	// entry of Codes; a nil or missing entry means none.
	Errs []error
	// Outputs are what successive calls write to the spec's Stdout, when it
	// has one; a call past the end of the list writes nothing.
	Outputs []string
	// Specs records each ProcessSpec passed to Run, in order.
	Specs []sys.ProcessSpec

	mu sync.Mutex
}

// Run records spec and returns the next scripted result. Pure: it runs no
// process and mutates only the fake's own records. It is safe for concurrent
// use, so a test can run recipes in parallel.
func (p *FakeProcess) Run(_ context.Context, spec sys.ProcessSpec) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Specs = append(p.Specs, spec)
	if p.Err != nil {
		return -1, p.Err
	}
	call := len(p.Specs) - 1
	if call < len(p.Outputs) && spec.Stdout != nil {
		_, _ = io.WriteString(spec.Stdout, p.Outputs[call])
	}
	code := 0
	if call < len(p.Codes) {
		code = p.Codes[call]
	}
	if call < len(p.Errs) {
		return code, p.Errs[call]
	}
	return code, nil
}

// FakeClock is a sys.Clock that always reports the same time, so that a test
// can pin the output of anything that prints it.
type FakeClock struct {
	At sys.Time
}

// Now returns the fixed time. Pure: it reads only the fake's own field.
func (c FakeClock) Now() sys.Time {
	return c.At
}
