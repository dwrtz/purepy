# Writing PurePy 0.1 programs

PurePy checks a closed subset of Python 3.14 without running your source. A
successful check applies to every Python module beneath the configured source
root. Put ordinary Python runtime mechanics outside that root and describe any
external functions in a trusted declarative manifest.

## Start with immutable data and ordinary functions

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

Parameters, returns and record fields have explicit exact types. Records have
fields without defaults, methods or inheritance. Their runtime instances are
frozen and slotted. Tuples are homogeneous and recursively immutable; use an
annotation for an empty tuple, such as `lines: tuple[Line, ...] = ()`.

The primitive types are `None`, `bool`, `int`, `float`, `str` and `bytes`.
`bool` is distinct from `int`; mixed numeric arithmetic requires an explicit
conversion. Local assignment rebinds a name and preserves its declared type.
Attribute assignment, list/dict/set mutation, and arbitrary method calls reject.

## Handle missing values explicitly

```python
def increment(number: int | None) -> int:
    if number is None:
        return 0
    return number + 1
```

An exact `is None` or `is not None` guard narrows an optional local. Conditions
must be `bool`, so write `len(text) > 0` rather than using string truthiness.
Both branches and unreachable source are checked. Loops may execute zero times;
a variable assigned only in a loop is not definitely assigned afterward.

Record equality requires all nested fields to support sealed equality. An
immutable opaque external field can be stored and forwarded but does not acquire
an equality implementation from its manifest. Identity checks against `None`
remain available for every Pure Value.

## Compose through exact declarations

Use direct imports such as `from app.domain import total`. Module imports,
aliases, re-exports, import cycles, local callable values and dynamic call targets
reject. Calls use fixed positional or explicit keyword arguments; defaults,
`*args`, `**kwargs`, callbacks and parameter generics are outside 0.1.

Module constants require the imported `Final` marker and an immutable initializer:

```python
from typing import Final

LIMIT: Final[int] = 100
```

Initializers may use literals, earlier constants, tuples and declared record
constructors. They cannot call arbitrary functions. Importing a verified module
therefore does not perform application work.

## Pass external authority explicitly

```python
from host.database import DatabaseRead, load_count

async def doubled_count(database_read: DatabaseRead) -> int:
    count = await load_count(database_read)
    return count * 2
```

This example requires a manifest declaring the import-safe external module, the
capability type and the exact async function signature. Capabilities are original
parameters forwarded directly into known calls. They cannot be constructed,
aliased, returned, compared or stored in records. Host references obey the same
forwarding restrictions and do not themselves grant authority.

Every async call must be directly awaited inside an async function. The host
starts concurrent invocations and owns cancellation and cleanup. Verified code
does not create tasks, save coroutines or manage context-manager resources.
See the [host-boundary guide](HOST_BOUNDARY.md) and
[manifest format](MANIFESTS.md) for the full contract.

## Configure and check

```toml
[tool.purepy]
language = "0.1"
python_syntax = "3.14"
source_root = "src"
entrypoints = ["app.total"]
manifests = []
```

Run `purepy check .`, `purepy capabilities app.total`, or
`purepy explain src/app.py:10:12`. Use `--format json` for deterministic machine
output and `--no-cache` to force reanalysis. Exclusions within the source root
are prohibited. A missing import, unknown operation or unsupported syntax is an
error; there is no unsafe suppression.

The [syntax and intrinsic matrix](SYNTAX_MATRIX.md) enumerates accepted forms.
The [specification](PUREPY_SPEC.md) is authoritative. The
[reference service](../examples/reference_service/README.md) demonstrates routing,
database reads and atomic writes, and SSE loops above a small trusted host.
Verification does not prove termination, absence of deterministic exceptions,
or correctness of trusted external implementations.
