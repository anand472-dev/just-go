package sys_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
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
	_ sys.Clock   = sys.OSClock{}
	_ sys.Clock   = sysfake.FakeClock{}
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

func TestFakeProcessWritesScriptedOutput(t *testing.T) {
	p := &sysfake.FakeProcess{Outputs: []string{"one\n", "two"}}
	var out bytes.Buffer
	wantRun(t, p, sys.ProcessSpec{Stdout: &out}, 0)
	wantRun(t, p, sys.ProcessSpec{Stdout: &out}, 0)
	wantRun(t, p, sys.ProcessSpec{Stdout: &out}, 0) // script exhausted: nothing
	wantRun(t, p, sys.ProcessSpec{}, 0)             // no stream to write to
	if out.String() != "one\ntwo" {
		t.Errorf("output = %q, want %q", out.String(), "one\ntwo")
	}
}

func TestCaptureCollectsStandardOutput(t *testing.T) {
	p := &sysfake.FakeProcess{Outputs: []string{"hello\n"}, Codes: []int{3}}
	var ignored bytes.Buffer
	got, err := sys.Capture(context.Background(), p, sys.ProcessSpec{Argv: []string{"x"}, Stdout: &ignored})
	if err != nil || string(got.Stdout) != "hello\n" || got.ExitCode != 3 || got.Signal != 0 || got.Caught != 0 {
		t.Errorf("Capture = %+v, %v; want the output and status 3", got, err)
	}
	if ignored.Len() != 0 || p.Specs[0].Argv[0] != "x" {
		t.Errorf("the caller's Stdout got %q, spec %+v; want it replaced and the rest kept", ignored.String(), p.Specs[0])
	}
}

func TestCaptureReportsSignalsAsResults(t *testing.T) {
	for _, sig := range []*sys.SignalError{{Signal: 9}, {Caught: 2}} {
		p := &sysfake.FakeProcess{Err: sig}
		got, err := sys.Capture(context.Background(), p, sys.ProcessSpec{})
		if err != nil || got.Signal != sig.Signal || got.Caught != sig.Caught || got.ExitCode != -1 {
			t.Errorf("Capture with %+v = %+v, %v", sig, got, err)
		}
	}
}

func TestCaptureReturnsOtherErrors(t *testing.T) {
	boom := errors.New("boom")
	if _, err := sys.Capture(context.Background(), &sysfake.FakeProcess{Err: boom}, sys.ProcessSpec{}); !errors.Is(err, boom) {
		t.Errorf("Capture error = %v, want boom", err)
	}
}

func TestCaptureRunsARealProgram(t *testing.T) {
	got, err := sys.Capture(context.Background(), sys.OSProcess{}, sys.ProcessSpec{
		Argv: []string{"sh", "-c", "printf out; exit 4"}, Env: []string{"PATH=/usr/bin:/bin"},
	})
	if err != nil || string(got.Stdout) != "out" || got.ExitCode != 4 {
		t.Errorf("Capture = %+v, %v; want out and status 4", got, err)
	}
}

