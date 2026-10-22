# just-go: Design

**Status:** Approved by the owner on 2026-10-22, then amended to add the engineering standards. Decisions the owner made are marked **[owner]**. Calls the author made are marked **[my call]** and can be overridden.

**Module:** `gitea.com/wimwian/just-go` **[owner]**

## Intent and why

`just-go` is a green-field Go port of [`just`](https://github.com/casey/just), the command runner that reads a `justfile` and runs recipes from it. It exists to give Go users and Go-based toolchains a `just` that is:

1. **Feature-compatible** with upstream, so existing justfiles run unchanged.
2. **Fast**, measured against upstream on the same workloads.
3. **Secure**, with its security surface reviewed on purpose rather than inherited by accident.

Priority order when these conflict: compatibility, then security, then performance. Performance work never changes observable output.

`just-go` ships as two things, built from the same engine:

1. **A command**, `just-go`, that behaves as upstream `just` does on the command line.
2. **A Go library** that another CLI can import and embed. A host program can load a justfile, list and inspect its recipes, and run recipes in-process, without shelling out to a `just` binary and without the library touching process-global state.

## Reference

- Upstream: `casey/just`, pinned to **v1.59.0**, commit `d6fee036379c531369dc37f67f8d4640f6bf1e2d` (2026-10-18). Later upstream changes are out of scope until a new pin is chosen.
- Upstream size, for scale: about 28,400 lines of Rust in `src/`, and about 1,900 `#[test]` functions in `tests/`.
- Upstream feature surface (from source review):
  - **Settings (27):** `allow-duplicate-recipes`, `allow-duplicate-variables`, `dotenv-*` (command, filename, load, override, path, required), `export`, `fallback`, `guards`, `ignore-comments`, `indentation`, `lazy`, `lists`, `minimum-version`, `no-cd`, `no-exit-message`, `positional-arguments`, `quiet`, `script-interpreter`, `shell`, `tempdir`, `unstable`, `windows-powershell`, `windows-shell`, `working-directory`, and others.
  - **Attributes:** `confirm`, `continue`, `default`, `doc`, `env`, `exit-message`, `extension`, `group`, `metadata`, `no-cd`, `no-exit-message`, `no-quiet`, `parallel`, `positional-arguments`, `private`, `script`, `shell`, `timestamp`, `working-directory`, and OS gates (`linux`, `macos`, `unix`, `windows`, `android`, `freebsd`, `netbsd`, `openbsd`, `dragonfly`).
  - **Builtin functions (about 130):** string, path, env, system, datetime, hashing (`sha256`, `blake3`), ANSI style, and file reading (`read`, `path_exists`, and similar). The full list is in `src/function.rs`.
  - **Subcommands:** `changelog`, `choose`, `clean`, `command`, `completions`, `dump`, `edit`, `evaluate`, `format`, `groups`, `init`, `list`, `man`, `request`, `run`, `show`, `summary`, `usage`, `variables`.
  - **Unstable features:** `cached recipes`, `lists` setting, user-defined functions. Gated behind `set unstable` / `--unstable`.
  - **Language:** modules (`mod`), imports (`import`), conditionals, `assert`, `error()`, string and list literals, backticks, and recipe dependencies with arguments.

## Goals

1. **Feature compatibility.** For any justfile that upstream v1.59.0 accepts, `just-go` produces the same result: same recipe selection, same command execution order, same variable values, same exit codes. For any justfile upstream rejects, `just-go` rejects it too. The error text need not match byte for byte, but the error category and location must.
   - **Documented exception:** strict path rules (see Threat model) refuse some paths that upstream allows. These refusals are explicit errors that name the path and the rule, and the opt-out flag lifts them. They are the only intentional divergence from upstream behavior.
2. **CLI compatibility.** Every flag and subcommand listed above behaves as upstream does. Output formats for `--list`, `--summary`, `--dump` (JSON), `--show`, `--evaluate`, and `--usage` match upstream.
3. **Performance.** Measured, not assumed. A benchmark harness runs the same workloads through upstream and `just-go`. Startup, parse time, evaluation, recipe dispatch overhead, and parallel recipe execution are all measured. No optimization ships without a before/after number.
4. **Security.** A written threat model (see below) is the basis for every security decision. Security-relevant behavior is covered by tests.
5. **Embeddable library.** The library is a public Go package with a stable, documented API. It meets these requirements:
   - It never calls `os.Exit`, never writes to `os.Stdout` or `os.Stderr` directly, and never parses `os.Args`.
   - Everything the run depends on comes in through an options value: working directory, environment, stdin, stdout, stderr, the chosen justfile or its search start, and the recipes to run. Nothing is read from the process unless the caller passes it in. The CLI is the place that fills these from the real process.
   - It accepts a `context.Context`. Cancelling it stops the run and cleans up child processes, the same way Ctrl-C does for the CLI.
   - It returns errors as values. The error categories and locations match the CLI's, so a host can branch on them without parsing text.
   - It has no package-level mutable state, so two runs in one process do not interfere.

## Engineering standards **[owner]**

These apply to every package in the module. The implementation plan checks them.

1. **Pure and impure functions are segregated.** A pure function does no I/O and reads no process, environment, clock, or filesystem state; its output depends only on its inputs. An impure function does any of those. Each function's doc comment says which it is.
2. **Impure functions live behind interfaces in a separate package.** `internal/sys` defines the interfaces (filesystem and process execution), the OS implementations, and test fakes in `internal/sys/sysfake`. Pure packages never import `os`, `os/exec`, or `time` directly. Impure code receives the interfaces as parameters.
3. **Coverage.** Statement coverage is above 95% overall, and every function has 100% coverage: each one runs under at least one test.
4. **Cyclomatic complexity** is below 10 for every function.
5. **Performance benchmarks are built in.** Hot paths have `Benchmark` functions in their own package. `make bench` runs them, and the results are recorded in `docs/benchmarks/`.

## Non-goals

- Reproducing upstream's Rust crate API. The Go library is a new API designed for Go hosts. It mirrors upstream's behavior, not its Rust types.
- Adding new justfile syntax, settings, attributes, or functions beyond upstream v1.59.0. Anything new goes through a separate design.
- Replacing the shell. Recipes still run through the configured shell (`sh` by default, or whatever `set shell` says).
- A plugin system.

## Anti-goals

These would make the port a failure even if it compiled and passed the basic tests:

- Silent divergence from upstream on a valid justfile. A recipe that runs differently, or a variable that evaluates differently, is a bug even if the output looks reasonable. A documented, explicit refusal under strict path rules is not silent divergence.
- Performance gains that change output, ordering, or error reporting.
- Security defaults that are weaker than upstream's, unless the user opts in to the weaker behavior explicitly.
- Hidden network access or telemetry.

## Compatibility scope

**In scope:**
- The full justfile grammar and evaluation semantics for v1.59.0.
- All settings, attributes, and builtin functions listed above.
- Modules and imports, including path resolution relative to the importing file, subject to the strict path rule.
- Dotenv loading with all `dotenv-*` settings, subject to the strict path rule.
- Shell selection (`sh`, `bash`, `pwsh`, `powershell`, `cmd`, and custom interpreters).
- Unstable gating: `set unstable` and `--unstable` are refused as upstream refuses them without the flag. The features behind them are not implemented in v1. **[owner]**
- CLI behavior listed under Goals.
- Linux, macOS, and Windows, with Windows shell parity (`pwsh`, `powershell`, `cmd`). BSD is built and tested on a best-effort basis. **[owner]**

**Out of scope for now:**
- Upstream features added after v1.59.0.
- Cached recipes, the `lists` setting, and user-defined functions. These are later milestones. **[owner]**
- Byte-identical shell completion scripts. **[my call]** `just completions` must produce working completions for bash, zsh, fish, and PowerShell, generated from the same flag definitions the CLI uses. The output does not have to match upstream's text byte for byte.

## Constraints

- Go. **[my call]** Use the latest stable Go release at implementation start, and pin the minimum in `go.mod`.
- Single static binary per platform. No runtime dependencies beyond the configured shell.
- **License.** `just-go` is MIT-licensed. **[owner]** The copyright holder line is not yet confirmed. **[to confirm]** Upstream `casey/just` v1.59.0 is CC0-1.0 (its `LICENSE` file and `Cargo.toml` agree) and ships no NOTICE file. CC0 imposes no obligation on the port. **[my call]** The README credits upstream anyway.
- Prefer the standard library. Each third-party dependency needs a justification in the plan. **[my call]** Likely candidates: a CLI flag parser that covers upstream's clap behavior, a BLAKE3 implementation, and a Windows console library if needed.

## Proposed architecture **[my call]**

One engine, two front ends.

**Public library (the module root package, `gitea.com/wimwian/just-go`):** the only API a host imports. Its surface is small:
- Load a justfile from a path, from a string, or by searching from a directory.
- Inspect it: list recipes, groups, variables, and attributes, and dump it as JSON, matching upstream's `--dump` format.
- Run one or more recipes with an `Options` value that carries everything in Goal 5, including the strict path opt-out.
- Typed errors and a result value that reports which recipes ran and their exit status.

Everything not listed above stays unexported, so the API can change without breaking hosts. **[owner]** The API starts at `v0`, so it may still change until a real host has used it.

**Command (`cmd/just-go`):** a thin `main` that parses upstream-compatible flags, fills an `Options` value from the real process (`os.Args`, `os.Environ`, the working directory, the standard streams), calls the library, and maps results to exit codes. All subcommands are implemented here on top of the same library calls. The CLI adds no behavior the library lacks.

**Internal packages (`internal/...`):** not importable by hosts.
- `sys`: the impure boundary. Defines the `FS` and `Process` interfaces, the OS implementations, and `sysfake`. It is the only package that reaches the outside world. Everything else is pure, or receives these interfaces as parameters.
- `lexer`, `parser`, `ast`: justfile source to syntax tree, with upstream's error locations.
- `analyzer`: name resolution, duplicate checks, recipe dependency graph, unstable and minimum-version gating.
- `eval`: expression and function evaluation, including builtins.
- `loader`: justfile discovery (walking up directories), imports, modules, dotenv, and the strict path check.
- `runner`: recipe execution, shells, parallel execution, `[confirm]`, `[continue]`, and signal handling.
- `cache`: reserved for the later milestone that implements cached recipes. Not built in v1.
- `output`: formatting for `--list`, `--summary`, `--show`, and the other human-readable outputs. The CLI uses it. The library exposes data, not formatted text, so hosts render their own.

**Testing the library:** the conformance suite runs through the CLI, since that is what upstream's tests exercise. The library gets its own tests against the same engine, plus a small set of embedding tests: two concurrent runs in one process, cancellation mid-run, custom stdout and stderr capture, and a run with no `os.Environ` leaking in.

## Testing strategy **[my call]**

- **Conformance suite.** Upstream's roughly 1,900 `#[test]` functions are Rust code, not a data corpus. Extract each case into a data format (a justfile, arguments, expected stdout, stderr, and exit code) and run it against `just-go`. Expected output is generated by running the pinned upstream v1.59.0 binary, built from the pinned commit, not hand-copied from Rust assertions. Hand translation is ruled out because it is the largest schedule risk. The port must pass every case upstream passes, with a documented exception list for any case that depends on Rust-specific behavior or on the strict path rule.
- **Oracle build.** Building the pinned upstream binary needs a Rust toolchain. This is used only to generate expected outputs and to run differential tests. It is not a runtime dependency of `just-go`, and CI caches the built binary.
- **Differential testing.** A corpus of justfiles (from the conformance suite and fuzzed inputs) runs through both the upstream binary and `just-go`. Any difference in output or exit code is a bug, or an entry in the exception list with a reason.
- **Platform matrix.** Linux, macOS, and Windows must pass the full suite for v1. BSD runs the suite on a best-effort basis, with failures recorded in the exception list. **[owner]**
- **Benchmarks.** Two kinds. Go `Benchmark` functions in each hot-path package, run by `make bench` (see Engineering standards). And a fixed set of end-to-end workloads, run on the same hardware for both binaries, reported as before/after numbers.
- **Coverage and complexity gates.** Statement coverage above 95% overall, 100% function coverage, and cyclomatic complexity below 10 for every function. These are checked by the build, not left to review (see Engineering standards).
- **Fuzzing.** Go's native fuzzing on the lexer and parser, with a crash-free requirement.

## Threat model and security scope

A justfile is code. Running `just` on a justfile executes its recipes, and that is the feature. Security here does not mean stopping a justfile from running commands. It means the port itself must not add risk beyond what the user asked for.

**Trust boundaries:**
- The justfile and the modules and imports it pulls in are trusted to the same degree as the user running it.
- Environment variables, dotenv files, and command-line arguments are inputs. They must not reach a shell unquoted where upstream quotes them, and must not be interpreted in places where upstream treats them as data.
- **Strict path rule. [owner]** Paths used by `read()`, `import`, `mod`, and dotenv loading must resolve inside the directory containing the root justfile. This applies by default.
  - **Containment is checked on the resolved path**, after symlinks are followed. A symlink inside the project that points outside it is refused. **[my call]**
  - Subdirectories of the project directory are allowed, so imports and modules in subfolders work.
  - A refused path is an explicit error that names the path and the rule. **[my call]** The error text is the author's wording to review.
  - **Opt-out.** The CLI has a flag to allow outside paths for one run, and the library has a matching `Options` field. **[my call]** The flag name is chosen in the plan. Nothing in the justfile syntax changes, so this does not add a setting.
  - The opt-out is the only way to match upstream on these paths. Hosts that embed the library should leave strict rules on unless they have a reason to lift them.

**Embedding changes who decides the inputs.** When `just-go` runs as a library, the host chooses the environment, working directory, and file paths, not the user at a terminal. The library must not silently inherit `os.Environ` or the process working directory. The CLI inherits them because a terminal user expects it. A host gets exactly what it passes in, and any inheritance is an explicit option. **[my call]**

**Specific concerns to cover in the plan:**
- Path handling for `import`, `mod`, dotenv, and `read()`: symlinks, relative paths, `..` segments, and case-insensitive filesystems on macOS and Windows.
- Cache integrity, when the cache is built in a later milestone: a cached recipe result must not be reused if its inputs changed. The cache must be created with restrictive permissions.
- Temporary files and directories (`tempdir`, script recipes): created with restrictive permissions, cleaned up on normal exit and on signal.
- Signal handling: child processes are cleaned up on Ctrl-C and SIGTERM, matching upstream's `[continue]` and signal behavior.
- `--edit`, `--choose`, and custom shell or interpreter settings execute external programs. Their arguments are constructed, not concatenated from user input.
- Secrets: values from dotenv and `env()` are not printed by `--dump` or error messages beyond what upstream prints. **[my call]** Match upstream here, then review.

## What is left to the builder

- The exact package layout and interfaces.
- The choice of CLI parsing library and the Windows console handling.
- The name of the strict-path opt-out flag and the exact error text for refused paths.
- The benchmark workloads and the performance targets. These should be set after the first measured baseline against upstream, not before.
- The order of implementation.
- How the conformance exception list is kept and reviewed.

## Decisions made by the owner **[owner]**

- Module path: `gitea.com/wimwian/just-go`.
- API version: `v0` until a host has used it.
- Unstable features: gated only in v1. Cached recipes, the `lists` setting, and user-defined functions are later milestones.
- Platform matrix: Linux, macOS, and Windows must pass the full suite, including Windows shell parity. BSD is best-effort.
- Strict paths: on by default for `read()`, `import`, `mod`, and dotenv, bounded by the directory containing the root justfile, with an opt-out. This is a documented divergence from upstream's compatibility goal.
- License: MIT.
- Engineering standards: pure and impure functions segregated, with impure functions behind interfaces in `internal/sys`; more than 95% statement coverage and 100% function coverage; cyclomatic complexity below 10; built-in benchmarks. Added to the spec after its approval, as standing requirements.

## Decisions made by the author **[my call]**

- Reference version is upstream v1.59.0, pinned by commit.
- Priority order is compatibility, then security, then performance.
- Windows is in scope, as the owner's platform matrix requires.
- Completions must work for bash, zsh, fish, and PowerShell, and are generated, not hand-written. Byte-match with upstream is not required.
- Standard library first; dependencies justified in the plan.
- Testing relies on a conformance suite extracted from upstream's tests, with expected output generated by the pinned upstream binary, and differential testing against that binary.
- Upstream's CC0-1.0 license imposes no obligation; the README credits upstream.
- One engine behind a public library and a thin CLI. The library never reads process state; the CLI fills its options from the process.
- Public API limited to load, inspect, and run, with data results instead of formatted text. Everything else stays internal.
- Strict path containment is checked after symlink resolution, and subdirectories of the project are allowed.
