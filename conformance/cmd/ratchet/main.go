// Command ratchet records conformance progress. It runs TestConformance
// against the binary named by JUST_GO_BIN, collects the ids of the cases
// that pass from `go test -json`, and merges them into the baseline file.
// It never removes an id, and it writes the file sorted, one id per line.
// TestBaseline then fails if any id in the baseline stops passing.
//
// Run it from the module root:
//
//	JUST_GO_BIN=bin/just-go go run ./conformance/cmd/ratchet
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"gitea.com/wimwian/just-go/internal/sys"
)

// conformanceTest is the test whose passing subtests are case ids.
const conformanceTest = "TestConformance"

// exit is os.Exit; tests replace it to observe main's exit status.
var exit = os.Exit

// writeFunc writes a whole file.
type writeFunc func(path string, data []byte) error

// options are the parsed command line.
type options struct {
	baseline string
	pkg      string
}

func main() {
	exit(run(os.Args[1:], os.Environ(), os.Stderr, sys.OSFS{}, sys.OSProcess{}, writeFile))
}

// writeFile writes data to path. Impure: it writes the filesystem.
func writeFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o644)
}

// run is the command: it returns 0 on success, 1 on failure, and 2 on a
// usage error. Impure: it runs `go test` through proc, reads the baseline
// through fsys, and writes it through write.
func run(args, environ []string, stderr io.Writer, fsys sys.FS, proc sys.Process, write writeFunc) int {
	opts, err := parseArgs(args, stderr)
	if err != nil {
		return 2
	}
	if lookupEnv(environ, "JUST_GO_BIN") == "" {
		fmt.Fprintln(stderr, "ratchet: JUST_GO_BIN must name the binary under test")
		return 2
	}
	if err := ratchet(opts, environ, stderr, fsys, proc, write); err != nil {
		fmt.Fprintln(stderr, "ratchet:", err)
		return 1
	}
	return 0
}

// parseArgs parses the command line. Pure apart from writing usage text to
// stderr.
func parseArgs(args []string, stderr io.Writer) (options, error) {
	var opts options
	flags := flag.NewFlagSet("ratchet", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&opts.baseline, "baseline", "conformance/baseline.txt", "baseline file to merge passing case ids into")
	flags.StringVar(&opts.pkg, "pkg", "./conformance", "package that holds "+conformanceTest)
	if err := flags.Parse(args); err != nil {
		return options{}, err
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "ratchet: unexpected arguments %q\n", flags.Args())
		return options{}, fmt.Errorf("unexpected arguments")
	}
	return opts, nil
}

// ratchet runs the conformance test and merges its passing ids into the
// baseline. Impure: it runs a process, reads, and writes files.
func ratchet(opts options, environ []string, stderr io.Writer, fsys sys.FS, proc sys.Process, write writeFunc) error {
	existing, err := fsys.ReadFile(opts.baseline)
	if err != nil {
		return err
	}
	var events bytes.Buffer
	_, err = proc.Run(context.Background(), sys.ProcessSpec{
		Argv:   []string{"go", "test", "-json", "-count=1", "-run", "^" + conformanceTest + "$", opts.pkg},
		Env:    environ,
		Stdout: &events,
		Stderr: stderr,
	})
	if err != nil {
		return fmt.Errorf("go test: %w", err)
	}
	passing, err := passingIDs(events.Bytes())
	if err != nil {
		return err
	}
	old := parseBaseline(string(existing))
	merged := mergeBaseline(old, passing)
	fmt.Fprintf(stderr, "ratchet: %d passing, %d in baseline (%d new)\n", len(passing), len(merged), len(merged)-len(old))
	return write(opts.baseline, []byte(formatBaseline(merged)))
}

// testEvent is the part of a `go test -json` event the ratchet reads.
type testEvent struct {
	Action string
	Test   string
}

// passingIDs returns the ids of the TestConformance subtests that passed,
// from `go test -json` output. Lines that are not JSON events (build
// output) are ignored. Pure.
func passingIDs(output []byte) ([]string, error) {
	var ids []string
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	prefix := conformanceTest + "/"
	for scanner.Scan() {
		var event testEvent
		if json.Unmarshal(scanner.Bytes(), &event) != nil {
			continue
		}
		if event.Action == "pass" && strings.HasPrefix(event.Test, prefix) {
			ids = append(ids, strings.TrimPrefix(event.Test, prefix))
		}
	}
	return ids, scanner.Err()
}

// parseBaseline returns the ids in a baseline file, one per line. Pure.
func parseBaseline(data string) []string {
	return strings.Fields(data)
}

// mergeBaseline returns the sorted union of old and passing without
// duplicates. It never drops an id of old. Pure.
func mergeBaseline(old, passing []string) []string {
	seen := make(map[string]bool, len(old)+len(passing))
	merged := make([]string, 0, len(old)+len(passing))
	for _, id := range append(append([]string{}, old...), passing...) {
		if !seen[id] {
			seen[id] = true
			merged = append(merged, id)
		}
	}
	sort.Strings(merged)
	return merged
}

// formatBaseline renders ids one per line. An empty baseline is an empty
// file. Pure.
func formatBaseline(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	return strings.Join(ids, "\n") + "\n"
}

// lookupEnv returns the value of key in a "KEY=VALUE" list, or "". The last
// entry wins, as in os/exec. Pure.
func lookupEnv(environ []string, key string) string {
	value := ""
	for _, entry := range environ {
		if k, v, ok := strings.Cut(entry, "="); ok && k == key {
			value = v
		}
	}
	return value
}
