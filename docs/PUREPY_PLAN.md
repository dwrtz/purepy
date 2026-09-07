# Historical PurePy Implementation Plan

This plan records the original implementation. The current language and release
contract are [PurePy 0.2](PUREPY_SPEC.md) and [Functional core](FUNCTIONAL_CORE.md).
Compatibility with the original language is not supported.

**File:** `PUREPY_PLAN.md`
**Plan version:** `0.2-draft`
**Normative specification:** `PUREPY_SPEC.md`, specification `0.3-draft`, PurePy language `0.1`
**Target source syntax:** Python 3.14
**Reference implementation language:** Go
**Status:** Implementation plan
**Last updated:** 2026-09-05

---

## 1. Purpose

This document defines the implementation plan for the PurePy 0.1 reference verifier.

PurePy 0.1 is intentionally much smaller than earlier drafts. It is a closed-world, first-order, monomorphic language using a strict subset of Python syntax. Ordinary values are deeply immutable. External authority enters verified code only through explicit capability parameters. Host-owned resource references may be forwarded only as direct call arguments. Async support consists of top-level `async def` and direct `await` of a known async call.

The implementation does **not** include:

- general Python type checking;
- method or protocol resolution;
- higher-order functions;
- whole-program effect fixed points;
- first-class coroutine values;
- resource ownership analysis;
- structured task analysis;
- generators;
- exception-flow analysis;
- memoization certificates; or
- an application framework.

The plan has four release outcomes:

1. A fast deterministic Go verifier for the complete PurePy 0.1 language.
2. A tiny Python support package containing `@value` and no application framework.
3. A minimal declarative host-manifest format.
4. A reference nonblocking service proving that host-managed concurrency, explicit database and network effects, SSE-style loops, and pure domain logic are practical.

When this plan conflicts with `PUREPY_SPEC.md`, the specification is authoritative.

---

## 2. Scope guard

Every implementation decision must preserve these boundaries.

### 2.1 PurePy is a verifier

Core packages MUST NOT implement:

- HTTP;
- routing;
- sockets;
- an event loop;
- database protocols;
- connection pools;
- transactions;
- SSE framing;
- task scheduling;
- resource cleanup;
- application/runtime cache storage; or
- deployment.

The reference service may contain such code only in its excluded host fixture.

### 2.2 PurePy does not model arbitrary Python

The verifier recognizes only the semantics explicitly admitted by the specification.

Unsupported Python syntax or object-model behavior is rejected rather than approximated.

### 2.3 External authority is typed dataflow

The implementation must never infer that a call is authorized merely because a function has a broad effect annotation.

Authorization comes from exact capability arguments at the call site.

### 2.4 Unknown means error

Unresolved symbols, annotations, imports, calls, fields, operators, manifest entries, or async forms are errors.

### 2.5 No analyzed-code execution

The verifier MUST NOT:

- invoke Python;
- import project modules;
- execute decorators;
- inspect live objects;
- load executable plugins; or
- run host code.

Development-only differential tests may invoke a pinned Python interpreter outside the production analysis path.

### 2.6 No speculative generality

A feature should not be generalized in anticipation of future profiles.

In particular, v0.1 data structures must not be distorted to pre-implement:

- ownership transfer;
- borrowing;
- callbacks;
- task scopes;
- generator closing;
- exception aggregation; or
- canonical memoization encodings.

### 2.7 Deterministic behavior

Diagnostics, IDs, reports, cache keys, and manifest resolution MUST be independent of filesystem order, goroutine scheduling, and Go map iteration.

### 2.8 Performance through architecture

Parsing, lowering, and function checking must be independently parallelizable. Global linking should consume compact summaries, not parser nodes.

---

## 3. Deliverables

### 3.1 `purepy` Go binary

The initial binary provides:

```text
purepy check
purepy explain
purepy capabilities
purepy cache clean
```

It supports text and JSON diagnostics and deterministic exit codes.

### 3.2 Python support package

The package contains only the runtime pieces required for valid PurePy source to execute:

```text
python/purepy/
    __init__.py
    value.py
    py.typed
```

The public surface for v0.1 is:

```python
from purepy import value
```

The `@value` implementation should produce immutable slotted records with stable value equality. It must not perform registration, reflection-based discovery, dependency injection, or verifier communication at runtime.

### 3.3 Manifest schema

The manifest schema describes only:

- import-safe modules;
- value, capability, and host-reference types;
- sync and async free functions;
- concrete parameter types; and
- concrete Pure Value return types.

### 3.4 Sealed intrinsic table

The verifier ships a versioned intrinsic table for approved operations over exact primitive types.

### 3.5 Conformance suite

The repository includes positive and negative fixtures for every normative language rule.

### 3.6 Reference service

The example contains:

- an excluded ordinary-Python host;
- a verified async handler;
- immutable request and result records;
- pure parsing, routing, domain, and rendering functions;
- explicit async network reads and writes;
- explicit async database reads and atomic writes;
- an SSE-style loop; and
- a load-test harness.

The example is not published as a framework package.

### 3.7 Documentation

Required documentation includes:

```text
docs/LANGUAGE_GUIDE.md
docs/HOST_BOUNDARY.md
docs/MANIFESTS.md
docs/DIAGNOSTICS.md
docs/CONFORMANCE.md
```

---

## 4. Repository layout

The recommended initial layout is:

```text
purepy/
├── cmd/
│   └── purepy/
│       └── main.go
├── internal/
│   ├── app/
│   │   ├── check.go
│   │   ├── explain.go
│   │   └── capabilities.go
│   ├── cache/
│   │   ├── cache.go
│   │   ├── key.go
│   │   └── schema.go
│   ├── check/
│   │   ├── module.go
│   │   ├── function.go
│   │   ├── flow.go
│   │   ├── calls.go
│   │   ├── async.go
│   │   └── intrinsics.go
│   ├── config/
│   │   ├── config.go
│   │   └── load.go
│   ├── diag/
│   │   ├── code.go
│   │   ├── diagnostic.go
│   │   ├── render_json.go
│   │   └── render_text.go
│   ├── discovery/
│   │   ├── discover.go
│   │   ├── modules.go
│   │   └── packages.go
│   ├── frontend/
│   │   ├── parser.go
│   │   ├── treesitter.go
│   │   ├── lower.go
│   │   └── unsupported.go
│   ├── intrinsic/
│   │   ├── table.go
│   │   ├── numeric.go
│   │   ├── sequence.go
│   │   └── text.go
│   ├── manifest/
│   │   ├── schema.go
│   │   ├── parse.go
│   │   ├── validate.go
│   │   └── merge.go
│   ├── model/
│   │   ├── id.go
│   │   ├── span.go
│   │   ├── module.go
│   │   ├── decl.go
│   │   ├── stmt.go
│   │   ├── expr.go
│   │   ├── types.go
│   │   └── category.go
│   ├── resolve/
│   │   ├── index.go
│   │   ├── imports.go
│   │   ├── annotations.go
│   │   └── calls.go
│   ├── summary/
│   │   ├── module.go
│   │   ├── encode.go
│   │   └── decode.go
│   └── workpool/
│       └── pool.go
├── python/
│   ├── purepy/
│   │   ├── __init__.py
│   │   ├── value.py
│   │   └── py.typed
│   └── tests/
├── manifests/
│   ├── schema/
│   └── stdlib/
├── fixtures/
│   ├── conformance/
│   │   ├── valid/
│   │   └── invalid/
│   ├── projects/
│   └── manifests/
├── examples/
│   └── reference_service/
│       ├── host/
│       ├── src/
│       ├── manifests/
│       └── loadtest/
├── docs/
├── tools/
├── go.mod
├── purepy.toml
├── PUREPY_SPEC.md
└── PUREPY_PLAN.md
```

Package boundaries are intentional. Parser-specific types stay in `internal/frontend`. Language-level structures live in `internal/model`. No package outside `frontend` may depend on tree-sitter node types.

---

