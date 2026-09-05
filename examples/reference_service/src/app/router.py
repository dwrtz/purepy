from app.http_parse import decimal
from app.types import Request, Route


def route(request: Request) -> Route:
    if request.valid == False:
        return Route(400, 0, 0)
    if request.method == b"GET" and request.path == b"/health":
        return Route(1, 0, 0)
    if request.method == b"GET" and request.path == b"/events":
        return Route(4, 0, 0)
    if request.path[:7] == b"/users/":
        rest = request.path[7:]
        if request.method == b"GET":
            identifier = decimal(rest)
            if identifier > 0:
                return Route(2, identifier, 0)
            return Route(400, 0, 0)
        separator = -1
        for offset in range(len(rest)):
            if rest[offset] == 47:
                separator = offset
                break
        if separator > 0 and rest[separator:separator + 11] == b"/increment/":
            identifier = decimal(rest[:separator])
            amount = decimal(rest[separator + 11:])
            if identifier > 0 and amount > 0:
                return Route(3, identifier, amount)
        return Route(400, 0, 0)
    return Route(404, 0, 0)
