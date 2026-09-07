"""Immutable cellular worlds and persistent, branching simulation history."""

from collections.abc import Callable
from copy import replace
from typing import NamedTuple


class World(NamedTuple):
    width: int
    height: int
    cells: tuple[int, ...]
    generation: int


class History[T](NamedTuple):
    state: T
    previous: History[T] | None


def evolve[T](advance: Callable[[T], T], history: History[T], steps: int) -> History[T]:
    current = history
    for unused in range(steps):
        current = History(advance(current.state), current)
    return current


def rule(birth: int) -> Callable[[int, int], int]:
    def decide(alive: int, neighbors: int) -> int:
        if neighbors == 3 or (alive == 1 and neighbors == 2):
            return 1
        if alive == 0 and neighbors == birth:
            return 1
        return 0
    return decide


def step_with(world: World, decide: Callable[[int, int], int]) -> World:
    cells: tuple[int, ...] = ()
    for y in range(world.height):
        for x in range(world.width):
            neighbors = 0
            for dy in range(-1, 2):
                for dx in range(-1, 2):
                    if dx != 0 or dy != 0:
                        index = ((y + dy) % world.height) * world.width + (x + dx) % world.width
                        neighbors = neighbors + world.cells[index]
            cells = cells + (decide(world.cells[y * world.width + x], neighbors),)
    return replace(world, cells=cells, generation=world.generation + 1)


def step(world: World) -> World:
    return step_with(world, rule(3))


def seed(width: int, height: int, entropy: int) -> World:
    cells: tuple[int, ...] = ()
    state = entropy
    for unused in range(width * height):
        state = (state * 1664525 + 1013904223) % 4294967296
        alive = 0
        if state % 100 < 28:
            alive = 1
        cells = cells + (alive,)
    return World(width, height, cells, 0)


def simulate(world: World, steps: int) -> History[World]:
    return evolve(step, History(world, None), steps)


def fork(history: History[World], cell: int, steps: int) -> History[World]:
    world = history.state
    cells = world.cells[:cell] + (1 - world.cells[cell],) + world.cells[cell + 1:]
    changed = replace(world, cells=cells)
    return evolve(step, History(changed, history.previous), steps)
