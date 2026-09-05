#!/usr/bin/env python3
"""Compare schema-2 benchmark matrices measured with the same method and hardware.

Budgets allow a relative increase plus an absolute noise allowance. They are
regression tolerances, not universal latency or memory targets. The optional
incompatibility override emits exploratory results and always exits nonzero.
"""

import argparse
from dataclasses import asdict, dataclass
import json
import math
from pathlib import Path
import statistics
import sys


METRICS = {
    "wall_ms": ("wall_ms", "median"),
    "process_cpu_ms": ("median_process_cpu_ms",),
    "peak_rss_bytes": ("median_peak_rss_bytes",),
}
PHASES = {"uncached", "cold_cache_population", "warm_unchanged", "one_changed_declaration"}
EQUIVALENCE = {"workers", "repeated_uncached", "cold_warm_uncached",
               "edited_cached_uncached", "declaration_invalidation"}
CORPORA = {"tiny", "deep", "wide", "invalid", "manifest", "reference"}
RUNTIME_VARIABLES = {"GOGC", "GOMEMLIMIT", "GOMAXPROCS", "GODEBUG"}
GENERIC_PROCESSORS = {"", "unknown", "arm", "arm64", "aarch64", "x86_64", "amd64", "i386", "i686"}


@dataclass(frozen=True)
class Budgets:
    relative: float = 0.50
    wall_ms: float = 25.0
    process_cpu_ms: float = 50.0
    peak_rss_bytes: float = 16 * 1024 * 1024
    growth_relative: float = 0.25

    def validate(self):
        for name, value in asdict(self).items():
            number(value, f"budget {name}")


def number(value, name, *, positive=False):
    if isinstance(value, bool) or not isinstance(value, (int, float)) or not math.isfinite(value):
        raise ValueError(f"{name} must be a finite number")
    if value < 0 or (positive and value == 0):
        raise ValueError(f"{name} must be {'positive' if positive else 'nonnegative'}")
    return value


def digest(value, name):
    if not isinstance(value, str) or len(value) != 64 or any(c not in "0123456789abcdef" for c in value):
        raise ValueError(f"{name} must be a SHA-256 digest")
    return value


def metric(summary, name):
    value = summary
    for part in METRICS[name]:
        value = value[part]
    return number(value, name)


def identity(corpus):
    return corpus["corpus"], corpus["modules"], corpus["jobs"]


def label(key, phase, metric_name):
    kind, size, jobs = key
    return f"{kind}/modules={size}/jobs={jobs}/{phase}/{metric_name}"


