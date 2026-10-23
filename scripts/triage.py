#!/usr/bin/env python3
"""Sort every failing conformance case into one feature group.

Usage: scripts/triage.py [--input EVENTS.json] [--output GROUPS.json]

Without --input it builds bin/just-go and runs the conformance suite with
`go test -json` (JUST_GO_BIN=bin/just-go). It then reads each failing case
from conformance/corpus/cases.jsonl, together with the actual output the
harness printed for it, and assigns the case to exactly one group with a
one-line reason. The result is written to conformance/triage/groups.json.

The groups, in the order the rules are tried (the first that matches wins):

  unstable:<family>  uses a feature upstream gates behind --unstable
  completions        shell completion scripts or dynamic completion
  cli:<flag>         passes a command-line flag (or JUST_* variable, or a
                     NAME=VALUE override) the command does not implement, or
                     has a recipe run just_executable() with one
  module             `mod` or `import`
  dotenv             .env loading and the dotenv settings (which are parsed
                     and validated, but nothing loads a file yet)
  setting:<name>     a `set <name>` the parser does not implement
  attribute          an attribute the parser does not implement (all are
                     implemented but `arg`, and `cache`, which is unstable)
  shebang            a shebang or [script] recipe
  builtin:<family>   a call to a builtin function
  grammar            any other syntax outside the implemented subset, or a
                     parse error worded differently from upstream's
  core-defect        uses only the implemented subset and still fails
  other              fails for a reason none of the rules above explains

Standard library only. Pure apart from main(), which runs commands and
reads and writes files.
"""

import argparse
import json
import os
import re
import subprocess
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
CORPUS = os.path.join(ROOT, "conformance", "corpus", "cases.jsonl")
GROUPS = os.path.join(ROOT, "conformance", "triage", "groups.json")
SUBTEST = "TestConformance/"

# Command-line flags the command implements (cmd/just-go longFlagName).
SUPPORTED_FLAGS = {"justfile", "list", "quiet", "shell", "shell-arg", "unstable"}
VALUE_FLAGS = {"justfile", "shell", "shell-arg"}
# Upstream's one-letter flags (src/arguments.rs) by their long name.
SHORT_FLAGS = {
    "E": "dotenv-path", "F": "dotenv-filename", "V": "version",
    "c": "command", "d": "working-directory", "e": "edit",
    "f": "justfile", "g": "global-justfile", "h": "help", "l": "list",
    "n": "dry-run", "q": "quiet", "s": "show", "u": "unsorted",
    "v": "verbose",
}
FLAG_ALIASES = {"eval": "evaluate", "format": "fmt",
                "initialize": "init", "no-dependencies": "no-deps"}
# Settings the parser implements (internal/parser setting) and the runner
# applies. `fallback` is parsed and validated but has no effect yet: it needs
# the search of parent justfiles (Phase 3), so a case that sets it stays in its
# setting group. `default-script`, `script-interpreter` and `tempdir` act
# through script recipes (Task 11).
SUPPORTED_SETTINGS = {
    "allow-duplicate-recipes", "allow-duplicate-variables", "default-list",
    "default-script", "export", "guards", "ignore-comments", "indentation", "lazy", "lists",
    "minimum-version", "no-cd", "no-exit-message", "positional-arguments",
    "quiet", "script-interpreter", "shell", "tempdir", "unstable",
    "windows-powershell", "windows-shell", "working-directory",
}
DOTENV_SETTINGS = {"dotenv-command", "dotenv-filename", "dotenv-load",
                   "dotenv-override", "dotenv-path", "dotenv-required"}
# Environment variables upstream reads in place of a flag (src/arguments.rs
# `env = ...`); JUST_UNSTABLE is the unstable rule's, JUST_COMPLETE the
# completions rule's.
FLAG_VARIABLES = {
    "JUST_ALIAS_STYLE": "alias-style", "JUST_CEILING": "ceiling",
    "JUST_CHOOSER": "chooser", "JUST_COMPLETE_ALIASES": "complete-aliases",
    "JUST_GLOBAL_FALLBACK_JUSTFILE": "global-fallback-justfile",
    "JUST_INDENTATION": "indentation",
}
# Upstream's predefined constants (src/constants.rs).
CONSTANTS = set("""HEX HEXLOWER HEXUPPER PATH_SEP PATH_VAR_SEP CLEAR NORMAL
BOLD ITALIC UNDERLINE INVERT HIDE STRIKETHROUGH BLACK RED GREEN YELLOW BLUE
MAGENTA CYAN WHITE BG_BLACK BG_RED BG_GREEN BG_YELLOW BG_BLUE BG_MAGENTA
BG_CYAN BG_WHITE""".split())

