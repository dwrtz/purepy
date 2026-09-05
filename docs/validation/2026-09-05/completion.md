# PurePy 0.1 implementation completion validation

The completion candidate closes the concrete specification, differential testing
and release preparation gaps identified in the implementation review. The
checks below are finite evidence under the documented runtime/host contracts;
they are not a proof of arbitrary-program soundness or external implementation
correctness. Public release status is reported separately after upload.

## Implemented closures

- The normative spec and syntax matrix agree on recursively sealed record
  equality, opaque optional comparison, Python private-name mangling and explicit
  resource-limit rejection. Intrinsic contract version 1 is named in the checker,
  cache identity and CLI version report.
- Focused tests cover forbidden returned value categories, argument unpacking,
  nested unsupported types and every entrypoint parameter-category combination.
- The new fixed module differential corpus covers nominal records, imports,
  opaque values and direct async composition, including host-event provenance.
- Frozen manifest/check/capabilities/explain schemas, explicit source inventories,
  reproducible binary/source/Python packaging, checksums and publication workflows
  make the release procedure executable and auditable.

## Functional and robustness checks

The completion work passed `go test ./...`, `go test -race ./...`, `go vet ./...`,
13 Python runtime tests, 28 service/load integration tests, syntax validation of
34 positive fixtures plus 158 syntax edges, and pinned Unicode 16.0 generation
and CPython 3.14.7 comparisons. Darwin emitted its previously documented linker
warnings during race builds; the test processes succeeded.

With seed 37 and 256 supplemental samples, the differential gates passed 6,620
expression cases and 297 whole-function cases with 2,270 invocations. The fixed
module gate passes 29 projects (20 accepted, nine rejected), 48 invocations and
two independently isolated hash-randomized CPython processes. The module harness
also has 16 tests that reject wrong types, identities, verdicts, event orders and
worker-protocol changes.

All eight fuzz targets completed fresh 60-second campaigns, totaling 2,687,654
executions. The [campaign report](completion-fuzz.json) records unchanged source
fingerprints before/after, exact commands, budgets, outcomes and log hashes.
The logs are preserved in [the log archive](completion-fuzz-logs.tar.gz).
These shorter completion runs supplement the earlier ten-minute campaigns;
they are not described as new ten-minute runs. Later edits added tests and
documentation; the measured production Go code remained unchanged through the
completion benchmark and workload measurements.

## Fixed acceptance budgets

The [acceptance profile](../../../benchmarks/acceptance-m4.json) was set before
candidate measurement from the prior recorded baseline, with explicit absolute
ceilings and required workload sizes. Hardware: Apple M4, ten logical CPUs,
16 GiB RAM, macOS 26.5.2 arm64; Go 1.24.5 and CPython 3.14.7.

The [full verifier matrix](completion-benchmark.json) measured 100/1,000/3,000
modules, ten functions per module, one/four workers, three repetitions and four
corpora. Each sample checks report equivalence, cache hits and edited-source
reanalysis. The [acceptance result](completion-acceptance.json) passes all **314**
verifier and service budget checks. The measured binary SHA-256 is
`2248637075712bd432afc57416cbf126446dad5bfe775e4bf80344fe39b6b583`.

The [five-minute service report](completion-service.json) records:

| Measurement | Result |
| --- | ---: |
| Requests | 462,456 |
| Requests/second | 1,541.464 |
| Request p95 / p99 | 24.155 / 35.792 ms |
| Successful writes | 92,491 |
| SSE deliveries across four streams | 369,964 |
| SSE write-start-to-event p99 | 71.129 ms |
| Peak process RSS | 32.25 MiB |
| Errors / residual tasks, writers and reads | 0 / 0 |

Every stream received every write exactly once. Final balance/event rows matched
the writes; the listener and database closed. Workload traffic overlapped parts
of the fuzz and verifier matrix runs, so this is explicitly local shared-machine
acceptance evidence, not an idle dedicated-runner latency guarantee.

## Investigated historical regression signal

The [comparison with the historical baseline](completion-regression.json)
reported failures during the concurrent validation workload. Those failed results
are retained. The deep 3,000-module warm median was about 1,050 ms versus the
historical 468 ms; that signal was investigated before accepting completion.

A fresh immutable build of base commit `6772045` and the current binary were
then measured sequentially with the same harness, machine, input sizes and
worker count. The [controlled base](completion-controlled-base.json),
[controlled candidate](completion-controlled-head.json), and
[comparison](completion-controlled-comparison.json) pass the unchanged budgets:
24 scalar measurements and 12 growth checks, with no failures.

| Deep 3,000 modules, four workers | Base | Candidate |
| --- | ---: | ---: |
| Uncached | 594.187 ms | 665.748 ms |
| Cache population | 829.047 ms | 929.274 ms |
| Warm | 462.861 ms | 591.602 ms |

The large historical slowdown did not reproduce. There is measured frontend
overhead consistent with the added native-tree and pre-decode resource guards;
the controlled run remained within existing tolerances. No validation was
removed and no regression threshold was increased. The controlled rerun is a
focused diagnosis, not a claim to have repeated the entire matrix while idle.

## Reproduction

```sh
make test race python-test service-test syntax-test differential-test unicode-test coverage-test
make acceptance-test package-test
.venv/bin/python tools/performance_acceptance.py \
  --profile benchmarks/acceptance-m4.json \
  --benchmark docs/validation/2026-09-05/completion-benchmark.json \
  --service docs/validation/2026-09-05/completion-service.json
.venv/bin/python tools/benchmark_compare.py \
  --baseline docs/validation/2026-09-05/completion-controlled-base.json \
  --candidate docs/validation/2026-09-05/completion-controlled-head.json
```

Final release archives carry a complete source-tree fingerprint and member
inventories. These measured artifacts retain their original binary/source
identities instead of being relabeled as measurements of every later release.
