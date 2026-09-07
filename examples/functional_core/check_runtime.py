"""Runtime fidelity checks for the verified functional-core examples."""

from collections import Counter
from copy import replace
import importlib.util
from pathlib import Path
import random
import sys


# Load only the adjacent example module. This also works under -I -S, where
# neither the script directory nor site packages are on the import search path.
example_path = Path(__file__).parent / "src" / "core.py"
spec = importlib.util.spec_from_file_location("functional_core_examples", example_path)
examples = importlib.util.module_from_spec(spec)
sys.modules[spec.name] = examples
spec.loader.exec_module(examples)


def add(left: int, right: int) -> int:
    return left + right


def square(number: int) -> int:
    return number * number


def increment(number: int) -> int:
    return number + 1


def positive(number: int) -> bool:
    return number > 0


def unpack(chain):
    result = []
    while chain is not None:
        result.append(chain.head)
        chain = chain.tail
    return tuple(result)


def test_runtime_fidelity():
    tail = examples.Link(1, None)
    head = examples.Link(2, tail)
    changed_head = replace(head, head=3)
    assert head.head == 2 and changed_head.head == 3
    assert changed_head.tail is tail
    original_tree = examples.CountsNode("python", 1, None, None)
    changed_tree = replace(original_tree, count=2)
    assert original_tree.count == 1 and changed_tree.count == 2
    assert type(changed_tree) is type(original_tree)
    for record, field in ((head, "head"), (original_tree, "count")):
        try:
            setattr(record, field, 99)
        except AttributeError:
            pass
        else:
            raise AssertionError("standard record field unexpectedly writable")

    rng = random.Random(20260906)
    for _ in range(200):
        values = tuple(rng.randrange(-50, 51) for _ in range(rng.randrange(101)))
        chain = examples.from_tuple(values)
        assert unpack(chain) == values
        assert examples.fold_tuple(add, 0, values) == sum(values)
        assert examples.fold_chain(add, 0, chain) == sum(values)
        assert unpack(examples.map_chain(square, chain)) == tuple(
            item * item for item in values
        )
        assert unpack(examples.filter_chain(positive, chain)) == tuple(
            item for item in values if item > 0
        )
        assert unpack(chain) == values
        combined = examples.compose(increment, square)
        assert all(combined(item) == item * item + 1 for item in values)

    for _ in range(200):
        labels = tuple(str(rng.randrange(20)) for _ in range(rng.randrange(101)))
        expected = Counter(labels)
        tree, order = examples.group_labels(labels)
        assert unpack(order) == tuple(expected)
        assert all(
            examples.count_for(tree, key) == count
            for key, count in expected.items()
        )
        assert examples.count_for(tree, "absent") is None
        changed = examples.increment_count(tree, "absent")
        assert examples.count_for(tree, "absent") is None
        assert examples.count_for(changed, "absent") == 1
        assert all(
            examples.count_for(changed, key) == count
            for key, count in expected.items()
        )

    alphabet = "AZaz09 !\n\tÉİßΩ😀"
    translation = str.maketrans(
        "ABCDEFGHIJKLMNOPQRSTUVWXYZ", "abcdefghijklmnopqrstuvwxyz"
    )
    for _ in range(200):
        text = "".join(rng.choice(alphabet) for _ in range(rng.randrange(101)))
        assert examples.lower_ascii(text) == text.translate(translation)

    large = tuple(range(10000))
    chain = examples.from_tuple(large)
    transformed = examples.map_chain(increment, chain)
    assert examples.fold_chain(add, 0, transformed) == sum(large) + len(large)
    assert not any(
        name == "purepy" or name.startswith("purepy.") for name in sys.modules
    ), "examples must run without importing a PurePy runtime"
    print(
        "PASS: 200 sequence/composition cases, 200 ordered-grouping/persistence "
        "cases, 200 ASCII cases, a 10,000-element chain case, and record "
        "immutability/replacement checks."
    )
    print("Run purepy check examples/functional_core for static verification.")
    print("Interpreter:", sys.version.split()[0])
    print("Isolated:", bool(sys.flags.isolated), "Site disabled:", bool(sys.flags.no_site))
    print("PurePy runtime imported: False")


if __name__ == "__main__":
    test_runtime_fidelity()
