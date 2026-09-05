# Verifier performance measurements

The schema-2 [benchmark harness](../tools/benchmark.py) measures fresh verifier
processes on generated or copied temporary projects. It records repeated size and
worker matrices, separate pipeline and worker timings, per-process CPU and peak
resident memory, and original/edited input identities. It never imports or executes
analyzed Python or host implementations. Source edits and cache deletion are
confined to its temporary directory.

## Run and reproduce

```sh
make setup build
make benchmark-test
make benchmark BENCHMARK_ARGS='--corpora wide,deep,invalid,manifest --sizes 100,1000,3000 --workers 1,4 --repeat 3 --hardware "Apple M4" --output /tmp/purepy-benchmark.json'
make benchmark-compare BENCHMARK_CANDIDATE=/tmp/purepy-benchmark.json
```

The last command requires the same hardware metadata, toolchain/build settings,
Go runtime environment, input matrix, repetitions, and measurement method as the
checked-in baseline. For a
change on a different machine, measure both binaries there with identical harness
arguments and use `BENCHMARK_BASELINE=/tmp/base.json` and
`BENCHMARK_CANDIDATE=/tmp/head.json`. Baseline replacement is an explicit reviewed
change; running the comparison does not update it.

`--sizes`, `--workers`, and `--corpora` take comma-separated lists. The legacy
`--modules`, `--jobs`, and `--corpus` options select single values. `--functions`
sets functions per generated leaf, `--repeat` sets repetitions, and `--timeout`
bounds each child invocation. Defaults remain 100 leaves, 10 functions per leaf,
three repetitions, and up to four workers. `tiny` and `reference` have fixed sizes
and are not duplicated for each requested size. `--hardware` adds a label but
does not replace automatically collected machine metadata. If CPU-model detection
is unavailable, an explicit machine label is required. Use the actual machine's
label when adapting the command above. The committed baseline retains the sandbox's
generic `arm` processor field with a separately verified `Apple M4` label; collection
outside that sandbox can report different metadata and requires a fresh base/head
pair. Reports record `GOGC`, `GOMEMLIMIT`, `GOMAXPROCS`, and `GODEBUG`, including
whether each is unset, and comparisons require them to match.

Corpora exercise distinct costs:

| Corpus | Workload |
| --- | --- |
| `tiny` | One generated leaf, package initializer, and entrypoint. |
| `deep` | A linear import/call chain with additional small functions per leaf. |
| `wide` | An entry module that imports and calls every leaf. |
| `invalid` | Unknown calls and incompatible arithmetic in every generated function, including structured diagnostic types and parameter declaration paths. |
| `manifest` | Import-safe external modules with one declarative external signature per generated function. Source callers exercise linking and signature checking. |
| `reference` | Configuration, source, and manifests copied from the reference service; host implementations are absent. |

Every repetition measures uncached verification, empty-cache population, an
unchanged warm check, and one source declaration edit. The manifest corpus also
measures a manifest return-type edit. Original reports must be byte-identical
across repetitions, cache modes, and workers. Edited reports must change their
rejection diagnostics and match fresh uncached checking across workers. Initial
population must have zero hits, a warm run all hits, and a source edit all but one
hit. A manifest edit invalidates every parse summary because manifest bytes enter
the image key. A failed correctness check aborts the harness instead of producing
a successful performance result.

## Timing and memory contract

`purepy check --timings` emits `timings_schema 2`, seconds with nine decimal places,
and `cache_hits` to stderr after output completes. Verification JSON remains
schema 1 and contains no timing values. All timing categories appear, including
zeros when a stage is skipped or parsing is avoided by a cache hit.

| Wall interval | Included work |
| --- | --- |
| `wall_configuration` | Configuration loading and path validation. |
| `wall_discovery` | Source enumeration and module discovery. |
| `wall_manifests` | Manifest loading, validation, and source-location projection. |
| `wall_image_inputs` | Serial configuration/manifest input reads, serialization, and hashing for cache identity. |
| `wall_frontend` | Elapsed parallel read/hash/cache/parse/lower phase, including worker scheduling. |
| `wall_link` | Project linking, signatures, constants, cycles, and entrypoint diagnostics. |
| `wall_check` | Function checking and collecting worker results. |
| `wall_report` | Report assembly, merging, and sorting, including early error reports. |
| `wall_render` | Post-check report selection, encoding/text formatting, and output-writer time. |

