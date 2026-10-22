# just-go Full Compatibility Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Take `just-go` from the foundations milestone (125 of 2011 recorded upstream cases passing) to full upstream v1.59.0 compatibility, covering roadmap items 1-5: grammar, builtins, modules and dotenv, the remaining CLI, and completions and platforms. Unstable features are out of scope (owner decision): they stay gated, as the approved spec says.

**Architecture:** The same engine as the foundations milestone. Each phase adds one feature family, and its acceptance is measured by the conformance corpus: a phase is done when the ratchet baseline grows by that family's cases and no existing baseline case regresses. The per-task rules and the engineering standards are unchanged.

**Tech Stack:** Go 1.27 standard library only, plus `gocyclo` as a developer tool. The upstream oracle (`oracle/bin/just`, built from v1.59.0) generates expected output.

**Spec:** `docs/superpowers/specs/2026-10-22-just-go-port-design.md`. No spec amendment is needed: unstable features stay gated in v1.

**Foundations plan:** `docs/superpowers/plans/2026-10-22-just-go-foundations.md`, with its rulings R1-R14 in its ledger.

## Global Constraints

- Module path: `gitea.com/wimwian/just-go`. Reference: casey/just v1.59.0, commit `d6fee036379c531369dc37f67f8d4640f6bf1e2d`.
- Priority order: compatibility, then security, then performance. Performance work never changes observable output.
- Feature compatibility: same recipe selection, command execution order, variable values, and exit codes as upstream. Error text need not match byte for byte, but error category and location must match. Columns are counted in runes, as upstream does; the foundations milestone counted bytes in one path and this is fixed in Phase 1.
- Documented divergence: strict path rules refuse some paths upstream allows. The refusal is an explicit error naming the path and the rule, and the opt-out lifts it.
- The library never calls `os.Exit`, never writes to `os.Stdout` or `os.Stderr` directly, and never parses `os.Args`. Library inputs come only through `Options`.
- The library accepts a `context.Context`, returns errors as values, and has no package-level mutable state.
- Pure and impure functions are segregated. Impure functions live in `internal/sys` behind interfaces. Pure packages never import `os`, `os/exec`, or `time`.
- Coverage: more than 95% statement coverage overall, and 100% function coverage. Checked by `scripts/check-coverage.sh`. Cyclomatic complexity below 10, checked by `gocyclo -over 9 .`.
- Performance benchmarks are built in: `Benchmark<Name>` for hot paths, run by `make bench`.
- Standard library first. Each new third-party dependency needs a ruling in the ledger.
- Ratchet gate (R10): `make check` runs vet, test, and the baseline check. `make conformance-report` runs the full corpus and reports counts; it is allowed to fail until the phase that covers a case lands.
- `--shell` and `--shell-arg` are in scope (R11). Recorder gaps are fixed, and the oracle-exception categories are closed to path-hash, machine-env, and fifo (R8, R9).
- Benchmark output is committed as the recorded baseline (R12).
- Unstable features are out of scope (owner decision). `set unstable` is parsed and gated as the spec requires; unstable-only families stay as exceptions.

## Review Focus

Five input classes most likely to bite a user of this milestone, each pinned by a test in the owning task.

1. **Non-ASCII text before an error location.** Columns count runes. Pinned by Task 1, `TestErrorColumnCountsRunes`.
2. **A shell that does not exist.** Prints one line with no snippet and no column zero. Pinned by Task 1, `TestDescribeSkipsSnippetWithoutSource`.
3. **Escaped braces inside strings and bodies.** `{{{{` in quoted text. Pinned by Task 2, `TestLexBraceEscapeInsideString`.
4. **Cyclic dependencies and circular definitions.** Reported with upstream's category and location. Pinned by Task 4, `TestAnalyzeCircularDependencyMatchesUpstream`.
5. **Symlinks that leave the project.** Refused by the strict path rule, and allowed with the opt-out. Pinned by Task 19, `TestStrictPathRefusesSymlinkEscape`.

## Phases and acceptance

Each phase names a roadmap item, the case families it covers, and its gate. Counts come from `make conformance-report` before and after the phase.