## 5. Reference architecture

### 5.1 Pipeline

```text
configuration
      ↓
module discovery ───────────────┐
      ↓                         │
parallel parse                  │
      ↓                         │
parallel lowering               │
      ↓                         │
module declaration summaries    │
      └──────────┬──────────────┘
                 ↓
          deterministic link
                 ↓
     parallel local function checks
                 ↓
      entrypoint and trust reports
                 ↓
     deterministic diagnostics/output
```

### 5.2 Why no effect graph

Earlier designs required a whole-program call graph and fixed-point propagation of inferred effects.

PurePy 0.1 does not need that algorithm. A capability-authorized call is valid only when the caller passes an exact capability parameter. Authority cannot be acquired from globals or unknown calls.

The implementation may retain direct call edges for explanation and reports, but correctness does not depend on transitive effect inference.

### 5.3 Why no ownership CFG

Capabilities are shareable authority tokens, not owned resources. Host references are host-owned and may only be forwarded directly from parameters.

Because verified code cannot alias, store, return, close, transfer, or spawn tasks with these values, no general ownership state machine is required.

### 5.4 Why no coroutine state machine

The parser accepts only direct `await known_async_call(...)` forms. It never admits an assignable coroutine expression into semantic IR.

The lowerer should represent direct await as a single `AwaitCallExpr`, not as separate “create coroutine” and “consume coroutine” operations.

---

## 6. Core implementation model

### 6.1 Stable identifiers

All semantic identities should derive from stable names, not discovery order.

Recommended IDs:

```text
ModuleID       = hash(canonical module name)
TypeID         = hash(module name + type name)
FunctionID     = hash(module name + function name)
FieldID        = hash(TypeID + field name)
CapabilityID   = hash(qualified capability type)
ManifestID     = hash(canonical manifest path + content hash)
```

The hash function and canonical encoding must be versioned.

### 6.2 Source spans

`internal/model/span.go` should define byte offsets and line/column information independent of tree-sitter.

Every lowered node and diagnostic-relevant declaration must retain a span.

### 6.3 Type representation

`internal/model/types.go` should use a small closed tagged union:

```text
None
Bool
Int
Float
Str
Bytes
Tuple(element TypeID)
Optional(inner TypeID)
ValueRecord(TypeID)
Capability(TypeID)
HostRef(TypeID)
EphemeralRange
Unknown
Invalid
```

There is no `Any`, union set, type variable, method receiver, callable type, task type, resource ownership qualifier, or generic substitution environment.

### 6.4 Value categories

`internal/model/category.go` should define:

```text
PureValue
Capability
HostReference
EphemeralIntrinsic
Unknown
```

Type and category are related but distinct so diagnostics can explain why an exact type cannot cross a boundary.

### 6.5 Function signature

A normalized signature should contain:

```text
FunctionSignature
    id
    kind: sync | async
    parameters[]:
        name
        type
        category
        span
    return_type
    origin: project | manifest | intrinsic
    trust: verified | trusted_pure | trusted_host
```

### 6.6 Semantic IR

The IR must contain only admitted or explicitly unsupported forms.

Important expression nodes:

```text
LiteralExpr
NameExpr
TupleExpr
FieldExpr
UnaryExpr
BinaryExpr
BooleanExpr
CompareExpr
ConditionalExpr
IndexExpr
SliceExpr
CallExpr
AwaitCallExpr
ValueConstructExpr
UnsupportedExpr
```

Important statement nodes:

```text
AssignStmt
IfStmt
ForStmt
WhileStmt
BreakStmt
ContinueStmt
ReturnStmt
ExprStmt
PassStmt
UnsupportedStmt
```

There is intentionally no generic attribute-call node, callback node, coroutine-value node, yield node, context-manager node, task node, or exception handler in v0.1 analysis.

### 6.7 Module summary

A serializable module summary should contain:

```text
schema_version
verifier_version
language_version
python_syntax_version
source_hash
module_name
imports[]
constants[]
value_records[]
function_signatures[]
function_bodies_or_local_facts[]
diagnostics[]
```

The first cache implementation may retain normalized function bodies to allow rechecking after dependency-signature changes without reparsing.

### 6.8 Direct call edge

For explanation and capability reports, record:

```text
CallEdge
    caller FunctionID
    callee FunctionID
    kind sync | await
    span
    capability_arguments[]
    host_ref_arguments[]
```

No fixed-point lattice is needed.

---

## 7. Milestone overview

| Phase | Result |
|---|---|
| 0 | Language freeze, ADRs, fixture format, repository scaffold |
| 1 | Deterministic project discovery, configuration, parser adapter |
| 2 | Minimal semantic IR, imports, symbols, annotations, direct resolution |
| 3 | Pure Values, records, control flow, intrinsics, local purity checking |
| 4 | Manifest schema, capabilities, host references, trust reporting |
| 5 | Restricted `async def` and direct `await` |
| 6 | Parallel checking, content-addressed summaries, CLI and diagnostics hardening |
| 7 | Reference service, conformance completion, PurePy 0.1 release |

Every phase ends with executable tests and an explicit list of deferred work.

---

## 8. Phase 0 — Freeze the minimal language

### 8.1 Objective

Turn the simplified specification into implementation constraints before writing semantic code.

### 8.2 Deliverables

#### Repository scaffold

Create:

```text
cmd/purepy/main.go
internal/model/
internal/diag/
internal/config/
fixtures/conformance/
python/purepy/
```

The initial binary may print version information and validate that a configuration file exists.

#### Architecture decision records

Create these files:

```text
docs/adr/0001-python-syntax-purepy-semantics.md
docs/adr/0002-first-order-monomorphic-language.md
docs/adr/0003-capability-parameter-authorization.md
docs/adr/0004-direct-await-only.md
docs/adr/0005-host-owned-resources.md
docs/adr/0006-no-source-level-unsafe.md
docs/adr/0007-no-executable-plugins.md
docs/adr/0008-simple-cache-invalidation.md
```

Each ADR must state alternatives rejected and the evidence required to reopen the decision.

#### Syntax matrix

Create `docs/SYNTAX_MATRIX.md` with one row for every Python statement and expression family:

```text
accepted
accepted with exact restriction
rejected in 0.1
deferred extension
```

#### Fixture metadata

Define a fixture metadata format in `fixtures/README.md`:

```toml
name = "missing database capability"
expect = "invalid"
codes = ["PP312"]
language = "0.1"
```

#### Diagnostic code registry

Create `internal/diag/code.go` and `docs/DIAGNOSTICS.md` with reserved families and stability policy.

### 8.3 Tests

- Go unit test for deterministic version output.
- Configuration smoke test.
- Fixture-loader tests.
- Diagnostic-code uniqueness test.
- CI check that specification, syntax matrix, and fixture categories do not contradict one another.

### 8.4 Exit criteria

- All v0.1 syntax decisions are explicit.
- No milestone depends on ownership, generators, callbacks, or general effect inference.
- Repository builds and tests on supported development platforms.
- The coding-agent issue sequence in Section 24 is approved.

### 8.5 Deferred

All parsing and semantic verification.

---

## 9. Phase 1 — Configuration, discovery, and parsing

### 9.1 Objective

Read `purepy.toml`, discover one deterministic module graph, and parse every source file without executing Python.

### 9.2 Relevant files

```text
internal/config/config.go
internal/config/load.go
internal/discovery/discover.go
internal/discovery/modules.go
internal/discovery/packages.go
internal/frontend/parser.go
internal/frontend/treesitter.go
internal/model/span.go
internal/workpool/pool.go
cmd/purepy/main.go
```

### 9.3 Configuration

Implement the exact v0.1 fields:

```text
language
python_syntax
source_root
entrypoints[]
manifests[]
```

Reject:

- unknown keys;
- duplicate keys;
- environment interpolation;
- missing source root;
- multiple source roots;
- unsupported versions; and
- paths escaping the project root unless explicitly permitted by a later security policy.

### 9.4 Discovery

