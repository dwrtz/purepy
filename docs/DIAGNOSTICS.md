# PurePy diagnostic registry

PurePy diagnostics are errors: an unsupported or unresolved operation never
becomes a warning or a purity assumption. This registry describes the current
PurePy 0.1 implementation. Once a code is published in a tagged language release,
its meaning must not be repurposed; add a code for a new meaning and preserve
existing consumers. Wording and explanatory notes may improve without changing
the underlying rule. Unassigned numbers remain reserved within their family.

| Family | Reserved purpose |
| --- | --- |
| `PP0xx` | Configuration, syntax and internal verifier failures |
| `PP1xx` | Modules, imports and name resolution |
| `PP2xx` | Exact types, Pure Values and local control flow |
| `PP3xx` | Calls and capability/host-reference authorization |
| `PP4xx` | Direct async composition |
| `PP5xx` | Module initialization and prohibited mutation |
| `PP6xx` | Manifest schema, categories and trust |
| `PP7xx` | Entrypoints and host boundaries |

| Code | Meaning | Typical correction |
| --- | --- | --- |
| `PP001` | Configuration cannot be read or fails the strict schema, version or path policy. | Supply a valid `purepy.toml`, explicit lists and safe existing paths. |
| `PP002` | Invalid Python 3.14 syntax, encoding, indentation or literal spelling; parser failure. | Correct the located syntax or use valid UTF-8 source. |
| `PP003` | Python syntax or statement context is outside the accepted subset. | Use the accepted form in the syntax matrix; move runtime mechanics into the host. |
| `PP099` | A malformed semantic node or unexpected internal verifier failure. | Report the reproducible identifier when present and a minimized input; acceptance was not established. |
| `PP101` | Source discovery, reading or package-initializer shape is invalid. | Add required `__init__.py` files, remove ambiguous/symlink paths, and keep initializers empty or docstring-only. |
| `PP102` | A direct import is invalid or cannot resolve. | Import an exact declared symbol from its defining absolute module. |
| `PP103` | A declaration, import binding or parameter conflicts with another name. | Give each binding one unique declaration. |
| `PP104` | A name is unknown or dunder access is prohibited. | Use an explicitly declared ordinary name. |
| `PP105` | The verified import graph contains a cycle. | Move shared declarations into an acyclic dependency module. |
| `PP106` | A project module shadows a sealed support package. | Rename the project module; `purepy` and `typing` support meanings are fixed. |
| `PP201` | A Pure Value is required but the type belongs to another category. | Keep capabilities and host references in parameter forwarding only. |
| `PP202` | A record violates the data-only `@value` declaration form. | Use the exact decorator and annotated fields without bases, methods or defaults. |
| `PP203` | A type annotation is missing/unsupported, or a literal has no admitted type. | Write one concrete supported type and use an approved literal. |
| `PP204` | Value-record types are recursive. | Use a finite nonrecursive record structure. |
| `PP205` | Exact types disagree at an assignment, argument, result, condition or control-flow join. | Keep one exact type; use an explicit approved conversion or optional annotation. |
| `PP206` | A local may be read before definite assignment. | Initialize it on every reachable path before use. |
| `PP207` | A tuple is heterogeneous or an empty tuple lacks a contextual element type. | Use one element type or a record; annotate an empty tuple context. |
| `PP208` | Attribute access is not a declared field of an exact verified record. | Use the record's declared field or a manifest-backed free function. |
| `PP209` | An operator has no sealed rule for the exact operands. | Use admitted same-type operands and an operator from the sealed table. |
| `PP210` | Subscription or slicing violates the immutable sequence rules. | Index tuple/str/bytes with `int`; slice with `int | None` bounds and no step. |
| `PP211` | String formatting uses an unsupported value, conversion or specification. | Interpolate exact bool/int/float/str with no nonempty formatting specification. |
| `PP212` | Comparison has no exact equality, order, membership or `None` identity rule. | Use an admitted pair of comparable Pure Value types. |
| `PP213` | A function may fall through without its declared non-None result. | Return the declared value on every completing path. |
| `PP214` | A `for` expression is not an approved iterable. | Iterate over direct `range`, a homogeneous tuple, `str` or `bytes`. |
| `PP215` | `break` or `continue` appears outside a loop. | Place loop control in its enclosing loop. |
| `PP301` | A call target is not one statically known callable declaration, or a declaration is used as ordinary data. | Call a top-level function, intrinsic or verified record constructor directly. |
| `PP302` | Arguments cannot bind exactly to the fixed parameter list. | Supply each required parameter once without unpacking; intrinsics use positional arguments. |
| `PP303` | No sealed intrinsic signature matches the supplied arguments. | Use an exact supported intrinsic form. |
| `PP304` | A `range` value escapes its direct `for` consumption. | Write `for item in range(...)` directly. |
| `PP312` | A capability argument is missing or is not the original parameter of the required exact capability type. | Declare and directly forward that capability parameter. |
| `PP313` | A capability is aliased, rebound or used as ordinary data. | Retain its original parameter name and use it only as a direct known-call argument. |
| `PP334` | A host reference is aliased, rebound, inspected, stored or forwarded incorrectly. | Directly forward the original exact host-reference parameter. |
| `PP401` | An async call is used without direct await, exposing a coroutine value. | Directly await the known async call. |
| `PP402` | Await does not directly contain one known call. | Write `await known_async_function(...)`. |
| `PP403` | A synchronous function, intrinsic or record construction is awaited. | Use an ordinary synchronous call. |
| `PP404` | Await appears outside an async function. | Put direct async composition in an `async def` entrypoint or helper. |
| `PP501` | A module assignment is not one initialized `Final[T]` constant. | Supply the exact module-constant declaration form. |
| `PP502` | A module initializer contains a nonconstant expression, call or unavailable constant. | Use literals, earlier/imported constants, tuples and direct record construction only. |
| `PP503` | Assignment or parameter binding would mutate/rebind a prohibited target or shadow a module/imported declaration. | Rebind one ordinary local name without hiding fixed declarations. |
| `PP601` | A manifest cannot load, has invalid schema/declarations, or conflicts with the program's module boundary. | Fix the manifest and its source/module names; declarations never override one another. |
| `PP602` | A manifest type is unresolved/unsupported, its result is not pure, or a trusted-pure signature contains a non-Pure Value. | Declare exact resolvable types and preserve Pure Value inputs/results for pure operations. |
| `PP603` | A host operation has no capability parameter. | Declare the capability that authorizes its effects, or declare a genuinely pure operation as pure. |
| `PP604` | An imported external module is not marked `import_safe`. | Supply an accurate import-safety assertion in its trusted manifest. |
| `PP701` | An entrypoint is duplicate or does not name a verified top-level function. | Configure the exact qualified project function name. |

