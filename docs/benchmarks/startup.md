# Startup benchmark

The first measured startup comparison between the pinned oracle and
`just-go`. These are baseline numbers only. Performance targets are set from
them in a later plan, not here.

## Method

`sh scripts/bench-startup.sh` runs `--justfile conformance/bench/justfile
--list` 20 times for each binary, after one untimed warm-up run, with output
sent to `/dev/null`. It prints the mean wall time of one invocation. Both
binaries read the same file and print the same listing (checked byte for
byte). The fixture has two assignments, eight recipes, parameters with
defaults, dependencies, and a mix of documented and undocumented recipes.

## Results

Recorded on 2026-10-22.

| Binary | Mean wall time per `--list` (20 runs) |
|---|---|
| `oracle/bin/just` (just 1.59.0, commit `d6fee036379c`) | 14.09 ms |
| `bin/just-go` (built from commit `722d05d`) | 11.42 ms |

Two earlier runs of the script on the same machine, the same day, gave
14.45 / 11.54 ms and 13.32 / 11.11 ms, so run-to-run noise is about 1 ms.

## Machine

- Apple M1 Pro, 10 cores, 32 GiB memory
- macOS 27.2 (build 26B5091g), darwin/arm64
- Go 1.27.2

## Notes

- Most of each number is process creation on macOS, not work in either
  binary. A comparison of parse, evaluation, and dispatch cost belongs to the
  Go benchmarks in `docs/benchmarks/go-bench.txt` (`just bench`) and to the
  end-to-end workloads of a later plan.
- `just-go` implements only the core subset, so it does less work at startup
  than the oracle. The comparison becomes meaningful once the feature plans
  have landed.
