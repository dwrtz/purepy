"""Execute only the fixed module development catalog in a CPython worker.

This is not an arbitrary-source runner or a security sandbox. Worker requests
contain names and content fingerprints, never source, input values, or oracles.
Only the reviewed in-repository catalog is imported; project and host code used
by the production verifier is never executed. The fixture host is intentionally
implemented here, outside every verified temporary source root.
"""

from contextlib import contextmanager
from dataclasses import fields, is_dataclass
import hashlib
import importlib
import inspect
import json
from pathlib import Path
import platform
import sys
import tempfile
import types

from differential_semantics import encode_value
from function_runtime import CorpusError, RuntimeBudgetExceeded, _bounded_value
from module_cases import External, Record, catalog


ROOT = Path(__file__).resolve().parents[1]
MAX_REQUEST = 64 * 1024
MAX_EVENTS = 100000
MAX_SUSPENSIONS = 64
DOMAIN_ERRORS = (ArithmeticError, ValueError, TypeError, IndexError, AttributeError)


def executable_spec(case):
    """Exclude all expected types, verdicts, outcomes and host-event oracles."""
    return {"name": case.name, "sources": list(case.sources),
            "arguments": [[input_data(value) for value in call.arguments] for call in case.calls]}


def input_data(value):
    if isinstance(value, Record):
        return {"type": "record", "name": value.name,
                "fields": [[name, input_data(item)] for name, item in value.fields]}
    if isinstance(value, External):
        return {"type": "external", "name": value.name, "value": value.value}
    if type(value) is tuple:
        return {"type": "tuple", "value": [input_data(item) for item in value]}
    _bounded_value(value)
    return encode_value(value)


def fingerprint(case):
    return hashlib.sha256(json.dumps(executable_spec(case), sort_keys=True,
                                     allow_nan=False).encode()).hexdigest()


def worker_entry(case):
    return {"name": case.name, "fingerprint": fingerprint(case)}


def select_cases(entries):
    known = {case.name: case for case in catalog()}
    if not isinstance(entries, list) or not 1 <= len(entries) <= len(known):
        raise CorpusError("invalid fixed-corpus request size")
    selected = []
    for entry in entries:
        if (not isinstance(entry, dict) or set(entry) != {"name", "fingerprint"}
                or not isinstance(entry["name"], str) or entry["name"] not in known):
            raise CorpusError("worker accepts only fixed catalog names and fingerprints")
        case = known[entry["name"]]
        if entry != worker_entry(case):
            raise CorpusError("worker catalog fingerprint mismatch")
        selected.append(case)
    if len({case.name for case in selected}) != len(selected):
        raise CorpusError("duplicate module corpus cases")
    return selected


def write_sources(root, case):
    for name, source in case.sources:
        # Catalog names are fixed, but fail closed if a future edit adds a path.
        if any(not part.isidentifier() for part in name.split(".")):
            raise CorpusError("invalid catalog module name")
        path = root.joinpath(*name.split(".")).with_suffix(".py")
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(source, encoding="utf-8")


def make_host(events):
    host = types.ModuleType("fixture")

    class Token:
        __slots__ = ("label",)

        def __init__(self, label):
            object.__setattr__(self, "label", label)

        def __setattr__(self, _name, _value):
            raise TypeError("immutable fixture value")

        def __eq__(self, other):
            events.append(("equal", self.label, other.label))
            return type(other) is Token and self.label == other.label

    class Read:
        def __init__(self, label):
            self.label = label

    class Connection:
        def __init__(self, label):
            self.label = label

    @types.coroutine
    def suspend():
        yield None

    async def double(n):
        await suspend()
        return n * 2

    async def read(cap, conn, n):
        if type(cap) is not Read or type(conn) is not Connection:
            raise CorpusError("fixture received incorrect authority identity")
        events.append(("read", cap.label, conn.label, n))
        await suspend()
        return n + 10

    async def note(cap, n):
        if type(cap) is not Read:
            raise CorpusError("fixture received incorrect authority identity")
        events.append(("note", cap.label, n))
        await suspend()

    for cls in (Token, Read, Connection):
        cls.__module__ = "fixture"
        cls.__qualname__ = cls.__name__
        setattr(host, cls.__name__, cls)
    host.double, host.read, host.note = double, read, note
    return host


@contextmanager
def execution_budget(prefix):
    remaining = MAX_EVENTS

    def trace(frame, event, _arg):
        nonlocal remaining
        if not frame.f_code.co_filename.startswith(prefix):
            return None
        if event == "call":
            frame.f_trace_opcodes = True
        elif event in ("line", "opcode"):
            remaining -= 1
            if remaining < 0:
                raise RuntimeBudgetExceeded("module corpus execution budget exceeded")
        return trace

    previous = sys.gettrace()
    try:
        sys.settrace(trace)
        yield
    finally:
        sys.settrace(previous)


