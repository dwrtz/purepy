# PurePy 0.1 syntax matrix

PurePy uses Python 3.14 syntax with closed PurePy semantics. Parser acceptance
alone does not establish verification. The tables enumerate statement,
expression, declaration and annotation families; an unlisted operation is
rejected. Restrictions apply recursively, including inside unreachable code.
The implementation's sealed operator and intrinsic tables are listed below.

Statuses are **accepted**, **accepted with exact restriction**, **rejected in
0.1**, and **deferred extension**. Deferred forms are rejected by the current
verifier; the label records a possible future design, not implicit permission.

## Source and module structure

| Family | Status | Rule |
| --- | --- | --- |
| UTF-8 source, comments, blank lines | accepted | The verifier never executes source. Non-UTF-8 encodings and NUL bytes are rejected. |
| Unicode identifiers | accepted with exact restriction | Source identifiers are normalized with NFKC. Module paths, configuration names and manifest names must already use canonical spelling. |
| Parentheses, continuation, multiline layout | accepted with exact restriction | Must form valid Python syntax; inconsistent indentation is rejected. |
| Module docstring | accepted with exact restriction | One ordinary string expression at the start of a module. |
| Package `__init__.py` | accepted with exact restriction | Empty or one docstring only. No imports or re-exports. Every package directory needs this file. |
| `from package.module import symbol` | accepted with exact restriction | Absolute, direct, module-level import; all symbols resolve in the fixed program image. Import cycles are rejected. |
| Support imports | accepted with exact restriction | `from purepy import value` and `from typing import Final`. |
| Plain, relative, star or aliased imports | rejected in 0.1 | Import defining symbols directly. |
| Local, conditional or dynamic imports | rejected in 0.1 | Imports are top-level declarations. |
| `__future__` imports | rejected in 0.1 | Source semantics are selected by configuration. |
| Module `Final[T]` constant | accepted with exact restriction | Explicit Pure Value annotation and immutable initializer: literals, tuples, prior/imported verified constants or record construction. |
| Module rebinding or executable statements | rejected in 0.1 | This includes script guards and ordinary calls during initialization. |
| Namespace packages, ambiguous modules, symlinks | rejected in 0.1 | Discovery assigns one canonical name per source file; it never follows source symlinks. |

## Declarations and statements

| Python family | Status | Rule |
| --- | --- | --- |
| `def` | accepted with exact restriction | Top-level only; every parameter and result annotated; fixed ordered parameters. |
| `async def` | accepted with exact restriction | Same signature rules; async results are available only through direct await. |
| Default arguments, `/`, bare `*`, `*args`, `**kwargs` | rejected in 0.1 | Signatures contain only ordinary explicitly annotated parameters. |
| Function decorators | rejected in 0.1 | No entrypoint, purity or unsafe decorators. Configure entrypoints by qualified name. |
| Type parameters and overloads | rejected in 0.1 | Functions are monomorphic. |
| Nested functions or local classes | rejected in 0.1 | No closures or local declarations. |
| `class` | accepted with exact restriction | Only top-level, data-only `@value` records; no bases, methods, defaults or metaclass options. Fields are annotated Pure Values. |
| Class decorators | accepted with exact restriction | Exactly the recognized `@value` decorator. |
| Function/record docstring | accepted with exact restriction | Optional leading ordinary string. |
| Simple assignment | accepted with exact restriction | Rebind one local name to a Pure Value of its declared exact type; definite assignment is required before reads. |
| Local annotated assignment | accepted with exact restriction | A Pure Value annotation fixes the local's type; declaration without a value does not initialize it. |
| Chained or destructuring assignment | rejected in 0.1 | Use a separate simple assignment for each name. |
| Attribute, index or starred assignment target | rejected in 0.1 | No object mutation or unpacking. |
| Imported-name assignment | rejected in 0.1 | Imported declarations are fixed. |
| Capability/host-reference alias or rebinding | rejected in 0.1 | Forward the original parameter directly to an exact known call parameter. |
| Augmented assignment (`+=`, etc.) | rejected in 0.1 | In-place operator semantics are unavailable. |
| `if` / `elif` / `else` | accepted with exact restriction | Conditions are exact `bool`; joining paths must agree on local types and initialization. |
| `while` | accepted with exact restriction | Exact `bool` condition; no `else` clause. Termination is not proved. |
| `for` | accepted with exact restriction | One local target; iterate directly over `range(...)`, a homogeneous tuple, `str` or `bytes`; no `else` clause. |
| `break`, `continue` | accepted with exact restriction | Inside a loop only. |
| `return` | accepted with exact restriction | Return a Pure Value matching the declared result. Bare return and fallthrough require result `None`. |
| `pass` | accepted with exact restriction | Allowed in a function body. Package initializers allow only a docstring. |
| Expression statement | accepted with exact restriction | A direct call or direct awaited call returning `None`, apart from leading docstrings. |
| `assert` | rejected in 0.1 | Represent expected failure as an explicit result. |
| `raise` | rejected in 0.1 | Expected failures are Pure Values. Deterministic primitive failures may terminate an invocation. |
| `try`, `except`, `except*`, exception `else`, `finally` | rejected in 0.1 | No exception inspection, cancellation suppression or cleanup inside verified code. |
| `with`, `async with` | deferred extension | Lexical resources require a separate ownership and cleanup specification. |
| `async for` | deferred extension | Async iteration and suspended streams are not admitted. |
| `match` / `case` | rejected in 0.1 | Use explicit conditions and optional values. |
| `global`, `nonlocal` | rejected in 0.1 | No ambient mutable state. |
| `del` | rejected in 0.1 | No deletion of names, attributes or subscriptions. |
| Type-alias statement (`type T = ...`) | rejected in 0.1 | Use concrete supported annotations directly. |

