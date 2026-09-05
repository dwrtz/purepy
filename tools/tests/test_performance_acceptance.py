"""Ensure incomplete or worse measurements cannot pass fixed acceptance budgets."""

from copy import deepcopy
import json
from pathlib import Path
import sys
import unittest

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "tools"))
from performance_acceptance import accept


class AcceptanceTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.profile = json.loads((ROOT / "benchmarks/acceptance-m4.json").read_text())
        cls.benchmark = json.loads((ROOT / "benchmarks/baseline.json").read_text())
        cls.service = json.loads((ROOT / "docs/validation/2026-09-05/service.json").read_text())

    def test_recorded_measurements_pass_predefined_ceilings(self):
        result = accept(self.profile, self.benchmark, self.service)
        self.assertTrue(result["passed"], result)
        self.assertGreater(result["measurements_checked"], 250)

    def test_wrong_machine_and_incomplete_matrix_reject(self):
        for field, key, value in (("hardware", "label", "different CPU"),
                                  ("matrix", "repeats", 1)):
            with self.subTest(field=field):
                candidate = deepcopy(self.benchmark)
                candidate[field][key] = value
                with self.assertRaises(ValueError):
                    accept(self.profile, candidate, self.service)

    def test_service_shortcuts_and_accounting_fail_closed(self):
        for path, value in ((('ok',), False), (('options', 'duration'), 30),
                            (('resources_after_cleanup', 'host_tasks_remaining'), 1),
                            (('errors', 'count'), 1), (('requests',), 1),
                            (('sse', 'events_per_stream'), [1, 1, 1, 1]),
                            (('database_final', 'balance'), 0)):
            with self.subTest(path=path):
                candidate = deepcopy(self.service)
                target = candidate
                for part in path[:-1]:
                    target = target[part]
                target[path[-1]] = value
                with self.assertRaises(ValueError):
                    accept(self.profile, self.benchmark, candidate)

    def test_latency_throughput_and_memory_regressions_fail(self):
        for key, value in (("throughput_requests_per_second", 1),
                           ("peak_resident_bytes", 200 * 1024 ** 2),
                           ("traced_python_bytes_after_cleanup", 20 * 1024 ** 2)):
            with self.subTest(key=key):
                candidate = deepcopy(self.service)
                candidate[key] = value
                self.assertFalse(accept(self.profile, self.benchmark, candidate)["passed"])
        candidate = deepcopy(self.service)
        candidate["latency_ms"]["p99"] = 500
        self.assertFalse(accept(self.profile, self.benchmark, candidate)["passed"])

    def test_verifier_measurement_over_ceiling_fails(self):
        candidate = deepcopy(self.benchmark)
        summary = candidate["corpora"][0]["phases"]["warm_unchanged"]
        for sample in summary["samples"]:
            sample["wall_ms"] = 100_000
        summary["wall_ms"]["median"] = 100_000
        result = accept(self.profile, candidate, self.service)
        self.assertFalse(result["passed"])
        self.assertTrue(any("wall_ms" in failure["measurement"] for failure in result["failures"]))

    def test_warm_parsing_and_nonfinite_samples_reject(self):
        for key, value in (("parse", 1), ("lower", float("nan"))):
            candidate = deepcopy(self.benchmark)
            candidate["corpora"][0]["phases"]["warm_unchanged"]["samples"][0]["worker_ms"][key] = value
            with self.assertRaises(ValueError):
                accept(self.profile, candidate, self.service)


if __name__ == "__main__":
    unittest.main()