Implement:

- normalized absolute project root;
- one normalized source root;
- recursive `.py` discovery;
- deterministic sorting by canonical relative path;
- package validation through `__init__.py`;
- path-to-module-name mapping;
- duplicate and ambiguous module rejection;
- symlink policy; and
- exclusion of cache/build directories only when they are outside the source root or normatively ignored by configuration format.

Namespace packages are rejected.

### 9.5 Parser adapter

Use tree-sitter-python or an equivalent production parser behind:

```go
// internal/frontend/parser.go
package frontend

type Parser interface {
    Parse(path string, source []byte) (*ParsedFile, error)
}
```

No other package may import tree-sitter types.

### 9.6 Source positions

Convert parser ranges into the stable `model.Span` representation.

Tests must cover:

- ASCII;
- UTF-8 identifiers and strings;
- CRLF and LF;
- tabs;
- multiline expressions; and
- syntax errors near EOF.

### 9.7 Unsupported syntax collection

The parser phase should identify grammar-valid syntax families without deciding all semantics. Unsupported syntax nodes must lower later to explicit `Unsupported` IR with source spans.

### 9.8 Parallel parsing

Implement a bounded worker pool. Results are stored by canonical module name and merged only after all workers complete.

### 9.9 CLI

`purepy check` should now report:

- configuration failures;
- discovery failures;
- Python parse errors; and
- basic file counts.

### 9.10 Tests

- golden module-name mapping;
- namespace-package rejection;
- package initializer detection;
- deterministic discovery under randomized filesystem creation order;
- parser corpus for every Python 3.14 statement family;
- malformed syntax diagnostics;
- worker-count equivalence for `--jobs 1`, `2`, and default;
- race-detector run; and
- cancellation of worker pool on fatal configuration errors.

### 9.11 Exit criteria

- Every source file is deterministically discovered and parsed.
- No analyzed code executes.
- Parser nodes are isolated to `internal/frontend`.
- Output is identical across worker counts.

### 9.12 Deferred

Imports, symbols, annotations, semantic checks, manifests, and caching.


---

## 10. Phase 2 — Semantic IR, modules, symbols, and annotations

### 10.1 Objective

Lower parsed files into parser-independent IR, enforce module shape, resolve direct imports, and normalize the small type language.

### 10.2 Relevant files

```text
internal/frontend/lower.go
internal/frontend/unsupported.go
internal/model/module.go
internal/model/decl.go
internal/model/stmt.go
internal/model/expr.go
internal/model/types.go
internal/model/category.go
internal/model/id.go
internal/resolve/index.go
internal/resolve/imports.go
internal/resolve/annotations.go
```

### 10.3 Semantic lowering

Lower every relevant syntax node into the closed IR.

The lowerer should preserve unsupported syntax rather than crashing or silently dropping it:

```go
// internal/model/stmt.go
type UnsupportedStmt struct {
    Kind string
    Span Span
}
```

The semantic checker later emits the normative diagnostic.

### 10.4 Module-shape validation

Recognize only:

- module docstring;
- `from ... import ...`;
- `Final` constant declarations;
- `@value` record declarations;
- top-level `def`; and
- top-level `async def`.

A package `__init__.py` is a special case and may contain only an optional docstring.

Emit immediate diagnostics for executable module statements, package-initializer imports or declarations, import aliases, relative imports, `import module`, star imports, and script blocks.

### 10.5 Declaration index

Build a deterministic project index before resolving function bodies.

The index contains:

- module names;
- local declaration names;
- declaration kind;
- exact source span;
- exported visibility; and
- stable ID.

Duplicate declarations are errors.

### 10.6 Import linking

Resolve each direct imported symbol to exactly one project declaration or, temporarily, an unresolved external placeholder to be completed in Phase 4.

Implement cycle detection over project-module imports. The cycle diagnostic should show one deterministic cycle path.

Do not implement:

- package re-exports;
- alias tracking;
- wildcard expansion;
- implicit namespace lookup; or
- Python import-hook behavior.

### 10.7 Annotation parser

Normalize only:

```text
None
bool
int
float
str
bytes
tuple[T, ...]
T | None
project @value type
external value type placeholder
external capability type placeholder
external host-ref type placeholder
Final[T]
```

All other forms lower to `InvalidType` with a diagnostic.

### 10.8 Function signature normalization

Validate:

- top-level location;
- fixed parameters;
- no defaults;
- no variadics;
- complete annotations;
- explicit return type;
- no decorators; and
- no type parameters.

At this phase, unresolved external types may remain placeholders linked in Phase 4.

### 10.9 Value-record declaration extraction

Extract field names, annotations, order, and spans. Reject methods and all executable class-body content.

Full field-category validation happens in Phase 3.

### 10.10 Tests

- lowering golden tests for every accepted IR node;
- one negative fixture per unsupported statement and expression family;
- import graph cycle tests;
- duplicate module and declaration tests;
- exact source-span tests;
- annotation acceptance/rejection matrix;
- function signature matrix;
- value-record class-body matrix;
- deterministic stable-ID tests; and
- fuzzing of lowering over syntactically valid parser trees.

### 10.11 Exit criteria

- Parser-independent IR exists for every module.
- Project declarations and imports link deterministically.
- Every annotation is normalized or rejected.
- No method, callback, generic, overload, or dynamic import machinery exists in the model.

### 10.12 Deferred

Expression typing, flow checking, intrinsics, manifests, capability categories, and async semantics.

---

## 11. Phase 3 — Pure Values and local synchronous verification

### 11.1 Objective

Verify complete synchronous PurePy modules that use only project declarations, `@value` records, constants, and sealed intrinsics.

### 11.2 Relevant files

```text
internal/check/module.go
internal/check/function.go
internal/check/flow.go
internal/check/calls.go
internal/check/intrinsics.go
internal/intrinsic/table.go
internal/intrinsic/numeric.go
internal/intrinsic/sequence.go
internal/intrinsic/text.go
internal/model/types.go
python/purepy/value.py
python/tests/test_value.py
```

### 11.3 Pure Value classification

Implement recursive classification for:

- primitive types;
- homogeneous tuples;
- optional Pure Values; and
- value records.

Detect recursive record definitions and reject them in v0.1 unless a later specification explicitly admits recursive immutable values.

### 11.4 `@value` support package

Implement the complete runtime decorator in `python/purepy/value.py`.

Required behavior:

- frozen instances;
- slots;
- field-order construction;
- explicit keyword construction;
- value equality;
- stable field access;
- no user methods;
- no runtime registry;
- no I/O; and
- no mutation after construction.

The verifier is authoritative about valid class shapes. Runtime checks may defend against accidental misuse but must remain small.

### 11.5 Constant checking

Validate module constants:

- exact `Final[T]` annotation;
- initializer constant expression;
- references only to earlier known constants;
- no cycles;
- no capability or host-reference types; and
- exact initializer type.

### 11.6 Expression typing

Implement exact typing for:

- literals;
- tuples;
- names;
- record construction;
- record field reads;
- arithmetic and bitwise operators;
- boolean operators;
- exact comparisons;
- optional-`None` checks;
- membership;
- indexing and slicing;
- conditional expressions;
- approved f-strings;
- direct synchronous calls; and
- sealed intrinsics.

Every operator table entry must name exact input and output types. There is no fallback method lookup.

### 11.7 Local flow checker

Implement a small forward dataflow analysis for:

- first-assignment type inference;
- same-exact-type rebinding;
- definite assignment;
- branch joins;
- optional narrowing on `is None` and `is not None`;
- loop-variable typing;
- valid `break` and `continue` placement;
- return-path validation; and
- unreachable code reporting where obvious.

This is a local function analysis, not a general SSA optimizer.

### 11.8 Statement checks

Enforce:

- local-name assignment only;
- no augmented assignment;
- exact boolean conditions;
- approved `for` iterables;
- no loop `else`;
- expression statements returning `None` only;
- no prohibited statement families; and
- no function or class nesting.

