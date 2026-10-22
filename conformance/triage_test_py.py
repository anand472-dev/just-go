"""Unit tests for scripts/triage.py, the classifier behind
conformance/triage/groups.json.

Run with `make triage-test` (python3 -m unittest conformance.triage_test_py
from the repository root). It is not part of `make check`.
"""

import os
import sys
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)),
                                os.pardir, "scripts"))

import triage  # noqa: E402


def case(justfile="", args=(), env=None, files=None, **expected):
    """A corpus case with the fields the classifier reads."""
    all_files = {"justfile": justfile}
    all_files.update(files or {})
    return dict({"id": "t#1", "args": list(args), "env": env or {},
                 "files": all_files}, **expected)


def group(*args, **kwargs):
    """The group name classify assigns to a case."""
    actual = kwargs.pop("actual", "")
    return triage.classify(case(*args, **kwargs), actual)[0]


class UnsupportedFlagTest(unittest.TestCase):
    def test_supported_flags_are_none(self):
        self.assertIsNone(triage.unsupported_flag(["--list", "-q"]))

    def test_first_unsupported_long_flag(self):
        self.assertEqual(triage.unsupported_flag(["--dump"]), "dump")

    def test_short_flag_maps_to_long_name(self):
        self.assertEqual(triage.unsupported_flag(["-n"]), "dry-run")

    def test_combined_short_flags_stop_at_unsupported(self):
        self.assertEqual(triage.unsupported_flag(["-qn"]), "dry-run")

    def test_alias_maps_to_canonical_name(self):
        self.assertEqual(triage.unsupported_flag(["--eval"]), "evaluate")

    def test_value_flag_consumes_next_word(self):
        self.assertIsNone(triage.unsupported_flag(["--shell", "--dump"]))

    def test_value_flag_with_equals_does_not_consume(self):
        self.assertEqual(
            triage.unsupported_flag(["--shell=sh", "--dump"]), "dump")

    def test_override_is_set(self):
        self.assertEqual(triage.unsupported_flag(["a=b"]), "set")

    def test_flags_after_first_positional_are_ignored(self):
        self.assertIsNone(triage.unsupported_flag(["recipe", "--dump"]))

    def test_double_dash_ends_flags(self):
        self.assertIsNone(triage.unsupported_flag(["--", "--dump"]))

    def test_lone_dash_is_positional(self):
        self.assertIsNone(triage.unsupported_flag(["-", "--dump"]))


class PositionalsTest(unittest.TestCase):
    def test_no_arguments(self):
        self.assertEqual(triage.positionals([]), [])

    def test_only_flags(self):
        self.assertEqual(triage.positionals(["--list", "-q"]), [])

    def test_words_after_flags(self):
        self.assertEqual(triage.positionals(["-q", "a", "-b"]), ["a", "-b"])

    def test_value_flag_skips_its_value(self):
        self.assertEqual(
            triage.positionals(["--justfile", "x", "a"]), ["a"])

    def test_value_flag_with_equals_keeps_next_word(self):
        self.assertEqual(triage.positionals(["--justfile=x", "a"]), ["a"])

    def test_double_dash_returns_the_rest(self):
        self.assertEqual(triage.positionals(["--", "-a", "b"]), ["-a", "b"])

    def test_lone_dash_is_a_word(self):
        self.assertEqual(triage.positionals(["-", "a"]), ["-", "a"])

    def test_trailing_value_flag(self):
        self.assertEqual(triage.positionals(["--shell"]), [])


class HasShebangTest(unittest.TestCase):
    def test_shebang_recipe(self):
        self.assertTrue(triage.has_shebang("a:\n  #!/bin/sh\n  echo\n"))

    def test_tab_indented(self):
        self.assertTrue(triage.has_shebang("a:\n\t#!/bin/sh\n"))

    def test_shebang_on_first_line_is_not_a_recipe_body(self):
        self.assertFalse(triage.has_shebang("#!/usr/bin/env just\na:\n  x\n"))

    def test_unindented_shebang_line(self):
        self.assertFalse(triage.has_shebang("a:\n#!/bin/sh\n"))

    def test_shebang_after_first_body_line(self):
        self.assertFalse(triage.has_shebang("a:\n  echo\n  #!/bin/sh\n"))

    def test_comment_is_not_a_shebang(self):
        self.assertFalse(triage.has_shebang("a:\n  # !/bin/sh\n"))

    def test_continued_header_counts_as_one_line(self):
        self.assertTrue(
            triage.has_shebang("a b \\\n  c:\n  #!/bin/sh\n"))

    def test_empty_source(self):
        self.assertFalse(triage.has_shebang(""))


class ExpressionTextTest(unittest.TestCase):
    """scripts/triage.py has no has_dependency_args function. Dependency
    arguments reach the classifier as the expression text of an unindented
    recipe header line, which these tests pin instead."""

    def test_dependency_arguments_are_expression_text(self):
        text = triage.expression_text("a: (b os())\n  echo\n")
        self.assertIn("os()", text)
        self.assertEqual(triage.builtin_group(text)[0], "builtin:system")

    def test_body_text_is_not_expression_text(self):
        self.assertNotIn("os()", triage.expression_text("a:\n  os()\n"))

    def test_interpolation_is_expression_text(self):
        self.assertEqual(
            triage.expression_text("a:\n  {{ x }} y\n").split("\n"),
            ["a:", " x "])


