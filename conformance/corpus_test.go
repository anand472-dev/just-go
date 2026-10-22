package conformance

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"testing"

	"gitea.com/wimwian/just-go/internal/sys"
	"gitea.com/wimwian/just-go/internal/sys/sysfake"
)

const (
	corpusPath   = "corpus/cases.jsonl"
	upstreamTest = "../oracle/src/tests"
)

// notRecorded names the upstream test files that have #[test] functions but
// cannot reach the corpus. The recorder hooks Test::status; these files run
// the just binary directly (or run no binary at all), so they never call it.
var notRecorded = map[string]string{
	"edit":     "runs just directly with VISUAL set to external programs",
	"examples": "parses upstream's examples directory, outside any test tempdir",
	"print":    "runs just directly with a closed stdout pipe",
	"readme":   "checks upstream's README.md; it runs no just binary",
	"signals":  "runs just directly and delivers signals to the child",
}

const sampleLine = `{"args":["--list"],"cwd":"","dirs":[],"env":{},"expected_files":{},` +
	`"files":{"justfile":"a:\n"},"id":"alias::a#1","modes":{},"path_extra":[],"response":null,` +
	`"symlinks":{},` +
	`"status":0,"stderr":"","stderr_regex":null,"stdin":"","stdout":"x\n","stdout_regex":null}`

func lineWith(t *testing.T, mutate func(m map[string]any)) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(sampleLine), &m); err != nil {
		t.Fatal(err)
	}
	mutate(m)
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCorpusSchema(t *testing.T) {
	cases, err := LoadCases(sys.OSFS{}, corpusPath)
	if err != nil {
		t.Fatalf("load corpus: %v", err)
	}
	if err := ValidateCorpus(cases); err != nil {
		t.Fatalf("validate corpus: %v", err)
	}
	checkRecordedInputs(t, cases)
}

// checkRecordedInputs spot-checks that the recorder keeps the inputs that
// are not regular files: an empty directory, a symbolic link, and a file
// mode outside the corpus convention, each from a known upstream test.
func checkRecordedInputs(t *testing.T, cases []Case) {
	t.Helper()
	byID := make(map[string]Case, len(cases))
	for _, c := range cases {
		byID[c.ID] = c
	}
	if c := byID["init::write_error#1"]; !slices.Contains(c.Dirs, "justfile") {
		t.Errorf("init::write_error#1 dirs = %q, want the empty directory justfile", c.Dirs)
	}
	if c := byID["functions::canonicalize#1"]; c.Symlinks["foo"] != TempDirToken+"/justfile" {
		t.Errorf("functions::canonicalize#1 symlinks = %q, want foo -> %s/justfile", c.Symlinks, TempDirToken)
	}
	if c := byID["format::write_error#1"]; c.Modes["justfile"] != "0400" {
		t.Errorf("format::write_error#1 modes = %q, want justfile 0400", c.Modes)
	}
	if c := byID["which_function::ignores_nonexecutable_candidates#1"]; c.Modes["dummy/foo.exe"] != "0644" {
		t.Errorf("which_function::ignores_nonexecutable_candidates#1 modes = %q, want dummy/foo.exe 0644", c.Modes)
	}
}

// TestCorpusHasNoRecorderPaths guards the placeholders: no recorded string may
// hold a path of the machine that recorded the corpus.
func TestCorpusHasNoRecorderPaths(t *testing.T) {
	data, err := sys.OSFS{}.ReadFile(corpusPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"/var/folders/", "/private/var/", "just-go-corpus", "/src/target/release/just"} {
		if strings.Contains(string(data), marker) {
			t.Errorf("corpus contains %q; the recorder must replace it with a placeholder", marker)
		}
	}
	for _, token := range []string{TempDirToken, TempDirParentToken, JustExecutableToken} {
		if !strings.Contains(string(data), token) {
			t.Errorf("corpus never uses %s", token)
		}
	}
}