# Upstream's unstable features (src/unstable_feature.rs). Logical
# operators, comparisons, and which() also require the lists setting.
UNSTABLE_MESSAGES = (
    ("cached recipes are currently unstable", "cached-recipes"),
    ("the `lists` setting is currently unstable", "lists"),
    ("user-defined functions are currently unstable",
     "user-defined-functions"),
    ("require `set lists`", "lists"),
    ("requires `set lists`", "lists"),
)

BUILTIN_FAMILIES = {
    "case": "capitalize kebabcase lowercamelcase lowercase shoutykebabcase "
            "shoutysnakecase snakecase titlecase uppercamelcase uppercase",
    "datetime": "datetime datetime_utc",
    "directories": "cache_directory config_directory "
                   "config_local_directory data_directory "
                   "data_local_directory executable_directory "
                   "home_directory runtime_directory",
    "env": "env env_var env_var_or_default",
    "error": "error",
    "external": "require shell which",
    "hash": "blake3 blake3_file sha256 sha256_file",
    "invocation": "invocation_directory invocation_directory_native "
                  "is_dependency just_executable just_pid just_version "
                  "justfile justfile_directory module_directory "
                  "module_file module_path num_jobs recipe_name "
                  "source_directory source_file",
    "path": "absolute_path canonicalize clean extension file_name "
            "file_stem join parent_directory path_exists read "
            "without_extension",
    "random": "choose uuid",
    "semver": "semver_matches",
    "string": "append bool encode_uri_component join_list len prepend "
              "quote replace replace_regex show split trim trim_end "
              "trim_end_match trim_end_matches trim_start "
              "trim_start_match trim_start_matches",
    "style": "style",
    "system": "arch num_cpus os os_family",
}
BUILTINS = {name: family for family, names in BUILTIN_FAMILIES.items()
            for name in names.split()}
# Builtins the evaluator implements (internal/eval/builtins table, entries
# with an implementation). A call of one of these does not put a case in a
# builtin group; the case is sorted by what else it uses.
IMPLEMENTED_BUILTINS = set("""
absolute_path append arch blake3 blake3_file canonicalize capitalize
choose clean datetime datetime_utc encode_uri_component env env_var
env_var_or_default error extension file_name file_stem
invocation_directory invocation_directory_native is_dependency join
just_executable just_pid just_version justfile justfile_directory
kebabcase len lowercamelcase lowercase module_directory module_file
module_path num_cpus num_jobs os os_family parent_directory path_exists
prepend quote read recipe_name replace replace_regex require
semver_matches sha256 sha256_file shell shoutykebabcase shoutysnakecase
snakecase source_directory source_file style titlecase trim trim_end
trim_end_match trim_end_matches trim_start trim_start_match
trim_start_matches uppercamelcase uppercase uuid which without_extension
""".split())

IDENT = r"[A-Za-z_][A-Za-z0-9_-]*"
CALL = re.compile(r"(?<![A-Za-z0-9_-])(" + IDENT + r")\s*\(")
UDF = re.compile(r"^" + IDENT + r"\s*\([^)]*\)\s*:=", re.M)
SETTING = re.compile(r"^set\s+(" + IDENT + r")", re.M)
# Attributes the parser implements (internal/parser attributeSpecs). A case
# that uses only these is sorted by what else it uses; one that uses any other
# is in the attribute group until the task that implements it.
SUPPORTED_ATTRIBUTES = {
    "android", "confirm", "continue", "default", "doc", "dragonfly", "env",
    "exit-message", "extension", "freebsd", "group", "linux", "macos",
    "metadata", "netbsd", "no-cd", "no-exit-message", "no-quiet", "openbsd",
    "parallel", "positional-arguments", "private", "script", "shell",
    "timestamp", "unix", "windows", "working-directory",
}
CACHE_ATTRIBUTE = re.compile(r"^\[[^\]]*\bcache\b", re.M)
MODULE = re.compile(r"^(?:\[[^\]]*\]\s*)?(?:mod|import)\??\s", re.M)
HEADER = re.compile(r"^@?(" + IDENT + r")([^:=]*?):(?![=])(.*)$")
INTERPOLATION = re.compile(r"\{\{(.*?)\}\}")
GRAMMAR_KEYWORDS = re.compile(r"^(?:unexport|alias)\s", re.M)
PARSE_ERRORS = ("error: expected ", "error: unknown start of token",
                "error: unknown setting", "error: unexpected closing delimiter",
                "error: mismatched closing delimiter",
                "error: unpaired carriage return", "error: unterminated ")


