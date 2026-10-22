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
Run-to-run noise on this machine is a few percent.
