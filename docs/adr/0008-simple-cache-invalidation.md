# ADR 0008: Conservative cache invalidation

Status: accepted for PurePy 0.1.

Reuse content-addressed frontend results only when all relevant inputs match:
verifier/frontend identity, language version, Python syntax version, normalized
configuration, manifest content, module identity and source content. Relink and
recheck the complete project against the current declarations. Missing, corrupt
or incompatible entries are cache misses; cached data never authorizes source.
Output must agree across cold, warm and disabled-cache runs.

This rejects fine-grained semantic dependency invalidation, effect fixed-point
reuse and trusting a previous successful check without current inputs. A broader
key may invalidate harmlessly, but an incomplete key can accept changed behavior.
Correctness and deterministic output precede hit-rate optimization.

Reopen only after named benchmark corpora show invalidation or repeated linking
to be a dominant measured cost. A refined scheme must enumerate every semantic
dependency and demonstrate equivalent results under source, configuration,
manifest, version and import-graph changes, including corruption and concurrent
execution. A higher cache hit rate alone is not acceptance evidence.
