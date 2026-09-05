"""Validate and render the specification-to-test evidence map, without execution.

Inventory units are mandatory keyword-bearing lines and their directly attached
list items, outside fenced examples and the normative-keyword glossary. The map
may additionally cite exact prose restrictions. Accounting for an obligation is
not proof that an implementation satisfies it: partial, trust, and release rows
remain visible. This tool parses test sources but never imports or executes them.
"""

from __future__ import annotations

import argparse
import ast
from collections import Counter
from dataclasses import dataclass
from hashlib import sha256
import json
from pathlib import Path
import re
import sys


ROOT = Path(__file__).resolve().parents[1]
SPEC = Path("docs/PUREPY_SPEC.md")
MAP_DIR = Path("docs/conformance")
REPORT = Path("docs/CONFORMANCE.md")
STATUSES = {"tested", "partial", "trust", "release", "deferred"}
FIELDS = {"section", "quote", "status", "positive", "negative", "fixtures", "notes"}
MANDATORY = re.compile(r"\b(?:MUST|REQUIRED|SHALL)\b")
HEADING = re.compile(r"^#{2,3} (\d+(?:\.\d+)*)(?:\.)? ")
LIST_ITEM = re.compile(r"^(?:- |\d+\. )")


@dataclass(frozen=True)
class Clause:
    section: str
    quote: str
    line: int
    mandatory: bool
    parent: str | None = None

    @property
    def key(self):
        return self.section, self.quote

    @property
    def identifier(self):
        digest = sha256(self.quote.encode("utf-8")).hexdigest()[:8]
        return f"S{self.section}-{digest}"


def inventory(source):
    """Return exact non-example source clauses, marking mandatory obligations."""
    lines = source.splitlines()
    section = "1"
    fenced = False
    clauses = {}
    eligible = []
    for i, line in enumerate(lines):
        if line.lstrip().startswith("```"):
            fenced = not fenced
            continue
        if fenced:
            continue
        match = HEADING.match(line)
        if match:
            section = match.group(1)
        if not line.strip() or line.startswith("#") or line == "---":
            continue
        mandatory = section != "1.1" and bool(MANDATORY.search(line))
        clause = Clause(section, line, i + 1, mandatory)
        # Identical prose in one section has the same semantic inventory unit;
        # retain its first location so generated reports remain stable.
        clauses.setdefault(clause.key, clause)
        if mandatory and line.endswith(":"):
            eligible.append(clause)
    for parent in eligible:
        i = parent.line
        while i < len(lines) and not lines[i].strip():
            i += 1
        while i < len(lines) and LIST_ITEM.match(lines[i]):
            item = Clause(parent.section, lines[i], i + 1, True, parent.identifier)
            clauses[item.key] = item
            i += 1
            while i < len(lines) and not lines[i].strip():
                i += 1
    return clauses


def safe_path(root, relative):
    if not isinstance(relative, str) or Path(relative).is_absolute():
        raise ValueError(f"expected repository-relative path, got {relative!r}")
    path = (root / relative).resolve()
    if not path.is_relative_to(root.resolve()):
        raise ValueError(f"path escapes repository: {relative}")
    return path


def test_location(root, evidence):
    if not isinstance(evidence, dict) or set(evidence) != {"path", "test"}:
        raise ValueError("test evidence requires exactly path and test")
    path = safe_path(root, evidence["path"])
    name = evidence["test"]
    if not isinstance(name, str) or not name:
        raise ValueError("test selector must be a nonempty string")
    text = path.read_text(encoding="utf-8")
    if path.name.endswith("_test.go"):
        parts = name.split("/")
        function = parts[0]
        argument = "F" if function.startswith("Fuzz") else "T"
        match = re.search(r"(?m)^func " + re.escape(function) + r"\(\s*\w+ \*testing\." + argument + r"\)", text)
        if not function.startswith(("Test", "Fuzz")) or match is None:
            raise ValueError(f"Go test {name!r} not found in {evidence['path']}")
        # Statically named table cases and t.Run strings can be checked without
        # running Go. Use the parent function for dynamically generated names.
        for subtest in parts[1:]:
            if not subtest or json.dumps(subtest, ensure_ascii=False) not in text:
                raise ValueError(f"static Go subtest {subtest!r} not found in {evidence['path']}; reference its parent test for dynamic selectors")
        return text.count("\n", 0, match.start()) + 1
    if path.suffix == ".py":
        tree = ast.parse(text, filename=str(path))
        parts = name.split(".")
        if len(parts) == 1:
            candidates = [node for node in ast.walk(tree) if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef))]
        elif len(parts) == 2:
            candidates = [method for node in tree.body if isinstance(node, ast.ClassDef) and node.name == parts[0] for method in node.body if isinstance(method, (ast.FunctionDef, ast.AsyncFunctionDef))]
        else:
            candidates = []
        hits = [node for node in candidates if node.name == parts[-1] and node.name.startswith("test_")]
        if len(hits) != 1:
            raise ValueError(f"Python test {name!r} not found unambiguously in {evidence['path']}")
        return hits[0].lineno
    raise ValueError(f"evidence is not a Go or Python test file: {evidence['path']}")


