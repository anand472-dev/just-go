package conformance

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"gitea.com/wimwian/just-go/internal/sys"
	"gitea.com/wimwian/just-go/internal/sys/sysfake"
)

const (
	baselinePath   = "baseline.txt"
	exceptionsPath = "oracle-exceptions.txt"
	// caseTimeout bounds one run of the binary under test, so a hung case
	// fails instead of stalling the whole suite.
	caseTimeout = 60 * time.Second
	// tempDirName has the shape of upstream's temporary directories: the
	// prefix "just-test-tempdir" and six characters (tests/tempdir.rs).
	tempDirName = "just-test-tempdir000000"
)

// harness is what every case run shares: the binary under test, the
// environment inherited from the test process, and the impure boundary.
type harness struct {
	bin     string
	environ []string
	proc    sys.Process
	fsys    sys.FS
}

// outcome is what one run of the binary produced.
type outcome struct {
	status         int
	stdout, stderr string
}

// placeholders substitutes the machine-specific paths of one run into
// recorded strings: as they are, regex-quoted, and JSON-string-escaped.
type placeholders struct {
	plain, regex, json *strings.Replacer
}

// newHarness returns the harness for JUST_GO_BIN, or skips the test when it
// is unset. A relative JUST_GO_BIN is resolved against the module root,
// because `go test` runs this package in conformance/. Impure: it reads the
// test process environment.
func newHarness(t *testing.T) harness {
	t.Helper()
	bin := os.Getenv("JUST_GO_BIN")
	if bin == "" {
		t.Skip("JUST_GO_BIN is not set; it names the binary under test")
	}
	if !filepath.IsAbs(bin) {
		bin = filepath.Join("..", bin)
	}
	abs, err := filepath.Abs(bin)
	if err != nil {
		t.Fatal(err)
	}
	return harness{bin: abs, environ: os.Environ(), proc: sys.OSProcess{}, fsys: sys.OSFS{}}
}

