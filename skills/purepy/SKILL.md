---
name: purepy
description: Write and verify PurePy 0.1 Python projects with the purepy CLI, diagnose rejected source, and inspect capabilities and trusted host boundaries. Use for projects configured with purepy.toml or explicit PurePy requests.
---

# PurePy

PurePy statically verifies a closed, immutable subset of Python 3.14. It never
imports or executes the application. Use the verifier to establish acceptance;
ordinary Python syntax acceptance or passing runtime tests is insufficient.

## Locate the tool and project

Use `purepy version` and `purepy help` to inspect the installed CLI. The default
installation is `~/.local/bin/purepy`; use that path if it is absent from PATH.
In a PurePy source checkout, `make build` produces `bin/purepy` and `make install`
installs the CLI and this skill. Building needs Go 1.24+ and a C compiler, but
verification needs no Python interpreter. Running code that imports `value`
requires the separate `purepy-lang` Python distribution (`from purepy import value`).

Inspect the project's existing `purepy.toml` before changing its structure.
Prefer an explicit `--config` path when working outside the project directory.

## Create a minimal verified project

Place `purepy.toml` beside `src/`:

```toml
[tool.purepy]
language = "0.1"
python_syntax = "3.14"
source_root = "src"
entrypoints = ["app.total"]
manifests = []
```

Put this in `src/app.py`:

```python
from purepy import value

@value
class Line:
    price: int
    quantity: int

def total(lines: tuple[Line, ...]) -> int:
    amount = 0
    for line in lines:
        amount = amount + line.price * line.quantity
    return amount
```

Paths in configuration are relative to its directory and must exist within that
project boundary, without symlinks or environment/home interpolation. All Python
files under `source_root` are checked, including unreachable code; entrypoints
do not limit discovery. Packages beneath that root need `__init__.py` containing
only an optional docstring. Do not place `__init__.py` at the source root itself.

## Check, explain, and inspect authority

```sh
purepy check /path/to/project
purepy check --config /path/to/project/purepy.toml --format json --jobs 4
purepy explain /path/to/project/src/app.py:11:18 --config /path/to/project/purepy.toml
purepy capabilities app.total --config /path/to/project/purepy.toml --format json
purepy check --config /path/to/project/purepy.toml --no-cache --timings
purepy cache clean --config /path/to/project/purepy.toml
```

`check` exits 0 for acceptance, 1 for rejected source, and 2 for usage,
configuration, manifest-loading, or internal errors. Read diagnostics and fix
their source locations, then rerun `check` for the whole project. `explain`
provides location-specific semantic facts; it is not a replacement for a final
successful `check`. JSON output uses schema version 1. `--timings` writes to
stderr; `--no-cache` disables both cache reads and writes.

`capabilities` reports explicit authority, unused capabilities, and direct and
reachable trusted declarations, including their source manifests. Omit the
function argument for a broader report. Use it after changing the host boundary.

## Write within the language

- Annotate every function parameter, return, and record field with an exact type.
  Values are `None`, `bool`, `int`, `float`, `str`, `bytes`, homogeneous
  `tuple[T, ...]`, data-only `@value` records, and optionals `T | None`.
  `bool` is distinct from `int`; mixed numeric arithmetic needs explicit conversion.
- Records have no inheritance, methods, or field defaults. Use typed empty tuples
  (`items: tuple[int, ...] = ()`). No lists, dictionaries, sets, or object mutation.
- Rebind locals without changing their type. Write `n = n + 1`, not `n += 1`.
  Conditions must be `bool`; use `len(text) > 0` instead of truthiness. Narrow
  optional locals with `is None` or `is not None` before using their value.
- Declare functions at module scope and call them directly. Import defining
  symbols with absolute `from package.module import name`; no aliases, re-exports,
  import cycles, callbacks, lambdas, callable values, defaults, or argument unpacking.
- Module constants use `from typing import Final` and `NAME: Final[int] = 1`.
  Initializers cannot perform arbitrary calls. No script guards or application
  work at import time; no `__future__` imports or quoted annotations.
- Every async call is directly awaited inside an async function. No saved
  coroutines, tasks, generators, context managers, or exception handling.
- Built-ins and operators are a sealed set, not all of Python. Common accepted
  intrinsics include `len`, numeric conversions, and `range` directly in a `for`.
  Arbitrary methods and standard-library calls need a supported alternative or
  an accurately declared external operation. There are no unsafe suppressions
  or per-file exclusions.

## Integrate a trusted host

Keep scheduling, I/O resource ownership, cancellation, cleanup, and transaction
execution outside the verified source root. Keep application decisions in
verified functions. The host constructs capabilities and invokes entrypoints.
Capabilities and host references can only be original parameters forwarded
directly to known calls; never alias, construct, return, compare, or store them.
A host reference identifies a resource but grants no authority itself.

For example, add `manifests = ["manifests/host.purepy.toml"]` to the configuration
and create that manifest:

```toml
schema = 1

[[module]]
name = "host.database"
import_safe = true

[[type]]
name = "host.database.DatabaseRead"
category = "capability"
labels = ["database.read"]

[[function]]
name = "host.database.load_count"
kind = "async"
trust = "host"
parameters = [{ name = "read", type = "host.database.DatabaseRead" }]
returns = "int"
```

Verified code can then use:

```python
from host.database import DatabaseRead, load_count

async def doubled_count(read: DatabaseRead) -> int:
    count = await load_count(read)
    return count * 2
```

The manifest declares a contract; implement the actual import-safe module in the
host's runtime environment. `trust = "host"` requires a capability parameter.
`trust = "pure"` promises deterministic, effect-free behavior with Pure Value
parameters and results. External opaque values require `category = "value"`
and `immutable = true`; resource references use `category = "host_ref"`.
All function results must be Pure Values. Declare empty parameters explicitly
as `parameters = []`. Never invent trust assertions merely to silence a diagnostic.

Verification does not prove host contracts, termination, or absence of runtime
exceptions, and is not a Python security sandbox. Run relevant runtime tests
separately when changing application behavior.

For operations beyond this guide, consult the source checkout's
`docs/SYNTAX_MATRIX.md`, `docs/MANIFESTS.md`, `docs/HOST_BOUNDARY.md`, and authoritative
`docs/PUREPY_SPEC.md`. The upstream documentation is at
https://github.com/dwrtz/purepy/tree/main/docs; these files are not bundled with
the installed skill.
