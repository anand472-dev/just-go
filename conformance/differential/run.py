#!/usr/bin/env python3
"""Differential check of settings behaviour: runs each case in cases*.json
(Task 6 in cases.json, Task 7 in cases-unstable.json, Task 8 in
cases-attributes.json) against the oracle and against bin/just-go, and
compares exit status, stdout and stderr (the temporary directory is masked as
$D). A case may set "env", added to the environment of both, "cwd", the
directory under the temporary one to run in, and "stdin", the input of both.

Usage: ORACLE=oracle/bin/just MINE=bin/just-go python3 conformance/differential/run.py
Both default to the paths above, relative to the repository root. Exit status
is 1 when a case differs that is not marked "known" in cases.json (a known
case records a divergence a task report lists; it is printed, not failed). This is a developer check, not part of make check:
it needs the oracle binary, which is built by scripts/build-oracle.sh.
"""
import glob
import json
import os
import subprocess
import sys
import tempfile

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
ORACLE = os.path.abspath(os.environ.get("ORACLE", os.path.join(ROOT, "oracle/bin/just")))
MINE = os.path.abspath(os.environ.get("MINE", os.path.join(ROOT, "bin/just-go")))
CASES = sorted(glob.glob(os.path.join(os.path.dirname(os.path.abspath(__file__)), "cases*.json")))


def run(binary, case):
    with tempfile.TemporaryDirectory() as d:
        for name, text in case["files"].items():
            path = os.path.join(d, name)
            os.makedirs(os.path.dirname(path), exist_ok=True)
            with open(path, "w") as f:
                f.write(text)
        wd = os.path.join(d, case.get("cwd", ""))
        os.makedirs(wd, exist_ok=True)
        r = subprocess.run(
            [binary, "--shell", "bash"] + case.get("args", []),
            cwd=wd, capture_output=True, text=True, input=case.get("stdin", ""),
            env={"PATH": "/usr/bin:/bin", "HOME": d, **case.get("env", {})},
        )
        return (r.returncode, r.stdout.replace(d, "$D"), r.stderr.replace(d, "$D"))


def main():
    cases = []
    for path in CASES:
        with open(path) as f:
            cases += json.load(f)
    bad = known = 0
    for c in cases:
        a, b = run(ORACLE, c), run(MINE, c)
        if a != b:
            label = "KNOWN" if "known" in c else "DIFF"
            if "known" in c:
                known += 1
            else:
                bad += 1
            print(label, c.get("name", ""), json.dumps(c["files"])[:200], c.get("args"))
            print("  oracle", a)
            print("  mine  ", b)
    print("cases", len(cases), "diffs", bad, "known", known)
    return 1 if bad else 0


if __name__ == "__main__":
    sys.exit(main())
