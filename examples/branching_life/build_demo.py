"""Unverified host: test the core and serialize its worlds for display."""

import json
from pathlib import Path
import sys

sys.path.insert(0, str(Path(__file__).parent / "src"))
from life import History, World, evolve, fork, seed, simulate, step


def frames(history):
    result = []
    while history is not None:
        result.append("".join(str(cell) for cell in history.state.cells))
        history = history.previous
    return result[::-1]


def test():
    block = World(4, 4, (0, 0, 0, 0, 0, 1, 1, 0, 0, 1, 1, 0, 0, 0, 0, 0), 0)
    assert step(block).cells == block.cells
    blinker = World(5, 5, tuple(int(i in (11, 12, 13)) for i in range(25)), 0)
    assert step(blinker).cells == tuple(int(i in (7, 12, 17)) for i in range(25))
    assert step(step(blinker)).cells == blinker.cells
    original = simulate(blinker, 4)
    snapshot = frames(original)
    alternate = fork(original, 12, 0)
    assert alternate.previous is original.previous
    assert sum(a != b for a, b in zip(alternate.state.cells, original.state.cells)) == 1
    assert frames(original) == snapshot
    assert frames(simulate(blinker, 4)) == snapshot


def main():
    test()
    initial = seed(40, 28, 2026)
    past = simulate(initial, 20)
    original = evolve(step, past, 100)
    # A live cell near the center: removing it changes only one initial bit.
    candidates = [i for i, c in enumerate(past.state.cells) if c and 10 < i % 40 < 30 and 8 < i // 40 < 20]
    changed_cell = candidates[len(candidates) // 2]
    alternate = fork(past, changed_cell, 100)
    data = dict(width=40, height=28, fork=20, cell=changed_cell,
                original=frames(original), alternate=frames(alternate))
    assert len(data["original"]) == len(data["alternate"]) == 121
    assert data["original"][:20] == data["alternate"][:20]
    assert sum(a != b for a, b in zip(data["original"][20], data["alternate"][20])) == 1
    template = Path(__file__).with_name("demo.template.html").read_text()
    output = Path(sys.argv[1]) if len(sys.argv) > 1 else Path(__file__).with_name("demo.html")
    output.write_text(template.replace("__WORLD_DATA__", json.dumps(data, separators=(",", ":"))))
    print(f"Runtime checks passed; 242 verified-core frames written to {output}")


if __name__ == "__main__":
    main()