| Phase | Roadmap item | Covers | Gate |
|---|---|---|---|
| 1 | 1. Grammar | Core-subset defects, settings, attributes, backticks, conditionals, dependency arguments, shebang and script recipes | Baseline grows by the phase's families; no regressions |
| 2 | 2. Builtins | About 130 builtin functions in groups | Same |
| 3 | 3. Modules and dotenv | `import`, `mod`, dotenv, strict path rules | Same |
| 4 | 4. CLI | `--evaluate`, `--dump`, `--summary`, `--show`, `--usage`, `--groups`, `--variables`, `--choose`, `--init`, `--clean`, `--command`, `--edit`, `--man`, `--changelog`, `--request`, and the rest | Same |
| 5 | 5. Completions and platforms | `completions` for bash, zsh, fish, PowerShell; Windows shells; BSD best-effort | Same |

Performance targets (roadmap item 7) are out of this plan.

---

## Phase 1: Grammar (roadmap item 1)

### Task 1: Triage and Plan 1 defects

**Files:**
- Create: `conformance/triage/README.md`, `conformance/triage/groups.json`, `scripts/triage.py`
- Modify: `internal/loader/loader.go` (error column rune counting), `cmd/just-go/main.go` (describe skips the snippet when the source is empty or the column is zero), `internal/diag/diag.go` (rune columns)
- Test: `internal/diag/diag_test.go`, `cmd/just-go/main_test.go`, `conformance/triage_test.go`

**Interfaces:**
- Consumes: the conformance output from `make conformance-report`.
- Produces: `conformance/triage/groups.json`, mapping every failing case ID to exactly one group name and one reason. Later tasks use the group names.

- [ ] **Step 1: Triage.** `scripts/triage.py` runs the conformance suite with `go test -json`, reads each failing case's expected and actual output, and assigns it to one group: `grammar`, `setting:<name>`, `attribute`, `backtick`, `conditional`, `dependency-args`, `shebang`, `builtin:<family>`, `module`, `dotenv`, `cli:<flag>`, `completions`, `unstable:<family>`, `core-defect`, or `other`. Commit `groups.json`.
- [ ] **Step 2: Acceptance for triage.** `TestTriageCoversEveryFailure`: every failing case ID appears in exactly one group, and the count of groups' cases equals the failing count.
- [ ] **Step 3: Rune columns and the snippet fix.** Write `TestErrorColumnCountsRunes` (a line with `é` before the error gives the rune column) and `TestDescribeSkipsSnippetWithoutSource` (a runtime error with no source prints one line). Then implement.
- [ ] **Step 4: Verify the core-defect groups.** Re-run `make conformance-report`; the `core-defect` count should drop to zero before Phase 1 continues.
- [ ] **Step 5: Commit** with `fix: rune columns and no snippet for sourceless errors`.

### Task 2: Lexer escapes and string literals

**Files:**
- Modify: `internal/lexer/lexer.go`, `internal/parser/parser.go`
- Test: `internal/lexer/lexer_test.go`, `internal/parser/parser_test.go`

**Interfaces:**
- Consumes: `diag.Error`.
- Produces: string literal tokens for cooked, raw, and indented forms; brace-escape handling in bodies and strings.

- [ ] **Step 1: Failing tests.** `TestLexBraceEscapeInsideString` (`echo '{{{{'` yields `{{`), `TestLexIndentedStringRemovesCommonIndent`, `TestLexUnterminatedStringMatchesUpstream` (message and location from the corpus case `misc::brace_escape#1`).
- [ ] **Step 2: Implement** to make them pass, keeping the lexer pure.
- [ ] **Step 3: Verify** the `string`, `interpolation`, and `brace_escape` groups in the conformance output.
- [ ] **Step 4: Commit** `feat: lexer brace escapes and string literal forms`.

### Task 3: Parser error wording and categories

**Files:**
- Modify: `internal/parser/parser.go`, `internal/diag/diag.go`
- Test: `internal/parser/parser_test.go`

**Interfaces:**
- Consumes: `lexer.Token`.
- Produces: error messages and locations for unexpected characters, delimiter mismatches, and escape errors, matched to the corpus cases in `error_messages`, `delimiters`, and `parser`.

