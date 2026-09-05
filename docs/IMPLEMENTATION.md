# Implementation and conformance status

The repository now implements the PurePy 0.1 design as an experimental Go verifier
(`0.1.0-dev`). The normative source remains `PUREPY_SPEC.md`; this file records the
concrete choices and remaining release gates without silently changing that spec.

## Implemented pipeline

1. Strict TOML configuration and version validation, project-contained paths, and
   deterministic package/module discovery. Namespace packages, symlinks inside the
   project boundary, and ambiguous module mappings are rejected.
2. A pinned tree-sitter Python grammar, Python layout/token validity checks, UTF-8
   source spans, NFKC identifier normalization, and parser-independent syntax IR.
   Parser handles never leave `internal/frontend`.
3. Stable qualified-name declaration identities, direct imports, import-cycle
   rejection, exact annotations and signatures, immutable records and constants,
   and initialization-order checks.
4. Exact primitive operations, tuple/optional types, record construction and fields,
   local flow and definite assignment, first-order recursion/calls, and explicit
   unsupported-node rejection. Both branches and unreachable source are checked.
5. Schema-v1 manifests, import-safety assertions, deeply immutable opaque values,
   exact capability/host-reference forwarding, and trust/authority reports.
6. Direct async calls with no first-class coroutine values. The host owns scheduling,
   resource lifetime, and cancellation.
7. Bounded parallel parsing/checking, checksummed content-addressed module summaries,
   conservative image inputs, mandatory relinking/rechecking, deterministic text
   and JSON reports, source explanations, and safe artifact cleanup.
8. The frozen/slotted `@value` support package, reference network/database/SSE
   service, runtime and socket integration tests, load harness, CI, and packaging.

Qualified names act as stable declaration IDs without an extra hash interner.
The first summary schema retains normalized syntax bodies and source spans; it
does not cache authorization decisions. Every function is rechecked against current
dependencies, including on warm runs.

## Concrete closed-language choices

- Only UTF-8 source is accepted. Named Unicode escapes (`\N{...}`) currently reject
  with PP003; literal Unicode and numeric escapes work. This is a documented gap
  from the broader primitive-literal wording in the draft spec.
- Integer/float mixed arithmetic requires an explicit conversion. Power is rejected
  because its Python return category depends on operand values (`int`, `float`, or
  `complex`). Sequence repetition is outside the initial table.
- F-strings accept the exact primitive types in the syntax matrix; nonempty format
  specifications, conversion flags, and debug/repr forms reject. Empty `:` is safe.
- Floating-point `sum` requires an explicit float start so an empty tuple still
  returns float. Opaque manifest values have no intrinsic comparison operations;
  equality of records containing them is also rejected to prevent hidden dispatch.
- Local and parameter names cannot shadow module declarations. Optional narrowing
  uses exact `is None` / `is not None` guards. Loop joins conservatively discard
  refinements for values written by an iteration.
- No broad standard-library manifest ships. The sealed intrinsic table and the
  explicit example host manifest are the initial interoperation surface.
- The `@value` runtime trusts the verifier's annotations and the host's exact-value
  obligations. It avoids evaluating deferred annotations during decoration, which
  permits Python 3.14 forward record references and trusted opaque external fields.

## Verification evidence

The repository contains 89 named integration conformance cases plus parser,
configuration, discovery, manifest, cache, CLI, and adversarial category/flow tests.
Positive fixtures are checked as a full program image; negative fixtures assert
stable diagnostic families. The example is also a conformance gate.

Cold/warm/no-cache and worker-count comparisons assert identical JSON output.
Dependency signatures and manifest edits are exercised. Corrupt, truncated, and
malformed cache summaries fall back to parsing. Sentinel projects demonstrate
that analyzed imports, decorators, and module statements never execute.

Runtime tests cover immutability, nominal equality, field order, no inheritance,
and deferred/external annotations. Service tests cover parallel reads, concurrent
atomic writes, rollback, parsing, SSE events, cancellation, and host cleanup.
Fuzz targets exercise parsing, manifest type syntax, and other data boundaries.

The [specification-to-test audit](CONFORMANCE.md) maps mandatory keyword-bearing
rules and their attached list items, plus additional prose restrictions, to named
positive and negative tests. `make coverage-test` checks source quotes, evidence
references, missing rules, and the generated report in CI. The map distinguishes
focused coverage from partial evidence, host trust contracts, and open release
gates; its counts are not a correctness percentage.

The audit added focused declaration, expression, control-flow, boundary, and
tooling regressions. It also fixed function-wide local types being forgotten after
a returning branch, missing import-safety module provenance in trust reports,
incomplete configuration and manifest error locations, diagnostic tie ordering,
and missing specification/syntax version metadata.

## Release gates and trust limits

This is an experimental implementation, not a declaration that every PurePy 0.1
release gate in the plan has been completed. Schemas are versioned but have not
been declared frozen for public release. A broader independent soundness audit,
long-running fuzz campaigns, exhaustive CPython 3.14 differential coverage,
large-repository performance targets, and public binary/support-package publication
remain release work. The packaging workflow prepares artifacts when explicitly run;
no release is published by the implementation task.

External implementations and their manifests remain trusted. The verifier does not
prove their purity, deep immutability, import safety, or complete effect labels.
The runtime, Python platform, intrinsic semantics, and verifier are also part of
the trusted computing base. Content-cache files are local disposable artifacts,
not signed certificates or portable security proofs.

Post-0.1 builders, parallel joins, lexical resources, memoization, streams, and
general ownership/concurrency remain deliberately unimplemented as the plan requires.
