# Phase 5 gate

Recorded 2026-10-23 (compat Task 31, with Tasks 32 and 33 limited to what this
machine can check) on an Apple M1 Pro, `darwin/arm64`.

Phase 5 was started ahead of Phase 4's Tasks 26 to 30, by the controller's
direction. Phase 4 is not complete: `--dump-format json`, Tasks 27 to 29, and
the Phase 4 gate are still open.

| Gate | Result |
|---|---|
| `make check` (vet, test, baseline) | pass |
| `make cover` | total statement coverage 100.0%, every function at 100.0% |
| `make cyclo` (`gocyclo -over 9 .`) | prints nothing |
| `make bench` | every Benchmark function runs; output in `go-bench.txt` |
| `make cross` | vet passes for windows, freebsd and openbsd; the windows `internal/sys` test binary compiles |
| `make ratchet` | 891 passing, 891 in baseline (26 new: completions) |
| oracle exceptions | 23 skipped: 16 from Phase 1, 3 machine-env, 2 strict-path, 2 unsupported-shell |

## What the corpus shows

Completions: 26 of 40 pass. The rest:

| Cases | Why |
|---|---|
| 6 (`aliases_*`, `clean_aliases`, `list_module_aliases_*`, `private_aliases_excluded`) | aliases are not implemented in the grammar |
| 2 (`list_modules`, `clean_recipes_and_modules`) | module candidates for `--list` and `--clean` are not offered |
| 1 (`show_malformed_path`) | the expected candidate list repeats `foo`; not traced |
| 1 (`completion_scripts#3`) | fish: ignored by the controller's ruling |
| 2 (`completion_scripts#2`, `#4`) | elvish and nushell: exceptions, category `unsupported-shell` |

Completion scripts are checked with `bash -n`, `zsh -n`, and pwsh's parser.

## Rulings that the gate depends on

- elvish and nushell are not target shells; `--completions` refuses them, and
  the two oracle cases are recorded under the new category `unsupported-shell`.
  The exception test treats that category as oracle-passing, like `strict-path`.
- fish is ignored, by the controller's direction. No fish script is generated.
- Candidates follow the corpus order. The long flags come from the same table
  the parser reads, so a flag added there is parsed and completed.

## Platforms

Only macOS has run the suite. Windows and BSD are compile-checked by
`make cross`, which runs no tests. `docs/platforms.md` lists what has run where.
Windows shell parity (Task 32) is not verified: it needs a Windows host.

## Reproducing the gate

Run from the repository root with `gocyclo` installed (`make tools`).

```
make check cover cyclo bench   # vet, test, baseline; 100.0% coverage; no gocyclo output
make cross                     # windows, freebsd, openbsd: vet and compile
make ratchet                   # ratchet: 891 passing, 891 in baseline (0 new)
```
