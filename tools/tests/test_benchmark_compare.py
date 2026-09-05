"""Exercise false-negative and noise paths in the performance regression gate."""

from contextlib import redirect_stderr, redirect_stdout
from copy import deepcopy
import io
import json
from pathlib import Path
import sys
import tempfile
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import benchmark_compare as gate


def summary(wall, cpu, rss, phase, files):
    hits = files if phase == "warm_unchanged" else files - 1 if phase == "one_changed_declaration" else 0
    return {
        "wall_ms": {"min": wall, "median": wall, "max": wall},
        "median_process_cpu_ms": cpu,
        "median_peak_rss_bytes": rss,
        "cache_hits": [hits] * 3,
        "samples": [{"wall_ms": wall, "process_cpu_ms": cpu, "peak_rss_bytes": rss,
                     "files": files, "cache_hits": hits} for _ in range(3)],
    }


def report():
    result = {
        "schema": 2,
        "hardware": {"label": "test", "processor": "test CPU", "logical_cpus": 4,
                     "platform": "Linux-test", "machine": "aarch64", "harness_python": "3.14.7",
                     "memory_bytes": 8 * 1024 ** 3},
        "method": "isolated process measurement",
        "build_settings": {"toolchain": "go1.24.0", "GOARCH": "arm64", "GOOS": "linux"},
        "runtime_environment": {name: None for name in gate.RUNTIME_VARIABLES},
        "matrix": {"sizes": [10, 40], "workers": [1, 2], "corpora": ["deep"],
                   "functions_per_module": 10, "repeats": 3, "timeout_seconds": 120},
        "binary_sha256": "a" * 64,
        "corpora": [],
    }
    for size in (10, 40):
        for jobs in (1, 2):
            result["corpora"].append({
                "corpus": "deep", "modules": size, "jobs": jobs, "repeats": 3,
                "source_files": size + 2, "source_bytes": size * 100, "input_bytes": size * 100,
                "corpus_sha256": str(size // 10) * 64,
                "baseline_report_sha256": "e" * 64,
                "changed_report_sha256": {"one_changed_declaration": "f" * 64},
                "changed_input_sha256": {"one_changed_declaration": "c" * 64},
                "equivalence": {key: True for key in gate.EQUIVALENCE},
                "phases": {phase: summary(size * 10, size * 20, size * 1024 * 1024, phase, size + 2)
                           for phase in gate.PHASES},
            })
    return result


def replace_metric(entry, phase, metric_name, value):
    measurement = entry["phases"][phase]
    for sample in measurement["samples"]:
        sample[metric_name] = value
    if metric_name == "wall_ms":
        measurement["wall_ms"] = {"min": value, "median": value, "max": value}
    else:
        measurement["median_" + metric_name] = value


class BenchmarkComparisonTests(unittest.TestCase):
    def test_identical_complete_matrix_passes_and_counts_growth_checks(self):
        baseline = report()
        candidate = deepcopy(baseline)
        candidate["binary_sha256"] = "b" * 64
        candidate["verifier"] = "a newer revision"
        candidate["measured_at_utc"] = "a later measurement"
        result = gate.compare(baseline, candidate)
        self.assertTrue(result["passed"])
        self.assertEqual(result["status"], "passed")
        self.assertEqual(result["measurements_checked"], 48)
        self.assertEqual(result["growth_checks"], 24)

    def test_noise_allowance_is_additive_to_relative_budget(self):
        baseline = report()
        candidate = deepcopy(baseline)
        replace_metric(candidate["corpora"][0], "warm_unchanged", "wall_ms", 175)
        self.assertTrue(gate.compare(baseline, candidate)["passed"])
        replace_metric(candidate["corpora"][0], "warm_unchanged", "wall_ms", 175.001)
        result = gate.compare(baseline, candidate)
        self.assertFalse(result["passed"])
        self.assertEqual(result["failures"][0]["limit"], 175)

    def test_detects_cpu_and_per_process_peak_memory_regressions(self):
        for name, value in (("process_cpu_ms", 351), ("peak_rss_bytes", 32 * 1024 * 1024)):
            with self.subTest(metric=name):
                baseline = report()
                candidate = deepcopy(baseline)
                replace_metric(candidate["corpora"][0], "uncached", name, value)
                result = gate.compare(baseline, candidate)
                self.assertFalse(result["passed"])
                self.assertTrue(any(item["case"].endswith(name) for item in result["failures"]))

    def test_growth_gate_detects_increase_within_individual_point_budget(self):
        baseline = report()
        candidate = deepcopy(baseline)
        replace_metric(candidate["corpora"][2], "uncached", "wall_ms", 550)
        result = gate.compare(baseline, candidate, gate.Budgets(wall_ms=0))
        self.assertEqual([item["kind"] for item in result["failures"]], ["growth"])
        self.assertEqual(result["failures"][0]["input_growth"], 4)
        self.assertEqual(result["failures"][0]["limit"], 500)

    def test_growth_gate_covers_memory_and_does_not_punish_small_input_improvement(self):
        baseline = report()
        candidate = deepcopy(baseline)
        replace_metric(candidate["corpora"][2], "uncached", "peak_rss_bytes", 55 * 1024 * 1024)
        result = gate.compare(baseline, candidate, gate.Budgets(peak_rss_bytes=0))
        self.assertEqual([item["kind"] for item in result["failures"]], ["growth"])
        candidate = deepcopy(baseline)
        replace_metric(candidate["corpora"][0], "uncached", "wall_ms", 1)
        self.assertTrue(gate.compare(baseline, candidate)["passed"])

    def test_hardware_settings_and_methods_must_match(self):
        for field, replacement in (("hardware", {**report()["hardware"], "processor": "different"}),
                                   ("method", "different measurement method"),
                                   ("build_settings", {**report()["build_settings"], "toolchain": "another compiler"})):
            with self.subTest(field=field):
                baseline = report()
                candidate = deepcopy(baseline)
                candidate[field] = replacement
                result = gate.compare(baseline, candidate)
                self.assertFalse(result["passed"])
                self.assertFalse(result["comparable"])
                self.assertEqual(result["measurements_checked"], 0)
                result = gate.compare(baseline, candidate, allow_incompatible=True)
                self.assertEqual(result["status"], "exploratory")
                self.assertFalse(result["passed"])
                self.assertGreater(result["measurements_checked"], 0)

    def test_corpus_content_and_settings_cannot_silently_differ(self):
        baseline = report()
        candidate = deepcopy(baseline)
        candidate["corpora"][0]["corpus_sha256"] = "9" * 64
        candidate["corpora"][1]["corpus_sha256"] = "9" * 64
        result = gate.compare(baseline, candidate, allow_incompatible=True)
        self.assertEqual(result["status"], "exploratory")
        self.assertEqual(result["measurements_checked"], 24)
        candidate = deepcopy(baseline)
        candidate["matrix"]["timeout_seconds"] = 30
        self.assertIn("matrix differs", gate.compare(baseline, candidate)["incompatibilities"])

    def test_runtime_controls_must_match_even_when_hardware_and_binary_match(self):
        for key, value in (("GOGC", "off"), ("GOMEMLIMIT", "512MiB"),
                           ("GOMAXPROCS", "1"), ("GODEBUG", "asyncpreemptoff=1"), ("GOGC", "")):
            with self.subTest(key=key, value=value):
                baseline = report()
                candidate = deepcopy(baseline)
                candidate["runtime_environment"][key] = value
                result = gate.compare(baseline, candidate)
                self.assertEqual(result["incompatibilities"], ["runtime_environment differs"])
                self.assertFalse(result["passed"])
                self.assertEqual(result["measurements_checked"], 0)
                self.assertEqual(gate.compare(baseline, candidate, allow_incompatible=True)["status"], "exploratory")

    def test_runtime_control_mapping_is_required_complete_and_typed(self):
        invalid = (None, {}, {"GOGC": "100"},
                   {**report()["runtime_environment"], "EXTRA": "value"},
                   {**report()["runtime_environment"], "GOGC": 100},
                   {**report()["runtime_environment"], "GODEBUG": False})
        for value in invalid:
            with self.subTest(value=value):
                candidate = report()
                candidate["runtime_environment"] = value
                with self.assertRaisesRegex(ValueError, "runtime_environment"):
                    gate.compare(report(), candidate)
        candidate = report()
        candidate.pop("runtime_environment")
        with self.assertRaisesRegex(ValueError, "runtime_environment"):
            gate.compare(report(), candidate)

    def test_missing_duplicate_and_extra_matrix_entries_are_rejected(self):
        for operation in (lambda entries: entries.pop(),
                          lambda entries: entries.append(deepcopy(entries[0])),
                          lambda entries: entries[0].update(jobs=3)):
            with self.subTest(operation=operation):
                baseline = report()
                candidate = deepcopy(baseline)
                operation(candidate["corpora"])
                with self.assertRaises(ValueError):
                    gate.compare(baseline, candidate, allow_incompatible=True)

    def test_missing_phases_samples_and_unverified_equality_fail_closed(self):
        operations = (
            lambda item: item["phases"].pop("uncached"),
            lambda item: item["phases"]["uncached"]["samples"].pop(),
            lambda item: item.update(equivalence={}),
            lambda item: item["equivalence"].update(cold_warm_uncached=False),
        )
        for operation in operations:
            with self.subTest(operation=operation):
                baseline = report()
                candidate = deepcopy(baseline)
                operation(candidate["corpora"][0])
                with self.assertRaises(ValueError):
                    gate.compare(baseline, candidate)

    def test_worker_equivalence_requires_identical_input_and_report_hashes(self):
        for field in ("corpus_sha256", "baseline_report_sha256", "changed_report_sha256", "changed_input_sha256"):
            with self.subTest(field=field):
                baseline = report()
                candidate = deepcopy(baseline)
                candidate["corpora"][1][field] = ({"one_changed_declaration": "0" * 64}
                                                  if field.startswith("changed_") else "0" * 64)
                with self.assertRaisesRegex(ValueError, "differ across worker counts"):
                    gate.compare(baseline, candidate)

    def test_changed_input_identity_is_part_of_artifact_comparability(self):
        baseline = report()
        candidate = deepcopy(baseline)
        for item in candidate["corpora"]:
            item["changed_input_sha256"]["one_changed_declaration"] = "d" * 64
        result = gate.compare(baseline, candidate, allow_incompatible=True)
        self.assertFalse(result["passed"])
        self.assertEqual(result["measurements_checked"], 0)
        self.assertTrue(all("changed_input_sha256" in reason for reason in result["incompatibilities"]))

    def test_declared_invalidation_requires_changed_inputs_and_reports(self):
        for changed, original in (("changed_input_sha256", "corpus_sha256"),
                                  ("changed_report_sha256", "baseline_report_sha256")):
            baseline = report()
            candidate = deepcopy(baseline)
            candidate["corpora"][0][changed]["one_changed_declaration"] = candidate["corpora"][0][original]
            with self.assertRaisesRegex(ValueError, "edit did not change"):
                gate.compare(baseline, candidate)

    def test_manifest_edit_uses_conservative_full_cache_invalidation(self):
        baseline = report()
        baseline["matrix"]["corpora"] = ["manifest"]
        for item in baseline["corpora"]:
            item["corpus"] = "manifest"
            item["changed_report_sha256"]["one_changed_manifest"] = "d" * 64
            item["changed_input_sha256"]["one_changed_manifest"] = "b" * 64
            item["phases"]["one_changed_manifest"] = summary(100, 200, 1024 ** 2,
                                                              "one_changed_manifest", item["source_files"])
        self.assertTrue(gate.compare(baseline, deepcopy(baseline))["passed"])
        candidate = deepcopy(baseline)
        candidate["corpora"][0]["phases"]["one_changed_manifest"]["samples"][0]["cache_hits"] = 1
        with self.assertRaisesRegex(ValueError, "incorrect cache"):
            gate.compare(baseline, candidate)

    def test_fixed_size_corpora_are_singletons_per_worker(self):
        baseline = report()
        baseline["matrix"]["corpora"] = ["tiny", "reference"]
        for index, item in enumerate(baseline["corpora"]):
            item["corpus"] = "tiny" if index < 2 else "reference"
            item["modules"] = 1 if index < 2 else None
        result = gate.compare(baseline, deepcopy(baseline))
        self.assertTrue(result["passed"])
        self.assertEqual(result["growth_checks"], 0)

    def test_empty_hardware_and_build_metadata_cannot_claim_comparability(self):
        for field, key, value in (("hardware", "processor", ""),
                                  ("hardware", "platform", None),
                                  ("hardware", "logical_cpus", 0),
                                  ("build_settings", "toolchain", "")):
            candidate = report()
            candidate[field][key] = value
            with self.assertRaises(ValueError):
                gate.compare(report(), candidate)

    def test_generic_processor_requires_an_explicit_hardware_label(self):
        baseline = report()
        baseline["hardware"].update(processor="arm", label="arm")
        with self.assertRaisesRegex(ValueError, "explicit hardware label"):
            gate.compare(baseline, deepcopy(baseline))
        baseline["hardware"]["label"] = "Apple M4"
        self.assertTrue(gate.compare(baseline, deepcopy(baseline))["passed"])

    def test_medians_and_numeric_values_are_validated(self):
        for value in (float("nan"), float("inf"), -1, True, "100", 3):
            with self.subTest(value=value):
                baseline = report()
                candidate = deepcopy(baseline)
                candidate["corpora"][0]["phases"]["uncached"]["wall_ms"]["median"] = value
                with self.assertRaises(ValueError):
                    gate.compare(baseline, candidate)
        for invalid in (-1, float("nan"), float("inf")):
            with self.assertRaises(ValueError):
                gate.compare(report(), report(), gate.Budgets(relative=invalid))

    def test_timing_stage_additions_do_not_change_scalar_comparability(self):
        baseline = report()
        candidate = deepcopy(baseline)
        candidate["corpora"][0]["phases"]["uncached"]["median_stage_ms"] = {"new_stage": 5}
        self.assertTrue(gate.compare(baseline, candidate)["passed"])

    def test_schema_one_aggregate_rss_is_never_accepted_as_per_process_rss(self):
        candidate = report()
        candidate["schema"] = 1
        candidate["aggregate_child_peak_rss_bytes"] = 123
        with self.assertRaisesRegex(ValueError, "schema 2"):
            gate.compare(report(), candidate, allow_incompatible=True)

    def test_cli_exit_codes_distinguish_pass_regression_and_exploratory(self):
        with tempfile.TemporaryDirectory() as scratch:
            folder = Path(scratch)
            baseline_path = folder / "base.json"
            candidate_path = folder / "head.json"
            output = folder / "nested" / "comparison.json"
            baseline = report()
            candidate = deepcopy(baseline)
            baseline_path.write_text(json.dumps(baseline))
            args = ["--baseline", str(baseline_path), "--candidate", str(candidate_path),
                    "--output", str(output)]
            for expected, modified, extra in (
                (0, candidate, []),
                (2, {**candidate, "method": "other"}, ["--allow-incompatible"]),
                (2, {**candidate, "schema": 1}, []),
            ):
                candidate_path.write_text(json.dumps(modified))
                with redirect_stdout(io.StringIO()), redirect_stderr(io.StringIO()):
                    self.assertEqual(gate.main(args + extra), expected)
            replace_metric(candidate["corpora"][0], "uncached", "wall_ms", 1000)
            candidate_path.write_text(json.dumps(candidate))
            with redirect_stdout(io.StringIO()):
                self.assertEqual(gate.main(args), 1)
            self.assertEqual(json.loads(output.read_text())["status"], "regressed")


if __name__ == "__main__":
    unittest.main()
