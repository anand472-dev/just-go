// Package conformance holds the conformance corpus recorded from upstream's
// tests and the types that describe it. The corpus is data: one Case per
// upstream Test::status call, recorded by scripts/record-corpus.sh.
package conformance

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"gitea.com/wimwian/just-go/internal/sys"
)

// Placeholders that stand for machine-specific absolute paths in every string
// of a recorded case. A runner substitutes its own values before it runs a
// case and in the expectations. Inside StdoutRegex and StderrRegex the value
// is regex-quoted; everywhere else it is inserted as it is.
const (
	// TempDirToken is the case's temporary directory. The runner must create
	// it with symbolic links resolved, so that the plain and the canonical
	// spelling of the path are the same string.
	TempDirToken = "@@TEMPDIR@@"
	// TempDirParentToken is the directory that contains the temporary
	// directory.
	TempDirParentToken = "@@TEMPDIR_PARENT@@"
	// JustExecutableToken is the absolute path of the binary under test.
	JustExecutableToken = "@@JUST_EXECUTABLE@@"
)

// Case is one recorded invocation of the just binary and the result upstream
// expects. It mirrors one line of conformance/corpus/cases.jsonl.
type Case struct {
	// ID is "<upstream test name>#<n>" and is unique within the corpus.
	ID string `json:"id"`
	// Files are written into the case's temporary directory before the run.
	// Each file gets the mode FileMode returns: Modes when it names the file,
	// otherwise the corpus convention (ConventionalMode).
	Files map[string]string `json:"files"`
	// Dirs lists the empty directories upstream's test created, relative to
	// the temporary directory. A runner creates them before the run.
	// Directories that hold a file or a symbolic link are implied by it and
	// are not listed.
	Dirs []string `json:"dirs"`
	// Symlinks maps the path of a symbolic link, relative to the temporary
	// directory, to its target exactly as upstream created it. Upstream's
	// Test::symlink makes absolute targets inside the temporary directory, so
	// most targets start with TempDirToken; a runner substitutes it like any
	// other placeholder. A target may dangle.
	Symlinks map[string]string `json:"symlinks"`
	// Modes maps a path in Files to its permission bits as four octal digits,
	// such as "0400". It lists only files whose mode differs from
	// ConventionalMode.
	Modes map[string]string `json:"modes"`
	// Args is the full argument list, including a leading "--shell bash"
	// when the upstream test ran with its default shell.
	Args []string `json:"args"`
	// Env overlays the environment the child inherits. Upstream's child
	// inherits the full environment of the test process minus every
	// variable whose name starts with "JUST_" (just() in upstream's
	// tests/lib.rs removes them), and Env is then applied on top: a key in
	// Env replaces the inherited value. PATH is handled by PathExtra when
	// that is set.
	Env map[string]string `json:"env"`
	// PathExtra lists directories, relative to the temporary directory ("."
	// is the directory itself). Upstream's Test::path REPLACES PATH, so when
	// PathExtra is not empty the runner sets PATH to exactly these
	// directories, resolved against the temporary directory and joined with
	// the platform's path list separator. It does not prepend them to the
	// inherited PATH. The name is historical. When it is empty, PATH is
	// either not set by the case or is in Env.
	PathExtra []string `json:"path_extra"`
	// Cwd is the working directory relative to the temporary directory.
	Cwd   string `json:"cwd"`
	Stdin string `json:"stdin"`
	// Stdout and Stderr are exact expectations, set when the matching
	// regex is not.
	Stdout *string `json:"stdout"`
	Stderr *string `json:"stderr"`
	// StdoutRegex and StderrRegex are complete patterns, anchors included.
	StdoutRegex *string `json:"stdout_regex"`
	StderrRegex *string `json:"stderr_regex"`
	// Status is the expected exit status. It is a pointer so that a missing
	// value is detectable.
	Status *int `json:"status"`
	// ExpectedFiles maps a path in the temporary directory to the base64 of
	// its expected content after the run.
	ExpectedFiles map[string]string `json:"expected_files"`
	// Response is the JSON value upstream compared with stdout, or null.
	// Every recorded line carries the key, so a JSON null decodes to
	// []byte("null"), not to nil: Response != nil is true for every case.
	// Use HasResponse to tell the cases that compare a JSON response.
	Response json.RawMessage `json:"response"`
}

// Corpus file modes. A file whose name ends in ".exe" is executable, as
// upstream's Test::write_executable requires; every other file is 0644.
const (
	executableMode = "0755"
	plainMode      = "0644"
)

// modePattern is the form of a value in Case.Modes: four octal digits.
var modePattern = regexp.MustCompile(`^[0-7]{4}$`)

// ConventionalMode returns the mode the corpus assumes for a file when
// Case.Modes does not name it: "0755" for a name ending in ".exe", otherwise
// "0644". Pure.
func ConventionalMode(name string) string {
	if strings.HasSuffix(name, ".exe") {
		return executableMode
	}
	return plainMode
}

// FileMode returns the permission bits a runner gives the file name of the
// case: its entry in Modes, or ConventionalMode. It assumes a validated
// case. Pure.
func (c Case) FileMode(name string) fs.FileMode {
	mode, ok := c.Modes[name]
	if !ok {
		mode = ConventionalMode(name)
	}
	bits, _ := strconv.ParseUint(mode, 8, 32)
	return fs.FileMode(bits)
}

// HasResponse reports whether the case compares stdout with a JSON value,
// that is, whether Response holds something other than JSON null. Pure.
func (c Case) HasResponse() bool {
	return len(c.Response) > 0 && string(c.Response) != "null"
}

