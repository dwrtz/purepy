#!/usr/bin/env python3
"""Compare fixed nominal/import/async projects with the CLI and CPython 3.14.

The verifier only reads temporary source projects. A separate isolated worker
executes the fixed reviewed development catalog, including a local fixture host.
No supplied project, manifest implementation, or failure-artifact source runs.
"""

from __future__ import annotations

import argparse
import json
from pathlib import Path
import platform
import re
import subprocess
import sys
import tempfile

sys.path.insert(0, str(Path(__file__).resolve().parent))
from differential_functions import known_value
from differential_semantics import ROOT, matches_type, read_results, run_process
from module_cases import catalog
from module_runtime import (fingerprint, input_data, runtime_worker, select_cases,
                            worker_entry, write_sources)


# These declarations are independent of Python annotations and verifier output.
RECORD_FIELDS = {
    **{name + ".Pair": (("x", "int"), ("label", "str"))
       for name in ("models", "left", "right", "pkg.models")},
    "main.Batch": (("entries", "tuple[models.Pair | None, ...]"),),
    "main.Box": (("token", "fixture.Token"),),
}
EXTERNAL_TYPES = frozenset(("fixture.Token", "fixture.Read", "fixture.Connection"))
EXCEPTIONS = frozenset(("ZeroDivisionError", "OverflowError", "ValueError",
                        "TypeError", "IndexError", "AttributeError"))


def valid_observation(value, depth=0):
    if depth > 8 or not isinstance(value, dict):
        return False
    kind = value.get("type")
    if kind == "record":
        entries = value.get("fields")
        return (set(value) == {"type", "name", "fields"}
                and isinstance(value.get("name"), str) and value["name"] in RECORD_FIELDS
                and isinstance(entries, list) and len(entries) <= 16
                and all(isinstance(item, list) and len(item) == 2 and isinstance(item[0], str)
                        and valid_observation(item[1], depth + 1) for item in entries)
                and len({item[0] for item in entries}) == len(entries))
    if kind == "external":
        return (set(value) == {"type", "name", "value"}
                and isinstance(value.get("name"), str) and value["name"] in EXTERNAL_TYPES
                and isinstance(value.get("value"), str) and len(value["value"]) <= 128)
    if kind == "tuple":
        return (set(value) == {"type", "value"} and isinstance(value.get("value"), list)
                and len(value["value"]) <= 64
                and all(valid_observation(item, depth + 1) for item in value["value"]))
    return known_value(value)


def exact_type(value, annotation):
    if not valid_observation(value):
        return False
    if annotation.endswith(" | None"):
        return value["type"] == "None" or exact_type(value, annotation[:-7])
    if annotation.startswith("tuple[") and annotation.endswith(", ...]"):
        return value["type"] == "tuple" and all(exact_type(item, annotation[6:-6]) for item in value["value"])
    if annotation in RECORD_FIELDS:
        return (value["type"] == "record" and value["name"] == annotation
                and [item[0] for item in value["fields"]] == [item[0] for item in RECORD_FIELDS[annotation]]
                and all(exact_type(item[1], field[1]) for item, field in zip(value["fields"], RECORD_FIELDS[annotation])))
    if annotation in EXTERNAL_TYPES:
        return value["type"] == "external" and value["name"] == annotation
    return matches_type(value, annotation)


def reported_type(annotation):
    if annotation.endswith(" | None"):
        return {"kind": "optional", "element": reported_type(annotation[:-7])}
    if annotation.startswith("tuple["):
        return {"kind": "tuple", "element": reported_type(annotation[6:-6])}
    if annotation in RECORD_FIELDS:
        return {"kind": "record", "name": annotation}
    if annotation in EXTERNAL_TYPES:
        return {"kind": {"fixture.Read": "capability", "fixture.Connection": "host_ref"}.get(annotation, "value"),
                "name": annotation}
    return {"kind": annotation}


