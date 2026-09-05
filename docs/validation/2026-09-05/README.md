# Robustness and service validation — 2026-09-05

The campaign exposed and fixed two TOML decoder behaviors that violated PurePy's
closed schemas: singleton tables coerced into manifest arrays, and case-folded
keys accepted as canonical fields. The latter affected both manifests and project
configuration, including aliases that could overwrite `language` or `source_root`.
Each of the eight fuzz targets has now completed a successful ten-minute run;
the new manifest target completed its full rerun after the fixes.

## Revisions and evidence

Work began at `e46acc19b3040a2f38770eb0e8153d5feb9a2b82`. Reports record the base
commit, dirty status, and actual Go-source/data fingerprints. The initial campaign
is intentionally retained as **failed**: it found a minimized input and its source
fingerprint changed during the resulting fixes. Its seven successful target
results precede those final key-validation changes. The manifest rerun was taken
after all Go edits, and its before/after source fingerprint is unchanged:

`53ca9b091f90df8299fdc95207817d2d8f3438cec6674715fc47bc8ca73a6aa3`

This identifies the Go inputs shipped with this report, including new tests,
embedded Unicode data, and the permanent failing seed. Standard integration and
race checks also passed on the final sources. The whole-function and service
reports retain their measured binary/harness hashes; their measurements preceded
the final key-case guards. The service/application/harness sources were unchanged
throughout the soak. Its corpus uses canonical keys, and the final service
verification and default differential gate passed again after the guards landed.

Machine: macOS 26.5.2, arm64, 10 logical CPUs, 16 GiB physical memory.
Toolchains: Go 1.24.5 and uv-managed CPython 3.14.7. These are local development
measurements, not dedicated-runner release budgets.

- [Initial fuzz campaign](fuzz-initial.json)
- [Successful manifest rerun](fuzz-manifest-retry.json)
- [64-seed whole-function campaign](whole-functions.json)
- [Service measurements](service.json) and [invocation/source identity](service-run.json)
- [Complete logs and original reports](logs.tar.gz), SHA-256 `705b53d0ceaa4d51ecf94067e69487cf4d2c97ddc8128ac9a7e2cdd88d945f6a`

The archive uses the directories `fuzz-initial/`, `fuzz-manifest-retry/`,
`whole-functions/`, and `service/`. Log paths resolve within each directory;
original commands retain their recorded workspace paths. Target log hashes
refer to uncompressed bytes.

## Fuzz campaigns

The initial batch used four concurrent target processes and one mutation worker
per target, with `GOMAXPROCS=2`, a 512 MiB soft Go memory target per process,
30-second minimization, 720-second Go timeouts, and 750-second outer timeouts.
The manifest rerun used one target/worker. Source bounds remain those documented
in [the fuzzing guide](../../FUZZING.md). Aggregate memory and C parser allocations
are not bounded by `GOMEMLIMIT`.

| Target | Mutation budget | Executions in successful run | Result |
| --- | ---: | ---: | --- |
| `FuzzCheckerSemantics` | 600 s | 329,606 | Pass |
| `FuzzCheckerSource` | 600 s | 1,598,624 | Pass |
| `FuzzCacheSummary` | 600 s | 155,504 | Pass |
| `FuzzCacheArtifact` | 600 s | 218,178 | Pass |
| `FuzzCacheFallback` | 600 s | 66,325 | Pass |
| `FuzzParseNeverPanics` | 600 s | 4,542,591 | Pass |
| `FuzzTypeSyntax` | 600 s | 14,646,233 | Pass |
| `FuzzManifestLoad` | 600 s | 1,122,330 | Pass |

Successful runs total **22,679,391 fuzzer executions**.
Counts include the engine's execution accounting and repeated inputs; they are
not unique-program counts or performance benchmarks. Flat intervals during
coverage minimization recovered within the configured windows.