### 11.9 Direct project calls

Resolve direct calls to project functions and value constructors.

At each call:

- target must be unique;
- argument count must match;
- keywords must match exact parameter names;
- no unpacking;
- argument types must match exactly; and
- synchronous functions may call only synchronous functions.

Capability-bearing signatures do not exist until Phase 4, so every accepted function in this phase is pure.

### 11.10 Intrinsics

Build the intrinsic table as data plus small check functions. Every intrinsic requires:

- stable name;
- exact accepted signatures;
- exact result type;
- pure semantics statement;
- positive tests;
- negative near-miss tests; and
- runtime parity tests against the selected Python version where applicable.

Avoid creating a generic Python protocol engine.

### 11.11 Purity report

The checker should be able to report every successfully verified function as pure at this phase.

### 11.12 Tests

- positive and negative fixtures for every value type;
- deep-immutability tests;
- record construction and field-access tests;
- operator matrix tests;
- exact boolean-condition tests;
- local assignment and branch-merge tests;
- loop typing tests;
- optional narrowing tests;
- direct call tests;
- recursion tests;
- prohibited-method tests;
- no-object-identity tests;
- no-hash tests;
- no-reflection tests;
- Python runtime tests for `@value`; and
- differential intrinsic tests against a pinned Python runtime in development CI.

### 11.13 Exit criteria

- A multi-module synchronous functional program can be verified.
- Every accepted function has only Pure Value parameters and results.
- No operation can invoke user-defined Python dispatch.
- Unknown calls and types fail closed.
- `@value` records execute correctly under normal Python.

### 11.14 Deferred

External declarations, capabilities, host references, async functions, caching, and repository performance work.

---

## 12. Phase 4 — Manifests, capabilities, and the host boundary

### 12.1 Objective

Admit narrow trusted host and native operations without introducing source-level unsafe code or whole-program effect inference.

### 12.2 Relevant files

```text
internal/manifest/schema.go
internal/manifest/parse.go
internal/manifest/validate.go
internal/manifest/merge.go
internal/resolve/index.go
internal/resolve/imports.go
internal/resolve/annotations.go
internal/check/calls.go
internal/check/function.go
internal/app/capabilities.go
manifests/schema/
docs/MANIFESTS.md
docs/HOST_BOUNDARY.md
```

### 12.3 Manifest schema v1

Define a compact versioned schema. A conceptual TOML form may look like:

```toml
schema = 1

[[module]]
name = "host.database"
import_safe = true

[[type]]
name = "host.database.DatabaseRead"
category = "capability"
labels = ["database.read"]

[[type]]
name = "host.database.UserResult"
category = "value"

[[function]]
name = "host.database.load_user"
kind = "async"
trust = "host"
parameters = [
  { name = "database_read", type = "host.database.DatabaseRead" },
  { name = "user_id", type = "int" },
]
returns = "app.types.UserResult"
```

The exact format should avoid embedded expressions or executable conditions.

### 12.4 Schema validation

Reject:

- unknown schema versions;
- unknown fields;
- duplicate declarations;
- invalid qualified names;
- unknown type references;
- capability types without labels;
- host functions without capability parameters;
- non-Pure Value return types;
- methods;
- variadics;
- overloads;
- callbacks;
- ownership fields;
- generator fields;
- task fields; and
- optimization fields.

### 12.5 External type linking

Complete the placeholders introduced in Phase 2.

External `value` types may cross PurePy boundaries only when the manifest declares their deep immutability contract. They remain opaque unless individual fields are separately and statically exposed through a future manifest version; v0.1 application records are preferred.

External capability and host-reference types receive their categories and stable IDs from the manifest.

### 12.6 Import-safe modules

A verified module may directly import a declared external symbol only from a manifest module marked `import_safe`.

The verifier does not inspect the implementation. The trust report must identify the assertion.

### 12.7 Function classification

Once parameter categories are resolved:

- functions with only Pure Value parameters are pure candidates;
- functions with any capability or host-reference parameter are effectful verified functions.

No decorator or inferred effect set is involved.

### 12.8 Capability call checking

At every direct call:

- a capability parameter must receive the exact incoming capability parameter name;
- a host-reference parameter must receive the exact incoming host-reference parameter name;
- a Pure Value parameter receives an exact typed Pure Value expression;
- capability and host-reference values cannot be assigned, returned, stored, formatted, compared, or passed to pure parameters; and
- an effectful external function must require at least one exact capability.

### 12.9 Host-reference checking

Implement the direct-forwarding rule syntactically and semantically.

No ownership state is needed. The checker needs only to recognize that a capability or host-reference name appears in a forbidden context or a mismatched call position.

### 12.10 Pure external functions

Support manifest entries with `trust = "pure"` only when every parameter and result is a Pure Value.

These declarations must appear prominently in trust reports because incorrect purity assertions can violate the guarantee.

### 12.11 Trust report

`purepy capabilities` and JSON output should report:

- each configured entrypoint;
- capability types and labels in its signature;
- host-reference types in its signature;
- direct trusted host calls in its body;
- direct trusted pure calls in its body; and
- manifest source locations or paths.

A later transitive reachability report may be useful but is not required for authorization correctness.

### 12.12 No source-level unsafe

Add conformance fixtures proving that `@unsafe`, suppression comments, excluded files within the source root, and unknown external calls are rejected.

### 12.13 Tests

- manifest parser golden tests;
- unknown-field rejection;
- duplicate and conflict tests;
- deterministic merge-order tests;
- external import linking;
- capability-category annotation tests;
- missing and wrong capability arguments;
- capability alias/store/return/compare tests;
- host-reference alias/store/return/compare tests;
- pure-caller-to-effectful-callee rejection;
- effectful forwarding through multiple project functions;
- trusted pure function tests;
- trust-report golden tests; and
- malformed manifest fuzzing.

### 12.14 Exit criteria

- Verified code can call narrow host operations.
- Every effectful call is authorized by an exact explicit capability argument.
- No source-level escape hatch exists.
- Host references require no ownership analysis.
- Trust dependencies are visible and deterministic.

### 12.15 Deferred

Async call execution, manifest locks, ownership, context managers, tasks, and framework profiles.


---

## 13. Phase 5 — Restricted async verification

### 13.1 Objective

Support nonblocking verified programs through top-level `async def` and direct `await` of exact known async calls, without introducing first-class coroutine values.

### 13.2 Relevant files

```text
internal/model/decl.go
internal/model/expr.go
internal/frontend/lower.go
internal/check/async.go
internal/check/function.go
internal/check/calls.go
internal/manifest/schema.go
internal/app/check.go
```

### 13.3 Async function declarations

Add a `FunctionKind` enum:

```text
Sync
Async
```

The function category remains derived from parameter categories:

```text
pure sync
pure async
effectful sync
effectful async
```

### 13.4 Direct await lowering

The lowerer must recognize:

```python
result = await target(arguments)
return await target(arguments)
await target(arguments)
```

and emit one `AwaitCallExpr`.

Any other `await` operand emits an unsupported-async diagnostic. In particular, the IR must not represent a standalone coroutine value.

### 13.5 Async call rules

Implement:

- sync caller → sync callee only;
- async caller → sync callee normally;
- async caller → async callee only through direct await;
- async call without await is an error;
- await of sync call is an error;
- awaited argument checking identical to ordinary call checking;
- awaited host operations still require exact capability and host-reference arguments; and
- eventual result must be a Pure Value.

### 13.6 Pure async functions

A pure async function must contain no capability or host-reference parameter and may await only pure async targets.

The verifier should expose its classification in JSON output.

### 13.7 Cancellation restrictions

Because `try`, exception inspection, task APIs, and context managers are already rejected, most cancellation-sensitive constructs are excluded automatically.

Add explicit negative fixtures for:

- `asyncio.current_task`;
- task creation;
- futures;
- shielding;
- timeout contexts;
- event-loop access;
- cancellation exception references; and
- custom awaitables.

