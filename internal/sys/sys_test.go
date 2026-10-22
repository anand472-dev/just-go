package sys_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gitea.com/wimwian/just-go/internal/sys"
	"gitea.com/wimwian/just-go/internal/sys/sysfake"
)

var (
	_ sys.FS      = sys.OSFS{}
	_ sys.FS      = (*sysfake.FakeFS)(nil)
	_ sys.Process = sys.OSProcess{}
	_ sys.Process = (*sysfake.FakeProcess)(nil)
)

func wantIsFile(t *testing.T, fsys sys.FS, path string, want bool) {
	t.Helper()
	if ok, err := fsys.IsFile(path); err != nil || ok != want {
		t.Fatalf("IsFile(%s) = %v, %v; want %v, nil", path, ok, err, want)
	}
}

func TestOSFSReadsAndDetectsFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	fsys := sys.OSFS{}

	got, err := fsys.ReadFile(path)
	if err != nil || string(got) != "hello" {
		t.Fatalf("ReadFile = %q, %v", got, err)
	}
	wantIsFile(t, fsys, path, true)
	wantIsFile(t, fsys, dir, false)
	wantIsFile(t, fsys, filepath.Join(dir, "missing"), false)
	if _, err := fsys.ReadFile(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("ReadFile(missing) succeeded")
	}
}

func TestOSFSIsFileReportsOtherErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	// A path component that is a file makes Stat fail with ENOTDIR, not ErrNotExist.
	if _, err := (sys.OSFS{}).IsFile(filepath.Join(path, "child")); err == nil {
		t.Fatal("IsFile through a file succeeded; want error")
	}
}

func TestOSProcessRunsWithExactEnv(t *testing.T) {
	var out, errOut bytes.Buffer
	code, err := sys.OSProcess{}.Run(context.Background(), sys.ProcessSpec{
		Argv:   []string{"/bin/sh", "-c", `printf '%s %s' "${A-unset}" "${HOME-unset}"`},
		Env:    []string{"A=1"},
		Stdout: &out,
		Stderr: &errOut,
	})
	if err != nil || code != 0 {
		t.Fatalf("Run = %d, %v", code, err)
	}
	if out.String() != "1 unset" {
		t.Fatalf("stdout = %q, want %q", out.String(), "1 unset")
	}
}

func TestOSProcessReportsExitCodeDirAndStdin(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	code, err := sys.OSProcess{}.Run(context.Background(), sys.ProcessSpec{
		Argv:   []string{"/bin/sh", "-c", "cat; pwd -P; exit 7"},
		Dir:    dir,
		Stdin:  bytes.NewBufferString("in\n"),
		Stdout: &out,
	})
	if err != nil || code != 7 {
		t.Fatalf("Run = %d, %v; want 7, nil", code, err)
	}
	want, _ := filepath.EvalSymlinks(dir)
	if out.String() != "in\n"+want+"\n" {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestOSProcessRejectsEmptyArgv(t *testing.T) {
	if _, err := (sys.OSProcess{}).Run(context.Background(), sys.ProcessSpec{}); err == nil {
		t.Fatal("empty argv succeeded")
	}
}

func TestOSProcessStartFailureIsError(t *testing.T) {
	code, err := sys.OSProcess{}.Run(context.Background(), sys.ProcessSpec{
		Argv: []string{"/nonexistent/program"},
	})
	if err == nil || code != -1 {
		t.Fatalf("Run = %d, %v; want -1 and an error", code, err)
	}
}

func TestOSProcessCancelKillsGroup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	// The shell forks sleep. Without a group kill, the orphaned sleep holds the
	// output pipe open until Cmd.WaitDelay (killGrace, 2s) force-closes it, so
	// the bound must be below killGrace to prove the group was killed.
	var out bytes.Buffer
	_, err := sys.OSProcess{}.Run(ctx, sys.ProcessSpec{
		Argv:   []string{"/bin/sh", "-c", "sleep 30; echo done"},
		Stdout: &out,
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("took %v; process group was not killed", d)
	}
}

func wantRun(t *testing.T, p sys.Process, spec sys.ProcessSpec, want int) {
	t.Helper()
	if code, err := p.Run(context.Background(), spec); err != nil || code != want {
		t.Fatalf("Run = %d, %v; want %d, nil", code, err, want)
	}
}

func TestFakeProcessRecordsSpec(t *testing.T) {
	p := &sysfake.FakeProcess{Codes: []int{3, 0}}

	wantRun(t, p, sys.ProcessSpec{Argv: []string{"echo", "hi"}, Dir: "/d", Env: []string{"K=V"}}, 3)
	wantRun(t, p, sys.ProcessSpec{Argv: []string{"x"}}, 0)
	wantRun(t, p, sys.ProcessSpec{}, 0) // script exhausted: exit code 0

	if len(p.Specs) != 3 {
		t.Fatalf("recorded %d specs, want 3", len(p.Specs))
	}
	got := p.Specs[0]
	if got.Argv[0] != "echo" || got.Dir != "/d" || got.Env[0] != "K=V" {
		t.Fatalf("recorded spec = %+v", got)
	}
}

func TestFakeProcessReturnsScriptedError(t *testing.T) {
	boom := errors.New("boom")
	p := &sysfake.FakeProcess{Err: boom}
	if _, err := p.Run(context.Background(), sys.ProcessSpec{}); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
}

func TestFakeFSMissingFileIsError(t *testing.T) {
	fsys := sysfake.FakeFS{"a": "x"}
	if _, err := fsys.ReadFile("missing"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want ErrNotExist", err)
	}
	got, err := fsys.ReadFile("a")
	if err != nil || string(got) != "x" {
		t.Fatalf("ReadFile = %q, %v", got, err)
	}
	if ok, _ := fsys.IsFile("a"); !ok {
		t.Fatal("IsFile(a) = false")
	}
	if ok, _ := fsys.IsFile("missing"); ok {
		t.Fatal("IsFile(missing) = true")
	}
}

func BenchmarkOSFSReadFile(b *testing.B) {
	path := filepath.Join(b.TempDir(), "f")
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), 4096), 0o644); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := (sys.OSFS{}).ReadFile(path); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkFakeFSReadFile(b *testing.B) {
	fsys := sysfake.FakeFS{"f": string(bytes.Repeat([]byte("x"), 4096))}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := fsys.ReadFile("f"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkOSProcessRun(b *testing.B) {
	spec := sys.ProcessSpec{Argv: []string{"/bin/sh", "-c", "exit 0"}}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := (sys.OSProcess{}).Run(context.Background(), spec); err != nil {
			b.Fatal(err)
		}
	}
}
