# PurePy Language and Verification Specification

**File:** `PUREPY_SPEC.md`  
**PurePy specification version:** `0.3-draft`  
**Initial conformance level:** PurePy `0.1`  
**Target source syntax:** Python 3.14  
**Status:** Design specification  
**Last updated:** 2026-09-05

---

## 1. Status of this document

This document defines PurePy: a deliberately small, statically verified language that uses a strict subset of Python syntax.

A PurePy source file is valid Python source, but PurePy does **not** adopt the full Python data model or the semantics of every Python operation. The verifier assigns closed semantics to the constructs expressly admitted by this specification. Any syntax, operation, dispatch mechanism, library call, or runtime behavior not expressly admitted is rejected.

PurePy is designed around five commitments:

1. Ordinary functions are pure by construction.
2. External authority enters verified code only through explicit capability parameters.
3. Calls are first-order, monomorphic, and statically resolved.
4. Asynchronous execution is admitted only through direct `await` of known async calls.
5. Scheduling and resource ownership remain in a narrow host boundary in PurePy 0.1.

PurePy is not:

- a proposal to change Python;
- a general Python type checker;
- a web, database, concurrency, or caching framework;
- a runtime effect interpreter;
- a verifier for arbitrary Python frameworks;
- an attempt to prove termination; or
- an attempt to make unknown behavior appear safe through annotations.

The reference `purepy` tool is a static verifier. It MUST NOT import or execute analyzed project code.

### 1.1 Normative language

The words **MUST**, **MUST NOT**, **REQUIRED**, **SHALL**, **SHALL NOT**, **SHOULD**, **SHOULD NOT**, **RECOMMENDED**, **MAY**, and **OPTIONAL** are normative.

Sections marked **Non-normative** explain motivation or implementation strategy and do not define conformance.

### 1.2 Versioning

This document is specification draft `0.3`. It defines the intended PurePy `0.1` language level.

Later language levels MAY add features, but they MUST preserve the guarantees of accepted PurePy 0.1 programs. New features MUST be independently specified and MUST NOT retroactively reinterpret unsupported PurePy 0.1 syntax as trusted behavior.

---

## 2. Executive definition

PurePy is:

> A closed-world, first-order, monomorphic subset of Python syntax in which ordinary data is deeply immutable, all calls are statically resolved, and every interaction with external state requires an explicit host-provided capability value.

A normal PurePy function accepts only Pure Values and returns a Pure Value:

```python
def calculate_total(unit_price: int, quantity: int) -> int:
    return unit_price * quantity
```

Such a function cannot read a clock, access a database, use a socket, inspect process state, or mutate externally visible state because it has no value that grants authority to do so.

An effectful function receives explicit capabilities:

```python
from host.database import DatabaseRead, load_user


async def find_user(
    database_read: DatabaseRead,
    user_id: int,
) -> UserResult:
    return await load_user(database_read, user_id)
```

`DatabaseRead` is an opaque host-provided capability type. Verified code cannot construct it, store it in ordinary data, return it, compare it, inspect it, or obtain it from ambient state. It can only pass it to a statically known operation whose declaration requires that exact capability.

Effects are therefore authorized locally at the call site:

```text
caller has DatabaseRead
        ↓
passes DatabaseRead explicitly
        ↓
known database operation is authorized
```

A function without a `DatabaseRead` parameter cannot perform that operation.

PurePy 0.1 supports modern nonblocking service code through restricted `async def` and direct `await`. The host remains responsible for:

- creating the event loop;
- accepting network connections;
- scheduling concurrent request handlers;
- owning sockets, database pools, transactions, and other resources;
- delivering cancellation;
- closing resources; and
- invoking configured PurePy entrypoints.

Verified code remains responsible for protocol parsing, routing, validation, business logic, rendering, explicit database operations, explicit network operations, and long-lived loops such as server-sent-event handlers.

---

## 3. Design axioms

### 3.1 Python syntax, PurePy semantics

PurePy targets a subset of Python 3.14 syntax. It does not promise to preserve the full Python object model for accepted operations.

For example, `a + b` is accepted only when `a` and `b` have exact approved built-in numeric or sequence types. PurePy does not resolve arbitrary `__add__` methods.

Likewise, `value.field` is accepted only for:

- a field of a verified `@value` record; or
- a statically declared module symbol.

PurePy does not model descriptors, properties, custom attribute lookup, metaclass behavior, or open inheritance.

### 3.2 Fail closed

When the verifier cannot establish the exact meaning of an operation, it MUST reject the operation.

Unknown behavior MUST NOT be downgraded to a warning, inferred as pure, or authorized by a broad suppression comment.

### 3.3 Authority is explicit dataflow

External authority MUST enter verified code as a capability parameter supplied by the host or by another verified caller that already possesses the capability.

No capability may be obtained from:

- a module global;
- a closure;
- an environment variable;
- reflection;
- dynamic import;
- a registry;
- a service locator;
- an ordinary Pure Value; or
- an unknown external call.

### 3.4 First-order calls

Functions are not ordinary data in PurePy 0.1.

Every call target MUST resolve statically to:

- a top-level project function;
- a top-level trusted external function declared in a manifest; or
- a sealed PurePy intrinsic.

### 3.5 Monomorphic semantics

PurePy 0.1 does not define user generics, overload resolution, protocol conformance, structural typing, or effect polymorphism.

Every verified function has one concrete signature after names are resolved.

### 3.6 Host-owned resources

PurePy 0.1 deliberately does not prove general ownership, borrowing, transfer, cleanup, or task lifetimes.

Sockets, connections, transactions, cursors, pools, task handles, and similar objects are host-owned opaque references. Verified code may use such references only within the invocation in which the host supplied them and only through known operations.

### 3.7 Direct asynchronous composition

PurePy admits asynchronous composition without admitting first-class coroutine values.

`await` MUST directly contain a known async call. The verifier lowers the call and await as one semantic operation.

### 3.8 Verifier, not framework

PurePy defines language rules and verifies source. It does not supply application abstractions such as requests, routes, sessions, transactions, streams, task groups, or cache backends.

### 3.9 Practical service expressiveness

The initial language MUST be able to verify a useful nonblocking service above a narrow host boundary, including:

- immutable request parsing;
- static routing;
- database reads and atomic writes;
- explicit network reads and writes;
- server-sent-event loops;
- pure business logic and rendering; and
- host-managed concurrent invocation.

### 3.10 Performance through simplicity

The language is intentionally restricted so the verifier can provide fast, deterministic, repository-scale analysis without becoming a complete Python compiler or type checker.

---

## 4. Goals

PurePy 0.1 has the following goals.

### 4.1 Strong purity

For functions whose parameters and return type are Pure Values, the verifier should establish a strong observational purity guarantee.

### 4.2 Local authorization

A reviewer should be able to inspect a function signature and see every category of external authority the function can exercise.

### 4.3 Minimal semantic machinery

The initial verifier should not require:

- whole-program effect fixed-point inference;
- general dynamic dispatch;
- callback analysis;
- coroutine ownership analysis;
- resource ownership analysis;
- task lifetime analysis;
- generator finalization analysis; or
- persistent memoization certificates.

### 4.4 Hermetic analysis

Verification must not execute analyzed code, import project modules, run decorators, or invoke executable plugins.

### 4.5 Fast feedback

The reference implementation should support bounded parallel parsing and checking, deterministic output, and content-addressed reuse of unchanged module summaries.

### 4.6 Narrow interoperability

PurePy may call trusted host or native operations when their complete statically relevant signatures are declared in a manifest.

### 4.7 Honest rejection

Compatibility with arbitrary Python libraries is not a goal. Rejecting dynamic frameworks is acceptable.

---

## 5. Non-goals

PurePy 0.1 does not attempt to support or verify:

