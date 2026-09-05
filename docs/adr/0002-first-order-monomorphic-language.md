# ADR 0002: First-order monomorphic language

Status: accepted for PurePy 0.1.

Every function has one fixed, fully annotated signature, and every call resolves
to a top-level project function, declared external function, sealed intrinsic or
verified record constructor. Functions are not ordinary values. Types use exact
nominal equality plus the explicit optional form.

This rejects callback flow analysis, function-valued fields, method dispatch,
overloads, protocols and user generics. They expand resolution and authorization
into general Python type checking or whole-program inference. Explicit free
functions and records provide a smaller executable model with local call checks.

Reopen only after a representative verified workload demonstrates material
friction that direct functions cannot reasonably address. An extension must
specify target resolution, concrete signature selection, capability preservation,
cache effects and a bounded analysis algorithm, with adversarial tests and
repository-scale performance evidence.
