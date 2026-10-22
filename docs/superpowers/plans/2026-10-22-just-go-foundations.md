# just-go Foundations Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a working `just-go` command and embeddable library for a defined core subset of the justfile language, with a conformance harness that tests every recorded upstream case against the pinned `just` v1.59.0 binary.

**Architecture:** One engine behind two front ends. A recorded corpus of upstream test cases is the oracle; a ratchet file pins which cases pass. The engine is a pipeline: lexer, parser, analyzer, evaluator, runner, with the loader finding and reading files. A public library wraps the engine, and the CLI wraps the library.

**Tech Stack:** Go 1.27 (stdlib only, no third-party dependencies in this plan). Rust toolchain for building the oracle only. POSIX `sh` for the oracle and corpus scripts.

**Spec:** `docs/superpowers/specs/2026-10-22-just-go-port-design.md`

**Scope of this plan:** Milestone 1 of the spec. Later subsystems each get their own plan (see Roadmap at the end). Language features in this plan are the subset named under "Core subset" below. Cases that use anything outside that subset stay out of the baseline until a later plan covers them.

## Global Constraints

- Module path: `gitea.com/wimwian/just-go`.
- Reference: casey/just v1.59.0, commit `d6fee036379c531369dc37f67f8d4640f6bf1e2d`.
- Priority order: compatibility, then security, then performance. Performance work never changes observable output.
- Feature compatibility: same recipe selection, command execution order, variable values, and exit codes as upstream. Error text need not match byte for byte, but error category and location must.
- Documented exception: strict path rules refuse some paths upstream allows. These arrive in a later plan, not this one.
- The library never calls `os.Exit`, never writes to `os.Stdout` or `os.Stderr` directly, and never parses `os.Args`.
- Library inputs come only through `Options`. Nothing is read from the process unless the caller passes it in. The CLI fills `Options` from the real process.
- The library accepts a `context.Context`. Cancelling it stops the run and cleans up child processes.
- The library returns errors as values, with the same categories and locations as the CLI.
- No package-level mutable state in the library.
- Standard library first. No third-party dependencies in this plan. The developer tool `gocyclo` is excepted; it is not a module dependency.
- License: MIT. Copyright holder not yet confirmed; see Task 0.
- Single static binary. No runtime dependencies beyond the configured shell.
- Public API limited to load, inspect, and run, with data results instead of formatted text.
- Pure and impure functions are segregated. A pure function does no I/O and reads no process, environment, clock, or filesystem state. Each doc comment says whether the function is pure or impure.
- Impure functions live in `internal/sys`, behind interfaces. The OS implementations and the test fakes live there too (`internal/sys/sysfake`). Pure packages never import `os`, `os/exec`, or `time`.
- Coverage: more than 95% statement coverage overall, and 100% function coverage (every function runs under a test). Checked by `scripts/check-coverage.sh`.
- Cyclomatic complexity below 10 for every function, checked by `gocyclo -over 9 .`.
- Performance benchmarks are built in: `Benchmark<Name>` functions for hot paths, run by `make bench`, with results recorded in `docs/benchmarks/`.

## Core subset

Language features covered by this plan. Anything else is out of scope here.

- Recipes with a name, optional parameters with string defaults, optional dependencies (no arguments on dependencies), and a body.
- Recipe body lines with `@` (suppress echo) and `-` (ignore failure) prefixes, and `{{expr}}` interpolation.
- Assignments `name := expr` with string literals, variable references, and `+` concatenation.
- Comments starting with `#`.
- `set quiet`, and `set shell := [ "string", ... ]`.
- Line handling: BOM, CRLF, and LF line endings.
- Justfile discovery by the names `justfile` and `.justfile`, searched upward from a directory. Upstream's order is `justfile` first.
- CLI flags: `-f/--justfile PATH`, `-l/--list`, `-q/--quiet`, and positional recipe names.

Not in this plan: conditionals, functions, `mod`, `import`, dotenv, backticks, `[attributes]`, other settings, shebang recipes, `--dump`, `--summary`, `--show`, `--evaluate`, completions, and unstable features.

## Review Focus

The five input classes most likely to bite a user of this milestone, each pinned by a test in the owning task.

1. **A recipe exits non-zero.** The status must reach the process exit code, and the error message must match upstream. Pinned by Task 8, `TestRunFailureExitStatusPropagates`.
2. **No justfile in the search path.** The CLI must report that no justfile was found and exit with upstream's status. Pinned by Task 9, `TestFindNoJustfileIsError`.
3. **Byte order mark and CRLF line endings.** A justfile saved on Windows must parse the same as its LF twin. Pinned by Task 4, `TestLexStripsBOMAndNormalizesCRLF`.
4. **Mixed tabs and spaces in a recipe body.** Upstream rejects this; `just-go` must reject it at the same location. Pinned by Task 5, `TestParseMixedIndentationIsError`.
5. **Multibyte text in strings and output.** Non-ASCII in string literals and recipe output must survive byte for byte. Pinned by Task 7, `TestEvaluatePreservesMultibyteText`.

## File Structure