The configuration and manifest loaders include the relevant file path in their
messages. Errors produced before source parsing use an input-level location;
semantic diagnostics use the rejected source range. A rejected construct may
produce more than one diagnostic when multiple independent rules apply.

`purepy check --format json` emits a report with integer `schema = 1`, verifier
and language versions, `ok`, file/function summaries and `diagnostics`. Each
diagnostic contains:

| JSON field | Meaning |
| --- | --- |
| `code` | Stable registry identifier. |
| `severity` | Currently `error`. |
| `message` | Explanation of the violated rule. |
| `span` | File, half-open byte offsets `start`/`end`, and one-based Unicode code-point line/column positions. |
| `symbol` | Qualified related symbol, when available. |
| `notes` | Ordered explanatory strings, possibly empty. |
| `related_locations` | Other relevant spans, possibly empty. |

Diagnostics sort by file, starting byte offset, code and message. Cache hits,
worker scheduling and stage timings do not change the JSON report. Timing output
is requested with `--timings` and goes to stderr.

The `check` command exits with `0` for successful verification, `1` for rejected
source or linked semantics, and `2` for command/configuration/manifest-loading
failure or an internal CLI failure. Usage errors are printed directly to stderr.
Use `purepy explain FILE:LINE[:COLUMN] --config PATH` for source facts and local
diagnostics, or `purepy capabilities --config PATH` for verified entrypoint
authority and trusted external dependencies.
