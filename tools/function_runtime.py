"""Guarded CPython execution of trusted whole-function development cases.

This checks a generated/curated test corpus, not arbitrary Python or analyzed
user projects. AST restrictions, per-invocation execution budgets, process resource
limits, and the parent timeout guard against accidental corpus expansion.
Harness failures never become expected Python domain-exception observations.
"""

from __future__ import annotations

import ast
from dataclasses import dataclass
import json
import platform
import re
import sys

from differential_semantics import BUILTINS, decode_value, encode_value, matches_type


MAX_SOURCE = 32768
MAX_NODES = 2000
MAX_FUNCTIONS = 8
MAX_PARAMETERS = 16
MAX_CASES = 2000
MAX_INVOCATIONS = 64
MAX_SEQUENCE = 16
MAX_DEPTH = 4
MAX_INTEGER_BITS = 256
MAX_TEXT = 128
MAX_REQUEST = 16 * 1024 * 1024
OPCODE_BUDGET = 20000  # Counts line events too, including optimized loop backedges.
PRIMITIVES = frozenset(("None", "bool", "int", "float", "str", "bytes"))
DOMAIN_ERRORS = (ArithmeticError, ValueError, TypeError, IndexError, UnboundLocalError)
NAME = re.compile(r"[A-Za-z_][A-Za-z_0-9]{0,127}\Z")
LABEL = re.compile(r"[a-zA-Z0-9][a-zA-Z0-9_.:/-]{0,159}\Z")


class CorpusError(ValueError):
    """The development corpus or worker protocol violated the harness contract."""


class RuntimeBudgetExceeded(RuntimeError):
    """The execution-event budget was exhausted; never a domain exception."""


def _name(value):
    return isinstance(value, str) and NAME.fullmatch(value) is not None and "__" not in value


def _type_node(node, depth=0):
    """Parse the independent, closed input-type grammar, without evaluation."""
    if depth > MAX_DEPTH:
        raise CorpusError("type exceeded development gate depth")
    if isinstance(node, ast.Name) and node.id in PRIMITIVES - {"None"}:
        return node.id
    if isinstance(node, ast.Constant) and node.value is None:
        return "None"
    if (isinstance(node, ast.BinOp) and isinstance(node.op, ast.BitOr)
            and isinstance(node.right, ast.Constant) and node.right.value is None):
        element = _type_node(node.left, depth + 1)
        if element == "None" or element.endswith(" | None"):
            raise CorpusError("nested optional or optional None is unsupported")
        return element + " | None"
    if (isinstance(node, ast.Subscript) and isinstance(node.value, ast.Name)
            and node.value.id == "tuple" and isinstance(node.slice, ast.Tuple)
            and len(node.slice.elts) == 2 and isinstance(node.slice.elts[1], ast.Constant)
            and node.slice.elts[1].value is Ellipsis):
        return "tuple[" + _type_node(node.slice.elts[0], depth + 1) + ", ...]"
    raise CorpusError("type must be an exact primitive, homogeneous tuple, or optional")


def parse_type(text):
    if not isinstance(text, str) or not text or len(text) > 256:
        raise CorpusError("invalid type metadata")
    try:
        return _type_node(ast.parse(text, mode="eval").body)
    except (SyntaxError, RecursionError) as error:
        raise CorpusError("invalid type metadata") from error


def _bounded_value(value, depth=0):
    if depth > MAX_DEPTH:
        raise CorpusError("value exceeded development gate depth")
    kind = type(value)
    if value is None or kind in (bool, float):
        return
    if kind is int:
        if value.bit_length() > MAX_INTEGER_BITS:
            raise CorpusError("integer exceeded development gate bound")
        return
    if kind in (str, bytes):
        if len(value) > MAX_TEXT:
            raise CorpusError("text exceeded development gate bound")
        return
    if kind is tuple:
        if len(value) > MAX_SEQUENCE:
            raise CorpusError("tuple exceeded development gate bound")
        for item in value:
            _bounded_value(item, depth + 1)
        return
    raise CorpusError(f"unsupported runtime input/value type {kind.__name__}")


