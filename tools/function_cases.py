"""Independent, bounded whole-function cases for PurePy's flow checker.

Static verdicts follow the closed language rules in PUREPY_SPEC.md §§9, 13,
14, and 16. Values below are curated or calculated from small independent
formulas over the inputs; generating cases never compiles or executes source.
These templates exercise function bodies and paths, not expression wrappers.
"""

from dataclasses import dataclass
import random
from textwrap import dedent


@dataclass(frozen=True)
class Invocation:
    arguments: tuple[object, ...]
    expected_value: object = None
    check_value: bool = False
    exception: str | None = None


@dataclass(frozen=True)
class FunctionCase:
    name: str
    family: str
    source: str
    parameters: tuple[tuple[str, str], ...]
    returns: str
    invocations: tuple[Invocation, ...]
    expected_codes: tuple[str, ...] = ()


def _value(arguments, value):
    return Invocation(tuple(arguments), value, True)


def _raises(arguments, exception):
    return Invocation(tuple(arguments), exception=exception)


def _case(name, family, parameters, returns, body, invocations,
          expected_codes=(), helpers=""):
    signature = ", ".join(f"{name}: {annotation}" for name, annotation in parameters)
    body = dedent(body).strip("\n")
    source = dedent(helpers).lstrip("\n")
    if source and not source.endswith("\n"):
        source += "\n"
    source += f"def probe({signature}) -> {returns}:\n"
    source += "\n".join("    " + line if line else "" for line in body.splitlines()) + "\n"
    return FunctionCase(name, family, source, tuple(parameters), returns,
                        tuple(invocations), tuple(expected_codes))


