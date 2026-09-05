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
