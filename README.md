# PurePy

PurePy verifies a small, immutable subset of Python without importing or executing
the project being checked. Calls resolve to exact top-level declarations, external
authority requires explicit capability parameters, and async composition uses only
direct `await` of known calls.

This repository contains the experimental PurePy 0.1 verifier, the `@value` Python
support package, conformance tests, and a working asynchronous reference service.
The Go module is `github.com/dwrtz/purepy`.

## Build and set up

Prerequisites: Go 1.24 or later, a C compiler for the pinned tree-sitter parser,
and `uv`. The Makefile creates a repository-local `.venv` with Python 3.14 and
installs the support package through the checked-in `python/uv.lock`.

```sh
make setup
make build
bin/purepy version
make example
```

`make setup` runs `uv sync`; rerunning it updates the existing environment. Override
the executable or interpreter with `UV=...` or `PYTHON_VERSION=...` if needed.
Building and running the Go verifier itself does not require Python.

## Verify a project

Create `purepy.toml` beside a `src` directory:

```toml
[tool.purepy]
language = "0.1"
python_syntax = "3.14"
source_root = "src"
entrypoints = ["app.total"]
manifests = []
```

Then put this in `src/app.py`:

```python
from purepy import value

@value
class Line:
    price: int
    quantity: int

def total(line: Line) -> int:
    return line.price * line.quantity
```

```sh
bin/purepy check /path/to/project
bin/purepy check --config /path/to/project/purepy.toml --format json --jobs 4
bin/purepy capabilities app.total --config /path/to/project/purepy.toml
bin/purepy explain /path/to/project/src/app.py:10:12 --config /path/to/project/purepy.toml
bin/purepy cache clean --config /path/to/project/purepy.toml
```

Use `--no-cache` to disable cache reads and writes, and `--timings` for stage timings
on stderr. Exit status is 0 on success, 1 for rejected source, and 2 for usage,
configuration, manifest-loading, or internal errors. JSON schema version 1 is
deterministic across worker counts and cache states.

## Test and run the service

```sh
make test
make race
make fuzz-test
make python-test
make service-test
make syntax-test
make differential-test
make unicode-test
make coverage-test
make schema-test
make benchmark-test
make acceptance-test
make package-test
make loadtest
make serve
```

The Python targets use `.venv`, with `make setup` as a prerequisite. The service
keeps request parsing, routing, domain validation, rendering, and its SSE loop in
verified source. A small excluded host owns asyncio scheduling, sockets, SQLite,
atomic transactions, and cancellation. Its tests exercise real concurrent socket
requests, rollback, repeated SSE events, and cleanup.

`make differential-test` compares the verifier's operator and intrinsic types,
whole-function verdicts, and exact runtime return types with CPython 3.14 on a
reproducible generated corpus. Whole functions exercise optional narrowing,
branches, loop exits, and helper calls with multiple inputs. See the
[differential testing guide](docs/DIFFERENTIAL_TESTING.md) for case selection,
larger seeded runs, and preserving mismatches as regression fixtures.

`make unicode-test` verifies the pinned Unicode name data and compares character
names and aliases with CPython 3.14. See the [Unicode data guide](docs/UNICODE.md)
for offline regeneration. Building and running the verifier uses the checked-in
Go tables and requires no Python interpreter.

`make fuzz-test` runs short bounded campaigns over checker semantics, source,
cache summaries and fallback, parsing, manifest type syntax, and complete
manifest loading. `make robustness-campaign` records longer campaigns with
per-target logs, source identity, budgets, and outcomes. See the
[fuzzing guide](docs/FUZZING.md) for campaign controls and regression replay.

`make loadtest LOADTEST_ARGS='--duration 300 --sse-connections 4'` sustains mixed
reads and writes while draining SSE streams and checking every update. The
[load-test guide](examples/reference_service/loadtest/README.md) explains bounded
statistics, memory accounting, and measurement limits.

`make benchmark` measures repeated cold, warm, edited, and uncached verification,
with separate pipeline/worker timings and per-process memory. Pass
`BENCHMARK_ARGS='--sizes 100,1000 --workers 1,4 --corpora wide,invalid,manifest'`
for a scaling matrix. `make benchmark-compare` checks matching measurements against
tolerant regression budgets; see the [performance guide](docs/PERFORMANCE.md).
`make package` prepares local binary/checksum and Python distribution artifacts in
`dist/candidate`, together with a source archive and verified inventories. Use a
fresh `RELEASE_OUTPUT=...` for each candidate. Neither target publishes a release.

## Design and status

- [Language specification](docs/PUREPY_SPEC.md) and [implementation plan](docs/PUREPY_PLAN.md)
- [Language guide](docs/LANGUAGE_GUIDE.md)
- [Implemented syntax and intrinsic table](docs/SYNTAX_MATRIX.md)
- [Manifest schema and examples](docs/MANIFESTS.md)
- [Host-boundary guide](docs/HOST_BOUNDARY.md)
- [Diagnostics](docs/DIAGNOSTICS.md)
- [Implementation and conformance status](docs/IMPLEMENTATION.md)
- [Specification-to-test coverage](docs/CONFORMANCE.md)
- [Measured verifier performance](docs/PERFORMANCE.md)
- [Frozen schema contracts](docs/SCHEMA_CONTRACT.md) and [release preparation](docs/RELEASE.md)
- [Reference service](examples/reference_service/README.md)

The implementation deliberately has no unsafe suppression, executable verifier
plugins, per-file exclusions, mutable containers, higher-order calls, task creation,
generators, context managers, or runtime effect framework.
