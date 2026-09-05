#!/usr/bin/env python3
"""Development-only Python 3.14 AST syntax gate; never execute fixture code.

Checks accepted conformance snippets and a shared lexical/syntax edge corpus.
The edge corpus is independently checked by the Go frontend test suite. Passing
this finite corpus is evidence of coverage, not full Python 3.14 conformance.
"""

import argparse
import ast
import json
import platform
from pathlib import Path
import sys
import warnings


ROOT = Path(__file__).resolve().parents[1]


def check_source(name, source, expected):
    try:
        # Deliberately stop at the AST. Imports, decorators, host operations and
        # function bodies in analyzed fixture source must never be executed.
        with warnings.catch_warnings():
            warnings.simplefilter("ignore", SyntaxWarning)
            ast.parse(source, filename=name, mode="exec", feature_version=(3, 14))
        accepted = True
        detail = "accepted"
    except (SyntaxError, ValueError) as exc:
        accepted = False
        detail = str(exc)
    if accepted != expected:
        expectation = "valid Python syntax" if expected else "a Python syntax error"
        return f"{name}: expected {expectation}; observed {detail}"
    return None


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--cases", type=Path, default=ROOT / "fixtures/conformance/cases.json")
    parser.add_argument("--edges", type=Path, default=ROOT / "fixtures/conformance/syntax_edges.json")
    args = parser.parse_args(argv)
    if sys.version_info[:2] != (3, 14) or platform.python_implementation() != "CPython":
        parser.error("this gate requires CPython 3.14; run python3.14 tools/differential_syntax.py")
    cases = json.loads(args.cases.read_text(encoding="utf-8"))
    edges = json.loads(args.edges.read_text(encoding="utf-8"))
    failures = []
    accepted_count = 0
    for case in cases:
        if case["valid"]:
            accepted_count += 1
            error = check_source(f"conformance:{case['name']}", case["source"], True)
            if error:
                failures.append(error)
    for edge in edges:
        error = check_source(f"syntax:{edge['name']}", edge["source"], edge["python_valid"])
        if error:
            failures.append(error)
    if failures:
        for failure in failures:
            print(failure, file=sys.stderr)
        print(f"FAILED: {len(failures)} syntax expectations disagreed with CPython {platform.python_version()}", file=sys.stderr)
        return 1
    print(f"CPython {platform.python_version()}: AST syntax gate passed for {accepted_count} accepted conformance snippets and {len(edges)} syntax edges.")
    print("Fixture source was parsed only. This finite development gate does not establish complete Python 3.14 conformance.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