These should fail as unknown imports/calls or unsupported syntax, not through a special cancellation dataflow engine.

### 13.8 Async external manifests

Manifest functions already carry `kind = "async"`. Validate that:

- async host operations return Pure Values eventually;
- pure async external functions have only Pure Value parameters;
- capability-bearing async operations require exact capability arguments; and
- sync/async declaration mismatches are errors.

### 13.9 Entrypoints

Configured async entrypoints are accepted. The verifier emits a host signature report but does not provide an event loop or runner in v0.1.

A small optional development runner may exist under `examples/` or `tools/`, but not in the core CLI.

### 13.10 Tests

- every permitted direct-await position;
- assignment of coroutine rejection;
- returning coroutine rejection;
- passing coroutine rejection;
- async call without await;
- await of sync function;
- sync caller to async callee;
- pure async composition;
- effectful async capability forwarding;
- async recursion;
- async manifest mismatch;
- host-reference forwarding across direct await;
- deterministic diagnostics; and
- runtime smoke tests in the reference host.

### 13.11 Exit criteria

- Async handlers can perform sequential nonblocking host operations.
- The semantic model contains no first-class coroutine state.
- No suspended-value ownership analysis exists or is needed.
- Pure async functions are identified soundly under the v0.1 rules.

### 13.12 Deferred

`async for`, async generators, task creation, concurrent fan-out, timeouts, context managers, and resource cleanup.

---

## 14. Phase 6 — Parallel analysis, caching, diagnostics, and CLI hardening

### 14.1 Objective

Make the complete verifier responsive and deterministic on repository-scale projects without adding fine-grained invalidation complexity prematurely.

### 14.2 Relevant files

```text
internal/workpool/pool.go
internal/summary/module.go
internal/summary/encode.go
internal/summary/decode.go
internal/cache/cache.go
internal/cache/key.go
internal/cache/schema.go
internal/app/check.go
internal/app/explain.go
internal/app/capabilities.go
internal/diag/render_text.go
internal/diag/render_json.go
cmd/purepy/main.go
```

### 14.3 Parallel work decomposition

Use bounded parallelism for:

1. file reads and source hashing;
2. parsing;
3. lowering;
4. local module-shape checks; and
5. local function checks after linking.

Keep these operations deterministic and side-effect-free apart from result collection.

Global operations may initially remain single-threaded:

- module/import linking;
- manifest merge;
- stable symbol assignment; and
- final diagnostic sort.

These phases should be measured before parallelization.

### 14.4 Work-pool contract

`internal/workpool/pool.go` should provide a bounded generic worker facility with:

- context cancellation;
- panic containment and conversion to internal diagnostics;
- no unbounded queues;
- deterministic result indexing supplied by caller; and
- race-detector coverage.

### 14.5 Cache key

A module cache key should include:

```text
verifier version
summary schema version
PurePy language version
Python syntax version
relevant configuration hash
manifest-set hash
canonical module name
source content hash
```

Including the full manifest-set hash is deliberately conservative.

### 14.6 Cache artifact

Cache a parser-independent module summary. Never serialize tree-sitter pointers or process-specific IDs.

The artifact should be self-validating with:

- magic/version;
- checksum;
- canonical module name;
- source hash; and
- schema hash.

### 14.7 Initial invalidation

The initial policy is simple:

- unchanged source and unchanged global inputs reuse a module summary;
- changed source reparses and relowers that module;
- any changed declaration or manifest causes deterministic project relinking;
- local function checks may be reused only when their normalized body and referenced signatures are unchanged, if this can be implemented simply;
- otherwise recheck all functions after linking.

Full relinking and cheap rechecking are acceptable. Do not implement a dependency invalidation engine until benchmark data requires it.

### 14.8 Diagnostic rendering

Text output should optimize for human actionability. JSON output is the compatibility surface for editors and agents.

A JSON diagnostic should contain at least:

```text
code
severity
message
path
start/end byte offsets
start/end line and column
primary symbol
notes[]
related_locations[]
```

### 14.9 `explain`

Implement `purepy explain FILE:LINE[:COLUMN]` using retained semantic facts.

It should explain:

- resolved declaration;
- exact type;
- value category;
- function classification;
- call target;
- capability parameter requirement;
- host-reference restriction; or
- unsupported syntax rule.

### 14.10 `capabilities`

Implement stable text and JSON reports for:

- all configured entrypoints; or
- one qualified function.

The report should distinguish:

```text
capabilities accepted by signature
host references accepted by signature
direct trusted host calls
direct trusted pure calls
unused capability parameters
```

### 14.11 Performance harness

Add:

```text
tools/gen_synthetic_repo/
tools/bench_verifier/
benchmarks/baseline.json
```

Measure:

- process startup;
- discovery;
- parse throughput;
- lowering throughput;
- link time;
- function-check throughput;
- peak memory;
- warm-cache time;
- cache size; and
- CPU utilization by worker count.

### 14.12 Performance gates

Before release, establish measured budgets on named hardware. Initial qualitative gates are:

- near-linear parse/check scaling until parser or memory bandwidth saturation;
- no superlinear growth on synthetic module graphs;
- warm unchanged runs dominated by startup, cache reads, and linking rather than parsing;
- bounded peak memory per source byte;
- identical results for every worker count; and
- no race-detector findings.

Do not claim Ruff-equivalent performance until measured on comparable corpora.

The PurePy 0.1 local Apple M4 acceptance profile is versioned in
`benchmarks/acceptance-m4.json` and enforced by `tools/performance_acceptance.py`.
The [2026-09-05 completion record](validation/2026-09-05/completion.md) reports the
full verifier matrix, sustained reference-service workload, absolute acceptance,
and controlled base/head regression results. These measurements complete the
named local profile; dedicated CI hardware and other deployment workloads need
their own measured budgets.

### 14.13 Tests

- cache hit/miss tests;
- corrupt cache fallback;
- schema/version invalidation;
- manifest hash invalidation;
- cold/warm JSON equivalence;
- `--no-cache` equivalence;
- randomized worker completion ordering;
- worker-count equivalence;
- deterministic diagnostics over repeated runs;
- benchmark regression checks with tolerant thresholds;
- large synthetic repository; and
- memory profile snapshots.

### 14.14 Exit criteria

- Complete v0.1 analysis runs concurrently where useful.
- Cached and uncached results are identical.
- CLI commands and JSON schema are documented.
- Performance bottlenecks are measured rather than guessed.

### 14.15 Deferred

Daemon mode, incremental syntax edits, semantic-interface invalidation, editor protocol, graph rendering, and distributed analysis.

---

## 15. Phase 7 — Reference service and PurePy 0.1 release

### 15.1 Objective

Prove that the deliberately small language can support a useful modern nonblocking service without turning PurePy into a framework.

### 15.2 Relevant files

```text
examples/reference_service/host/main.py
examples/reference_service/host/network.py
examples/reference_service/host/database.py
examples/reference_service/src/types.py
examples/reference_service/src/http_parse.py
examples/reference_service/src/router.py
examples/reference_service/src/domain.py
examples/reference_service/src/render.py
examples/reference_service/src/handlers.py
examples/reference_service/manifests/host.purepy.toml
examples/reference_service/loadtest/load.py
examples/reference_service/purepy.toml
```

### 15.3 Two-layer design

#### Excluded host layer

The host may use ordinary Python, `asyncio`, sockets, an existing database driver, and mutable resources.

It is responsible for:

- listener creation;
- connection acceptance;
- task scheduling;
- request invocation concurrency;
- capability and host-reference creation;
- connection lifetime;
- database pool lifetime;
- cancellation;
- cleanup; and
- mapping expected host errors into Pure Value results.

#### Verified application layer

The verified `src/` directory contains only PurePy 0.1 code.

It is responsible for:

- immutable data records;
- HTTP-like parsing;
- route selection;
- validation;
- domain decisions;
- response rendering;
- explicit network operations;
- explicit database operations; and
- SSE-style loops.

