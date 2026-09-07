from typing import NamedTuple


class Request(NamedTuple):
    valid: bool
    method: bytes
    path: bytes


class Route(NamedTuple):
    code: int
    user_id: int
    amount: int


class User(NamedTuple):
    user_id: int
    name: str
    balance: int


class UserResult(NamedTuple):
    found: bool
    user: User
    error: str


class UpdatePlan(NamedTuple):
    user_id: int
    amount: int


class UpdateResult(NamedTuple):
    applied: bool
    balance: int
    error: str


class Event(NamedTuple):
    event_id: int
    user_id: int
    balance: int


class EventPage(NamedTuple):
    events: tuple[Event, ...]