- `go.mod`: module declaration and Go version.
- `LICENSE`, `README.md`, `.gitignore`: repository metadata.
- `Makefile`: `check`, `cover`, `cyclo`, and `bench` targets for the whole gate.
- `scripts/check-coverage.sh`: fails unless total statement coverage is above 95.0% and every function is at 100.0%.
- `scripts/build-oracle.sh`: clones and builds the pinned upstream binary into `oracle/bin/just`.
- `scripts/recorder.patch`: patch applied to a scratch copy of upstream's tests so every case is recorded.
- `scripts/record-corpus.sh`: runs the patched upstream suite and writes the corpus.
- `scripts/bench-startup.sh`: first measured startup baseline against the oracle.
- `conformance/corpus/cases.jsonl`: recorded cases, one JSON object per line.
- `conformance/corpus/README.md`: how the corpus was produced and its counts.
- `conformance/runner_test.go`: runs each case against the subject binary.
- `conformance/baseline.txt`: IDs of cases that must keep passing (ratchet).
- `conformance/cmd/ratchet/main.go`: adds newly passing IDs to the baseline. Never removes IDs.
- `conformance/oracle-exceptions.txt`: cases the oracle itself cannot pass consistently (for example, nondeterministic output), with reasons.
- `internal/diag/diag.go`: the one error type shared across packages.
- `internal/sys/sys.go`: the impure interfaces, `FS` and `Process`. The only place the rest of the code reaches the outside world.
- `internal/sys/os.go`: OS implementations of `FS` and `Process`. Process-group handling for Unix lives here.
- `internal/sys/sysfake/fake.go`: in-memory fakes of `FS` and `Process`, for tests.
- `internal/lexer/lexer.go`: tokens.
- `internal/ast/ast.go`: syntax tree.
- `internal/parser/parser.go`: tokens to syntax tree.
- `internal/analyzer/analyzer.go`: semantic checks.
- `internal/eval/eval.go`: expressions to strings.
- `internal/runner/runner.go`: recipe execution.
- `internal/loader/loader.go`: discovery and file loading.
- `justfile.go` (module root, package `just`): the public library API.
- `cmd/just-go/main.go`: the command.

Each file has one responsibility. Packages depend downward only: `diag` and `sys` at the bottom; `lexer`; `ast`; `parser`; `analyzer`; `eval`; `runner`; `loader`; the root package; then `cmd/just-go`.

---

### Task 0: Repository bootstrap

**Files:**
- Create: `go.mod`, `LICENSE`, `.gitignore`, `Makefile`, `internal/sys/sys.go`, `internal/sys/os.go`, `internal/sys/sysfake/fake.go`, `internal/sys/sys_test.go`
- Modify: `README.md` (currently empty)

**Interfaces:**
- Consumes: nothing.
- Produces: module path `gitea.com/wimwian/just-go`, Go 1.27. Every later package imports under that path.
- Produces, in `internal/sys` (impure; the only package that performs OS calls):

```go
type FS interface {
    ReadFile(path string) ([]byte, error)
    IsFile(path string) (bool, error) // true when path exists and is a regular file
}
type Process interface {
    Run(ctx context.Context, spec ProcessSpec) (exitCode int, err error)
}
type ProcessSpec struct {
    Argv   []string  // Argv[0] is the program
    Dir    string
    Env    []string  // exact environment, "KEY=VALUE"; nil means empty
    Stdin  io.Reader
    Stdout io.Writer
    Stderr io.Writer
}
// OS implementations: type OSFS struct{}, type OSProcess struct{}.
// sysfake: type FakeFS (in-memory map of path to content), type FakeProcess (records each ProcessSpec and returns a scripted exit code).
```

- [ ] **Step 1: Confirm the directory is a git repository, and initialize it if not**

Run: `cd /Users/pancha/Projects/just-go && (git rev-parse --is-inside-work-tree 2>/dev/null || git init -b main)`
Expected: `true`, or `Initialized empty Git repository`. Every later commit step depends on this.

- [ ] **Step 2: Write `go.mod`**

```
module gitea.com/wimwian/just-go

go 1.27
```

- [ ] **Step 3: Write `LICENSE` with the MIT text**

Use the standard MIT text. The copyright line is `Copyright (c) 2026 <copyright holder>`. **Leave `<copyright holder>` as the literal placeholder until the owner supplies the name.** Do not guess it from the module path or the machine user.

- [ ] **Step 4: Write `.gitignore`**

Ignore `bin/`, `oracle/src/`, `oracle/bin/`, `*.test`, and `.DS_Store`. Do not ignore `conformance/corpus/`, because the corpus is committed.

- [ ] **Step 5: Write `Makefile` with the targets used later**

Also install the complexity checker as a developer tool, not a module dependency: `go install github.com/fzipp/gocyclo/cmd/gocyclo@<pinned version>`. Choose the version when you run this step and record it in the Makefile as `GOCYCLO_VERSION`.

Targets: `build` (builds `bin/just-go` from `./cmd/just-go`), `test` (`go test ./...`), `vet` (`go vet ./...`), `oracle` (runs `scripts/build-oracle.sh`), `corpus` (runs `scripts/record-corpus.sh`), `conformance` (depends on `build`, runs `JUST_GO_BIN=bin/just-go go test ./conformance/...`), `cover` (runs `scripts/check-coverage.sh`), `cyclo` (runs `gocyclo -over 9 .`), `bench` (runs `go test -bench=. -benchmem ./...` and writes the output to `docs/benchmarks/go-bench.txt`), and `check` (runs `vet`, `test`, `conformance`). Leave bodies empty where the target's script does not exist yet; later tasks fill them in.

- [ ] **Step 6: Write the README**

Replace the empty file. Contents: one paragraph saying what `just-go` is and that it is a port of `casey/just` v1.59.0, a credit line for upstream, the module path, and a note that the project is in its foundations milestone.

- [ ] **Step 7: Write the impure boundary package**

Write `internal/sys/sys.go` with the `FS` and `Process` interfaces and the `ProcessSpec` type, exactly as in Interfaces above. Write `internal/sys/os.go`: `OSFS` reads files and checks for regular files with `os`; `OSProcess` runs `spec.Argv` with `os/exec`, using `exec.CommandContext` so cancellation kills the child, and `SysProcAttr` to create a process group on Unix so the whole group is killed, not just the shell. Write `internal/sys/sysfake/fake.go`: `FakeFS` (a map of path to content), and `FakeProcess` (records each `ProcessSpec` and returns the exit code it was scripted to return).

`internal/sys/sys_test.go`:

```go
func TestOSFSReadsAndDetectsFiles(t *testing.T)     // writes a temp file; ReadFile returns its bytes; IsFile is true for it and false for a directory
func TestOSProcessRunsWithExactEnv(t *testing.T)    // Env ["A=1"]; a shell prints ${A-unset} and ${HOME-unset} -> "1 unset"
func TestOSProcessCancelKillsGroup(t *testing.T)    // "sleep 30" cancelled after 100ms -> returns promptly with a context error
func TestFakeProcessRecordsSpec(t *testing.T)       // FakeProcess returns the scripted code and records argv, dir, env
func TestFakeFSMissingFileIsError(t *testing.T)     // FakeFS.ReadFile on a missing path returns an error
```