def justfile_texts(case):
    """The justfile sources of a case: every file that is not a .env file,
    an executable, or plain data. Pure."""
    texts = []
    for name, content in sorted(case["files"].items()):
        base = name.rsplit("/", 1)[-1]
        if base.startswith(".env") or base.endswith((".env", ".exe")):
            continue
        if base.endswith((".txt", ".md")) and base != "justfile":
            continue
        texts.append(content)
    return texts


def expression_text(source):
    """The parts of a justfile that hold expressions: every unindented line
    and the interpolations of indented (recipe body) lines, without
    comments. Pure."""
    parts = []
    for line in source.splitlines():
        if line[:1] in (" ", "\t"):
            parts.extend(INTERPOLATION.findall(line))
        elif not line.startswith("#"):
            parts.append(line)
    return "\n".join(parts)


def flag_name(arg):
    """The long flag names one command-line word sets: ["dump"] for
    "--dump", ["quiet", "dry-run"] for "-qn". Pure."""
    if arg.startswith("--"):
        name = arg[2:].split("=", 1)[0]
        return [FLAG_ALIASES.get(name, name)]
    return [SHORT_FLAGS.get(letter, letter) for letter in arg[1:]]


def unsupported_flag(args):
    """The first flag before the first positional word that the command
    does not implement, "set" for a NAME=VALUE override, or None. Pure."""
    i = 0
    while i < len(args):
        arg = args[i]
        if arg == "--" or not arg.startswith("-") or arg == "-":
            if re.match(IDENT + "=", arg):
                return "set"
            return None
        names = flag_name(arg)
        for name in names:
            if name not in SUPPORTED_FLAGS:
                return name
        if names[-1] in VALUE_FLAGS and "=" not in arg:
            i += 1
        i += 1
    return None


def unstable_family(case, sources, expected):
    """The unstable feature the case uses or expects to be gated, or None.
    Pure."""
    for message, family in UNSTABLE_MESSAGES:
        if message in expected:
            return family, "expects the unstable gate: " + message
    for source in sources:
        if CACHE_ATTRIBUTE.search(source):
            return "cached-recipes", "uses the [cache] attribute"
        if re.search(r"^set\s+lists\b", source, re.M):
            return "lists", "uses `set lists`"
        if UDF.search(source):
            return "user-defined-functions", "defines a function"
    return None


def cli_group(case):
    """The cli group for a flag, JUST_* variable, or override. Pure."""
    flag = unsupported_flag(case["args"])
    if flag:
        return "cli:" + flag, "passes --" + flag
    for key in sorted(case["env"]):
        if key in FLAG_VARIABLES:
            return "cli:" + FLAG_VARIABLES[key], "sets " + key
    words = positionals(case["args"])
    if words and "=" in words[0]:
        return "cli:set", "overrides a variable with NAME=VALUE"
    if words and "/" in words[0]:
        return "cli:search-directory", "names a recipe by a directory path"
    if stdin_justfile(case["args"]):
        return "cli:justfile", "reads the justfile from standard input"
    if words and listing(case["args"]):
        return "cli:list", "lists a submodule"
    return None


NESTED = re.compile(r"just_executable\(\)\s*\}\}'?\s*(.*)$")


def nested_cli_group(sources):
    """The cli group for the command line that a line of a recipe body hands
    to the program just_executable() names, or None. Pure."""
    for source in sources:
        for line in source.splitlines():
            found = NESTED.search(line)
            args = [w.strip("'\"") for w in found.group(1).split()] if found else []
            flag = unsupported_flag(args)
            if flag:
                return "cli:" + flag, "runs just with --" + flag
            words = positionals(args)
            if words and "/" in words[0]:
                return ("cli:search-directory",
                        "runs just with a recipe named by a directory path")
    return None


def positionals(args):
    """The words after the flags, as unsupported_flag scans them. Pure."""
    i = 0
    while i < len(args):
        arg = args[i]
        if arg == "--":
            return args[i + 1:]
        if not arg.startswith("-") or arg == "-":
            return args[i:]
        if flag_name(arg)[-1] in VALUE_FLAGS and "=" not in arg:
            i += 1
        i += 1
    return []


def stdin_justfile(args):
    """Whether the justfile flag is given "-" (standard input). Pure."""
    for i, arg in enumerate(args[:-1]):
        if arg in ("-f", "--justfile") and args[i + 1] == "-":
            return True
    return "--justfile=-" in args