def audit(root=ROOT):
    source = (root / SPEC).read_text(encoding="utf-8")
    clauses = inventory(source)
    errors = []
    rows = []
    seen = set()
    fixtures_list = json.loads((root / "fixtures/conformance/cases.json").read_text(encoding="utf-8"))
    fixtures = {item["name"]: item for item in fixtures_list}
    if len(fixtures) != len(fixtures_list):
        errors.append("duplicate conformance fixture names")
    maps = sorted((root / MAP_DIR).glob("*.json"))
    if not maps:
        errors.append("no evidence maps found")
    for path in maps:
        try:
            entries = json.loads(path.read_text(encoding="utf-8"))
            if not isinstance(entries, list):
                raise ValueError("evidence map must be a JSON array")
        except (ValueError, OSError) as error:
            errors.append(f"{path.relative_to(root)}: {error}")
            continue
        for number, row in enumerate(entries, 1):
            label = f"{path.relative_to(root)} entry {number}"
            try:
                if not isinstance(row, dict) or set(row) != FIELDS:
                    raise ValueError(f"entry requires exactly {', '.join(sorted(FIELDS))}")
                if not isinstance(row["section"], str) or not isinstance(row["quote"], str):
                    raise ValueError("section and quote must be strings")
                key = row["section"], row["quote"]
                clause = clauses.get(key)
                if clause is None:
                    raise ValueError(f"source quote not found verbatim in section {row['section']}")
                if key in seen:
                    raise ValueError(f"duplicate mapping for {clause.identifier}")
                seen.add(key)
                if row["status"] not in STATUSES:
                    raise ValueError("unknown evidence status")
                if not isinstance(row["notes"], str) or not row["notes"].strip():
                    raise ValueError("notes must explain the evidence and its limits")
                for kind in ("positive", "negative", "fixtures"):
                    if not isinstance(row[kind], list):
                        raise ValueError(f"{kind} must be an array")
                for evidence in row["positive"] + row["negative"]:
                    test_location(root, evidence)
                for fixture in row["fixtures"]:
                    if fixture not in fixtures:
                        raise ValueError(f"unknown fixture {fixture!r}")
                positive = bool(row["positive"]) or any(fixtures[name]["valid"] for name in row["fixtures"])
                negative = bool(row["negative"]) or any(not fixtures[name]["valid"] for name in row["fixtures"])
                if row["status"] == "tested" and not (positive and negative):
                    raise ValueError("tested requires both positive and negative evidence; use partial for a remaining gap")
                rows.append((clause, row))
            except (ValueError, OSError, TypeError) as error:
                errors.append(f"{label}: {error}")
    for key, clause in clauses.items():
        if clause.mandatory and key not in seen:
            errors.append(f"unmapped mandatory obligation {clause.identifier} at {SPEC}:{clause.line}: {clause.quote}")
    rows.sort(key=lambda pair: (pair[0].line, pair[0].quote))
    return clauses, rows, errors


