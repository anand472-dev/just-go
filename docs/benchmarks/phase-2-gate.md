# Phase 2 gate

Recorded 2026-10-23 (compat Tasks 16 to 20) on an Apple M1 Pro, `darwin/arm64`.

| Gate | Result |
|---|---|
| `make check` (vet, test, baseline) | pass |
| `make cover` | total statement coverage 100.0%, every function at 100.0% |
| `make cyclo` (`gocyclo -over 9 .`) | prints nothing |
| `make bench` | every Benchmark function runs; output committed as `go-bench.txt` (R12) |
| `make ratchet` | 541 passing, 541 in baseline (8 new: `functions::semver_matches#1` and the seven `constants::*` cases) |
| builtin table | 83 names; 79 implemented; `bool`, `join_list`, `show` and `split` are not, because they need `set lists` (R15) |
| triage `builtin:*` groups | empty |

## What the corpus can and cannot show

The 541 passing cases are the ones that run without `--evaluate`, `--dump`,
`--color` or `set lists`. Most of the Phase 2 families use those flags, so
their corpus cases stay failing for a Phase 4 or R15 reason, not a Phase 2
one:

| Family | Failing cases | Blocked by |
|---|---|---|
| `style` | 44 `cli:evaluate`, 4 `unstable:lists`, 1 `cli:command-color` | `--evaluate`, `set lists`, `--command-color` |
| `directories` | 8 `cli:evaluate` | `--evaluate` |
| `datetime` | 4 `cli:evaluate` | `--evaluate` (and the output is the clock, which no case can fix) |
| `functions` (hashing, uuid) | 5 `cli:evaluate` | `--evaluate` (uuid is random) |
| `constants` | 4 `cli:evaluate`, 1 `cli:request` | `--evaluate`, `--request` |

So the Phase 2 functions are verified by three other means, not by the corpus
count: the unit tests in `internal/eval/builtins`, whose expected values were
read from the oracle (`semver_matches` 162 rows, the BLAKE3 and SHA-256 digests
at the chunk and tree edges, the 63 `style` token results, the macOS
directories); and direct comparisons of `just-go` and the oracle on the same
recipes, which were identical for the directory, hash, semver, datetime,
style and constant outputs.

## Rulings that the gate depends on

- BLAKE3 is ported from the specification; the module takes no dependency.
- `semver_matches` follows the semver 1.0.28 crate that upstream 1.59.0 uses.
- The Unix and Windows directory rules follow dirs 6.0.0 source; only the
  macOS rules were run against the oracle. Windows folders come from the
  profile variables, not the known-folder API.
- `style` refuses `stdout` and `stderr` until `--color` lands (Phase 4).
- `warning` is not a function; upstream has only the style name `warning`.
- Constants are part of Phase 2, though the plan's tasks do not name them.
- Regular expressions keep Go's ASCII `\w`, `\d`, `\s` and `\b` (RE2). This is
  an accepted engine difference, not fixed; see the ledger.

## Reproducing the gate

Run from the repository root with `gocyclo` installed (`make tools`).

```
make check cover cyclo bench   # vet, test, baseline; 100.0% coverage; no gocyclo output
make ratchet                   # ratchet: 541 passing, 541 in baseline (0 new)
```

`make bench` rewrites `go-bench.txt`; ns/op varies by up to about 5% from run
to run, but the set of Benchmark functions and their PASS result does not.
The benchmarks added in this phase are `BenchmarkBLAKE3`,
`BenchmarkSemverMatches` and `BenchmarkStyle`.
