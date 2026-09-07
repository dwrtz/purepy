# PurePy schema contract

PurePy 0.2 reports specification `0.4-draft` and JSON schema 2. Only this language
is supported. The independent trusted-host manifest format remains schema 1.

| Interface | Definition |
| --- | --- |
| Decoded TOML manifests | [manifest schema 1](../manifests/schema/v1.json) and [semantic rules](MANIFESTS.md) |
| `purepy check --format json` | [check schema 2](schema/diagnostics-v2.json) |
| `purepy capabilities --format json` | [capabilities schema 2](schema/capabilities-v2.json) |
| `purepy explain --format json` | [explanation schema 2](schema/explain-v2.json) |

The definitions use JSON Schema draft 2020-12. Relative references resolve against
the adjacent check schema, so distribute this directory together. Product tuples,
generic arguments, pure callables, type variables, and nested functions have
explicit representations. Dependency edges include ordinary calls, callbacks,
function values, and closure creation. See [Functional core](FUNCTIONAL_CORE.md).

[frozen-v2.json](schema/frozen-v2.json) pins the contract bytes. Release preparation
rejects drift or mismatched implementation identifiers. A contract change requires
explicit review and an appropriate schema version change.

`tools/check_schema_contract.py` validates live successful and rejected reports,
capabilities, explanations, and reference-service manifests:

```sh
uv run --no-project --with jsonschema==4.23.0 python tools/check_schema_contract.py --verifier bin/purepy
```

Diagnostic codes retain their documented meanings. Message text, notes, paths,
counts, locations, and diagnostic-specific type roles depend on the input.
Empty arrays stay arrays. Spans use half-open byte offsets and one-based Unicode
code-point positions; unavailable locations may use zeros.

Text output, timing records, cache files, benchmark reports, and release inventories
are separate interfaces. Check JSON excludes scheduling, timing, and cache status.
Release inventories use schema 1 and SHA-256. Checksums detect changed bytes;
they are not cryptographic publisher signatures.