func TestConformance(t *testing.T) {
	h := newHarness(t)
	cases := loadCorpus(t, h.fsys)
	exceptions := loadExceptions(t, h.fsys)
	for _, c := range cases {
		t.Run(c.ID, func(t *testing.T) {
			t.Parallel()
			if reason, ok := exceptions[c.ID]; ok {
				t.Skip("oracle exception: " + reason)
			}
			if err := h.check(t, c); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestBaseline(t *testing.T) {
	h := newHarness(t)
	byID := map[string]Case{}
	for _, c := range loadCorpus(t, h.fsys) {
		byID[c.ID] = c
	}
	data, err := h.fsys.ReadFile(baselinePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range strings.Fields(string(data)) {
		t.Run(id, func(t *testing.T) {
			t.Parallel()
			c, ok := byID[id]
			if !ok {
				t.Fatalf("baseline id %q is not in the corpus", id)
			}
			if err := h.check(t, c); err != nil {
				t.Error(err)
			}
		})
	}
}

// loadCorpus reads the corpus through fsys or fails the test. Impure.
func loadCorpus(t *testing.T, fsys sys.FS) []Case {
	t.Helper()
	cases, err := LoadCases(fsys, corpusPath)
	if err != nil {
		t.Fatal(err)
	}
	return cases
}

// loadExceptions reads oracle-exceptions.txt through fsys or fails the test.
// Impure.
func loadExceptions(t *testing.T, fsys sys.FS) map[string]string {
	t.Helper()
	data, err := fsys.ReadFile(exceptionsPath)
	if err != nil {
		t.Fatal(err)
	}
	exceptions, err := parseExceptions(string(data))
	if err != nil {
		t.Fatal(err)
	}
	return exceptions
}

// exceptionCategories is the closed set of reasons an oracle exception may
// give (ledger ruling R9). path-hash: a .justcache name derives from the
// recording machine's temporary path. machine-env: a variable such as USER
// comes from the recording machine. fifo: upstream feeds a named pipe from a
// thread the corpus cannot record. Empty directories, symbolic links, and
// file modes are recorded (R8) and materialized, so they are not excused.
// Adding a category here widens what the oracle is excused from, so it
// needs a controller ruling.
var exceptionCategories = map[string]bool{
	"path-hash":   true,
	"machine-env": true,
	"fifo":        true,
}

// parseExceptions maps case id to reason. Each entry line is an id, white
// space, and the reason; blank lines and lines starting with "#" are
// comments. A reason starts with "<category>:", and the category must be in
// exceptionCategories. An entry without a reason, or with an unknown
// category, is an error. Pure.
func parseExceptions(data string) (map[string]string, error) {
	exceptions := map[string]string{}
	for i, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		id, reason, _ := strings.Cut(line, " ")
		reason = strings.TrimSpace(reason)
		if err := checkExceptionReason(reason); err != nil {
			return nil, fmt.Errorf("%s:%d: exception %q %w", exceptionsPath, i+1, id, err)
		}
		exceptions[id] = reason
	}
	return exceptions, nil
}

// exceptionCategory returns the "<category>" prefix of a reason. Pure.
func exceptionCategory(reason string) string {
	category, _, _ := strings.Cut(reason, ":")
	return category
}

// checkExceptionReason rejects an empty reason and one whose "<category>:"
// prefix is not in exceptionCategories. Pure.
func checkExceptionReason(reason string) error {
	if reason == "" {
		return errors.New("has no reason")
	}
	if !strings.Contains(reason, ":") || !exceptionCategories[exceptionCategory(reason)] {
		return fmt.Errorf("has reason %q, which does not start with an allowed category", reason)
	}
	return nil
}

// isOracle reports whether bin is the pinned oracle, oracle/bin/just at the
// module root. Impure: it resolves the module root against the working
// directory.
func isOracle(bin string) bool {
	oracle, err := filepath.Abs(filepath.Join("..", "oracle", "bin", "just"))
	return err == nil && oracle == bin
}

// check runs one case by the comparison rules and returns every mismatch.
// Impure: it writes the case's files, runs the binary, and reads files back.
func (h harness) check(t *testing.T, c Case) error {
	dir := materialize(t, c)
	ph := newPlaceholders(dir, h.bin)
	ctx, cancel := context.WithTimeout(context.Background(), caseTimeout)
	defer cancel()
	got, err := runCase(ctx, h.proc, h.bin, c, dir, buildEnv(c, dir, h.environ, ph), ph)
	if err != nil {
		return err
	}
	return errors.Join(
		compareStatus(*c.Status, got.status),
		compareOutput("stdout", got.stdout, c.Stdout, c.StdoutRegex, ph),
		compareOutput("stderr", got.stderr, c.Stderr, c.StderrRegex, ph),
		compareResponse(got.stdout, c.Response, ph),
		compareFiles(h.fsys, dir, c.ExpectedFiles, ph),
	)
}

// materialize builds the case's inputs in a fresh temporary directory and
// returns its path with symbolic links resolved. The directory is named
// like upstream's (tempDirName), because some expectations match that name.
// It creates the empty directories, then writes the files with the mode
// Case.FileMode gives, then creates the working directory, and creates the
// symbolic links last, because a link's target may be any of those (or may
// dangle). Placeholders are substituted in file contents and link targets.
// Impure: it writes the filesystem. Writing is test scaffolding: sys.FS is
// read-only, and t.TempDir owns the cleanup.
func materialize(t *testing.T, c Case) string {
	t.Helper()
	parent := caseParentDir(t)
	dir := filepath.Join(parent, tempDirName)
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeInputs(c, dir, newPlaceholders(dir, "")); err != nil {
		t.Fatal(err)
	}
	return dir
}

// caseParentDir returns a fresh directory, symbolic links resolved, to hold
// one case's temporary directory, and removes it when the test ends. It is
// not t.TempDir, whose path holds the subtest name: case ids contain "#",
// and just reads a "#" in a shebang path as the start of a comment, as
// upstream's own temporary paths never contain one. Impure.
func caseParentDir(t *testing.T) string {
	t.Helper()
	parent, err := os.MkdirTemp("", "just-conformance-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(parent) })
	parent, err = filepath.EvalSymlinks(parent)
	if err != nil {
		t.Fatal(err)
	}
	return parent
}

// writeInputs creates the dirs, files, working directory, and symlinks of c
// under dir, in that order. Impure.
func writeInputs(c Case, dir string, ph placeholders) error {
	for _, d := range c.Dirs {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			return err
		}
	}
	for name, content := range c.Files {
		if err := writeCaseFile(filepath.Join(dir, name), ph.plain.Replace(content), c.FileMode(name)); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, c.Cwd), 0o755); err != nil {
		return err
	}
	for link, target := range c.Symlinks {
		if err := writeSymlink(filepath.Join(dir, link), ph.plain.Replace(target)); err != nil {
			return err
		}
	}
	return nil
}

// writeCaseFile writes one file of a case with exactly mode, creating its
// parent directories. The explicit chmod makes the mode independent of the
// umask. Impure.
func writeCaseFile(path, content string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}

// writeSymlink creates the symbolic link path pointing at target, creating
// its parent directories. Impure.
func writeSymlink(path, target string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.Symlink(target, path)
}

// newPlaceholders builds the substitutions for a case run in dir against
// bin. Pure.
func newPlaceholders(dir, bin string) placeholders {
	values := [][2]string{
		{TempDirToken, dir},
		{TempDirParentToken, filepath.Dir(dir)},
		{JustExecutableToken, bin},
	}
	var plain, regex, js []string
	for _, v := range values {
		quoted, _ := json.Marshal(v[1])
		plain = append(plain, v[0], v[1])
		regex = append(regex, v[0], regexp.QuoteMeta(v[1]))
		js = append(js, v[0], string(quoted[1:len(quoted)-1]))
	}
	return placeholders{
		plain: strings.NewReplacer(plain...),
		regex: strings.NewReplacer(regex...),
		json:  strings.NewReplacer(js...),
	}
}

// buildEnv returns the environment of a run, following upstream's test
// harness (see the Env and PathExtra docs in case.go): the inherited
// environment minus every variable whose name starts with "JUST_", with the
// case's env overlaid (placeholders substituted). When path_extra is set,
// PATH is exactly those directories resolved against dir, replacing any
// other PATH, as upstream's Test::path does. The result is sorted. Pure.
func buildEnv(c Case, dir string, inherited []string, ph placeholders) []string {
	vars := make(map[string]string, len(inherited)+len(c.Env))
	for _, kv := range inherited {
		key, value, _ := strings.Cut(kv, "=")
		if !strings.HasPrefix(key, "JUST_") {
			vars[key] = value
		}
	}
	for key, value := range c.Env {
		vars[key] = ph.plain.Replace(value)
	}
	if len(c.PathExtra) > 0 {
		vars["PATH"] = joinPathExtra(c.PathExtra, dir)
	}
	env := make([]string, 0, len(vars))
	for key, value := range vars {
		env = append(env, key+"="+value)
	}
	sort.Strings(env)
	return env
}

// joinPathExtra resolves each entry against dir and joins them with the
// path list separator. The result is the whole PATH. Pure.
func joinPathExtra(entries []string, dir string) string {
	parts := make([]string, 0, len(entries))
	for _, entry := range entries {
		parts = append(parts, filepath.Join(dir, entry))
	}
	return strings.Join(parts, string(os.PathListSeparator))
}

// runCase runs bin with the case's arguments and stdin in its working
// directory under env. Impure: it runs a process through proc.
func runCase(ctx context.Context, proc sys.Process, bin string, c Case, dir string, env []string, ph placeholders) (outcome, error) {
	argv := []string{bin}
	for _, arg := range c.Args {
		argv = append(argv, ph.plain.Replace(arg))
	}
	var stdout, stderr bytes.Buffer
	status, err := proc.Run(ctx, sys.ProcessSpec{
		Argv:   argv,
		Dir:    filepath.Join(dir, c.Cwd),
		Env:    env,
		Stdin:  strings.NewReader(ph.plain.Replace(c.Stdin)),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		return outcome{}, fmt.Errorf("run %s: %w", bin, err)
	}
	return outcome{status: status, stdout: stdout.String(), stderr: stderr.String()}, nil
}

// compareStatus checks the exit status. Pure.
func compareStatus(want, got int) error {
	if want != got {
		return fmt.Errorf("status: got %d, want %d", got, want)
	}
	return nil
}

// compareOutput checks one output stream: by regex when pattern is set,
// otherwise exactly. Pure.
func compareOutput(name, got string, exact, pattern *string, ph placeholders) error {
	if pattern != nil {
		re, err := regexp.Compile(ph.regex.Replace(*pattern))
		if err != nil {
			return fmt.Errorf("%s_regex: %w", name, err)
		}
		if !re.MatchString(got) {
			return fmt.Errorf("%s: %q does not match /%s/", name, got, re)
		}
		return nil
	}
	if want := ph.plain.Replace(*exact); got != want {
		return fmt.Errorf("%s:\n got: %q\nwant: %q", name, got, want)
	}
	return nil
}

// compareResponse decodes stdout as JSON and compares it with the expected
// response, when the case has one. A JSON null response means none. Pure.
func compareResponse(stdout string, response json.RawMessage, ph placeholders) error {
	if len(response) == 0 || string(response) == "null" {
		return nil
	}
	var want, got any
	if err := json.Unmarshal([]byte(ph.json.Replace(string(response))), &want); err != nil {
		return fmt.Errorf("response: expected value: %w", err)
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		return fmt.Errorf("response: stdout is not JSON: %w", err)
	}
	if !reflect.DeepEqual(got, want) {
		return fmt.Errorf("response: got %v, want %v", got, want)
	}
	return nil
}

// compareFiles checks each expected file byte for byte. Impure: it reads
// through fsys.
func compareFiles(fsys sys.FS, dir string, expected map[string]string, ph placeholders) error {
	var errs []error
	for path, encoded := range expected {
		want, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			errs = append(errs, fmt.Errorf("expected file %s: %w", path, err))
			continue
		}
		got, err := fsys.ReadFile(filepath.Join(dir, path))
		if err != nil {
			errs = append(errs, fmt.Errorf("expected file %s: %w", path, err))
			continue
		}
		if wantText := ph.plain.Replace(string(want)); string(got) != wantText {
			errs = append(errs, fmt.Errorf("file %s:\n got: %q\nwant: %q", path, got, wantText))
		}
	}
	return errors.Join(errs...)
}

// The tests below check the harness itself, without JUST_GO_BIN.

func ptr[T any](v T) *T { return &v }

func TestOracleExceptionsNameCorpusCases(t *testing.T) {
	fsys := sys.OSFS{}
	exceptions := loadExceptions(t, fsys)
	byID := map[string]bool{}
	for _, c := range loadCorpus(t, fsys) {
		byID[c.ID] = true
	}
	for id := range exceptions {
		if !byID[id] {
			t.Errorf("oracle exception %q is not a corpus case", id)
		}
	}
}

// TestOracleExceptionsStillFail keeps oracle-exceptions.txt honest in the
// other direction: every listed case must still fail against the oracle.
// An exception that passes is stale (for example after a recorder fix) and
// must be removed. It runs only when JUST_GO_BIN is the pinned oracle,
// because the exceptions describe the oracle, not just-go. A machine-env
// entry is skipped: the run inherits the environment, so on the recording
// machine it passes, and it fails only elsewhere.
func TestOracleExceptionsStillFail(t *testing.T) {
	h := newHarness(t)
	if !isOracle(h.bin) {
		t.Skip("JUST_GO_BIN is not oracle/bin/just; the exceptions describe the oracle")
	}
	byID := map[string]Case{}
	for _, c := range loadCorpus(t, h.fsys) {
		byID[c.ID] = c
	}
	for id, reason := range loadExceptions(t, h.fsys) {
		t.Run(id, func(t *testing.T) {
			t.Parallel()
			if exceptionCategory(reason) == "machine-env" {
				t.Skip("machine-env: the case fails only on a machine other than the recording one")
			}
			if err := h.check(t, byID[id]); err == nil {
				t.Errorf("oracle exception %q now passes against the oracle; remove it from %s", id, exceptionsPath)
			}
		})
	}
}

func TestIsOracle(t *testing.T) {
	oracle, err := filepath.Abs(filepath.Join("..", "oracle", "bin", "just"))
	if err != nil {
		t.Fatal(err)
	}
	if !isOracle(oracle) {
		t.Errorf("isOracle(%q) = false, want true", oracle)
	}
	if isOracle("/usr/bin/just-go") {
		t.Error("isOracle(/usr/bin/just-go) = true, want false")
	}
}

func TestParseExceptions(t *testing.T) {
	got, err := parseExceptions("# comment\n\na#1 fifo: why  not\n  b#2   machine-env: because \n")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"a#1": "fifo: why  not", "b#2": "machine-env: because"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for _, bad := range []string{"a#1\n", "a#1 no category here\n", "a#1 flaky: unknown category\n", "a#1 mode: no longer excused (R9)\n"} {
		if _, err := parseExceptions(bad); err == nil || !strings.Contains(err.Error(), ":1:") {
			t.Errorf("parseExceptions(%q): want error naming the line, got %v", bad, err)
		}
	}
}

func TestNewPlaceholders(t *testing.T) {
	ph := newPlaceholders(`/t/d"x.y`, "/b/just")
	in := "@@TEMPDIR@@|@@TEMPDIR_PARENT@@|@@JUST_EXECUTABLE@@"
	if got, want := ph.plain.Replace(in), `/t/d"x.y|/t|/b/just`; got != want {
		t.Errorf("plain = %q, want %q", got, want)
	}
	if got, want := ph.regex.Replace(in), `/t/d"x\.y|/t|/b/just`; got != want {
		t.Errorf("regex = %q, want %q", got, want)
	}
	if got, want := ph.json.Replace(in), `/t/d\"x.y|/t|/b/just`; got != want {
		t.Errorf("json = %q, want %q", got, want)
	}
}

func TestBuildEnv(t *testing.T) {
	ph := newPlaceholders("/d", "/b")
	sep := string(os.PathListSeparator)
	inherited := []string{"HOME=/h", "PATH=/usr/bin", "JUST_GO_BIN=/x", "JUST_UNSTABLE=1", "K=old", "EQ=a=b"}
	for name, tc := range map[string]struct {
		c    Case
		want []string
	}{
		"inherits minus JUST_": {Case{}, []string{"EQ=a=b", "HOME=/h", "K=old", "PATH=/usr/bin"}},
		"env overlays": {
			Case{Env: map[string]string{"K": "@@TEMPDIR@@/v", "JUST_X": "kept"}},
			[]string{"EQ=a=b", "HOME=/h", "JUST_X=kept", "K=/d/v", "PATH=/usr/bin"},
		},
		"case PATH wins": {
			Case{Env: map[string]string{"PATH": "@@TEMPDIR@@/bin"}},
			[]string{"EQ=a=b", "HOME=/h", "K=old", "PATH=/d/bin"},
		},
		"path_extra replaces PATH": {
			Case{PathExtra: []string{".", "sub"}, Env: map[string]string{"PATH": "/ignored"}},
			[]string{"EQ=a=b", "HOME=/h", "K=old", "PATH=/d" + sep + "/d/sub"},
		},
	} {
		if got := buildEnv(tc.c, "/d", inherited, ph); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %v, want %v", name, got, tc.want)
		}
	}
	if got := buildEnv(Case{PathExtra: []string{"a"}}, "/d", nil, ph); !reflect.DeepEqual(got, []string{"PATH=/d/a"}) {
		t.Errorf("no inherited environment: got %v", got)
	}
}

func TestRunCase(t *testing.T) {
	proc := &sysfake.FakeProcess{Codes: []int{3}}
	c := Case{Args: []string{"--x", "@@TEMPDIR@@"}, Cwd: "sub", Stdin: "in @@JUST_EXECUTABLE@@"}
	got, err := runCase(context.Background(), proc, "/b/just", c, "/d", []string{"A=1"}, newPlaceholders("/d", "/b/just"))
	if err != nil || got.status != 3 {
		t.Fatalf("runCase = %+v, %v", got, err)
	}
	spec := proc.Specs[0]
	if !reflect.DeepEqual(spec.Argv, []string{"/b/just", "--x", "/d"}) || spec.Dir != "/d/sub" || !reflect.DeepEqual(spec.Env, []string{"A=1"}) {
		t.Fatalf("spec = %+v", spec)
	}
	stdin, _ := io.ReadAll(spec.Stdin)
	if string(stdin) != "in /b/just" {
		t.Fatalf("stdin = %q", stdin)
	}
	_, err = runCase(context.Background(), &sysfake.FakeProcess{Err: errors.New("boom")}, "/b", c, "/d", nil, newPlaceholders("/d", "/b"))
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("want start error, got %v", err)
	}
}

func TestCompareStatus(t *testing.T) {
	if compareStatus(1, 1) != nil || compareStatus(0, 1) == nil {
		t.Fatal("compareStatus")
	}
}

func TestCompareOutput(t *testing.T) {
	ph := newPlaceholders("/d.x", "/b")
	for name, tc := range map[string]struct {
		got            string
		exact, pattern *string
		ok             bool
	}{
		"exact match":        {"at /d.x\n", ptr("at @@TEMPDIR@@\n"), nil, true},
		"exact mismatch":     {"at /d.x", ptr("at @@TEMPDIR@@\n"), nil, false},
		"regex match":        {"at /d.x!", nil, ptr("^at @@TEMPDIR@@!$"), true},
		"regex is quoted":    {"at /dyx!", nil, ptr("^at @@TEMPDIR@@!$"), false},
		"regex mismatch":     {"nope", nil, ptr("^yes$"), false},
		"regex does not com": {"", nil, ptr("("), false},
	} {
		if err := compareOutput("stdout", tc.got, tc.exact, tc.pattern, ph); (err == nil) != tc.ok {
			t.Errorf("%s: err = %v, want ok=%v", name, err, tc.ok)
		}
	}
}

func TestCompareResponse(t *testing.T) {
	ph := newPlaceholders("/d", "/b")
	for name, tc := range map[string]struct {
		stdout, response string
		ok               bool
	}{
		"no response":      {"anything", "", true},
		"null response":    {"anything", "null", true},
		"equal":            {`{"a": ["/d", 1]}`, `{"a":["@@TEMPDIR@@",1]}`, true},
		"different":        {`{"a": 2}`, `{"a":1}`, false},
		"stdout not JSON":  {"x", `{"a":1}`, false},
		"bad expectation":  {"{}", `{`, false},
		"null field value": {`{"environment-variable":null}`, `{"environment-variable":null}`, true},
	} {
		if err := compareResponse(tc.stdout, json.RawMessage(tc.response), ph); (err == nil) != tc.ok {
			t.Errorf("%s: err = %v, want ok=%v", name, err, tc.ok)
		}
	}
}

func TestCompareFiles(t *testing.T) {
	ph := newPlaceholders("/d", "/b")
	fsys := sysfake.FakeFS{"/d/ok": "path /d\n", "/d/bad": "other"}
	enc := base64.StdEncoding.EncodeToString
	if err := compareFiles(fsys, "/d", map[string]string{"ok": enc([]byte("path @@TEMPDIR@@\n"))}, ph); err != nil {
		t.Fatal(err)
	}
	err := compareFiles(fsys, "/d", map[string]string{
		"bad":     enc([]byte("want")),
		"missing": enc([]byte("x")),
		"undec":   "!",
	}, ph)
	for _, want := range []string{"file bad", "expected file missing", "expected file undec"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("error %v does not mention %q", err, want)
		}
	}
}