- arbitrary Python code;
- arbitrary standard-library or third-party libraries;
- methods on user-defined classes;
- inheritance or subclassing;
- descriptors or properties;
- operator overloading;
- metaclasses;
- dynamic imports;
- monkey-patching;
- reflection;
- callbacks or higher-order functions;
- user-defined generics;
- exception handling;
- generators or asynchronous generators;
- first-class coroutine objects;
- task creation inside verified code;
- context managers;
- general mutable containers;
- resource ownership or borrowing;
- framework dependency injection;
- runtime decorator semantics other than `@value`;
- transparent persistent or distributed memoization;
- proving termination;
- cross-platform bit-identical floating-point results; or
- verification of host implementations.

These exclusions are deliberate. A later specification may add narrow versions of some features without adopting their unrestricted Python semantics.

---

## 6. Program model

### 6.1 Program image

A **program image** consists of:

- all verified project modules under one configured source root;
- the selected PurePy language version;
- the project configuration;
- the loaded external semantic manifests;
- the sealed intrinsic table;
- the configured entrypoint names; and
- the verifier version.

Purity is defined relative to a fixed program image.

### 6.2 Closed world

PurePy 0.1 operates in closed-world application mode.

Every imported symbol and every call target must resolve within the program image. Code loaded dynamically after verification is outside the guarantee.

### 6.3 Source root

A project has exactly one verified source root in PurePy 0.1.

Each `.py` file beneath the source root maps deterministically to one module name. Namespace packages are not supported. Package directories MUST contain `__init__.py` files.

### 6.4 Host boundary

The **host** is ordinary Python or native code outside the verified source root. It may:

- initialize the runtime;
- import verified modules;
- create capabilities and opaque host references;
- invoke configured entrypoints;
- schedule concurrent invocations;
- manage cancellation;
- own mutable resources; and
- translate host failures into explicit Pure Value results or terminate the invocation.

The host is trusted. PurePy verifies only the use of host-provided declarations, not their implementation.

### 6.5 Entrypoints

Entrypoints are configured by fully qualified function name in `purepy.toml`.

PurePy 0.1 does not require or recognize an `@entrypoint` decorator.

An entrypoint MAY be synchronous or asynchronous. Its parameters MAY include Pure Values, capabilities, and host references. Its return type MUST be a Pure Value type.

### 6.6 Ordinary Python execution

Accepted PurePy source remains ordinary Python source and executes on a normal Python implementation together with a small support package and host implementation.

Verification does not imply that arbitrary unverified callers will respect PurePy contracts. The host is responsible for invoking verified entrypoints with values matching their declared categories.

---

## 7. Core purity guarantee

### 7.1 Pure function

A top-level function is a **pure function** when:

- all parameters are Pure Value types;
- its return type is a Pure Value type;
- its body conforms to PurePy;
- every call in its body targets another pure function, a trusted pure external function, or a pure intrinsic; and
- it contains no capability or host-reference value.

For a fixed program image and equivalent Pure Value arguments, evaluating a pure synchronous function MUST have one of these outcomes:

1. return an equivalent Pure Value;
2. terminate through the same deterministic runtime exception class under the same operation and equivalent operands; or
3. fail to terminate.

The outcome MUST NOT depend on ambient state.

### 7.2 Pure async function

An `async def` function is pure when it satisfies the pure-function conditions and every direct await targets another pure async function or trusted pure async external operation.

For equivalent inputs, if a conforming host drives the coroutine to completion without externally cancelling it, it MUST produce the same eventual Pure Value, the same deterministic runtime exception, or divergence.

External cancellation is outside the pure result relation. Verified code cannot catch, inspect, delay, or suppress cancellation in PurePy 0.1.

### 7.3 Excluded ambient dependencies

Pure functions MUST NOT depend on:

- wall, monotonic, CPU, or process time;
- entropy or nondeterministic randomness;
- environment variables;
- filesystem contents or metadata;
- network state;
- database state;
- process identifiers, arguments, or working directory;
- thread, task, or event-loop identity;
- mutable module state;
- mutable closure state;
- object identity;
- Python hash randomization;
- locale or mutable numeric context;
- import-cache state;
- signal state;
- tracing or debugger state;
- garbage-collector timing; or
- any other ambient value not represented by Pure Value arguments or immutable program constants.

### 7.4 No externally observable mutation

Calling a pure function MUST NOT modify:

- any object reachable before the call;
- a module namespace;
- a class namespace;
- a closure cell;
- a host reference;
- an external resource;
- process-wide state; or
- state observable by another PurePy function.

Allocation of new immutable values is permitted.

### 7.5 Partiality

Purity does not imply termination. PurePy 0.1 does not prove loop or recursion termination.

### 7.6 Deterministic runtime exceptions

PurePy source cannot explicitly raise or catch exceptions in 0.1, but approved primitive operations may still fail, for example integer division by zero or indexing outside a valid range.

Such failures do not constitute an external effect when the exception is determined solely by Pure Value inputs and the fixed program image.

Tracebacks, frames, exception context, and process-level exception hooks are not observable from verified code.

### 7.7 Floating-point behavior

`float` is a Pure Value type. Results are guaranteed relative to the selected Python runtime and execution platform. PurePy 0.1 does not promise bit-identical floating-point behavior across processors, operating systems, Python implementations, or math libraries.

---

## 8. Value categories

Every statically known value belongs to one of four categories.

### 8.1 Pure Value

A **Pure Value** is deeply immutable and has only statically approved effect-free operations.

Core Pure Value types are:

- `None`;
- `bool`;
- `int`;
- `float`;
- `str`;
- `bytes`;
- homogeneous tuples `tuple[T, ...]` where `T` is a Pure Value type;
- `T | None` where `T` is a Pure Value type; and
- instances of verified `@value` records whose fields are Pure Value types.

Immutability is recursive.

### 8.2 Capability Value

A **Capability Value** is an opaque host-provided authority token.

A capability type has:

- a fully qualified type name;
- one or more exact reporting labels, such as `database.read`; and
- a trusted manifest declaration.

Capability values are not Pure Values.

### 8.3 Host Reference

A **Host Reference** is an opaque, host-owned reference to a resource or invocation context, such as:

- a network connection;
- a request body source;
- a response sink;
- a database pool facade;
- a transaction plan executor; or
- a service invocation context.

Host references are not Pure Values and do not themselves grant authority. A host operation normally requires both a capability and the relevant host reference.

### 8.4 Ephemeral Intrinsic Value

An **Ephemeral Intrinsic Value** exists only as part of a statically bounded approved operation.

PurePy 0.1 recognizes:

- `range` values consumed directly by a `for` loop;
- internal iteration state for approved built-in immutable iterables; and
- internal call-binding values.

Ephemeral intrinsic values cannot be returned, stored, or passed to external functions.

### 8.5 Unknown Value

A value whose type or category cannot be established is an **Unknown Value** and MUST be rejected at its first attempted use.

### 8.6 Category separation

Capability values and host references MUST NOT be:

- returned from a verified function;
- stored in a tuple or `@value` record;
- assigned to a module constant;
- compared;
- formatted;
- hashed;
- indexed;
- inspected through attributes;
- converted to a Pure Value;
- passed to a pure function;
- passed to an unknown function; or
- aliased through local assignment.

They MAY be forwarded directly from a function parameter to a statically known call parameter of the exact declared category and type.

---

## 9. Type subset

### 9.1 Required annotations

Every function parameter and return value MUST have an explicit type annotation.

Every `@value` field MUST have an explicit type annotation.

Module constants MUST have an explicit `Final[T]` annotation.

### 9.2 Supported type expressions

PurePy 0.1 supports only:

```text
None
bool
int
float
str
bytes
tuple[T, ...]
T | None
VerifiedValueRecord
DeclaredCapabilityType
DeclaredHostReferenceType
Final[T]                 module constants only
```

`T` in `tuple[T, ...]` and `T | None` must be a permitted concrete type.

