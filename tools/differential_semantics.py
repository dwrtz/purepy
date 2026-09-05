#!/usr/bin/env python3
"""Development-only differential gate for trusted generated primitive expressions.

The Go helper parses and checks source without executing it. A separate CPython
3.14 process evaluates only this development corpus, never analyzed user projects
or host manifests. The finite matrix checks exact runtime types, not a proof of
all programs, floating-point portability, or absence of deterministic exceptions.
"""

from __future__ import annotations

import argparse
import ast
from collections import Counter
from dataclasses import asdict
import json
import math
from pathlib import Path
import platform
import re
import subprocess
import sys

# Isolated worker mode ignores Python's usual script-directory import path.
sys.path.insert(0, str(Path(__file__).resolve().parent))
from semantic_cases import Case, generate_cases


ROOT = Path(__file__).resolve().parents[1]
BUILTINS = {name: getattr(__import__("builtins"), name) for name in (
    "len", "range", "abs", "min", "max", "sum", "all", "any", "int", "float", "str", "bytes"
)}
MAX_CASES = 10000
MAX_SEQUENCE = 4096
TIMEOUT = 60


def source_case(case):
    """Probe a read of the inferred local, without imposing an expected type."""
    lines = ["def probe() -> None:"]
    lines.extend(f"    {name}: {annotation} = {value}" for name, annotation, value in case.bindings)
    if case.mode == "range":
        lines.extend([f"    for result in {case.expression}:", "        observed = result"])
    elif case.mode == "expression":
        lines.extend([f"    result = {case.expression}", "    observed = result"])
    else:
        raise ValueError(f"unknown mode {case.mode!r}")
    source = "\n".join(lines) + "\n"
    start = len(source[:source.rindex("result")].encode("utf-8"))
    return {"name": case.name, "source": source, "start": start, "end": start + len("result")}


def encode_value(value):
    """Portable JSON representation preserving bool/int and signed zero."""
    kind = type(value)
    if value is None:
        return {"type": "None", "value": None}
    if kind is bool:
        return {"type": "bool", "value": value}
    if kind is int:
        return {"type": "int", "value": str(value)}
    if kind is float:
        return {"type": "float", "value": "nan" if math.isnan(value) else value.hex()}
    if kind is str:
        return {"type": "str", "value": value}
    if kind is bytes:
        return {"type": "bytes", "value": value.hex()}
    if kind is tuple:
        if len(value) > MAX_SEQUENCE:
            raise ValueError("runtime tuple exceeded development gate bound")
        return {"type": "tuple", "value": [encode_value(item) for item in value]}
    # Rejected Python operations can produce a value outside PurePy's model.
    return {"type": kind.__name__}


def matches_type(observed, expected):
    if expected.endswith(" | None"):
        return observed["type"] == "None" or matches_type(observed, expected[:-7])
    if expected.startswith("tuple[") and expected.endswith(", ...]"):
        element = expected[6:-6]
        return observed["type"] == "tuple" and all(matches_type(item, element) for item in observed["value"])
    return observed["type"] == expected


def compile_expression(source, names):
    """Guard against accidental corpus expansion into executable host machinery.

    This is a development-corpus check, not a sandbox for arbitrary expressions.
    Resource limits and a parent timeout also bound the separate runtime process.
    """
    tree = ast.parse(source, mode="eval")
    allowed = (ast.Expression, ast.Constant, ast.Name, ast.Load, ast.Tuple,
               ast.UnaryOp, ast.unaryop, ast.BinOp, ast.operator, ast.BoolOp,
               ast.boolop, ast.Compare, ast.cmpop, ast.Subscript, ast.Slice,
               ast.IfExp, ast.JoinedStr, ast.FormattedValue, ast.Call, ast.keyword,
               ast.Starred)
    nodes = list(ast.walk(tree))
    if len(nodes) > 1000 or len(source) > 32768:
        raise ValueError("expression exceeded development gate bound")
    for node in nodes:
        if not isinstance(node, allowed):
            raise ValueError(f"corpus contains disallowed AST node {type(node).__name__}")
        if isinstance(node, ast.Name) and node.id not in names and node.id not in BUILTINS:
            raise ValueError(f"corpus references unknown name {node.id!r}")
        if isinstance(node, ast.Call) and (not isinstance(node.func, ast.Name) or node.func.id not in BUILTINS):
            raise ValueError("corpus calls must name a fixed primitive builtin")
        if isinstance(node, ast.Constant):
            if type(node.value) not in (type(None), bool, int, float, str, bytes):
                raise ValueError("unsupported corpus literal")
            if type(node.value) is int and node.value.bit_length() > 8192:
                raise ValueError("integer exceeded development gate bound")
            if isinstance(node.value, (str, bytes)) and len(node.value) > MAX_SEQUENCE:
                raise ValueError("literal exceeded development gate bound")
    return compile(tree, "<generated differential expression>", "eval")