func TestMaterialize(t *testing.T) {
	c := Case{
		Files:    map[string]string{"a/b.txt": "in @@TEMPDIR@@", "x.exe": "#!/bin/sh\n", "ro": "r", "plain.exe": ""},
		Modes:    map[string]string{"ro": "0400", "plain.exe": "0644"},
		Dirs:     []string{"empty", "deep/empty"},
		Symlinks: map[string]string{"abs": "@@TEMPDIR@@/a", "rel/dangle": "nowhere", "to-file": "@@TEMPDIR@@/x.exe"},
		Cwd:      "deep/cwd",
	}
	dir := materialize(t, c)
	if filepath.Base(dir) != tempDirName {
		t.Fatalf("dir %q is not named %q", dir, tempDirName)
	}
	data, err := os.ReadFile(filepath.Join(dir, "a/b.txt"))
	if err != nil || string(data) != "in "+dir {
		t.Fatalf("a/b.txt = %q, %v", data, err)
	}
	for name, want := range map[string]os.FileMode{"a/b.txt": 0o644, "x.exe": 0o755, "ro": 0o400, "plain.exe": 0o644} {
		assertMode(t, filepath.Join(dir, name), want)
	}
	for _, d := range []string{"deep/cwd", "empty", "deep/empty"} {
		assertEmptyDir(t, filepath.Join(dir, d))
	}
	for link, want := range map[string]string{"abs": dir + "/a", "rel/dangle": "nowhere", "to-file": dir + "/x.exe"} {
		assertLink(t, filepath.Join(dir, link), want)
	}
	if info, err := os.Stat(filepath.Join(dir, "abs")); err != nil || !info.IsDir() {
		t.Errorf("abs does not resolve to a directory: %v", err)
	}
}