class ClassifyPrecedenceTest(unittest.TestCase):
    def test_unstable_beats_cli(self):
        self.assertEqual(
            group("set lists\n", args=["--dump"]), "unstable:lists")

    def test_unstable_gate_message(self):
        self.assertEqual(
            group("a:\n", stderr="user-defined functions are currently "
                  "unstable"), "unstable:user-defined-functions")

    def test_completions_beats_cli(self):
        self.assertEqual(
            group("", args=["--completions", "bash"]), "completions")

    def test_cli_beats_module(self):
        self.assertEqual(
            group("mod x\n", args=["--dump"]), "cli:dump")

    def test_env_variable_flag(self):
        self.assertEqual(
            group("", env={"JUST_CEILING": "x"}), "cli:ceiling")

    def test_override(self):
        self.assertEqual(group("", args=["x=1"]), "cli:set")

    def test_directory_path(self):
        self.assertEqual(group("", args=["sub/a"]), "cli:search-directory")

    def test_stdin_justfile(self):
        self.assertEqual(
            group("", args=["-f", "-"]), "cli:justfile")

    def test_list_of_submodule(self):
        self.assertEqual(
            group("", args=["--list", "sub"]), "cli:list")

    def test_module_beats_dotenv(self):
        self.assertEqual(
            group("import 'x'\n", files={".env": "A=1"}), "module")

    def test_module_path_on_command_line(self):
        self.assertEqual(group("a:\n", args=["m::a"]), "module")

    def test_dotenv_beats_setting(self):
        self.assertEqual(
            group("set nope\n", files={".env": "A=1"}), "dotenv")

    def test_dotenv_setting(self):
        self.assertEqual(group("set dotenv-load\n"), "dotenv")

    def test_unknown_setting(self):
        self.assertEqual(group("set fancy := true\n"), "setting:fancy")

    def test_supported_setting_falls_through(self):
        self.assertEqual(group("set quiet\n", actual="status: got 1"),
                         "other")

    def test_setting_beats_attribute(self):
        self.assertEqual(
            group("set fancy\n[arg('x')]\na:\n"), "setting:fancy")

    def test_attribute_beats_shebang(self):
        self.assertEqual(
            group("[arg('x')]\na:\n  #!/bin/sh\n"), "attribute")

    def test_shebang_beats_builtin(self):
        self.assertEqual(
            group("a:\n  #!/bin/sh\n  {{os()}}\n"), "shebang")

    def test_script_attribute_is_shebang(self):
        self.assertEqual(group("[script]\na:\n  x\n"), "shebang")

    def test_builtin_family(self):
        self.assertEqual(group("x := uuid()\n"), "builtin:random")

    def test_implemented_builtin_is_not_a_builtin_group(self):
        self.assertEqual(group("x := uppercase('a')\n", actual="status: got 1"),
                         "other")

    def test_unimplemented_builtin_after_an_implemented_one(self):
        self.assertEqual(group("x := uppercase(uuid())\n"),
                         "builtin:random")

    def test_dir_alias_maps_to_directory(self):
        self.assertEqual(group("x := home_dir()\n"),
                         "builtin:directories")

    def test_constant(self):
        self.assertEqual(group("x := HEX\n"), "builtin:constants")

    def test_grammar_keyword(self):
        self.assertEqual(group("alias b := a\n"), "grammar")

    def test_grammar_parse_error(self):
        self.assertEqual(
            group("a:\n", actual="error: expected x"), "grammar")

    def test_grammar_compile_error(self):
        self.assertEqual(group("a:\n", stderr="——▶ justfile:1:1"),
                         "grammar")

    def test_core_defect_column_zero(self):
        self.assertEqual(
            group("a:\n", actual="——▶ justfile:1:0\n"), "core-defect")

    def test_core_defect_location(self):
        self.assertEqual(
            group("a:\n", actual="——▶ justfile:1:2\n"), "core-defect")

    def test_other_status(self):
        self.assertEqual(
            group("a:\n", actual="status: got 1"), "other")

    def test_other_output(self):
        self.assertEqual(group("a:\n", actual="stdout differs"), "other")


class SourceHelpersTest(unittest.TestCase):
    def test_justfile_texts_skips_env_exe_and_data(self):
        c = case("j", files={".env": "e", "x.exe": "b", "d.txt": "t",
                             "sub/justfile": "s"})
        self.assertEqual(triage.justfile_texts(c), ["j", "s"])

    def test_attribute_names(self):
        self.assertEqual(
            triage.attribute_names("[private, doc('x]')]\n[group: 'g']\n"),
            ["private", "doc", "group"])

    def test_unsupported_attribute(self):
        self.assertEqual(triage.unsupported_attribute("[arg('x')]\n"), "arg")
        self.assertIsNone(triage.unsupported_attribute("[private]\n"))

    def test_triage_counts_and_groups(self):
        cases = [dict(case("set lists\n"), id="a#1"),
                 dict(case(""), id="b#1"), dict(case(""), id="c#1")]
        results = {"a#1": ("fail", ""), "b#1": ("pass", ""),
                   "c#1": ("skip", "")}
        doc = triage.triage(cases, results)
        self.assertEqual(doc["counts"],
                         {"pass": 1, "fail": 1, "skip": 1, "total": 3})
        self.assertEqual(doc["group_sizes"], {"unstable:lists": 1})

    def test_triage_rejects_a_missing_result(self):
        with self.assertRaises(ValueError):
            triage.triage([case("")], {})


if __name__ == "__main__":
    unittest.main()