def _decode_argument(value, depth=0):
    # Validate the wire shape and encoded sizes before the shared decoder:
    # valid_value deliberately permits unknown output types for rejected cases.
    if depth > MAX_DEPTH or not isinstance(value, dict) or set(value) != {"type", "value"}:
        raise CorpusError("invalid encoded argument")
    kind, data = value["type"], value["value"]
    if not isinstance(kind, str):
        raise CorpusError("invalid encoded argument type")
    if kind == "tuple":
        if not isinstance(data, list) or len(data) > MAX_SEQUENCE:
            raise CorpusError("invalid encoded tuple argument")
        result = tuple(_decode_argument(item, depth + 1) for item in data)
    else:
        if kind not in PRIMITIVES:
            raise CorpusError("unknown encoded argument type")
        if kind in ("int", "float", "str", "bytes"):
            maximum = 80 if kind == "int" else 2 * MAX_TEXT if kind == "bytes" else MAX_TEXT
            if not isinstance(data, str) or len(data) > maximum:
                raise CorpusError("encoded argument exceeded development gate bound")
        try:
            result = decode_value(value)
        except (ValueError, TypeError, OverflowError) as error:
            raise CorpusError("invalid encoded argument") from error
    _bounded_value(result, depth)
    return result


def _signature(function):
    args = function.args
    if (function.decorator_list or function.type_params or args.posonlyargs
            or args.kwonlyargs or args.defaults or args.kw_defaults
            or args.vararg is not None or args.kwarg is not None
            or len(args.args) > MAX_PARAMETERS):
        raise CorpusError("functions require fixed positional signatures without defaults or decorators")
    parameters = []
    seen = set()
    for parameter in args.args:
        if not _name(parameter.arg) or parameter.arg in seen:
            raise CorpusError("invalid or duplicate parameter name")
        seen.add(parameter.arg)
        parameters.append((parameter.arg, _type_node(parameter.annotation)))
    return tuple(parameters), _type_node(function.returns)


ALLOWED_NODES = (
    ast.FunctionDef, ast.arguments, ast.arg, ast.Return, ast.Assign, ast.AnnAssign,
    ast.If, ast.For, ast.While, ast.Break, ast.Continue, ast.Pass, ast.Expr,
    ast.Name, ast.Load, ast.Store, ast.Constant, ast.Tuple, ast.UnaryOp,
    ast.UAdd, ast.USub, ast.Not, ast.Invert, ast.BinOp, ast.Add, ast.Sub,
    ast.Mult, ast.Div, ast.FloorDiv, ast.Mod, ast.BitAnd, ast.BitOr, ast.BitXor,
    ast.BoolOp, ast.And, ast.Or, ast.Compare, ast.Eq, ast.NotEq, ast.Lt,
    ast.LtE, ast.Gt, ast.GtE, ast.Is, ast.IsNot, ast.In, ast.NotIn,
    ast.Subscript, ast.Slice, ast.IfExp, ast.Call, ast.keyword,
)