func TestCorpusIsNotEmpty(t *testing.T) {
	fsys := sys.OSFS{}
	cases, err := LoadCases(fsys, corpusPath)
	if err != nil {
		t.Fatalf("load corpus: %v", err)
	}
	if len(cases) == 0 {
		t.Fatal("corpus has no cases")
	}
	lib := upstreamTest + "/lib.rs"
	if ok, _ := fsys.IsFile(lib); !ok {
		t.Skip("oracle/src is not present; run `make oracle` to compare against upstream's test files")
	}
	missing, err := modulesWithoutCases(fsys, upstreamTest, cases)
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) > 0 {
		t.Fatalf("upstream test files with #[test] functions but no corpus case: %v", missing)
	}
}

// modulesWithoutCases lists the upstream test modules in dir that have
// #[test] functions, are not exempt, and have no case in cases.
func modulesWithoutCases(fsys sys.FS, dir string, cases []Case) ([]string, error) {
	lib, err := fsys.ReadFile(dir + "/lib.rs")
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for _, c := range cases {
		have[CaseModule(c.ID)] = true
	}
	var missing []string
	for _, name := range parseModules(string(lib)) {
		src, err := fsys.ReadFile(fmt.Sprintf("%s/%s.rs", dir, name))
		if err != nil {
			return nil, err
		}
		_, exempt := notRecorded[name]
		if strings.Contains(string(src), "#[test]") && !exempt && !have[name] {
			missing = append(missing, name)
		}
	}
	return missing, nil
}

// parseModules returns the modules declared in upstream's tests/lib.rs that
// are compiled on unix: those with no cfg attribute, or only cfg(unix).
func parseModules(src string) []string {
	var names []string
	cfg := ""
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "#[cfg("):
			cfg = line
		case strings.HasPrefix(line, "mod ") && strings.HasSuffix(line, ";"):
			if cfg == "" || cfg == "#[cfg(unix)]" {
				names = append(names, strings.TrimSuffix(strings.TrimPrefix(line, "mod "), ";"))
			}
			cfg = ""
		case strings.HasPrefix(line, "#["):
		default:
			cfg = ""
		}
	}
	return names
}

func TestParseModules(t *testing.T) {
	src := "mod a;\n#[cfg(unix)]\nmod b;\n#[cfg(windows)]\nmod c;\n#[macro_use]\nmod d;\n#[cfg(unix)]\nfn f() {}\nmod e;\n"
	got := strings.Join(parseModules(src), ",")
	if want := "a,b,d,e"; got != want {
		t.Fatalf("parseModules = %q, want %q", got, want)
	}
}

func TestModulesWithoutCases(t *testing.T) {
	fsys := sysfake.FakeFS{
		"t/lib.rs":  "mod a;\nmod b;\nmod c;\nmod edit;\nmod none;\n",
		"t/a.rs":    "#[test]\nfn x() {}\n",
		"t/b.rs":    "#[test]\nfn x() {}\n",
		"t/c.rs":    "fn helper() {}\n",
		"t/edit.rs": "#[test]\nfn x() {}\n",
		"t/none.rs": "#[test]\nfn x() {}\n",
	}
	cases := []Case{{ID: "a::x#1"}, {ID: "c::y#1"}}
	missing, err := modulesWithoutCases(fsys, "t", cases)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(missing, ","); got != "b,none" {
		t.Fatalf("missing = %q, want b,none", got)
	}
}

func TestModulesWithoutCasesErrors(t *testing.T) {
	if _, err := modulesWithoutCases(sysfake.FakeFS{}, "t", nil); err == nil {
		t.Fatal("want error for missing lib.rs")
	}
	fsys := sysfake.FakeFS{"t/lib.rs": "mod a;\n"}
	if _, err := modulesWithoutCases(fsys, "t", nil); err == nil {
		t.Fatal("want error for missing module file")
	}
}