## Expressions

| Python family | Status | Rule |
| --- | --- | --- |
| `None`, Boolean, integer, float literals | accepted | Numeric spelling must be valid Python 3.14. `bool` is distinct from `int`. |
| String and bytes literals | accepted with exact restriction | Ordinary/raw/triple-quoted and adjacent literals retain Python value semantics. Named Unicode escapes (`\N{...}`) accept Unicode 16.0 character names and aliases, including algorithmic names; named sequences and malformed/unknown names reject. Raw strings and bytes preserve `\N` literally. |
| Complex literals | deferred extension | Complex numbers have no 0.1 type. |
| Ellipsis (`...`) value | rejected in 0.1 | Ellipsis appears only in `tuple[T, ...]` annotations. |
| Tuple display | accepted with exact restriction | One exact Pure Value element type. Empty tuples require a contextual `tuple[T, ...]` annotation. |
| List, dict and set displays | rejected in 0.1 | No mutable or unordered containers. |
| List, dict and set comprehensions | rejected in 0.1 | Write explicit loops over approved immutable iterables. |
| Generator expression | deferred extension | First-class suspended computation is unavailable. |
| Name reference | accepted with exact restriction | A definitely assigned local Pure Value or verified constant. Functions, modules and classes are declarations, not data. |
| Attribute expression | accepted with exact restriction | A declared field of one exact verified record. External values remain opaque. |
| Unary `+`, `-`, `~` | accepted with exact restriction | Numeric signs on `int`/`float`; bitwise complement on `int`. |
| Binary arithmetic | accepted with exact restriction | Only the exact operator table below; no implicit numeric coercion or user dispatch. |
| Boolean `and`, `or`, `not` | accepted with exact restriction | Exact `bool` operands and results; no general Python truthiness. |
| Comparison, including chains | accepted with exact restriction | Each adjacent operand pair must match a sealed equality, ordering or membership rule. |
| Identity `is`, `is not` | accepted with exact restriction | Only `None` identity tests, including optional narrowing. |
| Subscription | accepted with exact restriction | Exact `int` index on tuple, `str` or `bytes`; bytes indexing returns `int`. |
| Slice | accepted with exact restriction | Tuple, `str` or `bytes`; `int | None` bounds; no third colon/step or multidimensional subscription. |
| Conditional expression | accepted with exact restriction | Exact `bool` condition and equal exact branch types. |
| Direct call | accepted with exact restriction | A known top-level project/external function, sealed intrinsic or verified record constructor. Fixed arguments, no unpacking. |
| Keyword argument | accepted with exact restriction | Exact declared parameter name; no duplicate binding or following positional argument. Intrinsics use positional arguments only. |
| Method, callable object, local callable or dynamic call | rejected in 0.1 | All targets are statically known declarations. |
| Starred expression / argument unpacking | rejected in 0.1 | No `*values` or `**values` expansion. |
| `await known_async_call(...)` | accepted with exact restriction | Only inside an async function; the call and await form one semantic operation. |
| Non-call await or await of a sync function | rejected in 0.1 | No first-class awaitables or protocol dispatch. |
| Unawaited async call | rejected in 0.1 | Coroutines cannot be assigned, returned, stored, compared or discarded. |
| `yield`, `yield from` | deferred extension | Generators, send/close and suspended lifetime semantics are unspecified. |
| Lambda | rejected in 0.1 | Functions are not first-class values. |
| Assignment expression (`:=`) | rejected in 0.1 | Assignment is a statement with a single local target. |
| F-string | accepted with exact restriction | Interpolate exact `bool`, `int`, `float` or `str`; an empty `:` format is permitted. No conversions, debug `=`, or nonempty format specifications. |
| Template string (`t"..."`) | rejected in 0.1 | Template objects and their runtime behavior are outside the closed type model. |
| Reflection, dynamic evaluation and dunder access | rejected in 0.1 | Includes `eval`, `exec`, `compile`, `getattr`, `globals`, `id`, `hash` and runtime type inspection. |

