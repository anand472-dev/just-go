package builtins

import (
	"errors"
	"testing"

	"gitea.com/wimwian/just-go/internal/sys/sysfake"
)

// A builtin that reads a file refuses it when the Host's strict path check does,
// and reads it when the check passes.
func TestReadAndCanonicalizeApplyTheStrictPathCheck(t *testing.T) {
	refused := errors.New("outside the project")
	files := sysfake.FakeFS{"/w/f": "text"}
	h := host(files)
	h.CheckPath = func(string) error { return refused }
	if _, err := hostCall(t, h, "read", "f"); !errors.Is(err, refused) {
		t.Errorf("read under a refusing check = %v; want the refusal", err)
	}
	if _, err := hostCall(t, h, "canonicalize", "f"); !errors.Is(err, refused) {
		t.Errorf("canonicalize under a refusing check = %v; want the refusal", err)
	}
	h.CheckPath = func(string) error { return nil }
	if got, err := hostCall(t, h, "read", "f"); err != nil || got != "text" {
		t.Errorf("read under a passing check = %q, %v", got, err)
	}
}
