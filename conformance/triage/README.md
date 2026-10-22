# Conformance triage

`groups.json` sorts every case that fails the full conformance run into
exactly one group, with a one-line reason. Later tasks of the compatibility
plan name these groups as the cases they cover. `scripts/triage.py` writes it;
do not edit it by hand.

```sh
scripts/triage.py                       # builds bin/just-go, runs the suite, writes groups.json
scripts/triage.py --input events.json   # triages a saved `go test -json` run instead
```

`TestTriageCoversEveryFailure` (`conformance/triage_test.go`) keeps it honest.
Every failing case is in exactly one group. The groups hold exactly the
failing count. Pass, fail, and skip add up to the corpus. No grouped case is
an oracle exception or a baseline case. The `core-defect` group is empty.
When a change makes cases pass, run `make ratchet` and then
`scripts/triage.py`, and commit both. A ratcheted case that is still in a
group fails the test.

## Format

```json
{
  "counts": {"pass": 130, "fail": 1865, "skip": 16, "total": 2011},
  "group_sizes": {"attribute": 220, "...": 0},
  "groups": {"attribute": {"<case id>": "<reason>", "...": "..."}}
}
```

## Groups

The script tries the rules in this order, and the first match wins. A case
that uses several features lands in the earliest of them. For example, an
unstable case that also passes `--evaluate` is `unstable:lists`, not
`cli:evaluate`.

| Group | The case |
|---|---|
| `unstable:<family>` | Uses an unstable feature (`cached-recipes`, `lists`, `user-defined-functions`) or expects its gate error. Logical operators, comparisons, and `which()` need `set lists`, so they are `lists`. Out of scope (ruling R15). |
| `completions` | Passes `--completions` or sets `JUST_COMPLETE`. |
| `cli:<flag>` | Passes a flag the command does not implement, sets the `JUST_*` variable of one, or uses a command-line form the command does not implement: `cli:set` for `NAME=VALUE`, `cli:search-directory` for `dir/recipe`, `cli:justfile` for `--justfile -`, and `cli:list` for `--list <module>`. |
| `module` | Uses `mod` or `import`, or names a `module::recipe` path. |
| `dotenv` | Has a `.env` file or a `dotenv-*` setting. The settings are parsed and validated; the cases that remain need a file loaded or a command run (Phase 3). |
| `setting:<name>` | Sets a setting that has no effect yet: `default-script` (needs script recipes, Task 11) and `fallback` (needs the search of parent justfiles, Phase 3). |
| `attribute` | Uses an attribute the parser does not implement yet: `[arg]`, which no task owns yet, or `[cache]`, which is unstable (Tasks 8 and 9 implemented the rest). A case that uses only the others is sorted by what else it uses. |
| `shebang` | Has a shebang recipe or a `[script]` recipe. |
| `conditional` | Uses an `if` expression. A case that also uses a backtick is here: backticks are implemented (Task 10), so only the `if` keeps it failing. |
| `builtin:<family>` | Calls a builtin function, or uses a builtin constant (`builtin:constants`). Families: case, constants, datetime, directories, env, error, external, hash, invocation, path, random, semver, string, style, system. |
| `grammar` | Uses other syntax outside the implemented subset (`alias`, `unexport`), or gets a parse error from just-go that upstream does not report or words differently (the reason quotes it), or gets a compile error that just-go words differently. |
| `core-defect` | Uses only the implemented subset and still fails. Must be empty. |
| `other` | Fails for a reason no rule above explains. |

## Current counts

From the run recorded in `groups.json`: 415 pass, 1580 fail, 16 skip, of
2011 cases.

| Family | Cases |
|---|---|
| `cli:*` | 655 |
| `unstable:*` | 382 |
| `attribute` | 112 |
| `module` | 157 |
| `grammar` | 76 |
| `builtin:*` | 63 |
| `completions` | 40 |
| `dotenv` | 21 |
| `setting:*` | 12 |
| `conditional` | 24 |
| `shebang` | 38 |
| `core-defect` | 0 |
| `other` | 0 |

Before this task's fixes, the triage left 5 cases outside every feature
group: 1 in `core-defect` and 4 in `other`. All 5 were defects in the
implemented subset:

- a snippet at column 0 for a shell that cannot be found, and that error's
  wording
- the `usage:` line after a wrong number of arguments
- the `Did you mean` suggestion for an unknown recipe

All 5 are fixed, and those cases now pass and are in the baseline. The same
change also fixes the column after multibyte text. No corpus case
exercised it, so it is pinned by unit tests.
