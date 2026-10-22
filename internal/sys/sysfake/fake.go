// Package sysfake provides in-memory fakes of the sys interfaces for tests.
package sysfake

import (
	"context"
	"fmt"
	"io/fs"

	"gitea.com/wimwian/just-go/internal/sys"
)

// FakeFS is an in-memory sys.FS mapping path to file content.
type FakeFS map[string]string

// ReadFile returns the content at path, or an error wrapping fs.ErrNotExist.
func (f FakeFS) ReadFile(path string) ([]byte, error) {
	content, ok := f[path]
	if !ok {
		return nil, fmt.Errorf("read %s: %w", path, fs.ErrNotExist)
	}
	return []byte(content), nil
}

// IsFile reports whether path is present in the map.
func (f FakeFS) IsFile(path string) (bool, error) {
	_, ok := f[path]
	return ok, nil
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

// Run records spec and returns the next scripted result.
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
