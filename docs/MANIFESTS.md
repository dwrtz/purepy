# PurePy manifest schema 1

A manifest is a TOML file containing `schema = 1` and zero or more `[[module]]`,
`[[type]]` and `[[function]]` tables. Add its path to `manifests` in
`purepy.toml`; paths are relative to the configuration file. The verifier reads
these files as data and never imports or executes the host implementation.

```toml
schema = 1

[[module]]
name = "host.database"
import_safe = true

[[type]]
name = "host.database.DatabaseRead"
category = "capability"
labels = ["database.read"]

[[type]]
name = "host.database.Connection"
category = "host_ref"

[[type]]
name = "host.database.Result"
category = "value"
immutable = true

[[function]]
name = "host.database.load"
kind = "async"
trust = "host"
parameters = [
  { name = "read", type = "host.database.DatabaseRead" },
  { name = "connection", type = "host.database.Connection" },
  { name = "id", type = "int" },
]
returns = "host.database.Result | None"
```

Every module requires `name` and an explicit Boolean `import_safe`. Importing
external declarations requires `import_safe = true`; `false` is retained as an
explicit declaration that imports are unavailable to verified code. The module
assertion covers import-time behavior and is part of the trusted host contract.

Types require a fully qualified `name` and one `category`:

| Category | Additional fields | Contract |
| --- | --- | --- |
| `value` | `immutable = true` required | Deeply immutable, opaque Pure Value. Fields are not exposed. |
| `capability` | Nonempty `labels` required | Opaque authority forwarded directly from parameters. |
| `host_ref` | None | Opaque host-owned resource reference; conveys no authority by itself. |

`labels` may appear only on capabilities, and `immutable` only on values.
Labels are unique, nonempty exact strings without whitespace, control characters
or wildcard characters (`*`, `?`). A label grants no hierarchy or implication:
`database` does not imply `database.read`.

Functions require `name`, `kind` (`sync` or `async`), `trust` (`pure` or `host`),
an ordered `parameters` array, and `returns`. Each parameter has exactly `name`
and `type`. An empty parameter list must be written as `parameters = []`.
Parameter names must be unique. Defaults, variadics, callbacks, methods,
overloads, ownership rules, tasks and executable conditions are unsupported.

Type strings admit `None`, `bool`, `int`, `float`, `str`, `bytes`, fully qualified
nominal type names, homogeneous `tuple[T, ...]`, and `T | None`. Names may refer
to types in other configured manifests or verified project records; all names
are resolved after the complete program is linked. Every result must be a Pure
Value. A `pure` function accepts only Pure Values and promises deterministic,
effect-free behavior. A `host` function requires at least one capability
parameter; a host reference alone cannot authorize an operation. The host is
responsible for accurately declaring every effect its implementation performs.

Names use valid Python identifiers in canonical Unicode NFKC spelling. Type and
function names must belong directly to a declared module, which may be declared
in another configured manifest. Declarations are merged in configured file
order. Duplicate names are errors, including identical declarations and names
shared by different declaration kinds. No manifest overrides another. Unknown
fields, duplicate TOML keys and unsupported schema versions are errors.

The machine-readable [schema](../manifests/schema/v1.json) describes the decoded
TOML structure using JSON Schema 2020-12. The verifier additionally checks
identifier spelling, module membership, lexical type syntax, cross-file
duplicates and linked category rules. Trust reports retain the source manifest
path for every declaration. A manifest is a trust assertion about host code,
not proof of that code's behavior.

Configuration uses exactly these fields, with explicit empty arrays when needed:

```toml
[tool.purepy]
language = "0.1"
python_syntax = "3.14"
source_root = "src"
entrypoints = []
manifests = ["manifests/host.purepy.toml"]
```

Configured paths must exist, remain inside the configuration's project root,
and contain no symlinks inside that boundary. Environment and home-directory
interpolation are prohibited. Every Python file under `source_root` is checked;
there are no per-file exclusions. Package directories require `__init__.py`.
The source root itself is the module search boundary, so put a package's
`__init__.py` beneath that boundary, not directly at its root.
