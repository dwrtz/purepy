"""Nominal/module/async differential observations and fail-closed protocol tests."""

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
import types
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import differential_modules as gate
import module_runtime as runtime
from module_cases import Call, Record, catalog


CASES = {case.name: case for case in catalog()}


def verdict(case, codes=None):
    codes = case.codes if codes is None else codes
    return {"schema": 1, "verifier_version": "test-verifier", "ok": not codes,
            "diagnostics": [{"code": code} for code in codes],
            "functions": [{"name": "main.probe", "returns": gate.reported_type(case.returns),
                           "kind": case.kind, "classification": case.classification}]}


def envelope(cases):
    return {"schema": 1, "python_version": "3.14.7", "hash_randomization": 1,
            "results": [runtime.runtime_case(case) for case in cases]}


class ModuleDifferentialTests(unittest.TestCase):
    def test_catalog_covers_nominal_import_opaque_and_async_contracts(self):
        self.assertEqual(len(CASES), len(catalog()))
        self.assertGreaterEqual(len(CASES), 26)
        self.assertTrue({"records/private_mangled_keyword", "records/nested_optional_tuple",
                         "imports/package_absolute_composition", "imports/nominal_names_are_distinct",
                         "opaque/nested_membership_rejected", "async/loop_await_order",
                         "async/capability_host_reference_and_none"}.issubset(CASES))
        for case in CASES.values():
            with self.subTest(case=case.name):
                self.assertEqual(gate.compare_case(case, verdict(case), runtime.runtime_case(case)), [])

    def test_exact_record_types_validate_identity_field_names_and_nested_types(self):
        value = runtime.input_data(Record("models.Pair", (("x", 1), ("label", "ok"))))
        self.assertTrue(gate.exact_type(value, "models.Pair"))
        wrong = deepcopy(value)
        wrong["name"] = "left.Pair"
        self.assertFalse(gate.exact_type(wrong, "models.Pair"))
        wrong = deepcopy(value)
        wrong["fields"][0][1] = runtime.input_data(True)
        self.assertFalse(gate.exact_type(wrong, "models.Pair"))
        for entries in (value["fields"][:1], list(reversed(value["fields"])),
                        [value["fields"][0]] * 2):
            self.assertFalse(gate.exact_type({**value, "fields": entries}, "models.Pair"))
        nested = runtime.input_data(Record("main.Batch", (("entries", (Record("models.Pair", (("x", 2), ("label", "x"))), None)),)))
        self.assertTrue(gate.exact_type(nested, "main.Batch"))
        nested["fields"][0][1]["value"][0]["fields"][0][1] = runtime.input_data(False)
        self.assertFalse(gate.exact_type(nested, "main.Batch"))

    def test_accidental_acceptance_detects_nominal_and_bool_field_witnesses(self):
        for name in ("imports/nominal_names_are_distinct", "records/exact_field_type"):
            case = CASES[name]
            observation = runtime.runtime_case(case)
            errors = gate.compare_case(case, verdict(case, ()), observation)
            self.assertIn("expected PurePy rejection", errors)
            self.assertTrue(any("exact return type" in error for error in errors))

    def test_host_calls_and_hidden_equality_use_independent_event_oracles(self):
        for name in ("opaque/record_equality_rejected", "opaque/nested_membership_rejected",
                     "async/capability_host_reference_and_none", "async/loop_await_order"):
            case = CASES[name]
            observed = runtime.runtime_case(case)
            self.assertEqual(gate.compare_case(case, verdict(case), observed), [])
            observed["outcomes"][-1]["events"] = []
            self.assertTrue(any("host operation/equality" in error for error in gate.compare_case(case, verdict(case), observed)))
        case = CASES["async/loop_await_order"]
        observed = runtime.runtime_case(case)
        observed["outcomes"][1]["events"][1][-1] = True
        self.assertTrue(gate.compare_case(case, verdict(case), observed))

    def test_worker_protocol_has_no_executable_or_oracle_fields(self):
        case = CASES["records/positional"]
        entry = runtime.worker_entry(case)
        self.assertEqual(set(entry), {"name", "fingerprint"})
        changed = replace(case, returns="bool", codes=("PP205",),
                          calls=tuple(replace(call, expected=False, events=(("fake",),)) for call in case.calls))
        self.assertEqual(runtime.worker_entry(changed), entry)
        self.assertNotIn("returns", runtime.executable_spec(case))
        self.assertEqual(runtime.select_cases([entry]), [case])

    def test_worker_rejects_unknown_duplicate_modified_and_source_requests(self):
        entry = runtime.worker_entry(CASES["records/positional"])
        for payload in (None, {}, [], [entry, entry], [{**entry, "source": "print('unsafe')"}],
                        [{**entry, "name": "../main"}], [{**entry, "fingerprint": "0" * 64}],
                        [{**entry, "expected": 3}], [{"name": ["records/positional"]}]):
            with self.subTest(payload=payload), self.assertRaises(ValueError):
                runtime.select_cases(payload)
        altered = replace(CASES["records/positional"], sources=(("main", "print('unsafe')\n"),))
        with self.assertRaisesRegex(ValueError, "unmodified fixed"):
            runtime.runtime_case(altered)

    def test_verifier_internal_failures_and_wrong_exact_codes_fail(self):
        case = CASES["opaque/record_equality_rejected"]
        observed = runtime.runtime_case(case)
        for codes in (("PP099",), ("PP212", "PP099"), ("PP205",), ("PP212", "PP205")):
            self.assertTrue(gate.compare_case(case, verdict(case, codes), observed))
        self.assertIn("internal verifier failure", gate.compare_case(case, verdict(case, ("PP099",)), observed))

    def test_malformed_static_schema_and_entrypoint_classification_fail(self):
        case = CASES["async/trusted_pure_operation"]
        observed = runtime.runtime_case(case)
        valid = verdict(case)
        for invalid in (None, {}, {**valid, "schema": True}, {**valid, "ok": 1},
                        {**valid, "verifier_version": None}, {**valid, "diagnostics": [None]},
                        {**valid, "functions": []}, {**valid, "functions": valid["functions"] * 2}):
            with self.subTest(invalid=invalid):
                self.assertTrue(gate.compare_case(case, invalid, observed))
        for field, value in (("kind", "sync"), ("classification", "effectful_async"), ("returns", {"kind": "bool"})):
            invalid = deepcopy(valid)
            invalid["functions"][0][field] = value
            self.assertTrue(gate.compare_case(case, invalid, observed))

    def test_missing_reordered_duplicate_and_malformed_runtime_outcomes_fail(self):
        case = CASES["records/positional"]
        valid = runtime.runtime_case(case)
        for invalid in (None, {}, {**valid, "name": "other"}, {**valid, "fingerprint": "wrong"},
                        {**valid, "outcomes": []}, {**valid, "outcomes": valid["outcomes"] * 2},
                        {**valid, "outcomes": list(reversed(valid["outcomes"]))}):
            self.assertTrue(gate.compare_case(case, verdict(case), invalid))
        for outcome in ({}, {"index": True, "value": runtime.input_data(1), "events": []},
                        {"index": 0, "exception": "RuntimeBudgetExceeded", "events": []},
                        {"index": 0, "exception": "ValueError", "value": runtime.input_data(1), "events": []},
                        {"index": 0, "value": {"type": "record", "name": "unknown.Type", "fields": []}, "events": []},
                        {"index": 0, "value": {"type": "float", "value": "0x1p99999999"}, "events": []}):
            invalid = deepcopy(valid)
            invalid["outcomes"][0] = outcome
            self.assertTrue(gate.compare_case(case, verdict(case), invalid))

    def test_domain_exception_is_observed_and_harness_errors_escape(self):
        case = CASES["async/domain_exception"]
        observed = runtime.runtime_case(case)
        self.assertEqual(observed["outcomes"][1]["exception"], "ZeroDivisionError")
        with patch.object(runtime, "finish_coroutine", side_effect=runtime.CorpusError("fixture broken")):
            with self.assertRaisesRegex(runtime.CorpusError, "fixture broken"):
                runtime.runtime_case(CASES["records/positional"])

    def test_execution_and_suspension_budgets_do_not_become_domain_errors(self):
        with patch.object(runtime, "MAX_EVENTS", 0), self.assertRaises(runtime.RuntimeBudgetExceeded):
            runtime.runtime_case(CASES["records/positional"])
        with patch.object(runtime, "MAX_SUSPENSIONS", 0), self.assertRaises(runtime.RuntimeBudgetExceeded):
            runtime.runtime_case(CASES["async/trusted_pure_operation"])

    def test_module_identity_is_fresh_and_previous_modules_and_path_restored(self):
        sentinel = types.ModuleType("models")
        prior_path = sys.path[:]
        with patch.dict(sys.modules, {"models": sentinel}):
            first = runtime.runtime_case(CASES["records/positional"])
            second = runtime.runtime_case(CASES["records/keyword"])
            self.assertIs(sys.modules["models"], sentinel)
            self.assertEqual(first["outcomes"], second["outcomes"])
        self.assertEqual(sys.path, prior_path)
        from dataclasses import make_dataclass
        false_pair = make_dataclass("Pair", [("x", int), ("label", str)])
        false_pair.__module__ = "models"
        fake_module = types.ModuleType("models")
        fake_module.Pair = object
        with self.assertRaisesRegex(ValueError, "exact class identity"):
            runtime.observe(false_pair(1, "x"), {"models": fake_module})

    def test_two_isolated_workers_must_agree_and_enable_hash_randomization(self):
        cases = (CASES["records/positional"],)
        valid = envelope(cases)
        with patch.object(gate, "static_case", return_value=verdict(cases[0])), patch.object(
                gate, "run_process", side_effect=[valid, deepcopy(valid)]) as run:
            failures, versions = gate.run_gate(cases, Path("verifier"))
        self.assertEqual(failures, [])
        self.assertEqual(versions["python_version"], "3.14.7")
        self.assertEqual(run.call_count, 2)
        for call in run.call_args_list:
            self.assertIn("-I", call.args[0])
            self.assertEqual(call.args[1], [runtime.worker_entry(cases[0])])
        for changed in ({**valid, "schema": True}, {**valid, "python_version": "3.13.9"},
                        {**valid, "hash_randomization": 0}, {**valid, "hash_randomization": True},
                        {**valid, "results": []}):
            with patch.object(gate, "static_case", return_value=verdict(cases[0])), patch.object(
                    gate, "run_process", side_effect=[valid, changed]), self.assertRaises(ValueError):
                gate.run_gate(cases, Path("verifier"))

    def test_static_runner_is_uncached_read_only_cli_with_declared_manifest(self):
        case = CASES["async/trusted_pure_operation"]

        def invoke(command, **kwargs):
            root = Path(command[2])
            self.assertEqual(command[1], "check")
            self.assertIn("--no-cache", command)
            self.assertEqual((root / "src/main.py").read_text(), dict(case.sources)["main"])
            self.assertEqual((root / "fixture.toml").read_text(), case.manifest)
            self.assertFalse((root / "src/fixture.py").exists())
            self.assertIn('entrypoints = ["main.probe"]', (root / "purepy.toml").read_text())
            return subprocess.CompletedProcess(command, 0, json.dumps(verdict(case)), "")

        with patch.object(gate.subprocess, "run", side_effect=invoke):
            self.assertEqual(gate.static_case(case, Path("verifier")), verdict(case))
        for result in (subprocess.CompletedProcess([], 2, "{}", "bad configuration"),
                       subprocess.CompletedProcess([], 1, json.dumps(verdict(case)), "")):
            with patch.object(gate.subprocess, "run", return_value=result), self.assertRaises(ValueError):
                gate.static_case(case, Path("verifier"))

    def test_failures_preserve_reviewable_source_inputs_oracles_and_stable_rerun_name(self):
        case = CASES["records/positional"]
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "failures.json"
            gate.save_failures(path, [case], [{"name": case.name, "errors": ["witness"]}], {"python_version": "3.14.7"})
            saved = json.loads(path.read_text())
            self.assertEqual(saved["kind"], "fixed_module")
            record = saved["failures"][0]["case"]
            self.assertEqual(record["sources"], dict(case.sources))
            self.assertEqual(record["fingerprint"], runtime.fingerprint(case))
            self.assertEqual(record["calls"][0]["expected"], runtime.input_data(case.calls[0].expected))
            self.assertEqual(record["returns"], case.returns)
            with patch.object(gate, "run_gate", side_effect=subprocess.TimeoutExpired("verifier", 30)):
                with redirect_stdout(io.StringIO()), redirect_stderr(io.StringIO()):
                    self.assertEqual(gate.main(["--case", case.name, "--failures", str(path)]), 1)
            self.assertEqual(json.loads(path.read_text())["failures"][0]["name"], case.name)

    @unittest.skipUnless(platform.python_implementation() == "CPython" and sys.version_info[:2] == (3, 14),
                         "live module gate requires CPython 3.14")
    def test_live_fixed_catalog(self):
        verifier = gate.ROOT / "bin/purepy"
        if not verifier.exists():
            self.skipTest("make differential-test builds the production verifier")
        failures, versions = gate.run_gate(catalog(), verifier)
        self.assertEqual(failures, [])
        self.assertRegex(versions["python_version"], r"^3\.14\.[0-9]+$")


if __name__ == "__main__":
    unittest.main()
