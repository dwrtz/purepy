"""Independent, bounded oracle cases for the PurePy 0.1 sealed operation table.

Acceptance expectations come from docs/SYNTAX_MATRIX.md, not the verifier's
implementation or its output.  These are finite development checks, not an
exhaustive proof over Python values.  Expressions and bindings are generated
only from the fixed templates and bounded literal domains below.
"""

from dataclasses import dataclass
from itertools import product
import random


@dataclass(frozen=True)
class Case:
    name: str
    family: str
    expression: str
    expected_type: str | None
    bindings: tuple[tuple[str, str, str], ...] = ()
    exception: str | None = None
    mode: str = "expression"
    expected_value: object = None
    check_value: bool = False


@dataclass(frozen=True)
class _Operand:
    name: str
    annotation: str
    literal: str
    element: str | None = None


_BASE = (
    _Operand("none", "None", "None"),
    _Operand("bool", "bool", "True"),
    _Operand("int", "int", "2"),
    _Operand("float", "float", "2.5"),
    _Operand("str", "str", "'2'"),
    _Operand("bytes", "bytes", "b'2'"),
    _Operand("tuple_int", "tuple[int, ...]", "(2, 3)", "int"),
    _Operand("tuple_bool", "tuple[bool, ...]", "(True, False)", "bool"),
)
_EXTRA_TUPLES = (
    _Operand("tuple_float", "tuple[float, ...]", "(2.5, -3.5)", "float"),
    _Operand("tuple_str", "tuple[str, ...]", "('a', 'b')", "str"),
    _Operand("tuple_bytes", "tuple[bytes, ...]", "(b'a', b'b')", "bytes"),
    _Operand("tuple_none", "tuple[None, ...]", "(None, None)", "None"),
)
_NUMERIC = {"int", "float"}
_ORDERED = {"int", "float", "str", "bytes"}
_SEQUENCES = {"str", "bytes"}


def _sequence(operand: _Operand) -> bool:
    return operand.annotation in _SEQUENCES or operand.element is not None