### 9.3 Prohibited type expressions

PurePy 0.1 rejects:

- `Any`;
- `object`;
- `Callable`;
- `TypeVar` and user-defined generics;
- `Protocol`;
- `Generic`;
- `Literal`;
- arbitrary unions;
- intersections;
- overloads;
- recursive aliases;
- `list`, `dict`, `set`, `frozenset`, `bytearray`, and mutable collection types;
- iterator, generator, coroutine, task, future, context-manager, or exception types; and
- user class types not declared as `@value` records or in trusted manifests.

### 9.4 No subtyping

PurePy 0.1 uses exact nominal type matching, except for the explicit optional form `T | None` and the ordinary relation that a value of type `T` may be used where `T | None` is expected.

There is no user-defined inheritance, structural subtyping, variance, or capability subtyping.

### 9.5 Local variable typing

A local variable receives its type from its first assignment or explicit local annotation.

Every later assignment to the same name MUST have the same exact type.

A local MUST be definitely assigned on every path before use.

Branch joins MUST agree on the exact type of every live local.

### 9.6 Optional narrowing

For `value: T | None`, the verifier MAY narrow `value` to `T` within a branch guarded by an exact comparison to `None`:

```python
if value is None:
    return fallback
return use_value(value)
```

No other union narrowing is defined in PurePy 0.1.

---

## 10. Data-only value records

### 10.1 Declaration

PurePy provides one approved class form:

```python
from purepy import value


@value
class User:
    user_id: int
    display_name: str
```

A `@value` class defines a deeply immutable nominal record.

### 10.2 Class-body restrictions

A `@value` class body MAY contain only:

- an optional docstring; and
- annotated field declarations without defaults.

It MUST NOT contain:

- methods;
- properties;
- constructors;
- static or class methods;
- class constants;
- computed fields;
- descriptors;
- nested classes;
- decorators other than the exact `@value` decorator;
- inheritance;
- metaclass declarations; or
- executable statements.

Identifiers in a record class body follow Python private-name mangling after Unicode NFKC normalization.

For example, the source field `__key` in `class Secret` has the effective field
name `_Secret__key`. Construction by keyword and field reads
outside the class use that effective name; `Secret(__key=1)` has no matching
field, while `Secret(_Secret__key=1)` and positional `Secret(1)` are permitted.
This preserves Python's field-name semantics and does not grant reflection or
access to prohibited double-leading-and-trailing-underscore names.

### 10.3 Field types

Every field type MUST be a Pure Value type.

Capability values and host references cannot be record fields.

### 10.4 Construction

A value record may be constructed by a direct call to its statically resolved class name.

Arguments MUST exactly match the declared fields. Positional arguments in field order and explicit keyword arguments are permitted. Argument unpacking is prohibited.

### 10.5 Operations

Permitted operations on a value record are:

- construction;
- field reads;
- equality and inequality with the same exact record type when every field is recursively equality-comparable under section 14.6; and
- pure formatting of approved field values through a verifier-recognized representation.

Record identity is not observable.

A record may contain immutable manifest-declared values without supporting
equality. Construction, field reads, and forwarding remain permitted for such
records; their fields do not acquire comparison behavior from the record wrapper.

### 10.6 Behavior belongs in functions

All behavior over records MUST be expressed as top-level functions:

```python
def rename_user(user: User, display_name: str) -> User:
    return User(user_id=user.user_id, display_name=display_name)
```


---

## 11. Function subset

### 11.1 Top-level definitions only

PurePy 0.1 permits only top-level function definitions:

- `def`;
- `async def`.

Nested functions and lambdas are prohibited.

### 11.2 Fixed signatures

A function signature MUST have:

- a fixed ordered parameter list;
- an annotation on every parameter;
- an explicit return annotation; and
- no default values.

The following are prohibited:

- positional-only markers;
- keyword-only markers;
- `*args`;
- `**kwargs`;
- parameter unpacking;
- type parameters;
- overloads; and
- decorators.

The only decorator admitted anywhere in a verified module is `@value` on a data-only record.

### 11.3 Parameter categories

Each parameter is exactly one of:

1. a Pure Value parameter;
2. a capability parameter; or
3. a host-reference parameter.

Capability and host-reference parameters MUST retain their original names and MUST NOT be rebound.

### 11.4 Return category

Every verified function MUST return a Pure Value type or `None`.

Capabilities, host references, ephemeral intrinsic values, functions, modules, classes, and suspended computations cannot be returned.

### 11.5 Function classification

A function is classified as:

- **pure synchronous**;
- **pure asynchronous**;
- **effectful synchronous**; or
- **effectful asynchronous**.

The classification follows directly from syntax and parameter categories:

| Syntax | Capability or host-reference parameter? | Classification |
|---|---:|---|
| `def` | no | pure synchronous |
| `async def` | no | pure asynchronous |
| `def` | yes | effectful synchronous |
| `async def` | yes | effectful asynchronous |

An effectful function remains verified. “Effectful” does not mean “unsafe.”

### 11.6 Calls by name, not by value

A function object cannot be assigned, returned, stored, compared, or passed as an argument.

A call expression MUST name its target directly through a statically resolved imported or local symbol.

### 11.7 Recursion

Direct and mutual recursion between statically resolved top-level functions is permitted.

PurePy does not prove termination or bounded recursion depth.

### 11.8 Function introspection

Verified code cannot inspect:

- `__name__`;
- `__module__`;
- `__annotations__`;
- `__defaults__`;
- `__closure__`;
- code objects;
- signatures; or
- any other function metadata.

---

## 12. Module subset

### 12.1 Permitted module items

A verified module MAY contain only:

- an optional module docstring;
- approved module-level imports;
- immutable `Final` constants;
- `@value` record definitions;
- top-level `def` definitions; and
- top-level `async def` definitions.

No other module-level statement is permitted.

### 12.2 Imports

PurePy 0.1 permits only absolute imports of the form:

```python
from package.module import symbol
```

The following are prohibited:

- `import module`;
- relative imports;
- star imports;
- import aliases;
- imports inside functions;
- conditional imports;
- dynamic imports;
- namespace-package resolution; and
- import cycles.

The verifier resolves imports without executing them.

### 12.3 Approved support imports

The verifier recognizes these support symbols intrinsically:

```python
from typing import Final
from purepy import value
```

Additional imported symbols must resolve to another verified project module, a sealed intrinsic module, or a trusted external manifest declaration.

### 12.4 Package initializers

A package `__init__.py` file MAY contain only an optional docstring.

Imports, re-exports, constants, records, functions, and executable statements are prohibited in package initializers in PurePy 0.1. Application modules import directly from the defining module.

### 12.5 Module constants

A module constant has the form:

```python
MAX_REQUEST_BYTES: Final[int] = 1_048_576
LINE_ENDING: Final[bytes] = b"\r\n"
```

The initializer MUST be a constant expression consisting only of:

- primitive literals;
- tuple displays of constant expressions;
- references to earlier constants in the same module;
- references to imported verified constants; and
- direct construction of a `@value` record from constant expressions.

No ordinary function call is permitted during module initialization.

### 12.6 No mutable module state

Module rebinding, caches, registries, counters, and mutable containers are prohibited.

### 12.7 No script blocks

`if __name__ == "__main__":` and other executable bootstrap code are prohibited in verified modules. The host invokes configured entrypoints.

---

## 13. Statement subset

### 13.1 Permitted statements

A verified function body MAY contain:

- an optional docstring;
- simple local assignment;
- explicit local variable annotation followed by assignment;
- `if` / `elif` / `else`;
- `for` over an approved iterable;
- `while`;
- `break`;
- `continue`;
- `return`;
- `pass`; and
- a direct known call or direct awaited call used as an expression statement.

### 13.2 Assignment

Assignment is local name rebinding only:

```python
count = 0
count = count + 1
```

