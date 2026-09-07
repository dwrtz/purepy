package check

import (
	"strings"
	"testing"

	"github.com/dwrtz/purepy/internal/diag"
)

// Each case names the language rule it exercises. Rejection cases assert the
// responsible diagnostic when it is a semantic rule, rather than accepting an
// unrelated parsing failure as conformance evidence.
func specCoreCheck(t *testing.T, source, code string) {
	t.Helper()
	ds := securityVerify(t, map[string]string{"app": source}, nil)
	if code == "" {
		if len(ds) > 0 {
			t.Fatalf("valid source rejected: %v\n%s", ds, source)
		}
		return
	}
	for _, d := range ds {
		if d.Code == code {
			return
		}
	}
	t.Fatalf("expected %s, got %v\n%s", code, ds, source)
}

func TestSpecCoreAccepted(t *testing.T) {
	cases := map[string]string{
		"annotated_primitives":                 "def f(n: None, b: bool, i: int, real: float, s: str, data: bytes) -> bytes:\n    return data\n",
		"optional_argument_and_return":         "def g(x: int | None) -> int | None:\n    return x\ndef f() -> int | None:\n    first = g(1)\n    return g(None)\n",
		"local_annotation_then_assignment":     "def f() -> int:\n    x: int\n    x = 1\n    x = x + 1\n    return x\n",
		"both_branches_assign":                 "def f(flag: bool) -> int:\n    if flag:\n        x = 1\n    else:\n        x = 2\n    return x\n",
		"returning_branch_same_local_type":     "def f(flag: bool) -> int:\n    if flag:\n        x = 1\n        return x\n    x = 2\n    return x\n",
		"loop_keeps_preassigned_local":         "def f(xs: tuple[int, ...]) -> int:\n    result = 0\n    for x in xs:\n        result = x\n    return result\n",
		"narrowing_reversed_none":              "def f(x: int | None) -> int:\n    if None is not x:\n        return x\n    return 0\n",
		"narrowing_not_guard":                  "def f(x: int | None) -> int:\n    if not (x is None):\n        return x\n    return 0\n",
		"narrowing_conditional":                "def f(x: int | None) -> int:\n    return x if x is not None else 0\n",
		"none_returns_and_fallthrough":         "def a() -> None:\n    return\ndef b() -> None:\n    return None\ndef c() -> None:\n    pass\n",
		"direct_none_statements":               "def g() -> None:\n    return\nasync def h() -> None:\n    return\nasync def f() -> None:\n    g()\n    await h()\n",
		"record_fields_and_top_level_behavior": "from typing import NamedTuple\n\nclass Inner(NamedTuple):\n    number: int\n\nclass Record(NamedTuple):\n    \"\"\"Immutable payload.\"\"\"\n    inner: Inner\n    values: tuple[str, ...]\n    maybe: int | None\ndef f(item: Record) -> Record:\n    return Record(Inner(item.inner.number), maybe=None, values=item.values)\n",
		"same_record_equality":                 "from typing import NamedTuple\n\nclass Record(NamedTuple):\n    values: tuple[int, ...]\ndef f(a: Record, b: Record) -> bool:\n    return a == b or a != b\n",
		"module_docstring_and_constant_forms":  "\"\"\"Static constants.\"\"\"\nfrom typing import Final\nfrom typing import NamedTuple\n\nclass Record(NamedTuple):\n    value: int\nA: Final[int] = 1\nB: Final[int] = A\nC: Final[tuple[int, ...]] = (A, B)\nD: Final[Record] = Record(value=A)\ndef f() -> int:\n    return D.value\n",
		"range_forms":                          "def f() -> int:\n    total = 0\n    for x in range(3):\n        total = total + x\n    for y in range(1, 3):\n        total = total + y\n    for z in range(1, 5, 2):\n        total = total + z\n    return total\n",
		"iterable_element_types":               "def f(text: str, data: bytes, xs: tuple[float, ...]) -> float:\n    total = 0.0\n    for char in text:\n        size: int = len(char)\n    for byte in data:\n        total = total + float(byte)\n    for x in xs:\n        total = total + x\n    return total\n",
		"tuple_nested_context":                 "def g(xs: tuple[int, ...]) -> tuple[int, ...]:\n    return xs\ndef f() -> tuple[tuple[int, ...], ...]:\n    return (g(()), ())\n",
		"sequences_and_membership":             "def f(text: str, data: bytes, xs: tuple[int, ...]) -> bool:\n    joined = xs + (1,)\n    return 'x' in text and 1 not in data or 1 in joined\n",
		"optional_slice_bounds":                "def f(xs: tuple[int, ...], start: int | None, stop: int | None) -> tuple[int, ...]:\n    return xs[start:stop]\n",
		"primitive_formatting":                 "def f(b: bool, n: int, x: float, text: str) -> str:\n    return f'{b} {n} {x} {text}'\n",
		"explicit_numeric_conversions":         "def f(x: int, y: float) -> float:\n    return float(x) + y\n",
		"primitive_intrinsics":                 "def f(xs: tuple[int, ...], flags: tuple[bool, ...]) -> str:\n    count = len(xs)\n    total = sum(xs)\n    low = min(xs)\n    high = max(1, 2)\n    yes = all(flags) and any(flags)\n    data = bytes(xs)\n    return str(abs(total))\n",
		"float_sum_explicit_start":             "def f(xs: tuple[float, ...]) -> float:\n    return sum(xs, 0.0)\n",
		"direct_recursive_call":                "def f(n: int) -> int:\n    if n == 0:\n        return 0\n    return f(n - 1)\n",
	}
	for name, source := range cases {
		t.Run(name, func(t *testing.T) { specCoreCheck(t, source, "") })
	}
}

