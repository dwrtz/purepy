# PurePy 0.1 release notes

This release line implements the PurePy 0.1 language against specification
`0.3-draft`. The verifier's authoritative version is in
`internal/app/check.go`; the independently versioned Python runtime's name and
version are in `python/pyproject.toml`. Release archives record both identifiers.
A `-dev` verifier identifier denotes a development candidate, not a final tag.

The verifier checks Python 3.14 syntax without importing analyzed code. It
implements exact primitive, homogeneous tuple and optional types, finite nominal
`@value` records, deterministic module linking, sealed operations, direct known
calls, capability/host-reference forwarding and direct-await async composition.
Closed configuration and manifest schemas reject unknown fields, aliases and
coerced container shapes. Resource limits reject excessive inputs, and corrupt
cache entries fall back to reanalysis. Structured diagnostics, explanations and
authority reports share the frozen version-1 contracts.

The Python runtime supplies immutable records; selected CPython 3.12, 3.13 and
3.14 support is exercised by the release workflow. The reference service keeps
network, database and resource lifecycle mechanics in an explicit trusted host.
The source archive contains conformance tests, differential harnesses, fuzz seeds,
benchmark tooling and recorded service reports. Reports identify their original
source/binary fingerprints and remain finite evidence for those recorded inputs.
They should not be read as measurements of every later build.

Native release artifacts are prepared for Linux amd64 and macOS arm64. The
source archive supports building on other environments, but those environments
are outside this initial binary validation matrix. Archives include schemas,
usage/implementation documentation, the specification and plan, conformance
maps, reference service and available validation reports. Every artifact is
indexed and checksummed. The packaging workflow repeats builds and compares
bytes before artifact publication.

Deferred language features remain explicit exclusions:

- Mutable lists/dicts/sets, comprehensions, generators and local builder scopes.
- User-defined classes beyond data-only records, inheritance, protocols,
  descriptors, dynamic dispatch, callbacks, higher-order functions and generics.
- General unions, recursive data types, pattern matching and unrestricted
  container methods; implicit mixed numeric arithmetic and exponentiation.
- Dynamic/relative/star imports, aliases, package re-exports, executable module
  initialization and reflective/evaluation primitives.
- Exception handling and user-visible exception values; exceptions from approved
  operations still terminate the current execution normally under Python rules.
- Coroutine/task/stream values, async iterators and context managers, task spawn,
  detached work, general cancellation APIs and `parallel_join`.
- Lexically scoped resources, affine/linear ownership, borrowing, cleanup proofs,
  lock APIs, callback lifetime verification and host-reference inspection.
- In-process memoization, semantic-body cache hashes and fine-grained dependent
  body invalidation beyond the documented cache model.
- Executable verifier plugins, dependency resolution, package registries,
  framework APIs and source-level `unsafe` blocks.

PurePy verifies admitted source operations under declared host assertions. It
cannot prove an external implementation obeys its manifest, prevent hostile
reflection by unverified Python, or guarantee liveness, cancellation cleanup or
resource bounds of the host. See [the trust boundary](HOST_BOUNDARY.md), the
[implementation contract](IMPLEMENTATION.md), and [conformance evidence](CONFORMANCE.md).