### 15.4 Required host declarations

The reference manifest should expose a narrow surface such as:

```text
NetworkRead capability
NetworkWrite capability
DatabaseRead capability
DatabaseWrite capability
ClockWait capability
Connection host_ref

receive_bytes(NetworkRead, Connection) -> async bytes
send_bytes(NetworkWrite, Connection, bytes) -> async bool
load_user(DatabaseRead, int) -> async UserResult
execute_update(DatabaseWrite, UpdatePlan) -> async UpdateResult
load_event_page(DatabaseRead, int, int) -> async EventPage
sleep(ClockWait, float) -> async None
encode_utf8(str) -> bytes            trusted pure, if not intrinsic
```

No socket, cursor, transaction, task, or pool object enters verified code.

### 15.5 Required endpoints or scenarios

The workload should implement at least:

1. **Health:** pure fixed response.
2. **User lookup:** parse identifier, perform database read, pure render, send response.
3. **Atomic update:** validate immutable command, perform one atomic database write, render result.
4. **SSE:** poll or await event pages, pure event encoding, repeated send, explicit wait.
5. **Bad request:** explicit parse result, no exception handling.

### 15.6 Static routing

The router should be a pure function returning an immutable route code or route record. It must not use decorators, registries, callbacks, or dynamic dispatch.

### 15.7 Concurrency demonstration

The host invokes many handler instances concurrently. The verified handler itself performs sequential direct awaits.

Measure and document that concurrency is not prevented by the language restriction.

### 15.8 Performance demonstration

The load test should report:

- concurrent connection count;
- request throughput;
- latency distribution;
- SSE connection count and event cadence;
- database mock or real-driver configuration;
- CPU and memory usage; and
- verifier cold/warm times.

The goal is to validate viability, not to establish universal framework benchmarks.

### 15.9 Anti-framework constraints

The example MUST NOT:

- publish a reusable router package;
- define a general request middleware system;
- provide dependency injection;
- provide an ORM;
- provide a task abstraction;
- become a runtime dependency of `purepy`; or
- add application concepts to verifier packages.

### 15.10 Conformance completion

Before release:

- every normative rule has fixtures;
- every diagnostic family is documented;
- manifest schema is frozen as v1;
- JSON output schema is versioned;
- support package behavior is tested on selected Python runtimes;
- reference service passes `purepy check`; and
- release artifacts are reproducible.

### 15.11 Security and trust review

Review:

- path handling;
- symlink handling;
- manifest path traversal;
- malformed parser trees;
- cache corruption;
- untrusted source memory exhaustion;
- manifest trust-report completeness;
- support-package mutability; and
- accidental execution of analyzed code.

### 15.12 Release artifacts

Produce:

- Go binaries for supported platforms;
- source archive;
- checksums;
- Python support package;
- manifest schema documentation;
- conformance corpus;
- reference-service report; and
- release notes listing every deferred language feature.

### 15.13 Exit criteria

PurePy 0.1 is complete when:

- the conformance suite passes;
- the reference service is verified and runnable;
- host-managed concurrency and SSE are demonstrated;
- capability reports accurately expose authority;
- cold/warm verifier measurements are published;
- unknown behavior fails closed; and
- no v0.1 package implements a framework or general ownership system.


---

## 16. Testing strategy

### 16.1 Conformance fixtures

Use one directory per normative rule family:

```text
fixtures/conformance/
    syntax/
    modules/
    imports/
    annotations/
    values/
    records/
    statements/
    expressions/
    intrinsics/
    calls/
    capabilities/
    host_refs/
    async/
    manifests/
    entrypoints/
```

Every accepted feature requires:

- at least one minimal positive fixture;
- at least one realistic positive fixture;
- one negative near miss; and
- one composition fixture with another feature.

Every prohibited Python feature requires at least one negative fixture.

### 16.2 Golden diagnostics

Golden tests should compare versioned JSON diagnostics, not only text output.

Human-readable text snapshots are useful but should not become the only compatibility contract.

### 16.3 Unit tests

Unit-test:

- path normalization;
- stable IDs;
- source spans;
- annotation normalization;
- type equality;
- intrinsic signatures;
- branch joins;
- optional narrowing;
- manifest validation;
- capability argument matching;
- direct-await lowering;
- cache keys; and
- deterministic sorting.

### 16.4 Property tests

Property tests should establish:

- diagnostic sort is total and stable;
- stable IDs do not depend on insertion order;
- summary encoding round-trips;
- cache keys change when any semantic input changes;
- worker scheduling does not affect output;
- no capability or host-reference type is classified as Pure Value; and
- unsupported IR never reaches a path that assumes support.

### 16.5 Fuzzing

Fuzz:

- manifest parsing;
- annotation parsing;
- semantic lowering;
- cache decoding;
- malformed UTF-8 source handling as permitted by parser behavior;
- deeply nested syntax with resource limits; and
- diagnostic rendering.

Fuzz targets must enforce memory and recursion bounds.

### 16.6 Differential parser tests

Development CI may compare parsing acceptance and source ranges against a pinned CPython parser for the targeted Python syntax version.

This is a test oracle only. Production verification must not invoke Python.

### 16.7 Runtime support tests

Run the Python `@value` package tests against supported CPython versions.

Test:

- construction;
- frozen mutation failure;
- slot behavior;
- equality;
- annotation retention required for tooling;
- nested value records; and
- rejection or defensive failure for unsupported class bodies.

### 16.8 Reference-service runtime tests

Test:

- many concurrent host invocations;
- request parsing;
- database result conversion;
- atomic update behavior;
- SSE disconnect;
- host cancellation cleanup;
- no resource leakage in the host fixture; and
- identical pure-function results under concurrent calls.

These tests validate the host fixture and expressiveness. They do not expand PurePy semantics.

### 16.9 Real-repository corpus

Build a corpus of intentionally PurePy-style projects rather than measuring arbitrary framework compatibility.

The corpus should include:

- numerical functions;
- parsers;
- protocol codecs;
- business rules;
- static routers;
- async host adapters;
- command handlers; and
- data transformations.

Track rejection causes to guide later extensions.

---

## 17. Diagnostic design

### 17.1 Stable codes

Once a diagnostic appears in a tagged language release, its meaning should not be silently repurposed.

New detail may be added through notes while preserving the primary code.

### 17.2 Explanation-first implementation

Each checker function should return or retain enough structured facts for an explanation.

For a call failure, retain:

- resolved target or resolution candidates;
- expected signature;
- actual argument types and categories;
- capability labels; and
- source spans for target declaration and call.

### 17.3 Actionable capability errors

Prefer:

```text
This call requires `DatabaseRead`, but `load_page` has no parameter of that type.
```

Over:

```text
Effect violation.
```

### 17.4 Unsupported Python behavior

Unsupported-feature diagnostics should name the rejected Python mechanism and the accepted PurePy alternative where one exists.

Examples:

- method call → use a top-level function;
- mutable list → use a tuple, then later a local builder extension;
- coroutine variable → directly await the call;
- task creation → let the host schedule entrypoints;
- transaction object → use an atomic host operation;
- generator → use an explicit loop or page result.

### 17.5 Internal errors

Parser inconsistencies, impossible IR states, and corrupted summaries must produce an internal-error diagnostic with a reproducible identifier rather than panic the process.

---

## 18. Manifest and trust plan

### 18.1 Schema stewardship

The manifest schema is part of the trusted boundary and must receive the same review discipline as language rules.

A schema change requires:

- specification update;
- schema version bump when incompatible;
- positive and negative fixtures;
- trust-report update; and
- migration notes.

### 18.2 Standard manifests

Ship only a small reviewed standard set.

Prefer sealed intrinsics over a broad standard-library manifest when semantics are fundamental and exact.

### 18.3 Third-party manifests

Third-party manifests are data, not executable plugins.

The verifier should make their trust status and source path visible. It should not silently download manifests in v0.1.

### 18.4 Locking

