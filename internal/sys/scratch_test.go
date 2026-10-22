package sys

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// skipOnWindows skips t on Windows, where these tests' unix paths (such as
// /run/user/1) are not absolute.
func skipOnWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("assumes unix paths")
	}
}

func TestRuntimeRoot(t *testing.T) {
	skipOnWindows(t)
	for _, tc := range []struct {
		name, goos, xdg, want string
	}{
		{"linux with a runtime directory", "linux", "/run/user/1", "/run/user/1/just"},
		{"linux without one", "linux", "", ""},
		{"linux with a relative one", "linux", "run/user", ""},
		{"macOS ignores it", "darwin", "/run/user/1", ""},
		{"windows ignores it", "windows", "/run/user/1", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := runtimeRoot(tc.goos, tc.xdg); got != tc.want {
				t.Errorf("runtimeRoot(%q, %q) = %q, want %q", tc.goos, tc.xdg, got, tc.want)
			}
		})
	}
}

// Without a directory, a Linux system with a runtime directory makes its
// scripts there, in a directory called just, as upstream does.
func TestMkdirTempUsesTheRuntimeDirectory(t *testing.T) {
	skipOnWindows(t)
	xdg := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", xdg)
	dir, err := (OSFS{goos: "linux"}).MkdirTemp("", "just-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	if want := filepath.Join(xdg, "just"); filepath.Dir(dir) != want {
		t.Errorf("MkdirTemp = %q, want a directory of %s", dir, want)
	}
}

func TestMkdirTempReportsAnUnusableRuntimeDirectory(t *testing.T) {
	skipOnWindows(t)
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_RUNTIME_DIR", file)
	if dir, err := (OSFS{goos: "linux"}).MkdirTemp("", "just-"); err == nil {
		t.Fatalf("MkdirTemp = %q, want an error", dir)
	}
}
