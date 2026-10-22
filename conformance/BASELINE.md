# Conformance baseline

This file is written by hand at each milestone gate. `make corpus`
regenerates `conformance/corpus/README.md` and `cases.jsonl`, and does not
touch this file.

`conformance/baseline.txt` lists the cases `bin/just-go` passes. The
ratchet (`make ratchet`) only adds to it, and `TestBaseline` (`make baseline`)
fails if any of them stops passing. `make check` runs that gate.
`make conformance-report` runs the full corpus and prints the pass, fail, and
skip counts. It is allowed to fail.

| | |
|---|---|
| Recorded | 2026-10-22, after the whole-branch review fixes, `bin/just-go` built from the commit that adds this file |
| Corpus cases | 2011 |
| Cases in the baseline | 125 |
| Full run: pass | 125 |
| Full run: fail | 1870 |
| Full run: skip (oracle exceptions) | 16 |

The gate proves only the 125 baseline cases, so the honest pass rate is 125
of the 2011 cases. Of the 1870 failures, 790 pass a command-line flag before
the first recipe name that Plan 1 does not implement (for example
`--evaluate` or `--dump`). The other 1080 fail on other differences from
upstream that are outside Plan 1's scope, such as syntax beyond the core
subset or parse-error wording. Upstream adds `--shell bash` to 1949 cases.
`--shell` is in Plan 1's scope, and no case fails on it.

## History

| Recorded | Baseline | Pass | Fail | Skip |
|---|---|---|---|---|
| Milestone 1 gate, `bin/just-go` from `bfd0b56` | 80 | 80 | 1915 | 16 |
| After the whole-branch review fixes | 125 | 125 | 1870 | 16 |
| Compat Task 1: triage and the core-subset defects | 130 | 130 | 1865 | 16 |
| Compat Task 2: brace escapes and string literal forms | 142 | 142 | 1853 | 16 |
| Compat Task 3: parser error wording, delimiters, and code escapes | 168 | 168 | 1827 | 16 |
| Compat Task 4: analyzer checks and function calls | 177 | 177 | 1818 | 16 |
| Compat Task 5: dependency arguments and subsequents | 195 | 195 | 1800 | 16 |
| Compat Task 6: settings, group A | 260 | 260 | 1735 | 16 |
| Compat Task 7: settings, group B (dotenv, lists, unstable gate) | 273 | 273 | 1722 | 16 |
| Compat Task 8: attributes, group A | 329 | 329 | 1666 | 16 |
| Compat Task 9: attributes, group B | 360 | 360 | 1635 | 16 |

The review fixes added 45 cases. Most are error cases: the command line now
prints upstream's diagnostic format (the file's base name, `line:column`, the
quoted line and carets), so a case that reports an error can match stderr
byte for byte when its message matches.

## Platform-dependent baseline entries

The corpus was recorded on macOS, and the baseline was measured on macOS. Of
the 360 baseline cases exactly one expects output that depends on the
operating system: `os_attributes::os#1`. Its justfile defines `foo` once per
operating-system gate (`[macos]`, `[linux]`, `[windows]`, and the BSDs), and
the recorded output is the `[macos]` recipe's (`bar`). On Linux the `[linux]`
recipe runs and the case fails. The other `os_attributes` baseline cases
(`all`, `assignment`, `none`, `os_family`, `setting`) use `[unix]` or every
gate, and give the same output on any Unix. A scan of the other baseline
cases for gates, `os()`, `arch()`, `windows-shell`, and backslash or path
dependence found no other host-dependent case: the `arch()` cases fail to
parse before `arch()` is evaluated, and the `windows` cases do not select a
shell.

This entry is not an oracle exception (the oracle passes it on macOS and
fails it elsewhere, and the closed categories of R9 and R16 do not cover a
recording-platform difference). Task 33 (the platform matrix and
`docs/platforms.md`) owns it: before a Linux or Windows job gates on
`make check`, that task must either skip this case off the recording
platform, with the reason printed, or record the per-platform result. Until
then the gate is macOS-only.
