#!/usr/bin/env python3
"""Compare trusted generated whole functions with PurePy and CPython 3.14.

This development gate checks static verdicts and concrete invocation outcomes;
it neither executes analyzed projects nor proves behavior for every input.
"""

from __future__ import annotations

import argparse
import json
from pathlib import Path
import platform
import re
import subprocess
import sys

# The isolated worker ignores the normal script-directory import path.
sys.path.insert(0, str(Path(__file__).resolve().parent))
from differential_semantics import (ROOT, decode_value, encode_value,
                                    matches_type, read_results, run_process,
                                    valid_value)
from function_cases import FunctionCase, Invocation, generate_cases
from function_runtime import runtime_worker, validate_case


MAX_CASES = 2000
MAX_REQUEST = 16 * 1024 * 1024
EXCEPTIONS = frozenset(("ZeroDivisionError", "OverflowError", "FloatingPointError",
                        "ArithmeticError", "ValueError", "TypeError", "IndexError",
                        "UnboundLocalError"))


def worker_entry(case):
    """Send source and typed inputs only; expectations stay in the parent."""
    return {"name": case.name, "family": case.family, "source": case.source,
            "parameters": [list(parameter) for parameter in case.parameters],
            "returns": case.returns,
            "invocations": [{"arguments": [encode_value(value) for value in invocation.arguments]}
                            for invocation in case.invocations]}


def case_entry(case):
    entry = worker_entry(case)
    entry["expected_codes"] = list(case.expected_codes)
    for invocation, record in zip(case.invocations, entry["invocations"]):
        record.update(check_value=invocation.check_value,
                      expected_value=encode_value(invocation.expected_value) if invocation.check_value else None,
                      exception=invocation.exception)
    return entry


def known_value(value, depth=0):
    """Validate bounded tagged data without accepting unknown result kinds."""
    if depth > 8 or not isinstance(value, dict) or set(value) != {"type", "value"}:
        return False
    if value.get("type") == "tuple":
        return (isinstance(value["value"], list) and len(value["value"]) <= 4096
                and all(known_value(item, depth + 1) for item in value["value"]))
    if value.get("type") not in ("None", "bool", "int", "float", "str", "bytes"):
        return False
    try:
        return valid_value(value)
    except (ValueError, TypeError, OverflowError):
        return False


def validate_oracles(case):
    validate_case(case)
    if not isinstance(case.expected_codes, tuple) or any(
            not isinstance(code, str) or not re.fullmatch(r"PP[0-9]{3}", code) or code == "PP099"
            for code in case.expected_codes):
        raise ValueError("expected diagnostic codes must be ordinary PurePy codes")
    for invocation in case.invocations:
        if type(invocation.check_value) is not bool:
            raise ValueError("check_value must be Boolean")
        if invocation.exception is not None and (not isinstance(invocation.exception, str)
                                                 or invocation.exception not in EXCEPTIONS):
            raise ValueError("unsupported expected exception")
        if invocation.exception and invocation.check_value:
            raise ValueError("an invocation cannot expect both a value and an exception")
        if invocation.check_value and not known_value(encode_value(invocation.expected_value)):
            raise ValueError("unsupported expected value")


def decode_case(entry):
    fields = {"name", "family", "source", "parameters", "returns", "invocations", "expected_codes"}
    if not isinstance(entry, dict) or set(entry) != fields:
        raise ValueError("invalid replay case fields")
    if (not isinstance(entry["parameters"], list) or not isinstance(entry["invocations"], list)
            or not isinstance(entry["expected_codes"], list)):
        raise ValueError("invalid replay case arrays")
    invocations = []
    for item in entry["invocations"]:
        if not isinstance(item, dict) or set(item) != {"arguments", "check_value", "expected_value", "exception"}:
            raise ValueError("invalid replay invocation fields")
        if not isinstance(item["arguments"], list) or not all(known_value(value) for value in item["arguments"]):
            raise ValueError("invalid replay arguments")
        if item["check_value"] and not known_value(item["expected_value"]):
            raise ValueError("invalid replay expected value")
        if not item["check_value"] and item["expected_value"] is not None:
            raise ValueError("unused replay expected value must be null")
        invocations.append(Invocation(tuple(decode_value(value) for value in item["arguments"]),
                                      decode_value(item["expected_value"]) if item["check_value"] else None,
                                      item["check_value"], item["exception"]))
    if any(not isinstance(parameter, list) or len(parameter) != 2 for parameter in entry["parameters"]):
        raise ValueError("invalid replay parameters")
    case = FunctionCase(entry["name"], entry["family"], entry["source"],
                        tuple(tuple(parameter) for parameter in entry["parameters"]),
                        entry["returns"], tuple(invocations), tuple(entry["expected_codes"]))
    validate_oracles(case)
    return case


