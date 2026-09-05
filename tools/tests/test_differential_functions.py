"""Exercise whole-function verdict, runtime, protocol and replay failures."""

from contextlib import redirect_stderr, redirect_stdout
from copy import deepcopy
from dataclasses import replace
import io
import json
from pathlib import Path
import platform
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import differential_functions as gate
from function_cases import FunctionCase, Invocation, generate_cases
from function_runtime import runtime_case, worker_case


def identity_case(name="identity", annotation="int", values=(3,)):
    return FunctionCase(
        name, "test", f"def probe(x: {annotation}) -> {annotation}:\n    return x\n",
        (("x", annotation),), annotation,
        tuple(Invocation((value,), value, True) for value in values))


def verdict(case, *codes):
    return {"name": case.name, "ok": not codes, "inferred_types": [],
            "diagnostics": [{"code": code} for code in codes]}


def outcomes(case, *values):
    return {"name": case.name, "outcomes": [
        {"index": index, "value": gate.encode_value(value)}
        for index, value in enumerate(values)]}


def envelopes(cases):
    return (
        {"schema": 1, "verifier_version": "test-verifier", "results": [
            verdict(case, *case.expected_codes) for case in cases]},
        {"schema": 1, "python_version": "3.14.7", "results": [
            runtime_case(case) for case in cases]},
    )