def validate(report, name):
    """Fail closed on incomplete matrices or unverified equality measurements."""
    if report.get("schema") != 2:
        raise ValueError(f"{name}: benchmark schema 2 is required")
    for field in ("hardware", "method", "matrix", "build_settings"):
        if not report.get(field):
            raise ValueError(f"{name}: missing {field}")
    for field in ("hardware", "matrix", "build_settings"):
        if not isinstance(report[field], dict):
            raise ValueError(f"{name}: {field} must be an object")
    if not isinstance(report["method"], str):
        raise ValueError(f"{name}: method must be a string")
    runtime = report.get("runtime_environment")
    if not isinstance(runtime, dict) or set(runtime) != RUNTIME_VARIABLES:
        raise ValueError(f"{name}: runtime_environment must record every Go runtime control")
    if any(value is not None and not isinstance(value, str) for value in runtime.values()):
        raise ValueError(f"{name}: runtime_environment values must be strings or null when unset")
    machine = report["hardware"]
    for field in ("processor", "platform", "machine", "harness_python"):
        if not isinstance(machine.get(field), str) or not machine[field].strip():
            raise ValueError(f"{name}: hardware {field} must be a nonempty string")
    if machine["processor"].strip().lower() in GENERIC_PROCESSORS:
        if not isinstance(machine.get("label"), str) or machine["label"].strip().lower() in GENERIC_PROCESSORS:
            raise ValueError(f"{name}: generic processor metadata needs an explicit hardware label")
    if type(machine.get("logical_cpus")) is not int or machine["logical_cpus"] <= 0:
        raise ValueError(f"{name}: logical_cpus must be a positive integer")
    if machine.get("memory_bytes") is not None:
        number(machine["memory_bytes"], f"{name} hardware memory_bytes", positive=True)
    for field in ("toolchain", "GOARCH", "GOOS"):
        if not isinstance(report["build_settings"].get(field), str) or not report["build_settings"][field].strip():
            raise ValueError(f"{name}: build setting {field} must be a nonempty string")
    digest(report.get("binary_sha256"), f"{name} binary identity")
    matrix = report["matrix"]
    for field in ("sizes", "workers", "corpora"):
        values = matrix.get(field)
        if not isinstance(values, list) or not values or len(values) != len(set(values)):
            raise ValueError(f"{name}: {field} must be a nonempty unique matrix axis")
    for field in ("sizes", "workers"):
        if any(type(value) is not int or value <= 0 for value in matrix[field]):
            raise ValueError(f"{name}: {field} must contain positive integers")
    if not set(matrix["corpora"]) <= CORPORA:
        raise ValueError(f"{name}: unknown corpus")
    if type(matrix.get("repeats")) is not int or matrix["repeats"] <= 0:
        raise ValueError(f"{name}: repeats must be a positive integer")
    if type(matrix.get("functions_per_module")) is not int or matrix["functions_per_module"] <= 0:
        raise ValueError(f"{name}: functions_per_module must be a positive integer")
    number(matrix.get("timeout_seconds"), f"{name} timeout_seconds", positive=True)
    found = {}
    worker_identities = {}
    for corpus in report.get("corpora", []):
        key = identity(corpus)
        if key in found:
            raise ValueError(f"{name}: duplicate corpus matrix entry {key}")
        found[key] = corpus
        if corpus.get("repeats") != matrix["repeats"]:
            raise ValueError(f"{name}: {key} repeat count disagrees with the matrix")
        digest(corpus.get("corpus_sha256"), f"{name}: {key} corpus identity")
        digest(corpus.get("baseline_report_sha256"), f"{name}: {key} baseline report identity")
        if type(corpus.get("source_files")) is not int or corpus["source_files"] <= 0:
            raise ValueError(f"{name}: {key} source_files must be a positive integer")
        number(corpus.get("source_bytes"), "source_bytes", positive=True)
        number(corpus.get("input_bytes"), "input_bytes", positive=True)
        if corpus["input_bytes"] < corpus["source_bytes"]:
            raise ValueError(f"{name}: {key} input_bytes cannot be less than source_bytes")
        equality = corpus.get("equivalence")
        if not isinstance(equality, dict) or set(equality) != EQUIVALENCE or any(value is not True for value in equality.values()):
            raise ValueError(f"{name}: {key} has no successful report-equivalence checks")
        expected_phases = PHASES | ({"one_changed_manifest"} if key[0] == "manifest" else set())
        if set(corpus.get("phases", {})) != expected_phases:
            raise ValueError(f"{name}: {key} has missing or unexpected phases")
        edited = corpus.get("changed_report_sha256")
        edited_phases = {"one_changed_declaration"} | ({"one_changed_manifest"} if key[0] == "manifest" else set())
        if not isinstance(edited, dict) or set(edited) != edited_phases:
            raise ValueError(f"{name}: {key} has missing or unexpected edited report identities")
        for phase, value in edited.items():
            digest(value, f"{name}: {key}/{phase} report identity")
            if value == corpus["baseline_report_sha256"]:
                raise ValueError(f"{name}: {key}/{phase} edit did not change the report")
        changed_inputs = corpus.get("changed_input_sha256")
        if not isinstance(changed_inputs, dict) or set(changed_inputs) != edited_phases:
            raise ValueError(f"{name}: {key} has missing or unexpected edited input identities")
        for phase, value in changed_inputs.items():
            digest(value, f"{name}: {key}/{phase} input identity")
            if value == corpus["corpus_sha256"]:
                raise ValueError(f"{name}: {key}/{phase} edit did not change the input")
        shared = {field: corpus[field] for field in ("corpus_sha256", "baseline_report_sha256",
                  "changed_report_sha256", "changed_input_sha256", "source_files", "source_bytes", "input_bytes")}
        group = key[:2]
        if group in worker_identities and worker_identities[group] != shared:
            raise ValueError(f"{name}: {group} inputs or reports differ across worker counts")
        worker_identities[group] = shared
        for phase, summary in corpus["phases"].items():
            samples = summary.get("samples")
            if not isinstance(samples, list) or len(samples) != matrix["repeats"]:
                raise ValueError(f"{name}: {key}/{phase} has an incomplete sample set")
            files = corpus["source_files"]
            hits = files if phase == "warm_unchanged" else files - 1 if phase == "one_changed_declaration" else 0
            if any(sample.get("files") != files or sample.get("cache_hits") != hits for sample in samples):
                raise ValueError(f"{name}: {key}/{phase} has incorrect cache hit or file counts")
            if summary.get("cache_hits") != [hits] * matrix["repeats"]:
                raise ValueError(f"{name}: {key}/{phase} summary cache counts disagree with samples")
            for metric_name in METRICS:
                measured = metric(summary, metric_name)
                values = [number(sample.get(metric_name), metric_name) for sample in samples]
                if not math.isclose(measured, statistics.median(values), rel_tol=0, abs_tol=0.000501):
                    raise ValueError(f"{name}: {key}/{phase}/{metric_name} median disagrees with samples")
    expected = {(kind, size, jobs) for kind in matrix["corpora"]
                for size in ([1] if kind == "tiny" else [None] if kind == "reference" else matrix["sizes"])
                for jobs in matrix["workers"]}
    if set(found) != expected:
        raise ValueError(f"{name}: missing or unexpected corpus matrix entries")
    return found


