"""Keep whole-function corpus failures separate from runtime observations."""

from copy import deepcopy
from pathlib import Path
import json
import subprocess
import sys
from types import SimpleNamespace
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import function_runtime as runtime


def case(source="def probe(n: int) -> int:\n    return n\n", parameters=(("n", "int"),), returns="int", arguments=((1,),)):
    return SimpleNamespace(
        name="runtime_case", family="runtime_test", source=source,
        parameters=parameters, returns=returns,
        invocations=tuple(SimpleNamespace(arguments=values) for values in arguments),
    )


def entry(value=None):
    value = case() if value is None else value
    return {
        "name": value.name, "family": value.family, "source": value.source,
        "parameters": [list(parameter) for parameter in value.parameters], "returns": value.returns,
        "invocations": [{"arguments": [runtime.encode_value(argument) for argument in invocation.arguments]} for invocation in value.invocations],
    }


class FunctionRuntimeTests(unittest.TestCase):
    def test_multiple_inputs_keep_exact_values_types_and_indexes(self):
        value = case(
            "def twice(n: int) -> int:\n    return n + n\n\ndef probe(n: int) -> int:\n    total = 0\n    for i in range(n):\n        if i == 2:\n            continue\n        total = total + twice(i)\n    return total\n",
            arguments=((0,), (2,), (4,)),
        )
        runtime.validate_case(value)
        result = runtime.runtime_case(value)
        self.assertEqual(result, {"name": value.name, "outcomes": [
            {"index": 0, "value": runtime.encode_value(0)},
            {"index": 1, "value": runtime.encode_value(2)},
            {"index": 2, "value": runtime.encode_value(8)},
        ]})

    def test_guard_rejects_host_machinery_before_execution(self):
        sources = (
            "import os\ndef probe(n: int) -> int:\n    return n\n",
            "marker = 1\ndef probe(n: int) -> int:\n    return n\n",
            "@print\ndef probe(n: int) -> int:\n    return n\n",
            "async def probe(n: int) -> int:\n    return n\n",
            "class Box:\n    pass\ndef probe(n: int) -> int:\n    return n\n",
            "def probe(n: int) -> int:\n    return n.__class__\n",
            "def probe(n: int) -> int:\n    return __import__('os')\n",
            "def probe(n: int) -> int:\n    return eval('n')\n",
            "def probe(n: int) -> int:\n    return globals()\n",
            "def probe(n: int) -> int:\n    return [x for x in range(n)]\n",
            "def probe(n: int) -> int:\n    return lambda: n\n",
            "def probe(n: int) -> int:\n    def inner() -> int:\n        return n\n    return inner()\n",
            "def probe(n: int) -> int:\n    global n\n    return n\n",
            "def probe(n: int) -> int:\n    try:\n        return n\n    finally:\n        pass\n",
            "def probe(n: int) -> int:\n    raise ValueError()\n",
            "def probe(n: int) -> int:\n    return 2 ** n\n",
            "def probe(n: int) -> int:\n    return n << 100\n",
        )
        for source in sources:
            with self.subTest(source=source), self.assertRaises(runtime.CorpusError):
                runtime.validate_case(case(source))

    def test_signatures_and_metadata_are_independently_checked(self):
        for source in (
            "def probe(n: int = 1) -> int:\n    return n\n",
            "def probe(n: int, /) -> int:\n    return n\n",
            "def probe(*, n: int) -> int:\n    return n\n",
            "def probe(*n: int) -> int:\n    return 0\n",
            "def probe(n) -> int:\n    return n\n",
            "def probe(n: int):\n    return n\n",
            "def probe(n: int) -> 'int':\n    return n\n",
            "def probe(n: int) -> int:\n    break\n",
        ):
            with self.subTest(source=source), self.assertRaises(runtime.CorpusError):
                runtime.validate_case(case(source))
        for parameters, returns in (((("different", "int"),), "int"), ((("n", "bool"),), "int"), ((("n", "int"),), "str"), ((("n", " int "),), "int"), ((("n", "int"),), " int")):
            with self.subTest(parameters=parameters, returns=returns), self.assertRaises(runtime.CorpusError):
                runtime.validate_case(case(parameters=parameters, returns=returns))

    def test_independent_type_parser_is_closed(self):
        for annotation in ("int", "bool", "None", "int | None", "tuple[bytes, ...]", "tuple[int | None, ...] | None"):
            self.assertEqual(runtime.parse_type(annotation), annotation)
        for annotation in ("Any", "object", "list[int]", "tuple[int]", "tuple[int, str]", "int | str", "None | None", "int | None | None", "host.Record", "str()", "'int'", "tuple[" * 10 + "int" + ", ...]" * 10):
            with self.subTest(annotation=annotation), self.assertRaises(runtime.CorpusError):
                runtime.parse_type(annotation)

    def test_inputs_require_exact_types_and_known_bounds(self):
        for value in (True, "1", 1.0, [], object(), 1 << 256):
            with self.subTest(value=value), self.assertRaises(runtime.CorpusError):
                runtime.validate_case(case(arguments=((value,),)))
        tuples = case("def probe(xs: tuple[int, ...]) -> int:\n    return len(xs)\n", (("xs", "tuple[int, ...]"),), arguments=(((1, 2),),))
        runtime.validate_case(tuples)
        for value in ((1, True), tuple(range(17))):
            tuples.invocations = (SimpleNamespace(arguments=(value,)),)
            with self.subTest(value=value), self.assertRaises(runtime.CorpusError):
                runtime.validate_case(tuples)
        for value in ("x" * 129, b"x" * 129, (((((0,),),),),)):
            with self.subTest(value=value), self.assertRaises(runtime.CorpusError):
                runtime._bounded_value(value)

    def test_recursive_unknown_aliased_and_shadowed_calls_are_rejected(self):
        for source in (
            "def probe(n: int) -> int:\n    return probe(n)\n",
            "def helper(n: int) -> int:\n    return probe(n)\ndef probe(n: int) -> int:\n    return helper(n)\n",
            "def probe(n: int) -> int:\n    return missing(n)\n",
            "def probe(n: int) -> int:\n    f = abs\n    return f(n)\n",
            "def probe(n: int) -> int:\n    len = n\n    return len(n)\n",
            "def probe(n: int) -> int:\n    return abs(*(n,))\n",
        ):
            with self.subTest(source=source), self.assertRaises(runtime.CorpusError):
                runtime.validate_case(case(source))

    def test_domain_exceptions_are_caught_per_invocation(self):
        result = runtime.runtime_case(case("def probe(n: int) -> int:\n    return 6 // n\n", arguments=((2,), (0,), (3,))))
        self.assertEqual(result["outcomes"], [
            {"index": 0, "value": runtime.encode_value(3)},
            {"index": 1, "exception": "ZeroDivisionError"},
            {"index": 2, "value": runtime.encode_value(2)},
        ])
        source = "def probe(n: int) -> int:\n    if n > 0:\n        x = n\n    return x\n"
        self.assertEqual(runtime.runtime_case(case(source, arguments=((0,),)))["outcomes"], [{"index": 0, "exception": "UnboundLocalError"}])

    def test_budget_exhaustion_is_never_a_domain_exception(self):
        forever = case("def probe(n: int) -> int:\n    while True:\n        pass\n    return n\n")
        with patch.object(runtime, "OPCODE_BUDGET", 100), self.assertRaises(runtime.RuntimeBudgetExceeded):
            runtime.runtime_case(forever)

    def test_harness_encoding_failures_are_not_domain_exceptions(self):
        too_large = case("def probe(n: int) -> str:\n    return 'a' * n\n", returns="str", arguments=((129,),))
        with self.assertRaises(runtime.CorpusError):
            runtime.runtime_case(too_large)
        with patch.object(runtime, "encode_value", side_effect=ValueError("encoder failed")), self.assertRaises(ValueError):
            runtime.runtime_case(case())

    def test_trace_is_restored_after_success_domain_and_budget_failure(self):
        previous = sys.gettrace()
        def existing_trace(_frame, _event, _arg):
            return existing_trace
        sys.settrace(existing_trace)
        try:
            runtime.runtime_case(case())
            self.assertIs(sys.gettrace(), existing_trace)
            runtime.runtime_case(case("def probe(n: int) -> int:\n    return n // 0\n"))
            self.assertIs(sys.gettrace(), existing_trace)
            with patch.object(runtime, "OPCODE_BUDGET", 10), self.assertRaises(runtime.RuntimeBudgetExceeded):
                runtime.runtime_case(case("def probe(n: int) -> int:\n    while True:\n        pass\n"))
            self.assertIs(sys.gettrace(), existing_trace)
        finally:
            sys.settrace(previous)

    def test_worker_wire_format_has_no_oracles_and_rejects_unknown_values(self):
        original = entry()
        decoded = runtime.worker_case(original)
        self.assertEqual(runtime.runtime_case(decoded), runtime.runtime_case(case()))
        for changes in ({"expected_codes": []}, {"check_value": True}, {"name": ""}):
            altered = dict(original, **changes)
            with self.subTest(changes=changes), self.assertRaises(runtime.CorpusError):
                runtime.worker_case(altered)
        for argument in ({"type": "object"}, {"type": "object", "value": None}, {"type": "int", "value": "9" * 10000}, {"type": "int", "value": "1", "extra": True}, {"type": "int", "value": True}, {"type": "bool", "value": 1}):
            altered = deepcopy(original)
            altered["invocations"][0]["arguments"] = [argument]
            with self.subTest(argument=argument), self.assertRaises(runtime.CorpusError):
                runtime.worker_case(altered)
        altered = deepcopy(original)
        altered["invocations"][0]["expected_value"] = runtime.encode_value(1)
        with self.assertRaises(runtime.CorpusError):
            runtime.worker_case(altered)

    def test_optional_tuple_bytes_and_signed_float_inputs_roundtrip(self):
        for annotation, argument in (("int | None", None), ("tuple[int, ...]", (1, 2)), ("bytes", b"\x00\xff"), ("float", -0.0)):
            value = case(f"def probe(n: {annotation}) -> {annotation}:\n    return n\n", (("n", annotation),), annotation, ((argument,),))
            outcome = runtime.runtime_case(runtime.worker_case(entry(value)))["outcomes"][0]
            self.assertEqual(outcome, {"index": 0, "value": runtime.encode_value(argument)})

    def test_source_function_node_invocation_and_literal_budgets(self):
        value = case()
        for mutation in (
            {"source": "#" * (runtime.MAX_SOURCE + 1)},
            {"source": "\n".join(f"def helper{i}(n: int) -> int:\n    return n" for i in range(runtime.MAX_FUNCTIONS)) + "\n" + value.source},
            {"invocations": ()},
            {"invocations": value.invocations * (runtime.MAX_INVOCATIONS + 1)},
            {"source": "def probe(n: int) -> int:\n    return " + str(1 << 256) + "\n"},
            {"source": "def probe(n: int) -> int:\n    return len('" + "x" * 129 + "')\n"},
        ):
            altered = SimpleNamespace(**(vars(value) | mutation))
            with self.subTest(mutation=mutation), self.assertRaises(runtime.CorpusError):
                runtime.validate_case(altered)
        with patch.object(runtime, "MAX_NODES", 1), self.assertRaises(runtime.CorpusError):
            runtime.validate_case(value)

    def test_isolated_worker_protocol_and_failure_exit(self):
        tools = str(Path(__file__).resolve().parents[1])
        script = "import sys; sys.path.insert(0, " + repr(tools) + "); from function_runtime import runtime_worker; runtime_worker()"
        def run(entries):
            return subprocess.run([sys.executable, "-I", "-c", script],
                                  input=json.dumps(entries), capture_output=True,
                                  text=True, timeout=10)
        value = case(arguments=((-1,), (0,), (1,)))
        result = run([entry(value)])
        self.assertEqual(result.returncode, 0, result.stderr)
        payload = json.loads(result.stdout)
        self.assertEqual(set(payload), {"schema", "python_version", "results"})
        self.assertEqual(payload["schema"], 1)
        self.assertEqual(payload["results"], [runtime.runtime_case(value)])
        for entries in ([], [entry(value)] * 2,
                        [entry(value)] * (runtime.MAX_CASES + 1),
                        [entry(case("def probe(n: int) -> int:\n    while True:\n        pass\n"))]):
            with self.subTest(count=len(entries), source=entries[0]["source"] if entries else ""):
                result = run(entries)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(result.stdout, "")


if __name__ == "__main__":
    unittest.main()