def load_replay(path):
    if path.stat().st_size > MAX_REQUEST:
        raise ValueError("replay file too large")
    payload = json.loads(path.read_text(encoding="utf-8"))
    if (not isinstance(payload, dict) or type(payload.get("schema")) is not int
            or payload["schema"] != 1 or payload.get("kind") != "whole_function"
            or not isinstance(payload.get("failures"), list)):
        raise ValueError("unsupported whole-function failure artifact")
    cases = []
    for failure in payload["failures"]:
        if not isinstance(failure, dict) or "case" not in failure:
            raise ValueError("invalid replay failure record")
        cases.append(decode_case(failure["case"]))
    return cases


def compare_case(case, static, runtime):
    errors = []
    if (not isinstance(static, dict) or type(static.get("ok")) is not bool
            or not isinstance(static.get("diagnostics"), list)
            or not isinstance(static.get("inferred_types"), list)
            or any(not isinstance(item, dict) or not isinstance(item.get("code"), str)
                   or not re.fullmatch(r"PP[0-9]{3}", item["code"]) for item in static["diagnostics"])):
        return ["malformed verifier result"]
    codes = {item["code"] for item in static["diagnostics"]}
    if static["ok"] != (not codes):
        errors.append("verifier verdict contradicts diagnostics")
    if "PP099" in codes:
        errors.append("internal verifier failure")
    if static["ok"] != (not case.expected_codes):
        errors.append(f"expected PurePy {'rejection' if case.expected_codes else 'acceptance'}")
    if case.expected_codes and not codes.intersection(case.expected_codes):
        errors.append(f"expected diagnostic in {case.expected_codes}, got {sorted(codes)}")
    if (not isinstance(runtime, dict) or set(runtime) != {"name", "outcomes"}
            or runtime["name"] != case.name or not isinstance(runtime["outcomes"], list)
            or len(runtime["outcomes"]) != len(case.invocations)):
        return errors + ["malformed CPython result or missing invocations"]
    for index, (invocation, outcome) in enumerate(zip(case.invocations, runtime["outcomes"])):
        prefix = f"invocation {index}: "
        if (not isinstance(outcome, dict) or type(outcome.get("index")) is not int
                or outcome["index"] != index or set(outcome) not in ({"index", "value"}, {"index", "exception"})):
            errors.append(prefix + "malformed, reordered, or duplicate CPython outcome")
            continue
        if "exception" in outcome:
            if not isinstance(outcome["exception"], str) or outcome["exception"] not in EXCEPTIONS:
                errors.append(prefix + "malformed or non-domain CPython exception")
                continue
        elif not known_value(outcome["value"]):
            errors.append(prefix + "malformed or unsupported CPython value")
            continue
        if invocation.exception:
            if outcome.get("exception") != invocation.exception:
                errors.append(prefix + f"expected CPython exception {invocation.exception}")
        elif "exception" in outcome:
            errors.append(prefix + f"unexpected CPython exception {outcome['exception']}")
        else:
            # Correctly rejected cases may deliberately demonstrate a bad return.
            # Accidental acceptance must still be tested against that witness.
            if static["ok"] and not matches_type(outcome["value"], case.returns):
                errors.append(prefix + f"CPython return does not have exact declared type {case.returns}")
            if invocation.check_value and outcome["value"] != encode_value(invocation.expected_value):
                errors.append(prefix + "CPython return disagrees with curated value expectation")
    return errors