def compare(baseline, candidate, budgets=None, *, allow_incompatible=False):
    budgets = budgets or Budgets()
    budgets.validate()
    previous = validate(baseline, "baseline")
    current = validate(candidate, "candidate")
    incompatible = []
    for field in ("schema", "hardware", "method", "matrix", "build_settings", "runtime_environment"):
        if baseline[field] != candidate[field]:
            incompatible.append(f"{field} differs")
    common = sorted(set(previous) & set(current))
    if set(previous) != set(current):
        incompatible.append("corpus matrix entries differ")
    for key in common:
        for field in ("corpus_sha256", "changed_input_sha256", "source_files", "source_bytes", "input_bytes", "modules"):
            if previous[key].get(field) != current[key].get(field):
                incompatible.append(f"{key}: {field} differs")
    result = {
        "schema": 1,
        "baseline_binary_sha256": baseline.get("binary_sha256"),
        "candidate_binary_sha256": candidate.get("binary_sha256"),
        "comparable": not incompatible,
        "passed": False,
        "status": "incompatible" if incompatible else "pending",
        "incompatibilities": incompatible,
        "budgets": asdict(budgets),
        "measurements_checked": 0,
        "growth_checks": 0,
        "failures": [],
    }
    if incompatible and not allow_incompatible:
        return result
    for key in common:
        # Even an exploratory comparison must not pair different source inputs.
        if (previous[key]["corpus_sha256"] != current[key]["corpus_sha256"] or
                previous[key]["changed_input_sha256"] != current[key]["changed_input_sha256"]):
            continue
        for phase in sorted(previous[key]["phases"]):
            for metric_name in METRICS:
                before = metric(previous[key]["phases"][phase], metric_name)
                after = metric(current[key]["phases"][phase], metric_name)
                limit = before * (1 + budgets.relative) + getattr(budgets, metric_name)
                result["measurements_checked"] += 1
                if after > limit:
                    result["failures"].append({"kind": "measurement", "case": label(key, phase, metric_name),
                                               "baseline": before, "candidate": after, "limit": limit})
    # Compare adjacent sizes within each corpus/worker group. A known baseline
    # growth curve or linear input growth (whichever is larger) is the envelope.
    # Do not penalize a faster small input when the larger input did not regress.
    groups = {}
    for key in common:
        if (previous[key]["corpus_sha256"] == current[key]["corpus_sha256"] and
                previous[key]["changed_input_sha256"] == current[key]["changed_input_sha256"]):
            groups.setdefault((key[0], key[2]), []).append(key)
    for keys in groups.values():
        keys.sort(key=lambda key: current[key].get("input_bytes", current[key]["source_bytes"]))
        for small, large in zip(keys, keys[1:]):
            small_bytes = current[small].get("input_bytes", current[small]["source_bytes"])
            large_bytes = current[large].get("input_bytes", current[large]["source_bytes"])
            if large_bytes <= small_bytes:
                continue
            input_growth = large_bytes / small_bytes
            for phase in sorted(current[large]["phases"]):
                for metric_name in METRICS:
                    base_small = metric(previous[small]["phases"][phase], metric_name)
                    base_large = metric(previous[large]["phases"][phase], metric_name)
                    head_small = metric(current[small]["phases"][phase], metric_name)
                    head_large = metric(current[large]["phases"][phase], metric_name)
                    if base_small <= 0 or head_small <= 0:
                        continue
                    envelope = max(input_growth, base_large / base_small)
                    noise = getattr(budgets, metric_name)
                    limit = head_small * envelope * (1 + budgets.growth_relative) + noise
                    result["growth_checks"] += 1
                    if head_large > limit and head_large > base_large + noise:
                        result["failures"].append({"kind": "growth", "case": label(large, phase, metric_name),
                                                   "smaller_modules": small[1], "input_growth": input_growth,
                                                   "baseline_growth": base_large / base_small,
                                                   "candidate_growth": head_large / head_small,
                                                   "candidate": head_large, "limit": limit})
    if incompatible:
        result["status"] = "exploratory"
    else:
        result["passed"] = not result["failures"]
        result["status"] = "passed" if result["passed"] else "regressed"
    return result


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--baseline", type=Path, required=True)
    parser.add_argument("--candidate", type=Path, required=True)
    parser.add_argument("--output", type=Path)
    parser.add_argument("--relative", type=float, default=Budgets.relative,
                        help="per-measurement relative allowance; 0.5 means 50%% (default: 0.5)")
    parser.add_argument("--wall-ms", type=float, default=Budgets.wall_ms)
    parser.add_argument("--cpu-ms", type=float, default=Budgets.process_cpu_ms)
    parser.add_argument("--rss-mib", type=float, default=Budgets.peak_rss_bytes / 1024 / 1024)
    parser.add_argument("--growth-relative", type=float, default=Budgets.growth_relative)
    parser.add_argument("--allow-incompatible", action="store_true",
                        help="emit exploratory comparisons; incompatibilities still exit 2 and never pass the gate")
    args = parser.parse_args(argv)
    try:
        budgets = Budgets(args.relative, args.wall_ms, args.cpu_ms, args.rss_mib * 1024 * 1024,
                          args.growth_relative)
        result = compare(json.loads(args.baseline.read_text()), json.loads(args.candidate.read_text()),
                         budgets, allow_incompatible=args.allow_incompatible)
    except (OSError, ValueError, KeyError, TypeError) as error:
        print(f"benchmark comparison rejected: {error}", file=sys.stderr)
        result = {"schema": 1, "passed": False, "comparable": False,
                  "status": "invalid", "error": str(error)}
    rendered = json.dumps(result, indent=2, sort_keys=True) + "\n"
    if args.output:
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(rendered)
    print(rendered, end="")
    return 0 if result["passed"] else 1 if result["comparable"] else 2


if __name__ == "__main__":
    sys.exit(main())
