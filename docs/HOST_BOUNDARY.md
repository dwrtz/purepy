# The PurePy host boundary

The verifier reads source and declarative manifests; it never imports the analyzed
application. At execution time a trusted host imports verified functions, creates
capabilities and opaque references, and calls configured entrypoints. The host
must supply exact declared types. Verification does not police arbitrary Python
callers or prove that a manifest tells the truth.

Keep resource mechanics outside the single verified source root: socket acceptance,
event-loop creation, task scheduling, connection pools, transaction execution,
cancellation, and cleanup. Keep application decisions inside it: parsing, routing,
validation, domain rules, rendering, choosing operations, and sequential direct
awaits. [The reference service](../examples/reference_service/README.md) demonstrates
this split with actual sockets and SQLite.

Capabilities grant one declared kind of authority. A host reference identifies an
invocation's resource but grants no authority itself. Pass both explicitly to a
known operation. Verified code cannot inspect, alias, store, compare, construct,
or return either category. No implicit global pool or ambient request context is
needed. The reference database capabilities contain their selected host-owned
database; its network operations additionally require a connection reference.

Each external module must be declared import-safe, including its package import
chain. This is a host promise that importing it does not perform application I/O,
start tasks, acquire resources, or change process-wide state. Construction and
startup happen only when the host is explicitly invoked. A manifest is reviewed
data, never a plugin containing executable hooks.

Expose narrow, top-level operations with exact signatures and explicit authority.
Declare `kind = "async"` for suspendable operations and `trust = "host"` when a
capability is required. Return immutable application records for database rows and
results. A `trust = "pure"` operation must accept and return only Pure Values and
be independent of ambient state; treat each such declaration as a trust decision.
Opaque external value types additionally promise deep immutability in the manifest.

Expected failures should become explicit Pure Value results. Unexpected failures
may terminate the invocation. Verified code cannot catch exceptions or cancellation,
so the host owns supervision and cleanup. Never return sockets, cursors,
transactions, tasks, coroutine objects, or lazy I/O properties. For writes, accept
an immutable plan and perform one atomic operation. Document cancellation and
retry behavior: cancellation of a waiter does not necessarily roll back a database
operation already running in a worker or external service.

Concurrent service operation comes from the host scheduling independent entrypoint
invocations. Within each verified invocation, direct awaits remain sequential.
Long-lived SSE handlers can use explicit loops, immutable event pages, explicit
network sends, and capability-authorized waits. Resource references stay owned by
the host for the complete invocation and are closed by its finalization path.

The Python package provides only frozen, slotted `@value` records. It has no effect
interpreter, registry, dependency injection, task API, or application abstractions.
Its guards reject accidental methods, defaults, and inheritance. The verifier
checks field types and deep immutability; the host supplies exact values, including
trusted immutable external types. The runtime preserves Python 3.14 deferred
annotations without a manifest registry or eager forward-type resolution. It is
not a security sandbox against unverified Python using reflection or monkeypatching.
