#!/usr/bin/env python3
"""Benchmark a built PurePy verifier without importing or executing project code.

Run from the repository root:
    .venv/bin/python tools/benchmark.py --verifier bin/purepy --output /tmp/purepy-benchmark.json
Only generated temporary copies are edited or have their caches removed.
"""

import argparse
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import platform
import resource
import shutil
import statistics
import subprocess
import sys
import tempfile
import time


REPOSITORY = Path(__file__).resolve().parents[1]
CORPORA = ("tiny", "deep", "wide", "invalid", "reference")


def write(path, content):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content, encoding="utf-8")


def generate(project, kind, modules, functions):
    """Return the module and replacement used for a declaration invalidation check."""
    count = 1 if kind == "tiny" else modules
    package = project / "src" / "bench"
    write(package / "__init__.py", '"""Generated benchmark package; never executed."""\n')
    for number in range(count):
        identifier = f"{number:05d}"
        lines = []
        if kind == "deep" and number > 0:
            previous = f"{number - 1:05d}"
            lines += [f"from bench.m{previous} import step_{previous}", ""]
            body = f"step_{previous}(value) + 1"
        else:
            body = "missing_call(value)" if kind == "invalid" else "value + 1"
        lines += [f"def step_{identifier}(value: int) -> int:", f"    return {body}", ""]
        for function in range(1, functions):
            lines += [f"def extra_{identifier}_{function}(value: int) -> int:",
                      f"    return value + {function}", ""]
        write(package / f"m{identifier}.py", "\n".join(lines) + "\n")
    if kind == "wide":
        lines = [f"from bench.m{number:05d} import step_{number:05d}" for number in range(count)]
        lines += ["", "def run(value: int) -> int:", "    result = 0"]
        lines += [f"    result = result + step_{number:05d}(value)" for number in range(count)]
        lines += ["    return result", ""]
    else:
        final = f"{count - 1:05d}"
        lines = [f"from bench.m{final} import step_{final}", "",
                 "def run(value: int) -> int:", f"    return step_{final}(value)", ""]
    write(package / "entry.py", "\n".join(lines))
    write(project / "purepy.toml", """[tool.purepy]
language = "0.1"
python_syntax = "3.14"
source_root = "src"
entrypoints = ["bench.entry.run"]
manifests = []
""")
    return package / "m00000.py", "-> int:", "-> str:"


def copy_reference(project):
    reference = REPOSITORY / "examples" / "reference_service"
    # Host implementations are deliberately absent. Verification needs declarations.
    shutil.copytree(reference / "src", project / "src", ignore=shutil.ignore_patterns("__pycache__"))
    shutil.copytree(reference / "manifests", project / "manifests")
    shutil.copyfile(reference / "purepy.toml", project / "purepy.toml")
    return project / "src" / "app" / "domain.py", "-> bool:", "-> int:"


def hardware(label):
    processor = platform.processor() or platform.machine()
    if not label:
        if platform.system() == "Darwin":
            found = subprocess.run(["sysctl", "-n", "machdep.cpu.brand_string"], capture_output=True, text=True)
            if found.returncode == 0:
                processor = found.stdout.strip()
        elif platform.system() == "Linux":
            try:
                for line in Path("/proc/cpuinfo").read_text().splitlines():
                    if line.startswith("model name"):
                        processor = line.partition(":")[2].strip()
                        break
            except OSError:
                pass
    try:
        memory = os.sysconf("SC_PAGE_SIZE") * os.sysconf("SC_PHYS_PAGES")
    except (OSError, ValueError):
        memory = None
    return {"label": label or processor, "processor": processor,
            "platform": platform.platform(), "logical_cpus": os.cpu_count(),
            "memory_bytes": memory, "harness_python": platform.python_version()}