Run: `go test ./internal/sys -v`
Expected: PASS for all five tests.

- [ ] **Step 8: Verify the module builds**

Run: `go build ./... ; go vet ./...`
Expected: no output and exit 0. There are no packages yet, so `go build ./...` succeeds with no packages.

- [ ] **Step 9: Commit**

```bash
git add go.mod LICENSE .gitignore Makefile README.md internal/sys
git commit -m "chore: bootstrap just-go module"
```

---

### Task 1: Build the pinned oracle

**Files:**
- Create: `scripts/build-oracle.sh`

**Interfaces:**
- Consumes: nothing.
- Produces: an executable at `oracle/bin/just`, built from the pinned commit. Later tasks invoke it as `JUST_ORACLE=oracle/bin/just`.

- [ ] **Step 1: Write the failing check**

Run: `test -x oracle/bin/just && oracle/bin/just --version`
Expected: FAIL, because the binary does not exist yet (`No such file or directory`).

- [ ] **Step 2: Write `scripts/build-oracle.sh`**

The script does this in order, and stops with a non-zero status on any failure:
1. Sets `PIN=d6fee036379c531369dc37f67f8d4640f6bf1e2d` and `REPO=https://github.com/casey/just.git`.
2. If `oracle/src` is missing, clones `REPO` into it. Then runs `git -C oracle/src fetch --depth 1 origin $PIN` and `git -C oracle/src checkout --detach $PIN`.
3. Checks `git -C oracle/src rev-parse HEAD` equals `$PIN`. If not, it exits 1 with `oracle commit mismatch`.
4. Runs `cargo build --release --locked --bin just` inside `oracle/src`.
5. Copies `oracle/src/target/release/just` to `oracle/bin/just`, creating `oracle/bin` as needed.

- [ ] **Step 3: Run the script**

Run: `sh scripts/build-oracle.sh`
Expected: exit 0. It takes several minutes the first time, because it compiles upstream.

- [ ] **Step 4: Verify the version**

Run: `oracle/bin/just --version`
Expected: `just 1.59.0`. If the printed string differs, record the exact string in `conformance/corpus/README.md` in Task 2 and use it as the version check. The pin is the commit, not the printed string.

- [ ] **Step 5: Commit**

```bash
git add scripts/build-oracle.sh
git commit -m "build: add script that builds the pinned upstream oracle"
```

---

### Task 2: Record the conformance corpus from upstream's tests

**Files:**
- Create: `scripts/recorder.patch`, `scripts/record-corpus.sh`, `conformance/corpus/cases.jsonl`, `conformance/corpus/README.md`
- Test: `conformance/corpus_test.go`

**Interfaces:**
- Consumes: `oracle/src` from Task 1.
- Produces: `conformance/corpus/cases.jsonl`. Each line is one JSON object with this schema:

```
{
  "id":          "<test thread name>#<n>",   // unique within the corpus
  "files":       {"<relpath>": "<content>"}, // written into the case tempdir before running
  "args":        ["<arg>", ...],
  "env":         {"<KEY>": "<value>"},
  "path_extra":  ["<dir>", ...],             // prepended to PATH, when the case sets .path()
  "cwd":         "<relpath or empty>",
  "stdin":       "<string>",
  "stdout":      "<exact expected stdout>",  // present when stdout_regex is absent
  "stderr":      "<exact expected stderr>",  // present when stderr_regex is absent
  "stdout_regex": "<regex or null>",
  "stderr_regex": "<regex or null>",
  "status":      <integer exit status>,
  "expected_files": {"<relpath>": "<base64 bytes>"},
  "response":    <JSON value or null>        // present for cases that compare a JSON response
}
```

- [ ] **Step 1: Write the failing schema test**

`conformance/corpus_test.go`:

```go
func TestCorpusSchema(t *testing.T)        // every line decodes into Case; ids are unique; status is set on every case
func TestCorpusIsNotEmpty(t *testing.T)    // at least one case per upstream test file that has #[test] functions
```

Run: `go test ./conformance -run TestCorpus`
Expected: FAIL, because the corpus file does not exist yet.

- [ ] **Step 2: Write `scripts/recorder.patch`**

The patch applies to upstream's `tests/test.rs`. In `Test::status`, immediately after `output_stderr` is computed, it appends one JSON line to the file named by the environment variable `JUST_RECORD_FILE`. The line carries every field in the schema. It uses `std::thread::current().name()` for the test name and a per-thread counter for `<n>`. It records the stdout and stderr the harness actually compares, after unindent. When `JUST_RECORD_FILE` is unset, the patch does nothing, so ordinary `cargo test` behaves as before.

- [ ] **Step 3: Write `scripts/record-corpus.sh`**

The script:
1. Copies `oracle/src` to a scratch directory outside the repository.
2. Applies `scripts/recorder.patch` to the copy with `patch -p1`.
3. Runs `JUST_RECORD_FILE=<scratch>/raw.jsonl cargo test --release` in the copy. It does not need every test to pass; the recorder captures a case before the comparison runs.
4. Sorts `raw.jsonl` by `id`, drops exact duplicates, and writes `conformance/corpus/cases.jsonl`.
5. Writes the counts into `conformance/corpus/README.md`: total cases, cases with regex comparisons, cases with JSON responses, and the oracle version string from Task 1.

- [ ] **Step 4: Run the recorder**

Run: `sh scripts/record-corpus.sh`
Expected: exit 0, and `conformance/corpus/cases.jsonl` is non-empty. Upstream's own tests may print failures for cases that use features the oracle does not have on this platform. That is acceptable; the recorder still captured them.

- [ ] **Step 5: Run the schema test**

Run: `go test ./conformance -run TestCorpus -v`
Expected: PASS for both tests.

- [ ] **Step 6: Commit**