func TestSpecCoreRejected(t *testing.T) {
	cases := map[string]struct{ source, code string }{
		"missing_parameter_annotation":        {"def f(x) -> int:\n    return 1\n", "PP203"},
		"missing_return_annotation":           {"def f(x: int):\n    return x\n", "PP203"},
		"missing_final_annotation":            {"X: int = 1\n", "PP501"},
		"final_without_initializer":           {"from typing import Final\nX: Final[int]\n", "PP501"},
		"local_final":                         {"from typing import Final\ndef f() -> int:\n    x: Final[int] = 1\n    return x\n", "PP203"},
		"unknown_local_first_use":             {"def f() -> int:\n    return unknown\n", "PP104"},
		"unassigned_annotated_local":          {"def f() -> int:\n    x: int\n    return x\n", "PP206"},
		"assignment_before_local_definition":  {"def f() -> int:\n    y = x\n    x = 1\n    return y\n", "PP206"},
		"local_annotation_changes_type":       {"def f() -> int:\n    x = 1\n    x: str = 'changed'\n    return 0\n", "PP205"},
		"returning_branch_changes_local_type": {"def f(flag: bool) -> str:\n    if flag:\n        x = 1\n        return 'early'\n    x = 'later'\n    return x\n", "PP205"},
		"loop_changes_local_type":             {"def f(xs: tuple[int, ...]) -> int:\n    x = 1\n    for item in xs:\n        x = 'changed'\n    return 0\n", "PP205"},
		"optional_not_narrowed":               {"def f(x: int | None) -> int:\n    return x + 1\n", "PP209"},
		"optional_narrowing_does_not_escape":  {"def f(x: int | None) -> int:\n    if x is not None:\n        pass\n    return x\n", "PP205"},
		"exact_return_type":                   {"def f() -> int:\n    return True\n", "PP205"},
		"non_none_fallthrough":                {"def f(flag: bool) -> int:\n    if flag:\n        return 1\n", "PP213"},
		"wrong_none_return":                   {"def f() -> None:\n    return 1\n", "PP205"},
		"discard_non_none_call":               {"def g() -> int:\n    return 1\ndef f() -> None:\n    g()\n", "PP205"},
		"discard_non_none_await":              {"async def g() -> int:\n    return 1\nasync def f() -> None:\n    await g()\n", "PP205"},
		"bare_expression_statement":           {"def f() -> None:\n    1\n", "PP003"},
		"truthy_string_if":                    {"def f(s: str) -> None:\n    if s:\n        return\n", "PP205"},
		"truthy_integer_while":                {"def f(n: int) -> None:\n    while n:\n        return\n", "PP205"},
		"truthy_tuple_conditional":            {"def f(xs: tuple[int, ...]) -> int:\n    return 1 if xs else 0\n", "PP205"},
		"value_returning_and":                 {"def f(x: int) -> bool:\n    return x and True\n", "PP205"},
		"value_returning_or":                  {"def f(x: str) -> bool:\n    return x or False\n", "PP205"},
		"non_bool_not":                        {"def f(x: bytes) -> bool:\n    return not x\n", "PP205"},
		"conditional_branch_type_conflict":    {"def f(flag: bool) -> int:\n    return 1 if flag else True\n", "PP205"},
		"empty_tuple_no_context":              {"def f() -> None:\n    xs = ()\n", "PP207"},
		"heterogeneous_tuple":                 {"def f() -> None:\n    xs = (1, True)\n", "PP207"},
		"mixed_numeric_arithmetic":            {"def f(x: int, y: float) -> float:\n    return x + y\n", "PP209"},
		"power_has_no_fixed_result_type":      {"def f(x: int, y: int) -> int:\n    return x ** y\n", "PP209"},
		"identity_exposes_object_identity":    {"def f(x: str, y: str) -> bool:\n    return x is y\n", "PP212"},
		"bool_is_not_integer_index":           {"def f(xs: tuple[int, ...], x: bool) -> int:\n    return xs[x]\n", "PP205"},
		"slice_bound_float":                   {"def f(xs: str) -> str:\n    return xs[1.0:]\n", "PP205"},
		"extended_slice":                      {"def f(xs: str) -> str:\n    return xs[::2]\n", "PP003"},
		"unknown_instance_field":              {"def f(s: str) -> int:\n    return s.real\n", "PP208"},
		"membership_wrong_element_type":       {"def f(xs: tuple[int, ...]) -> bool:\n    return True in xs\n", "PP212"},
		"record_field_missing":                {"from typing import NamedTuple\n\nclass R(NamedTuple):\n    x: int\ndef f(r: R) -> int:\n    return r.y\n", "PP208"},
		"record_nominal_type_mismatch":        {"from typing import NamedTuple\n\nclass A(NamedTuple):\n    x: int\n\nclass B(NamedTuple):\n    x: int\ndef f(a: A) -> B:\n    return a\n", "PP205"},
		"record_field_default":                {"from typing import NamedTuple\n\nclass R(NamedTuple):\n    x: int = 1\n", "PP202"},
		"record_method":                       {"from typing import NamedTuple\n\nclass R(NamedTuple):\n    x: int\n    def method(self: R) -> int:\n        return self.x\n", "PP202"},
		"record_inheritance":                  {"from typing import NamedTuple\n\nclass A(NamedTuple):\n    x: int\n\nclass B(A):\n    y: int\n", "PP202"},
		"record_class_options":                {"from typing import NamedTuple\n\nclass R(metaclass=int):\n    x: int\n", "PP003"},
		"record_non_field_statement":          {"from typing import NamedTuple\n\nclass R(NamedTuple):\n    pass\n", "PP202"},
		"record_nested_class":                 {"from typing import NamedTuple\n\nclass R(NamedTuple):\n    class Nested:\n        x: int\n", "PP202"},
		"record_class_constant":               {"from typing import NamedTuple\n\nclass R(NamedTuple):\n    x = 1\n", "PP202"},
		"record_missing_decorator":            {"class R:\n    x: int\n", "PP202"},
		"record_extra_decorator":              {"from typing import NamedTuple\n@value\n\nclass R(NamedTuple):\n    x: int\n", "PP202"},
		"record_missing_argument":             {"from typing import NamedTuple\n\nclass R(NamedTuple):\n    x: int\ndef f() -> R:\n    return R()\n", "PP302"},
		"record_wrong_field_type":             {"from typing import NamedTuple\n\nclass R(NamedTuple):\n    x: int\ndef f() -> R:\n    return R(True)\n", "PP205"},
		"record_unknown_keyword":              {"from typing import NamedTuple\n\nclass R(NamedTuple):\n    x: int\ndef f() -> R:\n    return R(other=1)\n", "PP302"},
		"record_duplicate_argument":           {"from typing import NamedTuple\n\nclass R(NamedTuple):\n    x: int\ndef f() -> R:\n    return R(1, x=2)\n", "PP302"},
		"record_argument_unpacking":           {"from typing import NamedTuple\n\nclass R(NamedTuple):\n    x: int\ndef f(xs: tuple[int, ...]) -> R:\n    return R(*xs)\n", "PP003"},
		"expanding_recursive_record":          {"from typing import NamedTuple\n\nclass R[T](NamedTuple):\n    items: R[tuple[T, ...]] | None\n", "PP204"},
		"constant_ordinary_call":              {"from typing import Final\ndef f() -> int:\n    return 1\nX: Final[int] = f()\n", "PP502"},
		"constant_intrinsic_call":             {"from typing import Final\nX: Final[int] = len('x')\n", "PP502"},
		"constant_later_reference":            {"from typing import Final\nX: Final[int] = Y\nY: Final[int] = 1\n", "PP502"},
		"constant_computed_expression":        {"from typing import Final\nX: Final[int] = 1 + 2\n", "PP502"},
		"constant_rebinding":                  {"from typing import Final\nX: Final[int] = 1\nX: Final[int] = 2\n", "PP103"},
		"constant_local_rebinding":            {"from typing import Final\nX: Final[int] = 1\ndef f() -> None:\n    X = 2\n", "PP503"},
		"script_bootstrap":                    {"if __name__ == '__main__':\n    pass\n", "PP003"},
		"unknown_import":                      {"from missing.module import run\ndef f() -> None:\n    run()\n", "PP102"},
		"duplicate_callable":                  {"def f() -> int:\n    return 1\ndef f() -> int:\n    return 2\n", "PP103"},
		"exact_argument_type":                 {"def g(x: int) -> int:\n    return x\ndef f() -> int:\n    return g(True)\n", "PP205"},
		"dynamic_call_target":                 {"def a() -> int:\n    return 1\ndef b() -> int:\n    return 2\ndef f(flag: bool) -> int:\n    return (a if flag else b)()\n", "PP301"},
		"function_metadata":                   {"def f() -> str:\n    return f.__name__\n", "PP104"},
		"range_stored":                        {"def f() -> None:\n    xs = range(3)\n", "PP304"},
		"range_forwarded":                     {"def g(xs: tuple[int, ...]) -> None:\n    return\ndef f() -> None:\n    g(range(3))\n", "PP304"},
		"range_non_int_bound":                 {"def f() -> None:\n    for x in range(True):\n        pass\n", "PP205"},
		"for_unapproved_iterable":             {"def f() -> None:\n    for x in 1:\n        pass\n", "PP214"},
		"for_else":                            {"def f() -> None:\n    for x in range(1):\n        pass\n    else:\n        pass\n", "PP003"},
		"while_else":                          {"def f(flag: bool) -> None:\n    while flag:\n        pass\n    else:\n        pass\n", "PP003"},
		"format_raw_bytes":                    {"def f(data: bytes) -> str:\n    return f'{data}'\n", "PP211"},
		"format_repr":                         {"def f(x: int) -> str:\n    return f'{x!r}'\n", "PP003"},
		"format_locale":                       {"def f(x: int) -> str:\n    return f'{x:n}'\n", "PP211"},
		"format_dynamic_spec":                 {"def f(x: int, width: int) -> str:\n    return f'{x:{width}}'\n", "PP003"},
		"intrinsic_no_protocol_dispatch":      {"from typing import NamedTuple\n\nclass R(NamedTuple):\n    x: int\ndef f(r: R) -> int:\n    return len(r)\n", "PP303"},
		"float_sum_requires_start":            {"def f(xs: tuple[float, ...]) -> float:\n    return sum(xs)\n", "PP303"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) { specCoreCheck(t, tc.source, tc.code) })
	}
}