def runtime_case(case):
    namespace = {}
    for name, _annotation, literal in case.bindings:
        if not re.fullmatch(r"[a-z][a-z_0-9]*", name) or name in namespace or name in BUILTINS or name in ("result", "observed"):
            raise ValueError(f"invalid corpus binding {name!r}")
        # Binding initialization must not hide a domain error in the operation.
        namespace[name] = eval(compile_expression(literal, namespace), {"__builtins__": BUILTINS}, namespace)
    code = compile_expression(case.expression, namespace)
    try:
        value = eval(code, {"__builtins__": BUILTINS}, namespace)
    except (ArithmeticError, ValueError, TypeError, IndexError) as error:
        return {"name": case.name, "exception": type(error).__name__}
    # Failures in the gate itself must never masquerade as expected CPython
    # domain exceptions, including for cases requiring verifier rejection.
    if case.mode == "range":
        if type(value) is not range:
            raise ValueError("range-mode corpus expression did not produce range")
        if len(value) > MAX_SEQUENCE:
            raise ValueError("range exceeded development gate bound")
        value = tuple(value)
    return {"name": case.name, "value": encode_value(value)}


def runtime_worker():
    # Only generated expressions run here. No modules, decorators or host code
    # named in a verifier input are imported or executed.
    import resource
    def limit(kind, maximum):
        soft, hard = resource.getrlimit(kind)
        for existing in (soft, hard):
            if existing != resource.RLIM_INFINITY:
                maximum = min(maximum, existing)
        resource.setrlimit(kind, (maximum, hard))
    limit(resource.RLIMIT_CPU, 30)
    # Darwin does not support lowering RLIMIT_AS consistently; Linux CI also
    # has a 1 GiB address-space cap. Both use the parent wall timeout.
    if sys.platform.startswith("linux"):
        limit(resource.RLIMIT_AS, 1024 * 1024 * 1024)
    data = sys.stdin.buffer.read(16 * 1024 * 1024 + 1)
    if len(data) > 16 * 1024 * 1024:
        raise ValueError("runtime request too large")
    entries = json.loads(data)
    if not isinstance(entries, list) or len(entries) > MAX_CASES:
        raise ValueError("invalid runtime case list")
    cases = [Case(**entry) for entry in entries]
    print(json.dumps({"python_version": platform.python_version(), "results": [runtime_case(case) for case in cases]}, allow_nan=False))


def read_results(payload, cases, label):
    if not isinstance(payload, dict) or not isinstance(payload.get("results"), list):
        raise ValueError(f"{label} returned an invalid result envelope")
    results = payload["results"]
    if len(results) != len(cases) or any(not isinstance(result, dict) or result.get("name") != case.name for result, case in zip(results, cases)):
        raise ValueError(f"{label} returned missing, reordered, or duplicate cases")
    return results


