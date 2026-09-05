# Maintaining the conformance evidence map

The JSON files in this directory map exact specification clauses to executable
test sources and named integration fixtures. `tools/spec_coverage.py` validates
the references and produces [the coverage report](../CONFORMANCE.md).

Each row has these fields:

```json
{
  "section": "9.5",
  "quote": "Every later assignment to the same name MUST have the same exact type.",
  "status": "tested",
  "positive": [{"path": "internal/check/spec_core_test.go", "test": "TestSpecCoreAccepted"}],
  "negative": [{"path": "internal/check/spec_core_test.go", "test": "TestSpecCoreRejected"}],
  "fixtures": ["inconsistent_rebinding"],
  "notes": "Same-type rebinding is accepted; type-changing assignments are rejected."
}
```

Quotes retain the exact source line, including Markdown punctuation. List items
directly attached to a mandatory introduction each have their own row, as does
the introduction. The tool also permits rows for other exact normative prose;
these appear separately from the mandatory-keyword inventory.

`tested` means there is identified positive and negative regression evidence.
It does not establish an exhaustive proof. `partial` identifies a concrete
coverage limitation or a broad guarantee tested only through examples. `trust`
marks a host/runtime assertion beyond static verification; `release` and
`deferred` identify obligations that cannot be closed by a current fixture.
Every row must explain its scope in `notes`.

Evidence paths are relative to the repository. A Go reference names an existing
`Test...` function or a `Fuzz...` function taking `*testing.F`, optionally followed
by a literal table-case/subtest name. Go fuzz seeds run during ordinary tests;
`make fuzz-test` additionally runs bounded mutation campaigns.
For dynamically generated subtests, reference the parent test and identify the
case in the notes. Python references name a unique `test_...` function or a
`TestClass.test_method`. Sources are parsed without importing them. The tool
checks references, not verdicts: run the Go and Python test targets to execute
the evidence. `fixtures` names existing entries in the integration corpus;
their recorded valid/invalid verdict supplies positive/negative classification.

After modifying the specification, test names, or mappings:

```sh
make setup
.venv/bin/python tools/spec_coverage.py --write
make coverage-test
```

CI runs the evidence-map check alongside the verifier, runtime, service, and
syntax tests. New mandatory clauses, missing list items, stale quotations,
deleted tests, nonexistent fixtures, unsupported status values, duplicate rows,
and one-sided `tested` claims fail the gate. Open obligations remain visible in
the generated report; a passing gate means the map is current, not that every
PurePy 0.1 release obligation has been discharged.
