# ADR 0005: Host-owned resources

Status: accepted for PurePy 0.1.

The excluded host creates, owns and closes connections, pools, transactions,
streams and scheduling resources. Verified code may receive opaque host-reference
parameters and forward them directly to known host operations. A separate
capability authorizes effects. No verified code owns cleanup or transfers a
reference through an ordinary result or record.

This rejects a borrow checker, general ownership qualifiers, context managers
and transaction objects inside the initial subset. Their cleanup and cancellation
semantics would dominate a verifier intended to check business logic. Atomic host
operations and invocation-scoped opaque references provide the initial boundary.

Reopen only when reference applications demonstrate that one-shot host operations
or invocation-scoped references cannot express a necessary resource protocol.
The smallest proposed lexical resource form must define cleanup on normal exit,
primitive failure and host cancellation, and prove nonescape before transfer or
borrowing is considered.