def run_gate(cases, probe):
    if not cases or len(cases) > MAX_CASES or len({case.name for case in cases}) != len(cases):
        raise ValueError("corpus must have unique names and between 1 and 2000 cases")
    for case in cases:
        validate_oracles(case)
    # The adapter always checks the entire module. A range over the initial 'd'
    # selects no expression fact; whole-function verdicts need no inferred local.
    requests = {"cases": [{"name": case.name, "source": case.source, "start": 0, "end": 1}
                          for case in cases]}
    worker_request = [worker_entry(case) for case in cases]
    if any(len(json.dumps(request, allow_nan=False).encode()) > MAX_REQUEST
           for request in (requests, worker_request)):
        raise ValueError("whole-function request too large")
    static_payload = run_process([str(probe)], requests)
    if (not isinstance(static_payload, dict) or type(static_payload.get("schema")) is not int
            or static_payload["schema"] != 1 or not isinstance(static_payload.get("verifier_version"), str)
            or not static_payload["verifier_version"]):
        raise ValueError("unsupported verifier probe schema/version")
    static_results = read_results(static_payload, cases, "verifier")
    runtime_payload = run_process([sys.executable, "-I", str(Path(__file__).resolve()), "--runtime-worker"], worker_request)
    if (not isinstance(runtime_payload, dict) or type(runtime_payload.get("schema")) is not int
            or runtime_payload["schema"] != 1 or not isinstance(runtime_payload.get("python_version"), str)
            or not re.fullmatch(r"3\.14\.[0-9]+", runtime_payload["python_version"])):
        raise ValueError("unsupported CPython worker schema/version")
    runtime_results = read_results(runtime_payload, cases, "CPython")
    failures = []
    for case, static, runtime in zip(cases, static_results, runtime_results):
        errors = compare_case(case, static, runtime)
        if errors:
            failures.append({"name": case.name, "errors": errors, "case": case_entry(case),
                             "verifier": static, "runtime": runtime})
    return failures, {"verifier_version": static_payload["verifier_version"],
                      "python_version": runtime_payload["python_version"]}


def save_failures(path, failures, versions, seed, samples):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps({"schema": 1, "kind": "whole_function", **versions,
                               "seed": seed, "samples": samples, "failures": failures},
                              indent=2, allow_nan=False) + "\n", encoding="utf-8")


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--probe", type=Path, default=ROOT / "bin/purepy-semantic-probe")
    parser.add_argument("--seed", type=int, default=0)
    parser.add_argument("--samples", type=int, default=32)
    parser.add_argument("--case", help="run one stable case name")
    parser.add_argument("--replay", type=Path, help="rerun cases from a trusted whole-function failure artifact")
    parser.add_argument("--failures", type=Path, default=ROOT / "build/function-differential-failures.json")
    parser.add_argument("--runtime-worker", action="store_true", help=argparse.SUPPRESS)
    args = parser.parse_args(argv)
    if platform.python_implementation() != "CPython" or sys.version_info[:2] != (3, 14):
        parser.error("requires CPython 3.14; use make differential-test")
    if not 0 <= args.samples <= 256:
        parser.error("--samples must be between 0 and 256")
    # Replays use stored source and inputs, not the current generator settings.
    seed, samples = (None, None) if args.replay else (args.seed, args.samples)
    origin = {"replay_source": str(args.replay)} if args.replay else {}
    cases = []
    try:
        if args.runtime_worker:
            runtime_worker()
            return 0
        cases = load_replay(args.replay) if args.replay else generate_cases(args.seed, args.samples)
        if args.case:
            cases = [case for case in cases if case.name == args.case]
            if not cases:
                parser.error(f"unknown case {args.case!r}")
        failures, versions = run_gate(cases, args.probe.resolve())
        versions.update(origin)
        invocations = sum(len(case.invocations) for case in cases)
        accepted = sum(not case.expected_codes for case in cases)
        selection = f"replay={args.replay}" if args.replay else f"seed={seed}, samples={samples}"
        print(f"CPython {versions['python_version']}, PurePy {versions['verifier_version']}: "
              f"{len(cases)} whole-function cases, {invocations} invocations, {selection}.")
        print(f"accepted={accepted}, rejected={len(cases) - accepted}")
        if failures:
            save_failures(args.failures, failures, versions, seed, samples)
            for failure in failures:
                print(f"{failure['name']}: {'; '.join(failure['errors'])}", file=sys.stderr)
            print(f"Saved {len(failures)} reproducible mismatches to {args.failures}", file=sys.stderr)
            return 1
        print("PASS: documented verdicts, exact return types and curated invocation outcomes agree.")
        print("Finite development evidence over bounded synchronous functions and selected inputs.")
        return 0
    except (OSError, ValueError, SyntaxError, subprocess.TimeoutExpired) as error:
        print(f"whole-function differential gate failed: {error}", file=sys.stderr)
        if cases:
            try:
                failures = [{"name": case.name, "errors": [f"gate failure: {error}"],
                             "case": case_entry(case), "verifier": None, "runtime": None} for case in cases]
                save_failures(args.failures, failures, {"python_version": platform.python_version(), **origin}, seed, samples)
                print(f"Saved gate failure inputs to {args.failures}", file=sys.stderr)
            except (OSError, ValueError, TypeError) as artifact_error:
                print(f"could not preserve failure artifact: {artifact_error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