func TestLoadCases(t *testing.T) {
	fsys := sysfake.FakeFS{"ok": sampleLine + "\n"}
	cases, err := LoadCases(fsys, "ok")
	if err != nil || len(cases) != 1 || cases[0].ID != "alias::a#1" {
		t.Fatalf("LoadCases = %v, %v", cases, err)
	}
	if _, err := LoadCases(fsys, "missing"); err == nil {
		t.Fatal("want error for missing file")
	}
	if _, err := LoadCases(sysfake.FakeFS{"bad": "{\n"}, "bad"); err == nil || !strings.Contains(err.Error(), "bad") {
		t.Fatalf("want parse error naming the path, got %v", err)
	}
}

func TestParseCasesRejects(t *testing.T) {
	extra := lineWith(t, func(m map[string]any) { m["surprise"] = 1 })
	trailing := sampleLine + " {}"
	for name, data := range map[string]string{
		"malformed":     "{\n",
		"unknown field": extra + "\n",
		"trailing data": trailing + "\n",
		"blank line":    sampleLine + "\n\n" + sampleLine + "\n",
	} {
		if _, err := ParseCases([]byte(data)); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestParseCasesEmpty(t *testing.T) {
	cases, err := ParseCases(nil)
	if err != nil || len(cases) != 0 {
		t.Fatalf("ParseCases(nil) = %v, %v", cases, err)
	}
}

func TestValidateCorpus(t *testing.T) {
	good, err := ParseCases([]byte(sampleLine))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateCorpus(good); err != nil {
		t.Fatalf("good corpus rejected: %v", err)
	}
	dup := append(append([]Case{}, good...), good...)
	if err := ValidateCorpus(dup); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate id: got %v", err)
	}
	for name, mutate := range map[string]func(m map[string]any){
		"no id":               func(m map[string]any) { m["id"] = "" },
		"no status":           func(m map[string]any) { delete(m, "status") },
		"no stdout":           func(m map[string]any) { delete(m, "stdout") },
		"no stderr":           func(m map[string]any) { delete(m, "stderr") },
		"both stdout":         func(m map[string]any) { m["stdout_regex"] = ".*" },
		"both stderr":         func(m map[string]any) { m["stderr_regex"] = ".*" },
		"bad stdout re":       func(m map[string]any) { delete(m, "stdout"); m["stdout_regex"] = "(" },
		"bad stderr re":       func(m map[string]any) { delete(m, "stderr"); m["stderr_regex"] = "(" },
		"bad base64 file":     func(m map[string]any) { m["expected_files"] = map[string]any{"f": "!"} },
		"absolute path_extra": func(m map[string]any) { m["path_extra"] = []string{"/tmp/x"} },
		"path list entry":     func(m map[string]any) { m["path_extra"] = []string{"a:b"} },
		"backslash entry":     func(m map[string]any) { m["path_extra"] = []string{`a\b`} },
		"empty path_extra":    func(m map[string]any) { m["path_extra"] = []string{""} },
		"empty dir":           func(m map[string]any) { m["dirs"] = []string{""} },
		"absolute dir":        func(m map[string]any) { m["dirs"] = []string{"/tmp"} },
		"dotdot dir":          func(m map[string]any) { m["dirs"] = []string{"../x"} },
		"parent dir":          func(m map[string]any) { m["dirs"] = []string{".."} },
		"dot dir":             func(m map[string]any) { m["dirs"] = []string{"."} },
		"unclean dir":         func(m map[string]any) { m["dirs"] = []string{"a//b"} },
		"backslash dir":       func(m map[string]any) { m["dirs"] = []string{`a\b`} },
		"dir is a file":       func(m map[string]any) { m["dirs"] = []string{"justfile"} },
		"duplicate dir":       func(m map[string]any) { m["dirs"] = []string{"a", "a"} },
		"absolute symlink":    func(m map[string]any) { m["symlinks"] = map[string]any{"/l": "t"} },
		"symlink is a file":   func(m map[string]any) { m["symlinks"] = map[string]any{"justfile": "t"} },
		"symlink is a dir":    func(m map[string]any) { m["dirs"] = []string{"l"}; m["symlinks"] = map[string]any{"l": "t"} },
		"empty link target":   func(m map[string]any) { m["symlinks"] = map[string]any{"l": ""} },
		"mode for no file":    func(m map[string]any) { m["modes"] = map[string]any{"other": "0400"} },
		"mode not octal":      func(m map[string]any) { m["modes"] = map[string]any{"justfile": "0489"} },
		"mode short":          func(m map[string]any) { m["modes"] = map[string]any{"justfile": "644"} },
		"mode is convention":  func(m map[string]any) { m["modes"] = map[string]any{"justfile": "0644"} },
	} {
		cases, err := ParseCases([]byte(lineWith(t, mutate)))
		if err != nil {
			t.Fatalf("%s: parse: %v", name, err)
		}
		if err := ValidateCorpus(cases); err == nil {
			t.Errorf("%s: want validation error", name)
		}
	}
}

func TestValidateAcceptsRegexForms(t *testing.T) {
	line := lineWith(t, func(m map[string]any) {
		delete(m, "stdout")
		delete(m, "stderr")
		m["stdout_regex"] = "(?s)^.*$"
		m["stderr_regex"] = "^(?s).*$"
		m["response"] = map[string]any{"environment-variable": nil}
		m["expected_files"] = map[string]any{"f": "YQ=="}
		m["path_extra"] = []string{".", "bin/dir"}
	})
	cases, err := ParseCases([]byte(line))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateCorpus(cases); err != nil {
		t.Fatal(err)
	}
}

func TestValidateAcceptsInputs(t *testing.T) {
	line := lineWith(t, func(m map[string]any) {
		m["files"] = map[string]any{"justfile": "a:\n", "bin/x.exe": "", "bin/y.exe": "", "ro": ""}
		m["dirs"] = []string{"empty", "a/b/.git"}
		m["symlinks"] = map[string]any{"link": TempDirToken + "/justfile", "a/rel": "../justfile"}
		m["modes"] = map[string]any{"bin/x.exe": "0644", "bin/y.exe": "0700", "ro": "0400"}
	})
	cases, err := ParseCases([]byte(line))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateCorpus(cases); err != nil {
		t.Fatal(err)
	}
}

func TestFileMode(t *testing.T) {
	c := Case{Modes: map[string]string{"ro": "0400", "bin/x.exe": "0644"}}
	for name, want := range map[string]fs.FileMode{
		"justfile":  0o644,
		"bin/y.exe": 0o755,
		"ro":        0o400,
		"bin/x.exe": 0o644,
	} {
		if got := c.FileMode(name); got != want {
			t.Errorf("FileMode(%q) = %#o, want %#o", name, got, want)
		}
	}
}

func TestHasResponse(t *testing.T) {
	for raw, want := range map[string]bool{"": false, "null": false, "{}": true, `"x"`: true, "0": true} {
		if got := (Case{Response: json.RawMessage(raw)}).HasResponse(); got != want {
			t.Errorf("HasResponse(%q) = %v, want %v", raw, got, want)
		}
	}
}

func TestConventionalMode(t *testing.T) {
	if got := ConventionalMode("a/b.exe"); got != "0755" {
		t.Errorf("ConventionalMode(a/b.exe) = %q", got)
	}
	if got := ConventionalMode("a/b.exe.txt"); got != "0644" {
		t.Errorf("ConventionalMode(a/b.exe.txt) = %q", got)
	}
}

func TestCaseModule(t *testing.T) {
	for id, want := range map[string]string{
		"alias::a#1":  "alias",
		"x::y::z#2":   "x",
		"top_level#1": "top_level",
		"no_hash":     "no_hash",
		"":            "",
	} {
		if got := CaseModule(id); got != want {
			t.Errorf("CaseModule(%q) = %q, want %q", id, got, want)
		}
	}
}

func BenchmarkParseCases(b *testing.B) {
	data := []byte(strings.Repeat(sampleLine+"\n", 2000))
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		if _, err := ParseCases(data); err != nil {
			b.Fatal(err)
		}
	}
}