def render(root, clauses, rows):
    mandatory = [row for clause, row in rows if clause.mandatory]
    counts = Counter(row["status"] for row in mandatory)
    extra = len(rows) - len(mandatory)
    lines = [
        "# Specification-to-test coverage audit", "",
        "Generated by `tools/spec_coverage.py --write`. Edit the evidence in",
        "`docs/conformance/*.json`, then regenerate this file. `make coverage-test`",
        "rejects missing mandatory clauses, stale quotes, unknown fixtures or test",
        "references, and an out-of-date report. It does not run the referenced tests;",
        "the Go, runtime, service, and syntax targets supply execution evidence.", "",
        "## What is counted", "",
        "The mandatory inventory includes every `MUST`, `MUST NOT`, `REQUIRED`, or",
        "`SHALL` line outside examples and the keyword glossary, plus each item in",
        "a list directly introduced by such a line. Multiple obligations in one",
        "sentence remain one inventory unit. Additional prose restrictions are",
        "mapped explicitly; keyword counting does not enumerate every possible",
        "Python program or prove the complete language contract.", "",
        f"The map accounts for **{len(mandatory)} mandatory inventory units** and",
        f"**{extra} additional prose rules**. These counts describe traceability, not",
        "a percentage of semantic correctness.", "",
        "| Mandatory evidence status | Count | Meaning |", "| --- | ---: | --- |",
    ]
    meanings = {
        "tested": "Focused positive and negative regression evidence exists; not a proof.",
        "partial": "Some evidence exists; the notes identify uncovered behavior or a broad guarantee.",
        "trust": "The obligation relies on a runtime/host contract the verifier cannot establish.",
        "release": "A completion or publication gate remains open.",
        "deferred": "An obligation for a later language version, outside the current implementation.",
    }
    for status in ("tested", "partial", "trust", "release", "deferred"):
        lines.append(f"| {status} | {counts[status]} | {meanings[status]} |")
    lines.extend(["", "## Open evidence and scope limits", ""])
    for clause, row in rows:
        if row["status"] != "tested":
            lines.append(f"- [{clause.identifier}](#{clause.identifier.lower().replace('.', '')}) ({row['status']}): {row['notes']}")
    lines.extend(["", "## Rule evidence", ""])
    for clause, row in rows:
        lines.extend([f"### {clause.identifier}", "", f"Section {clause.section}; **{row['status']}**; " + ("mandatory" if clause.mandatory else "additional prose rule") + f". [Specification](PUREPY_SPEC.md#L{clause.line})", "", "> " + clause.quote, ""])
        if clause.parent:
            lines.extend([f"List obligation introduced by {clause.parent}.", ""])
        for kind in ("positive", "negative"):
            if row[kind]:
                links = []
                for evidence in row[kind]:
                    line = test_location(root, evidence)
                    links.append(f"[{evidence['test']}](../{evidence['path']}#L{line})")
                lines.extend([f"{kind.capitalize()}: " + "; ".join(links) + ".", ""])
        if row["fixtures"]:
            names = ", ".join(f"`{name}`" for name in row["fixtures"])
            lines.extend([f"[Conformance fixtures](../fixtures/conformance/cases.json): {names}.", ""])
        lines.extend([row["notes"], ""])
    return "\n".join(lines)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument("--write", action="store_true", help="regenerate docs/CONFORMANCE.md after validating maps")
    mode.add_argument("--check", action="store_true", help="also reject a stale generated report")
    args = parser.parse_args(argv)
    clauses, rows, errors = audit()
    if errors:
        for error in errors:
            print(error, file=sys.stderr)
        return 1
    report = render(ROOT, clauses, rows)
    path = ROOT / REPORT
    if args.write:
        path.write_text(report, encoding="utf-8")
    elif args.check and (not path.is_file() or path.read_text(encoding="utf-8") != report):
        print(f"{REPORT} is stale; run tools/spec_coverage.py --write", file=sys.stderr)
        return 1
    mandatory = [row for clause, row in rows if clause.mandatory]
    counts = Counter(row["status"] for row in mandatory)
    print(f"Mapped {len(mandatory)} mandatory inventory units and {len(rows) - len(mandatory)} additional prose rules.")
    print("Mandatory evidence: " + ", ".join(f"{key}={counts[key]}" for key in sorted(STATUSES)) + ".")
    print("Evidence references are valid. Partial, trust, and release obligations remain explicit; this is not a conformance proof.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
