#!/usr/bin/env python3
"""Apply explicit machine/workload acceptance budgets to validated measurements.

This complements same-run regression comparisons. A report from another machine,
an incomplete matrix, or a shortened service soak cannot satisfy this profile.
"""

import argparse
import json
from pathlib import Path
import sys

from benchmark_compare import metric, number, validate


def accept(profile, benchmark, service):
    if profile.get("schema") != 1 or not profile.get("name"):
        raise ValueError("acceptance profile schema/name is invalid")
    entries = validate(benchmark, "candidate")
    for key, expected in profile["hardware"].items():
        if benchmark["hardware"].get(key) != expected:
            raise ValueError(f"hardware {key} does not match the acceptance profile")
    for key, expected in profile["matrix"].items():
        if benchmark["matrix"].get(key) != expected:
            raise ValueError(f"matrix {key} does not match the acceptance profile")
    failures, checked = [], 0

    def maximum(label, value, limit):
        nonlocal checked
        number(value, label)
        number(limit, label + " budget", positive=True)
        checked += 1
        if value > limit:
            failures.append({"measurement": label, "actual": value, "maximum": limit})

    def minimum(label, value, limit):
        nonlocal checked
        number(value, label)
        number(limit, label + " budget", positive=True)
        checked += 1
        if value < limit:
            failures.append({"measurement": label, "actual": value, "minimum": limit})

    for key, entry in entries.items():
        limits = profile["verifier"][entry["corpus"]]
        size = entry["modules"] or 1
        for phase, summary in entry["phases"].items():
            for name, formula in limits.items():
                # Absolute ceilings with a linear per-module allowance are
                # fixed in the reviewed profile before candidate measurement.
                for part in ("fixed", "per_module"):
                    number(formula[part], name + " " + part)
                limit = formula["fixed"] + formula["per_module"] * size
                maximum(f"{key}/{phase}/{name}", metric(summary, name), limit)
            if phase == "warm_unchanged":
                for sample in summary["samples"]:
                    for stage in ("parse", "lower"):
                        if sample["worker_ms"][stage] != 0:
                            raise ValueError("warm acceptance requires zero parsing and lowering")

    if service.get("schema") != 2 or service.get("ok") is not True:
        raise ValueError("a successful schema-2 service report is required")
    if service.get("platform") != benchmark["hardware"]["platform"]:
        raise ValueError("service and verifier measurements must use the same platform")
    if service.get("python") != benchmark["hardware"]["harness_python"]:
        raise ValueError("service and verifier harness Python versions differ")
    policy = profile["service"]
    for key, expected in policy["options"].items():
        if service["options"].get(key) != expected:
            raise ValueError(f"service option {key} does not match the acceptance profile")
    if service.get("mode") != "duration":
        raise ValueError("acceptance requires a duration-based service soak")
    minimum("service/duration_seconds", service["request_window_seconds"], policy["options"]["duration"])
    if service["errors"]["count"] != 0 or service["host_error_count"] != 0:
        raise ValueError("service errors cannot pass acceptance")
    resources = service["resources_after_cleanup"]
    for key in ("client_tasks_remaining", "client_writers_remaining", "database_reads_remaining", "host_tasks_remaining"):
        if resources.get(key) != 0:
            raise ValueError(f"service leaked {key}")
    if resources.get("database_closed") is not True or resources.get("listener_closed") is not True:
        raise ValueError("service listener and database must close")
    operations = service["operations"]
    for key in ("read", "write"):
        op = operations[key]
        if type(op["succeeded"]) is not int or op["succeeded"] <= 0 or op["attempted"] != op["succeeded"] or op["failed"] != 0:
            raise ValueError("both read and write workloads must complete without errors")
    successful = sum(operations[key]["succeeded"] for key in ("read", "write"))
    if service["requests"] != successful or service["latency_ms"]["count"] != successful:
        raise ValueError("request accounting does not match successful operations")
    writes = operations["write"]["succeeded"]
    streams = policy["options"]["sse_connections"]
    sse = service["sse"]
    if (sse["events_per_stream"] != [writes] * streams or sse["last_event_ids"] != [writes] * streams
            or sse["events_received"] != writes * streams or sse["pending_events"] != 0
            or service["database_final"]["balance"] != writes or service["database_final"]["event_rows"] != writes):
        raise ValueError("SSE delivery and database ledger do not match completed writes")
    minimum("service/overlapping_reads", service["observed_overlapping_database_reads"], 2)
    minimum("service/requests_per_second", service["throughput_requests_per_second"], policy["minimum_requests_per_second"])
    for name in ("p95", "p99"):
        maximum("service/latency_" + name + "_ms", service["latency_ms"][name], policy["maximum_latency_" + name + "_ms"])
    maximum("service/sse_delivery_p99_ms", sse["write_start_to_event_ms"]["p99"], policy["maximum_sse_delivery_p99_ms"])
    maximum("service/peak_resident_bytes", service["peak_resident_bytes"], policy["maximum_peak_resident_bytes"])
    maximum("service/traced_bytes_after_cleanup", service["traced_python_bytes_after_cleanup"], policy["maximum_traced_bytes_after_cleanup"])
    return {"schema": 1, "profile": profile["name"], "passed": not failures,
            "binary_sha256": benchmark["binary_sha256"], "measurements_checked": checked,
            "failures": failures}


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--profile", type=Path, required=True)
    parser.add_argument("--benchmark", type=Path, required=True)
    parser.add_argument("--service", type=Path, required=True)
    parser.add_argument("--output", type=Path)
    args = parser.parse_args(argv)
    try:
        result = accept(*(json.loads(path.read_text()) for path in (args.profile, args.benchmark, args.service)))
    except (OSError, ValueError, KeyError, TypeError) as error:
        result = {"schema": 1, "passed": False, "error": str(error)}
    rendered = json.dumps(result, indent=2, sort_keys=True) + "\n"
    if args.output:
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(rendered)
    print(rendered, end="")
    return 0 if result["passed"] else 1


if __name__ == "__main__":
    sys.exit(main())