```bash
git add scripts/recorder.patch scripts/record-corpus.sh conformance/corpus conformance/corpus_test.go
git commit -m "test: record upstream v1.59.0 test cases as a conformance corpus"
```

---

### Task 3: Conformance runner and ratchet

**Files:**
- Create: `conformance/runner_test.go`, `conformance/baseline.txt` (empty to start), `conformance/cmd/ratchet/main.go`, `conformance/oracle-exceptions.txt`
- Modify: `Makefile` (fill in the `conformance` target if Task 0 left it empty)

**Interfaces:**
- Consumes: `conformance/corpus/cases.jsonl` (Task 2) and the `Case` type from `conformance/corpus_test.go`.
- Produces:
  - `TestConformance`: runs every case against the binary named by `JUST_GO_BIN`.
  - `TestBaseline`: fails if any ID in `baseline.txt` does not pass.
  - `cmd/ratchet`: appends IDs that pass now to `baseline.txt`, keeps existing lines, and writes the file sorted.

Comparison rules, in order:
1. Write `files` into a fresh temporary directory. Run `cwd` relative to it.
2. Set the environment to exactly `env`, plus `PATH` as `path_extra` prepended to the current `PATH`. Do not inherit the rest of the environment. This matches the spec's embedding rule.
3. Run `JUST_GO_BIN` with `args`, with `stdin` piped in.
4. Compare the exit status. Then compare stdout and stderr: exact match, or `regexp.MatchString` when a regex field is set. For `response`, decode stdout as JSON and compare with `reflect.DeepEqual` after decoding the expected value.
5. Compare each `expected_files` entry byte for byte after base64 decoding.

- [ ] **Step 1: Write the failing tests**

`conformance/runner_test.go` contains `TestConformance`, which loops over all cases and calls `t.Run(id, ...)`, and `TestBaseline`, which reads `baseline.txt`. When `JUST_GO_BIN` is unset, both tests call `t.Skip`. The rules above are the behavior under test.

Run: `JUST_GO_BIN=oracle/bin/just go test ./conformance -run TestConformance -v`
Expected: FAIL, because the runner has no implementation yet.

- [ ] **Step 2: Implement the runner to the rules above**

One function per rule keeps the comparisons separate: `materialize`, `buildEnv`, `runCase`, `compareStatus`, `compareOutput`, `compareFiles`. Keep `TestConformance` a thin loop.

- [ ] **Step 3: Validate the harness against the oracle itself**

Run: `JUST_GO_BIN=oracle/bin/just go test ./conformance -run TestConformance`
Expected: every case passes, except cases listed in `conformance/oracle-exceptions.txt`. The oracle must pass its own corpus. A failure here means the harness is wrong, not the oracle. Fix the harness. Add a case to `oracle-exceptions.txt` only when the oracle's output is nondeterministic (for example, timestamps or PIDs), and write the reason next to it.

- [ ] **Step 4: Implement the ratchet**

`conformance/cmd/ratchet/main.go` runs `TestConformance` with `JUST_GO_BIN` set, collects passing IDs from `go test -json`, merges them with `baseline.txt`, and rewrites the file sorted. It never removes an ID. Test it: run it once against the oracle and check that `baseline.txt` now lists the oracle's passing cases.

- [ ] **Step 5: Verify `TestBaseline` with the oracle**

Run: `JUST_GO_BIN=oracle/bin/just go test ./conformance -run TestBaseline`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add conformance/runner_test.go conformance/baseline.txt conformance/cmd/ratchet/main.go conformance/oracle-exceptions.txt Makefile
git commit -m "test: conformance runner, oracle validation, and ratchet baseline"
```

---

### Task 4: Lexer, with BOM and CRLF handling

**Files:**
- Create: `internal/diag/diag.go`, `internal/lexer/lexer.go`
- Test: `internal/lexer/lexer_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces:

```go
// package diag
type Kind int // KindParse, KindAnalysis, KindRuntime, KindUsage
type Error struct {
    Kind Kind
    File string
    Line int // 1-based
    Col  int // 1-based, in runes
    Msg  string
}
func (e *Error) Error() string // "<file>:<line>:<col>: <msg>"

// package lexer
type TokenKind int
const (
    Ident TokenKind = iota
    String      // Text is the unescaped contents
    Assign      // :=
    Equals      // =
    Colon
    Plus
    LParen
    RParen
    Comma
    At          // @ prefix
    Dash        // - prefix
    Interp      // {{
    Close       // }}
    Indent      // leading whitespace of a recipe body line; Text is the exact whitespace
    Text        // literal text inside a recipe body; Text is the raw text
    Newline
    Comment     // Text is the comment body without '#'
    EOF
)
type Token struct {
    Kind TokenKind
    Text string
    Line int
    Col  int
}
func Lex(file string, src []byte) ([]Token, error) // errors are *diag.Error with KindParse
```

- [ ] **Step 1: Write the failing tests**

