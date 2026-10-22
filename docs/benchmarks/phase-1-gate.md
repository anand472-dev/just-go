# Phase 1 gate

Recorded 2026-10-22 (compat Task 13) on an Apple M1 Pro, `darwin/arm64`.

| Gate | Result |
|---|---|
| `make check` (vet, test, baseline) | pass |
| `make cover` | total statement coverage 100.0%, every function at 100.0% |
| `make cyclo` (`gocyclo -over 9 .`) | prints nothing |
| `make bench` | every Benchmark function runs; output committed as `go-bench.txt` (R12) |
| `make conformance-report` | 485 pass, 1510 fail, 16 skip (of 2011) |
| `make ratchet` | 485 passing, 485 in baseline (0 new) |

The 1510 failures are outside Phase 1: Phases 2 to 5 own them, and the
unstable-only families (R15) stay failing by decision. The 16 skips are the
oracle exceptions of the closed categories in R16.

`go-bench.txt` is the baseline for the performance work in later phases.
Run-to-run noise on this machine, measured as the ns/op difference of the 58
benchmarks between the `go-bench.txt` of `c589227` and this one (no Go source
differs between the two), has a median of 0.4% and a largest value of 5.1%
(`BenchmarkMergeEnv`); 57 of the 58 differ by less than 5%.

## Reproducing the gate

Run from the repository root with `gocyclo` installed (`make tools`). The
counts hold for any commit whose Go sources equal commit `c589227`
(`git diff c589227 HEAD -- '*.go'` prints nothing).

```
make check cover cyclo bench   # vet, test, baseline; 100.0% coverage; no gocyclo output
make conformance-report        # exits 1 by design: 485 pass, 1510 fail, 16 skip (of 2011)
make ratchet                   # ratchet: 485 passing, 485 in baseline (0 new)
```

`make bench` rewrites `go-bench.txt`; ns/op varies by up to about 5% from run
to run, but the set of Benchmark functions and their PASS result does not.
