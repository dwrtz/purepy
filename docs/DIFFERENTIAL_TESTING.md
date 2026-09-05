# Operator and intrinsic differential testing

Run the development gate with:

```sh
make differential-test
```

The Makefile uses `uv sync --locked` to set up the repository's `.venv` with
CPython 3.14. It builds `bin/purepy-semantic-probe`, runs the harness regression
tests, and compares generated cases against the verifier and CPython. CI runs
this target alongside the existing syntax and conformance gates.

## What is compared

Each case declares an expected PurePy verdict and, for accepted operations, an
exact result type. These expectations come from the documented
[sealed operation tables](SYNTAX_MATRIX.md), independently of the Go checker.
CPython success alone never authorizes a PurePy operation: for example, Python
accepts `True + True`, while PurePy must reject it.

The Go helper parses and lowers an isolated generated module, links its
declarations and constants, and runs the normal function checker. The generated
function assigns the expression to an unannotated local. An exact byte range
selects the type inferred for a subsequent read of that local. The expected type
is never supplied as a local or function result annotation. Direct `range`
consumption instead probes a loop variable read inside the loop body. This
adapter does not bypass the checker or add a production verification mode.

A separate CPython process evaluates the case's primitive expression and input
bindings. Accepted operations must produce the exact expected type, recursively
for tuples, or the explicit expected domain exception. The gate distinguishes
`bool` from `int`, preserves negative zero in value expectations, and handles
NaN, infinities, bytes, and typed empty tuples. Curated value assertions check
specific Python edge behavior; PurePy is a verifier and has no separate evaluator
whose computed values could be compared.

Exceptions such as division by zero, invalid conversions, invalid byte values,
and out-of-range indexing can occur under valid type signatures. They do not
mean verification should reject the program. Harness failures, malformed
responses, missing results, crashes and resource-limit failures always fail the
gate; they cannot satisfy an expected Python exception or required rejection.

## Corpus and bounds

The default corpus has **2,557 cases**: 450 accepted signatures (including 37
explicit domain failures) and 2,107 required rejections. It combines:

- Fixed Cartesian matrices over eight representative type shapes, covering
  arithmetic, bitwise, unary, Boolean, comparison, indexing, slicing, formatting,
  conditional expressions, and direct range iteration.
- Intrinsic signatures, arities, keyword and unpacking near misses, and additional
  homogeneous tuple types for every sealed intrinsic.
- Curated empty tuples, negative operands, zero divisors, 401-digit integers,
  Unicode, byte boundaries, NaN, infinities, and signed zero.
- Named Unicode character names and aliases, raw/bytes spellings, and f-string
  combinations with explicit literal value expectations.
- Three bounded integer expressions per seeded sample, using a local PRNG.
- Seven permanent regression controls independent of matrix generation.

Cases are trusted development code generated only from fixed templates and
reviewed fixture data. The gate never evaluates analyzed project files, imports
named host implementations, or executes conformance project modules. The runtime
worker restricts its AST and available builtins to primitive test machinery;
this guard is not a sandbox for arbitrary Python input. Both child processes
have a 60-second wall timeout. CPython has a 30-second CPU limit and, on Linux,
a 1 GiB address-space limit; source, case counts, literals and sequence lengths
are also bounded. Darwin retains the corpus, CPU and wall-time bounds because
lowering its address-space limit is not consistently supported.

## Reproduce and preserve a mismatch

After building the helper once, vary the supplemental values without changing
the fixed matrix:

```sh
.venv/bin/python tools/differential_semantics.py --seed 2026 --samples 256
.venv/bin/python tools/differential_semantics.py --seed 2026 --samples 256 --case seeded/017/floor
.venv/bin/python tools/differential_semantics.py --case regression/empty_float_sum_requires_start
```

`--samples` accepts 0 through 256; zero retains all fixed cases and regression
fixtures. Case names and ordering are deterministic. Successful output records
the seed, sample count, verifier version, and actual CPython patch version.

Mismatches fail the gate and write `build/differential-failures.json` (override
with `--failures PATH`). Each record retains the exact source and probe range,
input bindings, expected verdict/type/value/exception, verifier diagnostics and
inferred types, and observed CPython outcome. The file also records the versions,
seed, and sample count. Existing failure artifacts are retained after a later
successful run, so check their recorded metadata before using them.

Reduce a mismatch to one expression and the bindings needed to reproduce it,
then give it a descriptive `regression/...` name. Copy its `case` object into the
`cases` array of
[`semantic_regressions.json`](../fixtures/conformance/semantic_regressions.json).
The value encoding is lossless for supported fixture values: decimal strings for
integers, hexadecimal floats, hexadecimal bytes, and recursively tagged tuples.
The loader reads these values as data; it never evaluates value-oracle strings.
Fix the implementation or correct an independently verified expectation, then
rerun the named case and the complete gate. Do not change an oracle solely to
match the implementation.

## Evidence limits

These matrices cover finite representative type shapes and values. They do not
prove every syntax composition, nominal/optional type combination, call graph,
host behavior, platform-specific floating-point result, or termination property.
Exact nonoptional-value identity comparisons with `None` remain outside this
matrix because the spec's wording and implementation restriction need separate
resolution. Named Unicode escapes have both valid-value cases in this gate and
malformed-name cases in the shared AST syntax corpus. The separate
[Unicode data gate](UNICODE.md) validates the pinned name table against CPython.
Cache correctness and worker determinism retain their existing Go tests.

The initial default run and three larger seeded runs found no production
verifier mismatches. Existing type-boundary behavior is retained in the permanent
fixtures. Harness regressions separately ensure mismatches and protocol failures
cannot become successful comparisons. [Checker and cache fuzzing](FUZZING.md)
now have a separate bounded `make fuzz-test` gate. Longer campaigns remain release
work; neither finite target claims to complete that release gate.
