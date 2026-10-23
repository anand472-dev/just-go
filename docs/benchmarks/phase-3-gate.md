# Phase 3 gate

Recorded 2026-10-23 (compat Tasks 21 to 25) on an Apple M1 Pro, `darwin/arm64`.

| Gate | Result |
|---|---|
| `make check` (vet, test, baseline) | pass |
| `make cover` | total statement coverage 100.0%, every function at 100.0% |
| `make cyclo` (`gocyclo -over 9 .`) | prints nothing |
| `make bench` | every Benchmark function runs; output committed as `go-bench.txt` (R12) |
| `make ratchet` | 631 passing, 631 in baseline (90 new: imports, modules, dotenv) |
| oracle exceptions | 21 skipped: 16 from Phase 1, 3 machine-env, 2 strict-path (see below) |

## What the corpus shows

The 90 new passing cases are the imports, modules and dotenv cases that run
without `--list`, `--dump`, `--evaluate`, the command-line dotenv flags, or
`set lists`. The families that stay failing are below. Each reason is a later
phase or a decision, not a bug in this phase.

| Family | Passing | Failing, and why |
|---|---|---|
| imports | 18 | `--dump` (2), `--list` load order (1), `--working-directory` (1), and a submodule case (1, module-dependent) |
| modules | 50 | aliases (grammar: `alias` is not implemented), `--list` and `--usage`, `unexport` and user functions (grammar), the `--verbose` flag, module settings (`set working-directory`, `set shell`, `set dotenv-load` inside a module), and exports from a module (environment names) |
| dotenv | 27 | the command-line dotenv flags (`--dotenv-path`, `--dotenv-filename`, `--dotenv-command`, `--no-dotenv`), `dir/recipe` searches, and `set lists` for dotenv lists; one fifo case is an oracle exception |

Settings inside an imported file or a module are not merged into the root;
that is the largest remaining gap in this phase.

## Rulings that the gate depends on

- Imports merge in upstream's order: the root's items first, then each import
  from a stack (analyzer.rs). A root definition overrides an import only where
  duplicates are allowed.
- Modules are merged under their names, `name::item`, with every reference
  inside the module renamed to match. A module's recipes run in the module
  file's directory.
- Module file discovery follows upstream's `find_module_file`: `name.just`,
  `name/mod.just`, and the justfile names in `name/` in any case, or the path
  given. More than one candidate is an error.
- Optional absent modules disable the recipes that depend on them. A request
  for one of those recipes is an error naming the module.
- A dotenv file is looked for in the justfile's directory and each directory
  above it. Values already in the environment are kept unless
  `dotenv-override` is set. A failing `dotenv-command` exits with its status.
- The strict path rule refuses an import, module, dotenv file, `read()`, or
  `canonicalize()` outside the root justfile's directory, after symbolic links
  are resolved. The message names the opt-out,
  `--allow-outside-project-paths`. Upstream allows these paths, so the rule is
  the documented divergence. Two corpus cases that upstream allows are recorded
  under the `strict-path` exception category, which the oracle passes.

## Reproducing the gate

Run from the repository root with `gocyclo` installed (`make tools`).

```
make check cover cyclo bench   # vet, test, baseline; 100.0% coverage; no gocyclo output
make ratchet                   # ratchet: 631 passing, 631 in baseline (0 new)
```

`make bench` rewrites `go-bench.txt`; ns/op varies by up to about 5% from run
to run, but the set of Benchmark functions and their PASS result does not.