```go
func TestLexSimpleAssignment(t *testing.T)           // `x := "a"` -> Ident x, Assign, String a, Newline, EOF
func TestLexComment(t *testing.T)                    // `# hi` -> Comment " hi", Newline, EOF
func TestLexRecipeBodyIndent(t *testing.T)           // body line "\tcargo build" -> Indent "\t", Text "cargo build"
func TestLexStripsBOMAndNormalizesCRLF(t *testing.T) // "\xEF\xBB\xBFx := \"a\"\r\n" tokenizes identically to "x := \"a\"\n"; columns match
```

`BenchmarkLex` lexes a generated 1,000-line justfile and reports allocations. `Lex` is pure.

Each test compares the full token slice, kind by kind and text by text.

Run: `go test ./internal/lexer -v`
Expected: FAIL, because `Lex` does not exist yet.

- [ ] **Step 2: Implement `diag` and `Lex`**

Strip a leading UTF-8 BOM before lexing. Treat `\r\n` as `\n`. Track line and column in runes, so multibyte text keeps correct columns. Keep the lexer a single pass over the bytes. Indentation is only significant at the start of a line inside a recipe body; the lexer emits `Indent` for leading whitespace and the parser decides whether the line is a body line.

- [ ] **Step 3: Run the tests**

Run: `go test ./internal/lexer -v`
Expected: PASS for all four tests.

- [ ] **Step 4: Commit**

```bash
git add internal/diag internal/lexer
git commit -m "feat: lexer for the core subset, with BOM and CRLF handling"
```

---

### Task 5: Parser, with mixed-indentation errors and a fuzz target

**Files:**
- Create: `internal/ast/ast.go`, `internal/parser/parser.go`
- Test: `internal/parser/parser_test.go`, `internal/parser/fuzz_test.go`

**Interfaces:**
- Consumes: `lexer.Lex`, `diag.Error` (Task 4).
- Produces:

```go
// package ast
type Justfile struct {
    File        string
    Settings    Settings          // Quiet bool; Shell []string (nil means default)
    Assignments []Assignment      // in source order
    Recipes     []Recipe          // in source order
}
type Settings struct {
    Quiet bool
    Shell []string
}
type Assignment struct {
    Name  string
    Value Expr
    Line  int
}
type Recipe struct {
    Name   string
    Doc    string   // comment immediately above the recipe, empty if none
    Params []Param
    Deps   []string
    Lines  []Line
    Line   int
}
type Param struct {
    Name    string
    Default Expr // nil when the parameter has no default
}
type Line struct {
    Prefix Prefix // PrefixNone, PrefixAt, PrefixDash, PrefixAtDash
    Parts  []Expr // literal text (StringLit) and interpolations (Var, Concat)
    Line   int
}
type Prefix int
type Expr interface{ exprNode() }
type StringLit struct{ Value string }
type Var struct{ Name string }
type Concat struct{ Left, Right Expr }