func TestSpecCoreProhibitedSyntax(t *testing.T) {
	cases := map[string]string{
		"default_parameter":        "def f(x: int = 1) -> int:\n    return x\n",
		"positional_only":          "def f(x: int, /) -> int:\n    return x\n",
		"keyword_only":             "def f(*, x: int) -> int:\n    return x\n",
		"variadic_positional":      "def f(*xs: int) -> int:\n    return 0\n",
		"variadic_keyword":         "def f(**xs: int) -> int:\n    return 0\n",
		"bounded_generic_function": "def f[T: int](x: T) -> T:\n    return x\n",
		"function_decorator":       "def marker() -> None:\n    return\n@marker\ndef f() -> None:\n    return\n",
		"import_module":            "import other\n",
		"relative_import":          "from .other import f\n",
		"star_import":              "from other import *\n",
		"import_alias":             "from typing import Final as F\n",
		"local_import":             "def f() -> None:\n    from typing import Final\n",
		"conditional_import":       "if True:\n    from typing import Final\n",
		"nested_generic_function":  "def f() -> None:\n    def g[T]() -> None:\n        return\n",
		"lambda":                   "def f() -> None:\n    g = lambda: 1\n",
		"attribute_assignment":     "from typing import NamedTuple\n\nclass R(NamedTuple):\n    x: int\ndef f(r: R) -> None:\n    r.x = 2\n",
		"subscription_assignment":  "def f(xs: tuple[int, ...]) -> None:\n    xs[0] = 1\n",
		"destructuring_assignment": "def f(xs: tuple[int, ...]) -> None:\n    x, y = xs\n",
		"starred_assignment":       "def f(xs: tuple[int, ...]) -> None:\n    x, *ys = xs\n",
		"augmented_assignment":     "def f(x: int) -> int:\n    x += 1\n    return x\n",
		"list_display":             "def f() -> None:\n    xs = [1]\n",
		"dictionary_display":       "def f() -> None:\n    xs = {1: 2}\n",
		"set_display":              "def f() -> None:\n    xs = {1}\n",
		"list_comprehension":       "def f(xs: tuple[int, ...]) -> None:\n    ys = [x for x in xs]\n",
		"dictionary_comprehension": "def f(xs: tuple[int, ...]) -> None:\n    ys = {x: x for x in xs}\n",
		"set_comprehension":        "def f(xs: tuple[int, ...]) -> None:\n    ys = {x for x in xs}\n",
		"generator_comprehension":  "def f(xs: tuple[int, ...]) -> None:\n    ys = (x for x in xs)\n",
		"global":                   "def f() -> None:\n    global state\n",
		"nonlocal":                 "def f() -> None:\n    nonlocal state\n",
		"delete":                   "def f(x: int) -> None:\n    del x\n",
		"assert":                   "def f(flag: bool) -> None:\n    assert flag\n",
		"raise":                    "def f() -> None:\n    raise Exception()\n",
		"try_finally":              "def f() -> None:\n    try:\n        pass\n    finally:\n        pass\n",
		"context_manager":          "def f(x: int) -> None:\n    with x:\n        pass\n",
		"match":                    "def f(x: int) -> None:\n    match x:\n        case 1:\n            pass\n",
		"yield":                    "def f() -> None:\n    yield 1\n",
		"type_alias":               "def f() -> None:\n    type Alias = int\n",
		"assignment_expression":    "def f() -> None:\n    if (x := True):\n        return\n",
	}
	for name, source := range cases {
		t.Run(name, func(t *testing.T) {
			if ds := securityVerify(t, map[string]string{"app": source}, nil); len(ds) == 0 {
				t.Fatalf("prohibited syntax accepted:\n%s", source)
			}
		})
	}
}