def _fixed_cases():
    cases = []

    def add(*args, **kwargs):
        cases.append(_case(*args, **kwargs))

    add("optional/early_guard", "optional", (("x", "int | None"),), "int", """
        if x is None:
            return 7
        return x + 1
    """, [_value((None,), 7), _value((0,), 1), _value((-3,), -2)])
    add("optional/elif_join", "branch", (("x", "int | None"), ("flag", "bool")), "int", """
        if x is None:
            result = 0
        elif flag:
            result = x + 2
        else:
            result = x - 2
        return result
    """, [_value((None, False), 0), _value((None, True), 0),
           _value((3, True), 5), _value((3, False), 1)])
    add("optional/short_circuit_and", "optional", (("x", "int | None"),), "bool", """
        positive = x is not None and x > 0
        if positive:
            return True
        return False
    """, [_value((None,), False), _value((-1,), False), _value((0,), False), _value((2,), True)])
    add("optional/short_circuit_or", "optional", (("x", "int | None"),), "bool", """
        if x is None or x > 0:
            return True
        return False
    """, [_value((None,), True), _value((-1,), False), _value((0,), False), _value((2,), True)])
    add("optional/conditional_then_branch", "optional", (("x", "int | None"),), "int", """
        number = x if x is not None else 0
        if number < 0:
            return -number
        return number
    """, [_value((None,), 0), _value((-3,), 3), _value((2,), 2)])
    add("branch/annotated_join", "branch", (("flag", "bool"),), "str", """
        result: str
        if flag:
            result = 'left'
        else:
            result = 'right'
        return result + '!'
    """, [_value((True,), "left!"), _value((False,), "right!")])
    add("branch/returning_path_does_not_assign", "branch", (("flag", "bool"),), "int", """
        if flag:
            return 4
        result = 9
        return result
    """, [_value((True,), 4), _value((False,), 9)])
    add("branch/missing_assignment", "branch", (("flag", "bool"),), "int", """
        if flag:
            result = 9
        return result
    """, [_value((True,), 9), _raises((False,), "UnboundLocalError")], ("PP206",))
    add("branch/conflicting_exact_types", "exact_type", (("flag", "bool"),), "int", """
        if flag:
            result = 1
        else:
            result = True
        return result
    """, [_value((True,), 1), _value((False,), True)], ("PP205",))
    add("branch/return_type_bool_is_not_int", "exact_type", (("flag", "bool"),), "int", """
        if flag:
            return True
        return 0
    """, [_value((True,), True), _value((False,), 0)], ("PP205",))
    add("branch/non_none_fallthrough", "early_return", (("flag", "bool"),), "int", """
        if flag:
            return 1
    """, [_value((True,), 1), _value((False,), None)], ("PP213",))

    add("for_tuple/filter_optional", "for_tuple", (("xs", "tuple[int | None, ...]"),), "tuple[int, ...]", """
        result: tuple[int, ...] = ()
        for x in xs:
            if x is None:
                continue
            result = result + (x,)
        return result
    """, [_value(((),), ()), _value(((None,),), ()),
           _value(((1, None, -2, None, 3),), (1, -2, 3))])
    add("for_tuple/break_at_negative", "for_tuple", (("xs", "tuple[int, ...]"),), "int", """
        total = 0
        for x in xs:
            if x < 0:
                break
            total = total + x
        return total
    """, [_value(((),), 0), _value(((-1, 3),), 0),
           _value(((2, 3, -1, 7),), 5), _value(((2, 3),), 5)])
    add("for_tuple/first_present_early_return", "early_return", (("xs", "tuple[int | None, ...]"),), "int | None", """
        for x in xs:
            if x is not None:
                return x
        return None
    """, [_value(((),), None), _value(((None, None),), None),
           _value(((None, 0, 7),), 0), _value(((3, None),), 3)])
    add("for_tuple/zero_iterations_unassigned", "for_tuple", (("xs", "tuple[int, ...]"),), "int", """
        for x in xs:
            pass
        return x
    """, [_raises(((),), "UnboundLocalError"), _value(((3,),), 3),
           _value(((2, 4),), 4)], ("PP206",))
    add("for_tuple/preassigned_target", "for_tuple", (("xs", "tuple[int, ...]"),), "int", """
        x = 7
        for x in xs:
            pass
        return x
    """, [_value(((),), 7), _value(((0,),), 0), _value(((2, 4),), 4)])
    add("for_range/skip_and_break", "for_range", (("n", "int"),), "int", """
        total = 0
        for x in range(n):
            if x == 2:
                continue
            if x >= 5:
                break
            total = total + x
        return total
    """, [_value((-2,), 0), _value((0,), 0), _value((3,), 1),
           _value((5,), 8), _value((8,), 8)])
    add("for_range/descending", "for_range", (("n", "int"),), "tuple[int, ...]", """
        result: tuple[int, ...] = ()
        for x in range(n, 0, -1):
            if x == 2:
                continue
            result = result + (x,)
        return result
    """, [_value((-1,), ()), _value((0,), ()), _value((1,), (1,)),
           _value((4,), (4, 3, 1))])
    add("for_range/bool_bound_is_not_int", "exact_type", (("flag", "bool"),), "int", """
        total = 0
        for x in range(flag):
            total = total + 1
        return total
    """, [_value((False,), 0), _value((True,), 1)], ("PP205",))

    add("while/bounded_counter", "while", (("n", "int"),), "int", """
        total = 0
        i = 0
        while i < n:
            i = i + 1
            if i == 2:
                continue
            if i >= 5:
                break
            total = total + i
        return total
    """, [_value((-2,), 0), _value((0,), 0), _value((1,), 1),
           _value((3,), 4), _value((8,), 8)])
    add("while/optional_guard_rechecked", "while", (("x", "int | None"),), "int", """
        total = 0
        remaining = 1
        while remaining > 0:
            remaining = remaining - 1
            if x is None:
                break
            total = total + x
            x = None
        return total
    """, [_value((None,), 0), _value((0,), 0), _value((-3,), -3)])
    add("while/repeated_condition_loses_refinement", "while", (("x", "int | None"),), "None", """
        if x is None:
            return
        remaining = 1
        while remaining > 0 and x > 0:
            x = None
            remaining = remaining - 1
    """, [_value((None,), None), _value((0,), None), _value((1,), None)], ("PP212",))
    add("nested_loop/inner_break_outer_continue", "nested_loop", (("rows", "tuple[tuple[int, ...], ...]"),), "int", """
        total = 0
        for row in rows:
            if len(row) == 0:
                continue
            for x in row:
                if x < 0:
                    break
                total = total + x
        return total
    """, [_value(((),), 0), _value((((), (2, -1, 8), (3,)),), 5),
           _value((((1, 2), (3, 4)),), 10)])
    add("nested_loop/for_inside_bounded_while", "nested_loop", (("n", "int"), ("xs", "tuple[int, ...]")), "int", """
        total = 0
        i = 0
        while i < n:
            i = i + 1
            for x in xs:
                if x == 0:
                    continue
                total = total + x
        return total
    """, [_value((0, (2, 3)), 0), _value((2, ()), 0),
           _value((3, (2, 0, -1)), 3)])
    add("none/fallthrough_and_explicit_return", "none", (("flag", "bool"), ("xs", "tuple[None, ...]")), "None", """
        for item in xs:
            if flag:
                return item
        if flag:
            return None
    """, [_value((False, ()), None), _value((True, ()), None),
           _value((False, (None, None)), None), _value((True, (None,)), None)])

    add("tuple/optional_elements_swap", "tuple", (("left", "int | None"), ("right", "int | None"), ("swap", "bool")), "tuple[int | None, ...]", """
        if swap:
            result = (right, left)
        else:
            result = (left, right)
        return result
    """, [_value((None, 2, False), (None, 2)), _value((None, 2, True), (2, None)),
           _value((1, None, True), (None, 1)), _value((None, None, False), (None, None))])
    nested = "tuple[tuple[int, ...], ...]"
    add("tuple/optional_nested_result", "tuple", (("xs", nested + " | None"), ("keep", "bool")), nested + " | None", """
        if xs is None:
            return None
        result: tuple[tuple[int, ...], ...] = ()
        if keep:
            result = xs
        return result
    """, [_value((None, True), None), _value((None, False), None),
           _value(((), True), ()), _value((((1, 2), ()), True), ((1, 2), ())),
           _value((((1, 2), ()), False), ())])
    optional_inner = "tuple[int, ...] | None"
    add("tuple/tuple_of_optional_tuples", "tuple", (("left", optional_inner), ("right", optional_inner), ("swap", "bool")), "tuple[tuple[int, ...] | None, ...]", """
        if swap:
            return (right, left)
        return (left, right)
    """, [_value((None, (), False), (None, ())),
           _value((None, (1, 2), True), ((1, 2), None)),
           _value(((3,), (), False), ((3,), ()))])

    add("domain/index_after_branch", "domain", (("xs", "tuple[int, ...]"), ("last", "bool")), "int", """
        if last:
            index = -1
        else:
            index = 0
        return xs[index]
    """, [_raises(((), False), "IndexError"), _raises(((), True), "IndexError"),
           _value(((2, 7), False), 2), _value(((2, 7), True), 7)])
    add("domain/guarded_division", "domain", (("n", "int"), ("divide", "bool")), "int", """
        if divide:
            return 8 // n
        return 17
    """, [_value((0, False), 17), _raises((0, True), "ZeroDivisionError"),
           _value((2, True), 4), _value((-2, True), -4)])

    # Permanent reviewed failures. These names and witness inputs remain present
    # regardless of seed/sample count; rejection is never inferred from a tool.
    for exit_kind in ("break", "continue"):
        add(f"regression/for_target_{exit_kind}_optional", "regression",
            (("xs", "tuple[int | None, ...]"),), "int", f"""
            x: int | None = 1
            if x is None:
                return 0
            for x in xs:
                if x is None:
                    {exit_kind}
            return x
        """, [_value(((),), 1), _value(((None,),), None), _value(((2,),), 2),
               _value(((None, 2),), None if exit_kind == "break" else 2)], ("PP205",))
    add("regression/for_iterable_before_body_rebinding", "regression", (("xs", "tuple[int, ...] | None"),), "int", """
        if xs is None:
            return 0
        total = 0
        for x in xs:
            xs = None
            total = total + x
        return total
    """, [_value((None,), 0), _value(((),), 0), _value(((1, 2),), 3),
           _value(((-2, 0, 3),), 1)])
    add("regression/range_argument_before_body_rebinding", "regression", (("n", "int | None"),), "int", """
        if n is None:
            return 0
        total = 0
        for x in range(n):
            n = None
            total = total + x
        return total
    """, [_value((None,), 0), _value((0,), 0), _value((1,), 0), _value((4,), 6)])
    return cases


