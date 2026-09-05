#!/usr/bin/env python3
"""Run bounded Go fuzz campaigns and preserve commands, logs and evidence.

This is a development runner, not a release certification or performance test.
Go's own failing corpus files remain in the packages' testdata/fuzz directories.
"""

from __future__ import annotations

import argparse
from concurrent.futures import ThreadPoolExecutor, as_completed
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import signal
import subprocess
import sys
import threading
import time


ROOT = Path(__file__).resolve().parents[1]
TARGETS = {
    "FuzzCheckerSemantics": "./internal/check",
    "FuzzCheckerSource": "./internal/check",
    "FuzzCacheSummary": "./internal/cache",
    "FuzzCacheArtifact": "./internal/cache",
    "FuzzCacheFallback": "./internal/app",
    "FuzzParseNeverPanics": "./internal/frontend",
    "FuzzTypeSyntax": "./internal/manifest",
    "FuzzManifestLoad": "./internal/manifest",
}


def now():
    return datetime.now(timezone.utc).isoformat(timespec="seconds")


def physical_memory():
    try:
        return os.sysconf("SC_PAGE_SIZE") * os.sysconf("SC_PHYS_PAGES")
    except (OSError, ValueError):
        return None


def command_output(command, root=ROOT):
    result = subprocess.run(command, cwd=root, capture_output=True, text=True, timeout=30)
    if result.returncode:
        raise ValueError(f"metadata command failed: {command}: {result.stderr.strip()}")
    return result.stdout.strip()


def source_identity(root=ROOT):
    """Fingerprint actual Go inputs, including unstaged tests and failing seeds."""
    files = [root / "go.mod", root / "go.sum"]
    for directory in ("cmd", "internal", "tools/semantic_probe"):
        files.extend(path for path in (root / directory).rglob("*") if path.is_file())
    digest = hashlib.sha256()
    for path in sorted(set(files)):
        relative = path.relative_to(root).as_posix().encode()
        content = path.read_bytes()
        digest.update(len(relative).to_bytes(8, "big"))
        digest.update(relative)
        digest.update(len(content).to_bytes(8, "big"))
        digest.update(content)
    return {"sha256": digest.hexdigest(), "file_count": len(set(files)),
            "scope": "go.mod, go.sum and all files beneath cmd, internal, tools/semantic_probe (including embedded data and testdata)"}


def write_report(path, report):
    temporary = path.with_suffix(".tmp")
    temporary.write_text(json.dumps(report, indent=2, allow_nan=False) + "\n", encoding="utf-8")
    temporary.replace(path)


def parse_evidence(log):
    progress = re.findall(r"fuzz: elapsed: ([0-9hms.]+), execs: (\d+).*?new interesting: (\d+) \(total: (\d+)\)", log)
    seeds = re.findall(r"Failing input written to (\S+)", log)
    baselines = re.findall(r"gathering baseline coverage: (\d+)/(\d+) completed", log)
    workers = re.findall(r"now fuzzing with (\d+) workers", log)
    phases = {"baseline_completed": any(int(done) == int(total) for done, total in baselines),
              "mutation_workers": int(workers[-1]) if workers else None}
    if not progress:
        return {"executions": None, "reported_elapsed": None, "new_interesting": None,
                "total_interesting": None, "failing_inputs": seeds, **phases}
    elapsed, executions, interesting, total = progress[-1]
    return {"executions": int(executions), "reported_elapsed": elapsed,
            "new_interesting": int(interesting), "total_interesting": int(total),
            "failing_inputs": seeds, **phases}


def elapsed_seconds(value):
    if value is None:
        return None
    parts = re.fullmatch(r"(?:(\d+)h)?(?:(\d+)m)?(\d+(?:\.\d+)?)s", value)
    if not parts:
        return None
    hours, minutes, seconds = parts.groups()
    return int(hours or 0) * 3600 + int(minutes or 0) * 60 + float(seconds)


