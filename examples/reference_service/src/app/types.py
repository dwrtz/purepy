from purepy import value


@value
class Request:
    valid: bool
    method: bytes
    path: bytes


@value
class Route:
    code: int
    user_id: int
    amount: int


@value
class User:
    user_id: int
    name: str
    balance: int


@value
class UserResult:
    found: bool
    user: User
    error: str


@value
class UpdatePlan:
    user_id: int
    amount: int


@value
class UpdateResult:
    applied: bool
    balance: int
    error: str


@value
class Event:
    event_id: int
    user_id: int
    balance: int


@value
class EventPage:
    events: tuple[Event, ...]