def static_case(case, verifier):
    with tempfile.TemporaryDirectory(prefix="purepy-module-static-") as directory:
        root = Path(directory)
        write_sources(root / "src", case)
        manifests = ["fixture.toml"] if case.manifest else []
        (root / "purepy.toml").write_text(
            '[tool.purepy]\nlanguage = "0.2"\npython_syntax = "3.14"\nsource_root = "src"\n'
            'entrypoints = ["main.probe"]\nmanifests = ' + json.dumps(manifests) + "\n", encoding="utf-8")
        if case.manifest:
            (root / "fixture.toml").write_text(case.manifest, encoding="utf-8")
        result = subprocess.run([str(verifier), "check", str(root), "--format", "json", "--no-cache"],
                                capture_output=True, text=True, timeout=30, cwd=ROOT)
        if result.returncode not in (0, 1) or result.stderr:
            raise ValueError(f"verifier failed ({result.returncode}): {result.stderr.strip()}")
        report = json.loads(result.stdout)
        if not isinstance(report, dict) or type(report.get("ok")) is not bool or result.returncode != int(not report["ok"]):
            raise ValueError("verifier exit status contradicts JSON verdict")
        return report


def compare_case(case, static, runtime):
    errors = []
    if (not isinstance(static, dict) or type(static.get("schema")) is not int or static["schema"] != 2
            or not isinstance(static.get("verifier_version"), str) or not static["verifier_version"]
            or type(static.get("ok")) is not bool or not isinstance(static.get("diagnostics"), list)
            or not isinstance(static.get("functions"), list)
            or any(not isinstance(item, dict) or not isinstance(item.get("code"), str)
                   or not re.fullmatch(r"PP[0-9]{3}", item["code"]) for item in static["diagnostics"])):
        return ["malformed verifier report"]
    codes = {item["code"] for item in static["diagnostics"]}
    if static["ok"] != (not codes):
        errors.append("verifier verdict contradicts diagnostics")
    if "PP099" in codes:
        errors.append("internal verifier failure")
    if static["ok"] != (not case.codes):
        errors.append("expected PurePy " + ("rejection" if case.codes else "acceptance"))
    if codes != set(case.codes):
        errors.append(f"expected exact diagnostic set {list(case.codes)}, got {sorted(codes)}")
    if static["ok"]:
        probes = [item for item in static["functions"] if isinstance(item, dict) and item.get("name") == "main.probe"]
        if (len(probes) != 1 or probes[0].get("returns") != reported_type(case.returns)
                or probes[0].get("kind") != case.kind or probes[0].get("classification") != case.classification):
            errors.append("entrypoint signature/classification differs from independent declaration")
    if (not isinstance(runtime, dict) or set(runtime) != {"name", "fingerprint", "outcomes"}
            or runtime["name"] != case.name or runtime["fingerprint"] != fingerprint(case)
            or not isinstance(runtime["outcomes"], list) or len(runtime["outcomes"]) != len(case.calls)):
        return errors + ["malformed CPython module result"]
    for index, (call, outcome) in enumerate(zip(case.calls, runtime["outcomes"])):
        prefix = f"invocation {index}: "
        if (not isinstance(outcome, dict) or type(outcome.get("index")) is not int or outcome["index"] != index
                or set(outcome) not in ({"index", "value", "events"}, {"index", "exception", "events"})):
            errors.append(prefix + "malformed or reordered outcome")
            continue
        events = outcome["events"]
        if (not isinstance(events, list) or len(events) > 64
                or any(not isinstance(event, list) or not 1 <= len(event) <= 4
                       or any(type(value) not in (str, int) for value in event) for event in events)
                or events != [list(event) for event in call.events]):
            errors.append(prefix + "host operation/equality events differ from curated order and arguments")
        if "exception" in outcome:
            if not isinstance(outcome["exception"], str) or outcome["exception"] not in EXCEPTIONS:
                errors.append(prefix + "non-domain or malformed runtime exception")
            elif outcome["exception"] != call.exception:
                errors.append(prefix + "unexpected CPython domain exception")
        elif not valid_observation(outcome["value"]):
            errors.append(prefix + "malformed runtime value")
        else:
            if call.exception:
                errors.append(prefix + "expected CPython domain exception")
            if static["ok"] and not exact_type(outcome["value"], case.returns):
                errors.append(prefix + "CPython value violates independently declared exact return type")
            if outcome["value"] != input_data(call.expected):
                errors.append(prefix + "CPython value differs from curated outcome")
    return errors


