"""Fixed development projects with independently curated static/runtime oracles.

Only this in-repository catalog is executable by the module differential worker.
Sources are not derived from verifier output, user projects, or replay files.
"""

from dataclasses import dataclass
from textwrap import dedent


@dataclass(frozen=True)
class Record:
    name: str
    fields: tuple


@dataclass(frozen=True)
class External:
    name: str
    value: str


@dataclass(frozen=True)
class Call:
    arguments: tuple
    expected: object = None
    exception: str | None = None
    events: tuple = ()


@dataclass(frozen=True)
class ModuleCase:
    name: str
    sources: tuple[tuple[str, str], ...]
    returns: str
    calls: tuple[Call, ...]
    codes: tuple[str, ...] = ()
    manifest: str = ""
    kind: str = "sync"
    classification: str = "pure_sync"


HOST_MANIFEST = '''schema = 1
[[module]]
name = "fixture"
import_safe = true
[[type]]
name = "fixture.Token"
category = "value"
immutable = true
[[type]]
name = "fixture.Read"
category = "capability"
labels = ["fixture.read"]
[[type]]
name = "fixture.Connection"
category = "host_ref"
[[function]]
name = "fixture.double"
kind = "async"
trust = "pure"
parameters = [{name = "n", type = "int"}]
returns = "int"
[[function]]
name = "fixture.read"
kind = "async"
trust = "host"
parameters = [{name = "cap", type = "fixture.Read"}, {name = "conn", type = "fixture.Connection"}, {name = "n", type = "int"}]
returns = "int"
[[function]]
name = "fixture.note"
kind = "async"
trust = "host"
parameters = [{name = "cap", type = "fixture.Read"}, {name = "n", type = "int"}]
returns = "None"
'''