The following assignment targets are prohibited:

- attributes;
- subscriptions;
- starred targets;
- tuple or list destructuring;
- capabilities;
- host references; and
- imported names.

Augmented assignment such as `+=` is prohibited because Python may dispatch to in-place mutation.

### 13.3 Conditions

Conditions MUST have exact type `bool`.

PurePy does not use general Python truthiness. An `int`, `str`, `bytes`, tuple, record, capability, or host reference cannot appear directly as a condition.

### 13.4 `for`

A `for` loop may iterate only over:

- `range(...)` produced directly by the sealed `range` intrinsic;
- `tuple[T, ...]`;
- `str`; or
- `bytes`.

The loop variable has type:

- `int` for `range` and `bytes`;
- `str` for `str`; or
- `T` for `tuple[T, ...]`.

`for ... else` is prohibited.

### 13.5 `while`

A `while` condition MUST have exact type `bool`.

`while ... else` is prohibited.

### 13.6 Return

Every reachable return expression MUST match the exact declared return type.

A function declared to return `None` may use either `return` or `return None`.

Falling off the end is permitted only for a function declared to return `None`.

### 13.7 Expression statements

An expression statement is permitted only when it is:

- a direct call to a known function returning `None`; or
- a direct `await` of a known async function returning `None`.

Discarding a non-`None` result is prohibited.

### 13.8 Prohibited statements

PurePy 0.1 prohibits:

- `assert`;
- `raise`;
- `try`, `except`, `else`, and `finally` in exception handling;
- `with` and `async with`;
- `match`;
- `yield` and `yield from`;
- `async for`;
- `global`;
- `nonlocal`;
- `del`;
- local imports;
- nested function definitions;
- local class definitions;
- type-alias statements;
- assignment expressions; and
- augmented assignment.

---

## 14. Expression subset

### 14.1 Primitive literals

Permitted literals are:

- `None`;
- booleans;
- integers;
- floats;
- strings; and
- bytes.

Complex literals are deferred.

### 14.2 Tuple displays

A tuple display is permitted when all elements have one exact Pure Value type.

```python
values = (1, 2, 3)
```

The inferred type is `tuple[int, ...]`.

Heterogeneous tuples are prohibited. Use a `@value` record instead.

The empty tuple has no standalone inferred element type. It is permitted only when an explicit contextual type supplies `tuple[T, ...]`, for example `items: tuple[int, ...] = ()`.

### 14.3 Prohibited displays

List, dictionary, set, and mutable-container displays are prohibited.

### 14.4 Arithmetic

Arithmetic is permitted only for exact approved built-in operand combinations.

PurePy 0.1 supports:

- integer arithmetic;
- float arithmetic;
- integer and float comparison;
- integer bitwise operations;
- string concatenation;
- bytes concatenation; and
- homogeneous tuple concatenation.

Mixed `int` and `float` arithmetic MAY be accepted only through a fixed intrinsic conversion table. No user-defined operator dispatch is performed.

### 14.5 Boolean operations

`and`, `or`, and `not` operate only on exact `bool` operands and produce `bool`.

PurePy does not adopt Python's value-returning `and` and `or` semantics for non-boolean operands.

### 14.6 Comparison

Equality and ordering are permitted only where the verifier has a sealed exact rule.

Permitted equality includes:

- same-type primitives;
- homogeneous tuples of equality-comparable Pure Values;
- same-type `@value` records whose fields are all recursively equality-comparable; and
- comparison of an optional value to `None` when its non-`None` element type is recursively equality-comparable.

Equality-comparability is closed over the exact primitives `None`, `bool`, `int`,
`float`, `str`, and `bytes`, homogeneous tuples of comparable elements, optionals
of comparable elements, and verified records whose fields are all comparable.
Recursive record types are prohibited. An immutable manifest-declared value has
no sealed equality rule, including when it occurs inside a tuple, optional, or
record. For example, `token == None` is rejected for an opaque `Token | None`:
Python could invoke `Token.__eq__` on the non-`None` path. Use `token is None`
for the supported absence check.

Identity operators `is` and `is not` are permitted only with `None`.

For each identity comparison, at least one operand has the exact type `None`;
the other operand may be any Pure Value, including a nonoptional value, an
optional value, a `@value` record, or a manifest-declared immutable value. The
`None` operand may be a literal or another expression with exact type `None`.
An optional type alone does not satisfy the exact-`None` requirement. Each
adjacent pair in a comparison chain is checked separately.

These comparisons produce `bool` without invoking equality methods or exposing
the allocation identity of non-`None` values. They do not require the other
operand to support equality. Optional narrowing retains the explicit literal
`is None` and `is not None` guard rules.

Capability and host-reference values cannot be compared.

### 14.7 Membership

`in` and `not in` are permitted only for:

- a Pure Value element in a homogeneous tuple;
- a `str` in a `str`; or
- an `int` in `bytes`.

### 14.8 Indexing and slicing

Subscription and slicing are permitted for:

- homogeneous tuples;
- strings; and
- bytes.

Indices must be `int`. Slice bounds must be `int | None`. Extended slicing and user-defined `__getitem__` dispatch are prohibited.

### 14.9 Field reads

`record.field` is permitted only when `record` has one exact verified `@value` type and `field` is a declared field.

No other instance attribute access is permitted in project code.

### 14.10 Function calls

A call is permitted only under Section 16.

Argument unpacking with `*` or `**` is prohibited.

Keyword arguments are permitted only when every keyword names an exact declared parameter and no positional argument follows a keyword argument.

### 14.11 Conditional expression

`a if condition else b` is permitted when:

- `condition` has type `bool`; and
- `a` and `b` have the same exact type.

### 14.12 String formatting

An f-string is permitted only when every inserted expression has one of these exact types:

- `bool`;
- `int`;
- `float`;
- `str`; or
- `bytes` through an explicit approved conversion.

Dynamic format specifications, locale-sensitive formatting, arbitrary `__format__`, `repr`, and object formatting are prohibited.

### 14.13 Comprehensions

List, dictionary, set, and generator comprehensions are prohibited in PurePy 0.1.

Equivalent loops over tuples may be written using tuple concatenation. A later local-builder extension is expected to provide an efficient form.

### 14.14 Reflection and dynamic evaluation

The following are prohibited:

- `eval`;
- `exec`;
- `compile`;
- `globals`;
- `locals`;
- `vars`;
- `dir`;
- `getattr`;
- `setattr`;
- `delattr`;
- `hasattr`;
- `id`;
- `hash`;
- `type` inspection;
- `isinstance` and `issubclass`;
- `__import__`;
- frame or code-object access; and
- access to names beginning and ending with double underscores, except names used by the verifier-recognized runtime support implementation outside verified code.

---

## 15. Sealed intrinsics

### 15.1 Purpose

PurePy defines a small set of operations directly in the language rather than modeling their full Python callable or protocol behavior.

### 15.2 Initial intrinsic set

The initial set MAY include exact typed forms of:

- `len`;
- `range`;
- `abs`;
- `min`;
- `max`;
- `sum`;
- `all`;
- `any`;
- `int` conversion from approved primitive types;
- `float` conversion from approved primitive types;
- `str` conversion from approved primitive types;
- `bytes` construction from approved byte values;
- tuple concatenation;
- approved string and bytes search operations; and
- construction and equality of `@value` records.

### 15.3 No protocol dispatch

Intrinsics are selected from exact static operand types. They do not invoke unknown methods, descriptors, iterators, or callbacks.

For example, `len(value)` is accepted only when `value` is exactly `str`, `bytes`, or `tuple[T, ...]`.

### 15.4 Intrinsic versioning

The verifier MUST version and test the intrinsic table. Adding an intrinsic is a language change.