def finish_coroutine(value):
    if not inspect.iscoroutine(value):
        return value
    try:
        for _ in range(MAX_SUSPENSIONS):
            try:
                yielded = value.send(None)
            except StopIteration as completed:
                return completed.value
            if yielded is not None:
                raise CorpusError("fixture yielded an unsupported awaitable")
        raise RuntimeBudgetExceeded("module corpus suspension budget exceeded")
    finally:
        value.close()


def materialize(value, modules):
    if isinstance(value, (Record, External)):
        module, name = value.name.rsplit(".", 1)
        cls = getattr(modules[module], name)
        if isinstance(value, External):
            return cls(value.value)
        return cls(**{name: materialize(item, modules) for name, item in value.fields})
    if type(value) is tuple:
        return tuple(materialize(item, modules) for item in value)
    _bounded_value(value)
    return value


def observe(value, modules, depth=0):
    if depth > 8:
        raise CorpusError("module result exceeds nesting budget")
    if type(value) is tuple:
        if len(value) > 64:
            raise CorpusError("module result exceeds tuple budget")
        return {"type": "tuple", "value": [observe(item, modules, depth + 1) for item in value]}
    cls = type(value)
    if is_dataclass(value) or cls.__module__ == "fixture":
        module, name = cls.__module__, cls.__name__
        if module not in modules or getattr(modules[module], name, None) is not cls:
            raise CorpusError("record does not have the loaded module's exact class identity")
        qualified = module + "." + name
        if cls.__module__ == "fixture":
            return {"type": "external", "name": qualified, "value": value.label}
        return {"type": "record", "name": qualified,
                "fields": [[field.name, observe(getattr(value, field.name), modules, depth + 1)]
                           for field in fields(value)]}
    _bounded_value(value)
    return encode_value(value)


def runtime_case(case):
    # Equality with the complete local catalog prevents callers from turning
    # this convenient test helper into an arbitrary-source execution API.
    known = {item.name: item for item in catalog()}
    if case.name not in known or case != known[case.name]:
        raise CorpusError("execution requires an unmodified fixed catalog case")
    roots = {name.split(".")[0] for name, _ in case.sources} | {"fixture"}
    previous_modules = {name: module for name, module in sys.modules.items()
                        if name.split(".")[0] in roots}
    for name in previous_modules:
        del sys.modules[name]
    previous_path = sys.path[:]
    events = []
    try:
        with tempfile.TemporaryDirectory(prefix="purepy-module-runtime-") as directory:
            root = Path(directory)
            write_sources(root, case)
            sys.path[:0] = [str(root), str(ROOT / "python")]
            sys.modules["fixture"] = make_host(events)
            importlib.invalidate_caches()
            with execution_budget(str(root)):
                main = importlib.import_module("main")
                # Import other catalog modules too: types used only by input
                # metadata must resolve to the same Python module singleton.
                for name, _ in case.sources:
                    importlib.import_module(name.removesuffix(".__init__"))
            modules = {name: module for name, module in sys.modules.items()
                       if name.split(".")[0] in roots}
            outcomes = []
            for index, call in enumerate(case.calls):
                arguments = tuple(materialize(value, modules) for value in call.arguments)
                events.clear()
                with execution_budget(str(root)):
                    try:
                        result = finish_coroutine(main.probe(*arguments))
                    except (CorpusError, RuntimeBudgetExceeded):
                        raise
                    except DOMAIN_ERRORS as error:
                        outcome = {"index": index, "exception": type(error).__name__}
                    else:
                        outcome = None
                if outcome is None:
                    outcome = {"index": index, "value": observe(result, modules)}
                outcome["events"] = [list(event) for event in events]
                outcomes.append(outcome)
            return {"name": case.name, "fingerprint": fingerprint(case), "outcomes": outcomes}
    finally:
        sys.path[:] = previous_path
        for name in list(sys.modules):
            if name.split(".")[0] in roots:
                del sys.modules[name]
        sys.modules.update(previous_modules)
        importlib.invalidate_caches()


def runtime_worker():
    import resource
    soft, hard = resource.getrlimit(resource.RLIMIT_CPU)
    existing = [value for value in (soft, hard) if value != resource.RLIM_INFINITY]
    resource.setrlimit(resource.RLIMIT_CPU, (min([30, *existing]), hard))
    if sys.platform.startswith("linux"):
        soft, hard = resource.getrlimit(resource.RLIMIT_AS)
        existing = [value for value in (soft, hard) if value != resource.RLIM_INFINITY]
        resource.setrlimit(resource.RLIMIT_AS, (min([1024 * 1024 * 1024, *existing]), hard))
    data = sys.stdin.buffer.read(MAX_REQUEST + 1)
    if len(data) > MAX_REQUEST:
        raise CorpusError("module worker request exceeds budget")
    cases = select_cases(json.loads(data))
    print(json.dumps({"schema": 1, "python_version": platform.python_version(),
                      "hash_randomization": sys.flags.hash_randomization,
                      "results": [runtime_case(case) for case in cases]}, allow_nan=False))
