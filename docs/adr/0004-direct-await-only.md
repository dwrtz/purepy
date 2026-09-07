# ADR 0004: Direct await only

Status: accepted for PurePy 0.2.

An await directly contains one statically resolved async call and is lowered as
one semantic operation. Sync functions cannot await; sync calls cannot be awaited;
async calls cannot occur without direct await. Coroutines, futures and tasks are
not values that verified code may store, pass or return.

This rejects first-class awaitables, task creation, detached work and generic
coroutine ownership tracking. Those require lifetime, cancellation, abandonment
and resource rules that are unnecessary for sequential async service handlers.
The host schedules concurrent entrypoint invocations and delivers cancellation.

Reopen only with a measured service workload needing in-invocation concurrency
that narrow host operations cannot reasonably supply. Prefer a bounded join
proposal before general tasks. It must define completion, cancellation, failure,
argument authority, resource sharing and nonescape, with runtime stress tests.
