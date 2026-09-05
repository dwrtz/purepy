#!/usr/bin/env python3
"""Measure verifier stages, scaling, memory, and cache correctness on temporary input.

Run `make benchmark` or use --sizes 100,400 --workers 1,4 --corpora wide,manifest.
Legacy --modules, --jobs, and --corpus remain single-value alternatives.
Project code and host implementations are never imported or executed.
"""

import argparse
from datetime import datetime, timezone
import hashlib
import json
import math
import os
from pathlib import Path
import platform
import shutil
import signal
import statistics
import subprocess
import sys
import tempfile
import time


REPOSITORY = Path(__file__).resolve().parents[1]
BENCHMARK_SCHEMA = 2
RUNTIME_VARIABLES = ("GOGC", "GOMEMLIMIT", "GOMAXPROCS", "GODEBUG")
METHOD = "isolated verifier process; wait4 per-process resource usage; temporary input; no project execution"
CORPORA = ("tiny", "deep", "wide", "invalid", "manifest", "reference")
GENERIC_PROCESSORS = {"", "unknown", "arm", "arm64", "aarch64", "x86_64", "amd64", "i386", "i686"}
WALL_STAGES = {"configuration", "discovery", "manifests", "image_inputs", "frontend",
               "link", "check", "report", "render"}
WORKER_STAGES = {"read", "hash", "cache_read", "parse", "lower", "cache_write"}


def write(path, content):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content, encoding="utf-8")


def generate(project, kind, modules, functions):
    """Generate source and return independently reversible declaration mutations."""
    count = 1 if kind == "tiny" else modules
    package = project / "src" / "bench"
    write(package / "__init__.py", '"""Generated benchmark package; never executed."""\n')
    manifest = ["schema = 1", ""]
    for number in range(count):
        identifier = f"{number:05d}"
        lines = []
        if kind == "deep" and number > 0:
            previous = f"{number - 1:05d}"
            lines += [f"from bench.m{previous} import step_{previous}", ""]
            body = f"step_{previous}(value) + 1"
        elif kind == "manifest":
            external = f"host.m{identifier}"
            lines += [f"from {external} import " + ", ".join(f"external_{i}" for i in range(functions)), ""]
            manifest += ["[[module]]", f'name = "{external}"', "import_safe = true", ""]
            for function in range(functions):
                manifest += ["[[function]]", f'name = "{external}.external_{function}"',
                             'kind = "sync"', 'trust = "pure"',
                             'parameters = [{ name = "value", type = "int" }]',
                             'returns = "int"', ""]
            body = "external_0(value)"
        else:
            body = "value + 1"
        lines += [f"def step_{identifier}(value: int) -> int:"]
        if kind == "invalid":
            lines += ["    broken = missing_call(value)", '    mixed = value + "invalid"']
        lines += [f"    return {body}", ""]
        for function in range(1, functions):
            lines += [f"def extra_{identifier}_{function}(value: int) -> int:"]
            if kind == "invalid":
                lines += ["    broken = missing_call(value)", '    mixed = value + "invalid"']
            value = f"external_{function}(value)" if kind == "manifest" else f"value + {function}"
            lines += [f"    return {value}", ""]
        write(package / f"m{identifier}.py", "\n".join(lines) + "\n")
    if kind in ("wide", "manifest"):
        lines = [f"from bench.m{number:05d} import step_{number:05d}" for number in range(count)]
        lines += ["", "def run(value: int) -> int:", "    result = 0"]
        lines += [f"    result = result + step_{number:05d}(value)" for number in range(count)]
        lines += ["    return result", ""]
    else:
        final = f"{count - 1:05d}"
        lines = [f"from bench.m{final} import step_{final}", "",
                 "def run(value: int) -> int:", f"    return step_{final}(value)", ""]
    write(package / "entry.py", "\n".join(lines))
    manifests = '["manifests/host.purepy.toml"]' if kind == "manifest" else "[]"
    write(project / "purepy.toml", f'''[tool.purepy]
language = "0.1"
python_syntax = "3.14"
source_root = "src"
entrypoints = ["bench.entry.run"]
manifests = {manifests}
''')
    mutations = {"one_changed_declaration": (package / "m00000.py", "-> int:", "-> str:", 1)}
    if kind == "manifest":
        target = project / "manifests" / "host.purepy.toml"
        write(target, "\n".join(manifest))
        # Manifest bytes enter the shared image key: every parse summary misses.
        mutations["one_changed_manifest"] = (target, 'returns = "int"', 'returns = "str"', None)
    return mutations


