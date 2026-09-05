# Expression and whole-function differential testing

Run the development gate with:

```sh
make differential-test
```

The Makefile uses `uv sync --locked` to set up the repository's `.venv` with
CPython 3.14. It builds `bin/purepy-semantic-probe`, runs the harness regression
tests, and compares generated cases against the verifier and CPython. CI runs
this target alongside the existing syntax and conformance gates.

## Expression comparisons

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

## Expression corpus and shared process bounds

The default corpus has **5,948 cases**: 794 accepted signatures (including 37
explicit domain failures) and 5,154 required rejections. It combines:

- Fixed Cartesian matrices over eight representative type shapes, covering
  arithmetic, bitwise, unary, Boolean, comparison, indexing, slicing, formatting,
  conditional expressions, and direct range iteration.
- Intrinsic signatures, arities, keyword and unpacking near misses, and additional
  homogeneous tuple types for every sealed intrinsic.
- Curated empty tuples, negative operands, zero divisors, 401-digit integers,
  Unicode, byte boundaries, NaN, infinities, and signed zero.
- Named Unicode character names and aliases, raw/bytes spellings, and f-string
  combinations with explicit literal value expectations.
- A 3,400-case identity matrix over exact and optional primitive/tuple types,
  including both `None` operand orders, typed `None` bindings, and chains. Every
  accepted identity case has an explicit expected Boolean value. Typed empty
  tuples are established before optional widening so these comparisons do not
  depend on inferring an empty tuple through an optional context.
- Three bounded integer expressions per seeded sample, using a local PRNG.
- Fourteen permanent regression controls independent of matrix generation.

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
`None` identity cases cover both operand orders, exact and optional primitive or
tuple values, chains, and rejection of identity pairs without an exact `None`
operand. Go conformance tests separately cover nominal and opaque immutable
values, authority categories, and optional flow. Named Unicode escapes have both
valid-value cases in this gate and malformed-name cases in the shared AST syntax
corpus. The separate
[Unicode data gate](UNICODE.md) validates the pinned name table against CPython.
Cache correctness and worker determinism retain their existing Go tests.

The initial default run and three larger seeded runs found no production
verifier mismatches. Existing type-boundary behavior is retained in the permanent
fixtures. Harness regressions separately ensure mismatches and protocol failures
cannot become successful comparisons. [Checker and cache fuzzing](FUZZING.md)
now have a separate bounded `make fuzz-test` gate. Longer campaigns remain release
work; neither finite target claims to complete that release gate.

## Whole-function comparisons

The same `make differential-test` target also runs
[`differential_functions.py`](../tools/differential_functions.py). Each case
contains a complete synchronous `probe` function, optionally with acyclic helper
functions, and several typed input tuples. The normal Go adapter checks every
function in the module. The parent requires a documented acceptance verdict or
rejection with at least one specified diagnostic code. An unrelated syntax error
cannot satisfy a flow-error expectation, and `PP099` always fails the gate.

The isolated CPython worker executes the generated functions on those inputs.
Every normal return from an accepted function must have its exact declared type,
recursively through optional and homogeneous tuple types. `bool` does not count
as `int`. Every default invocation also has an independently specified value or
domain-exception expectation, including the runtime witnesses in rejected cases.
Expected exceptions apply only to the corresponding invocation; an exception on
another input still fails. The worker receives source, signature metadata and
inputs, with all value and verdict oracles retained in the parent.

The default seed has **73 functions and 428 invocations**: 64 required acceptances
and 9 required rejections. Its 41 permanent cases cover optional guards and
short-circuit conditions, branch joins and definite assignment, exact return
types, implicit `None` returns, tuple/range iteration, bounded `while` loops,
`break`, `continue`, early returns, nested loops, and primitive/optional tuple
payloads passed through helpers. Four permanent regressions preserve loop-target
refinement loss through both exit statements and iterable evaluation before
body rebinding for tuple and range loops. The 32 seeded cases vary guard polarity,
loop kind and exit ordering, payload type, and helper depth. `--samples 256`
produces 297 functions; invocation counts depend on the seed.

The runtime independently parses signature annotations and checks exact argument
types before invocation. Its AST guard rejects imports, module initialization,
decorators, attributes, nested functions, recursion, computed calls, and builtin
or function shadowing. Each invocation has a 20,000-event execution budget;
budget failures escape the domain-exception handler. The shared CPU, memory and
wall-time limits also apply, including to work performed inside native builtins.
Additional bounds are 2,000 cases per request, 32 KiB source and 2,000 AST nodes
per case, 8 top-level functions, 16 parameters, and 64 invocations. Inputs,
literals and returned values are limited to 256-bit integers, 128-character
strings/bytes, and tuples of at most 16 elements with depth at most four.

Run and replay specific cases after building the adapter:

```sh
.venv/bin/python tools/differential_functions.py --seed 2026 --samples 256
.venv/bin/python tools/differential_functions.py --samples 0 --case regression/for_target_break_optional
.venv/bin/python tools/differential_functions.py --replay build/function-differential-failures.json
```

Whole-function mismatches write `build/function-differential-failures.json`.
Each record retains the complete source, signature, ordered typed inputs,
expected verdict/codes and invocation outcomes, verifier diagnostics, and actual
runtime outcomes. Child-process and protocol failures also preserve the selected
inputs, with unavailable outputs marked null. CI uploads this artifact alongside
the expression failures. `--replay` reads these records as data and reruns their
exact source and inputs through the same guards; use only trusted development
artifacts. Successful runs retain earlier artifacts. Promote minimized failures
to named permanent templates and focused checker regressions after independently
establishing the expected behavior.

This extends differential evidence to selected whole-function compositions and
paths. It does not prove all inputs, all path combinations, termination, async
effects, nominal/opaque values, module imports, or host behavior. The opcode and
line-event budget is a development guard, not a verifier termination guarantee.
