package sys_test

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"gitea.com/wimwian/just-go/internal/sys"
	"gitea.com/wimwian/just-go/internal/sys/sysfake"
)

var (
	_ sys.Env      = sys.EnvList(nil)
	_ sys.Env      = sysfake.FakeEnv(nil)
	_ sys.Platform = sys.OSPlatform{}
	_ sys.Platform = sysfake.FakePlatform{}
	_ sys.Random   = sys.OSRandom{}
	_ sys.Random   = (*sysfake.FakeRandom)(nil)
)

func TestEnvListFindsTheLastEntryOfAKey(t *testing.T) {
	env := sys.EnvList{"A=1", "B=", "A=2", "C=x=y", "NOEQUALS"}
	for _, tc := range []struct {
		key, want string
		ok        bool
	}{
		{"A", "2", true}, {"B", "", true}, {"C", "x=y", true},
		{"NOEQUALS", "", false}, {"missing", "", false}, {"", "", false}, {"C=x", "", false},
	} {
		if got, ok := env.LookupEnv(tc.key); got != tc.want || ok != tc.ok {
			t.Errorf("LookupEnv(%q) = %q, %v; want %q, %v", tc.key, got, ok, tc.want, tc.ok)
		}
	}
	if _, ok := sys.EnvList(nil).LookupEnv("A"); ok {
		t.Error("an empty list has a variable")
	}
}

func TestFakeEnvLooksKeysUp(t *testing.T) {
	env := sysfake.FakeEnv{"A": "1", "E": ""}
	if got, ok := env.LookupEnv("A"); got != "1" || !ok {
		t.Errorf("LookupEnv(A) = %q, %v", got, ok)
	}
	if got, ok := env.LookupEnv("E"); got != "" || !ok {
		t.Errorf("LookupEnv(E) = %q, %v", got, ok)
	}
	if _, ok := env.LookupEnv("Z"); ok {
		t.Error("found a variable that is not there")
	}
}

func TestOSPlatformDescribesTheSystemItRunsOn(t *testing.T) {
	p := sys.OSPlatform{}
	if p.OS() != runtime.GOOS || p.Arch() != runtime.GOARCH {
		t.Errorf("OS, Arch = %q, %q; want %q, %q", p.OS(), p.Arch(), runtime.GOOS, runtime.GOARCH)
	}
	if p.NumCPU() != runtime.NumCPU() || p.NumCPU() < 1 {
		t.Errorf("NumCPU = %d, want %d", p.NumCPU(), runtime.NumCPU())
	}
	if p.PID() != os.Getpid() {
		t.Errorf("PID = %d, want %d", p.PID(), os.Getpid())
	}
	want, err := os.Executable()
	if got, gotErr := p.Executable(); got != want || (gotErr == nil) != (err == nil) {
		t.Errorf("Executable = %q, %v; want %q, %v", got, gotErr, want, err)
	}
	if got := (sys.OSPlatform{GOOS: "plan9"}).OS(); got != "plan9" {
		t.Errorf("OS with an override = %q, want plan9", got)
	}
}

func TestOSPlatformIsExecutableLooksAtTheFile(t *testing.T) {
	dir := t.TempDir()
	run := filepath.Join(dir, "run.exe")
	if err := os.WriteFile(run, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(dir, "plain.txt")
	if err := os.WriteFile(plain, []byte("text"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := sys.OSPlatform{}
	if !p.IsExecutable(run) {
		t.Errorf("IsExecutable(%s) = false, want true", run)
	}
	if runtime.GOOS != "windows" && p.IsExecutable(plain) {
		t.Errorf("IsExecutable(%s) = true, want false: it has no execute bit", plain)
	}
	if p.IsExecutable(dir) || p.IsExecutable(filepath.Join(dir, "missing")) {
		t.Error("a directory or a missing path is executable")
	}
}

func TestOSRandomFillsTheBuffer(t *testing.T) {
	a, b := make([]byte, 16), make([]byte, 16)
	for _, buf := range [][]byte{a, b} {
		if n, err := (sys.OSRandom{}).Read(buf); n != len(buf) || err != nil {
			t.Fatalf("Read = %d, %v", n, err)
		}
	}
	if bytes.Equal(a, b) {
		t.Errorf("two reads of 16 random bytes were both %x", a)
	}
}

func TestFakePlatformAnswersWithItsFields(t *testing.T) {
	boom := errors.New("boom")
	p := sysfake.FakePlatform{
		GOOS: "linux", GOARCH: "amd64", CPUs: 4, Process: 7, Path: "/bin/just", ExecutableErr: boom,
		Executables: map[string]bool{"/bin/ok": true},
	}
	if p.OS() != "linux" || p.Arch() != "amd64" || p.NumCPU() != 4 || p.PID() != 7 {
		t.Errorf("got %q %q %d %d", p.OS(), p.Arch(), p.NumCPU(), p.PID())
	}
	if path, err := p.Executable(); path != "/bin/just" || err != boom {
		t.Errorf("Executable = %q, %v", path, err)
	}
	if !p.IsExecutable("/bin/ok") || p.IsExecutable("/bin/other") {
		t.Error("IsExecutable does not follow Executables")
	}
}

func TestFakeRandomReturnsItsBytesInOrder(t *testing.T) {
	r := &sysfake.FakeRandom{Bytes: []byte{1, 2, 3, 4, 5}}
	buf := make([]byte, 2)
	for _, want := range [][]byte{{1, 2}, {3, 4}} {
		if n, err := r.Read(buf); n != 2 || err != nil || !bytes.Equal(buf, want) {
			t.Fatalf("Read = %d, %v, %v; want %v", n, err, buf, want)
		}
	}
	if n, err := r.Read(buf); n != 0 || err != io.EOF {
		t.Errorf("Read past the end = %d, %v; want 0, EOF", n, err)
	}
	boom := errors.New("boom")
	if _, err := (&sysfake.FakeRandom{Err: boom}).Read(buf); err != boom {
		t.Errorf("Read with an error set = %v, want it", err)
	}
}

func BenchmarkEnvListLookupEnv(b *testing.B) {
	env := sys.EnvList{"HOME=/home/me", "PATH=/usr/local/bin:/usr/bin:/bin", "SHELL=/bin/zsh", "TERM=xterm", "USER=me"}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if v, ok := env.LookupEnv("TERM"); !ok || v != "xterm" {
			b.Fatal(v, ok)
		}
	}
}

func BenchmarkOSPlatformIsExecutable(b *testing.B) {
	path, err := os.Executable()
	if err != nil {
		b.Fatal(err)
	}
	p := sys.OSPlatform{}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if !p.IsExecutable(path) {
			b.Fatal("the running program is not executable")
		}
	}
}