def compare_case(case, static, runtime):
    failures = []
    accepted = case.expected_type is not None
    if not isinstance(runtime, dict) or ("value" in runtime) == ("exception" in runtime):
        return ["malformed CPython outcome: require exactly one value or exception"]
    if "exception" in runtime and (not isinstance(runtime["exception"], str) or not runtime["exception"]):
        return ["malformed CPython exception"]
    if "value" in runtime and not valid_value(runtime["value"]):
        return ["malformed CPython value"]
    if type(static.get("ok")) is not bool or not isinstance(static.get("diagnostics"), list) or not isinstance(static.get("inferred_types"), list):
        return ["malformed verifier result"]
    if static["ok"] != (not static["diagnostics"]):
        failures.append("verifier verdict contradicts diagnostics")
    if any(diagnostic.get("code") == "PP099" for diagnostic in static["diagnostics"]):
        failures.append("internal verifier failure")
    if static["ok"] != accepted:
        failures.append(f"expected PurePy {'acceptance' if accepted else 'rejection'}")
    if accepted:
        if static["inferred_types"] != [case.expected_type]:
            failures.append(f"expected inferred type {case.expected_type}, got {static['inferred_types']}")
        if case.exception:
            if runtime.get("exception") != case.exception:
                failures.append(f"expected CPython exception {case.exception}, got {runtime}")
        elif "exception" in runtime:
            failures.append(f"unexpected CPython exception {runtime['exception']}")
        elif "value" not in runtime:
            failures.append("missing CPython result")
        else:
            expected = f"tuple[{case.expected_type}, ...]" if case.mode == "range" else case.expected_type
            if not matches_type(runtime["value"], expected):
                failures.append(f"CPython result does not have exact type {expected}")
            if case.check_value and runtime["value"] != encode_value(case.expected_value):
                failures.append("CPython result disagrees with curated value expectation")
    return failures


def valid_value(value):
    if not isinstance(value, dict) or not isinstance(value.get("type"), str):
        return False
    kind = value["type"]
    data = value.get("value")
    if kind == "None":
        return "value" in value and data is None
    if kind == "bool":
        return type(data) is bool
    if kind == "int":
        return isinstance(data, str) and re.fullmatch(r"-?[0-9]+", data) is not None
    if kind == "float":
        if not isinstance(data, str):
            return False
        try:
            float.fromhex(data)
        except ValueError:
            return False
        return True
    if kind == "str":
        return isinstance(data, str)
    if kind == "bytes":
        return isinstance(data, str) and re.fullmatch(r"(?:[0-9a-f]{2})*", data) is not None
    if kind == "tuple":
        return isinstance(data, list) and all(valid_value(item) for item in data)
    return set(value) == {"type"} and bool(kind)


def decode_value(value):
    if not valid_value(value):
        raise ValueError("invalid encoded regression value")
    kind, data = value["type"], value.get("value")
    if kind in ("None", "bool", "str"):
        return data
    if kind == "int":
        return int(data)
    if kind == "float":
        return float.fromhex(data)
    if kind == "bytes":
        return bytes.fromhex(data)
    if kind == "tuple":
        return tuple(decode_value(item) for item in data)
    raise ValueError(f"unsupported regression value type {kind!r}")


def load_regressions(path=ROOT / "fixtures/conformance/semantic_regressions.json"):
    """Load reviewed development fixtures, including copied failure case records."""
    document = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(document, dict) or document.get("schema") != 1 or not isinstance(document.get("cases"), list):
        raise ValueError("unsupported semantic regression fixture schema")
    cases = []
    for entry in document["cases"]:
        if not isinstance(entry, dict) or set(entry) - set(Case.__dataclass_fields__):
            raise ValueError("unknown regression case fields")
        entry = dict(entry)
        entry["bindings"] = tuple(tuple(binding) for binding in entry.get("bindings", ()))
        if entry.get("check_value"):
            entry["expected_value"] = decode_value(entry.get("expected_value"))
        try:
            cases.append(Case(**entry))
        except TypeError as error:
            raise ValueError(f"invalid regression case: {error}") from error
    return cases


def run_process(command, request):
    result = subprocess.run(command, input=json.dumps(request, allow_nan=False), text=True,
                            capture_output=True, timeout=TIMEOUT, cwd=ROOT)
    if result.returncode:
        raise ValueError(f"{Path(command[0]).name} failed ({result.returncode}): {result.stderr.strip()}")
    return json.loads(result.stdout)