def listing(args):
    """Whether --list or -l is among the flags. Pure."""
    return any(flag_name(a)[-1] == "list" for a in args
               if a.startswith("-") and a != "-")


def setting_group(sources):
    """The group for the first unimplemented setting, or None. Pure."""
    for source in sources:
        for name in SETTING.findall(source):
            if name in DOTENV_SETTINGS:
                return "dotenv", "sets " + name
            if name not in SUPPORTED_SETTINGS:
                return "setting:" + name, "sets " + name
    return None


def code_lines(source):
    """The lines of source, with each line of code that ends in a backslash
    joined to the line it continues, as the lexer joins them. Pure."""
    lines = []
    for line in source.splitlines():
        if lines and lines[-1].endswith("\\") \
                and lines[-1][:1] not in (" ", "\t"):
            lines[-1] = lines[-1][:-1] + line.lstrip()
        else:
            lines.append(line)
    return lines


def has_shebang(source):
    """Whether a recipe body starts with #!. Pure."""
    lines = code_lines(source)
    for i, line in enumerate(lines[1:], 1):
        if line.lstrip().startswith("#!") and line[:1] in (" ", "\t") \
                and lines[i - 1][:1] not in (" ", "\t", ""):
            return True
    return False


def builtin_group(expressions):
    """The family of the first builtin called that is not implemented yet, or
    None. Pure."""
    for name in CALL.findall(expressions):
        lookup = re.sub(r"_dir(_native)?$", r"_directory\1", name)
        if lookup in BUILTINS and lookup not in IMPLEMENTED_BUILTINS:
            return "builtin:" + BUILTINS[lookup], "calls " + name + "()"
    return None


def attribute_names(source):
    """The names of the attributes on the lines of source that start with
    `[`, in order. Pure."""
    names = []
    for line in source.splitlines():
        if not line.startswith("["):
            continue
        text = re.sub(r"'[^']*'|\"(?:[^\"\\]|\\.)*\"", "''", line)
        depth, expect = 0, True
        for match in re.finditer(r"[\[\](),:]|" + IDENT, text):
            token = match.group(0)
            if token in "([":
                depth += 1
            elif token in ")]":
                depth -= 1
            elif token == "," and depth == 1:
                expect = True
            elif token == ":" and depth == 1:
                expect = False
            elif expect and depth == 1 and token[0].isalpha():
                names.append(token)
                expect = False
    return names


def unsupported_attribute(source):
    """The first attribute in source that the parser does not implement, or
    None. Pure."""
    for name in attribute_names(source):
        if name not in SUPPORTED_ATTRIBUTES:
            return name
    return None


def syntax_group(sources):
    """The group for a syntax feature the sources use, or None. Pure."""
    exprs = "\n".join(expression_text(s) for s in sources)
    for source in sources:
        name = unsupported_attribute(source)
        if name:
            return "attribute", "uses the [" + name + "] attribute"
    if any(has_shebang(s) for s in sources):
        return "shebang", "has a shebang recipe"
    if any("script" in attribute_names(s) for s in sources):
        return "shebang", "has a [script] recipe"
    builtin = builtin_group(exprs)
    if builtin:
        return builtin
    names = set(re.findall(r"[A-Za-z_][A-Za-z0-9_]*", exprs)) & CONSTANTS
    names |= set(re.findall(r"=\s*([A-Z_]+)", exprs)) & CONSTANTS
    if names:
        return "builtin:constants", "uses the constant " + min(names)
    return None


def grammar_reason(sources, actual, expected):
    """Why a case falls in the grammar group, or None. Pure."""
    for source in sources:
        keyword = GRAMMAR_KEYWORDS.search(source)
        if keyword:
            return "uses `" + keyword.group(0).strip() + "`"
    if any(marker in actual for marker in PARSE_ERRORS):
        return "just-go reports a parse error: " + first_error(actual)
    if "——▶" in expected:
        return "upstream reports a compile error just-go words differently"
    return None


def first_error(actual):
    """The first line of the error just-go printed, from the harness's
    "got:" text, cut to 100 characters. Pure."""
    match = re.search(r'got: "error: (.*?)(?:\\n|")', actual)
    return match.group(1)[:100] if match else ""


