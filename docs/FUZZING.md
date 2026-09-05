# Bounded verifier fuzzing

Run all short campaigns with:

```sh
make fuzz-test
```

The target runs each fuzzer separately for five seconds with two fuzz workers.
CI runs this target after the Go race and vet checks. Ordinary `go test ./...`
and `make race` replay the seed corpora without launching mutation campaigns.
The semantic differential gate remains a separate `make differential-test` target.
These fuzz tests do not invoke Python or execute analyzed project or host code.

## Properties

| Target | Property |
| --- | --- |
| `FuzzCheckerSemantics` | Bounded generated programs obey independent acceptance/rejection expectations for types, flow and calls; worker counts preserve diagnostics and semantic facts. |
| `FuzzCheckerSource` | Bounded arbitrary source either produces ordinary diagnostics or proceeds through linking/checking without a panic or internal-error diagnostic; worker results remain deterministic. |
| `FuzzCacheSummary` | Correctly checksummed malformed IR is rejected before checking; accepted summaries can be linked and checked safely. |
| `FuzzCacheArtifact` | Arbitrary bounded cache bytes either miss safely or decode to a summary that can be checked safely. |
| `FuzzCacheFallback` | Corrupt real cache artifacts trigger reparsing and preserve the complete no-cache JSON report across worker counts, for accepted and rejected programs. |
| `FuzzParseNeverPanics` | Bounded source parsing does not panic and diagnostic byte ranges stay within the input. |
| `FuzzTypeSyntax` | Bounded manifest type strings do not panic the closed annotation parser. |
| `FuzzManifestLoad` | Complete TOML file loading, source projection, declaration validation and ordered merging are deterministic and preserve valid locations; generated valid/invalid pairs require independent verdicts and exact declaration contents. |

The semantic generator uses known valid shapes and targeted invalid variations,
so acceptance is checked independently of the implementation's verdict. The
arbitrary-source target also reaches parser-to-checker combinations outside these
templates. Cache mutations exercise both envelope corruption and malformed
payloads with recomputed checksums; otherwise most inputs would stop at the
checksum test and leave structural validation untested.

Cache checksums detect corruption, not adversarial forgery. These tests do not
require a syntactically valid, deliberately rewritten semantic tree to match its
original source: the cache is a local disposable optimization, not an authenticated
proof. They require malformed summaries to be discarded safely, and genuine
round trips to preserve behavior.

## Bounds and longer campaigns

The semantic generator accepts at most 256 control bytes and emits at most
16 KiB of source, with expression depth at most three and at most four helper
functions. Arbitrary checker source is capped at 4 KiB, structural cache controls
at 64 bytes, raw cache artifacts at 32 KiB, fallback controls at 8 KiB, parser
source at 16 KiB, and manifest type strings at 4 KiB. Cache validation limits
trees to depth 1,024 and 100,000 nodes; focused tests exercise both boundaries.
The manifest type parser also limits type nesting to 64.

The frontend separately checks the native syntax tree before recursive visitors:
depth 512, 100,000 nodes including punctuation, and 64 MiB of cumulative source
spans. It retains at most 1,024 syntax diagnostics plus one explicit resource-limit
diagnostic. Detached node spellings share one immutable source copy. Cache reads
remain limited to 64 MiB even if the file grows during reading, and allocation-free
JSON preflight limits structural boundaries to 2,000,000 overall and 8,192 within
diagnostics, with nesting at most 2,064. Exceeding a cache budget causes a miss and
reparsing. These are implementation resource bounds, not an operating-system
memory limit or a complete bound on native parser allocation before tree inspection.
Full-manifest fuzzing uses at most two files with 16 KiB combined arbitrary TOML,
or at most 64 control bytes generating a valid manifest image and one of 15
targeted invalid variations. Generated inputs are limited to 8 KiB, eight
parameters, and type depth four. It checks inline arrays and table arrays,
cross-file references and conflicts, quoted/Unicode keys, declaration categories,
parameter syntax, and exact source-token locations. Loading remains separate from
the linker's nominal type resolution and host capability authorization.

Fuzz workers get a `GOMEMLIMIT=512MiB` Go memory target; this is a
soft garbage-collection budget, not an operating-system memory cap, and does not
cover all C parser allocations. Bounded corpus sizes constrain those inputs.

Each test process has a two-minute overall timeout and a five-second failure
minimization budget. The mutation duration excludes compilation, baseline corpus
replay and failure minimization, so a five-second target takes longer than five
seconds overall. A timeout or crash is a failure, never a successful skip.

