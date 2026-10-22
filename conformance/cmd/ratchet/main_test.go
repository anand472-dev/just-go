package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gitea.com/wimwian/just-go/internal/sys"
	"gitea.com/wimwian/just-go/internal/sys/sysfake"
)

// event renders one `go test -json` event line.
func event(action, test string) string {
	return fmt.Sprintf(`{"Action":%q,"Package":"p","Test":%q}`, action, test)
}

// scriptedGo is a sys.Process that answers every run with fixed stdout.
type scriptedGo struct {
	stdout string
	err    error
	specs  []sys.ProcessSpec
}

func (p *scriptedGo) Run(_ context.Context, spec sys.ProcessSpec) (int, error) {
	p.specs = append(p.specs, spec)
	if p.err != nil {
		return -1, p.err
	}
	_, _ = spec.Stdout.Write([]byte(p.stdout))
	return 1, nil
}

// recorder captures writes.
type recorder map[string]string

func (r recorder) write(path string, data []byte) error {
	r[path] = string(data)
	return nil
}

func TestRunMergesPassingIDs(t *testing.T) {
	output := strings.Join([]string{
		"# build output that is not JSON",
		event("run", "TestConformance/b#1"),
		event("pass", "TestConformance/b#1"),
		event("fail", "TestConformance/c#1"),
		event("skip", "TestConformance/d#1"),
		event("pass", "TestConformance/a#1"),
		event("pass", "TestBaseline/z#1"),
		event("pass", "TestConformance"),
		event("pass", ""),
	}, "\n")
	proc := &scriptedGo{stdout: output}
	fsys := sysfake.FakeFS{"base.txt": "kept#1\nb#1\n"}
	written := recorder{}
	var stderr bytes.Buffer
	env := []string{"JUST_GO_BIN=x", "HOME=/h"}
	code := run([]string{"-baseline", "base.txt", "-pkg", "./pkg"}, env, &stderr, fsys, proc, written.write)
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, stderr.String())
	}
	if got, want := written["base.txt"], "a#1\nb#1\nkept#1\n"; got != want {
		t.Fatalf("baseline = %q, want %q", got, want)
	}
	if !strings.Contains(stderr.String(), "2 passing, 3 in baseline (1 new)") {
		t.Fatalf("summary missing: %q", stderr.String())
	}
	spec := proc.specs[0]
	if got := strings.Join(spec.Argv, " "); got != "go test -json -count=1 -run ^TestConformance$ ./pkg" {
		t.Fatalf("argv = %q", got)
	}
	if strings.Join(spec.Env, ",") != strings.Join(env, ",") {
		t.Fatalf("env = %v", spec.Env)
	}
}

func TestRunNeverRemovesIDs(t *testing.T) {
	proc := &scriptedGo{stdout: event("fail", "TestConformance/old#1")}
	fsys := sysfake.FakeFS{"conformance/baseline.txt": "old#1\n"}
	written := recorder{}
	if code := run(nil, []string{"JUST_GO_BIN=x"}, &bytes.Buffer{}, fsys, proc, written.write); code != 0 {
		t.Fatalf("code %d", code)
	}
	if got := written["conformance/baseline.txt"]; got != "old#1\n" {
		t.Fatalf("baseline = %q", got)
	}
}

func TestRunUsageErrors(t *testing.T) {
	fsys := sysfake.FakeFS{"conformance/baseline.txt": ""}
	for name, tc := range map[string]struct {
		args, env []string
	}{
		"no JUST_GO_BIN":    {nil, []string{"HOME=/h"}},
		"empty JUST_GO_BIN": {nil, []string{"JUST_GO_BIN=x", "JUST_GO_BIN="}},
		"unknown flag":      {[]string{"-nope"}, []string{"JUST_GO_BIN=x"}},
		"extra argument":    {[]string{"extra"}, []string{"JUST_GO_BIN=x"}},
	} {
		var stderr bytes.Buffer
		if code := run(tc.args, tc.env, &stderr, fsys, &scriptedGo{}, recorder{}.write); code != 2 {
			t.Errorf("%s: code %d, want 2", name, code)
		}
		if stderr.Len() == 0 {
			t.Errorf("%s: no message", name)
		}
	}
}

func TestRunFailures(t *testing.T) {
	env := []string{"JUST_GO_BIN=x"}
	good := sysfake.FakeFS{"conformance/baseline.txt": ""}
	failWrite := func(string, []byte) error { return errors.New("disk full") }
	huge := `{"Action":"pass","Test":"TestConformance/` + strings.Repeat("x", 17*1024*1024) + `"}`
	for name, tc := range map[string]struct {
		fsys  sys.FS
		proc  sys.Process
		write writeFunc
		want  string
	}{
		"missing baseline": {sysfake.FakeFS{}, &scriptedGo{}, recorder{}.write, "baseline.txt"},
		"go test fails":    {good, &scriptedGo{err: errors.New("no go")}, recorder{}.write, "no go"},
		"line too long":    {good, &scriptedGo{stdout: huge}, recorder{}.write, "too long"},
		"write fails":      {good, &scriptedGo{}, failWrite, "disk full"},
	} {
		var stderr bytes.Buffer
		if code := run(nil, env, &stderr, tc.fsys, tc.proc, tc.write); code != 1 {
			t.Errorf("%s: code %d, want 1", name, code)
		}
		if !strings.Contains(stderr.String(), tc.want) {
			t.Errorf("%s: stderr %q does not mention %q", name, stderr.String(), tc.want)
		}
	}
}

func TestFormatBaseline(t *testing.T) {
	if got := formatBaseline(nil); got != "" {
		t.Fatalf("empty = %q", got)
	}
	if got := formatBaseline([]string{"a", "b"}); got != "a\nb\n" {
		t.Fatalf("got %q", got)
	}
}

func TestWriteFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "baseline.txt")
	if err := writeFile(path, []byte("a#1\n")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "a#1\n" {
		t.Fatalf("read back %q, %v", data, err)
	}
}

func TestMainExitsWithRunStatus(t *testing.T) {
	t.Setenv("JUST_GO_BIN", "")
	stderr, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	defer stderr.Close()
	oldArgs, oldExit, oldStderr := os.Args, exit, os.Stderr
	defer func() { os.Args, exit, os.Stderr = oldArgs, oldExit, oldStderr }()
	code := -1
	exit = func(c int) { code = c }
	os.Args = []string{"ratchet"}
	os.Stderr = stderr
	main()
	if code != 2 {
		t.Fatalf("exit code %d, want 2 (JUST_GO_BIN unset)", code)
	}
	data, err := os.ReadFile(stderr.Name())
	if err != nil || !strings.Contains(string(data), "JUST_GO_BIN must name") {
		t.Fatalf("stderr %q, %v; want the JUST_GO_BIN message", data, err)
	}
}

func BenchmarkPassingIDs(b *testing.B) {
	var lines []string
	for i := range 2000 {
		test := fmt.Sprintf("TestConformance/module::case_%d#1", i)
		lines = append(lines, event("run", test), event("output", test), event("pass", test))
	}
	output := []byte(strings.Join(lines, "\n"))
	b.ReportAllocs()
	b.SetBytes(int64(len(output)))
	for b.Loop() {
		if _, err := passingIDs(output); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMergeBaseline(b *testing.B) {
	var old, passing []string
	for i := range 2000 {
		old = append(old, fmt.Sprintf("m::c_%d#1", i))
		passing = append(passing, fmt.Sprintf("m::c_%d#1", i+1000))
	}
	b.ReportAllocs()
	for b.Loop() {
		mergeBaseline(old, passing)
	}
}
