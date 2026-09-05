"""Test that the differential gate detects mismatches instead of masking them."""

from pathlib import Path
from contextlib import redirect_stderr, redirect_stdout
import io
import json
import subprocess
import sys
import tempfile
import unittest
import warnings
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import differential_semantics as gate
from semantic_cases import Case, generate_cases


def accepted(typ):
    return {"ok": True, "inferred_types": [typ], "diagnostics": []}


class DifferentialTests(unittest.TestCase):
    def test_exact_types_are_not_python_subtyping(self):
        case = Case("integer", "test", "1", "int")
        self.assertEqual(gate.compare_case(case, accepted("int"), {"value": gate.encode_value(1)}), [])
        self.assertTrue(gate.compare_case(case, accepted("bool"), {"value": gate.encode_value(1)}))
        self.assertTrue(gate.compare_case(case, accepted("int"), {"value": gate.encode_value(True)}))
        self.assertFalse(gate.matches_type(gate.encode_value((1, True)), "tuple[int, ...]"))
        self.assertTrue(gate.matches_type(gate.encode_value(((), (1,))), "tuple[tuple[int, ...], ...]"))
        self.assertTrue(gate.matches_type(gate.encode_value(None), "int | None"))

    def test_static_rejection_is_independent_of_python_acceptance(self):
        case = Case("bool_add", "test", "True + True", None)
        rejected = {"ok": False, "inferred_types": [], "diagnostics": [{"code": "PP209"}]}
        runtime = gate.runtime_case(case)
        self.assertEqual(runtime["value"], gate.encode_value(2))
        self.assertEqual(gate.compare_case(case, rejected, runtime), [])
        self.assertTrue(gate.compare_case(case, accepted("int"), runtime))
        rejected["diagnostics"][0]["code"] = "PP099"
        self.assertIn("internal verifier failure", gate.compare_case(case, rejected, runtime))

    def test_declared_domain_failures_are_not_static_type_failures(self):
        case = Case("zero", "test", "1 / 0", "float", exception="ZeroDivisionError")
        self.assertEqual(gate.compare_case(case, accepted("float"), gate.runtime_case(case)), [])
        self.assertTrue(gate.compare_case(case, accepted("float"), {"value": gate.encode_value(1.0)}))
        self.assertTrue(gate.compare_case(case, accepted("float"), {"exception": "ValueError"}))
        ordinary = Case("ordinary", "test", "1 / 1", "float")
        self.assertTrue(gate.compare_case(ordinary, accepted("float"), {"exception": "ZeroDivisionError"}))

    def test_curated_values_preserve_negative_zero_bytes_and_nan(self):
        self.assertNotEqual(gate.encode_value(0.0), gate.encode_value(-0.0))
        self.assertEqual(gate.encode_value(float("nan")), {"type": "float", "value": "nan"})
        self.assertEqual(gate.encode_value(b"\x00\xff"), {"type": "bytes", "value": "00ff"})
        case = Case("signed_zero", "test", "-0.0", "float", expected_value=-0.0, check_value=True)
        self.assertEqual(gate.compare_case(case, accepted("float"), gate.runtime_case(case)), [])
        self.assertTrue(gate.compare_case(case, accepted("float"), {"value": gate.encode_value(0.0)}))

    def test_probe_source_has_no_expected_result_annotation_and_uses_byte_offsets(self):
        case = Case("unicode", "test", "len('é')", "int")
        probe = gate.source_case(case)
        self.assertIn("def probe() -> None:", probe["source"])
        self.assertNotIn("result:", probe["source"])
        self.assertEqual(probe["source"].encode()[probe["start"]:probe["end"]], b"result")
        self.assertEqual(gate.source_case(Case("different", "test", "len('é')", "bool"))["source"], probe["source"])

    def test_range_probe_observes_element_type_and_bounds_execution(self):
        case = Case("range", "test", "range(-2, 3, 2)", "int", mode="range")
        self.assertIn("for result in range(-2, 3, 2):", gate.source_case(case)["source"])
        self.assertEqual(gate.runtime_case(case)["value"], gate.encode_value((-2, 0, 2)))
        self.assertEqual(gate.compare_case(case, accepted("int"), gate.runtime_case(case)), [])
        oversized = Case("oversized", "test", "range(4097)", "int", exception="ValueError", mode="range")
        with self.assertRaisesRegex(ValueError, "exceeded development gate bound"):
            gate.runtime_case(oversized)

    def test_corpus_is_deterministic_bounded_and_covers_sealed_families(self):
        first = generate_cases(seed=42, samples=8)
        self.assertEqual(first, generate_cases(seed=42, samples=8))
        self.assertNotEqual(first, generate_cases(seed=43, samples=8))
        self.assertLessEqual(len(first), gate.MAX_CASES)
        self.assertEqual(len(first), len({case.name for case in first}))
        expressions = "\n".join(case.expression for case in first)
        for name in gate.BUILTINS:
            self.assertIn(name + "(", expressions)
        self.assertTrue(any(case.expected_type is None for case in first))
        self.assertTrue(any(case.exception for case in first))

    def test_named_unicode_cases_have_independent_literal_value_expectations(self):
        cases = [case for case in generate_cases(samples=0)
                 if case.name.startswith("literal/named/")]
        self.assertGreaterEqual(len(cases), 30)
        by_name = {case.name.removeprefix("literal/named/"): case for case in cases}
        for name in ("canonical", "control_alias", "correction_alias", "hangul_last",
                     "cjk_lowercase", "tangut", "khitan", "nushu", "form/adjacent",
                     "form/raw_unknown", "form/bytes_unknown", "form/formatted_braces",
                     "form/raw_formatted_braces", "form/raw_empty", "form/raw_bytes_empty",
                     "form/bytes_consecutive", "form/bytes_multiline",
                     "form/formatted_bytes_expression"):
            self.assertIn(name, by_name)
        for case in cases:
            with self.subTest(case=case.name):
                self.assertTrue(case.check_value)
                self.assertIn(case.expected_type, ("str", "bytes"))
                # Python 3.14 warns about ignored escapes in non-raw bytes;
                # acceptance and the literal backslash are intentional here.
                with warnings.catch_warnings():
                    warnings.simplefilter("ignore", SyntaxWarning)
                    runtime = gate.runtime_case(case)
                self.assertEqual(runtime["value"], gate.encode_value(case.expected_value))
                self.assertEqual(gate.compare_case(case, accepted(case.expected_type), runtime), [])
        # Malformed source must fail before runtime execution, including for a
        # case whose PurePy oracle expects rejection. Syntax errors are covered
        # by the AST-only syntax gate, never treated as runtime domain failures.
        malformed = Case("bad_escape", "test", r'''"\N{UNKNOWN}"''', None)
        with self.assertRaises(SyntaxError):
            gate.runtime_case(malformed)

    def test_none_identity_has_explicit_values_and_rejects_non_none_pairs(self):
        cases = {case.name: case for case in generate_cases(samples=0)
                 if case.name.startswith("identity/")}
        self.assertEqual(len(cases), 3400)
        accepted_count = 0
        for case in cases.values():
            if case.expected_type is None:
                continue
            accepted_count += 1
            with self.subTest(case=case.name):
                self.assertEqual(case.expected_type, "bool")
                self.assertTrue(case.check_value)
                self.assertIs(type(case.expected_value), bool)
                runtime = gate.runtime_case(case)
                self.assertEqual(runtime["value"], gate.encode_value(case.expected_value))
                self.assertEqual(gate.compare_case(case, accepted("bool"), runtime), [])
                self.assertTrue(gate.compare_case(case, accepted("bool"),
                                                 {"value": gate.encode_value(not case.expected_value)}))
        self.assertEqual(accepted_count, 342)

        rejected = {"ok": False, "inferred_types": [], "diagnostics": [{"code": "PP209"}]}
        for name in (
            "identity/is/int/int",
            "identity/is_not/str/bytes",
            "identity/is/optional_int_none/optional_int_none",
            "identity/is_not/optional_tuple_int_none/tuple_int",
            # A false first pair cannot hide an invalid second pair.
            "identity/chain/is/is/none/int/int",
            "identity/chain/is_not/is/int/int/none",
            "identity/chain/is/is/none/optional_int_none/optional_int_none",
        ):
            with self.subTest(case=name):
                case = cases[name]
                self.assertIsNone(case.expected_type)
                runtime = gate.runtime_case(case)
                self.assertEqual(gate.compare_case(case, rejected, runtime), [])
                self.assertTrue(gate.compare_case(case, accepted("bool"), runtime))

    def test_runtime_corpus_cannot_import_or_call_host_objects(self):
        for expression in ("__import__('os')", "open('/tmp/sentinel', 'w')", "(1).__class__", "[x for x in (1,)]", "(lambda: 1)()"):
            with self.subTest(expression=expression), self.assertRaises(ValueError):
                gate.compile_expression(expression, {})

    def test_missing_reordered_or_malformed_results_fail(self):
        cases = [Case("one", "test", "1", "int"), Case("two", "test", "2", "int")]
        for payload in ({}, {"results": []}, {"results": [{"name": "two"}, {"name": "one"}]}, {"results": [{"name": "one"}, {"name": "one"}]}):
            with self.subTest(payload=payload), self.assertRaises(ValueError):
                gate.read_results(payload, cases, "fake")
        self.assertTrue(gate.compare_case(cases[0], {"ok": "true"}, {}))
        rejected = {"ok": False, "diagnostics": [{"code": "PP209"}], "inferred_types": []}
        near_miss = Case("reject", "test", "True + True", None)
        for runtime in ({}, {"exception": "ValueError", "value": gate.encode_value(1)}, {"value": {"type": "int", "value": False}}, {"exception": None}):
            with self.subTest(runtime=runtime):
                self.assertTrue(gate.compare_case(near_miss, rejected, runtime))
        with patch.object(gate.subprocess, "run", return_value=subprocess.CompletedProcess(["probe"], 2, "", "bad input")):
            with self.assertRaisesRegex(ValueError, "bad input"):
                gate.run_process(["probe"], {})

    def test_live_primitive_and_near_miss_cases(self):
        probe = gate.ROOT / "bin/purepy-semantic-probe"
        if not probe.exists():
            self.skipTest("run make differential-test to build the development helper")
        cases = gate.load_regressions() + [
            Case("zero", "test", "1 / 0", "float", exception="ZeroDivisionError"),
            Case("range", "test", "range(-2, 3, 2)", "int", mode="range"),
        ]
        failures, versions = gate.run_gate(cases, probe)
        self.assertEqual(failures, [])
        self.assertTrue(versions["python_version"].startswith("3.14."))

    def test_reviewed_regression_values_round_trip_without_evaluation(self):
        for value in (None, True, -123, -0.0, float("inf"), "λ", b"\x00\xff", ((), (1, 2))):
            encoded = gate.encode_value(value)
            self.assertEqual(gate.encode_value(gate.decode_value(encoded)), encoded)
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "regressions.json"
            path.write_text('{"schema": 999, "cases": []}')
            with self.assertRaisesRegex(ValueError, "schema"):
                gate.load_regressions(path)

    def test_mismatch_artifact_retains_source_expectations_and_reproduction_seed(self):
        failure = {"name": "broken", "errors": ["wrong type"], "probe": {"source": "example"}}
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / "failures.json"
            with patch.object(gate, "run_gate", return_value=([failure], {"python_version": "3.14.7", "verifier_version": "test"})):
                with redirect_stdout(io.StringIO()), redirect_stderr(io.StringIO()):
                    self.assertEqual(gate.main(["--seed", "17", "--samples", "0", "--failures", str(output)]), 1)
            saved = json.loads(output.read_text())
            self.assertEqual(saved["seed"], 17)
            self.assertEqual(saved["samples"], 0)
            self.assertEqual(saved["failures"], [failure])


if __name__ == "__main__":
    unittest.main()
