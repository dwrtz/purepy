# PurePy v0.2.0 release notes

PurePy now uses the functional language by default, on ordinary Python 3.14.
Running verified programs requires no PurePy runtime. Omit `language` from
configuration to use generic and recursive `typing.NamedTuple` records,
product tuples, type aliases, pure synchronous `Callable` values, stable nested
closures, rank-one generic functions, sealed `copy.replace`, and `ord`/`chr`.

The [functional language contract](FUNCTIONAL_CORE.md) describes exact rules and
conservative limits. [Executable examples](../examples/functional_core/README.md)
cover composition, folds, immutable chain construction, ASCII lowercasing, and
persistent grouping. A production balanced map and broader Unicode function
interface remain future work.

Callback provenance flows through parameters, captures, and returned functions.
Authority reports include conservative function-value and closure dependencies.
Every function is rechecked even on a warm syntax cache. Generic recursion and
type expansion are bounded; unsupported or ambiguous programs fail verification.

Only language 0.2 is supported. It uses specification `0.4-draft` and JSON schema 2. Host manifest
schema 1 and direct capability forwarding are unchanged. Higher-order effectful
or async functions and host-supplied callbacks are outside the new profile.

The reference service now runs on standard NamedTuple records. Python distribution
metadata and wheel/sdist/PyPI publishing have been removed. The former `@value`
implementation and compatibility paths have been removed. Native releases
contain the verifier, documentation, examples, source inventory, and dependency
licenses, with hashes and reproducible archive metadata.

Verification and runtime testing remain complementary. Verification does not
prove termination, prevent exceptions, or establish that external implementations
obey their declared host contracts. Historical performance reports remain evidence
for their original snapshots and do not measure this candidate.
