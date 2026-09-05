# ADR 0007: No executable verifier plugins

Status: accepted for PurePy 0.1.

External semantics are supplied through strict versioned TOML manifests.
Declarations describe modules, value categories and fixed function signatures.
Unknown fields, unknown schemas and conflicting declarations are errors. Files
are loaded in configured order, with source paths retained for trust reporting.
No host module or manifest-supplied code executes during verification.

This rejects executable hooks, dynamic schema extensions, automatic manifest
downloads and broad framework profiles. They make verification non-hermetic or
allow the trusted boundary to grow through hidden behavior. A reviewed exact
declaration is sufficient for the first-order host surface.

Reopen only when a concrete external operation cannot be represented by fixed
types and signatures without sacrificing verified application logic. The proposal
must define a declarative extension first, version its schema, expose every new
trust assertion in reports, and include malformed-input and conflict fixtures.
An executable plugin needs an independently reviewed threat model and necessity
evidence beyond serialization convenience.
