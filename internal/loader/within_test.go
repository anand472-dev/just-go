package loader

import (
	"errors"
	"testing"

	"gitea.com/wimwian/just-go/internal/sys/sysfake"
)

// FindWithin finds the justfile in the start directory or one above it, up to and
// including the ceiling, and no further.
func TestFindWithinStopsAtTheCeiling(t *testing.T) {
	fsys := sysfake.FakeFS{"/p/justfile": "x:\n", "/p/a/b/c/.keep": ""}
	if got, err := FindWithin(fsys, "/p/a/b/c", "/p/a/b"); !errors.Is(err, ErrNoJustfile) || got != "" {
		t.Errorf("FindWithin below the justfile = %q, %v; want none within the ceiling", got, err)
	}
	if got, err := FindWithin(fsys, "/p/a/b/c", "/p"); err != nil || got != "/p/justfile" {
		t.Errorf("FindWithin to the ceiling = %q, %v; want /p/justfile", got, err)
	}
}

func TestFindWithinFindsAJustfileInTheStartDirectory(t *testing.T) {
	fsys := sysfake.FakeFS{"/p/justfile": "x:\n"}
	if got, err := FindWithin(fsys, "/p", "/p"); err != nil || got != "/p/justfile" {
		t.Errorf("FindWithin = %q, %v", got, err)
	}
}

// A ceiling that is not above the start is no limit: the search reaches the root.
func TestFindWithinWithANonAncestorCeilingSearchesToTheRoot(t *testing.T) {
	fsys := sysfake.FakeFS{"/justfile": "x:\n"}
	if got, err := FindWithin(fsys, "/a/b", "/elsewhere"); err != nil || got != "/justfile" {
		t.Errorf("FindWithin = %q, %v; want the root justfile", got, err)
	}
	if _, err := FindWithin(sysfake.FakeFS{}, "/a", "/nowhere"); !errors.Is(err, ErrNoJustfile) {
		t.Errorf("FindWithin with no justfile = %v", err)
	}
}
