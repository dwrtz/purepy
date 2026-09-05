"""Regression tests for the non-executing coverage inventory and evidence gate."""

from pathlib import Path
import json
import sys
import tempfile
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import spec_coverage as coverage


class CoverageTests(unittest.TestCase):
    def write(self, root, path, text):
        file = root / path
        file.parent.mkdir(parents=True, exist_ok=True)
        file.write_text(text, encoding="utf-8")
        return file

    def project(self, root):
        self.write(root, "docs/PUREPY_SPEC.md", "## 1. Scope\n\nThe verifier MUST reject unknown behavior.\n")
        self.write(root, "fixtures/conformance/cases.json", json.dumps([
            {"name": "valid", "valid": True}, {"name": "invalid", "valid": False}
        ]))
        self.write(root, "internal/check/example_test.go", "package check\nimport \"testing\"\nfunc TestExact(t *testing.T) {}\n")
        row = {
            "section": "1", "quote": "The verifier MUST reject unknown behavior.",
            "status": "tested", "positive": [{"path": "internal/check/example_test.go", "test": "TestExact"}],
            "negative": [], "fixtures": ["invalid"], "notes": "Positive exact call and negative unresolved call evidence."
        }
        self.save(root, [row])
        return row

    def save(self, root, rows):
        self.write(root, "docs/conformance/example.json", json.dumps(rows))

    def test_inventory_expands_only_attached_lists_and_excludes_examples(self):
        source = """## 1. Scope
Verifier MUST:

- reject unknown calls;
- retain source locations.

Other notes:
- unrelated example.

```python
# example MUST not become an obligation
```

### 1.1 Normative language
The words MUST and SHALL are normative.

### 1.2 Versioning
New versions MUST preserve guarantees.
"""
        clauses = coverage.inventory(source)
        required = [clause for clause in clauses.values() if clause.mandatory]
        self.assertEqual(len(required), 4)
        self.assertEqual(sum(clause.parent is not None for clause in required), 2)
        self.assertNotIn(("1", "# example MUST not become an obligation"), clauses)
        self.assertFalse(clauses[("1.1", "The words MUST and SHALL are normative.")].mandatory)

    def test_numbered_outcomes_and_multiple_modal_words_share_parent(self):
        clauses = coverage.inventory("## 7. Outcomes\nEvaluation MUST return and MUST NOT mutate:\n\n1. return a value;\n2. diverge.\n")
        self.assertEqual(sum(clause.mandatory for clause in clauses.values()), 3)
        parent = clauses[("7", "Evaluation MUST return and MUST NOT mutate:")]
        self.assertEqual(clauses[("7", "1. return a value;")].parent, parent.identifier)

    def test_valid_map_and_render_are_deterministic(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.project(root)
            clauses, rows, errors = coverage.audit(root)
            self.assertEqual(errors, [])
            text = coverage.render(root, clauses, rows)
            self.assertEqual(text, coverage.render(root, clauses, rows))
            self.assertIn("1 mandatory inventory units", text)
            self.assertIn("../internal/check/example_test.go#L3", text)

    def test_unknown_or_deleted_spec_clauses_fail(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            row = self.project(root)
            row["quote"] = "The verifier MUST accept unknown behavior."
            self.save(root, [row])
            errors = coverage.audit(root)[2]
            self.assertTrue(any("source quote not found" in error for error in errors))
            self.assertTrue(any("unmapped mandatory" in error for error in errors))

    def test_new_mandatory_rule_and_list_item_fail_until_mapped(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.project(root)
            path = root / "docs/PUREPY_SPEC.md"
            path.write_text(path.read_text() + "\nCalls MUST:\n\n- resolve exactly.\n", encoding="utf-8")
            errors = coverage.audit(root)[2]
            self.assertEqual(sum("unmapped mandatory" in error for error in errors), 2)

    def test_missing_tests_fixtures_duplicate_rows_and_one_sided_claims_fail(self):
        mutations = [
            (lambda row: row["positive"][0].update(test="TestMissing"), "not found"),
            (lambda row: row["positive"][0].update(test="TestExact/missing_case"), "subtest"),
            (lambda row: row.update(fixtures=["unknown"]), "unknown fixture"),
            (lambda row: row.update(fixtures=[]), "both positive and negative"),
            (lambda row: row.update(status="proved"), "unknown evidence status"),
            (lambda row: row.update(notes=""), "notes must explain"),
        ]
        for mutate, expected in mutations:
            with self.subTest(expected=expected), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                row = self.project(root)
                mutate(row)
                self.save(root, [row])
                self.assertTrue(any(expected in error for error in coverage.audit(root)[2]))
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            row = self.project(root)
            self.save(root, [row, row])
            self.assertTrue(any("duplicate mapping" in error for error in coverage.audit(root)[2]))

    def test_partial_and_trust_obligations_remain_visible(self):
        for status in ("partial", "trust", "release", "deferred"):
            with self.subTest(status=status), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                row = self.project(root)
                row.update(status=status, positive=[], negative=[], fixtures=[])
                self.save(root, [row])
                clauses, rows, errors = coverage.audit(root)
                self.assertEqual(errors, [])
                self.assertIn(f"| {status} | 1 |", coverage.render(root, clauses, rows))

    def test_python_test_evidence_is_parsed_without_importing(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            sentinel = root / "executed"
            source = f"from pathlib import Path\nPath({str(sentinel)!r}).touch()\nclass RuntimeTests:\n    async def test_host_contract(self):\n        pass\n"
            self.write(root, "tests/example.py", source)
            line = coverage.test_location(root, {"path": "tests/example.py", "test": "RuntimeTests.test_host_contract"})
            self.assertEqual(line, 4)
            self.assertFalse(sentinel.exists())

    def test_evidence_cannot_escape_repository_or_reference_non_tests(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.project(root)
            for path in ("../outside_test.go", "/tmp/outside_test.go"):
                with self.subTest(path=path), self.assertRaises(ValueError):
                    coverage.safe_path(root, path)
            with self.assertRaises(ValueError):
                coverage.test_location(root, {"path": "internal/check/example_test.go", "test": "ExampleHelper"})

    def test_go_fuzz_evidence_requires_a_real_fuzz_signature(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            path = "internal/check/fuzz_test.go"
            self.write(root, path, "package check\nimport \"testing\"\nfunc FuzzInput(f *testing.F) {}\nfunc FuzzWrong(t *testing.T) {}\n")
            self.assertEqual(coverage.test_location(root, {"path": path, "test": "FuzzInput"}), 3)
            with self.assertRaisesRegex(ValueError, "not found"):
                coverage.test_location(root, {"path": path, "test": "FuzzWrong"})


if __name__ == "__main__":
    unittest.main()