// package parser
func Parse(file string, src []byte) (*ast.Justfile, error) // errors are *diag.Error with KindParse
```

- [ ] **Step 1: Write the failing tests**

```go
func TestParseRecipeWithParamsAndDeps(t *testing.T)  // "build target=\"debug\": test\n  cargo build {{target}}\n"
func TestParseLinePrefixes(t *testing.T)             // "@echo a", "-false", "@-false" map to the four Prefix values
func TestParseSetShell(t *testing.T)                 // `set shell := ["bash", "-cu"]` -> Settings.Shell = ["bash", "-cu"]
func TestParseSetQuiet(t *testing.T)                 // `set quiet` -> Settings.Quiet = true
func TestParseDocComment(t *testing.T)               // "# build it\nbuild:\n  x" -> Recipe.Doc = "build it"
func TestParseMixedIndentationIsError(t *testing.T)  // body with "\tone" then "  two" -> *diag.Error, Kind KindParse, Line of "  two"
```

For `TestParseMixedIndentationIsError`, the message text is taken from the matching corpus case (search `conformance/corpus/cases.jsonl` for the case that uses mixed indentation). Do not invent the wording. Assert the category and the line.

Run: `go test ./internal/parser -v`
Expected: FAIL, because `Parse` does not exist yet.

- [ ] **Step 2: Implement `ast` and `Parse`**

Parse the Plan 1 subset only. A recipe body is every following line whose first token is `Indent`, until a blank line that is followed by a non-indented line, or the next non-indented line. Record the first body line's indentation kind; any later body line that mixes tab and space with it is a parse error at that line.

- [ ] **Step 3: Write the fuzz target and benchmark**

`BenchmarkParse` in `internal/parser/bench_test.go` parses a generated 1,000-line justfile and reports allocations. `Parse` is pure.

`internal/parser/fuzz_test.go`:

```go
func FuzzParse(f *testing.F) // seeds: the six test inputs above; property: Parse never panics
```

- [ ] **Step 4: Run the tests and a fuzz pass**

Run: `go test ./internal/parser -v`
Expected: PASS for all six tests.

Run: `go test ./internal/parser -run=^$ -fuzz=FuzzParse -fuzztime=30s`
Expected: completes with no crash. Any crash input is saved under `testdata/fuzz/FuzzParse/` and must be fixed before the task is done.

- [ ] **Step 5: Commit**

```bash
git add internal/ast internal/parser
git commit -m "feat: parser for the core subset, with mixed-indentation errors and fuzzing"
```

---

### Task 6: Analyzer

**Files:**
- Create: `internal/analyzer/analyzer.go`
- Test: `internal/analyzer/analyzer_test.go`

**Interfaces:**
- Consumes: `ast.Justfile`, `diag.Error` (Tasks 4 and 5).
- Produces: `func Analyze(jf *ast.Justfile) error`. Errors are `*diag.Error` with `KindAnalysis`. It returns the first error in source order.

Checks, in order: duplicate recipe names; duplicate assignment names; references to undefined variables inside expressions; dependencies that name no recipe. Each check reports its line.

- [ ] **Step 1: Write the failing tests**

```go
func TestAnalyzeDuplicateRecipe(t *testing.T)       // two recipes named "build" -> KindAnalysis, Line of second
func TestAnalyzeDuplicateAssignment(t *testing.T)   // x := "a" twice -> KindAnalysis
func TestAnalyzeUndefinedVariable(t *testing.T)     // body uses {{nope}} -> KindAnalysis naming "nope"
func TestAnalyzeUnknownDependency(t *testing.T)     // deps on "missing" -> KindAnalysis naming "missing"
func TestAnalyzeAcceptsValidJustfile(t *testing.T)  // a valid file returns nil
```

Message text for each error is taken from the matching corpus case where one exists. Assert category and line otherwise.

Run: `go test ./internal/analyzer -v`
Expected: FAIL, because `Analyze` does not exist yet.

- [ ] **Step 2: Implement `Analyze`**

One helper per check. Keep the source-order walk in `Analyze` itself.

- [ ] **Step 3: Run the tests**

Run: `go test ./internal/analyzer -v`
Expected: PASS for all five tests.

- [ ] **Step 4: Commit**

```bash
git add internal/analyzer
git commit -m "feat: analyzer for duplicate names, undefined variables, and unknown dependencies"
```

---

### Task 7: Evaluator, with multibyte text

**Files:**
- Create: `internal/eval/eval.go`
- Test: `internal/eval/eval_test.go`

**Interfaces:**
- Consumes: `ast.Expr` (Task 5), `diag.Error` (Task 4).
- Produces:

```go
// package eval
type Scope map[string]string // evaluated assignment values, by name
func Evaluate(e ast.Expr, scope Scope) (string, error) // errors are *diag.Error with KindRuntime
```

`Evaluate` handles `StringLit`, `Var`, and `Concat`. A `Var` with no entry in `scope` is an error. The evaluator does not know about recipes, shells, or the environment.

- [ ] **Step 1: Write the failing tests**

```go
func TestEvaluateLiteral(t *testing.T)                    // StringLit "hi" -> "hi"
func TestEvaluateConcat(t *testing.T)                     // Concat("a","b") -> "ab"
func TestEvaluateVarFromScope(t *testing.T)               // Var "x", scope{x:"1"} -> "1"
func TestEvaluateUndefinedVar(t *testing.T)               // Var "y" not in scope -> KindRuntime
func TestEvaluatePreservesMultibyteText(t *testing.T)     // StringLit "héllo ✓ 日本" -> identical bytes
```

Run: `go test ./internal/eval -v`
Expected: FAIL, because `Evaluate` does not exist yet.

Also add `BenchmarkEvaluate`: concatenation of ten variables over a ten-entry scope. `Evaluate` is pure.

- [ ] **Step 2: Implement `Evaluate`**

A switch on the expression type, recursing on `Concat`. Strings are Go strings, which are byte sequences; no conversion is applied, so multibyte text passes through unchanged.

- [ ] **Step 3: Run the tests**

Run: `go test ./internal/eval -v`
Expected: PASS for all five tests.

- [ ] **Step 4: Commit**

```bash
git add internal/eval
git commit -m "feat: evaluator for literals, variables, and concatenation"
```

---

### Task 8: Runner, with dependency order, echo, prefixes, and exit status

**Files:**
- Create: `internal/runner/runner.go`
- Test: `internal/runner/runner_test.go`

**Interfaces:**
- Consumes: `ast.Justfile`, `eval.Evaluate`, `eval.Scope` (Tasks 5 and 7), `diag.Error` (Task 4).
- Produces:

```go
// package runner
type Plan struct {
    Justfile *ast.Justfile
    Recipes  []string   // requested recipe names; empty means the first recipe in the file
    Args     []string   // positional arguments for the requested recipe's parameters
    Env      []string   // exact environment for children, as "KEY=VALUE"; nil means empty
    WorkDir  string
    Stdin    io.Reader
    Stdout   io.Writer
    Stderr   io.Writer
    Quiet    bool       // overrides set quiet when true
    Exec     sys.Process // nil uses the OS implementation from internal/sys
}
type Result struct {
    ExitCode int
}
func Run(ctx context.Context, plan Plan) (Result, error)
```

Rules:
- Dependencies run before the recipe that names them, each at most once per invocation, in declaration order.
- Before each line runs, the rendered command line is written to `Stderr`, unless the line has `@` or `Quiet` is set.
- `@` suppresses the echo. `-` ignores a non-zero status for that line and continues.
- A non-zero status from a line stops the recipe and the invocation. `Result.ExitCode` is that status. The failure message is written to `Stderr`, using the wording from the matching corpus case for "failed on line N with exit code M".
- Default shell is `sh -cu`, unless `Settings.Shell` is set. Confirm the default against the corpus before relying on it.
- Cancelling `ctx` kills running children and returns `ctx.Err()`.

- [ ] **Step 1: Write the failing tests**

```go
func TestRunDependenciesRunOnce(t *testing.T)            // a depends on b and c; b depends on c; c runs once
func TestRunEchoesCommandsToStderr(t *testing.T)         // "echo hi" echoes "echo hi" on Stderr, prints "hi" on Stdout
func TestRunAtPrefixSuppressesEcho(t *testing.T)         // "@echo hi" prints only "hi"
func TestRunQuietSuppressesEcho(t *testing.T)            // Plan.Quiet = true suppresses every echo
func TestRunDashPrefixIgnoresFailure(t *testing.T)       // "-false" then "echo after" -> "after" runs; exit 0
func TestRunFailureExitStatusPropagates(t *testing.T)    // "exit 3" -> ExitCode 3; Stderr has the corpus wording
func TestRunCancelKillsChild(t *testing.T)               // "sleep 30", cancel after 100ms -> returns ctx.Err() within 2s
func TestRunEnvIsExactlyPlanEnv(t *testing.T)            // Plan.Env = ["A=1"]; "echo ${A-unset} ${HOME-unset}" -> "1 unset"
```

Each test uses `t.TempDir()` for `WorkDir`, and a `Plan` built by a small helper. Most tests set `Exec` to `sysfake.FakeProcess`. `TestRunCancelKillsChild` uses the OS implementation, to prove real children are killed. Also add `BenchmarkResolveOrder`: dependency ordering over a 200-recipe chain. `resolveOrder` is pure.

Run: `go test ./internal/runner -v`
Expected: FAIL, because `Run` does not exist yet.

- [ ] **Step 2: Implement `Run`**

Split the helpers by purity. Pure: `resolveOrder` (dependency order with a visited set) and `renderLine` (uses `eval`). Impure: `execLine`, which builds a `sys.ProcessSpec` and calls `Plan.Exec`. `execLine` never calls `os/exec` itself; the OS implementation in `internal/sys` handles cancellation and the process group. Windows handling is a later plan.

- [ ] **Step 3: Run the tests**

Run: `go test ./internal/runner -v`
Expected: PASS for all eight tests.

- [ ] **Step 4: Commit**

```bash
git add internal/runner
git commit -m "feat: runner for dependency order, echo, line prefixes, and exit status"
```

---

### Task 9: Loader, with justfile discovery

**Files:**
- Create: `internal/loader/loader.go`
- Test: `internal/loader/loader_test.go`

**Interfaces:**
- Consumes: `parser.Parse`, `analyzer.Analyze`, `diag.Error` (Tasks 4, 5, and 6).
- Produces:

```go
// package loader
var ErrNoJustfile = errors.New("no justfile found")
func Find(fsys sys.FS, startDir string) (string, error)          // walks upward; names: "justfile", then ".justfile"; returns ErrNoJustfile
func Load(fsys sys.FS, path string) (*ast.Justfile, error)       // reads, parses, and analyzes; returns the first error
```

- [ ] **Step 1: Write the failing tests**

```go
func TestFindWalksUpward(t *testing.T)                  // justfile in parent, search from child -> parent's path
func TestFindPrefersJustfileOverDotJustfile(t *testing.T) // both present in one dir -> "justfile"
func TestFindNoJustfileIsError(t *testing.T)            // empty tree -> ErrNoJustfile; CLI wording taken from corpus
func TestLoadReportsParseErrorWithFilePath(t *testing.T) // bad syntax -> *diag.Error whose File is the path
```

For `TestFindNoJustfileIsError`, the CLI message text is taken from the corpus case for "no justfile". Assert `errors.Is(err, ErrNoJustfile)` in the library test.

Run: `go test ./internal/loader -v`
Expected: FAIL, because `Find` and `Load` do not exist yet.

- [ ] **Step 2: Implement `Find` and `Load`**

`Find` checks each directory from `startDir` up to the filesystem root, for each name in order. `Load` reads the file, calls `parser.Parse` with the path as the file name, then `analyzer.Analyze`. Both are impure because they call `sys.FS`. The name list and the path-joining logic are pure helpers, tested without a filesystem.

- [ ] **Step 3: Run the tests**

Run: `go test ./internal/loader -v`
Expected: PASS for all four tests.

- [ ] **Step 4: Commit**

```bash
git add internal/loader
git commit -m "feat: loader with upward justfile discovery"
```

---

### Task 10: Library API and embedding tests

**Files:**
- Create: `justfile.go` (package `just`, at the module root), `embed_test.go`

**Interfaces:**
- Consumes: `loader.Find`, `loader.Load`, `runner.Run`, `runner.Plan`, `runner.Result`, `ast.Justfile` (Tasks 5, 8, and 9).
- Produces, all in package `just`:

```go
type Options struct {
    Justfile   string   // explicit path; empty means search from SearchFrom
    SearchFrom string   // directory to search upward from; used when Justfile is empty
    WorkDir    string   // working directory for recipes; empty means the justfile's directory
    Env        []string // exact environment, "KEY=VALUE"; nil means empty, never os.Environ
    Stdin      io.Reader
    Stdout     io.Writer
    Stderr     io.Writer
    Recipes    []string
    Args       []string
    Quiet      bool
    FS         sys.FS      // nil uses the OS implementation from internal/sys
    Exec       sys.Process // nil uses the OS implementation from internal/sys
}
type Result struct {
    ExitCode int
}
type RecipeInfo struct {
    Name   string
    Doc    string
    Params []string
}
type Justfile struct { /* unexported */ }

