//go:build unix

package sys

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func writeProgram(t *testing.T, dir, name string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho "+name+"\n"), mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestIsBare(t *testing.T) {
	for name, want := range map[string]bool{"sh": true, "/bin/sh": false, "./sh": false, "a/b": false} {
		if got := isBare(name); got != want {
			t.Errorf("isBare(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestEnvValueLastEntryWins(t *testing.T) {
	env := []string{"PATH=/a", "path=/x", "NOEQUALS", "PATH=/b"}
	if v, ok := envValue(env, "PATH"); !ok || v != "/b" {
		t.Fatalf("envValue = %q, %v; want /b, true", v, ok)
	}
	if _, ok := envValue(env, "HOME"); ok {
		t.Fatal("envValue(HOME) found a value")
	}
	if sameKey("PATH", "path") {
		t.Fatal("unix keys matched case-insensitively")
	}
}

func TestSearchPathDefaultsWithoutPATH(t *testing.T) {
	if got := searchPath([]string{"A=1"}); got != defaultPath {
		t.Fatalf("searchPath = %q, want %q", got, defaultPath)
	}
	if got := searchPath([]string{"PATH=/x"}); got != "/x" {
		t.Fatalf("searchPath = %q, want /x", got)
	}
	if got := searchPath([]string{"PATH="}); got != "" {
		t.Fatalf("searchPath = %q, want empty", got)
	}
}

func TestCandidatesSkipRelativeAndEmptyDirs(t *testing.T) {
	got := candidates("sh", "/a::rel:./b:/c", []string{"", ".x"})
	want := []string{"/a/sh", "/a/sh.x", "/c/sh", "/c/sh.x"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("candidates = %q, want %q", got, want)
	}
	if got := pathExts(nil); !reflect.DeepEqual(got, []string{""}) {
		t.Fatalf("pathExts = %q", got)
	}
}

func TestIsExecutable(t *testing.T) {
	dir := t.TempDir()
	if !isExecutable(writeProgram(t, dir, "x", 0o755)) {
		t.Fatal("executable file not executable")
	}
	if isExecutable(writeProgram(t, dir, "n", 0o644)) {
		t.Fatal("non-executable file reported executable")
	}
	if isExecutable(dir) {
		t.Fatal("directory reported executable")
	}
	if isExecutable(filepath.Join(dir, "missing")) {
		t.Fatal("missing file reported executable")
	}
}

func TestResolveProgramUsesChildPATHOnly(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	writeProgram(t, first, "jgprobe", 0o644) // not executable: skipped
	want := writeProgram(t, second, "jgprobe", 0o755)
	t.Setenv("PATH", second) // the parent's PATH must not be consulted

	env := []string{"PATH=rel:" + first + ":" + second}
	if got, err := resolveProgram("jgprobe", env); err != nil || got != want {
		t.Fatalf("resolveProgram = %q, %v; want %q", got, err, want)
	}
	_, err := resolveProgram("jgprobe", []string{"PATH=" + first})
	if !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("err = %v, want exec.ErrNotFound", err)
	}
	if got, err := resolveProgram("./rel/x", nil); err != nil || got != "./rel/x" {
		t.Fatalf("resolveProgram(path) = %q, %v", got, err)
	}
}

func TestRunResolvesBareNameFromSpecEnv(t *testing.T) {
	dir := t.TempDir()
	writeProgram(t, dir, "jgprobe", 0o755)
	t.Setenv("PATH", "/nonexistent")

	var out bytes.Buffer
	spec := ProcessSpec{Argv: []string{"jgprobe"}, Env: []string{"PATH=" + dir}, Stdout: &out}
	if code, err := (OSProcess{}).Run(context.Background(), spec); err != nil || code != 0 {
		t.Fatalf("Run = %d, %v", code, err)
	}
	if out.String() != "jgprobe\n" {
		t.Fatalf("stdout = %q", out.String())
	}

	t.Setenv("PATH", dir) // found only through the parent: must fail
	spec.Env = []string{"PATH=/nonexistent"}
	if code, err := (OSProcess{}).Run(context.Background(), spec); !errors.Is(err, exec.ErrNotFound) || code != -1 {
		t.Fatalf("Run = %d, %v; want -1, exec.ErrNotFound", code, err)
	}
}

func TestRunBareShellWithoutPATHUsesDefault(t *testing.T) {
	t.Setenv("PATH", "/nonexistent")
	var out bytes.Buffer
	spec := ProcessSpec{Argv: []string{"sh", "-c", "echo ok"}, Stdout: &out}
	if code, err := (OSProcess{}).Run(context.Background(), spec); err != nil || code != 0 || out.String() != "ok\n" {
		t.Fatalf("Run = %d, %v, %q", code, err, out.String())
	}
}

func BenchmarkResolveProgram(b *testing.B) {
	env := []string{"HOME=/h", "PATH=/usr/local/bin:/usr/bin:/bin"}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := resolveProgram("sh", env); err != nil {
			b.Fatal(err)
		}
	}
}