- [ ] **Step 1: Failing tests.** One test per corpus case in those groups, asserting the category and location. Message text is copied from the corpus.
- [ ] **Step 2: Implement.**
- [ ] **Step 3: Verify** the groups; the baseline grows.
- [ ] **Step 4: Commit** `fix: parser error wording and locations`.

### Task 4: Analyzer checks upstream applies

**Files:**
- Modify: `internal/analyzer/analyzer.go`
- Test: `internal/analyzer/analyzer_test.go`

**Interfaces:**
- Consumes: `ast.Justfile`.
- Produces: upstream's check order, circular dependency and circular definition errors, duplicate parameter errors, and undefined-function errors.

- [ ] **Step 1: Failing tests.** `TestAnalyzeCircularDependencyMatchesUpstream`, `TestAnalyzeDuplicateParameter`, `TestAnalyzeUndefinedVariableInDefault`, one per corpus case in `subsequents`, `function_definitions`, and `misc`.
- [ ] **Step 2: Implement** in upstream's order (read `oracle/src/src/analyzer.rs` for the order; the corpus is the authority).
- [ ] **Step 3: Verify** and commit `fix: analyzer check order and circular definitions`.

### Task 5: Dependency arguments

**Files:**
- Modify: `internal/ast/ast.go`, `internal/parser/parser.go`, `internal/analyzer/analyzer.go`, `internal/runner/runner.go`, `internal/eval/eval.go`
- Test: the owning packages' test files

**Interfaces:**
- Consumes: `ast.Recipe.Deps`.
- Produces: `ast.Dependency{Name string, Args []ast.Expr}`; the runner evaluates the arguments in the caller's scope and passes them to the dependency once per distinct argument list.

- [ ] **Step 1: Failing tests.** One parse test, one run test (a dependency receives its arguments), one test that the same dependency with different arguments runs twice.
- [ ] **Step 2: Implement.** Changing `Deps` from strings to `Dependency` is a breaking change to the Plan 1 AST; update every user in the same commit.
- [ ] **Step 3: Verify** the `dependencies` and `shell_expansion` groups.
- [ ] **Step 4: Commit** `feat: dependency arguments`.

### Task 6: Settings, group A (execution and defaults)

**Files:**
- Modify: `internal/ast/ast.go` (Settings), `internal/parser/parser.go`, `internal/runner/runner.go`, `justfile.go`
- Test: the owning packages' test files