def run_command(command, logfile, timeout, environment, root=ROOT, cancelled=None):
    """A parent wall timeout kills the process group, including fuzz workers."""
    started = now()
    begin = time.monotonic()
    failure = None
    returncode = None
    cancelled = cancelled or threading.Event()
    with logfile.open("wb") as output:
        try:
            if cancelled.is_set():
                return {"started_at": started, "finished_at": now(), "wall_seconds": 0,
                        "exit_code": None, "process_failure": "campaign interrupted before launch"}
            process = subprocess.Popen(command, cwd=root, env=environment,
                                       stdout=output, stderr=subprocess.STDOUT,
                                       start_new_session=True)
            try:
                deadline = begin + timeout
                while True:
                    if cancelled.is_set() or time.monotonic() >= deadline:
                        failure = "campaign interrupted" if cancelled.is_set() else "parent wall timeout"
                        os.killpg(process.pid, signal.SIGKILL)
                        returncode = process.wait()
                        break
                    try:
                        returncode = process.wait(timeout=min(1, max(0.001, deadline - time.monotonic())))
                        break
                    except subprocess.TimeoutExpired:
                        continue
            except BaseException:
                if process.poll() is None:
                    os.killpg(process.pid, signal.SIGKILL)
                process.wait()
                raise
        except OSError as error:
            failure = f"process launch failed: {error}"
            output.write((failure + "\n").encode())
    return {"started_at": started, "finished_at": now(),
            "wall_seconds": round(time.monotonic() - begin, 3),
            "exit_code": returncode, "process_failure": failure}