The PurePy 0.1 reference table has intrinsic contract version `1`, independent
of the verifier build version. Its exact call forms and result types are listed
in `SYNTAX_MATRIX.md`. The `purepy version` command identifies this version, and
it participates in program-image cache identity. Changes to accepted calls or
their semantics require a new intrinsic contract version and language review;
implementation fixes preserving the contract do not.

---

## 16. Name and call resolution

### 16.1 Exact target resolution

Every call target MUST resolve to exactly one callable declaration.

The target may be:

- a top-level function in the same module;
- a directly imported top-level function from another verified module;
- a directly imported trusted external function; or
- a sealed intrinsic.

### 16.2 Prohibited call targets

The following are prohibited:

- local variables holding callables;
- record fields holding callables;
- instance methods;
- class methods;
- static methods;
- callable objects;
- constructors other than `@value` record construction;
- union-dispatched callables;
- overloaded functions;
- dynamically selected functions; and
- unknown imported functions.

### 16.3 Argument checking

Every argument MUST match the exact declared parameter type and category.

A capability parameter must receive the exact incoming capability parameter name or another direct capability expression explicitly permitted by a future specification. PurePy 0.1 defines no capability constructors or transformations.

A host-reference parameter must receive the exact incoming host-reference parameter name.

### 16.4 Capability forwarding

Capability and host-reference values may only appear as direct call arguments.

Valid:

```python
return await load_user(database_read, user_id)
```

Invalid:

```python
alias = database_read
return await load_user(alias, user_id)
```

Invalid:

```python
wrapped = (database_read,)
```

This syntactic restriction intentionally avoids alias and ownership analysis.

### 16.5 Pure caller rule

A pure function has no capability or host-reference parameters. It therefore cannot satisfy the signature of an effectful operation.

A verifier MUST reject any attempt by a pure function to call an effectful function, even if dead-code analysis would suggest that the call is unreachable.

### 16.6 Effectful caller rule

An effectful function may call another effectful function only by passing the required capabilities and host references explicitly.

The caller need not redeclare a textual effect set: its authority is already visible in its parameter list.

### 16.7 No ambient resolution

The verifier MUST NOT resolve call targets through runtime registries, dependency-injection containers, decorators, route tables, import hooks, module `__getattr__`, or monkey-patched names.

---

## 17. Capability system

### 17.1 Definition

A capability type is a nominal opaque type declared in a trusted semantic manifest.

Example conceptual declarations:

```text
capability host.database.DatabaseRead:
    labels = ["database.read"]

capability host.network.NetworkWrite:
    labels = ["network.write"]
```

### 17.2 Exact authority

Capability labels are exact strings. PurePy 0.1 defines no hierarchy, implication, aliasing, or wildcard grants.

A capability with label `database` does not automatically authorize `database.read` or `database.write`.

### 17.3 Standard reporting labels

The standard manifest set SHOULD use labels such as:

```text
console.read
console.write
fs.read
fs.write
fs.metadata
network.listen
network.read
network.write
database.read
database.write
clock.read
clock.wait
random.read
env.read
env.write
process.inspect
process.spawn
log.write
state.read
state.write
```

These labels are reporting and policy identifiers. Authorization is determined by exact capability types in function signatures.

### 17.4 Capability acquisition

Verified code cannot construct a capability.

Capabilities enter verified code only as:

- host-supplied entrypoint parameters; or
- parameters of a verified caller that forwards them.

### 17.5 Capability use

A capability may be used repeatedly and sequentially within one invocation. It is not an owned resource.

PurePy 0.1 does not permit verified task creation, so concurrent capability sharing inside verified code does not arise.

### 17.6 Capability attenuation

PurePy 0.1 defines no source-level capability attenuation or composition.

A host may supply separate narrow capability types instead of a broad capability.

### 17.7 Authority summary

For each function, the verifier MUST report the set of capability labels present in its signature.

This is a signature summary, not an inferred transitive effect fixed point.

### 17.8 Unused authority

A function MAY accept a capability it does not use. The verifier SHOULD report this as a non-fatal diagnostic because unnecessary authority weakens reviewability.

---

## 18. Host references

### 18.1 Definition

A host-reference type is an opaque nominal type declared in a trusted manifest.

Example:

```text
host_ref host.network.Connection
```

### 18.2 Lifetime

The host guarantees that a host reference remains valid for the duration of the entrypoint invocation and every direct verified call awaited within that invocation.

PurePy 0.1 does not verify creation, ownership transfer, closure, or finalization.

### 18.3 Restrictions

A host reference:

- cannot be created by verified code;
- cannot be returned;
- cannot be stored in Pure Values;
- cannot be assigned to a new local name;
- cannot be compared or inspected;
- cannot be used as a dictionary key or hash input;
- cannot be captured by a callback, because callbacks are prohibited;
- cannot cross a verified task boundary, because task creation is prohibited; and
- can be passed only to an exact known host operation or verified forwarding function.

### 18.4 Separation from authority

A host reference identifies a host-owned object but does not authorize operations on it.

A network receive operation should therefore require both:

```text
NetworkRead capability
Connection host reference
```

This prevents possession of an opaque handle from implicitly granting every operation supported by the underlying object.

### 18.5 Atomic host operations

Operations whose safe use would otherwise require owned mutable resources SHOULD initially be exposed as one-shot host calls over Pure Values.

Examples:

```text
execute_transaction(DatabaseWrite, TransactionPlan) -> async TransactionResult
fetch_page(DatabaseRead, Query, CursorToken | None) -> async PageResult
```

Interactive transaction objects, cursors, and connection checkout are deferred.

---

## 19. Asynchronous subset

### 19.1 `async def`

Top-level `async def` is permitted under the same signature and body restrictions as `def`.

An async function returns an eventual Pure Value, but the coroutine object created by ordinary Python execution is not a PurePy value and cannot appear explicitly in verified semantics.

### 19.2 Direct await rule

An `await` expression MUST directly contain a call to a statically known async function:

```python
result = await load_user(database_read, user_id)
```

Permitted forms are:

```python
result = await known_async_call(arguments)
return await known_async_call(arguments)
await known_async_none_call(arguments)
```

### 19.3 Prohibited coroutine handling

The following are prohibited:

```python
pending = known_async_call(arguments)
result = await pending
```

```python
return known_async_call(arguments)
```

```python
consume(known_async_call(arguments))
```

Coroutine objects cannot be assigned, returned, passed, stored, compared, collected, or conditionally selected.

### 19.4 Async call completeness

Calling a known async function without directly awaiting it is a verification error.

### 19.5 Sync and async calling rules

A synchronous function cannot call an async function.

An async function may:

- call synchronous functions normally; and
- directly await async functions.

### 19.6 Pure async composition

A pure async function may directly await only pure async functions or trusted pure async external functions.

An effectful async function may directly await an operation requiring capabilities only when it passes the exact capabilities from its parameters.

### 19.7 Scheduling

`await` itself is not treated as an external effect. The awaited operation determines the required authority.

PurePy 0.1 prohibits:

- task creation;
- futures;
- task handles;
- `gather`;
- `wait`;
- `as_completed`;
- shielding;
- timeout contexts;
- event-loop access;
- current-task inspection; and
- custom awaitables.

### 19.8 Cancellation

The host may cancel an async entrypoint.

Verified code cannot catch or inspect cancellation because exception handling is prohibited. The host is responsible for resource cleanup.

### 19.9 Async loops

Ordinary `for` and `while` loops are permitted in async functions. This is sufficient to express long-lived polling and server-sent-event loops with explicit awaited host operations.

`async for` and async generators are deferred.

---

## 20. Mutation model

### 20.1 Ordinary mutation prohibited

Verified code cannot mutate:

- a function argument;
- a record field;
- a tuple element;
- a module binding;
- a class namespace;
- a capability;
- a host reference; or
- any external object except through a known capability-authorized host operation.

### 20.2 Local rebinding

Rebinding a local name to a new value of the same exact type is permitted.

This is not object mutation:

```python
index = 0
index = index + 1
```

### 20.3 Mutable containers

`list`, `dict`, `set`, `bytearray`, and mutable user objects are prohibited in PurePy 0.1.

### 20.4 No mutating methods

Method calls are prohibited generally, so operations such as `append`, `extend`, `update`, `sort`, and `write` cannot appear.

### 20.5 Future local-builder extension

A later specification is expected to admit narrowly recognized locally allocated builders, beginning with:

```text
bytearray -> bytes
list[T]   -> tuple[T, ...]
```

Such an extension must prove that the mutable builder is fresh, uniquely named, never exposed, and frozen before escape. It is not part of PurePy 0.1.

---

## 21. Expected failures and host failures

### 21.1 Explicit result values

Expected application failures MUST be represented as Pure Values.

Example:

```python
from purepy import value


@value
class ParseError:
    message: str


@value
class ParseResult:
    request: Request | None
    error: ParseError | None
```

### 21.2 No `raise` or `try`

PurePy 0.1 prohibits explicit exception construction, raising, catching, suppression, and `finally` cleanup.

### 21.3 Trusted host contracts

A trusted host operation used in verified code SHOULD return expected failures as an explicit Pure Value result.

Unexpected failures MAY terminate the entrypoint through the host. Such failures are outside the verified application's ordinary result semantics.

### 21.4 Deterministic primitive failures

Deterministic failures of approved pure primitive operations are allowed under Section 7.6, but code cannot catch or inspect them.

---

## 22. External semantic manifests

### 22.1 Purpose

A semantic manifest describes the small statically relevant surface of host or native code without executing it.

### 22.2 Manifest scope

PurePy 0.1 manifests may declare only:

- import-safe modules;
- pure external value types;
- capability types;
- host-reference types;
- pure synchronous functions;
- pure asynchronous functions;
- capability-authorized synchronous functions; and
- capability-authorized asynchronous functions.

### 22.3 Conceptual schema

A manifest entry conceptually contains:

```text
module:
    name
    import_safe

type:
    qualified_name
    category = value | capability | host_ref
    capability_labels[]

function:
    qualified_name
    kind = sync | async
    parameters[]
    return_type
    trust = pure | host
```

The concrete serialization format is versioned separately.

### 22.4 Function declarations

A function declaration MUST provide:

- one exact qualified name;
- sync or async kind;
- a fixed ordered parameter list;
- exact parameter types and categories;
- one exact Pure Value return type; and
- a trust classification.

### 22.5 Pure external declarations

A trusted pure external function accepts only Pure Values, returns a Pure Value, and asserts that its behavior satisfies the PurePy purity guarantee.

This assertion is part of the trusted computing base.

### 22.6 Host operation declarations

A host operation MUST require at least one capability parameter unless it is a pure external operation.

Every externally observable effect of the host operation MUST be represented by the labels of one or more capability parameters in its declared signature. An operation that reads a database and a clock, for example, must require capabilities authorizing both categories. The host implementation is trusted to obey this declaration.

Host-reference parameters MAY additionally identify the target resource, but possession of a host reference never substitutes for capability authorization.

### 22.7 Import safety

A manifest module imported by verified source MUST be marked `import_safe`.

`import_safe` is a trust assertion that importing the module does not expose mutable ambient authority to verified code and that any import-time work is contained by the host boundary.

### 22.8 No executable plugins

PurePy 0.1 supports no executable verifier plugins.

Manifests are declarative data. Unknown fields MUST be rejected unless the schema version explicitly permits them.

### 22.9 Search order

The verifier MUST use a deterministic manifest search order defined by configuration.

Conflicting declarations are errors. Later manifests MUST NOT silently override earlier ones.

### 22.10 Trust reporting

The verifier MUST be able to report every trusted external declaration reachable from a configured entrypoint.

### 22.11 Minimality

PurePy 0.1 manifests do not describe:

- callbacks;
- methods;
- context managers;
- ownership transfer;
- borrowing;
- generators;
- task scopes;
- cancellation handlers;
- exception aggregation;
- canonical encodings;
- memoization behavior; or
- executable adapters.

---

## 23. Unsafe and excluded code

### 23.1 Single escape boundary

PurePy 0.1 has no source-level `@unsafe` decorator.

Code that cannot be verified belongs outside the verified source root and is accessed only through trusted manifest declarations.

### 23.2 No suppression comments

There is no directive equivalent to:

```text
purepy: ignore
```

A semantic violation cannot be suppressed locally.

A file may be excluded only by project configuration, in which case no claim is made about that file.

### 23.3 Boundary discipline

An excluded host module SHOULD expose:

- small free functions;
- narrow capability types;
- opaque host-reference types;
- Pure Value inputs and outputs; and
- no hidden ambient acquisition by verified callers.

### 23.4 No trust laundering

A host declaration cannot classify an operation as pure merely because its external effects are considered acceptable.

Filesystem, network, database, clock, random, environment, process, and mutable-state observations are not pure.

### 23.5 Native implementations

A C, Rust, or other native function may be declared pure when its trusted contract satisfies the PurePy purity guarantee. Native implementation alone does not make a function pure.

---

## 24. Standard-library policy

### 24.1 Sealed allowlist

PurePy 0.1 does not assume the Python standard library is pure or statically modelable.

Only sealed intrinsics and explicit standard manifests are available.

### 24.2 Representative approved pure operations

The reference implementation may include exact declarations for carefully reviewed operations over approved primitive types, such as selected functions from:

- `math`;
- pure string or bytes processing helpers; and
- deterministic encoding routines.

### 24.3 Representative effectful operations

Filesystem, network, clock, entropy, environment, process, logging, and stateful library operations require capability-bearing host declarations.

### 24.4 Prohibited dynamic facilities

Reflection, import machinery, inspection, tracing, multiprocessing objects, thread objects, async task objects, context variables, weak references, finalizers, and arbitrary serialization are prohibited unless a future specification admits a narrow form.

---

## 25. Project configuration

### 25.1 `purepy.toml`

A project is configured by `purepy.toml`.

A minimal configuration is:

```toml
[tool.purepy]
language = "0.1"
python_syntax = "3.14"
source_root = "src"
entrypoints = [
  "app.server.handle_connection",
]
manifests = [
  "manifests/host.purepy.toml",
]
```

### 25.2 Required fields

PurePy 0.1 configuration MUST identify:

- language version;
- target Python syntax version;
- one source root;
- zero or more entrypoints; and
- an ordered manifest list.

### 25.3 Exclusions

Files outside the source root are outside the guarantee.

Files inside the source root cannot be individually excluded in PurePy 0.1. This avoids accidental holes in the verified module graph.

### 25.4 Entrypoint validation

Every configured entrypoint MUST resolve to one top-level verified function.

The verifier MUST report:

- sync or async kind;
- Pure Value parameters;
- capability parameters and labels;
- host-reference parameters; and
- return type.

### 25.5 Configuration determinism

Environment-variable interpolation, executable configuration, and dynamic discovery are prohibited.

Paths are resolved relative to the configuration file.

---

## 26. Service expressiveness

### 26.1 Required architecture

A PurePy 0.1 service uses a trusted host for runtime mechanics and verified functions for application behavior:

```text
trusted host
    accept connection
    schedule invocation
    create capabilities and host references
        ↓
verified async entrypoint
    explicit receive
    pure parse
    pure route
    explicit database call
    pure domain logic
    pure encode
    explicit send
        ↓
trusted host
    cancellation and cleanup
```

### 26.2 Host-managed concurrency

The host MAY invoke the same verified async entrypoint concurrently for many requests or connections.

PurePy 0.1 does not permit one verified invocation to spawn another task. This does not prevent concurrent service operation.

### 26.3 Database interaction

Database I/O occurs through explicit async host functions requiring database capabilities.

Database rows and operation results MUST cross into verified code as Pure Values.