def _validated_tree(case):
    if any(not isinstance(value, str) or LABEL.fullmatch(value) is None for value in (case.name, case.family)):
        raise CorpusError("invalid case name or family")
    if not isinstance(case.source, str) or len(case.source.encode("utf-8")) > MAX_SOURCE:
        raise CorpusError("source exceeded development gate bound")
    try:
        tree = ast.parse(case.source, mode="exec")
    except (SyntaxError, ValueError, RecursionError) as error:
        raise CorpusError("corpus source must be valid Python") from error
    if len(list(ast.walk(tree))) > MAX_NODES:
        raise CorpusError("AST exceeded development gate bound")
    if not 1 <= len(tree.body) <= MAX_FUNCTIONS or any(not isinstance(node, ast.FunctionDef) for node in tree.body):
        raise CorpusError("module must contain only bounded top-level synchronous functions")
    functions = {}
    signatures = {}
    for function in tree.body:
        if not _name(function.name) or function.name in functions or function.name in BUILTINS or function.name in PRIMITIVES or function.name == "tuple":
            raise CorpusError("invalid, duplicate, or reserved function name")
        functions[function.name] = function
        signatures[function.name] = _signature(function)
    if "probe" not in functions:
        raise CorpusError("corpus requires the probe entrypoint")
    if not isinstance(case.parameters, tuple) or len(case.parameters) > MAX_PARAMETERS:
        raise CorpusError("invalid parameter metadata")
    metadata = []
    for item in case.parameters:
        if not isinstance(item, tuple) or len(item) != 2 or not _name(item[0]):
            raise CorpusError("invalid parameter metadata")
        annotation = parse_type(item[1])
        if annotation != item[1]:
            raise CorpusError("parameter metadata must use canonical type spelling")
        metadata.append((item[0], annotation))
    returns = parse_type(case.returns)
    if returns != case.returns:
        raise CorpusError("return metadata must use canonical type spelling")
    if (tuple(metadata), returns) != signatures["probe"]:
        raise CorpusError("probe source signature differs from corpus metadata")
    if not isinstance(case.invocations, tuple) or not 1 <= len(case.invocations) <= MAX_INVOCATIONS:
        raise CorpusError("invalid invocation count")
    for invocation in case.invocations:
        if not isinstance(invocation.arguments, tuple) or len(invocation.arguments) != len(metadata):
            raise CorpusError("invocation arguments differ from probe parameters")
        for value, (_name_, annotation) in zip(invocation.arguments, metadata):
            _bounded_value(value)
            if not matches_type(encode_value(value), annotation):
                raise CorpusError(f"invocation argument does not have exact type {annotation}")
    graph = {name: set() for name in functions}
    for name, function in functions.items():
        nodes = list(ast.walk(function))
        locals_ = {parameter for parameter, _ in signatures[name][0]}
        for node in nodes:
            if not isinstance(node, ALLOWED_NODES) or isinstance(node, ast.FunctionDef) and node is not function:
                raise CorpusError(f"disallowed corpus AST node {type(node).__name__}")
            if isinstance(node, ast.Name) and isinstance(node.ctx, ast.Store):
                locals_.add(node.id)
            if isinstance(node, ast.Assign) and (len(node.targets) != 1 or not isinstance(node.targets[0], ast.Name)):
                raise CorpusError("assignment requires one local name")
            if isinstance(node, (ast.AnnAssign, ast.For)) and not isinstance(node.target, ast.Name):
                raise CorpusError("assignment or loop target requires one local name")
            if isinstance(node, ast.AnnAssign):
                _type_node(node.annotation)
            if isinstance(node, ast.Tuple) and len(node.elts) > MAX_SEQUENCE:
                raise CorpusError("tuple literal exceeded development gate bound")
            if isinstance(node, ast.Constant) and node.value is not Ellipsis:
                _bounded_value(node.value)
        for local in locals_:
            if not _name(local) or local in functions or local in BUILTINS or local in PRIMITIVES or local == "tuple":
                raise CorpusError("local names must not shadow functions, builtins, or types")
        known = locals_ | set(functions) | set(BUILTINS) | PRIMITIVES | {"tuple"}
        for node in nodes:
            if isinstance(node, ast.Name) and node.id not in known:
                raise CorpusError(f"unknown corpus name {node.id!r}")
            if isinstance(node, ast.Call):
                if not isinstance(node.func, ast.Name) or node.func.id not in set(functions) | set(BUILTINS):
                    raise CorpusError("calls must target a fixed builtin or top-level helper")
                if len(node.args) + len(node.keywords) > MAX_PARAMETERS or any(keyword.arg is None or not _name(keyword.arg) for keyword in node.keywords):
                    raise CorpusError("invalid call arguments")
                if node.func.id in functions:
                    graph[name].add(node.func.id)
    visited, active = set(), set()
    def visit(name):
        if name in active:
            raise CorpusError("recursive corpus helper calls are prohibited")
        if name not in visited:
            active.add(name)
            for target in sorted(graph[name]):
                visit(target)
            active.remove(name)
            visited.add(name)
    for name in sorted(functions):
        visit(name)
    try:
        # ast.parse accepts break outside loops and other non-executable ASTs.
        compile(tree, "<validated whole-function corpus>", "exec")
    except (SyntaxError, ValueError, RecursionError) as error:
        raise CorpusError("corpus source does not compile as Python") from error
    return tree


def validate_case(case):
    """Validate executable metadata, source, and typed inputs; ignore oracles."""
    _validated_tree(case)


