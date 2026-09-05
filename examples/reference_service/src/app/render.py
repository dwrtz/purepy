from app.types import Event, UpdateResult, UserResult
from host.text import encode_utf8


def response(status: bytes, body: bytes) -> bytes:
    return b"HTTP/1.1 " + status + b"\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Length: " + encode_utf8(str(len(body))) + b"\r\nConnection: close\r\n\r\n" + body


def render_user(result: UserResult) -> bytes:
    if result.error != "":
        return response(b"503 Service Unavailable", b"database unavailable\n")
    if result.found == False:
        return response(b"404 Not Found", b"user not found\n")
    user = result.user
    body = "user=" + str(user.user_id) + " name=" + user.name + " balance=" + str(user.balance) + "\n"
    return response(b"200 OK", encode_utf8(body))


def render_update(result: UpdateResult) -> bytes:
    if result.error != "":
        return response(b"503 Service Unavailable", b"database unavailable\n")
    if result.applied == False:
        return response(b"404 Not Found", b"user not found\n")
    return response(b"200 OK", encode_utf8("balance=" + str(result.balance) + "\n"))


def render_event(event: Event) -> bytes:
    body = "id: " + str(event.event_id) + "\nevent: balance\ndata: user=" + str(event.user_id) + " balance=" + str(event.balance) + "\n\n"
    return encode_utf8(body)
