"""The performance harness must reject broken measurements and cache equivalence."""

from contextlib import redirect_stderr, redirect_stdout
import argparse
import errno
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import benchmark


FAKE_VERIFIER = r'''
import hashlib, json, os
from pathlib import Path
import sys

if sys.argv[1] == "version":
    print("purepy benchmark fake")
    sys.exit(0)
project = Path(sys.argv[2])
no_cache = "--no-cache" in sys.argv
jobs = int(sys.argv[sys.argv.index("--jobs") + 1])
fault = os.environ.get("PUREPY_BENCHMARK_TEST_FAULT", "")
paths = sorted((project / "src").rglob("*.py"))
source = {path.relative_to(project).as_posix(): path.read_text() for path in paths}
manifest = "".join(path.read_text() for path in sorted((project / "manifests").glob("*.toml")))
invalid = sum(value.count("missing_call") for value in source.values())
edited = any("-> str:" in value for value in source.values()) or 'returns = "str"' in manifest
fingerprints = {key: hashlib.sha256((value + manifest).encode()).hexdigest() for key, value in source.items()}
cache = project / ".purepy-cache" / "fake.json"
previous = json.loads(cache.read_text()) if cache.exists() else {}
hits = 0 if no_cache else sum(previous.get(key) == value for key, value in fingerprints.items())
if not no_cache:
    cache.parent.mkdir(exist_ok=True)
    cache.write_text(json.dumps(fingerprints))
diagnostics = [{"code": "PP204", "n": number} for number in range(invalid)]
if edited and fault != "skip_invalidation":
    diagnostics.append({"code": "PP205", "message": "changed declaration"})
report = {"ok": not diagnostics, "files": len(paths), "functions": [], "diagnostics": diagnostics}
if fault == "workers":
    report["jobs"] = jobs
if fault == "cache_json":
    report["cached"] = not no_cache
if fault == "bad_hits" and not no_cache:
    hits = len(paths)
print(json.dumps(report, sort_keys=True))
print("timings_schema 2", file=sys.stderr)
for stage in ("configuration", "discovery", "manifests", "image_inputs", "frontend", "link", "check", "report", "render"):
    print(f"wall_{stage} 0.000001s", file=sys.stderr)
for stage in ("read", "hash", "cache_read", "parse", "lower", "cache_write"):
    print(f"work_{stage} 0.000002s", file=sys.stderr)
print(f"cache_hits {hits}/{len(paths)}", file=sys.stderr)
sys.exit(0 if report["ok"] else 1)
'''


def timing_text():
    return "\n".join(["timings_schema 2", "cache_hits 1/3"]
                     + [f"wall_{name} 0.001s" for name in sorted(benchmark.WALL_STAGES)]
                     + [f"work_{name} 0.1s" for name in sorted(benchmark.WORKER_STAGES)])