def run_target(name, args, environment, cancelled=None):
    package = TARGETS[name]
    command = [args.go, "test", package, "-run", "^$", "-fuzz", f"^{name}$",
               f"-fuzztime={args.seconds}s", f"-parallel={args.parallel}",
               f"-timeout={args.timeout_seconds}s", f"-fuzzminimizetime={args.minimize_seconds}s"]
    logfile = args.output / f"{name}.log"
    print(f"START {name}: {args.seconds}s mutation budget, {args.parallel} worker(s)", flush=True)
    result = run_command(command, logfile, args.timeout_seconds + 30, environment, cancelled=cancelled)
    data = logfile.read_bytes()
    log = data.decode("utf-8", errors="replace")
    evidence = parse_evidence(log)
    errors = []
    if result["process_failure"]:
        errors.append(result["process_failure"])
    if result["exit_code"] != 0:
        errors.append(f"Go exited with status {result['exit_code']}")
    if not re.search(r"(?m)^PASS\s*$", log) or not re.search(r"(?m)^ok\s", log):
        errors.append("missing Go PASS/package completion")
    if evidence["executions"] is None or evidence["executions"] <= 0:
        errors.append("missing mutation-execution evidence")
    if not evidence["baseline_completed"] or evidence["mutation_workers"] != args.parallel:
        errors.append("missing completed baseline or expected mutation-worker startup")
    elapsed = elapsed_seconds(evidence["reported_elapsed"])
    if elapsed is None or elapsed < args.seconds - 1:
        errors.append("reported fuzz duration is shorter than the requested budget")
    if evidence["failing_inputs"]:
        errors.append("Go reported failing inputs")
    record = {"target": name, "package": package, "command": command,
              "log": logfile.name, "log_sha256": hashlib.sha256(data).hexdigest(),
              **result, **evidence, "status": "failed" if errors else "passed", "errors": errors}
    print(f"{record['status'].upper()} {name}: executions={evidence['executions']}, wall={result['wall_seconds']}s", flush=True)
    return record


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--go", default="go")
    parser.add_argument("--seconds", type=int, default=600, help="mutation seconds per target")
    parser.add_argument("--timeout-seconds", type=int, default=720, help="Go timeout per target, including seed replay")
    parser.add_argument("--minimize-seconds", type=int, default=30)
    parser.add_argument("--parallel", type=int, default=1, help="mutation workers per target")
    parser.add_argument("--concurrent-targets", type=int, default=1)
    parser.add_argument("--memory", default="512MiB", help="soft Go memory target per process")
    parser.add_argument("--target", action="append", choices=TARGETS, help="repeat to select a subset")
    parser.add_argument("--output", type=Path, default=ROOT / "build/robustness-campaign")
    args = parser.parse_args(argv)
    if not 1 <= args.seconds <= 86400 or args.timeout_seconds <= args.seconds:
        parser.error("mutation budget must be 1..86400 seconds and timeout must exceed it")
    if not 1 <= args.minimize_seconds <= args.timeout_seconds - args.seconds:
        parser.error("minimization must fit inside timeout headroom")
    if not 1 <= args.parallel <= 8 or not 1 <= args.concurrent_targets <= 8 or args.parallel * args.concurrent_targets > 8:
        parser.error("use 1..8 workers/targets with at most 8 concurrent mutation workers")
    if not re.fullmatch(r"[1-9][0-9]*(?:KiB|MiB|GiB)", args.memory):
        parser.error("memory must be a positive KiB, MiB, or GiB quantity")
    selected = args.target or list(TARGETS)
    if len(set(selected)) != len(selected):
        parser.error("duplicate target")
    args.output = args.output.resolve()
    if args.output.exists() and (not args.output.is_dir() or any(args.output.iterdir())):
        parser.error("output directory must be new or empty to preserve prior evidence")
    try:
        args.output.mkdir(parents=True, exist_ok=True)
        identity = source_identity()
        environment = dict(os.environ, GOMEMLIMIT=args.memory)
        report = {
            "schema": 1, "kind": "go_fuzz_campaign", "status": "running", "started_at": now(),
            "base_commit": command_output(["git", "rev-parse", "HEAD"]),
            "git_status": command_output(["git", "status", "--short"]),
            "source_before": identity, "go_version": command_output([args.go, "version"]),
            "go_environment": json.loads(command_output([args.go, "env", "-json", "GOOS", "GOARCH", "CGO_ENABLED", "CC", "GOFLAGS", "GOEXPERIMENT"])),
            "platform": platform.platform(), "machine": platform.machine(), "logical_cpus": os.cpu_count(),
            "physical_memory_bytes": physical_memory(),
            "budgets": {"mutation_seconds": args.seconds, "go_timeout_seconds": args.timeout_seconds,
                        "parent_timeout_seconds": args.timeout_seconds + 30, "minimize_seconds": args.minimize_seconds,
                        "workers_per_target": args.parallel, "concurrent_targets": args.concurrent_targets,
                        "go_memory_soft_target": args.memory},
            "environment": {key: environment.get(key) for key in ("GOCACHE", "GOMAXPROCS", "GOMEMLIMIT", "GOGC", "GODEBUG")},
            "selected_targets": selected, "results": [],
            "limits": ["Finite local mutation evidence, not exhaustive conformance or a soundness proof.",
                       "GOMEMLIMIT is a soft Go GC target; C parser allocations are not covered.",
                       "Concurrent targets contend for this machine; execution counts are not performance benchmarks."]}
        destination = args.output / "report.json"
        write_report(destination, report)
        records = {}
        cancelled = threading.Event()
        previous_handlers = {sig: signal.getsignal(sig) for sig in (signal.SIGINT, signal.SIGTERM)}
        def interrupt(_signal, _frame):
            cancelled.set()
        for sig in previous_handlers:
            signal.signal(sig, interrupt)
        try:
            with ThreadPoolExecutor(max_workers=args.concurrent_targets) as executor:
                futures = {executor.submit(run_target, name, args, environment, cancelled): name for name in selected}
                for future in as_completed(futures):
                    name = futures[future]
                    try:
                        records[name] = future.result()
                    except Exception as error:
                        records[name] = {"target": name, "status": "failed", "errors": [f"runner failure: {type(error).__name__}: {error}"]}
                    report["results"] = [records[target] for target in selected if target in records]
                    write_report(destination, report)
        finally:
            for sig, handler in previous_handlers.items():
                signal.signal(sig, handler)
        report["source_after"] = source_identity()
        report["source_unchanged"] = report["source_before"] == report["source_after"]
        report["finished_at"] = now()
        report["interrupted"] = cancelled.is_set()
        report["status"] = "passed" if not report["interrupted"] and report["source_unchanged"] and all(record["status"] == "passed" for record in records.values()) else "failed"
        write_report(destination, report)
        print(f"{report['status'].upper()}: {len(records)} targets; report={destination}", flush=True)
        return 0 if report["status"] == "passed" else 1
    except (OSError, ValueError, subprocess.TimeoutExpired) as error:
        print(f"robustness campaign failed: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