A manifest lock file is useful but not release-critical for the first experimental build.

After the schema stabilizes, add a simple `purepy.lock` containing:

- canonical manifest path or package identity;
- content hash;
- schema version; and
- optional publisher metadata.

Do not implement dependency resolution or a package registry as part of PurePy.

### 18.5 Auditing pure native declarations

Trusted-pure external functions are the highest-risk manifest entries.

Provide a report mode that lists:

- function;
- manifest;
- signature;
- declaration source; and
- entrypoints that directly reference it.

A future transitive reachability report may be added without changing authorization semantics.

---

## 19. Performance plan

### 19.1 Measure by stage

Every benchmark should separately measure:

```text
startup
configuration
discovery
read/hash
parse
lower
link
check
render
cache read/write
```

Do not optimize aggregate wall time without identifying the dominant stage.

### 19.2 Expected hot paths

Likely hot paths are:

- parser traversal;
- IR allocation;
- name and type interning;
- expression checking;
- source-span storage;
- summary encoding; and
- diagnostic sorting in heavily invalid projects.

### 19.3 Allocation discipline

Use compact enums, interned strings or stable IDs, and arena-like per-module allocations where measurements justify them.

Avoid premature unsafe Go code.

### 19.4 Concurrency discipline

Parallelize coarse units:

- module parse/lower;
- function check.

Do not create goroutines per syntax node, expression, or diagnostic.

### 19.5 Memory discipline

After lowering, discard parser trees when no longer needed unless editor work is explicitly underway in a future release.

Cached summaries should omit redundant source text.

### 19.6 Benchmark corpora

Maintain:

1. tiny startup project;
2. medium realistic PurePy project;
3. large generated module corpus;
4. deep import chain;
5. wide import fan-out;
6. diagnostics-heavy invalid corpus; and
7. reference service.

### 19.7 Regression policy

Performance regressions beyond agreed tolerances require:

- benchmark output;
- explanation;
- correctness justification; and
- approval.

Correctness and fail-closed behavior take precedence over speed.

---

## 20. Risk register

### 20.1 The checker expands into a Python type checker

**Risk:** Requests for methods, overloads, protocols, frameworks, and rich inference enlarge scope.

**Mitigation:** First-order exact-call rule; closed type representation; syntax matrix; ADR requiring evidence before adding dispatch.

### 20.2 Capability passing becomes verbose

**Risk:** Application signatures accumulate many capability parameters.

**Mitigation:** Treat visibility as a feature. Explore narrow host-defined aggregate capability types only after real code demonstrates a need; do not add ambient context.

### 20.3 Host manifests lie

**Risk:** A trusted-pure declaration performs effects or a host operation violates its signature.

**Mitigation:** Prominent trust reports, small manifests, no automatic downloads, content locking later, and conformance tests for reference hosts.

### 20.4 Host boundary absorbs too much logic

**Risk:** Users move application behavior outside verification to avoid restrictions.

**Mitigation:** Reference workload, host-boundary guide, trust reports, and examples that keep parsing, routing, validation, business rules, and rendering verified.

### 20.5 Direct await is too restrictive

**Risk:** Real applications need in-request fan-out or reusable awaitables.

**Mitigation:** Collect examples. Add bounded `parallel_join` before general tasks. Do not introduce first-class coroutine values prematurely.

### 20.6 Immutable-only code is too slow

**Risk:** Tuple concatenation makes parsers and encoders inefficient.

**Mitigation:** Prioritize the narrow local-builder extension immediately after 0.1. Measure reference-service hotspots.

### 20.7 Tree-sitter grammar diverges from Python 3.14

**Risk:** Parser acceptance or ranges differ from CPython.

**Mitigation:** Isolated adapter, syntax corpus, development differential tests, and rejection of ambiguous parse situations.

### 20.8 Cache reuse is unsound

**Risk:** A summary survives a semantic input change.

**Mitigation:** Conservative cache key including verifier, language, syntax, configuration, manifest set, module name, and source hash; cold/warm equivalence tests.

### 20.9 The reference service becomes a framework

**Risk:** Convenience abstractions migrate into core packages.

**Mitigation:** Anti-framework release gate and separate excluded host/application directories.

### 20.10 Rejection rate discourages adoption

**Risk:** Strict language is impractical.

**Mitigation:** Publish rejection explanations and an extension-evidence process. Preserve soundness rather than accepting unknown semantics.

### 20.11 Python runtime behavior leaks through

**Risk:** An accepted construct invokes hidden dynamic behavior.

**Mitigation:** Exact primitive rules, no methods, no user operators, sealed records, no descriptors, and runtime parity tests.

### 20.12 Capability and host-reference values are accidentally treated as data

**Risk:** A checker path allows storage, comparison, or return.

**Mitigation:** Category-based central boundary checks and property tests proving these categories never satisfy Pure Value predicates.

---

## 21. Architecture decisions that remain explicit

The following decisions must not become accidental implementation details:

1. PurePy adopts Python syntax, not the unrestricted Python data model.
2. Calls are first-order and statically resolved.
3. Functions are monomorphic.
4. Effects are authorized by capability parameters.
5. Capabilities cannot be acquired from ambient state.
6. Host references are host-owned and direct-forward-only.
7. There is no source-level `@unsafe`.
8. Verified source root has no per-file exclusions.
9. Async is direct-await-only.
10. The host owns scheduling and resource cleanup.
11. Expected failures use Pure Values.
12. Exceptions cannot be caught in v0.1.
13. Generators and task APIs are not v0.1 milestones.
14. Manifests are declarative and minimal.
15. Cache invalidation is conservative before it is clever.
16. The reference service is not a framework.
17. Record equality requires recursively sealed comparable fields; an opaque
    manifest value never gains equality through a record, tuple, or optional.
18. The sealed intrinsic contract has its own version (`1` for PurePy 0.1),
    reported by the CLI and included in cache identity.
19. Documented implementation resource limits reject explicitly; exceeding a
    cache decoding limit triggers fresh source analysis. The exact frontend and
    cache bounds are specified in `PUREPY_SPEC.md` section 30.11.

Any proposal to change one of these requires an ADR and conformance impact analysis.

---

## 22. Post-0.1 extension sequence

Extensions should be driven by measured friction in conforming projects.

### 22.1 Extension A — Narrow local builders

#### Goal

Make pure parsers and encoders efficient without general mutation or ownership.

#### First accepted pattern

A `bytearray` builder:

- is allocated locally;
- has exactly one local name;
- is never copied or returned;
- is passed only to sealed mutating intrinsics;
- cannot appear in a branch join unless all paths retain the same unique builder;
- is converted exactly once to `bytes`; and
- is dead after conversion.

Then apply the same pattern to `list[T]` converted to `tuple[T, ...]`.

#### Relevant future files

```text
internal/model/types.go
internal/check/builders.go
internal/check/flow.go
internal/intrinsic/builders.go
```

#### Non-goal

No general borrow checker, mutable function arguments, shared buffers, or returned builders.

### 22.2 Extension B — Bounded `parallel_join`

#### Goal

Permit common in-request concurrent fan-out without task handles.

#### Shape

A sealed intrinsic accepts a fixed number of direct async call expressions and returns results in declaration order.

The checker validates:

- every operand is a direct async call;
- every child is joined;
- no task object is exposed;
- capability arguments are present;
- a host reference marked nonshareable is not duplicated across children; and
- failure/cancellation semantics are fixed by the intrinsic specification.

#### Non-goal

No arbitrary spawn, detach, task storage, futures, supervisors, or user schedulers.

### 22.3 Extension C — Lexical resource form

#### Goal

Support carefully scoped resources when one-shot host operations are insufficient.

#### Initial restrictions

- one approved resource constructor;
- one lexical scope;
- no assignment of the resource;
- no return;
- no storage;
- no ownership transfer;
- no borrowing;
- no task sharing;
- deterministic close on normal and host-mediated cancellation paths; and
- no user exception suppression.