func TestSpecCoreRecordBodies(t *testing.T) {
	cases := map[string]string{
		"missing_field_annotation": "    x\n",
		"constructor":              "    def __init__(self: R) -> None:\n        return\n",
		"property":                 "    @property\n    def computed(self: R) -> int:\n        return 1\n",
		"static_method":            "    @staticmethod\n    def make() -> int:\n        return 1\n",
		"class_method":             "    @classmethod\n    def make(cls: R) -> int:\n        return 1\n",
		"descriptor_value":         "    x = property()\n",
		"computed_field":           "    x: int = 1 + 2\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			specCoreCheck(t, "from typing import NamedTuple\n\nclass R(NamedTuple):\n"+body, "PP202")
		})
	}
}

func TestSpecCoreProhibitedTypes(t *testing.T) {
	for _, annotation := range []string{"Any", "object", "Callable", "TypeVar", "Protocol", "Generic", "Literal[1]", "int | str", "list[int]", "dict[str, int]", "set[int]", "frozenset[int]", "bytearray", "Iterator[int]", "Generator[int]", "Coroutine[int]", "Task[int]", "Future[int]", "Exception", "tuple[int, list[str]]"} {
		t.Run(annotation, func(t *testing.T) {
			specCoreCheck(t, "def f(x: "+annotation+") -> None:\n    return\n", "PP203")
		})
	}
}

