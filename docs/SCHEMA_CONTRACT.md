# PurePy 0.1 schema contract

The current manifest and JSON shapes are frozen as version 1 for the 0.1 release
line. This declares a compatibility contract; it does not certify the verifier
or remove the host trust assumptions in the specification. The language is
`0.1`, the specification identifier remains `0.3-draft`, and the verifier and
Python support distribution have separate version identifiers.

`tools/check_schema_contract.py` validates live successful and rejected check
reports, capabilities, explanations, and reference-service manifests against
all four schemas. Run it through the pinned tooling environment:

```sh
uv run --no-project --with jsonschema==4.23.0 python tools/check_schema_contract.py --verifier bin/purepy
```

| Interface | Frozen definition |
| --- | --- |
| Decoded TOML manifests | [manifest schema 1](../manifests/schema/v1.json) and [semantic constraints](MANIFESTS.md) |
| `purepy check --format json` | [check schema 1](schema/diagnostics-v1.json) |
| `purepy capabilities --format json` | [capabilities schema 1](schema/capabilities-v1.json) |
| `purepy explain --format json` | [explanation schema 1](schema/explain-v1.json) |

The JSON definitions use JSON Schema draft 2020-12. Relative references in the
capabilities and explanation definitions resolve against the adjacent check
schema. Distribute this directory together. Manifest JSON Schema describes the
shape after TOML decoding; qualified names, Unicode normalization, type grammar,
module relationships, declaration uniqueness and trust checks remain semantic
loader obligations documented in `MANIFESTS.md` and covered by Go tests.

The schema files are pinned by [frozen-v1.json](schema/frozen-v1.json). Release
preparation rejects changes to their bytes or implementation schema identifiers
without a matching explicit contract update. This prevents accidental schema
drift; a reviewer must still decide whether a change is compatible. Add/remove
fields, change a field's type or accepted shape, or change the interpretation of
an existing field only under a new schema version. Documentation-only schema
changes may update the pin after review without changing the wire version.

Diagnostic codes retain their documented meanings. Message text, explanatory
notes, file paths, counts and source locations depend on the verified input.
The optional `types` object accepts new diagnostic-specific role names under the
already frozen shape. Do not parse messages or assume an exhaustive list of type
roles. Empty arrays stay arrays. Spans use half-open byte offsets and one-based
Unicode code-point positions; unavailable locations may use zeros.

Text output and timing records are separate interfaces: check JSON excludes
stage timing, worker scheduling and cache-hit status. Timing schema 2 follows
[the performance guide](PERFORMANCE.md). Cache files and benchmark/differential
harness reports are implementation artifacts, not public verification results.
Their own versions may advance without changing manifest or verification schema
1. Release inventories use schema 1 and SHA-256; hashes identify bytes and detect
accidental corruption, but are not cryptographic publisher signatures.