def classify(case, actual):
    """Assign one failing case to (group, reason). actual is the failure
    text the harness printed for it. Pure."""
    sources = justfile_texts(case)
    expected = (case.get("stderr") or case.get("stderr_regex") or "") + \
        (case.get("stdout") or case.get("stdout_regex") or "")
    family = unstable_family(case, sources, expected)
    if family:
        return "unstable:" + family[0], family[1]
    if "--completions" in case["args"] or "JUST_COMPLETE" in case["env"]:
        return "completions", "generates or runs completions"
    for rule in (lambda: cli_group(case), lambda: nested_cli_group(sources),
                 lambda: module_group(case, sources),
                 lambda: dotenv_group(case), lambda: setting_group(sources),
                 lambda: syntax_group(sources)):
        found = rule()
        if found:
            return found
    reason = grammar_reason(sources, actual, expected)
    if reason:
        return "grammar", reason
    return residual(case, actual)


def module_group(case, sources):
    """The module group when a source uses mod or import or the command
    line names a module path. Pure."""
    if any(MODULE.search(s) for s in sources):
        return "module", "uses mod or import"
    if any("::" in word for word in positionals(case["args"])):
        return "module", "names a module path on the command line"
    return None


def dotenv_group(case):
    """The dotenv group when the case has a .env file. Pure."""
    names = [n.rsplit("/", 1)[-1] for n in case["files"]]
    if any(n.startswith(".env") or n.endswith(".env") for n in names):
        return "dotenv", "has a .env file"
    return None


def residual(case, actual):
    """Group a case that uses only the implemented subset. Pure."""
    if re.search(r"——▶ [^\n\\]*:\d+:0\b", actual):
        return "core-defect", "renders a snippet at column 0"
    if "——▶" in actual:
        return "core-defect", "error location or snippet differs"
    if "status: got" in actual:
        return "other", "exit status differs on the implemented subset"
    return "other", "output differs on the implemented subset"


def parse_events(lines):
    """Map each TestConformance subtest name to its action ("pass", "fail",
    or "skip") and its output text. Pure."""
    results = {}
    for line in lines:
        if not line.startswith("{"):
            continue
        event = json.loads(line)
        test = event.get("Test", "")
        if not test.startswith(SUBTEST):
            continue
        entry = results.setdefault(test[len(SUBTEST):], ["", []])
        if event["Action"] in ("pass", "fail", "skip"):
            entry[0] = event["Action"]
        elif event["Action"] == "output":
            entry[1].append(event["Output"])
    return {k: (v[0], "".join(v[1])) for k, v in results.items()}


def triage(cases, results):
    """The groups document for the corpus and the run's results. Pure."""
    counts = {"pass": 0, "fail": 0, "skip": 0}
    groups = {}
    for case in cases:
        action, output = results.get(case["id"], ("", ""))
        if action not in counts:
            raise ValueError("case %s has no result" % case["id"])
        counts[action] += 1
        if action == "fail":
            group, reason = classify(case, output)
            groups.setdefault(group, {})[case["id"]] = reason
    counts["total"] = len(cases)
    return {"counts": counts,
            "group_sizes": {g: len(ids) for g, ids in sorted(groups.items())},
            "groups": {g: dict(sorted(ids.items()))
                       for g, ids in sorted(groups.items())}}


def run_suite():
    """Build bin/just-go and run the conformance suite. Impure."""
    subprocess.run(["make", "-s", "build"], cwd=ROOT, check=True)
    env = dict(os.environ, JUST_GO_BIN="bin/just-go")
    proc = subprocess.run(
        ["go", "test", "-json", "-count=1", "-run", "^TestConformance$",
         "./conformance"],
        cwd=ROOT, env=env, capture_output=True, text=True)
    return proc.stdout.splitlines()


def main(argv):
    """Run or read the suite, triage it, and write the groups. Impure."""
    parser = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    parser.add_argument("--input", help="go test -json output to read")
    parser.add_argument("--output", default=GROUPS)
    opts = parser.parse_args(argv)
    if opts.input:
        with open(opts.input, encoding="utf-8") as f:
            lines = f.read().splitlines()
    else:
        lines = run_suite()
    with open(CORPUS, encoding="utf-8") as f:
        cases = [json.loads(line) for line in f]
    doc = triage(cases, parse_events(lines))
    os.makedirs(os.path.dirname(opts.output), exist_ok=True)
    with open(opts.output, "w", encoding="utf-8") as f:
        json.dump(doc, f, indent=2, ensure_ascii=False)
        f.write("\n")
    for group, size in doc["group_sizes"].items():
        print("%5d  %s" % (size, group))
    c = doc["counts"]
    print("%d pass, %d fail, %d skip (of %d)" %
          (c["pass"], c["fail"], c["skip"], c["total"]))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