All budgets can be changed explicitly:

```sh
make fuzz-test FUZZ_TIME=1m FUZZ_TIMEOUT=3m FUZZ_PARALLEL=2
make fuzz-test FUZZ_TIME=10m FUZZ_TIMEOUT=12m FUZZ_MINIMIZE_TIME=30s FUZZ_MEMORY=1GiB
```

Run one target for a focused investigation:

```sh
GOMEMLIMIT=512MiB go test ./internal/check -run '^$' -fuzz '^FuzzCheckerSemantics$' -fuzztime=1m -parallel=2 -timeout=3m
GOMEMLIMIT=512MiB go test ./internal/cache -run '^$' -fuzz '^FuzzCacheSummary$' -fuzztime=1m -parallel=2 -timeout=3m
GOMEMLIMIT=512MiB go test ./internal/app -run '^$' -fuzz '^FuzzCacheFallback$' -fuzztime=1m -parallel=2 -timeout=3m
```

The targets use Go's built-in fuzzing. Interesting non-failing inputs may be kept
in Go's disposable build-cache fuzz corpus. Do not treat that cache as permanent
regression evidence.

## Preserve and replay failures

Go writes failing inputs beneath the package's
`testdata/fuzz/FuzzTargetName/<hash>` and prints a replay command. Retain the
minimized file in version control and add a named regression test explaining the
violated invariant. For example, substitute the target and hash printed by Go:

```sh
go test ./internal/check -run 'FuzzCheckerSemantics/<hash>'
go test ./internal/cache -run 'FuzzCacheSummary/<hash>'
```

Correct the implementation or a demonstrably incorrect oracle, replay the exact
input, then run the relevant short campaign and race suite. Never suppress a
failure by swallowing panics or accepting `PP099` as an ordinary rejection.
For a malformed cache case, assert fallback against a fresh source parse as well
as the lower-level decoder behavior.

On CI failure, the workflow uses
[`actions/upload-artifact`](https://github.com/actions/upload-artifact/tree/v4)
to retain the package fuzz corpora and any semantic differential failure report
as `verification-failures` for 14 days. The archive can include existing regression
seeds; use the failed step's log to identify the new input.

Passing short campaigns supplies bounded regression evidence. It does not finish
the plan's long-running fuzz, independent soundness, or release performance gates.

## Recorded campaigns

`make robustness-campaign` runs all eight targets for ten minutes each and writes
commands, budgets, toolchain/runtime metadata, source fingerprints, ordered
results, and complete target logs to `build/robustness-campaign/`. Its default is
one target and one mutation worker at a time. A practical concurrent run is:

```sh
GOCACHE=/tmp/purepy-campaign-cache GOMAXPROCS=2 make robustness-campaign \
  ROBUSTNESS_ARGS='--concurrent-targets 4 --parallel 1 --output build/robustness-campaign'
```

Four concurrent targets still receive ten minutes each, approximately twenty
minutes of wall time plus compilation, baseline replay, and any minimization.
They compete for CPU and memory; execution counts are coverage observations, not
throughput benchmarks. `GOMEMLIMIT=512MiB` applies to each Go process, including
separate fuzz worker processes, and is neither a combined memory cap nor a limit
on C allocations.

The runner requires a completed baseline, the requested mutation-worker startup,
positive execution counts, the requested reported duration (allowing one second
for rounding), and successful Go completion. Seed-only runs, timeouts, crashes,
missing evidence, source changes, or interrupted campaigns fail. SIGINT/SIGTERM
stop active process groups and preserve partial reports; targets not yet started
remain failed rather than becoming successful skips. A Go timeout defaults to
twelve minutes per target, plus a thirty-second parent watchdog allowance.

Use repeated `--target NAME` arguments for a selected rerun, with a new output
directory. Existing evidence is never overwritten. Source identity includes all
files beneath `cmd`, `internal`, and `tools/semantic_probe`, including embedded
Unicode data and regression seeds, plus `go.mod` and `go.sum`. The recorded base
commit and dirty status distinguish work in progress from a committed checkout.
The report is updated after each target, and each result records its raw log's
SHA-256. Go's failing corpus files remain in their package for replay; retain
them as permanent regressions after triage.

`make robustness-test` checks the runner's failure handling and evidence rules
without launching a long campaign. Local campaign results and their limits belong
in a dated validation report; one successful campaign does not establish an
exhaustive security or soundness claim.

The [2026-09-05 report](validation/2026-09-05/README.md) preserves the initial
manifest failure, its fix and full rerun, all target logs, and the associated
whole-function and service-load results.