func Load(opts Options) (*Justfile, error)           // uses opts.Justfile or searches from opts.SearchFrom
func Parse(name string, src []byte) (*Justfile, error) // parses and analyzes from memory; name is used in errors
func (j *Justfile) Recipes() []RecipeInfo
func (j *Justfile) Run(ctx context.Context, opts Options) (Result, error)
```

Rules: nothing in this package reads `os.Args`, `os.Environ`, the working directory, or the standard streams. No `os.Exit`. No package-level variables other than constants. The OS implementations are the only defaults for `FS` and `Exec`, and they never read the environment.

- [ ] **Step 1: Write the failing embedding tests**

`embed_test.go`:

```go
func TestEmbedCapturesStdoutWithoutOSStdout(t *testing.T)   // Stdout is a bytes.Buffer; recipe "echo hi" -> buffer "hi\n"
func TestEmbedDoesNotInheritEnv(t *testing.T)               // t.Setenv("JUST_GO_PROBE","1"); recipe echoes ${JUST_GO_PROBE-unset} with Env nil -> "unset"
func TestEmbedContextCancelStopsChild(t *testing.T)         // "sleep 30" cancelled after 100ms -> Run returns a context error promptly
func TestEmbedTwoRunsDoNotInterfere(t *testing.T)           // two concurrent Runs with separate buffers each get only their own output
func TestLibraryNeverExitsOrReadsProcessState(t *testing.T) // scans the non-test .go files of the module root and internal/ for "os.Exit", "os.Args", "os.Environ(", "os.Stdout", "os.Stderr"; fails on any hit
```

Run: `go test . -v`
Expected: FAIL, because the package has no implementation yet.

- [ ] **Step 2: Implement the API**

`Load` builds `loader` calls from `Options`. `Parse` calls `parser.Parse` and `analyzer.Analyze`. `Recipes` maps `ast.Recipe` to `RecipeInfo`. `Run` maps `Options` to `runner.Plan`. When `WorkDir` is empty, it uses the justfile's directory. Errors are returned as values, not printed.

- [ ] **Step 3: Run the tests**

Run: `go test . -v`
Expected: PASS for all five tests.

- [ ] **Step 4: Commit**

```bash
git add justfile.go embed_test.go
git commit -m "feat: embeddable library API with options-only inputs"
```

---

### Task 11: CLI

**Files:**
- Create: `cmd/just-go/main.go`
- Test: `cmd/just-go/main_test.go`

**Interfaces:**
- Consumes: the library API (Task 10).
- Produces:

```go
// package main
func run(args []string, env []string, workDir string, stdin io.Reader, stdout, stderr io.Writer, fsys sys.FS, exec sys.Process) int
var exit = os.Exit // replaced in tests so main is covered
func main() // calls exit(run(os.Args[1:], os.Environ(), cwd, os.Stdin, os.Stdout, os.Stderr, sys.OSFS{}, sys.OSProcess{}))
```

Flags: `-f/--justfile PATH`, `-l/--list`, `-q/--quiet`, and positional recipe names. `--` ends flag parsing. `--list` prints upstream's exact format, `Available recipes:` followed by four-space-indented names, with ` # doc` appended when a recipe has a doc comment. Exact wording and layout come from the corpus case for `--list`.