_PAYLOADS = (
    ("int", "-2", -2, (0, 3, -1)),
    ("bool", "False", False, (True, False)),
    ("float", "-0.5", -0.5, (0.0, 1.25, -2.5)),
    ("str", "'fallback'", "fallback", ("", "λ", "hello")),
    ("bytes", "b'fallback'", b"fallback", (b"", b"\x00\xff", b"abc")),
    ("tuple[int, ...]", "()", (), ((), (0,), (1, -2))),
    ("tuple[tuple[int, ...], ...]", "()", (), ((), ((),), ((1, 2), ()))),
)


def _payload_case(name, payload, reverse, helper_depth):
    annotation, fallback_source, fallback, values = payload
    helpers = f"def identity0(item: {annotation}) -> {annotation}:\n    return item\n"
    for level in range(1, helper_depth):
        helpers += f"def identity{level}(item: {annotation}) -> {annotation}:\n    return identity{level - 1}(item)\n"
    call = f"identity{helper_depth - 1}(x)"
    if reverse:
        branches = f"if x is not None:\n    result = {call}\nelse:\n    result = {fallback_source}"
    else:
        branches = f"if x is None:\n    result = {fallback_source}\nelse:\n    result = {call}"
    body = f"result: {annotation}\n{branches}\nif keep:\n    return result\nreturn {fallback_source}"
    # An explicit local preserves empty-tuple element context in either branch;
    # a direct fallback return also has the concrete result annotation.
    invocations = [_value((value, keep), (fallback if value is None else value) if keep else fallback)
                   for value in (None, *values) for keep in (False, True)]
    return _case(name, "payload", (("x", annotation + " | None"), ("keep", "bool")),
                 annotation, body, invocations, helpers=helpers)


