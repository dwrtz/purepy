"""Check the whole-function corpus independently of either execution engine."""

import ast
from dataclasses import FrozenInstanceError
from pathlib import Path
import random
import sys
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from function_cases import FunctionCase, Invocation, generate_cases


def matches(value, annotation):
    if annotation.endswith(" | None"):
        return value is None or matches(value, annotation[:-7])
    if annotation.startswith("tuple[") and annotation.endswith(", ...]"):
        return type(value) is tuple and all(matches(item, annotation[6:-6]) for item in value)
    return type(value) is {
        "None": type(None), "bool": bool, "int": int, "float": float,
        "str": str, "bytes": bytes,
    }[annotation]


class FunctionCasesTests(unittest.TestCase):
    def test_dataclasses_are_frozen_and_have_agreed_defaults(self):
        invocation = Invocation((None,))
        self.assertFalse(invocation.check_value)
        self.assertIsNone(invocation.exception)
        self.assertIsNone(invocation.expected_value)
        case = FunctionCase("test", "test", "", (), "None", (invocation,))
        self.assertEqual(case.expected_codes, ())
        with self.assertRaises(FrozenInstanceError):
            invocation.check_value = True
        with self.assertRaises(FrozenInstanceError):
            case.returns = "int"

    def test_deterministic_local_rng_and_stable_permanent_cases(self):
        state = random.getstate()
        first = generate_cases(seed=42, samples=64)
        self.assertEqual(state, random.getstate())
        self.assertEqual(first, generate_cases(seed=42, samples=64))
        self.assertNotEqual(first, generate_cases(seed=43, samples=64))
        permanent = generate_cases(seed=42, samples=0)
        self.assertEqual(permanent, generate_cases(seed=-999, samples=0))
        self.assertEqual(first[:len(permanent)], permanent)

    def test_arguments_and_sample_bounds_are_strict(self):
        for seed, samples in ((True, 1), (1, False), ("0", 1), (0, 1.0)):
            with self.subTest(seed=seed, samples=samples), self.assertRaises(TypeError):
                generate_cases(seed=seed, samples=samples)
        for samples in (-1, 257):
            with self.subTest(samples=samples), self.assertRaises(ValueError):
                generate_cases(samples=samples)
        self.assertLess(len(generate_cases(samples=256)), 2000)

    def test_corpus_inputs_obey_exact_types_and_resource_bounds(self):
        def bounded(value, depth=0):
            self.assertLessEqual(depth, 4)
            if type(value) is int:
                self.assertLessEqual(value.bit_length(), 256)
            elif type(value) in (str, bytes):
                self.assertLessEqual(len(value), 128)
            elif type(value) is tuple:
                self.assertLessEqual(len(value), 16)
                for item in value:
                    bounded(item, depth + 1)
            else:
                self.assertIn(type(value), (type(None), bool, float))

        cases = generate_cases(seed=731, samples=256)
        self.assertEqual(len(cases), len({case.name for case in cases}))
        for case in cases:
            with self.subTest(case=case.name):
                self.assertRegex(case.name, r"\A[a-zA-Z0-9][a-zA-Z0-9_.:/-]{0,159}\Z")
                self.assertRegex(case.family, r"\A[a-zA-Z0-9][a-zA-Z0-9_.:/-]{0,159}\Z")
                self.assertGreaterEqual(len(case.invocations), 2)
                self.assertLessEqual(len(case.invocations), 64)
                self.assertLess(len(case.source.encode("utf-8")), 4096)
                self.assertEqual(len(case.invocations), len({inv.arguments for inv in case.invocations}))
                for invocation in case.invocations:
                    self.assertEqual(len(invocation.arguments), len(case.parameters))
                    for value, (_, annotation) in zip(invocation.arguments, case.parameters):
                        self.assertTrue(matches(value, annotation), (value, annotation))
                        bounded(value)
                    self.assertNotEqual(invocation.check_value, invocation.exception is not None)
                    if invocation.check_value:
                        bounded(invocation.expected_value)
                        if not case.expected_codes:
                            self.assertTrue(matches(invocation.expected_value, case.returns))

    def test_sources_match_metadata_and_use_only_top_level_helpers(self):
        for case in generate_cases(seed=93, samples=256):
            with self.subTest(case=case.name):
                tree = ast.parse(case.source)
                self.assertTrue(all(type(node) is ast.FunctionDef for node in tree.body))
                definitions = {node.name: node for node in tree.body}
                self.assertEqual(len(definitions), len(tree.body))
                self.assertIn("probe", definitions)
                entry = definitions["probe"]
                self.assertEqual(tuple((arg.arg, ast.unparse(arg.annotation)) for arg in entry.args.args), case.parameters)
                self.assertEqual(ast.unparse(entry.returns), case.returns)
                self.assertFalse(entry.args.defaults)
                self.assertFalse(entry.args.vararg or entry.args.kwarg)
                earlier = set()
                for function in tree.body:
                    for node in ast.walk(function):
                        if isinstance(node, ast.Call):
                            self.assertIsInstance(node.func, ast.Name)
                            if node.func.id in definitions:
                                self.assertIn(node.func.id, earlier, "helper dependency must be acyclic")
                    earlier.add(function.name)
                for node in ast.walk(tree):
                    self.assertNotIsInstance(node, (ast.Import, ast.ImportFrom, ast.Attribute,
                                                   ast.ClassDef, ast.Lambda, ast.AsyncFunctionDef,
                                                   ast.ListComp, ast.SetComp, ast.DictComp,
                                                   ast.GeneratorExp, ast.With, ast.Try))

    def test_fixed_matrix_covers_types_flow_families_and_exception_paths(self):
        cases = generate_cases(samples=0)
        families = {case.family for case in cases}
        self.assertTrue({"optional", "branch", "exact_type", "for_tuple", "for_range", "while",
                         "nested_loop", "early_return", "none", "tuple", "domain", "regression",
                         "payload"} <= families)
        returns = {case.returns for case in cases}
        self.assertTrue({"None", "bool", "int", "float", "str", "bytes", "int | None",
                         "tuple[int | None, ...]", "tuple[tuple[int, ...] | None, ...]",
                         "tuple[tuple[int, ...], ...] | None"} <= returns)
        self.assertTrue(any(case.expected_codes for case in cases))
        self.assertTrue(any(not case.expected_codes for case in cases))
        exceptions = {inv.exception for case in cases for inv in case.invocations}
        self.assertTrue({"IndexError", "ZeroDivisionError", "UnboundLocalError"} <= exceptions)

    def test_seeded_generation_varies_control_flow_not_only_literals(self):
        class WithoutConstants(ast.NodeTransformer):
            def visit_Constant(self, node):
                return ast.copy_location(ast.Constant(value=None), node)

        cases = [case for case in generate_cases(seed=37, samples=256) if case.name.startswith("seeded/")]
        structures = {ast.dump(WithoutConstants().visit(ast.parse(case.source))) for case in cases}
        self.assertGreaterEqual(len(structures), 16)
        sources = "\n".join(case.source for case in cases)
        for witness in ("while i < n:", "for i in range(", "if item is None:",
                        "if item is not None:", "def identity2(", "return total\n",
                        "for row in rows:", "continue\n", "break\n"):
            self.assertIn(witness, sources)

    def test_while_templates_advance_bounded_counters_before_any_continue(self):
        for case in generate_cases(seed=18, samples=256):
            for loop in (node for node in ast.walk(ast.parse(case.source)) if isinstance(node, ast.While)):
                with self.subTest(case=case.name):
                    condition_names = {node.id for node in ast.walk(loop.test) if isinstance(node, ast.Name)}
                    counter = "remaining" if "remaining" in condition_names else "i"
                    self.assertIn(counter, condition_names)
                    updates = []
                    for index, node in enumerate(loop.body):
                        if isinstance(node, ast.Assign) and any(isinstance(t, ast.Name) and t.id == counter for t in node.targets):
                            updates.append(index)
                            self.assertIsInstance(node.value, ast.BinOp)
                            self.assertIsInstance(node.value.left, ast.Name)
                            self.assertEqual(node.value.left.id, counter)
                            self.assertEqual(node.value.right.value, 1)
                            self.assertIsInstance(node.value.op, ast.Sub if counter == "remaining" else ast.Add)
                    self.assertEqual(len(updates), 1)
                    for node in loop.body[:updates[0]]:
                        self.assertFalse(any(isinstance(child, (ast.If, ast.For, ast.While, ast.Continue)) for child in ast.walk(node)))

    def test_permanent_regressions_have_explicit_break_continue_and_empty_witnesses(self):
        cases = {case.name: case for case in generate_cases(samples=0)}
        for exit_kind in ("break", "continue"):
            case = cases[f"regression/for_target_{exit_kind}_optional"]
            self.assertEqual(case.expected_codes, ("PP205",))
            witnesses = {inv.arguments: inv for inv in case.invocations}
            self.assertEqual(witnesses[((),)].expected_value, 1)
            self.assertIsNone(witnesses[((None,),)].expected_value)
            self.assertTrue(witnesses[((None,),)].check_value)
            self.assertEqual(witnesses[((None, 2),)].expected_value,
                             None if exit_kind == "break" else 2)
        case = cases["regression/for_iterable_before_body_rebinding"]
        self.assertEqual(case.expected_codes, ())
        witnesses = {inv.arguments: inv.expected_value for inv in case.invocations}
        self.assertEqual(witnesses[(None,)], 0)
        self.assertEqual(witnesses[((),)], 0)
        self.assertEqual(witnesses[((1, 2),)], 3)

    def test_branch_witnesses_exercise_each_join_path(self):
        cases = {case.name: case for case in generate_cases(samples=0)}
        branch = {inv.arguments: inv.expected_value for inv in cases["optional/elif_join"].invocations}
        self.assertEqual(branch[(None, False)], 0)
        self.assertEqual(branch[(3, True)], 5)
        self.assertEqual(branch[(3, False)], 1)
        unassigned = cases["branch/missing_assignment"]
        self.assertEqual(unassigned.invocations[0].arguments, (True,))
        self.assertEqual(unassigned.invocations[0].expected_value, 9)
        self.assertEqual(unassigned.invocations[1].arguments, (False,))
        self.assertEqual(unassigned.invocations[1].exception, "UnboundLocalError")


if __name__ == "__main__":
    unittest.main()
