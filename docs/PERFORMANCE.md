# Verifier performance measurements

The development verifier has a reproducible corpus generator and process benchmark
in [tools/benchmark.py](../tools/benchmark.py). It invokes the built Go binary and
never imports or executes the analyzed Python programs. All generated projects,
reference-service copies, declaration edits, and cache deletion occur in a temporary
directory. The repository's application sources and caches are left intact.

From the repository root:

```sh
make setup build
.venv/bin/python tools/benchmark.py --verifier bin/purepy --output /tmp/purepy-benchmark.json
.venv/bin/python tools/benchmark.py --verifier bin/purepy --corpus wide --modules 1000 --functions 10 --repeat 3 --jobs 4 --output /tmp/purepy-wide-1000.json
```

The harness requires macOS or Linux for child-process CPU and resident-memory
measurements. It detects the CPU model where available; `--hardware` supplies an
explicit machine label. `--corpus` selects `tiny`, `deep`, `wide`, `invalid`, or
`reference`, with `all` as the default. The default generated workload has 100
leaf modules and 10 functions per leaf; `--modules`, `--functions`, `--repeat`,
and `--jobs` control its size and run count. Each verifier process has a configurable
`--timeout` in seconds.

The deep corpus contains a linear import chain. The wide corpus has one entry
module that imports and calls every leaf. The invalid corpus contains one unresolved
call per leaf. The tiny corpus has one leaf plus its package and entry module.
The reference corpus copies only its configuration, source, and manifests; host
implementations are absent from the temporary project.

Every corpus run checks byte-for-byte JSON equality across one and the selected
worker count, uncached and cached analysis, and cold and warm analysis. It then
changes one return annotation, checks that the report changes, and compares that
cached result with a fresh uncached check. The declaration change intentionally
produces errors. Cache-hit counts must be zero for the initial population, all
files for the unchanged run, and all but one file after the edit. A mismatch fails
the harness instead of being included as a performance result.

Each repetition removes only that temporary project's cache, measures a check that
populates it, measures an unchanged warm check, and then measures the single-module
edit. These are fresh verifier processes with captured JSON output. Cold means an
empty PurePy cache; it does not mean flushed filesystem caches or a cold CPU.
The separate uncached worker comparison disables cache reads and writes. Its
one-versus-many timing is one observation per corpus, not a scaling benchmark.

The report contains all samples and median/minimum/maximum wall times, child CPU
time and mean CPU utilization, CLI stage timings, cache bytes, and the largest
completed child-process resident-memory peak observed over the whole harness.
CPU utilization uses one logical CPU as 100%. The memory metric includes version
and hardware-query subprocesses and is not a per-stage allocation measurement.
Version-only invocations give a separate process-startup baseline.

`--timings` currently separates configuration, discovery, manifests, linking, and
function checking. Reading, hashing, parsing, lowering, and cache reads/writes share
one `read_hash_parse_lower_cache` interval. The harness reports that combined stage
as provided. `unattributed_wall_ms` is total process wall time minus the recorded
stages; it includes startup, JSON rendering/output, and uninstrumented bookkeeping.
It must not be interpreted as an isolated rendering or startup measurement. Separate
parse/lower/cache/render intervals remain a measurement task from the implementation
plan.

Measurements on 2026-09-05 used an Apple M4, 16 GiB RAM, 10 logical CPUs,
macOS 26.5.2 arm64, and the uv-managed Python 3.14.7 harness. The verifier identified
itself as `purepy 0.1.0-dev`, targeting language 0.1 and Python syntax 3.14. The
measured binary SHA-256 was
`850e93668bd414cd6e53f6cb3afb91ad71a31cb7d4aea8316bd0341c827c43c8`.
The following are medians of three repetitions with four verifier workers:

| Corpus | Source files | Functions | Empty cache, ms | Warm, ms | One changed declaration, ms |
| --- | ---: | ---: | ---: | ---: | ---: |
| Tiny | 3 | 11 | 4.635 | 4.358 | 4.630 |
| Deep, 100 leaves | 102 | 1,001 | 32.511 | 21.569 | 21.780 |
| Wide, 100 leaves | 102 | 1,001 | 31.941 | 21.871 | 21.554 |
| Invalid, 100 leaves | 102 | 1,001 | 31.812 | 21.515 | 20.975 |
| Reference service | 8 | 13 | 7.922 | 6.668 | 6.474 |
| Wide, 1,000 leaves | 1,002 | 10,001 | 265.897 | 167.803 | 168.565 |

All equality checks and cache-hit assertions passed. The invalid corpus reported
100 baseline diagnostics. The large wide corpus had 673,113 source bytes and
26,268,815 cache bytes; its benchmark observed a maximum child resident footprint
of 212,615,168 bytes (202.8 MiB). The five smaller corpora together observed a
32,833,536-byte peak (31.3 MiB). The first three version-only process measurements
were 11.160, 3.921, and 3.674 ms.

For the 1,002-file wide corpus, the reported median stages were:

| Stage | Empty cache, ms | Warm, ms | One changed declaration, ms |
| --- | ---: | ---: | ---: |
| Configuration | 0.106 | 0.109 | 0.103 |
| Discovery | 3.706 | 3.934 | 3.761 |
| Manifest loading | 0.000 | 0.001 | 0.000 |
| Read/hash/parse/lower/cache | 193.490 | 94.215 | 94.406 |
| Link | 10.295 | 10.526 | 9.501 |
| Function check | 17.350 | 17.842 | 18.094 |

The single uncached worker comparison for that corpus took 454.931 ms with one
worker and 197.903 ms with four workers. Corresponding process CPU time was
610.962 and 730.221 ms, about 134% and 369% of one logical CPU. Go runtime work,
including garbage collection, can use multiple CPUs even when verifier work is
configured with one worker. Warm runs still read and decode cached syntax and
repeat project linking and function checking; they are not a cached final verdict.

These observations establish that the harness runs and that cache/worker results
agree on the measured corpora. They do not establish the release's scaling,
memory-growth, regression-budget, or cross-platform performance gates. There are
only two generated workload sizes, one machine, and three repetitions per cached
phase. No comparison with other Python tools or service frameworks was made.
The separate [reference-service load sample](../examples/reference_service/loadtest/sample.json)
measures application execution and is not part of these verifier measurements.