## Types and exact operation tables

| Annotation family | Status | Rule |
| --- | --- | --- |
| `None`, `bool`, `int`, `float`, `str`, `bytes` | accepted | Exact primitive types. |
| `tuple[T, ...]` | accepted with exact restriction | Concrete Pure Value element type. |
| `T | None` | accepted with exact restriction | Concrete Pure Value `T`; no arbitrary union or subtyping. |
| Verified record or external nominal type | accepted with exact restriction | Static name resolution; capability/host-reference types occur only in parameters. |
| `Final[T]` | accepted with exact restriction | Module constants only, with Pure Value `T`. |
| Quoted annotations, aliases, `Any`, `object`, `Callable`, protocols, generics, mutable container types | rejected in 0.1 | No execution, deferred annotation lookup, dynamic type expansion or user generics. |

| Operation | Exact operands | Result |
| --- | --- | --- |
| `+`, `-`, `*`, `//`, `%` | Two `int`, or two `float` | Same numeric type |
| `/` | Two `int`, or two `float` | `float` |
| `&`, `|`, `^`, `<<`, `>>` | Two `int` | `int` |
| `+` concatenation | Two `str`, two `bytes`, or equal homogeneous tuple types | Same sequence type |
| `==`, `!=` | Equal primitives, recursively equality-comparable records/tuples/optionals; comparable optional against `None` | `bool` |
| `<`, `<=`, `>`, `>=` | Equal `int`, `float`, `str` or `bytes` | `bool` |
| `in`, `not in` | Equality-comparable element in matching tuple; `str` in `str`; `int` in `bytes` | `bool` |

Exponentiation (`**`) is rejected: Python's integer exponentiation can return
`int` or `float`, and floating-point exponentiation can return `complex`.
Matrix multiplication, sequence repetition, mixed numeric operations and
external-value comparison have no sealed rule and are rejected.

| Intrinsic | Exact accepted call forms |
| --- | --- |
| `len` | One `str`, `bytes` or homogeneous tuple; returns `int`. |
| `range` | One to three `int` arguments, directly consumed by `for`; yields `int`. |
| `abs` | One `int` or `float`; preserves type. |
| `min`, `max` | One homogeneous tuple, or two or more equal arguments; element type is `int`, `float`, `str` or `bytes`. |
| `sum` | One `tuple[int, ...]`; or a homogeneous numeric tuple and an explicit start of the same numeric type. |
| `all`, `any` | One `tuple[bool, ...]`; returns `bool`. |
| `int`, `float` | One `bool`, `int`, `float`, `str` or `bytes`; returns the named type. |
| `str` | One `None`, `bool`, `int`, `float` or `str`; returns `str`. |
| `bytes` | One `bytes` or `tuple[int, ...]`; returns `bytes`. |

Runtime domain failures such as division by zero, invalid conversion, out-of-range
indexing or invalid byte values remain deterministic failures. Verification does
not prove their absence. No unlisted built-in or standard-library API is implicitly
trusted; other host operations require explicit manifests.

Local builders, bounded parallel composition, lexical resources, memoization,
streams and general ownership are deferred extensions. Each requires a new
specified boundary and conformance tests before acceptance changes.