class BenchmarkTests(unittest.TestCase):
    def setUp(self):
        self.scratch = tempfile.TemporaryDirectory(prefix="purepy-benchmark-test-")
        self.addCleanup(self.scratch.cleanup)
        self.root = Path(self.scratch.name)
        self.binary = self.root / "fake-purepy"
        self.binary.write_text(f"#!{sys.executable}\n" + FAKE_VERIFIER)
        self.binary.chmod(0o755)

    def project(self, kind="wide", modules=2):
        project = self.root / kind
        project.mkdir()
        mutations = benchmark.generate(project, kind, modules, 2)
        return project, mutations

    def test_generic_processor_requires_explicit_hardware_label(self):
        with patch.object(benchmark.platform, "system", return_value="Darwin"), \
             patch.object(benchmark.platform, "processor", return_value="arm"), \
             patch.object(benchmark.subprocess, "run", return_value=subprocess.CompletedProcess([], 1, "", "not permitted")):
            for label in (None, "", " ARM ", "unknown"):
                with self.assertRaisesRegex(RuntimeError, "--hardware"):
                    benchmark.hardware(label)
            metadata = benchmark.hardware("Apple M4")
            self.assertEqual(metadata["label"], "Apple M4")
            self.assertEqual(metadata["processor"], "arm")

    def test_wait4_measures_each_child_without_inheriting_previous_peak(self):
        large, high = benchmark.run_process([sys.executable, "-c", "x = bytearray(96 * 1024 * 1024); print(len(x))"], 10)
        small, low = benchmark.run_process([sys.executable, "-c", "print('small')"], 10)
        self.assertEqual(large.stdout.strip(), str(96 * 1024 * 1024))
        self.assertEqual(small.stdout.strip(), "small")
        self.assertGreater(high["peak_rss_bytes"], low["peak_rss_bytes"] + 48 * 1024 * 1024)
        self.assertGreater(low["process_cpu_ms"], 0)
        self.assertGreater(low["wall_ms"], 0)

    def test_runtime_environment_records_unset_and_explicit_values_only(self):
        with patch.dict(os.environ, {}, clear=True):
            self.assertEqual(benchmark.runtime_environment(), {
                "GOGC": None, "GOMEMLIMIT": None, "GOMAXPROCS": None, "GODEBUG": None})
        controls = {"GOGC": "off", "GOMEMLIMIT": "512MiB", "GOMAXPROCS": "2", "GODEBUG": ""}
        with patch.dict(os.environ, {**controls, "UNRELATED_TEST_VALUE": "not recorded"}, clear=True):
            self.assertEqual(benchmark.runtime_environment(), controls)

    def test_child_peak_does_not_include_live_parent_allocation(self):
        command = [sys.executable, "-c", "pass"]
        _, before = benchmark.run_process(command, 10)
        allocation = bytearray(64 * 1024 * 1024)
        for offset in range(0, len(allocation), 4096):
            allocation[offset] = 1
        _, during = benchmark.run_process(command, 10)
        self.assertLess(during["peak_rss_bytes"], before["peak_rss_bytes"] + 32 * 1024 * 1024)

    def test_timeout_kills_and_reaps_the_exact_child(self):
        pid_file = self.root / "pid"
        code = "import os,time; from pathlib import Path; Path(" + repr(str(pid_file)) + ").write_text(str(os.getpid())); time.sleep(60)"
        with self.assertRaises(subprocess.TimeoutExpired):
            benchmark.run_process([sys.executable, "-c", code], 0.5)
        self.assertTrue(pid_file.exists())
        with self.assertRaises(ProcessLookupError):
            os.kill(int(pid_file.read_text()), 0)
        with self.assertRaises(ChildProcessError):
            os.waitpid(int(pid_file.read_text()), os.WNOHANG)

    def test_failed_exec_preserves_errno_and_command(self):
        missing = self.root / "missing-command"
        with self.assertRaises(FileNotFoundError) as raised:
            benchmark.run_process([str(missing)], 10)
        self.assertEqual(raised.exception.errno, errno.ENOENT)
        self.assertEqual(raised.exception.filename, str(missing))
        denied = self.root / "not-executable"
        denied.write_text("not executable\n")
        with self.assertRaises(PermissionError) as raised:
            benchmark.run_process([str(denied)], 10)
        self.assertEqual(raised.exception.errno, errno.EACCES)

    def test_nonzero_and_signal_exits_preserve_child_status(self):
        command = [sys.executable, "-c", "import sys; print('output'); print('error', file=sys.stderr); sys.exit(7)"]
        completed, sample = benchmark.run_process(command, 10)
        self.assertEqual((completed.returncode, completed.stdout, completed.stderr), (7, "output\n", "error\n"))
        self.assertGreater(sample["peak_rss_bytes"], 0)
        completed, _ = benchmark.run_process([sys.executable, "-c", "import os,signal; os.kill(os.getpid(), signal.SIGTERM)"], 10)
        self.assertEqual(completed.returncode, -15)

    def test_linux_supervisor_metrics_are_independent_and_fail_closed(self):
        valid = {"schema": 1, "status": 0, "wall_ms": 20.0, "process_cpu_ms": 10.0,
                 "peak_rss_bytes": 12 * 1024 * 1024, "timed_out": False, "exec_errno": None}

        def collect(record):
            def invoke(command, timeout, **options):
                self.assertEqual(command[1:3], ["-I", "-S"])
                self.assertEqual(timeout, 15)
                self.assertTrue(options["new_session"])
                os.write(options["pass_fds"][0], json.dumps(record).encode())
                return subprocess.CompletedProcess(command, 0, "child stdout", "child stderr"), {
                    "wall_ms": 999, "process_cpu_ms": 999, "peak_rss_bytes": 999 * 1024 * 1024}
            return invoke

        with patch.object(benchmark.platform, "system", return_value="Linux"), patch.object(
                benchmark, "_direct_process", side_effect=collect(valid)):
            result, sample = benchmark.run_process(["verifier"], 10)
        self.assertEqual((result.args, result.stdout, result.stderr), (["verifier"], "child stdout", "child stderr"))
        self.assertEqual(sample, {"wall_ms": 20.0, "process_cpu_ms": 10.0,
                                  "peak_rss_bytes": 12 * 1024 * 1024, "mean_cpu_percent": 50.0})
        for record in (None, {}, {**valid, "schema": True}, {**valid, "status": 65536},
                       {**valid, "wall_ms": 0}, {**valid, "process_cpu_ms": float("nan")},
                       {**valid, "peak_rss_bytes": 0}, {**valid, "exec_errno": True}):
            with self.subTest(record=record), patch.object(benchmark.platform, "system", return_value="Linux"), patch.object(
                    benchmark, "_direct_process", side_effect=collect(record)), self.assertRaisesRegex(RuntimeError, "malformed metrics"):
                benchmark.run_process(["verifier"], 10)

    def test_linux_and_darwin_collection_methods_are_distinct(self):
        self.assertNotEqual(benchmark.LINUX_METHOD, benchmark.DIRECT_METHOD)
        self.assertIn("clean -I -S supervisor", benchmark.LINUX_METHOD)
        self.assertEqual(benchmark.DIRECT_METHOD,
                         "isolated verifier process; wait4 per-process resource usage; temporary input; no project execution")
        with patch.object(benchmark.platform, "system", return_value="Darwin"), patch.object(
                benchmark, "_direct_process", return_value=("direct", {})) as direct:
            self.assertEqual(benchmark.run_process(["verifier"], 10), ("direct", {}))
        direct.assert_called_once_with(["verifier"], 10)

    def test_large_stdout_and_stderr_do_not_deadlock(self):
        result, _ = benchmark.run_process([sys.executable, "-c", "import sys; print('x' * 200000); print('y' * 200000, file=sys.stderr)"], 10)
        self.assertEqual(len(result.stdout), 200001)
        self.assertEqual(len(result.stderr), 200001)

    def test_timing_schema_rejects_missing_duplicate_negative_and_unknown_fields(self):
        text = timing_text()
        stages, workers, hits = benchmark.parse_timings(text)
        self.assertEqual(stages["frontend"], 1)
        self.assertEqual(workers["parse"], 100)
        self.assertEqual(hits, (1, 3))
        for corrupt in (text.replace("timings_schema 2", "timings_schema 1"),
                        text.replace("wall_frontend 0.001s\n", ""),
                        text + "\nwall_frontend 0.001s",
                        text.replace("0.001s", "-0.001s", 1),
                        text.replace("0.001s", "nans", 1),
                        text.replace("cache_hits 1/3", "cache_hits 4/3"),
                        text + "\nunknown 1s"):
            with self.subTest(corrupt=corrupt), self.assertRaises(RuntimeError):
                benchmark.parse_timings(corrupt)

    def test_worker_elapsed_is_not_subtracted_from_wall(self):
        result = subprocess.CompletedProcess([], 0, json.dumps({"ok": True, "files": 3, "functions": [], "diagnostics": []}), timing_text())
        with patch.object(benchmark, "run_process", return_value=(result, {"wall_ms": 50, "process_cpu_ms": 20,
                                                                         "mean_cpu_percent": 40, "peak_rss_bytes": 1024})):
            _, _, sample = benchmark.invoke(self.binary, self.root, 2)
        self.assertEqual(sample["unattributed_wall_ms"], 50 - len(benchmark.WALL_STAGES))
        self.assertEqual(sum(sample["worker_ms"].values()), 600)

    def test_bad_exit_json_success_and_cache_protocol_fail(self):
        variants = ((2, "{}", timing_text()), (0, "not json", timing_text()),
                    (0, '{"ok": false,"files": 3}', timing_text()),
                    (0, '{"ok": 1,"files": 3}', timing_text()),
                    (0, '{"ok": true,"files": 4}', timing_text()))
        for code, stdout, stderr in variants:
            result = subprocess.CompletedProcess([], code, stdout, stderr)
            with self.subTest(code=code, stdout=stdout), patch.object(benchmark, "run_process", return_value=(result, {"wall_ms": 5})), self.assertRaises(RuntimeError):
                benchmark.invoke(self.binary, self.root, 1)
        good = subprocess.CompletedProcess([], 0, '{"ok": true,"files": 3,"functions": [],"diagnostics": []}', timing_text())
        with patch.object(benchmark, "run_process", return_value=(good, {"wall_ms": 5})), self.assertRaisesRegex(RuntimeError, "--no-cache"):
            benchmark.invoke(self.binary, self.root, 1, no_cache=True)

    def test_manifest_matrix_repeats_and_hashes_are_independent_of_temporary_paths(self):
        output = self.root / "result.json"
        args = ["--verifier", str(self.binary), "--corpora", "manifest,invalid", "--sizes", "2,1,2",
                "--workers", "2,1,2", "--functions", "1", "--repeat", "2", "--output", str(output)]
        with redirect_stderr(io.StringIO()), patch.object(benchmark, "hardware", return_value={"label": "fake"}), patch.object(benchmark, "build_settings", return_value={"toolchain": "test"}):
            report = benchmark.main(args)
        self.assertEqual(json.loads(output.read_text()), report)
        self.assertEqual(report["schema"], 2)
        self.assertEqual(report["runtime_environment"], benchmark.runtime_environment())
        self.assertEqual(report["matrix"]["sizes"], [1, 2])
        self.assertEqual(report["matrix"]["workers"], [1, 2])
        self.assertEqual(len(report["corpora"]), 8)
        by_identity = {}
        for corpus in report["corpora"]:
            key = corpus["corpus"], corpus["modules"]
            previous = by_identity.setdefault(key, corpus)
            self.assertEqual(previous["corpus_sha256"], corpus["corpus_sha256"])
            self.assertEqual(previous["baseline_report_sha256"], corpus["baseline_report_sha256"])
            self.assertEqual(previous["changed_report_sha256"], corpus["changed_report_sha256"])
            self.assertEqual(previous["changed_input_sha256"], corpus["changed_input_sha256"])
            self.assertTrue(all(value != corpus["corpus_sha256"] for value in corpus["changed_input_sha256"].values()))
            self.assertTrue(all(corpus["equivalence"].values()))
            self.assertEqual(set(corpus["phases"]), {"uncached", "cold_cache_population", "warm_unchanged", "one_changed_declaration"}
                             | ({"one_changed_manifest"} if key[0] == "manifest" else set()))
            for phase in corpus["phases"].values():
                self.assertEqual(len(phase["samples"]), 2)
                self.assertGreater(phase["median_peak_rss_bytes"], 0)
            files = corpus["source_files"]
            self.assertEqual(corpus["phases"]["one_changed_declaration"]["cache_hits"], [files - 1] * 2)
            if key[0] == "manifest":
                self.assertEqual(corpus["phases"]["one_changed_manifest"]["cache_hits"], [0] * 2)
                self.assertGreater(corpus["manifest_bytes"], 0)
            else:
                self.assertEqual(corpus["baseline_diagnostics"], corpus["modules"])
        other = self.root / "another-location"
        other.mkdir()
        benchmark.generate(other, "manifest", 1, 1)
        self.assertEqual(benchmark.input_identity(other)["corpus_sha256"], by_identity["manifest", 1]["corpus_sha256"])
        write_cache = other / ".purepy-cache" / "ignored"
        benchmark.write(write_cache, "not input")
        self.assertEqual(benchmark.input_identity(other)["corpus_sha256"], by_identity["manifest", 1]["corpus_sha256"])

    def test_faults_fail_and_restore_edited_source(self):
        project, mutations = self.project()
        original = next(iter(mutations.values()))[0].read_text()
        for fault, message in (("workers", "worker counts"), ("cache_json", "JSON differs"),
                               ("bad_hits", "cache-hit"), ("skip_invalidation", "declaration edit")):
            with self.subTest(fault=fault), patch.dict(os.environ, {"PUREPY_BENCHMARK_TEST_FAULT": fault}), self.assertRaisesRegex(RuntimeError, message):
                benchmark.measure(self.binary, project, "wide", mutations, 1, 2, 10)
            self.assertEqual(next(iter(mutations.values()))[0].read_text(), original)

    def test_legacy_options_and_fixed_corpora_do_not_duplicate_sizes(self):
        with redirect_stderr(io.StringIO()), redirect_stdout(io.StringIO()), patch.object(benchmark, "hardware", return_value={}), patch.object(benchmark, "build_settings", return_value=None):
            legacy = benchmark.main(["--verifier", str(self.binary), "--corpus", "tiny", "--modules", "2", "--jobs", "1", "--repeat", "1"])
            matrix = benchmark.main(["--verifier", str(self.binary), "--corpora", "tiny", "--sizes", "2,4", "--workers", "1,2", "--repeat", "1"])
        self.assertEqual(len(legacy["corpora"]), 1)
        self.assertEqual(legacy["corpora"][0]["modules"], 1)
        self.assertEqual(len(matrix["corpora"]), 2)
        self.assertEqual(benchmark.positive_list("4,1,4"), [1, 4])
        for invalid in ("", "1,", "0,1", "nan"):
            with self.subTest(value=invalid), self.assertRaises(argparse.ArgumentTypeError):
                benchmark.positive_list(invalid)

    def test_build_settings_ignore_revision_but_keep_comparable_flags(self):
        metadata = "binary: go1.24.1\n\tpath\tgithub.com/dwrtz/purepy/cmd/purepy\n\tbuild\t-buildmode=exe\n\tbuild\t-trimpath=true\n\tbuild\tGOOS=darwin\n\tbuild\tGOARCH=arm64\n\tbuild\tvcs=git\n\tbuild\tvcs.revision=abc\n\tbuild\tvcs.modified=true\n"
        with patch.object(benchmark.subprocess, "run", return_value=subprocess.CompletedProcess([], 0, metadata, "")):
            settings = benchmark.build_settings(self.binary, 10)
        self.assertEqual(settings, {"toolchain": "go1.24.1", "-buildmode": "exe", "-trimpath": "true", "GOOS": "darwin", "GOARCH": "arm64"})


if __name__ == "__main__":
    unittest.main()