These wall intervals do not overlap. `work_read`, `work_hash`, `work_cache_read`,
`work_parse`, `work_lower`, and `work_cache_write` instead sum elapsed durations
inside frontend workers. They are neither CPU time nor additional wall stages;
adding them to the wall intervals would double-count parallel work. Parser timing
includes source validation, native parser setup, syntax/trivia checks, and native
resource cleanup. Lowering covers detached IR construction, name normalization,
and diagnostic ordering. Ordinary verification and parsing avoid timing clock
reads unless instrumentation is enabled.

The harness stores these as `stage_ms` and `worker_ms`. Its unassigned wall interval
is total child elapsed time minus only the wall stages. It includes process
startup, the timing footer, process exit, and observation overhead; it is not a
separate parsing or rendering measurement. Child completion is observed with a
1 ms polling interval, so very small timing differences are not meaningful.
Version-only invocations provide separate startup samples.

CPU and peak RSS come from `wait4` for the exact child PID. Output goes to temporary
files to avoid pipe-buffer deadlocks; timeouts kill and reap that child. Each
sample therefore has its own resident-memory peak, unaffected by the largest
previous child. RSS is a peak, not cumulative allocation, and covers native parser
memory as well as Go. Parent and sibling memory are excluded. Tests check both a
large preceding child and a large live parent allocation on the current platform.

An empty PurePy cache does not mean flushed filesystem caches or a cold CPU. Warm
runs still decode and validate cached syntax, relink declarations, and recheck all
functions. Worker counts bound verifier concurrency; Go runtime and garbage
collection may use additional CPUs.

## Recorded baseline and measured changes

The checked-in [measurement artifacts](../benchmarks/README.md) retain raw samples,
medians, complete input and binary hashes, hardware, and Go build settings.
The current baseline table is generated from those artifacts below.

Measured on the hardware documented in `benchmarks/README.md`: three repetitions
per phase, 10 functions per leaf, and both one and four workers. The main matrix
contains 306 recorded process samples across 24 corpus/size/worker combinations;
additional uncached controls verify edited reports. Each size includes two
extra source files for the package initializer and entry module.

The following medians use four workers. Times are milliseconds; RSS is MiB.

| Corpus | Leaves | Uncached | Populate cache | Warm | Source edit | Warm peak RSS |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| wide | 100 | 24.79 | 32.31 | 21.46 | 20.97 | 28.61 |
| wide | 1,000 | 186.51 | 260.34 | 155.12 | 149.00 | 167.45 |
| wide | 3,000 | 565.03 | 795.61 | 471.11 | 460.38 | 492.39 |
| deep | 100 | 23.64 | 32.44 | 20.78 | 20.80 | 27.80 |
| deep | 1,000 | 181.09 | 257.47 | 149.02 | 150.48 | 160.02 |
| deep | 3,000 | 544.67 | 773.80 | 467.68 | 454.56 | 497.30 |
| invalid | 100 | 44.90 | 57.53 | 41.55 | 41.31 | 55.23 |
| invalid | 1,000 | 387.63 | 509.20 | 365.49 | 344.28 | 435.12 |
| invalid | 3,000 | 1,195.48 | 1,559.06 | 1,136.82 | 1,109.67 | 1,199.91 |
| manifest | 100 | 35.36 | 44.38 | 32.41 | 32.14 | 37.27 |
| manifest | 1,000 | 303.75 | 381.36 | 261.85 | 262.73 | 254.08 |
| manifest | 3,000 | 928.30 | 1,156.76 | 806.40 | 800.92 | 718.77 |

At 3,000 leaves, increasing from one to four workers reduced warm wall time
from 1,190 to 471 ms for wide, 1,156 to 468 ms for deep, 2,640 to 1,137 ms
for invalid, and 1,681 to 806 ms for manifest. From 1,000 to 3,000 leaves,
four-worker warm times grew by 3.04–3.14 times for roughly triple the input.
This is finite evidence of the measured scaling curve.