**Interfaces:**
- Consumes: `ast.Settings`.
- Produces: the settings `export`, `positional-arguments`, `working-directory`, `no-cd`, `ignore-comments`, `lazy`, `guards`, `fallback` (parsing and effect; `fallback`'s search is in Phase 3), `allow-duplicate-recipes`, `allow-duplicate-variables`, `default-list`, `default-script`, `no-exit-message`, `script-interpreter`, `windows-shell`, `windows-powershell`, `tempdir`, `indentation`, and `minimum-version`.

- [ ] **Step 1: Failing tests** for each setting, copied from the `settings`, `export`, `working_directory`, `positional_arguments`, `no_exit_message`, `ignore_comments`, `lazy`, `guards`, `allow_duplicates`, `minimum_version`, and `tempdir` groups.
- [ ] **Step 2: Implement** each setting. A setting's default and its validation error come from the corpus.
- [ ] **Step 3: Verify** and commit `feat: settings group A`.

### Task 7: Settings, group B (dotenv, lists, unstable gate, misc)

**Files:**
- Modify: `internal/ast/ast.go`, `internal/parser/parser.go`, `justfile.go`
- Test: the owning packages' test files

**Interfaces:**
- Consumes: `ast.Settings`.
- Produces: parsing and validation for `dotenv-command`, `dotenv-filename`, `dotenv-load`, `dotenv-override`, `dotenv-path`, `dotenv-required` (loading is Phase 3), `unstable` (the gate, which refuses unstable features unless enabled), and `lists` (parsed and refused, as upstream refuses it without unstable).

- [ ] **Step 1: Failing tests** from the `dotenv`, `unstable`, `lists`, and `settings` groups that are settings-only.
- [ ] **Step 2: Implement** parsing and validation. Dotenv loading and the `lists` behavior are not implemented here.
- [ ] **Step 3: Verify** and commit `feat: settings group B`.

### Task 8: Attributes, group A (visibility, grouping, doc, confirm)

**Files:**
- Modify: `internal/ast/ast.go` (Attribute), `internal/parser/parser.go`, `internal/runner/runner.go`, `justfile.go`
- Test: the owning packages' test files

**Interfaces:**
- Consumes: `ast.Recipe`.
- Produces: `ast.Attribute{Name string, Args []ast.Expr}` on recipes and `[private]`, `[group]`, `[doc]`, `[confirm]`, `[no-cd]`, `[no-exit-message]`, `[no-quiet]`, `[exit-message]`, `[positional-arguments]`, `[working-directory]`, `[default]`.

- [ ] **Step 1: Failing tests** from the `attributes`, `groups`, `private`, `confirm`, `no_exit_message`, and `default` groups.
- [ ] **Step 2: Implement.** `[confirm]` reads from stdin; the prompt text is copied from the corpus.
- [ ] **Step 3: Verify** and commit `feat: attributes group A`.

### Task 9: Attributes, group B (OS gates, env, script, shell, timestamp, parallel, continue, metadata)

**Files:**
- Modify: `internal/ast/ast.go`, `internal/parser/parser.go`, `internal/runner/runner.go`, `internal/sys/sys.go` (only if a new impure call is needed; it goes behind an interface)
- Test: the owning packages' test files

**Interfaces:**
- Consumes: `ast.Attribute`.
- Produces: `[linux]`, `[macos]`, `[unix]`, `[windows]`, `[android]`, `[freebsd]`, `[netbsd]`, `[openbsd]`, `[dragonfly]` as recipe filters; `[env]`, `[extension]`, `[script]`, `[shell]`, `[timestamp]`, `[parallel]`, `[continue]`, `[metadata]`.

- [ ] **Step 1: Failing tests** from `os_attributes`, `attributes`, `script`, `parallel`, `timestamps`, `continue`, `shell`, and `metadata`.
- [ ] **Step 2: Implement.** `[timestamp]` reads file times through `sys.FS` (add `ModTime` to the interface, with a fake).
- [ ] **Step 3: Verify** and commit `feat: attributes group B`.

### Task 10: Backticks

**Files:**
- Modify: `internal/lexer/lexer.go`, `internal/parser/parser.go`, `internal/eval/eval.go`, `internal/runner/runner.go`, `internal/sys/sys.go`
- Test: the owning packages' test files

**Interfaces:**
- Consumes: `sys.Process`.
- Produces: `ast.Backtick{Command string}`; evaluation runs the command through the configured shell, captures stdout with the trailing newline stripped, and fails with upstream's "backtick failed" error.

- [ ] **Step 1: Failing tests** from the `backticks` group and the backtick cases in `misc` and `lazy`.
- [ ] **Step 2: Implement.** Evaluation is impure; put it in a function that takes `sys.Process`.
- [ ] **Step 3: Verify** and commit `feat: backticks`.

### Task 11: Shebang and script recipes

**Files:**
- Modify: `internal/runner/runner.go`, `internal/sys/os.go` (only if needed)
- Test: `internal/runner/runner_test.go`

**Interfaces:**
- Consumes: `ast.Recipe` lines.
- Produces: a recipe whose first line starts with `#!` runs as a script file in the temporary directory; `[script]` uses the configured interpreter.

- [ ] **Step 1: Failing tests** from `shebang`, `script`, and `export` shebang cases.
- [ ] **Step 2: Implement** the script file path through `sys.FS` writes (add `WriteFile` to the interface, with a fake).
- [ ] **Step 3: Verify** and commit `feat: shebang and script recipes`.

### Task 12: Conditionals, assert, and the if/else grammar

**Files:**
- Modify: `internal/ast/ast.go`, `internal/parser/parser.go`, `internal/eval/eval.go`, `internal/analyzer/analyzer.go`
- Test: the owning packages' test files

**Interfaces:**
- Consumes: `ast.Expr`.
- Produces: `ast.Conditional{Cond, Then, Else Expr}` and `ast.Assert{Cond Expr, Message Expr}`. Comparison operators are not here; they are unstable and out of scope.

- [ ] **Step 1: Failing tests** from `conditional`, `assertions`, and the `if`/`else` cases in `misc`.
- [ ] **Step 2: Implement.**
- [ ] **Step 3: Verify** and commit `feat: conditionals and assert`.

### Task 13: Phase 1 gate

- [ ] **Step 1:** `make check cover cyclo bench`. Coverage 100% per function, complexity clean.
- [ ] **Step 2:** `make conformance-report`. Record counts in `conformance/corpus/README.md` and `docs/benchmarks`.
- [ ] **Step 3:** `make ratchet` (`JUST_GO_BIN=bin/just-go go run ./conformance/cmd/ratchet`) to grow the baseline. Commit with the counts in the message.

---

## Phase 2: Builtins (roadmap item 2)

Each builtin is a pure function in `internal/eval/builtins/<group>.go`, registered in one table. Impure builtins (`env`, `env_var`, `os`, `arch`, `num_cpus`, `path_exists`, `read`, `which`, `datetime`, `uuid`, the directory functions) take `sys` interfaces.

### Task 14: String builtins

**Files:**
- Create: `internal/eval/builtins/string.go`, `internal/eval/builtins/string_test.go`

**Interfaces:**
- Produces: `capitalize`, `kebabcase`, `lowercamelcase`, `lowercase`, `shoutykebabcase`, `shoutysnakecase`, `snakecase`, `titlecase`, `trim`, `trim_end`, `trim_end_match`, `trim_end_matches`, `trim_start`, `trim_start_match`, `trim_start_matches`, `uppercamelcase`, `uppercase`, `replace`, `replace_regex`, `quote`, `split`, `join`, `join_list`, `len`, `reverse`, `prepend`, `append`, `without_extension`, `file_stem`, `file_name`, `extension`. All pure.

- [ ] **Step 1: Failing tests** from the `string`, `format_string`, and `replace` groups, one table per function, copied from the corpus.
- [ ] **Step 2: Implement** each as a pure function in the table.
- [ ] **Step 3: Verify** and commit `feat: string builtins`.

### Task 15: Path builtins

**Files:**
- Create: `internal/eval/builtins/path.go`, `internal/eval/builtins/path_test.go`

**Interfaces:**
- Produces: `absolute_path`, `canonicalize`, `parent_directory`, `path_exists`, `read`, `join` (path form), `source_file`, `source_directory`, `justfile`, `justfile_directory`, `invocation_directory`, `invocation_directory_native`, `module_file`, `module_directory`, `module_path`. Impure ones take `sys.FS`.

- [ ] **Step 1: Failing tests** from the `functions`, `directories`, and `read` groups.
- [ ] **Step 2: Implement.** `canonicalize` and `read` follow symlinks and are subject to the strict path rule in Phase 3; in this task they follow upstream without the rule.
- [ ] **Step 3: Verify** and commit `feat: path builtins`.

### Task 16: Environment, system, and runtime builtins

**Files:**
- Create: `internal/eval/builtins/system.go`, `internal/eval/builtins/system_test.go`
- Modify: `internal/sys/sys.go` (add `Env` and `Platform` interfaces, with fakes)

**Interfaces:**
- Produces: `env`, `env_var`, `env_var_or_default`, `os`, `os_family`, `arch`, `num_cpus`, `num_jobs`, `just_executable`, `just_pid`, `just_version`, `which`, `require`, `shell`, `error`, `warning`, `choose` (the chooser; the CLI wires it in Phase 4), `command`, `clean`, `invocation_directory` (already in Task 15), `recipe_name`, `is_dependency`.

- [ ] **Step 1: Failing tests** from `env_var_functions`, `which_function`, `os`, `error`, `warning`, and `misc` groups.
- [ ] **Step 2: Implement.** `env` reads only what the caller passed in `Options.Env` (the embedding rule), except where the corpus requires the recording environment, which is the machine-env exception.
- [ ] **Step 3: Verify** and commit `feat: environment and system builtins`.

### Task 17: Datetime, hashing, and identifiers

**Files:**
- Create: `internal/eval/builtins/time.go`, `internal/eval/builtins/hash.go`, and their test files
- Modify: `internal/sys/sys.go` (add `Clock` and `Random` interfaces, with fakes)

**Interfaces:**
- Produces: `datetime`, `datetime_utc`, `sha256`, `sha256_file`, `blake3`, `blake3_file`, `uuid`, `semver_matches`. Time and randomness come only from `Clock` and `Random`.

- [ ] **Step 1: Failing tests** from `datetime`, `hashing`, `uuid`, and `semver` groups. Use fakes to pin time.
- [ ] **Step 2: Implement.** BLAKE3 uses a dependency only after a ruling in the ledger.
- [ ] **Step 3: Verify** and commit `feat: datetime, hashing, and identifier builtins`.

### Task 18: Style builtins and ANSI

**Files:**
- Create: `internal/eval/builtins/style.go`, `internal/eval/builtins/style_test.go`

**Interfaces:**
- Produces: `style`, `bold`, `italic`, `underline`, `dim`, `strikethrough`, `blink`, `hidden`, and their colour names. Pure, with the `--color` decision left to Phase 4.

- [ ] **Step 1: Failing tests** from the `style` group, the byte-exact escape sequences copied from the corpus.
- [ ] **Step 2: Implement** as pure functions.
- [ ] **Step 3: Verify** and commit `feat: style builtins`.

### Task 19: Cache-directory and config builtins

**Files:**
- Create: `internal/eval/builtins/dirs.go`, `internal/eval/builtins/dirs_test.go`

**Interfaces:**
- Produces: `cache_directory`, `config_directory`, `config_local_directory`, `data_directory`, `data_local_directory`, `executable_directory`, `home_directory`, `runtime_directory`. Each reads the platform's convention through `Platform`; the corpus defines each path.

- [ ] **Step 1: Failing tests** from the `directories` group, with fakes that set the platform's directory variables.
- [ ] **Step 2: Implement.**
- [ ] **Step 3: Verify** and commit `feat: directory builtins`.

### Task 20: Phase 2 gate

- [ ] **Step 1:** `make check cover cyclo bench`, plus the `functions` count for builtins.
- [ ] **Step 2:** `make ratchet`, and commit the new baseline with the counts.

---

## Phase 3: Modules, imports, and dotenv (roadmap item 3)

### Task 21: Imports

**Files:**
- Modify: `internal/loader/loader.go`, `internal/ast/ast.go`, `internal/parser/parser.go`, `justfile.go`
- Test: `internal/loader/loader_test.go`

**Interfaces:**
- Consumes: `sys.FS`.
- Produces: `import` and `import?` (optional) with recursive loading, cycle detection, and upstream's error for a missing required import.

- [ ] **Step 1: Failing tests** from `imports` and `missing_optional_imports` groups.
- [ ] **Step 2: Implement.**
- [ ] **Step 3: Verify** and commit `feat: imports`.

### Task 22: Modules

**Files:**
- Modify: `internal/loader/loader.go`, `internal/ast/ast.go`, `internal/parser/parser.go`, `internal/analyzer/analyzer.go`, `internal/runner/runner.go`, `justfile.go`
- Test: the owning packages' test files

**Interfaces:**
- Produces: `mod` and `mod?`, module file discovery (`<name>.just`, `<name>/mod.just`, and the `module-path` setting), `::` paths for recipes and aliases across modules, and `mod`-scoped `[private]` and `module_file` builtins.

- [ ] **Step 1: Failing tests** from the `modules`, `modules_*`, and `alias` groups.
- [ ] **Step 2: Implement.** Module search uses `sys.FS.IsFile`.
- [ ] **Step 3: Verify** and commit `feat: modules`.

### Task 23: Dotenv loading

**Files:**
- Create: `internal/dotenv/dotenv.go`, `internal/dotenv/dotenv_test.go`
- Modify: `internal/runner/runner.go`, `internal/sys/sys.go` (dotenv reads use `FS`; the dotenv command uses `Process`)

**Interfaces:**
- Produces: `dotenv.Load(fsys sys.FS, path string) (map[string]string, error)`, parsing `KEY=VALUE` with upstream's quoting and comment rules. `dotenv-command` runs through `Process`.

- [ ] **Step 1: Failing tests** from the `dotenv` group, one per quoting rule.
- [ ] **Step 2: Implement.** Dotenv values are inputs (the threat model): they reach the environment of children but never a shell command unquoted.
- [ ] **Step 3: Verify** and commit `feat: dotenv loading`.

### Task 24: Strict path rules and the opt-out

**Files:**
- Create: `internal/loader/pathcheck.go`, `internal/loader/pathcheck_test.go`
- Modify: `justfile.go` (Options field `AllowOutsidePaths bool`), `cmd/just-go/main.go` (flag `--allow-outside-project-paths`)

**Interfaces:**
- Produces: `CheckPath(root, path string, resolve func(string) (string, error)) error`, which resolves symlinks and refuses any path outside the directory of the root justfile. Used by `read`, `canonicalize`, `import`, `mod`, and dotenv.

- [ ] **Step 1: Failing tests:** `TestStrictPathRefusesSymlinkEscape` (the Review Focus item 5), `TestStrictPathAllowsSubdirectory`, `TestStrictPathOptOutLiftsRefusal`. The refusal text is the author's wording and is recorded in the ledger.
- [ ] **Step 2: Implement.** Containment is checked after symlink resolution. The opt-out is on the library `Options` and the CLI flag.
- [ ] **Step 3: Verify** the corpus cases that read outside the project are refused with the upstream error only where upstream also refuses. Cases where upstream allows the path and the strict rule refuses are the documented divergence; list them in `conformance/oracle-exceptions.txt` under category `strict-path`, and extend R9's closed category set by a ruling in the ledger first.
- [ ] **Step 4: Commit** `feat: strict path rules with an opt-out`.

### Task 25: Phase 3 gate

- [ ] **Step 1:** `make check cover cyclo bench`.
- [ ] **Step 2:** `make ratchet`, commit with the counts.

---

## Phase 4: Remaining CLI and output formats (roadmap item 4)

### Task 26: Evaluate and dump

**Files:**
- Create: `internal/output/dump.go`, `internal/output/dump_test.go`, `internal/output/evaluate.go`
- Modify: `cmd/just-go/main.go`, `justfile.go`

**Interfaces:**
- Produces: `--evaluate [NAME]` prints the evaluated assignments (upstream's format); `--dump` prints the justfile in upstream's canonical form, and `--dump-format json` prints upstream's JSON schema. The JSON shape comes from the corpus cases in `dump` and `json`.

- [ ] **Step 1: Failing tests** from `evaluate`, `dump`, `json`, and `dump_format` groups.
- [ ] **Step 2: Implement.** Output functions are pure: they take `ast`/`eval` values and return bytes.
- [ ] **Step 3: Verify** and commit `feat: evaluate and dump output`.

### Task 27: Summary, show, usage, groups, variables

**Files:**
- Create: `internal/output/summary.go`, `internal/output/show.go`, `internal/output/usage.go`, `internal/output/groups.go`, `internal/output/variables.go`, and test files
- Modify: `cmd/just-go/main.go`

**Interfaces:**
- Produces: `--summary`, `--show RECIPE`, `--usage RECIPE`, `--groups`, `--variables`, and `--list-submodules`, plus the `--list` details (groups, submodules, and the `--unsorted` flag).

- [ ] **Step 1: Failing tests** from `summary`, `show`, `usage`, `groups`, `variables`, `list`, `list_submodules`, and `unsorted` groups.
- [ ] **Step 2: Implement.**
- [ ] **Step 3: Verify** and commit `feat: summary, show, usage, groups, and variables output`.

### Task 28: Interactive and filesystem subcommands

**Files:**
- Create: `internal/chooser/chooser.go`, `internal/chooser/chooser_test.go`, `internal/initcmd/init.go`, `internal/cleancmd/clean.go`, and test files
- Modify: `cmd/just-go/main.go`

**Interfaces:**
- Produces: `--choose` (runs the chooser through `sys.Process`, using `--chooser`), `--init` (writes a starter justfile through `sys.FS`), `--clean` (cache is out of scope, so this task only handles the directory case the corpus tests), `--edit` (runs `$VISUAL`/`$EDITOR` through `sys.Process`).

- [ ] **Step 1: Failing tests** from `choose`, `init`, `clean`, and `edit` groups.
- [ ] **Step 2: Implement.** Every external program goes through `sys.Process` with an argv, never a shell string.
- [ ] **Step 3: Verify** and commit `feat: choose, init, clean, and edit`.

### Task 29: Command, man, changelog, request, and remaining flags

**Files:**
- Create: `internal/command/command.go`, `internal/manpage/manpage.go`, `internal/changelog/changelog.go`, `internal/request/request.go`, and test files
- Modify: `cmd/just-go/main.go`

**Interfaces:**
- Produces: `--command CMD ARGS` (runs a command in the justfile's environment), `--man`, `--changelog`, `--request`, and flags `--verbose`, `--color`, `--set`, `--ceiling`, `--chooser`, `--justfile-name`, `--working-directory`, `--dump-format`, `--check`. The `--version` output is decided here by a ruling: it prints `just-go` followed by the version, and the corpus cases that expect upstream's `just 1.59.0` are recorded as a divergence under `cli-identity`.

- [ ] **Step 1: Failing tests** from `command`, `man`, `changelog`, `request`, `help`, and `misc` flag groups.
- [ ] **Step 2: Implement.** `--man` and `--changelog` output comes from data embedded at build time, so the output is deterministic.
- [ ] **Step 3: Verify** and commit `feat: command, man, changelog, request, and remaining flags`.

### Task 30: Phase 4 gate

- [ ] **Step 1:** `make check cover cyclo bench`.
- [ ] **Step 2:** `make ratchet`, commit with the counts and the `cli-identity` exception list.

---

## Phase 5: Completions and platform parity (roadmap item 5)

### Task 31: Completions

**Files:**
- Create: `internal/completions/completions.go`, `internal/completions/completions_test.go`, `internal/completions/bash.go`, `internal/completions/zsh.go`, `internal/completions/fish.go`, `internal/completions/powershell.go`
- Modify: `cmd/just-go/main.go`

**Interfaces:**
- Produces: `--completions SHELL` and the `completions` subcommand. Generated from the same flag table the CLI uses, per the spec. Output must work; byte-match with upstream is not required.
- Ruling needed before Task 31 starts: `elvish` is not in the spec's target shells. The elvish cases stay as exceptions under a new category `unsupported-shell`, which needs a ledger entry and an addition to the closed category set.

- [ ] **Step 1: Ruling.** Record the elvish ruling in the ledger.
- [ ] **Step 2: Failing tests** from the `completions` group for bash, zsh, fish, and PowerShell: each generated script must be accepted by its shell's syntax check.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Verify** and commit `feat: completions for bash, zsh, fish, and PowerShell`.

### Task 32: Windows shells and process handling

**Files:**
- Modify: `internal/runner/runner.go`, `internal/sys/os_windows.go`, `internal/sys/os_unix.go`
- Test: `internal/sys/sys_windows_test.go`, `internal/runner/runner_test.go`

**Interfaces:**
- Produces: `pwsh`, `powershell`, and `cmd` as shells on Windows, with upstream's argument conventions; Windows process termination kills the process tree.

- [ ] **Step 1: Failing tests** from `windows`, `windows_shell`, and `windows_powershell` groups, guarded by build tags so they run on Windows only.
- [ ] **Step 2: Implement.** Also fix the unix-only tests that hardcode `/bin/sh` (deferred items T0-6, T8-5, T10-5, T11-8): give them a unix build tag and add Windows equivalents.
- [ ] **Step 3: Verify** on Windows, and commit `feat: windows shells and process tree termination`.

### Task 33: BSD best-effort and the platform matrix

**Files:**
- Modify: `Makefile`, `docs/platforms.md`

**Interfaces:**
- Produces: a CI matrix definition for Linux, macOS, and Windows (required) and BSD (best-effort), and `docs/platforms.md` listing the exception categories per platform.

- [ ] **Step 1: Run** the conformance suite on FreeBSD or OpenBSD if available; record the result either way.
- [ ] **Step 2: Commit** the matrix and the platform notes.

### Task 34: Phase 5 gate

- [ ] **Step 1:** `make check cover cyclo bench`.
- [ ] **Step 2:** `make ratchet`, commit with the counts.

---

## Out of this plan

- Roadmap item 6, the unstable features: owner decision, out of scope.
- Roadmap item 7, performance targets: set from `docs/benchmarks/startup.md` after the phases above have made the comparisons meaningful.
