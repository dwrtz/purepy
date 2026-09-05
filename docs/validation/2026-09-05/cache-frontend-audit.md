# Cache and frontend audit

This bounded review covers malformed cache decoding, parser/IR resource growth,
source-text ownership, and accidental execution in the frontend and cache paths.
It addresses the corresponding items in plan section 15.11. It does not complete
the separate configuration, discovery, filesystem-concurrency, runtime, or
authority reviews, and is not a claim of arbitrary-program soundness.

The reviewed baseline was commit `6772045`. The following corrections and
regressions are in the working tree alongside this report.

## Findings and corrections

1. **Nested source spellings amplified Go allocation.** Real `frontend.Parse`
   calls on addition chains with 1,000, 2,000, and 4,000 terms accepted sources
   of 4,035, 8,035, and 16,035 bytes while allocating approximately 4.2, 12.5,
   and 41.7 MB respectively. The pinned tree-sitter binding's `Utf8Text` copies
   the source range on each call; overlapping subtree text therefore caused
   quadratic copying. Keeping node spellings as substrings of one immutable
   source copy reduced the same probe to approximately 2.0, 3.9, and 7.8 MB
   before adding structural limits. The final parser also rejects excessive
   native depth, breadth, and overlapping spans before any recursive Go visitor.
   A regression checks a bounded nested expression's allocation ceiling and
   verifies that modifying the caller's source buffer cannot change detached IR.

2. **Malformed diagnostic arrays allocated before validation.** A checksum-valid
   300,216-byte cache artifact containing 100,000 empty diagnostic objects
   allocated approximately 107.5 MB before returning a miss. JSON structure is
   now checked without allocation before typed decoding, with a tighter budget
   for diagnostics. A raw-member projection is decoded separately so an earlier
   duplicate key cannot cause an unchecked array allocation before a later key
   overwrites it. Regression tests keep the reproducer below an 8 MiB allocation
   ceiling, exercise duplicate/case-insensitive keys, and ensure structural
   punctuation in quoted messages does not consume the budget.

3. **Invalid trivia produced unbounded diagnostics.** Inputs containing 1,000
   and 4,000 zero-width spaces produced 1,000 and 4,000 diagnostics despite a
   trivial native syntax tree. The parser now retains at most 1,024 diagnostics
   plus an explicit resource-limit diagnostic, and stops repeated lexical scans
   once this limit is reached. The source remains rejected.

4. **The cache byte bound depended on stale file metadata.** Reads now compare
   the opened file's identity and regular-file status against the initial check,
   and use a limited reader. Growth after metadata inspection cannot exceed the
   64 MiB read bound. Existing symlink and concurrent atomic-write tests continue
   to pass. This is not a guarantee against every filesystem mutation race.

The frontend cache identity is now `ir-5`, so cached trees from before the new
frontend resource checks cannot bypass those checks on ordinary cache hits.

## Bounds and remaining trust assumptions

- Native syntax trees are limited to depth 512, 100,000 nodes, and 64 MiB of
  cumulative source spans. These checks occur after the native parser produces
  its tree; source ingestion and native parser allocation still require separate
  project-level limits. At this review's completion, `internal/app/check.go`
  still reads source files with `os.ReadFile` before parsing.
- Cache JSON preflight allows 2,000,000 structural boundaries overall, 8,192
  within diagnostics, and nesting depth 2,064. Existing semantic shape checks
  still impose 100,000 IR nodes and depth 1,024. An oversized cache entry is
  disposable and becomes a miss, potentially reducing hit rate for unusually
  large rejected files. These are finite processing bounds, not an OS memory cap.
- SHA-256 keys include source content and current program inputs; envelope
  checksums detect corruption. Neither authenticates the cached IR as the actual
  parse of those bytes. Deliberate valid IR replacement with a recomputed key and
  checksum remains outside the documented local-cache trust model. Untrusted
  cache artifacts must not be treated as certificates or portable proofs.
- Static inspection found no Python execution or import facility in frontend,
  cache, model, or verification application production code. Existing CLI
  sentinel tests demonstrate that rejected project code and manifest-backed
  excluded host modules are not executed. The development syntax gate calls
  Python's AST parser only; semantic differential tools intentionally execute
  generated test programs and are a separate development workflow.
- The pinned C parser, Go runtime, identifier/Unicode tables, and verifier remain
  trusted. This review does not prove total native memory safety or termination.

## Validation

The focused package suite passed:

```text
GOCACHE=/tmp/purepy-review-go-cache go test ./internal/cache ./internal/frontend ./internal/app -count=1
GOCACHE=/tmp/purepy-review-go-cache go test -race ./internal/cache ./internal/frontend -count=1
.venv/bin/python tools/differential_syntax.py
git diff --check
```

The Python 3.14.7 AST gate passed all 34 accepted conformance snippets and 158
syntax edges. The Darwin linker emitted its existing `LC_DYSYMTAB` warning while
building race-test binaries; the tests passed. Temporary growth probes were
removed after permanent regressions were added. No new extended fuzz campaign or
release-readiness claim is made by this report.