def copy_reference(project):
    reference = REPOSITORY / "examples" / "reference_service"
    shutil.copytree(reference / "src", project / "src", ignore=shutil.ignore_patterns("__pycache__"))
    shutil.copytree(reference / "manifests", project / "manifests")
    shutil.copyfile(reference / "purepy.toml", project / "purepy.toml")
    return {"one_changed_declaration": (project / "src" / "app" / "domain.py", "-> bool:", "-> int:", 1)}


def input_identity(project):
    """Hash framed relative paths and bytes, independent of temporary directory names."""
    paths = [path for path in project.rglob("*") if path.is_file()
             and ".purepy-cache" not in path.relative_to(project).parts
             and "__pycache__" not in path.relative_to(project).parts]
    digest = hashlib.sha256()
    sizes = {"source_bytes": 0, "manifest_bytes": 0, "input_bytes": 0}
    for path in sorted(paths, key=lambda item: item.relative_to(project).as_posix()):
        relative = path.relative_to(project).as_posix()
        content = path.read_bytes()
        digest.update(relative.encode("utf-8") + b"\0" + str(len(content)).encode("ascii") + b"\0" + content)
        sizes["input_bytes"] += len(content)
        if path.suffix == ".py":
            sizes["source_bytes"] += len(content)
        elif relative.startswith("manifests/"):
            sizes["manifest_bytes"] += len(content)
    return {"corpus_sha256": digest.hexdigest(), **sizes}


def hardware(label):
    processor = platform.processor() or platform.machine()
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

    if processor.strip().lower() in GENERIC_PROCESSORS and (label or "").strip().lower() in GENERIC_PROCESSORS:
        raise RuntimeError("CPU model detection is unavailable; provide --hardware with an explicit machine label")
    return {"label": label or processor, "processor": processor,
            "platform": platform.platform(), "machine": platform.machine(),
            "logical_cpus": os.cpu_count(), "memory_bytes": memory,
            "harness_python": platform.python_version()}


def runtime_environment():
    """Record only Go runtime controls that the verifier inherits from this process."""
    return {name: os.environ.get(name) for name in RUNTIME_VARIABLES}


def build_settings(binary, timeout):
    """Separate comparable toolchain/build settings from revision-specific identity."""
    try:
        result = subprocess.run(["go", "version", "-m", str(binary)], capture_output=True,
                                text=True, timeout=timeout, check=True)
    except (OSError, subprocess.SubprocessError):
        return None
    lines = result.stdout.splitlines()
    if not lines or ": " not in lines[0]:
        return None
    settings = {"toolchain": lines[0].rsplit(": ", 1)[1].strip()}
    for line in lines[1:]:
        fields = line.strip().split(None, 1)
        if len(fields) == 2 and fields[0] == "build":
            key, separator, value = fields[1].partition("=")
            if separator and key != "vcs" and not key.startswith("vcs."):
                settings[key] = value
    return settings


