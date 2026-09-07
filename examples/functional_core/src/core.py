"""Verified functional-core examples for PurePy language 0.2.

These use Python 3.14, immutable data, function calls, and standard-library
Callable, NamedTuple, and replace. They require no PurePy runtime. They are
reference source for the functional-core proposal, not a new runtime API. The
grouping tree is deliberately unbalanced and is not a production collection or
a performance recommendation.
"""

from collections.abc import Callable
from copy import replace
from typing import NamedTuple


class Link[T](NamedTuple):
    head: T
    tail: Link[T] | None


class CountsNode(NamedTuple):
    key: str
    count: int
    left: CountsNode | None
    right: CountsNode | None


type Chain[T] = Link[T] | None
type CountsTree = CountsNode | None
type GroupState = tuple[CountsTree, Chain[str]]


def compose[A, B, C](
    outer: Callable[[B], C], inner: Callable[[A], B]
) -> Callable[[A], C]:
    def composed(argument: A) -> C:
        return outer(inner(argument))

    return composed


def fold_tuple[A, B](
    step: Callable[[A, B], A], initial: A, items: tuple[B, ...]
) -> A:
    state = initial
    for item in items:
        state = step(state, item)
    return state


def fold_chain[A, B](
    step: Callable[[A, B], A], initial: A, items: Chain[B]
) -> A:
    state = initial
    remaining = items
    while remaining is not None:
        state = step(state, remaining.head)
        remaining = remaining.tail
    return state


def reverse_chain[T](items: Chain[T]) -> Chain[T]:
    result: Chain[T] = None
    remaining = items
    while remaining is not None:
        result = Link(remaining.head, result)
        remaining = remaining.tail
    return result


def from_tuple[T](items: tuple[T, ...]) -> Chain[T]:
    result: Chain[T] = None
    for item in items:
        result = Link(item, result)
    return reverse_chain(result)


def map_chain[A, B](
    transform: Callable[[A], B], items: Chain[A]
) -> Chain[B]:
    def prepend_mapped(state: Chain[B], item: A) -> Chain[B]:
        return Link(transform(item), state)

    empty: Chain[B] = None
    return reverse_chain(fold_chain(prepend_mapped, empty, items))


def filter_chain[T](
    predicate: Callable[[T], bool], items: Chain[T]
) -> Chain[T]:
    def prepend_if(state: Chain[T], item: T) -> Chain[T]:
        if predicate(item):
            return Link(item, state)
        return state

    empty: Chain[T] = None
    return reverse_chain(fold_chain(prepend_if, empty, items))


def lower_ascii_character(character: str) -> str:
    code = ord(character)
    if 65 <= code <= 90:
        return chr(code + 32)
    return character


def lower_ascii_range(text: str, start: int, stop: int) -> str:
    if start == stop:
        return ""
    if stop - start == 1:
        return lower_ascii_character(text[start])
    middle = (start + stop) // 2
    return lower_ascii_range(text, start, middle) + lower_ascii_range(
        text, middle, stop
    )


def lower_ascii(text: str) -> str:
    return lower_ascii_range(text, 0, len(text))


def count_for(tree: CountsTree, key: str) -> int | None:
    remaining = tree
    while remaining is not None:
        if key == remaining.key:
            return remaining.count
        if key < remaining.key:
            remaining = remaining.left
        else:
            remaining = remaining.right
    return None


def increment_count(tree: CountsTree, key: str) -> CountsTree:
    if tree is None:
        return CountsNode(key, 1, None, None)
    if key == tree.key:
        return replace(tree, count=tree.count + 1)
    if key < tree.key:
        return replace(tree, left=increment_count(tree.left, key))
    return replace(tree, right=increment_count(tree.right, key))


def add_label(state: GroupState, label: str) -> GroupState:
    updated = increment_count(state[0], label)
    if count_for(state[0], label) is None:
        return (updated, Link(label, state[1]))
    return (updated, state[1])


def group_labels(labels: tuple[str, ...]) -> GroupState:
    initial: GroupState = (None, None)
    accumulated = fold_tuple(add_label, initial, labels)
    return (accumulated[0], reverse_chain(accumulated[1]))