def invoke(binary, project, jobs, no_cache=False, timeout=120):
    arguments = [str(binary), "check", str(project), "--format", "json",
                 "--jobs", str(jobs), "--timings"]
    if no_cache:
        arguments.append("--no-cache")
    before = resource.getrusage(resource.RUSAGE_CHILDREN)
    start = time.perf_counter()
    process = subprocess.run(arguments, capture_output=True, text=True, timeout=timeout)
    elapsed = (time.perf_counter() - start) * 1000
    after = resource.getrusage(resource.RUSAGE_CHILDREN)
    if process.returncode not in (0, 1):
        raise RuntimeError(f"verifier failed with exit {process.returncode}: {process.stderr}{process.stdout}")
    try:
        report = json.loads(process.stdout)
    except json.JSONDecodeError as error:
        raise RuntimeError(f"verifier emitted invalid JSON: {process.stdout[:1000]}") from error
    if bool(report.get("ok")) != (process.returncode == 0):
        raise RuntimeError("verifier JSON success and process exit status disagree")
    stages = {}
    hits = None
    for line in process.stderr.splitlines():
        parts = line.split()
        if len(parts) == 2 and parts[0] == "cache_hits":
            hits = tuple(int(value) for value in parts[1].split("/"))
        elif len(parts) == 2 and parts[1].endswith("s"):
            stages[parts[0]] = round(float(parts[1][:-1]) * 1000, 6)
        elif line:
            raise RuntimeError(f"unexpected verifier stderr: {line}")
    if hits is None or not stages:
        raise RuntimeError("the verifier did not emit --timings stage and cache data")
    cpu_ms = ((after.ru_utime + after.ru_stime) - (before.ru_utime + before.ru_stime)) * 1000
    sample = {
        "wall_ms": round(elapsed, 6),
        "process_cpu_ms": round(cpu_ms, 6),
        "mean_cpu_percent": round(cpu_ms / elapsed * 100, 3),
        "stage_ms": stages,
        "unattributed_wall_ms": round(elapsed - sum(stages.values()), 6),
        "cache_hits": hits[0], "files": hits[1],
    }
    return process.stdout, report, sample


def summary(samples):
    stages = sorted(set().union(*(sample["stage_ms"] for sample in samples)))
    return {
        "wall_ms": {"min": round(min(sample["wall_ms"] for sample in samples), 3),
                    "median": round(statistics.median(sample["wall_ms"] for sample in samples), 3),
                    "max": round(max(sample["wall_ms"] for sample in samples), 3)},
        "median_process_cpu_ms": round(statistics.median(sample["process_cpu_ms"] for sample in samples), 3),
        "median_mean_cpu_percent": round(statistics.median(sample["mean_cpu_percent"] for sample in samples), 3),
        "median_stage_ms": {stage: round(statistics.median(sample["stage_ms"].get(stage, 0) for sample in samples), 3)
                            for stage in stages},
        "median_unattributed_wall_ms": round(statistics.median(sample["unattributed_wall_ms"] for sample in samples), 3),
        "cache_hits": [sample["cache_hits"] for sample in samples],
        "samples": samples,
    }