// assertEmptyDir checks that path is a directory with no entries.
func assertEmptyDir(t *testing.T, path string) {
	t.Helper()
	if entries, err := os.ReadDir(path); err != nil || len(entries) != 0 {
		t.Errorf("%s: want an empty directory, got %v, %v", path, entries, err)
	}
}

// assertLink checks that path is a symbolic link to want.
func assertLink(t *testing.T, path, want string) {
	t.Helper()
	if got, err := os.Readlink(path); err != nil || got != want {
		t.Errorf("link %s -> %q, %v; want %q", path, got, err, want)
	}
}

func TestWriteInputsErrors(t *testing.T) {
	ph := newPlaceholders("/d", "")
	for name, c := range map[string]Case{
		"dir below a file":    {Files: map[string]string{"f": ""}, Dirs: []string{"f/d"}},
		"file below a file":   {Files: map[string]string{"f": "", "f/g": ""}},
		"cwd below a file":    {Files: map[string]string{"f": ""}, Cwd: "f/c"},
		"link over a file":    {Files: map[string]string{"f": ""}, Symlinks: map[string]string{"f": "x"}},
		"link below a file":   {Files: map[string]string{"f": ""}, Symlinks: map[string]string{"f/l": "x"}},
		"file in a ro dir":    {Files: map[string]string{"ro/f": ""}},
		"empty case succeeds": {},
	} {
		dir := t.TempDir()
		if name == "file in a ro dir" {
			if err := os.Mkdir(filepath.Join(dir, "ro"), 0o500); err != nil {
				t.Fatal(err)
			}
		}
		err := writeInputs(c, dir, ph)
		if wantErr := name != "empty case succeeds"; (err != nil) != wantErr {
			t.Errorf("%s: err = %v, want error %v", name, err, wantErr)
		}
	}
}