func TestSpecCoreDirectProjectImports(t *testing.T) {
	sources := map[string]string{
		"other": "from typing import Final\nLIMIT: Final[int] = 3\ndef increment(x: int) -> int:\n    return x + 1\n",
		"app":   "from typing import Final\nfrom other import LIMIT, increment\nBOUND: Final[int] = LIMIT\ndef f() -> int:\n    return increment(BOUND)\n",
	}
	if ds := securityVerify(t, sources, nil); len(ds) > 0 {
		t.Fatal(ds)
	}
	sources["app"] = "from other import increment\ndef f() -> None:\n    increment = 1\n"
	ds := securityVerify(t, sources, nil)
	if !specCoreHasCode(ds, "PP503") {
		t.Fatalf("imported name rebound: %v", ds)
	}
	sources["app"] = "from other import increment\ndef f() -> int:\n    return increment(1)\n"
	sources["other"] = "from app import f\ndef increment(x: int) -> int:\n    return f()\n"
	if ds := securityVerify(t, sources, nil); !specCoreHasCode(ds, "PP105") {
		t.Fatalf("expected import cycle rejection: %v", ds)
	}
}

func specCoreHasCode(ds []diag.Diagnostic, code string) bool {
	for _, d := range ds {
		if d.Code == code {
			return true
		}
	}
	return false
}