Only after this proves insufficient should ownership qualifiers be considered.

### 22.4 Extension D — Checked in-process memoization

#### Goal

Validate that a recognized wrapper is applied only to a verified pure function with Pure Value parameters and result.

#### Initial non-goals

- persistent cache keys;
- distributed caches;
- canonical serialization;
- deployment-compatible semantic hashes; and
- async single-flight.

### 22.5 Extension E — Conservative semantic hashes

Add source and transitive verified-dependency hashes only after a concrete cache or build consumer exists.

### 22.6 Extension F — Streams

Consider producer-only generators or async generators only if explicit loops and page operations create demonstrated architectural problems.

The proposal must include closing, abandonment, cancellation, and nonescape semantics before implementation.

### 22.7 Extension G — Richer concurrency and ownership

General structured concurrency, task handles, resource transfer, borrowing, and shared mutable synchronization are not presumed features. They require strong evidence that bounded combinators and lexical resources are inadequate.

---

## 23. Suggested issue breakdown

### Epic A — Foundation

1. Initialize Go module and CLI skeleton.
2. Add version metadata and deterministic build information.
3. Implement configuration schema.
4. Add diagnostic registry.
5. Add fixture harness.
6. Write eight foundational ADRs.
7. Complete syntax matrix.

### Epic B — Frontend

8. Implement deterministic source discovery.
9. Implement module-name mapping.
10. Integrate tree-sitter parser adapter.
11. Implement source-span conversion.
12. Add bounded parse worker pool.
13. Add parser and discovery corpus.

### Epic C — Semantic model

14. Define parser-independent IR.
15. Lower module declarations.
16. Lower statements and expressions.
17. Preserve unsupported forms.
18. Build stable declaration IDs.
19. Implement direct import linking.
20. Implement cycle detection.
21. Normalize annotations.
22. Normalize function signatures.

### Epic D — Pure synchronous core

23. Implement Pure Value type model.
24. Validate `@value` records.
25. Implement Python `@value` runtime package.
26. Validate constants.
27. Implement primitive literal and tuple typing.
28. Implement exact operator tables.
29. Implement record construction and field reads.
30. Implement local variable and definite-assignment flow.
31. Implement control-flow checks.
32. Implement direct project calls.
33. Implement intrinsic table.
34. Add pure-function reports.

### Epic E — Host boundary

35. Define manifest schema v1.
36. Implement manifest parser and validator.
37. Link external types and functions.
38. Implement capability categories.
39. Implement host-reference categories.
40. Enforce direct-forwarding rules.
41. Implement trusted-pure declarations.
42. Implement capability/trust reports.
43. Add host-boundary documentation.

### Epic F — Async

44. Add sync/async function kind.
45. Lower direct await as `AwaitCallExpr`.
46. Enforce async call matrix.
47. Reject first-class coroutine uses.
48. Support async manifest declarations.
49. Validate async entrypoints.
50. Add async conformance corpus.

### Epic G — Scale and UX

51. Define module summary schema.
52. Implement summary encoding and decoding.
53. Implement conservative cache keys.
54. Add bounded parallel function checking.
55. Implement deterministic JSON diagnostics.
56. Implement `purepy explain`.
57. Implement `purepy capabilities`.
58. Add benchmark generator and harness.
59. Add cold/warm equivalence tests.

### Epic H — Reference service and release

60. Implement minimal excluded async host.
61. Define host manifest.
62. Implement immutable application records.
63. Implement pure request parser.
64. Implement static router.
65. Implement database-read handler.
66. Implement atomic-write handler.
67. Implement SSE loop.
68. Add load tests.
69. Complete security review.
70. Freeze JSON and manifest schemas.
71. Package Go binaries and Python support package.
72. Publish PurePy 0.1 conformance report.

---

## 24. Immediate coding-agent sequence

A coding agent should implement the repository in this order. Each item should land as a reviewable change with tests.

1. Create Go module, `cmd/purepy/main.go`, version command, and CI.
2. Add `internal/diag` with stable diagnostic codes and JSON model.
3. Add strict `purepy.toml` loading in `internal/config`.
4. Add fixture metadata and test runner.
5. Add deterministic source discovery and module-name mapping.
6. Add parser interface and tree-sitter implementation.
7. Add stable source spans and syntax-error diagnostics.
8. Add bounded parse worker pool and worker-count equivalence tests.
9. Define the minimal semantic IR in `internal/model`.
10. Lower module items, statements, expressions, and unsupported forms.
11. Build stable declaration index and direct import resolver.
12. Implement cycle detection and module-shape diagnostics.
13. Implement the closed annotation parser.
14. Normalize function and value-record declarations.
15. Implement recursive Pure Value classification.
16. Implement the Python `@value` support package and tests.
17. Implement constants, expression typing, and intrinsic tables.
18. Implement local flow, direct calls, and synchronous purity checks.
19. Define and implement manifest schema v1.
20. Link capability, host-reference, value, and function declarations.
21. Enforce capability/host-reference direct forwarding and trust reports.
22. Add async function kinds and `AwaitCallExpr`.
23. Enforce direct-await-only semantics.
24. Implement entrypoint summaries and `purepy capabilities`.
25. Add module summaries and conservative content cache.
26. Parallelize independent function checking.
27. Implement `purepy explain` and finalize deterministic CLI output.
28. Build the reference service host and manifest.
29. Implement and verify service application modules.
30. Run load, conformance, race, fuzz, and security gates.
31. Freeze schemas and publish PurePy 0.1.

Do not begin local builders, `parallel_join`, resources, memoization, generators, or daemon work before Item 31 unless a blocking defect in the v0.1 language is demonstrated.

---

## 25. Definition of done

PurePy 0.1 is done when all of the following are true.

Implementation readiness and artifact publication are separate gates. The
completion record in `IMPLEMENTATION.md` links the current validation evidence;
`CONFORMANCE.md` tracks individual specification obligations. A `partial` row
can record a finite-testing or trusted-host limitation even when all enumerated
implementation cases are covered. Traceability counts do not replace regression
execution, soundness review, or the publication requirements below.

### Language

- The accepted syntax is completely enumerated.
- All other Python constructs are rejected with source-located diagnostics.
- Calls are first-order and monomorphic.
- Every type is represented by the closed v0.1 type model.
- Pure functions accept and return only Pure Values.
- Capability and host-reference values cannot enter Pure Values.
- Async is direct-await-only.

### Soundness boundary

- No analyzed project code executes.
- No source-level unsafe escape exists.
- Every external declaration is manifest-backed.
- Every effectful host call receives exact explicit capabilities.
- Every trusted-pure external dependency appears in a trust report.
- Unknown behavior fails closed.

### Implementation

- Parser-specific data does not escape the frontend.
- Parallel runs are deterministic.
- Cached and uncached results are equivalent.
- Race, fuzz, unit, golden, and conformance tests pass.
- Performance results are published on named hardware.

### Practicality

- The reference service is fully verified above its narrow host.
- The host invokes requests concurrently.
- Database reads and atomic writes work.
- SSE-style streaming works through an explicit async loop.
- Parsing, routing, domain logic, and rendering remain in verified code.
- The example does not become a framework or core dependency.

### Documentation and release

- Specification and plan match the implementation.
- Manifest and JSON schemas are versioned.
- Diagnostics are documented.
- Host-boundary guidance is complete.
- Binaries, support package, checksums, conformance corpus, and release notes are published reproducibly.

---

## 26. Final implementation principle

When deciding whether to add machinery, apply this test:

> Can the required program instead be expressed with immutable data, a direct top-level call, an explicit capability parameter, a direct await, or a narrow host operation?

If yes, do not generalize the verifier.

PurePy succeeds by making the safe program model small enough to understand completely:

```text
first-order functions
+ exact immutable values
+ explicit authority
+ direct async composition
+ a narrow trusted host
```

That model is sufficient to build the initial verifier, validate modern service viability, and establish a sound base for carefully justified extensions.