def worker_entry(case):
    # Value oracles stay in the parent; preserve tuple/bytes/float identity there.
    data = asdict(case)
    data["expected_value"] = None
    data["check_value"] = False
    return data


def run_gate(cases, probe):
    if not cases or len(cases) > MAX_CASES or len({case.name for case in cases}) != len(cases):
        raise ValueError("corpus must have unique names and between 1 and 10000 cases")
    requests = [source_case(case) for case in cases]
    static_payload = run_process([str(probe)], {"cases": requests})
    if static_payload.get("schema") != 1 or not static_payload.get("verifier_version"):
        raise ValueError("unsupported verifier probe schema/version")
    static_results = read_results(static_payload, cases, "verifier")
    # -I disables user site/environment paths; only this script's tools directory
    # is added explicitly before importing the case definitions.
    runtime_payload = run_process([sys.executable, "-I", str(Path(__file__).resolve()), "--runtime-worker"], [worker_entry(case) for case in cases])
    runtime_results = read_results(runtime_payload, cases, "CPython")
    failures = []
    for case, request, static, runtime in zip(cases, requests, static_results, runtime_results):
        errors = compare_case(case, static, runtime)
        if errors:
            expectation = worker_entry(case)
            expectation["expected_value"] = encode_value(case.expected_value) if case.check_value else None
            expectation["check_value"] = case.check_value
            failures.append({"name": case.name, "errors": errors, "case": expectation,
                             "probe": request, "verifier": static, "runtime": runtime})
    return failures, {"verifier_version": static_payload["verifier_version"], "python_version": runtime_payload["python_version"]}


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--probe", type=Path, default=ROOT / "bin/purepy-semantic-probe")
    parser.add_argument("--seed", type=int, default=0)
    parser.add_argument("--samples", type=int, default=32)
    parser.add_argument("--case", help="run one stable case name")
    parser.add_argument("--failures", type=Path, default=ROOT / "build/differential-failures.json")
    parser.add_argument("--runtime-worker", action="store_true", help=argparse.SUPPRESS)
    args = parser.parse_args(argv)
    if platform.python_implementation() != "CPython" or sys.version_info[:2] != (3, 14):
        parser.error("requires CPython 3.14; use make differential-test")
    try:
        if args.runtime_worker:
            runtime_worker()
            return 0
        if not 0 <= args.samples <= 256:
            parser.error("--samples must be between 0 and 256")
        cases = generate_cases(args.seed, args.samples) + load_regressions()
        if args.case:
            cases = [case for case in cases if case.name == args.case]
            if not cases:
                parser.error(f"unknown case {args.case!r}")
        failures, versions = run_gate(cases, args.probe.resolve())
        summary = Counter("rejected" if case.expected_type is None else "domain_failure" if case.exception else "value" for case in cases)
        print(f"CPython {versions['python_version']}, PurePy {versions['verifier_version']}: {len(cases)} semantic cases, seed={args.seed}, samples={args.samples}.")
        print(", ".join(f"{key}={summary[key]}" for key in sorted(summary)))
        if failures:
            args.failures.parent.mkdir(parents=True, exist_ok=True)
            args.failures.write_text(json.dumps({"schema": 1, **versions, "seed": args.seed, "samples": args.samples, "failures": failures}, indent=2, allow_nan=False) + "\n", encoding="utf-8")
            for failure in failures:
                print(f"{failure['name']}: {'; '.join(failure['errors'])}", file=sys.stderr)
            print(f"Saved {len(failures)} reproducible mismatches to {args.failures}", file=sys.stderr)
            return 1
        print("PASS: documented verdicts, inferred exact types, runtime outcomes and curated values agree.")
        print("Finite development evidence; trusted host behavior and complete language conformance remain outside this gate.")
        return 0
    except (OSError, ValueError, subprocess.TimeoutExpired) as error:
        print(f"differential gate failed: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