def run_gate(cases, verifier):
    selected = select_cases([worker_entry(case) for case in cases])
    if tuple(cases) != tuple(selected):
        raise ValueError("gate requires unchanged fixed catalog cases")
    # Separate processes and data paths preserve verifier/runtime independence.
    statics = [static_case(case, verifier) for case in cases]
    command = [sys.executable, "-I", str(Path(__file__).resolve()), "--runtime-worker"]
    request = [worker_entry(case) for case in cases]
    # Isolated CPython ignores PYTHONHASHSEED, so each process obtains its own
    # randomized seed from CPython. Keep isolation and compare actual outcomes.
    payloads = [run_process(command, request) for _ in range(2)]
    for payload in payloads:
        if (not isinstance(payload, dict) or type(payload.get("schema")) is not int or payload["schema"] != 1
                or type(payload.get("hash_randomization")) is not int or payload["hash_randomization"] != 1
                or not isinstance(payload.get("python_version"), str)
                or not re.fullmatch(r"3\.14\.[0-9]+", payload["python_version"])):
            raise ValueError("invalid CPython module worker envelope")
    if payloads[0] != payloads[1]:
        raise ValueError("independently randomized CPython module workers disagree")
    payload = payloads[0]
    runtimes = read_results(payload, cases, "module worker")
    failures = []
    versions = set()
    for case, static, runtime in zip(cases, statics, runtimes):
        errors = compare_case(case, static, runtime)
        if isinstance(static, dict) and isinstance(static.get("verifier_version"), str):
            versions.add(static["verifier_version"])
        if errors:
            failures.append({"name": case.name, "errors": errors, "verifier": static, "runtime": runtime})
    if len(versions) != 1:
        raise ValueError("verifier version missing or changed during gate")
    return failures, {"python_version": payload["python_version"], "verifier_version": versions.pop()}


def save_failures(path, cases, failures, versions):
    known = {case.name: case for case in cases}
    records = []
    for failure in failures:
        case = known[failure["name"]]
        records.append({**failure, "case": {**worker_entry(case), "sources": dict(case.sources),
                                           "manifest": case.manifest, "returns": case.returns,
                                           "codes": list(case.codes),
                                           "calls": [{"arguments": [input_data(value) for value in call.arguments],
                                                      "expected": input_data(call.expected), "exception": call.exception,
                                                      "events": [list(event) for event in call.events]} for call in case.calls]}})
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps({"schema": 1, "kind": "fixed_module", **versions, "failures": records},
                               indent=2, allow_nan=False) + "\n", encoding="utf-8")


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--verifier", type=Path, default=ROOT / "bin/purepy")
    parser.add_argument("--case", help="rerun one fixed catalog case by its stable name")
    parser.add_argument("--failures", type=Path, default=ROOT / "build/module-differential-failures.json")
    parser.add_argument("--runtime-worker", action="store_true", help=argparse.SUPPRESS)
    args = parser.parse_args(argv)
    if platform.python_implementation() != "CPython" or sys.version_info[:2] != (3, 14):
        parser.error("requires CPython 3.14")
    cases = ()
    try:
        if args.runtime_worker:
            runtime_worker()
            return 0
        cases = tuple(case for case in catalog() if not args.case or case.name == args.case)
        if not cases:
            parser.error(f"unknown fixed case {args.case!r}")
        failures, versions = run_gate(cases, args.verifier.resolve())
        print(f"CPython {versions['python_version']}, PurePy {versions['verifier_version']}: "
              f"{len(cases)} module cases, {sum(len(case.calls) for case in cases)} invocations.")
        print(f"accepted={sum(not case.codes for case in cases)}, rejected={sum(bool(case.codes) for case in cases)}")
        if failures:
            save_failures(args.failures, cases, failures, versions)
            for failure in failures:
                print(f"{failure['name']}: {'; '.join(failure['errors'])}", file=sys.stderr)
            print(f"Saved source, input and oracle evidence to {args.failures}; rerun with --case NAME.", file=sys.stderr)
            return 1
        print("PASS: exact nominal types, module identity, direct async outcomes and fixture host events agree.")
        print("Finite evidence from trusted fixed development projects and selected inputs.")
        return 0
    except (OSError, ValueError, RuntimeError, SyntaxError, subprocess.TimeoutExpired) as error:
        print(f"module differential gate failed: {error}", file=sys.stderr)
        if cases:
            save_failures(args.failures, cases,
                          [{"name": case.name, "errors": [str(error)], "verifier": None, "runtime": None} for case in cases], {})
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