Exit codes: a recipe failure returns the recipe's status. A usage or load error prints `error: <message>` to stderr and returns 1.

- [ ] **Step 1: Write the failing tests**

```go
func TestRunReturnsExitCodeFromRecipe(t *testing.T)     // justfile "x:\n  exit 4" -> run returns 4
func TestRunListMatchesUpstreamFormat(t *testing.T)     // with a doc comment and a recipe with none; output matches the corpus case exactly
func TestRunNoJustfileExitsOne(t *testing.T)            // empty dir -> 1, stderr starts with "error: "
func TestRunUnknownRecipeExitsOne(t *testing.T)         // unknown name -> 1, wording from the corpus
```

Run: `go test ./cmd/just-go -v`
Expected: FAIL, because `run` does not exist yet.

- [ ] **Step 2: Implement `run` and `main`**

`TestMainExitsWithRunStatus` replaces `exit` with a recorder, sets `os.Args`, calls `main()`, and asserts the recorded code. Without it, `main` is an uncovered function and fails the 100% function rule.

`run` parses flags with the standard `flag` package in a mode that allows positional arguments after flags, or with a small parser if `flag` cannot express upstream's behavior. Keep `main` to one line.

- [ ] **Step 3: Run the tests**

Run: `go test ./cmd/just-go -v`
Expected: PASS for all four tests.

- [ ] **Step 4: Build and smoke-test**

Run: `make build`, then run `bin/just-go --list` in a directory containing a justfile with one documented recipe and one undocumented recipe.
Expected: output in upstream's `--list` format, with the documented recipe shown with its ` # doc` suffix.

- [ ] **Step 5: Commit**

```bash
git add cmd/just-go
git commit -m "feat: just-go command with list, justfile, quiet, and exit codes"
```

---

### Task 12: Milestone gate and first measured baseline

**Files:**
- Create: `scripts/bench-startup.sh`, `scripts/check-coverage.sh`, `docs/benchmarks/startup.md`, `conformance/bench/justfile`
- Modify: `conformance/baseline.txt` (via the ratchet), `conformance/corpus/README.md`

**Interfaces:**
- Consumes: everything above.
- Produces: a passing gate, a baseline listing every core-subset case that passes, and a measured startup comparison.

- [ ] **Step 1: Run the full gate**

Run: `make check cover cyclo`
Expected: `go vet` clean, `go test ./...` passing, and the conformance run executed with `JUST_GO_BIN=bin/just-go`. `scripts/check-coverage.sh` reports total statement coverage above 95.0%, and `go tool cover -func` shows 100.0% for every function. `gocyclo -over 9 .` prints nothing. Record the actual percentages and the worst cyclomatic score in the gate's commit message.

- [ ] **Step 2: Ratchet the baseline**

Run: `JUST_GO_BIN=bin/just-go go run ./conformance/cmd/ratchet`
Expected: `baseline.txt` gains every case that now passes. Record the count and the number of corpus cases in `conformance/corpus/README.md`.

- [ ] **Step 3: Confirm the ratchet holds**

Run: `JUST_GO_BIN=bin/just-go go test ./conformance -run TestBaseline`
Expected: PASS. Every ID in the baseline still passes.

- [ ] **Step 4: Write the startup benchmark script**

Create `conformance/bench/justfile` with a few documented and undocumented recipes, so `--list` has real output to format. `scripts/bench-startup.sh` times 20 invocations of `--list` on that file, for `oracle/bin/just` and `bin/just-go`, and prints the mean wall time for each. Both binaries read the same file.

- [ ] **Step 5: Run the benchmark and record it**

Run: `make bench`, which runs every `Benchmark*` in the module and writes `docs/benchmarks/go-bench.txt`. Then run `sh scripts/bench-startup.sh`.
Expected: two mean times. Write them, the machine description, and the date into `docs/benchmarks/startup.md`. These are the first numbers. Performance targets are set from them in a later plan, not here.

- [ ] **Step 6: Commit**

```bash
git add scripts/bench-startup.sh docs/benchmarks conformance/baseline.txt conformance/corpus/README.md conformance/bench
git commit -m "chore: milestone 1 gate, conformance baseline, and first startup benchmark"
```

---

## Roadmap: later plans

Each later plan is written after this one passes, using its baseline. Each must produce working software on its own.

1. **Full grammar and settings.** Conditionals, `[attributes]`, the remaining settings, shebang recipes, and backticks.
2. **Builtin functions.** The roughly 130 functions, in groups (string, path, env, hashing, datetime, style, file reading).
3. **Modules, imports, dotenv, and strict path rules.** Includes the strict path containment check and its CLI and library opt-out.
4. **Remaining CLI subcommands and output formats.** `--dump` (JSON), `--summary`, `--show`, `--evaluate`, `--usage`, `--groups`, `--variables`, `--choose`, `--edit`, `--init`, `--clean`, `--man`, `--changelog`, `--request`, `--command`. This includes the `--version` output decision.
5. **Completions and platform parity.** Generated completions for bash, zsh, fish, and PowerShell. Windows shells (`pwsh`, `powershell`, `cmd`). BSD best-effort. Windows process handling.
6. **Unstable milestones.** Each is its own plan when started: cached recipes (needs upstream's cache semantics written into the spec first), the `lists` setting, and user-defined functions.
7. **Performance targets.** Set from the measured baseline in `docs/benchmarks/startup.md`, after the feature plans above have made the comparisons meaningful.

## Open items this plan depends on

- **Copyright holder** for `LICENSE` (Task 0). The placeholder stays until the owner supplies it.
- **Git initialization** (Task 0, Step 1). The plan assumes it is done. If the owner wants the repository set up differently, the commit steps change, not the code.