// assertMode checks the permission bits of path.
func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("%s: mode %v, want %v", path, got, want)
	}
}

// TestCheckDetectsMismatch runs the harness end to end against /bin/sh
// standing in for just, so it needs no JUST_GO_BIN: a case whose
// expectations match passes, and one that differs in every field fails on
// each of them.
func TestCheckDetectsMismatch(t *testing.T) {
	h := harness{bin: "/bin/sh", environ: os.Environ(), proc: sys.OSProcess{}, fsys: sys.OSFS{}}
	pass := Case{
		Args:          []string{"-c", `cat; echo "$K" >&2; printf x > out`},
		Env:           map[string]string{"K": "@@TEMPDIR@@"},
		Stdin:         "hi\n",
		Stdout:        ptr("hi\n"),
		StderrRegex:   ptr("^@@TEMPDIR@@\n$"),
		Status:        ptr(0),
		ExpectedFiles: map[string]string{"out": "eA=="},
		Response:      json.RawMessage("null"),
	}
	if err := h.check(t, pass); err != nil {
		t.Fatalf("matching case failed: %v", err)
	}
	fail := pass
	fail.Stdout, fail.StderrRegex, fail.Status = ptr("bye\n"), ptr("^nope$"), ptr(1)
	fail.ExpectedFiles = map[string]string{"out": "eQ=="}
	fail.Response = json.RawMessage(`{"a":1}`)
	err := h.check(t, fail)
	for _, want := range []string{"status", "stdout", "stderr", "response", "file out"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("error %v does not mention %s", err, want)
		}
	}
	if err := (harness{bin: "/no/such/just", proc: sys.OSProcess{}, fsys: sys.OSFS{}}).check(t, pass); err == nil {
		t.Error("want an error for a missing binary")
	}
}

func BenchmarkNewPlaceholders(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		newPlaceholders("/private/var/folders/t/just-test-tempdir000000", "/usr/local/bin/just")
	}
}

func BenchmarkCompareOutput(b *testing.B) {
	ph := newPlaceholders("/private/var/folders/t/just-test-tempdir000000", "/usr/local/bin/just")
	got := strings.Repeat("error: something at /private/var/folders/t/just-test-tempdir000000/justfile\n", 50)
	exact := strings.Repeat("error: something at @@TEMPDIR@@/justfile\n", 50)
	pattern := "(?s)^.*@@TEMPDIR@@/justfile\n$"
	b.ReportAllocs()
	for b.Loop() {
		if compareOutput("stdout", got, &exact, nil, ph) != nil || compareOutput("stdout", got, nil, &pattern, ph) != nil {
			b.Fatal("mismatch")
		}
	}
}