func BenchmarkCapture(b *testing.B) {
	p := &sysfake.FakeProcess{Outputs: []string{"some output\n"}}
	spec := sys.ProcessSpec{Argv: []string{"x"}}
	b.ReportAllocs()
	for b.Loop() {
		p.Specs = p.Specs[:0]
		if _, err := sys.Capture(context.Background(), p, spec); err != nil {
			b.Fatal(err)
		}
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

func TestOSFSReadDirListsSortedNames(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"b", ".a", "JUSTFILE"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := (sys.OSFS{}).ReadDir(dir)
	want := []string{".a", "JUSTFILE", "b", "sub"}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("ReadDir = %v, %v; want %v", got, err, want)
	}
}

func TestOSFSReadDirMissingIsError(t *testing.T) {
	_, err := (sys.OSFS{}).ReadDir(filepath.Join(t.TempDir(), "missing"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want ErrNotExist", err)
	}
}

func TestFakeFSReadDirListsDirectChildren(t *testing.T) {
	fsys := sysfake.FakeFS{"/p/b": "", "/p/a": "", "/p/sub/c": "", "/q/d": "", "top": ""}
	cases := []struct {
		dir  string
		want []string
	}{
		{"/p", []string{"a", "b"}},
		{"/p/", []string{"a", "b"}},
		{"/p/sub", []string{"c"}},
		{".", []string{"top"}},
		{"/none", nil},
	}
	for _, c := range cases {
		got, err := fsys.ReadDir(c.dir)
		if err != nil || !slices.Equal(got, c.want) {
			t.Errorf("ReadDir(%q) = %v, %v; want %v", c.dir, got, err, c.want)
		}
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

func TestOSClockReadsTheSystemClockInTheLocalZone(t *testing.T) {
	before := time.Now()
	got := (sys.OSClock{}).Now()
	after := time.Now()
	if got.Unix < before.Unix() || got.Unix > after.Unix() {
		t.Errorf("Now().Unix = %d, want between %d and %d", got.Unix, before.Unix(), after.Unix())
	}
	if got.Nano < 0 || got.Nano >= 1_000_000_000 {
		t.Errorf("Now().Nano = %d, want a fraction of a second", got.Nano)
	}
	if _, offset := time.Unix(got.Unix, 0).Zone(); got.Offset != offset || got.UTC {
		t.Errorf("Now() = %+v, want the local offset %d and not UTC", got, offset)
	}
}

func TestFakeClockReportsItsTime(t *testing.T) {
	at := sys.Time{Unix: 994518299, Nano: 5, Offset: 34200}
	if got := (sysfake.FakeClock{At: at}).Now(); got != at {
		t.Errorf("Now() = %+v, want %+v", got, at)
	}
}

func TestSignalNumberKnowsTheSignalsContinueNames(t *testing.T) {
	for name, want := range map[string]int{"SIGHUP": sys.SIGHUP, "SIGINT": sys.SIGINT, "SIGQUIT": sys.SIGQUIT} {
		if got, ok := sys.SignalNumber(name); !ok || got != want {
			t.Errorf("SignalNumber(%q) = %d, %v; want %d, true", name, got, ok, want)
		}
		if sys.SignalName(want) != name {
			t.Errorf("SignalName(%d) = %q, want %q", want, sys.SignalName(want), name)
		}
	}
	for _, name := range []string{"", "SIGTERM", "sigint", "INT"} {
		if got, ok := sys.SignalNumber(name); ok || got != 0 {
			t.Errorf("SignalNumber(%q) = %d, %v; want 0, false", name, got, ok)
		}
	}
}

func BenchmarkOSClockNow(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		(sys.OSClock{}).Now()
	}
}

func TestOSFSWritesAndRemovesAScriptDirectory(t *testing.T) {
	fsys := sys.OSFS{}
	root := t.TempDir()
	dir, err := fsys.MkdirTemp(root, "just-")
	if err != nil || filepath.Dir(dir) != root || !strings.HasPrefix(filepath.Base(dir), "just-") {
		t.Fatalf("MkdirTemp = %q, %v; want a just-* directory of %s", dir, err, root)
	}
	path := filepath.Join(dir, "script")
	if err := fsys.WriteFile(path, []byte("echo hi\n"), 0o700); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	wantFile(t, path, "echo hi\n")
	if err := fsys.RemoveAll(dir); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("directory still there: %v", err)
	}
}

// wantFile checks that the file at path holds want, and, where permissions
// mean something, that it can be executed.
func wantFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != want {
		t.Fatalf("file = %q, %v; want %q", got, err, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("mode = %v, want an executable file", info.Mode())
	}
}

func TestOSFSWriteFileReportsFailure(t *testing.T) {
	err := (sys.OSFS{}).WriteFile(filepath.Join(t.TempDir(), "missing", "f"), nil, 0o600)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want ErrNotExist", err)
	}
}

func TestOSFSMkdirTempReportsFailure(t *testing.T) {
	_, err := (sys.OSFS{}).MkdirTemp(filepath.Join(t.TempDir(), "missing"), "just-")
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want ErrNotExist", err)
	}
}

func TestOSFSMkdirTempWithoutADirectoryUsesTheTemporaryDirectory(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	t.Setenv("TMP", root)
	t.Setenv("TEMP", root)
	t.Setenv("XDG_RUNTIME_DIR", "")
	dir, err := (sys.OSFS{}).MkdirTemp("", "just-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(dir)
	if rel, err := filepath.Rel(root, dir); err != nil || strings.HasPrefix(rel, "..") {
		t.Fatalf("MkdirTemp = %q, want a directory of %s", dir, root)
	}
}

func TestFakeFSWritesAScriptDirectory(t *testing.T) {
	fsys := sysfake.FakeFS{"/keep": "k"}
	dir, err := fsys.MkdirTemp("/work", "just-")
	if err != nil || dir != "/work/just-fake" {
		t.Fatalf("MkdirTemp = %q, %v; want /work/just-fake", dir, err)
	}
	if got, _ := fsys.MkdirTemp("", "just-"); got != "/tmp/just-fake" {
		t.Fatalf("MkdirTemp(\"\") = %q; want /tmp/just-fake", got)
	}
	if err := fsys.WriteFile(dir+"/a", []byte("x"), 0o700); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if got, err := fsys.ReadFile(dir + "/a"); err != nil || string(got) != "x" {
		t.Fatalf("ReadFile = %q, %v", got, err)
	}
	_ = fsys.WriteFile(dir+"/sub/b", nil, 0o600)
	_ = fsys.WriteFile(dir+"-other/c", nil, 0o600)
	if err := fsys.RemoveAll(dir); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	if want := (sysfake.FakeFS{"/keep": "k", "/work/just-fake-other/c": ""}); !reflect.DeepEqual(fsys, want) {
		t.Fatalf("after RemoveAll = %v; want %v", fsys, want)
	}
}