func TestSpecCoreSuppressionDoesNotAuthorizeUnknownCalls(t *testing.T) {
	for _, comment := range []string{"# type: ignore", "# noqa", "# purepy: ignore", "# purepy: unsafe"} {
		t.Run(strings.TrimPrefix(comment, "# "), func(t *testing.T) {
			specCoreCheck(t, "def f() -> int:\n    return unknown() "+comment+"\n", "PP303")
		})
	}
}

func TestSpecCoreIdentityAndHashIntrinsicsRejected(t *testing.T) {
	for _, name := range []string{"id", "hash"} {
		t.Run(name, func(t *testing.T) {
			specCoreCheck(t, "def f(x: str) -> int:\n    return "+name+"(x)\n", "PP303")
		})
	}
}

func TestSpecCoreUnmanifestedAmbientOperations(t *testing.T) {
	// These imports are rejected by closed-world resolution. This does not
	// establish that a host-supplied manifest describes its implementation
	// truthfully; external purity remains an explicit trust obligation.
	for _, declaration := range []string{
		"time.time", "time.monotonic", "time.process_time", "random.random",
		"os.getenv", "pathlib.Path", "socket.socket", "sqlite3.connect",
		"os.getpid", "asyncio.current_task", "locale.setlocale",
		"sys.modules", "signal.signal", "sys.settrace", "gc.collect",
	} {
		t.Run(declaration, func(t *testing.T) {
			module, symbol, _ := strings.Cut(declaration, ".")
			specCoreCheck(t, "from "+module+" import "+symbol+"\ndef f() -> None:\n    return\n", "PP102")
		})
	}
}