Before mutation, the new seed controls exposed singleton `[module]`, `[type]`,
`[function]`, and parameter tables being accepted by the typed decoder despite
the published array schema. The loader now validates original container shapes,
retains explicit empty arrays, and reports the offending key.

The first extended manifest run stopped after 2,808 executions on this preserved
input:

```toml
sChemA=1
[[module]]
nAme='A.A'
import_sAfe=true
```

The decoder's case-insensitive field fallback also bypassed
`DisallowUnknownFields`. Both loaders now validate exact decoded key components;
quoted/escaped canonical keys remain valid. Focused tests cover case aliases,
canonical-plus-alias overwrites, all declaration/parameter positions, different
TOML layouts, and error locations. The fuzzer's oracle was retained. Replay the
permanent case with:

```sh
go test ./internal/manifest -run '^FuzzManifestLoad/3aaf28231761b9d9$'
```

## Whole-function expansion

Seeds 0 through 63, each with 256 supplemental samples, passed **19,008 function
trials and 144,610 invocation trials**. There were **240 distinct source texts**;
fixed cases and some generated compositions repeat across seeds. Every trial
retained independent static verdict and invocation expectations. Commands, counts,
logs and hashes are recorded per seed. Reproduce a selected seed with:

```sh
.venv/bin/python tools/differential_functions.py --seed 37 --samples 256
```

## Five-minute mixed service workload

The service dispatched mixed traffic for 300 seconds with 20 request workers,
four continuous SSE readers, one increment per five requests, and 10 ms simulated
latency on reads. Its actual request window was 300.011788 seconds,
including in-flight completion. A 360-second outer watchdog covered the run.
It overlapped up to four fuzz target processes, so latency and throughput include
CPU contention. A first sandboxed attempt could not bind localhost; it produced
a failed report and clean shutdown, and is excluded from these measurements.

| Measurement | Result |
| --- | ---: |
| Successful requests | 281,568 |
| Reads / writes | 225,255 / 56,313 |
| Throughput | 938.523 requests/s |
| Request p95 / p99 | 29.993 / 83.062 ms |
| SSE deliveries | 225,252 |
| Updates received by each stream | 56,313 |
| Write-start-to-event p95 / maximum | 65.823 / 1161.487 ms |
| Workload process CPU | 281.262 seconds |
| Peak process RSS | 33.11 MiB |
| Errors / resources left after cleanup | 0 / 0 |

All writes matched the final balance and retained event count; every stream
received each event exactly once. The listener and database closed, and client
writers/tasks, host tasks, and active database reads all reached zero. Review
also strengthened the harness itself: impossible read balances and speculative
SSE events predating their write dispatch now fail with regressions.

CPU/memory cover the combined clients, host, application, SQLite threads and
tracemalloc process. Percentiles use bounded 2,048-item reservoirs; totals and
extrema are exact. Current sampled RSS moved from 29.80 MiB to 23.84 MiB, while
SQLite retained 56,313 event rows and grew from 16,384 to 704,512 page bytes.
Traced Python memory after cleanup was about 0.99 MiB. History/reservoir warmup,
retained database rows, native allocation and OS residency all affect these
measurements. They do not prove leak freedom or establish a latency guarantee.
See the [load harness guide](../../../examples/reference_service/loadtest/README.md)
for metric definitions and resource bounds.

## Final checks and limits

The final sources passed `go test -race ./...`, `go vet ./...`, all 12 runtime
support tests, all 28 service/load tests, syntax and Unicode gates, the 5,948-case
expression and default 73-function differential gates, coverage-map validation,
35 benchmark harness tests, and 14 campaign-runner tests. Darwin's linker emitted
LC_DYSYMTAB warnings during race builds; all packages completed successfully.
The reference service also passed a fresh uncached verification.

This is stronger finite regression and workload evidence. Independent soundness
and security review, broader platforms/workloads, dedicated performance budgets,
schema freeze and reproducible public release remain separate work.
