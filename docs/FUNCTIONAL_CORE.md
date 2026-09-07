# Functional core: language 0.2

PurePy 0.2 verifies ordinary Python 3.14 programs. Running verified programs
requires Python and their declared host dependencies; it does not require a
PurePy package, runtime, decorator, interpreter, or generated code. This is the
standard language: omit `language` from `purepy.toml`. An explicit
`language = "0.2"` is an optional version pin. Other versions are rejected.

This document is part of the [language specification](PUREPY_SPEC.md). PurePy reports specification
`0.4-draft` and JSON schema 2. The manifest format remains version 1.

## Immutable records and products

Use the exact standard-library declaration `from typing import NamedTuple`.
A record has exactly that base and contains annotated fields, optionally preceded
by a docstring. Defaults, methods, decorators, additional bases, class options,
private fields, and subclassing are rejected. `@value` is not part of 0.2.

```python
from typing import NamedTuple

class Link[T](NamedTuple):
    head: T
    tail: Link[T] | None

type Chain[T] = Link[T] | None
```

Record fields contain immutable data, never functions, capabilities, host
references, lists, or dictionaries. A type parameter ranges over immutable data.
Generic records require explicit arguments in annotations; constructor arguments
and the expected result type determine constructor specialization. Record field
reads substitute the receiver's exact type arguments.

Recursive record references are nominal: the verifier does not expand an infinite
layout. Recursive cycles must preserve type parameters in their original
positions. Expanding recursion such as `Link[tuple[T, ...]]` is rejected. Type
aliases use `type Name[T] = ...`; alias cycles must pass through a named record.
There are no bounds, defaults, constraints, variadic parameters, subtyping, or
arbitrary unions. Optional data continues to use `T | None`.

`tuple[A, B]` is an immutable product; `tuple[T, ...]` remains a homogeneous
sequence. Product literals use an explicit contextual product annotation, and
product access requires a constant integer index within bounds. Negative indices
are supported. Dynamic indexing, iteration, and slicing of heterogeneous products
are outside this profile. Functions cannot be stored inside either kind of tuple.

`from copy import replace` admits `replace(record, field=value, ...)` for an exact
verified record, with explicit, distinct field keywords and the exact declared
field types. Unknown fields and argument unpacking are rejected. This rule does
not admit arbitrary `__replace__` dispatch. Python creates a new record and shares
its unchanged immutable fields.

NamedTuple uses Python's tuple equality at runtime. Verified comparisons require
the same exact record type, including generic arguments, and recursively sealed
equality for all fields. Code migrating from `@value` must account for this
standard-library behavior when records are compared by unverified host code.

## Pure function values and composition

Use `from collections.abc import Callable` and explicit signatures such as
`Callable[[int, str], bool]`. These are pure synchronous functions whose arguments
and results are immutable data or other pure synchronous functions. Function
values originate in linked declarations and checked nested definitions; a type
annotation alone cannot introduce an unknown callable.

```python
from collections.abc import Callable

def compose[A, B, C](
    outer: Callable[[B], C], inner: Callable[[A], B]
) -> Callable[[A], C]:
    def composed(argument: A) -> C:
        return outer(inner(argument))
    return composed
```

Named pure functions may be passed, returned, assigned to locals, and invoked by
name. Invocation through `Callable` uses positional arguments. Methods, callable
objects, lambdas, computed call targets, reflection, identity tests, and equality
of functions are rejected. Async functions remain directly awaited declarations;
higher-order async or effectful signatures are rejected.

Configured entrypoints have concrete signatures: data or existing explicit
capability/host-reference parameters, and data results. Generic entrypoints,
host-supplied callbacks, and function results across this boundary are rejected.
Manifest functions retain their existing contracts and cannot return callbacks.
A trusted pure manifest function can be used as a function value, and its trust
declaration remains visible in reports.

Pure synchronous generic top-level functions declare rank-one type parameters with Python's
`def f[T](...)` syntax. Bodies are checked once with rigid data variables. Calls
infer exact substitutions from the result context and data arguments before
checking callback arguments. Generic function values need a sufficiently precise
`Callable` context. Add an annotation when inference is ambiguous. Generic
operations cannot assume arithmetic, equality, or methods on an unconstrained
parameter. Recursive calls, including mutual recursion, preserve type parameters;
polymorphic recursion is rejected. Nested definitions are monomorphic and may use
the enclosing function's type parameters.

A nested function may capture assigned immutable data or a pure function. Captured
parameters cannot be rebound; captured locals must have a single assignment.
Bindings must be established before closure creation. Loop targets, changing
accumulators, capabilities, and host references cannot be captured. Definitions
inside loops and reassigned nested-function names are rejected. These intentionally
conservative rules avoid Python's changing closure cells without ownership or
escape analysis. Ordinary local rebinding of immutable data and existing loops
remain available outside captures.

## Strings, collection construction, and grouping

`ord(str) -> int` and `chr(int) -> str` are sealed free-function intrinsics.
They use Python's existing semantics and exceptions: verification does not prove
that `ord` receives exactly one character or that a code point is in range.
String indexing, slicing, comparison, and concatenation retain their existing
rules. There is no new method allowlist or PurePy string library.

The [executable example](../examples/functional_core/src/core.py) implements:

- Tuple and chain folds, composition, map, filter, and reverse as verified source.
- Linear chain construction by immutable prepend followed by reverse, avoiding
  repeated tuple copying and mutable builders.
- ASCII lowercasing using `ord`/`chr` and divide-and-conquer string concatenation.
- Grouping into a persistent search tree and preserving first-seen key order.

These are application examples, not a published PurePy collection API. The grouping
tree is unbalanced: sorted keys can produce linear depth, poor performance, and a
Python recursion-limit error. A production balanced map and a broader Unicode
function interface remain separate design work. ASCII lowercasing is deliberately
not presented as Unicode case conversion.

## Verification, reports, and limits

Every project function is checked, including unreachable functions and nested
bodies. Callback provenance flows through parameters, local aliases, captures,
and returned closures. Reports retain ordinary calls plus `callback`, `closure`,
and `function_value` dependencies. The latter two conservatively include latent
work even when a function is created but never invoked. Parameter provenance is
context-insensitive: a function report includes all verified callbacks passed to
it in the project. An uninstantiated internal abstraction can retain a symbolic
`$param:` dependency. No configured entrypoint accepts such an unknown callback.

Cache entries contain parsed syntax, not successful semantic judgments. Linking,
body checking, provenance, and trust analysis rerun on every check, including warm
cache runs. Language selection and the parser revision participate in cache
identity. JSON schema 2 describes product, callable, and type-variable shapes,
generic arguments, nested definitions, and additional dependency kinds. The schema 2 files and hashes define the current report contract.

The existing source and syntax limits still apply. Each expanded type is limited
to 4,096 nodes and 128 levels, including repeated shared subtrees. Annotation expansion is bounded
at 100,000 visits per linked program (and per local annotation); record analysis at
100,000 visits and 128 levels; callback provenance and recursive generic dependency
analysis each have a 1,000,000-step budget. Exceeding a budget rejects verification.
Recursive equality analysis is conservative and bounded. None of these checks
prove termination, prevent runtime exceptions, or replace application tests.

Run the example without importing PurePy:

```sh
bin/purepy check examples/functional_core
python3 -I -S -B examples/functional_core/check_runtime.py
```

PurePy is distributed through GitHub releases. Setup and release workflows contain
no Python runtime package, wheel, sdist, or PyPI publishing step.