def runtime_case(case):
    tree = _validated_tree(case)
    filename = "<whole-function corpus:" + case.name + ">"
    namespace = {"__builtins__": dict(BUILTINS, bool=bool, tuple=tuple)}
    # Only validated function definitions execute here. No body, annotation
    # call, decorator, import, or project initialization can run at module scope.
    exec(compile(tree, filename, "exec"), namespace)
    outcomes = []
    for index, invocation in enumerate(case.invocations):
        remaining = OPCODE_BUDGET
        def trace(frame, event, _arg):
            nonlocal remaining
            if frame.f_code.co_filename != filename:
                return None
            if event == "call":
                frame.f_trace_opcodes = True
            elif event in ("line", "opcode"):
                # CPython may omit opcode callbacks for an optimized loop;
                # repeated line events still bound backward jumps.
                remaining -= 1
                if remaining < 0:
                    raise RuntimeBudgetExceeded(f"{case.name} invocation {index} exceeded execution budget")
            return trace
        previous = sys.gettrace()
        try:
            sys.settrace(trace)
            try:
                value = namespace["probe"](*invocation.arguments)
            except DOMAIN_ERRORS as error:
                outcome = {"index": index, "exception": type(error).__name__}
            else:
                outcome = None
        finally:
            sys.settrace(previous)
        if outcome is None:
            # Validation/encoding failures are outside the domain-exception
            # handler, including oversized results from accidental generators.
            _bounded_value(value)
            outcome = {"index": index, "value": encode_value(value)}
        outcomes.append(outcome)
    return {"name": case.name, "outcomes": outcomes}


@dataclass(frozen=True)
class _Invocation:
    arguments: tuple


@dataclass(frozen=True)
class _Case:
    name: str
    family: str
    source: str
    parameters: tuple
    returns: str
    invocations: tuple


def worker_case(entry):
    """Decode the oracle-free worker protocol and validate it before execution."""
    fields = {"name", "family", "source", "parameters", "returns", "invocations"}
    if not isinstance(entry, dict) or set(entry) != fields:
        raise CorpusError("invalid runtime case fields")
    if not isinstance(entry["parameters"], list) or len(entry["parameters"]) > MAX_PARAMETERS:
        raise CorpusError("invalid runtime parameter list")
    parameters = []
    for item in entry["parameters"]:
        if not isinstance(item, list) or len(item) != 2:
            raise CorpusError("invalid runtime parameter metadata")
        parameters.append(tuple(item))
    if not isinstance(entry["invocations"], list) or not 1 <= len(entry["invocations"]) <= MAX_INVOCATIONS:
        raise CorpusError("invalid runtime invocation list")
    invocations = []
    for invocation in entry["invocations"]:
        if not isinstance(invocation, dict) or set(invocation) != {"arguments"} or not isinstance(invocation["arguments"], list) or len(invocation["arguments"]) > MAX_PARAMETERS:
            raise CorpusError("invalid runtime invocation fields")
        invocations.append(_Invocation(tuple(_decode_argument(argument) for argument in invocation["arguments"])))
    case = _Case(entry["name"], entry["family"], entry["source"], tuple(parameters), entry["returns"], tuple(invocations))
    validate_case(case)
    return case


def runtime_worker():
    import resource
    def limit(kind, maximum):
        soft, hard = resource.getrlimit(kind)
        for existing in (soft, hard):
            if existing != resource.RLIM_INFINITY:
                maximum = min(maximum, existing)
        resource.setrlimit(kind, (maximum, hard))
    limit(resource.RLIMIT_CPU, 30)
    if sys.platform.startswith("linux"):
        limit(resource.RLIMIT_AS, 1024 * 1024 * 1024)
    data = sys.stdin.buffer.read(MAX_REQUEST + 1)
    if len(data) > MAX_REQUEST:
        raise CorpusError("runtime request exceeded development gate bound")
    entries = json.loads(data)
    if not isinstance(entries, list) or not 1 <= len(entries) <= MAX_CASES:
        raise CorpusError("invalid runtime case list")
    cases = [worker_case(entry) for entry in entries]
    if len({case.name for case in cases}) != len(cases):
        raise CorpusError("duplicate runtime case names")
    payload = {"schema": 1, "python_version": platform.python_version(), "results": [runtime_case(case) for case in cases]}
    print(json.dumps(payload, allow_nan=False))
