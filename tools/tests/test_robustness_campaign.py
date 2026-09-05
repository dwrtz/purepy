"""Fail-closed evidence checks for bounded Go mutation campaigns."""

from argparse import Namespace
from contextlib import redirect_stderr, redirect_stdout
from copy import deepcopy
import hashlib
import io
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import threading
import unittest
from unittest.mock import Mock, patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import robustness_campaign as campaign


GOOD_LOG = """fuzz: elapsed: 0s, gathering baseline coverage: 0/2 completed
fuzz: elapsed: 0s, gathering baseline coverage: 2/2 completed, now fuzzing with 1 workers
fuzz: elapsed: 3s, execs: 15 (5/sec), new interesting: 1 (total: 3)
fuzz: elapsed: 10s, execs: 70 (8/sec), new interesting: 2 (total: 4)
PASS
ok  github.com/dwrtz/purepy/internal/check 10.012s
"""


def args(output):
    return Namespace(go="test-go", seconds=10, parallel=1, timeout_seconds=15,
                     minimize_seconds=2, output=output)


def completed(**changes):
    return {"started_at": "2026-09-05T00:00:00+00:00", "finished_at": "2026-09-05T00:00:10+00:00",
            "wall_seconds": 10.1, "exit_code": 0, "process_failure": None, **changes}


def metadata(command, **_kwargs):
    if command[:2] == ["git", "rev-parse"]:
        return "a" * 40
    if command[:2] == ["git", "status"]:
        return " M internal/check/flow.go"
    if command[1:] == ["version"]:
        return "go version go1.25.0 darwin/arm64"
    if len(command) > 1 and command[1] == "env":
        return json.dumps({"GOOS": "darwin", "GOARCH": "arm64", "CGO_ENABLED": "1", "CC": "clang"})
    raise AssertionError(f"unexpected metadata command {command}")