def _seeded_case(rng, index):
    variant = rng.randrange(4)
    name = f"seeded/{index:03d}"
    if variant == 0:
        return _payload_case(name + "/payload", rng.choice(_PAYLOADS),
                             bool(rng.randrange(2)), rng.randrange(1, 4))
    if variant == 1:
        delta, scale = rng.randrange(-3, 4), rng.randrange(1, 4)
        reverse = bool(rng.randrange(2))
        helpers = f"def offset(item: int) -> int:\n    return item + ({delta})\n"
        if rng.randrange(2):
            helpers += f"def adjust(item: int) -> int:\n    return offset(item) * {scale}\n"
            expression = "adjust(item)"
        else:
            expression = f"offset(item) * {scale}"
        if reverse:
            loop = f"""for item in xs:
    if item is not None:
        total = total + {expression}
    else:
        if stop:
            break
        continue"""
        else:
            loop = f"""for item in xs:
    if item is None:
        if stop:
            break
        continue
    total = total + {expression}"""
        invocations = []
        for xs in ((), (None,), (1, None, 2), (2, -1), (None, 3, None)):
            for stop in (False, True):
                prefix = xs[:xs.index(None)] if stop and None in xs else xs
                expected = sum((item + delta) * scale for item in prefix if item is not None)
                invocations.append(_value((xs, stop), expected))
        return _case(name + "/optional_loop", "seeded_optional_loop",
                     (("xs", "tuple[int | None, ...]"), ("stop", "bool")), "int",
                     "total = 0\n" + loop + "\nreturn total", invocations, helpers=helpers)
    if variant == 2:
        skip, stop_at = rng.randrange(1, 5), rng.randrange(2, 6)
        break_first = bool(rng.randrange(2))
        use_while = bool(rng.randrange(2))
        skip_block = f"if i == {skip}:\n    continue\n"
        break_block = f"if stop and i >= {stop_at}:\n    break\n"
        blocks = break_block + skip_block if break_first else skip_block + break_block
        if use_while:
            loop = "i = 0\nwhile i < n:\n    i = i + 1\n"
        else:
            loop = "for i in range(1, n + 1):\n"
        body = "total = 0\n" + loop
        body += "\n".join("    " + line for line in (blocks + "total = total + i").splitlines())
        body += "\nreturn total"
        invocations = []
        for n in (-1, 0, 1, 4, 6):
            for stop in (False, True):
                # Each accepted iteration contributes its counter. The exit
                # order controls whether a skipped threshold ends the loop.
                eligible = [i for i in range(1, n + 1) if i != skip]
                threshold = stop_at
                if not break_first and threshold == skip:
                    threshold += 1
                if stop:
                    eligible = [i for i in eligible if i < threshold]
                invocations.append(_value((n, stop), sum(eligible)))
        return _case(name + "/counter", "seeded_counter", (("n", "int"), ("stop", "bool")),
                     "int", body, invocations)
    reverse = bool(rng.randrange(2))
    early = bool(rng.randrange(2))
    threshold = rng.randrange(0, 4)
    guard = f"item >= {threshold}" if reverse else f"item < {threshold}"
    if reverse:
        inner = f"if {guard}:\n    total = total + item\nelse:\n    {'return total' if early else 'break'}"
    else:
        inner = f"if {guard}:\n    {'return total' if early else 'break'}\ntotal = total + item"
    body = "total = 0\nfor row in rows:\n    if len(row) == 0:\n        continue\n    for item in row:\n"
    body += "\n".join("        " + line for line in inner.splitlines()) + "\nreturn total"
    invocations = []
    for rows in ((), ((),), ((4, -1, 7), (5,)), ((0, 3), (), (2, 6)), ((5,), (6, 7))):
        prefixes = [row[:next((i for i, item in enumerate(row) if item < threshold), len(row))]
                    for row in rows]
        if early:
            bad = next((i for i, row in enumerate(rows) if any(item < threshold for item in row)), len(rows))
            prefixes = prefixes[:bad + 1]
        invocations.append(_value((rows,), sum(item for prefix in prefixes for item in prefix)))
    return _case(name + "/nested_exit", "seeded_nested", (("rows", "tuple[tuple[int, ...], ...]"),),
                 "int", body, invocations)


def generate_cases(seed: int = 0, samples: int = 32) -> list[FunctionCase]:
    """Return permanent flow cases plus bounded seeded control-flow compositions.

    Samples vary guard polarity, loop kind and exit ordering, payload type, and
    acyclic helper depth. A local RNG leaves the caller's random state intact.
    """
    if type(seed) is not int or type(samples) is not int:
        raise TypeError("seed and samples must be exact integers")
    if not 0 <= samples <= 256:
        raise ValueError("samples must be between 0 and 256")
    cases = _fixed_cases()
    for payload in _PAYLOADS:
        label = {"tuple[int, ...]": "tuple_int",
                 "tuple[tuple[int, ...], ...]": "nested_tuple_int"}.get(payload[0], payload[0])
        cases.append(_payload_case("payload/" + label, payload, False, 2))
    rng = random.Random(seed)
    cases.extend(_seeded_case(rng, index) for index in range(samples))
    return cases