class DifferentialFunctionTests(unittest.TestCase):
    def test_exact_return_types_do_not_use_python_subtyping(self):
        tests = (
            ("bool", True, 1),
            ("int", 1, True),
            ("int | None", None, False),
            ("int | None", 2, (2,)),
            ("tuple[int | None, ...]", (1, None), (True, None)),
            ("tuple[tuple[int, ...], ...]", ((), (1, 2)), ((), (True,))),
            ("tuple[tuple[int, ...], ...] | None", None, (None,)),
            ("tuple[tuple[int, ...] | None, ...]", (None, (1,)), (None, (False,))),
        )
        for annotation, correct, wrong in tests:
            with self.subTest(annotation=annotation, correct=correct):
                # Disable value equality here so only the declared type catches
                # Python's bool/int relation and recursively nested mismatches.
                case = identity_case(annotation=annotation, values=(correct,))
                case = replace(case, invocations=(Invocation((correct,)),))
                self.assertEqual(gate.compare_case(case, verdict(case), outcomes(case, correct)), [])
                errors = gate.compare_case(case, verdict(case), outcomes(case, wrong))
                self.assertTrue(any("exact declared type" in error for error in errors), errors)

    def test_reviewed_loop_witness_detects_accidental_acceptance(self):
        corpus = {case.name: case for case in generate_cases(samples=0)}
        for exit_kind in ("break", "continue"):
            case = corpus[f"regression/for_target_{exit_kind}_optional"]
            runtime = runtime_case(case)
            self.assertIn(gate.encode_value(None), [outcome.get("value") for outcome in runtime["outcomes"]])
            self.assertEqual(gate.compare_case(case, verdict(case, "PP205"), runtime), [])
            errors = gate.compare_case(case, verdict(case), runtime)
            self.assertIn("expected PurePy rejection", errors)
            self.assertTrue(any("exact declared type int" in error for error in errors), errors)

    def test_rejection_requires_expected_code_and_never_accepts_internal_errors(self):
        case = replace(identity_case(), expected_codes=("PP205", "PP206"))
        runtime = outcomes(case, 3)
        for code in case.expected_codes:
            self.assertEqual(gate.compare_case(case, verdict(case, code), runtime), [])
        unrelated = gate.compare_case(case, verdict(case, "PP003"), runtime)
        self.assertTrue(any("expected diagnostic" in error for error in unrelated))
        for codes in (("PP099",), ("PP205", "PP099")):
            self.assertIn("internal verifier failure", gate.compare_case(case, verdict(case, *codes), runtime))
        for static in ({**verdict(case), "ok": False}, {**verdict(case, "PP205"), "ok": True}):
            self.assertIn("verifier verdict contradicts diagnostics", gate.compare_case(case, static, runtime))

    def test_value_and_exception_oracles_apply_to_accepted_and_rejected_cases(self):
        for codes in ((), ("PP205",)):
            with self.subTest(codes=codes):
                case = replace(identity_case(annotation="float", values=(-0.0,)), expected_codes=codes)
                static = verdict(case, *codes)
                self.assertEqual(gate.compare_case(case, static, outcomes(case, -0.0)), [])
                self.assertTrue(any("curated value" in error for error in
                                    gate.compare_case(case, static, outcomes(case, 0.0))))
                exceptional = replace(case, invocations=(Invocation((-0.0,), exception="ZeroDivisionError"),))
                expected = {"name": case.name, "outcomes": [{"index": 0, "exception": "ZeroDivisionError"}]}
                self.assertEqual(gate.compare_case(exceptional, static, expected), [])
                self.assertTrue(gate.compare_case(exceptional, static, outcomes(case, 0.0)))
                wrong = {"name": case.name, "outcomes": [{"index": 0, "exception": "ValueError"}]}
                self.assertTrue(gate.compare_case(exceptional, static, wrong))
                self.assertTrue(any("unexpected CPython exception" in error for error in
                                    gate.compare_case(case, static, expected)))

    def test_malformed_verifier_records_fail(self):
        case = identity_case()
        malformed = (
            None, [], {}, {**verdict(case), "ok": "true"},
            {**verdict(case), "diagnostics": None},
            {**verdict(case), "inferred_types": "int"},
            {**verdict(case), "diagnostics": [None]},
            {**verdict(case), "diagnostics": [{"code": 205}]},
            {**verdict(case), "diagnostics": [{"code": "PP205extra"}]},
        )
        for static in malformed:
            with self.subTest(static=static):
                self.assertIn("malformed verifier result", gate.compare_case(case, static, outcomes(case, 3)))

    def test_missing_reordered_duplicate_and_malformed_invocations_fail(self):
        case = identity_case(values=(1, 2))
        valid = outcomes(case, 1, 2)
        malformed = [None, {}, {**valid, "name": "other"}, {**valid, "extra": True},
                     {**valid, "outcomes": None}, {**valid, "outcomes": []},
                     {**valid, "outcomes": valid["outcomes"][:1]},
                     {**valid, "outcomes": valid["outcomes"] + valid["outcomes"][:1]},
                     {**valid, "outcomes": list(reversed(valid["outcomes"]))},
                     {**valid, "outcomes": [valid["outcomes"][0]] * 2}]
        for invalid in (None, {}, {"index": True, "value": gate.encode_value(1)},
                        {"index": 0, "value": gate.encode_value(1), "extra": 2},
                        {"index": 0, "value": gate.encode_value(1), "exception": "ValueError"},
                        {"index": 0, "exception": None},
                        {"index": 0, "exception": "RuntimeError"},
                        {"index": 0, "value": {"type": "int", "value": True}},
                        {"index": 0, "value": {"type": "float", "value": "0x1p99999999"}},
                        {"index": 0, "value": {"type": "set"}},
                        {"index": 0, "value": {"type": "tuple", "value": [{"type": "object"}]}}):
            malformed.append({**valid, "outcomes": [invalid, valid["outcomes"][1]]})
        for runtime in malformed:
            with self.subTest(runtime=runtime):
                self.assertTrue(gate.compare_case(case, verdict(case), runtime))

    def test_gate_validates_envelope_schemas_versions_and_case_order(self):
        cases = [identity_case("first"), identity_case("second", values=(4,))]
        static, runtime = envelopes(cases)
        for which, original in ((0, static), (1, runtime)):
            variants = [None, [], {}, {**original, "schema": True}, {**original, "schema": 2},
                        {**original, "schema": "1"}, {**original, "results": []},
                        {**original, "results": list(reversed(original["results"]))},
                        {**original, "results": [original["results"][0]] * 2},
                        {**original, "results": [None, original["results"][1]]}]
            version_key = "verifier_version" if which == 0 else "python_version"
            versions = (None, "", 314) if which == 0 else (None, "", 314, "3.13.7", "3.14", "3.14.7garbage")
            variants.extend({**original, version_key: version} for version in versions)
            for invalid in variants:
                responses = [static, runtime]
                responses[which] = invalid
                with self.subTest(which=which, invalid=invalid), patch.object(gate, "run_process", side_effect=responses):
                    with self.assertRaises(ValueError):
                        gate.run_gate(cases, Path("probe"))

    def test_worker_payload_contains_only_source_signature_and_typed_inputs(self):
        case = identity_case(annotation="float", values=(-0.0,))
        changed_oracles = replace(case, expected_codes=("PP205",),
                                  invocations=(Invocation((-0.0,), exception="ValueError"),))
        entry = gate.worker_entry(case)
        self.assertEqual(entry, gate.worker_entry(changed_oracles))
        self.assertEqual(set(entry), {"name", "family", "source", "parameters", "returns", "invocations"})
        self.assertEqual(entry["invocations"], [{"arguments": [gate.encode_value(-0.0)]}])
        self.assertEqual(runtime_case(worker_case(entry)), runtime_case(case))
        responses = envelopes([case])
        with patch.object(gate, "run_process", side_effect=responses) as run:
            failures, versions = gate.run_gate([case], Path("probe"))
        self.assertEqual(failures, [])
        self.assertEqual(versions, {"verifier_version": "test-verifier", "python_version": "3.14.7"})
        self.assertEqual(run.call_args_list[0].args[1], {"cases": [{
            "name": case.name, "source": case.source, "start": 0, "end": 1}]})
        self.assertEqual(run.call_args_list[1].args[1], [entry])
        self.assertIn("-I", run.call_args_list[1].args[0])

    def test_invalid_oracles_fail_before_starting_subprocesses(self):
        case = identity_case()
        invalid = [replace(case, expected_codes=codes) for codes in
                   (["PP205"], ("",), ("PP099",), ("PP205extra",), (205,))]
        for invocation in (Invocation((3,), 3, "true"),
                           Invocation((3,), 3, True, "ValueError"),
                           Invocation((3,), exception="RuntimeError"),
                           Invocation((3,), object(), True)):
            invalid.append(replace(case, invocations=(invocation,)))
        for bad in invalid:
            with self.subTest(case=bad), patch.object(gate, "run_process") as run:
                with self.assertRaises(ValueError):
                    gate.run_gate([bad], Path("probe"))
                run.assert_not_called()

    def test_failure_artifact_replays_source_inputs_and_oracles_losslessly(self):
        specimens = [("None", None), ("bool", True), ("int", -123),
                     ("float", -0.0), ("float", float("inf")), ("float", float("nan")),
                     ("str", "λ\n\\text"), ("bytes", b"\x00\xff"),
                     ("tuple[tuple[int, ...], ...]", ((), (1, -2)))]
        cases = [identity_case(f"roundtrip/{index}", annotation, (value,))
                 for index, (annotation, value) in enumerate(specimens)]
        cases.append(replace(identity_case("roundtrip/rejected"), expected_codes=("PP205",),
                             invocations=(Invocation((3,), exception="ValueError"),)))
        failures = [{"name": case.name, "errors": ["test mismatch"], "case": gate.case_entry(case),
                     "verifier": verdict(case), "runtime": None} for case in cases]
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "nested" / "failures.json"
            gate.save_failures(path, failures, {"python_version": "3.14.7", "verifier_version": "test"}, 17, 8)
            saved = json.loads(path.read_text())
            self.assertEqual((saved["schema"], saved["kind"], saved["seed"], saved["samples"]),
                             (1, "whole_function", 17, 8))
            self.assertEqual(saved["failures"], failures)
            restored = gate.load_replay(path)
            self.assertEqual([gate.case_entry(case) for case in restored],
                             [gate.case_entry(case) for case in cases])
            with patch.object(gate, "generate_cases") as generate, patch.object(
                    gate, "run_gate", return_value=([], {"python_version": "3.14.7", "verifier_version": "test"})) as run:
                with redirect_stdout(io.StringIO()), redirect_stderr(io.StringIO()):
                    self.assertEqual(gate.main(["--replay", str(path), "--failures", str(path)]), 0)
                generate.assert_not_called()
                self.assertEqual([gate.case_entry(case) for case in run.call_args.args[0]],
                                 [gate.case_entry(case) for case in cases])
            self.assertEqual(json.loads(path.read_text()), saved)

    def test_invalid_replay_schema_fields_and_oracles_fail_closed(self):
        entry = gate.case_entry(identity_case())
        malformed_entries = [None, {}, {**entry, "extra": 1}, {**entry, "parameters": {}}]
        for field, value in (("arguments", [{"type": "object"}]),
                             ("arguments", [{"type": "float", "value": "0x1p99999999"}]),
                             ("expected_value", {"type": "int", "value": True}),
                             ("expected_value", {"type": "float", "value": "0x1p99999999"}),
                             ("exception", "RuntimeError"), ("check_value", "yes")):
            malformed = deepcopy(entry)
            malformed["invocations"][0][field] = value
            malformed_entries.append(malformed)
        for malformed in malformed_entries:
            with self.subTest(entry=malformed), self.assertRaises(ValueError):
                gate.decode_case(malformed)
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "replay.json"
            for payload in ([], {}, {"schema": True, "kind": "whole_function", "failures": []},
                            {"schema": 2, "kind": "whole_function", "failures": []},
                            {"schema": 1, "kind": "expression", "failures": []},
                            {"schema": 1, "kind": "whole_function", "failures": [None]}):
                path.write_text(json.dumps(payload))
                with self.subTest(payload=payload), self.assertRaises(ValueError):
                    gate.load_replay(path)

    def test_subprocess_and_protocol_failures_exit_one_and_preserve_inputs(self):
        cases = [identity_case("first"), identity_case("second", "bytes", (b"\x00\xff",))]
        static, _runtime = envelopes(cases)
        failures = (
            OSError("probe missing"), ValueError("probe failed (2): bad input"),
            subprocess.TimeoutExpired("probe", 1),
            [{"schema": False}],
            [static, {"schema": 1, "python_version": "3.14.7", "results": []}],
        )
        for failure in failures:
            with self.subTest(failure=failure), tempfile.TemporaryDirectory() as directory:
                output = Path(directory) / "failures.json"
                with patch.object(gate, "generate_cases", return_value=cases), patch.object(
                        gate, "run_process", side_effect=failure):
                    stderr = io.StringIO()
                    with redirect_stdout(io.StringIO()), redirect_stderr(stderr):
                        self.assertEqual(gate.main(["--seed", "23", "--samples", "2", "--failures", str(output)]), 1)
                self.assertIn("whole-function differential gate failed", stderr.getvalue())
                saved = json.loads(output.read_text())
                self.assertEqual((saved["seed"], saved["samples"]), (23, 2))
                self.assertEqual([record["case"] for record in saved["failures"]],
                                 [gate.case_entry(case) for case in cases])
                self.assertEqual([gate.case_entry(case) for case in gate.load_replay(output)],
                                 [gate.case_entry(case) for case in cases])

    def test_success_retains_existing_failure_artifact(self):
        case = identity_case()
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / "failures.json"
            sentinel = b"previous reviewed artifact\n"
            output.write_bytes(sentinel)
            with patch.object(gate, "generate_cases", return_value=[case]), patch.object(
                    gate, "run_process", side_effect=envelopes([case])):
                with redirect_stdout(io.StringIO()), redirect_stderr(io.StringIO()):
                    self.assertEqual(gate.main(["--samples", "0", "--failures", str(output)]), 0)
            self.assertEqual(output.read_bytes(), sentinel)

    def test_replay_failure_records_replay_origin_instead_of_generator_settings(self):
        case = identity_case()
        record = {"name": case.name, "errors": ["previous mismatch"],
                  "case": gate.case_entry(case), "verifier": None, "runtime": None}
        with tempfile.TemporaryDirectory() as directory:
            replay = Path(directory) / "previous.json"
            output = Path(directory) / "current.json"
            gate.save_failures(replay, [record], {}, 42, 256)
            static, runtime = envelopes([case])
            runtime["results"][0]["outcomes"][0]["value"] = gate.encode_value(True)
            with patch.object(gate, "run_process", side_effect=[static, runtime]):
                stdout = io.StringIO()
                with redirect_stdout(stdout), redirect_stderr(io.StringIO()):
                    self.assertEqual(gate.main(["--replay", str(replay), "--failures", str(output)]), 1)
            self.assertIn(f"replay={replay}", stdout.getvalue())
            self.assertNotIn("seed=", stdout.getvalue())
            saved = json.loads(output.read_text())
            self.assertEqual(saved["replay_source"], str(replay))
            self.assertIsNone(saved["seed"])
            self.assertIsNone(saved["samples"])
            self.assertEqual(gate.case_entry(gate.load_replay(output)[0]), gate.case_entry(case))

    def test_malformed_runtime_number_preserves_replayable_failure_artifact(self):
        case = identity_case(annotation="float", values=(1.0,))
        static, runtime = envelopes([case])
        runtime["results"][0]["outcomes"][0]["value"] = {"type": "float", "value": "0x1p99999999"}
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / "failures.json"
            with patch.object(gate, "generate_cases", return_value=[case]), patch.object(
                    gate, "run_process", side_effect=[static, runtime]):
                with redirect_stdout(io.StringIO()), redirect_stderr(io.StringIO()):
                    self.assertEqual(gate.main(["--samples", "0", "--failures", str(output)]), 1)
            saved = json.loads(output.read_text())
            self.assertTrue(any("malformed" in error for error in saved["failures"][0]["errors"]))
            self.assertEqual(saved["failures"][0]["case"], gate.case_entry(case))
            self.assertEqual(saved["failures"][0]["runtime"], runtime["results"][0])
            self.assertEqual([gate.case_entry(item) for item in gate.load_replay(output)], [gate.case_entry(case)])

    @unittest.skipUnless(platform.python_implementation() == "CPython" and sys.version_info[:2] == (3, 14),
                         "live worker requires CPython 3.14")
    def test_live_fixed_corpus(self):
        probe = gate.ROOT / "bin/purepy-semantic-probe"
        if not probe.exists():
            self.skipTest("run make differential-test to build the development helper")
        names = {"regression/for_target_break_optional", "regression/for_target_continue_optional",
                 "regression/for_iterable_before_body_rebinding", "regression/range_argument_before_body_rebinding",
                 "tuple/optional_nested_result", "domain/guarded_division"}
        cases = generate_cases(samples=0)
        self.assertTrue(names.issubset({case.name for case in cases}))
        self.assertGreaterEqual(len(cases), 41)
        failures, versions = gate.run_gate(cases, probe)
        self.assertEqual(failures, [])
        self.assertRegex(versions["python_version"], r"^3\.14\.[0-9]+$")


if __name__ == "__main__":
    unittest.main()