def run_process(arguments, timeout):
    """Own and reap exactly one child, recording its wait4 CPU and peak RSS.

    Disk-backed output avoids pipe deadlocks. Popen.poll/wait/send_signal are not
    used: they could reap this child before wait4 can return its resource usage.
    Every timeout or interrupted wait kills and reaps the child before returning.
    """
    if not hasattr(os, "wait4"):
        raise RuntimeError("benchmark resource measurement requires macOS or Linux wait4")
    with tempfile.TemporaryFile() as stdout, tempfile.TemporaryFile() as stderr:
        start = time.perf_counter()
        process = subprocess.Popen(arguments, stdout=stdout, stderr=stderr)
        try:
            while True:
                child, status, usage = os.wait4(process.pid, os.WNOHANG)
                if child:
                    process.returncode = os.waitstatus_to_exitcode(status)
                    break
                remaining = timeout - (time.perf_counter() - start)
                if remaining <= 0:
                    raise subprocess.TimeoutExpired(arguments, timeout)
                time.sleep(min(0.001, remaining))
        except BaseException:
            try:
                os.kill(process.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            _, status, _ = os.wait4(process.pid, 0)
            process.returncode = os.waitstatus_to_exitcode(status)
            raise
        elapsed = (time.perf_counter() - start) * 1000
        stdout.seek(0)
        stderr.seek(0)
        completed = subprocess.CompletedProcess(arguments, process.returncode,
                                                stdout.read().decode("utf-8"), stderr.read().decode("utf-8"))
    cpu_ms = (usage.ru_utime + usage.ru_stime) * 1000
    rss = usage.ru_maxrss if platform.system() == "Darwin" else usage.ru_maxrss * 1024
    return completed, {"wall_ms": round(elapsed, 6), "process_cpu_ms": round(cpu_ms, 6),
                       "mean_cpu_percent": round(cpu_ms / elapsed * 100, 3),
                       "peak_rss_bytes": rss}


def parse_timings(stderr):
    stages, workers = {}, {}
    hits = None
    version = None
    for line in stderr.splitlines():
        parts = line.split()
        if len(parts) != 2:
            raise RuntimeError(f"unexpected verifier stderr: {line}")
        name, value = parts
        try:
            if name == "timings_schema" and version is None:
                version = int(value)
            elif name == "cache_hits" and hits is None:
                hits = tuple(int(item) for item in value.split("/"))
                if len(hits) != 2 or not 0 <= hits[0] <= hits[1]:
                    raise ValueError("invalid cache hit count")
            elif name.startswith(("wall_", "work_")) and value.endswith("s"):
                target = stages if name.startswith("wall_") else workers
                key = name[5:]
                duration = float(value[:-1]) * 1000
                if key in target or not math.isfinite(duration) or duration < 0:
                    raise ValueError("invalid or duplicate duration")
                target[key] = round(duration, 6)
            else:
                raise ValueError("unexpected timing field")
        except ValueError as error:
            raise RuntimeError(f"invalid verifier timing: {line}") from error
    if version != 2 or hits is None or set(stages) != WALL_STAGES or set(workers) != WORKER_STAGES:
        raise RuntimeError("the verifier must emit timings_schema 2 and all wall/work/cache fields")
    return stages, workers, hits


def invoke(binary, project, jobs, no_cache=False, timeout=120):
    arguments = [str(binary), "check", str(project), "--format", "json",
                 "--jobs", str(jobs), "--timings"]
    if no_cache:
        arguments.append("--no-cache")
    process, sample = run_process(arguments, timeout)
    if process.returncode not in (0, 1):
        raise RuntimeError(f"verifier failed with exit {process.returncode}: {process.stderr}{process.stdout}")
    try:
        report = json.loads(process.stdout)
    except json.JSONDecodeError as error:
        raise RuntimeError(f"verifier emitted invalid JSON: {process.stdout[:1000]}") from error
    if not isinstance(report, dict) or type(report.get("ok")) is not bool:
        raise RuntimeError("verifier JSON must contain a boolean ok")
    if type(report.get("files")) is not int or report["files"] < 0 or not isinstance(report.get("functions"), list) or not isinstance(report.get("diagnostics"), list):
        raise RuntimeError("verifier JSON must contain a nonnegative file count and function/diagnostic lists")
    if report["ok"] != (process.returncode == 0):
        raise RuntimeError("verifier JSON success and process exit status disagree")
    stages, workers, hits = parse_timings(process.stderr)
    if report.get("files") != hits[1] or no_cache and hits[0] != 0:
        raise RuntimeError("verifier timing cache counts disagree with the report or --no-cache")
    sample.update({"stage_ms": stages, "worker_ms": workers,
                   "unattributed_wall_ms": round(sample["wall_ms"] - sum(stages.values()), 6),
                   "cache_hits": hits[0], "files": hits[1]})
    return process.stdout, report, sample


def summary(samples):
    def median(key):
        return round(statistics.median(sample[key] for sample in samples), 3)
    return {
        "wall_ms": {"min": round(min(sample["wall_ms"] for sample in samples), 3),
                    "median": median("wall_ms"),
                    "max": round(max(sample["wall_ms"] for sample in samples), 3)},
        "median_process_cpu_ms": median("process_cpu_ms"),
        "median_mean_cpu_percent": median("mean_cpu_percent"),
        "median_peak_rss_bytes": statistics.median(sample["peak_rss_bytes"] for sample in samples),
        "median_stage_ms": {stage: round(statistics.median(sample["stage_ms"][stage] for sample in samples), 3)
                            for stage in sorted(WALL_STAGES)},
        "median_worker_ms": {stage: round(statistics.median(sample["worker_ms"][stage] for sample in samples), 3)
                             for stage in sorted(WORKER_STAGES)},
        "median_unattributed_wall_ms": median("unattributed_wall_ms"),
        "cache_hits": [sample["cache_hits"] for sample in samples], "samples": samples,
    }


def measure(binary, project, kind, mutations, repeats, jobs, timeout, baseline=None):
    identity = input_identity(project)
    originals = {name: target.read_text(encoding="utf-8") for name, (target, _, _, _) in mutations.items()}
    for name, (target, old, _, _) in mutations.items():
        if old not in originals[name]:
            raise RuntimeError(f"mutation target missing in {target}")
    if baseline is None:
        baseline, _, _ = invoke(binary, project, 1, True, timeout)
    first = json.loads(baseline)
    if first["ok"] is not (kind != "invalid"):
        raise RuntimeError(f"{kind}: unexpected baseline outcome: {first['diagnostics'][:3]}")
    phases = {name: [] for name in ("uncached", "cold_cache_population", "warm_unchanged", *mutations)}
    changed_diagnostics, changed_hashes, changed_input_hashes = {}, {}, {}
    cache_bytes = 0
    try:
        for _ in range(repeats):
            uncached, _, sample = invoke(binary, project, jobs, True, timeout)
            if uncached != baseline:
                raise RuntimeError(f"{kind}: worker counts or repeated uncached JSON differ")
            phases["uncached"].append(sample)
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
            phases["cold_cache_population"].append(cold_sample)
            phases["warm_unchanged"].append(warm_sample)
            for index, (name, (target, old, new, expected_misses)) in enumerate(mutations.items()):
                if index:
                    restored, _, _ = invoke(binary, project, jobs, False, timeout)
                    if restored != baseline:
                        raise RuntimeError(f"{kind}: restoring an edit changed the baseline")
                target.write_text(originals[name].replace(old, new, 1), encoding="utf-8")
                try:
                    input_hash = input_identity(project)["corpus_sha256"]
                    if name in changed_input_hashes and changed_input_hashes[name] != input_hash:
                        raise RuntimeError(f"{kind}/{name}: repeated edited input differs")
                    changed_input_hashes[name] = input_hash
                    changed, changed_report, changed_sample = invoke(binary, project, jobs, False, timeout)
                    changed_fresh, _, _ = invoke(binary, project, jobs, True, timeout)
                    if changed != changed_fresh:
                        raise RuntimeError(f"{kind}/{name}: edited cached output differs from a fresh check")
                    if changed == baseline or changed_report["ok"] or changed_report["diagnostics"] == first["diagnostics"]:
                        raise RuntimeError(f"{kind}/{name}: declaration edit did not change rejection diagnostics")
                    expected_hits = 0 if expected_misses is None else first["files"] - expected_misses
                    if changed_sample["cache_hits"] != expected_hits:
                        raise RuntimeError(f"{kind}/{name}: incorrect parse-summary invalidation count")
                    digest = hashlib.sha256(changed.encode("utf-8")).hexdigest()
                    if name in changed_hashes and changed_hashes[name] != digest:
                        raise RuntimeError(f"{kind}/{name}: repeated edited JSON differs")
                    changed_hashes[name] = digest
                    changed_diagnostics[name] = len(changed_report["diagnostics"])
                    phases[name].append(changed_sample)
                finally:
                    target.write_text(originals[name], encoding="utf-8")
    finally:
        for name, (target, _, _, _) in mutations.items():
            target.write_text(originals[name], encoding="utf-8")
    return {
        "corpus": kind, **identity, "source_files": first["files"],
        "functions": len(first["functions"]), "baseline_diagnostics": len(first["diagnostics"]),
        "changed_diagnostics": changed_diagnostics, "cache_bytes_after_warm": cache_bytes,
        "jobs": jobs, "repeats": repeats,
        "baseline_report_sha256": hashlib.sha256(baseline.encode("utf-8")).hexdigest(),
        "changed_report_sha256": changed_hashes, "changed_input_sha256": changed_input_hashes,
        "equivalence": {"workers": True, "repeated_uncached": True, "cold_warm_uncached": True,
                        "edited_cached_uncached": True, "declaration_invalidation": True},
        "phases": {name: summary(samples) for name, samples in phases.items()},
    }


def positive_list(value):
    try:
        values = sorted({int(item) for item in value.split(",")})
    except ValueError as error:
        raise argparse.ArgumentTypeError("expected comma-separated positive integers") from error
    if not values or min(values) < 1:
        raise argparse.ArgumentTypeError("expected comma-separated positive integers")
    return values


def corpus_list(value):
    if value == "all":
        return sorted(CORPORA)
    values = sorted(set(value.split(",")))
    if not values or any(item not in CORPORA for item in values):
        raise argparse.ArgumentTypeError("expected all or comma-separated names: " + ",".join(CORPORA))
    return values


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--verifier", type=Path, default=REPOSITORY / "bin" / "purepy")
    corpus = parser.add_mutually_exclusive_group()
    corpus.add_argument("--corpus", choices=("all",) + CORPORA, default="all")
    corpus.add_argument("--corpora", type=corpus_list)
    sizes = parser.add_mutually_exclusive_group()
    sizes.add_argument("--modules", type=int, default=100)
    sizes.add_argument("--sizes", type=positive_list)
    workers = parser.add_mutually_exclusive_group()
    workers.add_argument("--jobs", type=int, default=min(4, os.cpu_count() or 1))
    workers.add_argument("--workers", type=positive_list)
    parser.add_argument("--functions", type=int, default=10, help="functions per generated leaf module")
    parser.add_argument("--repeat", type=int, default=3)
    parser.add_argument("--timeout", type=float, default=120, help="seconds per verifier invocation")
    parser.add_argument("--hardware", help="optional explicit machine label")
    parser.add_argument("--output", type=Path, help="write JSON here instead of stdout")
    args = parser.parse_args(argv)
    if min(args.modules, args.functions, args.repeat, args.jobs) < 1 or not math.isfinite(args.timeout) or args.timeout <= 0:
        parser.error("counts and timeout must be positive and finite")
    binary = args.verifier.resolve()
    if not binary.is_file():
        parser.error(f"built verifier not found: {binary}; run make build first")
    selected = args.corpora or corpus_list(args.corpus)
    sizes = args.sizes or [args.modules]
    workers = args.workers or [args.jobs]
    startup, version = [], ""
    for _ in range(args.repeat):
        process, sample = run_process([str(binary), "version"], args.timeout)
        if process.returncode != 0:
            raise RuntimeError(f"verifier version failed: {process.stderr}")
        if version and process.stdout.strip() != version:
            raise RuntimeError("verifier version changed across repeated invocations")
        version = process.stdout.strip()
        startup.append(sample)
    report = {
        "schema": BENCHMARK_SCHEMA, "measured_at_utc": datetime.now(timezone.utc).isoformat(),
        "hardware": hardware(args.hardware), "verifier": version,
        "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
        "build_settings": build_settings(binary, args.timeout),
        "runtime_environment": runtime_environment(),
        "version_process_samples": startup, "method": METHOD,
        "matrix": {"sizes": sizes, "workers": workers, "corpora": selected,
                   "functions_per_module": args.functions, "repeats": args.repeat,
                   "timeout_seconds": args.timeout}, "corpora": [],
    }
    with tempfile.TemporaryDirectory(prefix="purepy-benchmark-") as scratch:
        for kind in selected:
            for modules in ([1] if kind == "tiny" else [None] if kind == "reference" else sizes):
                project = Path(scratch) / f"{kind}-{modules}"
                project.mkdir()
                mutations = copy_reference(project) if kind == "reference" else generate(
                    project, kind, modules, args.functions)
                baseline, _, _ = invoke(binary, project, 1, True, args.timeout)
                edited_hashes = None
                for jobs in workers:
                    print(f"Measuring {kind} (modules {modules}, {args.repeat} repeats, jobs {jobs})",
                          file=sys.stderr, flush=True)
                    result = measure(binary, project, kind, mutations, args.repeat, jobs, args.timeout, baseline)
                    if edited_hashes is not None and edited_hashes != (result["changed_report_sha256"], result["changed_input_sha256"]):
                        raise RuntimeError(f"{kind}: edited reports differ across worker counts")
                    edited_hashes = (result["changed_report_sha256"], result["changed_input_sha256"])
                    report["corpora"].append({"modules": modules, **result})
    rendered = json.dumps(report, indent=2, sort_keys=True) + "\n"
    if args.output:
        args.output.write_text(rendered, encoding="utf-8")
        print(f"Wrote {args.output}", file=sys.stderr)
    else:
        print(rendered, end="")
    return report


if __name__ == "__main__":
    try:
        main()
    except (RuntimeError, OSError, subprocess.SubprocessError) as error:
        print(f"benchmark failed: {error}", file=sys.stderr)
        sys.exit(1)