Hidden lazy I/O through property access is impossible because methods and properties are prohibited.

### 26.4 Transactions

PurePy 0.1 does not expose an interactive transaction resource.

A host MAY expose atomic or transactional one-shot functions accepting immutable plans and returning immutable results.

### 26.5 Server-sent events

SSE may be implemented with an ordinary async loop:

```python
async def send_events(
    database_read: DatabaseRead,
    network_write: NetworkWrite,
    clock_wait: ClockWait,
    connection: Connection,
    account_id: int,
) -> int:
    cursor = 0
    active = True
    while active:
        page = await load_event_page(database_read, account_id, cursor)
        for event in page.events:
            payload = encode_sse_event(event)
            sent = await send_bytes(network_write, connection, payload)
            if sent is False:
                active = False
            cursor = event.event_id
        if active:
            await sleep(clock_wait, 1.0)
    return cursor
```

The host owns connection closure and cancellation cleanup.

### 26.6 Static routing

Routing logic can be pure and first-order. Dynamic decorator registration is not required.

A route selector can return a small immutable route code or request classification that the entrypoint handles with ordinary conditionals.

### 26.7 Performance path

PurePy 0.1 permits nonblocking I/O and host-managed concurrency. A later local-builder extension is expected to make parsing and encoding efficient without exposing general mutable state.

---

## 27. Memoization and optimization

### 27.1 Purity fact

The verifier can report that a function is verified pure.

That fact is a necessary foundation for safe memoization, parallel evaluation, worker offloading, and content-addressed computation.

### 27.2 Not part of PurePy 0.1

PurePy 0.1 does not define:

- a memoization decorator;
- cache key encoding;
- semantic function hashes;
- persistent cache invalidation;
- distributed cache compatibility;
- single-flight execution; or
- optimization certificates.

### 27.3 Runtime caches remain external

An application-visible cache remains an effectful host facility requiring explicit capabilities.

An external in-process optimization may choose to memoize a verified pure function, but that optimization is outside PurePy 0.1 conformance unless a later specification defines its observability and invalidation rules.

### 27.4 Planned progression

The intended progression is:

1. report verified pure functions;
2. validate a narrow in-process memoization wrapper;
3. define conservative source and dependency hashes;
4. define canonical Pure Value encoding; and
5. consider persistent or distributed certificates only for a demonstrated consumer.

---

## 28. Diagnostics

### 28.1 Requirements

A diagnostic MUST include:

- stable code;
- severity;
- file and source range;
- concise explanation;
- relevant symbol and type information; and
- a deterministic explanation path when another declaration is involved.

### 28.2 Initial diagnostic families

The reference verifier should reserve families such as:

| Family | Meaning |
|---|---|
| `PP0xx` | configuration and unsupported syntax |
| `PP1xx` | modules, imports, and symbol resolution |
| `PP2xx` | type and Pure Value violations |
| `PP3xx` | calls and capability authorization |
| `PP4xx` | async restrictions |
| `PP5xx` | module initialization and mutation |
| `PP6xx` | manifest and trust errors |
| `PP7xx` | entrypoint and host-boundary errors |

### 28.3 Examples

Unknown dynamic call:

```text
app.py:18:12 PP301 unknown call target

    return registry[name](request)
           ^^^^^^^^^^^^^^

PurePy 0.1 requires a direct call to one statically resolved top-level function.
```

Missing capability:

```text
app.py:25:18 PP312 argument 1 requires capability `host.database.DatabaseRead`

    return await load_user(user_id)
                 ^^^^^^^^^

The caller has no `DatabaseRead` parameter to pass.
```

Stored coroutine:

```text
app.py:31:15 PP401 coroutine values are not first-class in PurePy 0.1

    pending = load_user(database_read, user_id)
              ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^

Directly await the known async call.
```

Host-reference alias:

```text
app.py:42:5 PP334 host references may not be aliased

    other = connection
    ^^^^^^^^^^^^^^^^^^

Pass `connection` directly to a known host operation.
```

### 28.4 No misleading fixes

The verifier SHOULD suggest a fix only when the replacement preserves the user's apparent intent and remains inside PurePy 0.1.

---

## 29. CLI contract

PurePy 0.1 defines four commands.

### 29.1 `purepy check`

```text
purepy check [path]
```

Checks the configured project and emits deterministic human-readable diagnostics.

Required options SHOULD include:

```text
--config PATH
--format text|json
--no-cache
--jobs N
```

### 29.2 `purepy explain`

```text
purepy explain FILE:LINE[:COLUMN]
```

Explains the resolved symbol, type, category, capability requirement, or rejection at a source location.

### 29.3 `purepy capabilities`

```text
purepy capabilities [QUALIFIED_FUNCTION]
```

Reports configured entrypoints or a selected function with:

- capability parameter types;
- capability labels;
- host-reference parameters; and
- trusted external operations directly reachable from the function body.

The report is not a transitive inferred effect proof; it is an authority and call summary.

### 29.4 `purepy cache clean`

Removes verifier analysis artifacts.

### 29.5 Deferred commands

Graph visualization, resource reports, unsafe reports, memoization certificates, daemon mode, and execution commands are deferred.

---

## 30. Verification algorithm

### 30.1 Discovery

The verifier:

1. loads deterministic configuration;
2. discovers `.py` files under the one source root;
3. maps paths to module names;
4. rejects ambiguous modules, namespace packages, and cycles; and
5. loads manifests in configured order.

### 30.2 Parsing

Every file is parsed without execution.

Parser-specific nodes MUST be isolated behind a frontend adapter. Unsupported syntax MUST still retain an accurate source range for diagnostics.

### 30.3 Module lowering

Each parsed module is lowered to a small semantic IR containing only:

- imports;
- constants;
- value records;
- functions;
- statements;
- expressions;
- annotations; and
- source spans.

The whole-program checker MUST NOT depend directly on tree-sitter node identities.

### 30.4 Symbol indexing

The verifier builds deterministic IDs for:

- modules;
- constants;
- value types;
- capability types;
- host-reference types;
- functions; and
- fields.

### 30.5 Import linking

All direct imports are resolved. Unknown, ambiguous, cyclic, aliased, dynamic, or re-exported symbols are rejected under the module rules.

### 30.6 Type resolution

The verifier normalizes every supported annotation to a small exact type representation.

No general constraint solver is required.

### 30.7 Local function checking

Each function is checked for:

- valid signature;
- parameter categories;
- local type consistency;
- definite assignment;
- exact expression typing;
- supported control flow;
- direct call resolution;
- capability and host-reference forwarding;
- async call/await correctness;
- Pure Value return type; and
- absence of unsupported syntax and mutation.

### 30.8 Local authority checking

For each call, the verifier compares actual capability and host-reference arguments to the callee signature.

No whole-program effect fixed point is needed because authority cannot be acquired implicitly.

### 30.9 Entrypoint checking

Configured entrypoints are resolved and summarized. Their parameter categories determine the host contract.

### 30.10 Reporting

Diagnostics and summaries are sorted by stable module name, source position, code, and deterministic tie-breaker.

### 30.11 Implementation resource limits

A verifier MAY reject input exceeding documented implementation resource limits,
including input that otherwise satisfies the language's typing and syntax rules.
Such rejection MUST produce an explicit error and MUST NOT produce successful verification by skipping analysis.

The reference verifier applies the following fixed limits per source file before
recursive frontend lowering. Native syntax nodes include punctuation and rejected
syntax; the root has depth zero. Overlapping text is the sum of each native node's
source-span byte length, so the same source bytes can contribute more than once.

| Resource | Limit | Failure behavior |
| --- | ---: | --- |
| Native syntax-tree depth | 512 | `PP003` at the node exceeding the limit; no semantic tree is accepted. |
| Native syntax nodes | 100,000 | `PP003` at the node exceeding the limit; no semantic tree is accepted. |
| Overlapping native-node text | 64 MiB | `PP003` at the node exceeding the limit; no semantic tree is accepted. |
| Frontend diagnostics | 1,024 ordinary errors | One final `PP003` explains that further diagnostics were omitted. |

