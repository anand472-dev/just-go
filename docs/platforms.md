# Platforms

The spec's platform matrix: Linux, macOS and Windows must pass the full suite,
Windows shell parity included; BSD is best-effort.

## What has run where

| Platform | Full conformance suite | Build, vet and test compile | Notes |
|---|---|---|---|
| macOS (arm64) | yes, the recorded run | yes | The corpus was recorded on macOS; `machine-env` and `path-hash` exceptions come from there. |
| Linux | not run in this repository yet | not checked here | Required by the spec. |
| Windows (amd64) | not run; the Windows shell cases (`pwsh`, `powershell`, `cmd`) have not been exercised | `make cross` passes: vet and the `internal/sys` test binary compile | Required by the spec. Needs a Windows host to verify the shells. |
| FreeBSD (amd64) | not run | `make cross` passes: vet | Best-effort. |
| OpenBSD (amd64) | not run | `make cross` passes: vet | Best-effort. |

`make cross` checks that the code builds and vets for the platforms above. It
does not run a test, so a passing `make cross` says nothing about behavior.

## Exception categories per platform

The exceptions in `conformance/oracle-exceptions.txt` are the closed set of
divergences; each names its category.

| Category | Cause | Platforms it applies to |
|---|---|---|
| `path-hash` | a cache file named after the recorder's temporary path | every platform the recording machine is not |
| `machine-env` | a variable or fact inherited from the recording machine | every platform the recording machine is not |
| `fifo` | a named pipe fed by a thread the corpus cannot record | every platform |
| `strict-path` | a path upstream allows that the strict path rule refuses (the documented divergence, lifted by `--allow-outside-project-paths`) | every platform |
| `unsupported-shell` | a completion script for elvish or nushell, which the spec does not target | every platform |

## Closing the gaps

- Windows: run `go test ./...` and `./conformance` on a Windows host, with the
  shell cases, and record the result here. The shell settings that apply there
  are `windows-shell` and `windows-powershell`.
- BSD: run the conformance suite on FreeBSD or OpenBSD if a host is available,
  and record the result in the table above.
- Linux: run the suite on a Linux host and record it.