def generate_cases(seed: int = 0, samples: int = 32) -> list[Case]:
    """Return stable Cartesian type cases plus reproducible bounded values.

    ``samples`` controls only the supplemental random value cases.  Every
    invocation retains the same finite type matrices and curated boundaries.
    A local PRNG never changes global random state.
    """
    if type(seed) is not int or type(samples) is not int:
        raise TypeError("seed and samples must be exact integers")
    if samples < 0 or samples > 256:
        raise ValueError("samples must be between 0 and 256")
    cases: list[Case] = []

    def add(name: str, family: str, expression: str, expected: str | None,
            bindings: tuple[tuple[str, str, str], ...] = (),
            exception: str | None = None, *, mode: str = "expression",
            value: object = None, check_value: bool = False) -> None:
        cases.append(Case(name, family, expression, expected, bindings,
                          exception, mode, value, check_value))

    def bindings(*operands: _Operand) -> tuple[tuple[str, str, str], ...]:
        return tuple((name, operand.annotation, operand.literal)
                     for name, operand in zip(("left", "right", "third"), operands))

    # Complete Cartesian matrices over eight representative exact type shapes.
    # Additional homogeneous tuple element shapes are covered separately below.
    binary = (("add", "+"), ("subtract", "-"), ("multiply", "*"),
              ("divide", "/"), ("floor_divide", "//"), ("modulo", "%"),
              ("power", "**"), ("bit_and", "&"), ("bit_or", "|"),
              ("bit_xor", "^"), ("shift_left", "<<"), ("shift_right", ">>"))
    for (label, op), left, right in product(binary, _BASE, _BASE):
        result = None
        if left.annotation == right.annotation:
            annotation = left.annotation
            if annotation in _NUMERIC and op in {"+", "-", "*", "/", "//", "%"}:
                result = "float" if op == "/" else annotation
            elif annotation == "int" and op in {"&", "|", "^", "<<", ">>"}:
                result = "int"
            elif _sequence(left) and op == "+":
                result = annotation
        add(f"binary/{label}/{left.name}/{right.name}", "binary",
            f"left {op} right", result, bindings(left, right))

    for label, op in (("positive", "+"), ("negative", "-"),
                      ("complement", "~"), ("not", "not ")):
        for operand in _BASE:
            result = None
            if op in {"+", "-"} and operand.annotation in _NUMERIC:
                result = operand.annotation
            elif op == "~" and operand.annotation == "int":
                result = "int"
            elif op == "not " and operand.annotation == "bool":
                result = "bool"
            add(f"unary/{label}/{operand.name}", "unary", f"{op}left", result,
                bindings(operand))

    for op, left, right in product(("and", "or"), _BASE, _BASE):
        result = "bool" if left.annotation == right.annotation == "bool" else None
        add(f"boolean/{op}/{left.name}/{right.name}", "boolean",
            f"left {op} right", result, bindings(left, right))

    comparisons = (("equal", "=="), ("unequal", "!="), ("less", "<"),
                   ("less_equal", "<="), ("greater", ">"),
                   ("greater_equal", ">="), ("in", "in"), ("not_in", "not in"))
    for (label, op), left, right in product(comparisons, _BASE, _BASE):
        if op in {"==", "!="}:
            accepted = left.annotation == right.annotation
        elif op in {"<", "<=", ">", ">="}:
            accepted = left.annotation == right.annotation and left.annotation in _ORDERED
        else:
            accepted = ((right.element == left.annotation) or
                        left.annotation == right.annotation == "str" or
                        left.annotation == "int" and right.annotation == "bytes")
        add(f"comparison/{label}/{left.name}/{right.name}", "comparison",
            f"left {op} right", "bool" if accepted else None, bindings(left, right))

    # Identity checks between nonoptional values and None are deliberately not
    # assigned an oracle here: the prose's "only with None" is less specific
    # than the current optional-only implementation.  Optional flow belongs to
    # the checker suite; this matrix checks None itself and non-None near misses.
    for op, label in (("is", "is"), ("is not", "is_not")):
        for operand in _BASE:
            add(f"identity/{label}/{operand.name}", "comparison",
                f"left {op} left", "bool" if operand.annotation == "None" else None,
                bindings(operand))

    for left, right in product(_BASE, _BASE):
        # The integer representative is zero here to avoid hiding a result type
        # behind an indexing exception in the ordinary type matrix.
        index = _Operand(right.name, right.annotation, "0" if right.annotation == "int" else right.literal,
                         right.element)
        result = None
        if _sequence(left) and right.annotation == "int":
            result = left.element or ("int" if left.annotation == "bytes" else "str")
        add(f"index/{left.name}/{right.name}", "index", "left[right]", result,
            bindings(left, index))

    for sequence in (item for item in _BASE if _sequence(item)):
        for start, stop in product(_BASE[:6], _BASE[:6]):
            accepted = start.annotation in {"int", "None"} and stop.annotation in {"int", "None"}
            add(f"slice/{sequence.name}/{start.name}/{stop.name}", "slice",
                "left[right:third]", sequence.annotation if accepted else None,
                bindings(sequence, start, stop))
        add(f"slice/{sequence.name}/omitted", "slice", "left[:]", sequence.annotation,
            bindings(sequence))
        for label, suffix in (("step_one", "[::1]"), ("step_none", "[::None]"),
                              ("reverse", "[::-1]"), ("multi_index", "[0, 1]")):
            add(f"slice/{sequence.name}/{label}", "slice", "left" + suffix, None,
                bindings(sequence))

    for operand in _BASE:
        for label, suffix in (("plain", ""), ("empty_spec", ":")):
            accepted = operand.annotation in {"bool", "int", "float", "str"}
            add(f"format/{label}/{operand.name}", "format", 'f"value={left' + suffix + '}"',
                "str" if accepted else None, bindings(operand))
    for label, expression in (("repr", 'f"{2!r}"'), ("str_conversion", 'f"{2!s}"'),
                              ("ascii_conversion", 'f"{2!a}"'), ("debug", 'f"{2=}"'),
                              ("width", 'f"{2:04}"'), ("dynamic", 'f"{2:{3}}"')):
        add(f"format/reject/{label}", "format", expression, None)

    for left, right in product(_BASE, _BASE):
        add(f"conditional/branches/{left.name}/{right.name}", "conditional",
            "left if True else right", left.annotation if left.annotation == right.annotation else None,
            bindings(left, right))
    for operand in _BASE:
        add(f"conditional/condition/{operand.name}", "conditional", "2 if left else 3",
            "int" if operand.annotation == "bool" else None, bindings(operand))

    all_types = _BASE + _EXTRA_TUPLES
    unary_intrinsics = ("len", "abs", "min", "max", "sum", "all", "any",
                        "int", "float", "str", "bytes")
    for name, operand in product(unary_intrinsics, all_types):
        result = None
        annotation = operand.annotation
        if name == "len" and _sequence(operand):
            result = "int"
        elif name == "abs" and annotation in _NUMERIC:
            result = annotation
        elif name in {"min", "max"} and operand.element in _ORDERED:
            result = operand.element
        elif name == "sum" and operand.element == "int":
            result = "int"
        elif name in {"all", "any"} and operand.element == "bool":
            result = "bool"
        elif name in {"int", "float"} and annotation in {"bool", "int", "float", "str", "bytes"}:
            result = name
        elif name == "str" and annotation in {"None", "bool", "int", "float", "str"}:
            result = "str"
        elif name == "bytes" and (annotation == "bytes" or operand.element == "int"):
            result = "bytes"
        add(f"intrinsic/{name}/one/{operand.name}", "intrinsic", f"{name}(left)", result,
            bindings(operand))

    for name, left, right in product(("min", "max"), _BASE, _BASE):
        accepted = left.annotation == right.annotation and left.annotation in _ORDERED
        add(f"intrinsic/{name}/two/{left.name}/{right.name}", "intrinsic",
            f"{name}(left, right)", left.annotation if accepted else None, bindings(left, right))
    for name, operand in product(("min", "max"), _BASE):
        add(f"intrinsic/{name}/three/{operand.name}", "intrinsic", f"{name}(left, left, left)",
            operand.annotation if operand.annotation in _ORDERED else None, bindings(operand))
    for left, right in product(all_types, _BASE[:6]):
        accepted = left.element == right.annotation and right.annotation in _NUMERIC
        add(f"intrinsic/sum/two/{left.name}/{right.name}", "intrinsic", "sum(left, right)",
            right.annotation if accepted else None, bindings(left, right))

    # Arity and keyword variants are rejected even where CPython offers them.
    for name in unary_intrinsics:
        add(f"intrinsic/{name}/zero", "intrinsic", f"{name}()", None)
        add(f"intrinsic/{name}/keyword", "intrinsic", f"{name}(value=2)", None)
        if name not in {"min", "max", "sum"}:
            add(f"intrinsic/{name}/two_rejected", "intrinsic", f"{name}(2, 3)", None)
    for label, expression in (("sum_three", "sum((1, 2), 0, 0)"),
                              ("min_default", "min((1, 2), default=0)"),
                              ("max_default", "max((1, 2), default=0)"),
                              ("sum_start_keyword", "sum((1, 2), start=0)"),
                              ("min_unpack", "min(*(1, 2))"),
                              ("int_base", "int('10', 2)"),
                              ("bytes_count", "bytes(3)"),
                              ("bytes_encoding", "bytes('a', 'utf-8')"),
                              ("str_decode", "str(b'a', 'utf-8')")):
        add(f"intrinsic/reject/{label}", "intrinsic", expression, None)

    for operand in _BASE:
        add(f"range/one/{operand.name}", "range", "range(left)",
            "int" if operand.annotation == "int" else None, bindings(operand), mode="range")
    for left, right in product(_BASE, _BASE):
        add(f"range/two/{left.name}/{right.name}", "range", "range(left, right)",
            "int" if left.annotation == right.annotation == "int" else None,
            bindings(left, right), mode="range")
    for position, operand in product(range(3), _BASE):
        arguments = ["0", "4", "1"]
        arguments[position] = "left"
        add(f"range/three/position_{position}/{operand.name}", "range",
            "range(" + ", ".join(arguments) + ")",
            "int" if operand.annotation == "int" else None, bindings(operand), mode="range")
    for label, expression in (("zero_arguments", "range()"), ("four_arguments", "range(0, 3, 1, 1)"),
                              ("keyword", "range(stop=3)")):
        add(f"range/reject/{label}", "range", expression, None, mode="range")
    add("range/reject/first_class", "range", "range(3)", None)

    # Empty tuples receive their element type from explicit local annotations.
    for element in ("None", "bool", "int", "float", "str", "bytes"):
        empty = (("items", f"tuple[{element}, ...]", "()"),)
        add(f"empty/{element}/len", "intrinsic", "len(items)", "int", empty,
            value=0, check_value=True)
        add(f"empty/{element}/slice", "slice", "items[:1]", f"tuple[{element}, ...]", empty,
            value=(), check_value=True)
        add(f"empty/{element}/index", "index", "items[0]", element, empty, "IndexError")
        for name in ("min", "max"):
            accepted = element in _ORDERED
            add(f"empty/{element}/{name}", "intrinsic", f"{name}(items)",
                element if accepted else None, empty, "ValueError" if accepted else None)
        if element in _NUMERIC:
            start = "0" if element == "int" else "0.0"
            add(f"empty/{element}/sum_start", "intrinsic", f"sum(items, {start})", element,
                empty, value=0 if element == "int" else 0.0, check_value=True)
        if element == "bool":
            for name, value in (("all", True), ("any", False)):
                add(f"empty/bool/{name}", "intrinsic", f"{name}(items)", "bool", empty,
                    value=value, check_value=True)
        if element == "int":
            add("empty/int/sum", "intrinsic", "sum(items)", "int", empty, value=0, check_value=True)
            add("empty/int/bytes", "intrinsic", "bytes(items)", "bytes", empty,
                value=b"", check_value=True)
    add("empty/reject/uncontextualized_len", "intrinsic", "len(())", None)

    for operand in _EXTRA_TUPLES:
        add(f"tuple/{operand.name}/concat", "binary", "left + left", operand.annotation,
            bindings(operand))
        add(f"tuple/{operand.name}/equal", "comparison", "left == left", "bool", bindings(operand))
        add(f"tuple/{operand.name}/index", "index", "left[0]", operand.element, bindings(operand))

    # Curated values distinguish exact bool/int results, signs, failure domains,
    # Unicode code points, byte values and CPython's float edge behavior.
    values = (
        ("negative_floor", "binary", "-7 // 3", "int", -3),
        ("negative_modulo", "binary", "-7 % 3", "int", 2),
        ("negative_divisor", "binary", "7 % -3", "int", -2),
        ("float_floor", "binary", "-7.0 // 3.0", "float", -3.0),
        ("float_modulo", "binary", "-7.0 % 3.0", "float", 2.0),
        ("negative_zero_divide", "binary", "0.0 / -2.0", "float", -0.0),
        ("negative_zero_abs", "intrinsic", "abs(-0.0)", "float", 0.0),
        ("negative_zero_float", "intrinsic", "float('-0')", "float", -0.0),
        ("negative_zero_str", "intrinsic", "str(-0.0)", "str", "-0.0"),
        ("negative_zero_format", "format", 'f"{-0.0}"', "str", "-0.0"),
        ("bool_to_int", "intrinsic", "int(True)", "int", 1),
        ("bool_to_float", "intrinsic", "float(False)", "float", 0.0),
        ("none_to_str", "intrinsic", "str(None)", "str", "None"),
        ("byte_boundaries", "intrinsic", "bytes((0, 127, 128, 255))", "bytes", b"\x00\x7f\x80\xff"),
        ("byte_index", "index", "b'\\xff'[0]", "int", 255),
        ("unicode_index", "index", "'aé😀'[-1]", "str", "😀"),
        ("unicode_len", "intrinsic", "len('aé😀')", "int", 3),
        ("unicode_slice", "slice", "'aé😀'[-2:]", "str", "é😀"),
        ("slice_clamp", "slice", "(1, 2, 3)[-99:99]", "tuple[int, ...]", (1, 2, 3)),
        ("slice_empty", "slice", "b'abc'[2:1]", "bytes", b""),
        ("short_circuit_and", "boolean", "False and (1 // 0 == 0)", "bool", False),
        ("short_circuit_or", "boolean", "True or (1 // 0 == 0)", "bool", True),
        ("chain", "comparison", "1 < 2 <= 2 != 3", "bool", True),
        ("chain_short_circuit", "comparison", "3 < 2 < 1 // 0", "bool", False),
        ("float_sum", "intrinsic", "sum((1.0, 2.0), 0.0)", "float", 3.0),
        ("numeric_bytes_int", "intrinsic", "int(b' -12 ')", "int", -12),
        ("numeric_bytes_float", "intrinsic", "float(b'1.25')", "float", 1.25),
        ("int_truncation", "intrinsic", "int(-2.75)", "int", -2),
    )
    for label, family, expression, result, value in values:
        add(f"edge/value/{label}", family, expression, result, value=value, check_value=True)

    # Python 3.14 uses Unicode 16.0.0 names. Literal escape lookup accepts
    # single-character aliases and ASCII case variants, including algorithmic
    # names; named sequences and malformed escapes belong to syntax_edges.json
    # because the runtime gate only evaluates valid, constrained Python ASTs.
    named_characters = (
        ("canonical", "LATIN CAPITAL LETTER A", "A"),
        ("lowercase", "latin small letter a with acute", "á"),
        ("mixed_case", "Latin Capital Letter A", "A"),
        ("control_alias", "NULL", "\x00"),
        ("abbreviation_alias", "nul", "\x00"),
        ("line_feed_alias", "LF", "\n"),
        ("alternate_alias", "BYTE ORDER MARK", "\ufeff"),
        ("correction_alias", "LATIN CAPITAL LETTER GHA", "Ƣ"),
        ("hangul_first", "HANGUL SYLLABLE GA", "가"),
        ("hangul_last", "hangul syllable hih", "힣"),
        ("cjk_lowercase", "cjk unified ideograph-4e00", "一"),
        ("cjk_supplementary", "CJK UNIFIED IDEOGRAPH-2EBF0", "\U0002ebf0"),
        ("cjk_range_end", "CJK UNIFIED IDEOGRAPH-2EBE0", "\U0002ebe0"),
        ("cjk_compatibility", "CJK COMPATIBILITY IDEOGRAPH-F900", "\uf900"),
        ("tangut", "TANGUT IDEOGRAPH-17000", "\U00017000"),
        ("khitan", "KHITAN SMALL SCRIPT CHARACTER-18B00", "\U00018b00"),
        ("nushu", "NUSHU CHARACTER-1B170", "\U0001b170"),
        ("variation_selector", "VARIATION SELECTOR-256", "\U000e01ef"),
    )
    for label, name, value in named_characters:
        add(f"literal/named/{label}", "literal", '"\\N{' + name + '}"', "str",
            value=value, check_value=True)
    named_forms = (
        ("unicode_prefix", r'''u"\N{LATIN CAPITAL LETTER A}"''', "str", "A"),
        ("uppercase_prefix", r'''U"\N{LATIN CAPITAL LETTER A}"''', "str", "A"),
        ("triple_quote", r'''"""\N{LATIN CAPITAL LETTER A}"""''', "str", "A"),
        ("adjacent", r'''"\N{LATIN CAPITAL LETTER A}" "\N{LATIN CAPITAL LETTER B}"''', "str", "AB"),
        ("mixed_escapes", r'''"\N{LATIN CAPITAL LETTER A}\u0042\x43\N{LF}"''', "str", "ABC\n"),
        ("escaped_backslash", r'''"\\N{UNKNOWN}"''', "str", r"\N{UNKNOWN}"),
        ("raw_unknown", r'''r"\N{UNKNOWN}"''', "str", r"\N{UNKNOWN}"),
        ("raw_empty", r'''r"\N{}"''', "str", r"\N{}"),
        ("raw_incomplete", r'''R"\N{"''', "str", r"\N{"),
        ("bytes_unknown", r'''b"\N{UNKNOWN}"''', "bytes", br"\N{UNKNOWN}"),
        ("bytes_empty", r'''b"\N{}"''', "bytes", br"\N{}"),
        ("bytes_consecutive", r'''b"\N{UNKNOWN}\N{}\N"''', "bytes", br"\N{UNKNOWN}\N{}\N"),
        ("bytes_consecutive_incomplete", r'''b"\N{UNKNOWN}\N{\N"''', "bytes", br"\N{UNKNOWN}\N{\N"),
        ("bytes_consecutive_no_braces", r'''b"\N\N{UNKNOWN}\N"''', "bytes", br"\N\N{UNKNOWN}\N"),
        ("bytes_embedded_newline_escape", r'''b"\N{a\nb}\N"''', "bytes", b"\\N{a\nb}\\N"),
        ("bytes_empty_then_hex", r'''b"\N{}\x41"''', "bytes", br"\N{}A"),
        ("bytes_comment_character", r"""b'#\N'""", "bytes", br"#\N"),
        ("bytes_leading_space", r"""b' \N'""", "bytes", br" \N"),
        ("bytes_trailing_backslash", r"""b'\N\\'""", "bytes", b"\\N\\"),
        ("bytes_incomplete", r'''B"\N{"''', "bytes", br"\N{"),
        ("raw_bytes", r'''rb"\N{LATIN CAPITAL LETTER A}"''', "bytes", br"\N{LATIN CAPITAL LETTER A}"),
        ("raw_bytes_empty", r'''rb"\N{}"''', "bytes", br"\N{}"),
        ("bytes_triple_empty", r'''b"""\N{}"""''', "bytes", br"\N{}"),
        ("bytes_multiline", 'b"""\\N{}\n\\N{}"""', "bytes", b"\\N{}\n\\N{}"),
        ("bytes_multiline_name", 'b"""\\N{\n}"""', "bytes", b"\\N{\n}"),
        ("raw_multiline", 'r"""\\N{}\n\\N{}"""', "str", "\\N{}\n\\N{}"),
        ("raw_bytes_multiline", 'rb"""\\N{}\n\\N{}"""', "bytes", b"\\N{}\n\\N{}"),
        ("formatted", r'''f"\N{LATIN CAPITAL LETTER A}{2}"''', "str", "A2"),
        ("formatted_braces", r'''f"\N{LEFT CURLY BRACKET}{3}\N{RIGHT CURLY BRACKET}"''', "str", "{3}"),
        ("formatted_escaped_braces", r'''f"\N{LATIN CAPITAL LETTER A}{{literal}}{2}"''', "str", "A{literal}2"),
        ("formatted_expression", r'''f"{'\N{LATIN CAPITAL LETTER A}'}"''', "str", "A"),
        ("formatted_bytes_expression", r'''f"{len(b'\N{UNKNOWN}\N{}\N')}"''', "str", "17"),
        ("formatted_backspace_before_quote", r'''f"\b'{2}"''', "str", "\b'2"),
        ("formatted_adjacent", r'''f"\N{LATIN CAPITAL LETTER A}{2}" "\N{LATIN CAPITAL LETTER B}"''', "str", "A2B"),
        ("raw_formatted", r'''rf"\N{{UNKNOWN}}{2}"''', "str", r"\N{UNKNOWN}2"),
        ("raw_formatted_empty", r'''rf"\N{{}}"''', "str", r"\N{}"),
        ("raw_formatted_braces", r'''rf"\N{2}"''', "str", r"\N2"),
    )
    for label, expression, result, value in named_forms:
        add(f"literal/named/form/{label}", "literal", expression, result,
            value=value, check_value=True)

    failures = (
        ("int_divide_zero", "binary", "1 / 0", "float", "ZeroDivisionError"),
        ("int_floor_zero", "binary", "1 // 0", "int", "ZeroDivisionError"),
        ("int_modulo_zero", "binary", "1 % 0", "int", "ZeroDivisionError"),
        ("float_divide_zero", "binary", "1.0 / -0.0", "float", "ZeroDivisionError"),
        ("float_floor_zero", "binary", "1.0 // 0.0", "float", "ZeroDivisionError"),
        ("float_modulo_zero", "binary", "1.0 % 0.0", "float", "ZeroDivisionError"),
        ("negative_shift_left", "binary", "1 << -1", "int", "ValueError"),
        ("negative_shift_right", "binary", "1 >> -1", "int", "ValueError"),
        ("invalid_int", "intrinsic", "int('two')", "int", "ValueError"),
        ("invalid_float", "intrinsic", "float(b'two')", "float", "ValueError"),
        ("int_infinity", "intrinsic", "int(float('inf'))", "int", "OverflowError"),
        ("int_nan", "intrinsic", "int(float('nan'))", "int", "ValueError"),
        ("bytes_negative", "intrinsic", "bytes((-1,))", "bytes", "ValueError"),
        ("bytes_too_large", "intrinsic", "bytes((256,))", "bytes", "ValueError"),
        ("membership_negative", "comparison", "-1 in b'a'", "bool", "ValueError"),
        ("membership_too_large", "comparison", "256 not in b'a'", "bool", "ValueError"),
        ("str_index", "index", "'a'[1]", "str", "IndexError"),
        ("bytes_index", "index", "b'a'[-2]", "int", "IndexError"),
        ("tuple_index", "index", "(True,)[9]", "bool", "IndexError"),
    )
    for label, family, expression, result, exception in failures:
        add(f"edge/failure/{label}", family, expression, result, exception=exception)

    for spelling in ("nan", "inf", "-inf"):
        for label, expression in (("convert", f"float('{spelling}')"),
                                  ("abs", f"abs(float('{spelling}'))"),
                                  ("add", f"float('{spelling}') + 1.0"),
                                  ("min", f"min(float('{spelling}'), 1.0)"),
                                  ("max", f"max(1.0, float('{spelling}'))"),
                                  ("sum", f"sum((float('{spelling}'), 1.0), 0.0)")):
            add(f"edge/float/{spelling}/{label}", "intrinsic" if label != "add" else "binary",
                expression, "float")
    add("edge/float/nan/equal", "comparison", "float('nan') == float('nan')", "bool",
        value=False, check_value=True)
    add("edge/float/nan/less", "comparison", "float('nan') < 1.0", "bool",
        value=False, check_value=True)
    add("edge/float/overflow_multiply", "binary", "1e308 * 1e308", "float")
    add("edge/float/overflow_literal", "unary", "1e400", "float")
    huge = 10 ** 400
    add("edge/huge/int_add", "binary", f"{huge} + 1", "int", value=huge + 1, check_value=True)
    add("edge/huge/int_float_overflow", "intrinsic", f"float({huge})", "float", exception="OverflowError")
    add("edge/huge/int_divide_overflow", "binary", f"{huge} / 1", "float", exception="OverflowError")
    add("edge/huge/int_to_str", "intrinsic", f"str({huge})", "str", value=str(huge), check_value=True)
    for label, expression, value in (("ascending", "range(-3, 4, 2)", (-3, -1, 1, 3)),
                                    ("descending", "range(3, -4, -2)", (3, 1, -1, -3)),
                                    ("empty", "range(0)", ()),
                                    ("wrong_direction", "range(1, 3, -1)", ())):
        add(f"range/edge/{label}", "range", expression, "int", mode="range", value=value, check_value=True)
    add("range/edge/zero_step", "range", "range(1, 3, 0)", "int", exception="ValueError", mode="range")

    for label, expression in (("matrix_multiply", "2 @ 3"),
                              ("negative_power", "2 ** -1"),
                              ("complex_power", "(-1.0) ** 0.5"),
                              ("float_tuple_sum_no_start", "sum((1.0, 2.0))"),
                              ("bool_tuple_sum", "sum((True, False))"),
                              ("mixed_numeric_chain", "1 < 2 < 3.0"),
                              ("dead_boolean_operand", "False and 1")):
        add(f"edge/reject/{label}", "intrinsic" if "sum" in label else "binary", expression, None)

    rng = random.Random(seed)
    for index in range(samples):
        left = rng.randint(-(1 << 80), 1 << 80)
        right = rng.randint(-4096, 4096)
        divisor = right or 1
        shift = rng.randrange(0, 64)
        add(f"seeded/{index:03}/floor", "binary", f"{left} // {divisor}", "int",
            value=left // divisor, check_value=True)
        add(f"seeded/{index:03}/modulo", "binary", f"{left} % {divisor}", "int",
            value=left % divisor, check_value=True)
        add(f"seeded/{index:03}/shift", "binary", f"{left} << {shift}", "int",
            value=left << shift, check_value=True)

    names = [case.name for case in cases]
    if len(set(names)) != len(names):
        raise AssertionError("duplicate semantic case names")
    return cases