All warm samples recorded zero parsing and lowering. Cached JSON decoding
and validation remain substantial: the 3,000-leaf wide frontend took a median
284 ms of its 471 ms total. The invalid corpus emits 60,000 diagnostics at that
size; rendering took 341 ms and its warm peak reached about 1.17 GiB. This is
a remaining memory cost, not a completed memory target. A manifest declaration
edit at 3,000 leaves took a median 1,201 ms and invalidated all summaries.

The separate small baseline records four-worker warm medians of 4.48 ms for
tiny and 7.06 ms for the copied reference service. Startup and observation
overhead dominate these small values.

CPU and allocation profiles identified temporary token slices in cache
validation and repeated growth/copying of aggregate function results.
`strings.FieldsSeq` removes the token slices, and exact aggregate capacities
remove repeated result copying. The repeated 1,000-module in-process warm
benchmark measured these medians:

| Metric | Before | After | Reduction |
| --- | ---: | ---: | ---: |
| Allocated bytes/check | 200,002,475 | 163,308,493 | 18.35% |
| Allocations/check | 1,522,798 | 1,301,768 | 14.51% |
| Wall time/check | 119.42 ms | 111.01 ms | 7.05% |

The separate fresh-process before/after pair passed all 12 scalar regression
checks. Warm peak RSS fell from 183.56 to 168.77 MiB, while warm wall time
was 159.69 versus 163.96 ms. Those process timings do not show a consistent
latency improvement; the strongest measured result is reduced allocation.

## Regression checks and CI

[The comparator](../tools/benchmark_compare.py) requires complete matching matrices,
original and edited input hashes, hardware, build and runtime metadata, successful equality
flags, consistent report hashes across workers, expected cache counts, and raw
samples agreeing with their reported medians. An incompatible or malformed report
cannot pass. `--allow-incompatible` only produces exploratory results and still
exits nonzero.

Defaults allow each wall-time median up to `baseline * 1.5 + 25 ms`, CPU up to
`baseline * 1.5 + 50 ms`, and peak RSS up to `baseline * 1.5 + 16 MiB`. Adjacent-size
growth is compared with the larger of linear total-input growth and the previous
measured curve, allowing 25% growth noise plus the metric's absolute floor. A growth
failure also requires the larger case to regress, so improving a small case alone
does not cause a failure. Total input includes manifests, which are substantial in
the manifest corpus. These are deliberately tolerant regression budgets, not
universal acceptable latency or memory limits.

CI measures base and head on the same runner using the current harness: deep,
wide, invalid, and manifest corpora at 20 and 80 leaves, workers 1 and 2, and three
repetitions. A separate job isolates this from the verifier test process. Both
reports and comparison results are retained as artifacts. The first commit with
this measurement contract records an explicit bootstrap notice when the base has
only schema 1; later compatible bases must pass the comparison. Unknown base
schemas fail instead of silently skipping. The committed Mac baseline is not used
as an absolute threshold on Linux CI.

## CPU and allocation profiles

```sh
go test ./internal/app -run '^$' -bench '^BenchmarkCheckWarm/modules_1000$' \
  -benchtime=20x -count=3 -benchmem \
  -cpuprofile=/tmp/purepy.cpu -memprofile=/tmp/purepy.heap -o /tmp/purepy.test
go tool pprof -top /tmp/purepy.test /tmp/purepy.cpu
go tool pprof -top -alloc_space /tmp/purepy.test /tmp/purepy.heap
```

The benchmark fixes four verifier workers and checks warm hits and function counts
on every iteration. It measures the in-process pipeline, excluding process startup,
JSON output, and initial cache creation from benchmark counters. Process-wide CPU
and heap profiles also include fixture setup, initial parsing, and cache population.
Use their hot paths to explain allocation costs; do not equate their total sampled
bytes with one warm run. The checked-in profile summaries retain that distinction.

Measurements span one machine and a finite set of generated workloads. They do not
establish all graph shapes, cross-platform scaling, independent soundness, or the
runtime service's sustained-load release budget. The separate
[service load sample](../examples/reference_service/loadtest/sample.json) concerns
application execution. The [recorded five-minute mixed workload](validation/2026-09-05/README.md)
adds continuous reads/writes and correlated SSE delivery under concurrent fuzzing
load, with bounded statistics and explicit SQLite memory accounting. Dedicated
acceptance budgets and broader deployment workloads remain release work.
