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
	"regexp"
	"strings"

	"gitea.com/wimwian/just-go/internal/sys"
)

// Case is one recorded invocation of the just binary and the result upstream
// expects. It mirrors one line of conformance/corpus/cases.jsonl.
type Case struct {
	// ID is "<upstream test name>#<n>" and is unique within the corpus.
	ID string `json:"id"`
	// Files are written into the case's temporary directory before the run.
	// Files whose name ends in ".exe" were executable upstream.
	Files map[string]string `json:"files"`
	// Args is the full argument list, including a leading "--shell bash"
	// when the upstream test ran with its default shell.
	Args []string `json:"args"`
	// Env is the exact environment of the run, apart from PathExtra.
	Env map[string]string `json:"env"`
	// PathExtra lists directories, relative to the temporary directory,
	// that upstream's Test::path put in PATH.
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
	Response json.RawMessage `json:"response"`
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