Cache decoding has independent defensive bounds, including a 64 MiB entry size,
100,000 lowered nodes, depth 1,024, and structural JSON budgets. Cache data that
exceeds these bounds is ignored and source analysis is recomputed; a cache miss
does not relax the source limits or semantic checks. These limits bound selected
allocation and traversal risks and do not promise a universal wall-time or memory
bound for the native parser, whole project, or executing Python program.

---

## 31. Performance and determinism requirements

### 31.1 Reference implementation

The reference verifier SHOULD be implemented in Go with a production-quality Python syntax parser such as tree-sitter-python behind an isolated adapter.

### 31.2 Bounded concurrency

Parsing, lowering, and independent local checking SHOULD use bounded worker pools. The worker count defaults to an implementation-defined value related to `GOMAXPROCS` and may be overridden by `--jobs`.

Unbounded goroutine creation is prohibited.

### 31.3 Deterministic merge

Concurrent work products MUST be merged in stable order. Output MUST NOT depend on goroutine scheduling or Go map iteration.

### 31.4 Module summaries

Each unchanged module may be represented by a compact cached summary containing:

- source hash;
- syntax version;
- imported names;
- exported declarations;
- normalized signatures;
- value-record layouts;
- function bodies or local-check summaries as needed;
- diagnostics; and
- verifier/schema version.

### 31.5 Initial invalidation model

PurePy 0.1 may use a deliberately simple invalidation policy:

- reuse summaries for source-identical modules;
- when any relevant module or manifest changes, relink the project;
- rerun inexpensive global validation; and
- recheck only functions whose cached local facts are not reusable.

Fine-grained semantic-interface invalidation is deferred until measurements justify it.

### 31.6 Cache correctness

Cached and uncached runs MUST produce byte-for-byte equivalent machine-readable results.

Cache corruption or schema mismatch MUST fall back safely to reanalysis.

### 31.7 No performance-based weakening

Performance optimizations MUST NOT cause unknown operations to be accepted, skip validation, or make results schedule-dependent.

---

## 32. Conformance

### 32.1 Conforming verifier

A conforming PurePy 0.1 verifier MUST:

- accept every valid required conformance fixture;
- reject every invalid required conformance fixture;
- implement the closed syntax and semantic rules in this document;
- never execute analyzed project code;
- reject unknown behavior;
- produce deterministic machine-readable results; and
- identify its supported specification and language versions.

### 32.2 Conforming program

A program conforms when:

- every file under the configured source root conforms;
- every import and call resolves;
- all manifest dependencies are available and valid;
- every configured entrypoint resolves and validates; and
- the verifier reports no errors.

### 32.3 Trusted computing base

The guarantee depends on:

- the Python runtime executing accepted constructs according to the assumptions encoded by PurePy;
- the `purepy` support package correctly implementing `@value`;
- trusted external manifests accurately describing their implementations;
- the host respecting parameter categories and lifetimes; and
- the verifier implementation being correct.

### 32.4 Conformance fixtures

The conformance suite MUST include positive and negative fixtures for:

- every supported statement and expression;
- every prohibited syntax family;
- module and import rules;
- value records;
- exact type matching;
- optional narrowing;
- capability forwarding;
- host-reference restrictions;
- direct async usage;
- pure and effectful call boundaries;
- manifest validation; and
- deterministic diagnostics.

### 32.5 Reference service workload

Before PurePy 0.1 is considered complete, a reference workload MUST demonstrate:

- host-managed concurrent entrypoint invocation;
- an async verified handler;
- immutable HTTP-like request parsing;
- static routing;
- one database read;
- one atomic database write or transaction-plan call;
- an SSE-style send loop;
- pure domain and rendering functions; and
- acceptable verifier and runtime performance under a documented load test.

The workload is a conformance and expressiveness test, not a supported framework.

---

## 33. Deferred extensions

The following order is recommended after PurePy 0.1.

### 33.1 Narrow local builders

Admit syntactically bounded fresh local mutation for:

1. `bytearray` converted to `bytes`;
2. `list[T]` converted to `tuple[T, ...]`.

No general borrowing or ownership system is required.

### 33.2 Bounded `parallel_join`

Add one structured concurrency intrinsic that:

- accepts a fixed number of direct async calls;
- exposes no task handles;
- joins every child before returning;
- preserves result order;
- rejects duplicated nonshareable host references; and
- has fixed cancellation and failure semantics.

### 33.3 Lexically scoped resources

Add one narrow approved lexical resource form before considering general ownership.

The first form should prohibit:

- resource return;
- ownership transfer;
- borrowing;
- storage;
- task sharing; and
- exception suppression.

### 33.4 Checked in-process memoization

Permit a recognized memoization wrapper only around verified pure functions with Pure Value inputs and outputs.

### 33.5 Semantic hashes

Add conservative source and dependency hashing only after a concrete cache consumer exists.

### 33.6 Streams

Generators and async generators may be considered only after real programs demonstrate that explicit loops and page-oriented host calls are insufficient.

### 33.7 Richer concurrency and ownership

General task scopes, task handles, borrowing, transfer, shared mutable resources, and cancellation aggregation are last-resort extensions, not assumed milestones.

---

## 34. Complete illustrative service slice

The following is non-normative but illustrates the intended architecture.

### 34.1 Pure data

```python
from purepy import value


@value
class Request:
    method: str
    path: str
    body: bytes


@value
class ParseResult:
    request: Request | None
    error_status: int | None


@value
class User:
    user_id: int
    display_name: str


@value
class UserResult:
    user: User | None
    found: bool
```

### 34.2 Pure application functions

```python
from host.text import encode_utf8


def parse_request(payload: bytes) -> ParseResult:
    # A real implementation uses only approved immutable operations.
    if len(payload) == 0:
        return ParseResult(request=None, error_status=400)
    request = Request(method="GET", path="/users/1", body=b"")
    return ParseResult(request=request, error_status=None)


def render_user(user: User) -> bytes:
    text = f"{user.user_id}:{user.display_name}"
    return encode_utf8(text)
```

`encode_utf8` is a trusted pure free function declared in the host manifest. Arbitrary methods remain prohibited.

### 34.3 Effectful async entrypoint

```python
from host.database import DatabaseRead, load_user
from host.network import Connection, NetworkRead, NetworkWrite, receive_bytes, send_bytes


async def handle_connection(
    network_read: NetworkRead,
    network_write: NetworkWrite,
    database_read: DatabaseRead,
    connection: Connection,
) -> int:
    payload = await receive_bytes(network_read, connection)
    parsed = parse_request(payload)

    if parsed.request is None:
        await send_bytes(network_write, connection, b"bad request")
        return 400

    result = await load_user(database_read, 1)
    if result.user is None:
        await send_bytes(network_write, connection, b"not found")
        return 404

    body = render_user(result.user)
    await send_bytes(network_write, connection, body)
    return 200
```

The host may invoke `handle_connection` concurrently. The verifier need not model listener ownership, task creation, connection closure, or event-loop internals.

---

## 35. Summary

PurePy 0.1 is intentionally small:

```text
Python 3.14 syntax subset
+ first-order top-level functions
+ concrete immutable values
+ data-only records
+ exact direct calls
+ explicit capability parameters
+ opaque host references
+ direct await
+ host-managed concurrency and resources
```

It deliberately excludes:

```text
methods
inheritance
callbacks
generics
exceptions
generators
tasks
context managers
resource ownership
general mutation
persistent memoization certificates
```

The defining invariant is:

> A function can exercise only the external authority explicitly present in its parameter list, and a function accepting only Pure Values has no route to ambient state or observable mutation.
