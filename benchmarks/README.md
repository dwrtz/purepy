# Performance evidence

These files contain development measurements, not universal release targets.
`baseline.json` records repeated verifier processes on the hardware and input
matrix named in the report. Every sample must pass the harness's verdict,
cache-invalidation, and full-report equivalence checks before it is recorded.
`small-baseline.json` separately records the fixed tiny and reference-service
corpora at both worker counts.

The main matrix was measured on 2026-09-05 on an Apple M4 with 16 GiB RAM,
10 logical CPUs, macOS 26.5.2, Go 1.24.5, and CPython 3.14.7. CPU-model
autodetection was blocked by the collection sandbox, so the reports retain its
raw `arm` processor field. The `Apple M4` label was added after read-only
`sysctl -n machdep.cpu.brand_string` verification outside the sandbox. The four
recorded Go runtime environment variables were verified unset in the unchanged
collection environment and added to the artifacts. No timing or resource samples
were changed by these metadata annotations.

`optimization-before.json` and `optimization-after.json` measure the same input
and build settings before and after the allocation changes in this milestone.
`optimization-comparison.json` records the configured regression gate's result.
The earlier binary includes timing instrumentation so both use the same
measurement protocol. Its two allocation changes are absent.
The earlier timed binary was built from parent commit `456891b` plus this
milestone's instrumentation, before the `strings.FieldsSeq` and function-result
preallocation changes. The after binary is this milestone's production code;
both binary hashes and build flags are retained in the reports.

`profiles/` contains reproducible Go benchmark output and CPU/allocation profile
summaries. The warm benchmark excludes process startup, output encoding, and
initial cache creation from its benchmark counters. Go's process-wide profiles
also include fixture setup and cache population; interpret them accordingly.
The before Go benchmark uses parent commit `456891b` plus the new benchmark
fixture; the after benchmark uses this milestone's code. Each records three
20-iteration runs on the same machine. Only readable benchmark and profile
summaries are retained; executable and raw profile files are excluded.

For reproduction commands, timing definitions, budgets, and limitations, see
[the performance guide](../docs/PERFORMANCE.md). Baseline replacement is an explicit
reviewed change. CI compares base and head on the same runner rather than treating
these machine-specific values as portable thresholds.