def measure(binary, project, kind, mutation, repeats, jobs, timeout):
    target, old, new = mutation
    original = target.read_text(encoding="utf-8")
    if old not in original:
        raise RuntimeError(f"mutation target missing in {target}")
    expected_ok = kind != "invalid"
    baseline, first, serial = invoke(binary, project, 1, True, timeout)
    parallel_output, _, parallel = invoke(binary, project, jobs, True, timeout)
    if baseline != parallel_output:
        raise RuntimeError(f"{kind}: --jobs 1 and --jobs {jobs} differ")
    if first["ok"] is not expected_ok:
        raise RuntimeError(f"{kind}: unexpected baseline outcome: {first['diagnostics'][:3]}")
    phases = {"cold_cache_population": [], "warm_unchanged": [], "one_changed_declaration": []}
    cache_bytes = 0
    changed_diagnostics = 0
    for _ in range(repeats):
        target.write_text(original, encoding="utf-8")
        cache = project / ".purepy-cache"
        if cache.exists():
            shutil.rmtree(cache)
        cold, _, cold_sample = invoke(binary, project, jobs, False, timeout)
        warm, _, warm_sample = invoke(binary, project, jobs, False, timeout)
        if cold != baseline or warm != baseline:
            raise RuntimeError(f"{kind}: cached, uncached, cold, or warm JSON differs")
        if cold_sample["cache_hits"] != 0 or warm_sample["cache_hits"] != first["files"]:
            raise RuntimeError(f"{kind}: unexpected cold/warm cache-hit counts")
        cache_bytes = sum(path.stat().st_size for path in cache.rglob("*") if path.is_file())
        target.write_text(original.replace(old, new, 1), encoding="utf-8")
        changed, changed_report, changed_sample = invoke(binary, project, jobs, False, timeout)
        changed_fresh, _, _ = invoke(binary, project, jobs, True, timeout)
        if changed != changed_fresh:
            raise RuntimeError(f"{kind}: edited cached output differs from a fresh check")
        if changed == baseline or changed_report["ok"]:
            raise RuntimeError(f"{kind}: changed declaration did not invalidate the accepted signature")
        if changed_sample["cache_hits"] != first["files"] - 1:
            raise RuntimeError(f"{kind}: a one-file edit did not invalidate exactly one parse summary")
        changed_diagnostics = len(changed_report["diagnostics"])
        phases["cold_cache_population"].append(cold_sample)
        phases["warm_unchanged"].append(warm_sample)
        phases["one_changed_declaration"].append(changed_sample)
    target.write_text(original, encoding="utf-8")
    source = list((project / "src").rglob("*.py"))
    return {
        "corpus": kind, "source_files": first["files"],
        "source_bytes": sum(path.stat().st_size for path in source),
        "functions": len(first["functions"]), "baseline_diagnostics": len(first["diagnostics"]),
        "changed_declaration_diagnostics": changed_diagnostics,
        "cache_bytes_after_warm": cache_bytes,
        "jobs": jobs, "repeats": repeats,
        "equivalence": {"jobs_1_vs_selected": True, "cold_warm_uncached": True,
                        "changed_cached_vs_uncached": True},
        "uncached_jobs_comparison": {"jobs_1": serial, "selected_jobs": {"jobs": jobs, **parallel}},
        "phases": {name: summary(samples) for name, samples in phases.items()},
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--verifier", type=Path, default=REPOSITORY / "bin" / "purepy")
    parser.add_argument("--corpus", choices=("all",) + CORPORA, default="all")
    parser.add_argument("--modules", type=int, default=100)
    parser.add_argument("--functions", type=int, default=10, help="functions per generated leaf module")
    parser.add_argument("--repeat", type=int, default=3)
    parser.add_argument("--jobs", type=int, default=min(4, os.cpu_count() or 1))
    parser.add_argument("--timeout", type=float, default=120, help="seconds per verifier invocation")
    parser.add_argument("--hardware", help="optional explicit machine label")
    parser.add_argument("--output", type=Path, help="write JSON here instead of stdout")
    args = parser.parse_args()
    if min(args.modules, args.functions, args.repeat, args.jobs) < 1 or args.timeout <= 0:
        parser.error("counts and timeout must be positive")
    binary = args.verifier.resolve()
    if not binary.is_file():
        parser.error(f"built verifier not found: {binary}; run make build first")
    machine = hardware(args.hardware)
    startup = []
    version = ""
    for _ in range(args.repeat):
        start = time.perf_counter()
        process = subprocess.run([str(binary), "version"], capture_output=True, text=True,
                                 check=True, timeout=args.timeout)
        startup.append(round((time.perf_counter() - start) * 1000, 3))
        version = process.stdout.strip()
    report = {
        "schema": 1, "measured_at_utc": datetime.now(timezone.utc).isoformat(),
        "hardware": machine, "verifier": version,
        "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
        "version_process_wall_ms": startup,
        "method": "fresh child process per check; generated or copied temporary source; no project execution",
        "corpora": [],
    }
    selected = CORPORA if args.corpus == "all" else (args.corpus,)
    with tempfile.TemporaryDirectory(prefix="purepy-benchmark-") as scratch:
        for kind in selected:
            print(f"Measuring {kind} ({args.repeat} repeats, jobs {args.jobs})", file=sys.stderr, flush=True)
            project = Path(scratch) / kind
            project.mkdir()
            mutation = copy_reference(project) if kind == "reference" else generate(
                project, kind, args.modules, args.functions)
            report["corpora"].append(measure(binary, project, kind, mutation, args.repeat, args.jobs, args.timeout))
    peak = resource.getrusage(resource.RUSAGE_CHILDREN).ru_maxrss
    report["aggregate_child_peak_rss_bytes"] = peak if platform.system() == "Darwin" else peak * 1024
    rendered = json.dumps(report, indent=2, sort_keys=True) + "\n"
    if args.output:
        args.output.write_text(rendered, encoding="utf-8")
        print(f"Wrote {args.output}", file=sys.stderr)
    else:
        print(rendered, end="")


if __name__ == "__main__":
    main()