// ParseCases decodes JSON Lines into cases. It rejects unknown fields,
// blank lines, and data after the object on a line. Pure.
func ParseCases(data []byte) ([]Case, error) {
	if len(data) == 0 {
		return nil, nil
	}
	data = bytes.TrimSuffix(data, []byte("\n"))
	var cases []Case
	for i, line := range bytes.Split(data, []byte("\n")) {
		c, err := parseCase(line)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", i+1, err)
		}
		cases = append(cases, c)
	}
	return cases, nil
}

// parseCase decodes exactly one JSON object. Pure.
func parseCase(line []byte) (Case, error) {
	var c Case
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return Case{}, err
	}
	if dec.More() {
		return Case{}, errors.New("data after the case object")
	}
	return c, nil
}

// LoadCases reads and parses the corpus file at path. Impure: it reads
// through fsys.
func LoadCases(fsys sys.FS, path string) ([]Case, error) {
	data, err := fsys.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cases, err := ParseCases(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return cases, nil
}

// ValidateCorpus checks every case and that ids are unique. Pure.
func ValidateCorpus(cases []Case) error {
	seen := make(map[string]bool, len(cases))
	for _, c := range cases {
		if err := c.Validate(); err != nil {
			return fmt.Errorf("case %q: %w", c.ID, err)
		}
		if seen[c.ID] {
			return fmt.Errorf("duplicate id %q", c.ID)
		}
		seen[c.ID] = true
	}
	return nil
}

// Validate checks one case against the corpus schema. Pure.
func (c Case) Validate() error {
	if c.ID == "" {
		return errors.New("missing id")
	}
	if c.Status == nil {
		return errors.New("missing status")
	}
	if err := checkPathExtra(c.PathExtra); err != nil {
		return err
	}
	if err := checkOutput("stdout", c.Stdout, c.StdoutRegex); err != nil {
		return err
	}
	if err := checkOutput("stderr", c.Stderr, c.StderrRegex); err != nil {
		return err
	}
	for path, content := range c.ExpectedFiles {
		if _, err := base64.StdEncoding.DecodeString(content); err != nil {
			return fmt.Errorf("expected file %q: %w", path, err)
		}
	}
	return c.checkInputs()
}

// checkInputs validates Dirs, Symlinks, and Modes: every path is a clean
// relative path, no path is both a file, a directory, or a link, every link
// has a target, and every mode names a file and differs from the
// convention. Pure.
func (c Case) checkInputs() error {
	if err := c.checkDirs(); err != nil {
		return err
	}
	if err := c.checkSymlinks(); err != nil {
		return err
	}
	return c.checkModes()
}

// checkDirs validates Dirs. Pure.
func (c Case) checkDirs() error {
	seen := make(map[string]bool, len(c.Dirs))
	for _, dir := range c.Dirs {
		if err := checkRelPath("dirs", dir); err != nil {
			return err
		}
		if _, isFile := c.Files[dir]; isFile || seen[dir] {
			return fmt.Errorf("dirs entry %q is listed twice or is a file", dir)
		}
		seen[dir] = true
	}
	return nil
}

// checkSymlinks validates Symlinks. Pure.
func (c Case) checkSymlinks() error {
	for link, target := range c.Symlinks {
		if err := checkRelPath("symlinks", link); err != nil {
			return err
		}
		_, isFile := c.Files[link]
		if isFile || slices.Contains(c.Dirs, link) {
			return fmt.Errorf("symlink %q is also a file or a directory", link)
		}
		if target == "" {
			return fmt.Errorf("symlink %q has no target", link)
		}
	}
	return nil
}

// checkModes validates Modes. Pure.
func (c Case) checkModes() error {
	for name, mode := range c.Modes {
		if _, isFile := c.Files[name]; !isFile {
			return fmt.Errorf("mode for %q, which is not in files", name)
		}
		if !modePattern.MatchString(mode) {
			return fmt.Errorf("mode %q of %q is not four octal digits", mode, name)
		}
		if mode == ConventionalMode(name) {
			return fmt.Errorf("mode %q of %q is the convention and must be omitted", mode, name)
		}
	}
	return nil
}

// checkRelPath requires p to be a clean slash-separated path inside the
// temporary directory: not empty, not absolute, no backslash, and no "."
// or ".." element. field names the Case field for the error. Pure.
func checkRelPath(field, p string) error {
	if path.Clean(p) != p || path.IsAbs(p) || strings.Contains(p, `\`) ||
		p == "." || strings.HasPrefix(p+"/", "../") {
		return fmt.Errorf("%s entry %q is not a clean relative path", field, p)
	}
	return nil
}

// checkPathExtra requires every entry to be a non-empty path relative to the
// temporary directory that is not itself a path list. Pure.
func checkPathExtra(entries []string) error {
	for _, entry := range entries {
		if entry == "" || strings.HasPrefix(entry, "/") || strings.ContainsAny(entry, `:\`) {
			return fmt.Errorf("path_extra entry %q is not a relative directory", entry)
		}
	}
	return nil
}

// checkOutput requires exactly one of the exact value and the regex, and
// that the regex compiles. Pure.
func checkOutput(name string, exact, pattern *string) error {
	if (exact == nil) == (pattern == nil) {
		return fmt.Errorf("want exactly one of %s and %s_regex", name, name)
	}
	if pattern == nil {
		return nil
	}
	if _, err := regexp.Compile(*pattern); err != nil {
		return fmt.Errorf("%s_regex: %w", name, err)
	}
	return nil
}

// CaseModule returns the upstream test module a case id belongs to: the id up
// to its first "::", or up to its "#" when it has no module. Pure.
func CaseModule(id string) string {
	if module, _, found := strings.Cut(id, "::"); found {
		return module
	}
	module, _, _ := strings.Cut(id, "#")
	return module
}