def catalog():
    cases = []

    def add(name, main, returns, calls, *, modules=(), codes=(), host=False,
            kind="sync", effectful=False):
        sources = tuple((name, dedent(source).lstrip("\n")) for name, source in
                        (*modules, ("main", main)))
        cases.append(ModuleCase(name, sources, returns, tuple(calls), tuple(codes),
                                HOST_MANIFEST if host else "", kind,
                                ("effectful_" if effectful else "pure_") + kind))

    pair_source = '''
        from purepy import value
        @value
        class Pair:
            x: int
            label: str
    '''
    pair = lambda n, label: Record("models.Pair", (("x", n), ("label", label)))
    models = (("models", pair_source),)
    for style, construction in (("positional", "Pair(n, 'tag')"),
                                ("keyword", "Pair(label='tag', x=n)")):
        add("records/" + style, f"from models import Pair\ndef probe(n: int) -> Pair:\n    return {construction}\n",
            "models.Pair", [Call((n,), pair(n, "tag")) for n in (-3, 0, 4)], modules=models)
    add("records/field_forwarding", '''
        from models import Pair
        def probe(p: Pair) -> int:
            return p.x
    ''', "int", [Call((pair(-2, "a"),), -2), Call((pair(5, "b"),), 5)], modules=models)
    add("records/equality", '''
        from models import Pair
        def probe(a: Pair, b: Pair) -> bool:
            return a == b and not a != b
    ''', "bool", [Call((pair(1, "x"), pair(1, "x")), True),
                    Call((pair(1, "x"), pair(2, "x")), False),
                    Call((pair(1, "x"), pair(1, "y")), False)], modules=models)
    add("records/tuple_membership", '''
        from models import Pair
        def probe(p: Pair, values: tuple[Pair, ...]) -> bool:
            return p in values
    ''', "bool", [Call((pair(1, "x"), ()), False),
                    Call((pair(1, "x"), (pair(1, "x"),)), True),
                    Call((pair(1, "x"), (pair(2, "x"),)), False)], modules=models)
    add("records/nested_optional_tuple", '''
        from models import Pair
        from purepy import value
        @value
        class Batch:
            entries: tuple[Pair | None, ...]
        def probe(left: Pair | None, right: Pair | None) -> Batch:
            entries: tuple[Pair | None, ...] = (left, right)
            return Batch(entries)
    ''', "main.Batch", [Call((pair(3, "a"), None), Record("main.Batch", (("entries", (pair(3, "a"), None)),))),
                         Call((None, pair(4, "b")), Record("main.Batch", (("entries", (None, pair(4, "b"))),)))], modules=models)
    add("records/optional_refinement", '''
        from models import Pair
        def probe(p: Pair | None) -> Pair:
            if p is None:
                return Pair(0, 'default')
            return p
    ''', "models.Pair", [Call((None,), pair(0, "default")), Call((pair(8, "ok"),), pair(8, "ok"))], modules=models)
    secret = '''
        from purepy import value
        @value
        class Secret:
            __key: int
    '''
    add("records/private_mangled_keyword", '''
        from models import Secret
        def probe(n: int) -> Secret:
            return Secret(_Secret__key=n)
    ''', "models.Secret", [Call((7,), Record("models.Secret", (("_Secret__key", 7),)))], modules=(("models", secret),))
    add("records/private_mangled_read", '''
        from models import Secret
        def probe(n: int) -> int:
            secret = Secret(n)
            return secret._Secret__key
    ''', "int", [Call((7,), 7)], modules=(("models", secret),))
    add("records/private_source_keyword_rejected", '''
        from models import Secret
        def probe(n: int) -> Secret:
            return Secret(__key=n)
    ''', "models.Secret", [Call((7,), exception="TypeError")], modules=(("models", secret),), codes=("PP302",))
    add("records/exact_field_type", '''
        from models import Pair
        def probe(flag: bool) -> Pair:
            return Pair(flag, 'bool')
    ''', "models.Pair", [Call((True,), pair(True, "bool"))], modules=models, codes=("PP205",))
    add("imports/nominal_names_are_distinct", '''
        from left import Pair
        from right import make
        def probe(n: int) -> Pair:
            return make(n)
    ''', "left.Pair", [Call((1,), Record("right.Pair", (("x", 1), ("label", "wrong module"))))],
        modules=(("left", pair_source), ("right", dedent(pair_source) + "def make(n: int) -> Pair:\n    return Pair(n, 'wrong module')\n")), codes=("PP205",))
    add("imports/absolute_helper_composition", '''
        from models import Pair
        from helper import shift
        def probe(n: int) -> Pair:
            return shift(Pair(n, 'm'))
    ''', "models.Pair", [Call((2,), pair(3, "m"))], modules=(*models, ("helper", '''
        from models import Pair
        def shift(p: Pair) -> Pair:
            return Pair(p.x + 1, p.label)
    ''')))
    add("imports/package_absolute_composition", '''
        from pkg.models import Pair
        from pkg.helper import make
        def probe(n: int) -> Pair:
            return make(n)
    ''', "pkg.models.Pair", [Call((4,), Record("pkg.models.Pair", (("x", 4), ("label", "package"))))], modules=(
        ("pkg.__init__", '"""A data-only development package."""\n'),
        ("pkg.models", pair_source),
        ("pkg.helper", "from pkg.models import Pair\ndef make(n: int) -> Pair:\n    return Pair(n, 'package')\n")))
    add("imports/alias_rejected", '''
        from models import Pair as Alias
        def probe(n: int) -> Alias:
            return Alias(n, 'alias')
    ''', "models.Pair", [Call((1,), pair(1, "alias"))], modules=models, codes=("PP003",))
    add("imports/plain_import_rejected", '''
        import models
        def probe(n: int) -> models.Pair:
            return models.Pair(n, 'module')
    ''', "models.Pair", [Call((1,), pair(1, "module"))], modules=models, codes=("PP003",))
    add("imports/relative_import_rejected", '''
        from pkg.helper import make
        from pkg.models import Pair
        def probe(n: int) -> Pair:
            return make(n)
    ''', "pkg.models.Pair", [Call((4,), Record("pkg.models.Pair", (("x", 4), ("label", "relative"))))], modules=(
        ("pkg.__init__", ""), ("pkg.models", pair_source),
        ("pkg.helper", "from .models import Pair\ndef make(n: int) -> Pair:\n    return Pair(n, 'relative')\n")), codes=("PP003",))
    add("imports/constant_record_initialization", '''
        from settings import DEFAULT
        from models import Pair
        def probe() -> Pair:
            return DEFAULT
    ''', "models.Pair", [Call((), pair(6, "constant"))], modules=(*models, ("settings", '''
        from typing import Final
        from models import Pair
        DEFAULT: Final[Pair] = Pair(6, 'constant')
    ''')))

    token_a, token_b = External("fixture.Token", "a"), External("fixture.Token", "b")
    box = lambda token: Record("main.Box", (("token", token),))
    box_source = "from purepy import value\nfrom fixture import Token\n@value\nclass Box:\n    token: Token\n"
    add("opaque/construct_and_forward", box_source + "def probe(token: Token) -> Box:\n    return Box(token)\n",
        "main.Box", [Call((token_a,), box(token_a))], host=True)
    add("opaque/record_equality_rejected", box_source + "def probe(a: Token, b: Token) -> bool:\n    return Box(a) == Box(b)\n",
        "bool", [Call((token_a, token_b), False, events=(("equal", "a", "b"),))], host=True, codes=("PP212",))
    add("opaque/nested_membership_rejected", box_source + '''
def probe(a: Token, b: Token) -> bool:
    values: tuple[Box, ...] = (Box(a),)
    return Box(b) in values
''', "bool", [Call((token_a, token_b), False, events=(("equal", "a", "b"),))], host=True, codes=("PP212",))
    add("opaque/optional_identity", "from fixture import Token\ndef probe(token: Token | None) -> bool:\n    return token is None\n",
        "bool", [Call((None,), True), Call((token_a,), False)], host=True)
    add("async/direct_helpers_and_sync", '''
        from helper import increment, scale
        async def probe(n: int) -> int:
            first = await increment(n)
            return scale(await increment(first))
    ''', "int", [Call((n,), (n + 2) * 3) for n in (-2, 0, 4)], kind="async", modules=(("helper", '''
        async def increment(n: int) -> int:
            return n + 1
        def scale(n: int) -> int:
            return n * 3
    '''),))
    add("async/record_result", '''
        from models import Pair
        async def make(n: int) -> Pair:
            return Pair(n, 'async')
        async def probe(n: int) -> Pair:
            return await make(n)
    ''', "models.Pair", [Call((9,), pair(9, "async"))], kind="async", modules=models)
    add("async/trusted_pure_operation", '''
        from fixture import double
        async def probe(n: int) -> int:
            return await double(n)
    ''', "int", [Call((n,), n * 2) for n in (-1, 0, 3)], kind="async", host=True)
    cap, conn = External("fixture.Read", "read"), External("fixture.Connection", "connection")
    add("async/capability_host_reference_and_none", '''
        from fixture import Read, Connection, read, note
        async def helper(cap: Read, conn: Connection, n: int) -> int:
            return await read(conn=conn, n=n, cap=cap)
        async def probe(cap: Read, conn: Connection, n: int) -> int:
            first = await helper(cap, conn, n)
            await note(cap, first)
            return first + 1
    ''', "int", [Call((cap, conn, n), n + 11, events=(("read", "read", "connection", n), ("note", "read", n + 10)))
                    for n in (0, 2)], kind="async", effectful=True, host=True)
    add("async/loop_await_order", '''
        from fixture import Read, note
        async def probe(cap: Read, n: int) -> int:
            total = 0
            for i in range(n):
                await note(cap, i)
                total = total + i
            return total
    ''', "int", [Call((cap, 0), 0), Call((cap, 3), 3, events=(("note", "read", 0), ("note", "read", 1), ("note", "read", 2)))],
        kind="async", effectful=True, host=True)
    add("async/domain_exception", '''
        async def divide(n: int) -> float:
            return 1 / n
        async def probe(n: int) -> float:
            return await divide(n)
    ''', "float", [Call((2,), 0.5), Call((0,), exception="ZeroDivisionError")], kind="async")
    # CPython can execute this, but PurePy's direct-await rule rejects it.
    add("async/indirect_await_rejected", '''
        async def helper(n: int) -> int:
            return n + 1
        async def probe(n: int) -> int:
            pending = helper(n)
            return await pending
    ''', "int", [Call((2,), 3)], kind="async", codes=("PP003",))
    return tuple(cases)