class RobustnessCampaignTests(unittest.TestCase):
    def run_target(self, log, process_result=None):
        with tempfile.TemporaryDirectory() as directory:
            settings = args(Path(directory))
            def run(command, logfile, timeout, environment, **_kwargs):
                self.assertEqual(timeout, 45)
                self.assertEqual(environment["GOMEMLIMIT"], "512MiB")
                self.assertIn("^FuzzCheckerSemantics$", command)
                logfile.write_bytes(log.encode())
                return process_result or completed()
            with patch.object(campaign, "run_command", side_effect=run), redirect_stdout(io.StringIO()):
                result = campaign.run_target("FuzzCheckerSemantics", settings, {"GOMEMLIMIT": "512MiB"})
            self.assertEqual(result["log_sha256"], hashlib.sha256(log.encode()).hexdigest())
            self.assertEqual((settings.output / result["log"]).read_text(), log)
            return result

    def test_complete_fuzz_log_retains_final_mutation_counts(self):
        evidence = campaign.parse_evidence(GOOD_LOG)
        self.assertEqual(evidence["executions"], 70)
        self.assertEqual(evidence["reported_elapsed"], "10s")
        self.assertEqual(evidence["new_interesting"], 2)
        self.assertEqual(evidence["total_interesting"], 4)
        result = self.run_target(GOOD_LOG)
        self.assertEqual(result["status"], "passed")
        self.assertEqual(result["errors"], [])
        self.assertIn("-fuzztime=10s", result["command"])
        self.assertIn("-timeout=15s", result["command"])
        self.assertIn("-fuzzminimizetime=2s", result["command"])

    def test_pass_without_completed_mutation_work_is_failure(self):
        logs = ["", "PASS\nok  github.com/dwrtz/purepy/internal/check 0.001s\n",
                GOOD_LOG.replace("PASS\n", ""),
                GOOD_LOG.replace("ok  github.com/dwrtz/purepy/internal/check 10.012s\n", ""),
                GOOD_LOG.replace("execs: 15", "execs: 0").replace("execs: 70", "execs: 0"),
                "fuzz: elapsed: 10s, gathering baseline coverage: 1/2 completed\nPASS\nok check 10s\n",
                # Progress alone cannot establish that baseline replay finished.
                "\n".join(GOOD_LOG.splitlines()[2:]) + "\n",
                # A successful process stopping early did not run its budget.
                GOOD_LOG.replace("elapsed: 10s", "elapsed: 1s").replace("elapsed: 3s", "elapsed: 0s")]
        for log in logs:
            with self.subTest(log=log):
                self.assertEqual(self.run_target(log)["status"], "failed")

    def test_failures_and_failing_inputs_override_pass_text(self):
        for process in (completed(exit_code=1), completed(exit_code=-9, process_failure="parent wall timeout"),
                        completed(exit_code=None, process_failure="process launch failed: missing go")):
            with self.subTest(process=process):
                self.assertEqual(self.run_target(GOOD_LOG, process)["status"], "failed")
        log = GOOD_LOG + "Failing input written to testdata/fuzz/FuzzCheckerSemantics/abc123\n"
        result = self.run_target(log)
        self.assertEqual(result["status"], "failed")
        self.assertEqual(result["failing_inputs"], ["testdata/fuzz/FuzzCheckerSemantics/abc123"])

    def test_timeout_kills_and_reaps_entire_process_group(self):
        process = Mock(pid=12345)
        process.wait.side_effect = [subprocess.TimeoutExpired("test-go", 2), -signal.SIGKILL]
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "timeout.log"
            with patch.object(campaign.subprocess, "Popen", return_value=process) as launch, patch.object(campaign.os, "killpg") as kill, patch.object(
                    campaign.time, "monotonic", side_effect=[0, 0, 0, 3, 4]):
                result = campaign.run_command(["test-go"], path, 2, {"GOMEMLIMIT": "512MiB"})
            kill.assert_called_once_with(12345, signal.SIGKILL)
            self.assertEqual(process.wait.call_count, 2)
            self.assertTrue(launch.call_args.kwargs["start_new_session"])
            self.assertIs(launch.call_args.kwargs["stderr"], subprocess.STDOUT)
            self.assertEqual(result["exit_code"], -signal.SIGKILL)
            self.assertEqual(result["process_failure"], "parent wall timeout")
            self.assertTrue(path.exists())

    def test_cancelled_campaign_skips_launch_and_stops_running_group(self):
        with tempfile.TemporaryDirectory() as directory:
            event = threading.Event()
            event.set()
            with patch.object(campaign.subprocess, "Popen") as launch:
                result = campaign.run_command(["test-go"], Path(directory) / "queued.log", 20, {}, cancelled=event)
            launch.assert_not_called()
            self.assertIn("interrupted before launch", result["process_failure"])
            event.clear()
            process = Mock(pid=23456)
            calls = 0
            def wait(**_kwargs):
                nonlocal calls
                calls += 1
                if calls == 1:
                    event.set()
                    raise subprocess.TimeoutExpired("test-go", 1)
                return -signal.SIGKILL
            process.wait.side_effect = wait
            with patch.object(campaign.subprocess, "Popen", return_value=process), patch.object(campaign.os, "killpg") as kill:
                result = campaign.run_command(["test-go"], Path(directory) / "active.log", 20, {}, cancelled=event)
            kill.assert_called_once_with(23456, signal.SIGKILL)
            self.assertEqual(calls, 2)
            self.assertEqual(result["process_failure"], "campaign interrupted")

    def test_unexpected_interrupt_reaps_child_before_propagating(self):
        process = Mock(pid=34567)
        process.poll.return_value = None
        process.wait.side_effect = [KeyboardInterrupt(), -signal.SIGKILL]
        with tempfile.TemporaryDirectory() as directory:
            with patch.object(campaign.subprocess, "Popen", return_value=process), patch.object(campaign.os, "killpg") as kill:
                with self.assertRaises(KeyboardInterrupt):
                    campaign.run_command(["test-go"], Path(directory) / "interrupted.log", 20, {})
            kill.assert_called_once_with(34567, signal.SIGKILL)
            self.assertEqual(process.wait.call_count, 2)

    def test_launch_failure_preserves_log(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "failure.log"
            with patch.object(campaign.subprocess, "Popen", side_effect=OSError("missing executable")):
                result = campaign.run_command(["test-go"], path, 2, {})
            self.assertIn("missing executable", result["process_failure"])
            self.assertIn("missing executable", path.read_text())
            self.assertIsNone(result["exit_code"])

    def test_actual_short_child_log_and_exit_are_recorded(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "child.log"
            result = campaign.run_command([sys.executable, "-c", "print('child evidence'); raise SystemExit(3)"],
                                          path, 2, dict(os.environ))
            self.assertEqual(result["exit_code"], 3)
            self.assertEqual(path.read_text(), "child evidence\n")
            self.assertIsNone(result["process_failure"])

    def test_source_identity_tracks_embedded_data_and_failing_seeds(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "go.mod").write_text("module test\n")
            (root / "go.sum").write_text("")
            inputs = ("internal/example.go", "internal/unicodeident/identifiers.bin",
                      "internal/unicodenames/names.bin", "internal/check/testdata/fuzz/FuzzCheckerSource/failure")
            for relative in inputs:
                path = root / relative
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(b"before")
            original = campaign.source_identity(root)
            self.assertEqual(original, campaign.source_identity(root))
            for relative in inputs:
                path = root / relative
                path.write_bytes(b"changed")
                with self.subTest(relative=relative):
                    self.assertNotEqual(original["sha256"], campaign.source_identity(root)["sha256"])
                path.write_bytes(b"before")
            (root / "README.md").write_text("non-Go documentation")
            self.assertEqual(original, campaign.source_identity(root))

    def main_run(self, directory, selected, result_status="passed", source_changed=False, runner_error=False, interrupted=False):
        output = Path(directory) / "campaign"
        snapshots = []
        real_write = campaign.write_report
        def save(path, report):
            snapshots.append(deepcopy(report))
            real_write(path, report)
        def target(name, settings, environment, cancelled=None):
            (settings.output / (name + ".log")).write_text("preserved target log\n")
            if interrupted:
                cancelled.set()
            if runner_error:
                raise ValueError("malformed target result")
            return {"target": name, "status": result_status, "errors": [] if result_status == "passed" else ["target failed"]}
        identity = {"sha256": "before", "file_count": 3, "scope": "test"}
        identities = [identity, {**identity, "sha256": "after"} if source_changed else identity]
        arguments = ["--seconds", "10", "--timeout-seconds", "15", "--minimize-seconds", "2", "--output", str(output)]
        for name in selected:
            arguments.extend(("--target", name))
        with patch.object(campaign, "source_identity", side_effect=identities), patch.object(
                campaign, "command_output", side_effect=metadata), patch.object(campaign, "run_target", side_effect=target), patch.object(
                campaign, "write_report", side_effect=save), redirect_stdout(io.StringIO()), redirect_stderr(io.StringIO()):
            code = campaign.main(arguments)
        return code, json.loads((output / "report.json").read_text()), snapshots, output

    def test_campaign_records_metadata_and_each_partial_completion(self):
        selected = ["FuzzCheckerSource", "FuzzCheckerSemantics"]
        with tempfile.TemporaryDirectory() as directory:
            code, report, snapshots, _output = self.main_run(directory, selected)
        self.assertEqual(code, 0)
        self.assertEqual(report["status"], "passed")
        self.assertEqual(report["selected_targets"], selected)
        self.assertEqual([result["target"] for result in report["results"]], selected)
        self.assertEqual([len(snapshot["results"]) for snapshot in snapshots], [0, 1, 2, 2])
        self.assertEqual(report["base_commit"], "a" * 40)
        self.assertEqual(report["go_environment"]["CGO_ENABLED"], "1")
        self.assertEqual(report["budgets"]["mutation_seconds"], 10)
        self.assertEqual(report["environment"]["GOMEMLIMIT"], "512MiB")
        self.assertTrue(report["source_unchanged"])
        self.assertIn("started_at", report)
        self.assertIn("finished_at", report)

    def test_default_selection_contains_every_target_once(self):
        with tempfile.TemporaryDirectory() as directory:
            code, report, _snapshots, _output = self.main_run(directory, [])
        self.assertEqual(code, 0)
        self.assertEqual(report["selected_targets"], list(campaign.TARGETS))
        self.assertEqual([record["target"] for record in report["results"]], list(campaign.TARGETS))

    def test_target_failure_or_source_change_cannot_pass_and_logs_survive(self):
        for options in ({"result_status": "failed"}, {"runner_error": True}, {"source_changed": True}, {"interrupted": True}):
            with self.subTest(options=options), tempfile.TemporaryDirectory() as directory:
                code, report, snapshots, output = self.main_run(directory, ["FuzzCheckerSource"], **options)
                self.assertEqual(code, 1)
                self.assertEqual(report["status"], "failed")
                self.assertEqual((output / "FuzzCheckerSource.log").read_text(), "preserved target log\n")
                self.assertEqual(snapshots[0]["status"], "running")
                self.assertEqual(len(report["results"]), 1)
                if options.get("runner_error"):
                    self.assertIn("runner failure", report["results"][0]["errors"][0])
                if options.get("interrupted"):
                    self.assertTrue(report["interrupted"])

    def test_campaign_restores_existing_signal_handlers(self):
        previous = {sig: signal.getsignal(sig) for sig in (signal.SIGINT, signal.SIGTERM)}
        with tempfile.TemporaryDirectory() as directory:
            code, _report, _snapshots, _output = self.main_run(directory, ["FuzzCheckerSource"], interrupted=True)
        self.assertEqual(code, 1)
        self.assertEqual({sig: signal.getsignal(sig) for sig in previous}, previous)

    def test_duplicate_unknown_targets_and_existing_evidence_are_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / "previous"
            output.mkdir()
            evidence = output / "report.json"
            evidence.write_bytes(b"previous evidence\n")
            arguments = (["--target", "FuzzCheckerSource", "--target", "FuzzCheckerSource"],
                         ["--target", "MissingFuzzer"], ["--output", str(output)])
            for argument in arguments:
                with self.subTest(arguments=argument), patch.object(campaign, "run_target") as run:
                    with redirect_stderr(io.StringIO()), self.assertRaises(SystemExit) as raised:
                        campaign.main(argument)
                    self.assertEqual(raised.exception.code, 2)
                    run.assert_not_called()
            self.assertEqual(evidence.read_bytes(), b"previous evidence\n")


if __name__ == "__main__":
    unittest.main()
