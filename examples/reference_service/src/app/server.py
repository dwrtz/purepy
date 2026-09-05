from app.handlers import events, lookup, update
from app.http_parse import header_end, parse_request
from app.render import response
from app.router import route
from host.clock import ClockWait
from host.database import DatabaseRead, DatabaseWrite
from host.network import Connection, NetworkRead, NetworkWrite, receive_bytes, send_bytes


async def handle_connection(network_read: NetworkRead, network_write: NetworkWrite, database_read: DatabaseRead, database_write: DatabaseWrite, clock_wait: ClockWait, connection: Connection) -> int:
    data = b""
    while header_end(data) == 0 and len(data) <= 8192:
        chunk = await receive_bytes(network_read, connection)
        if len(chunk) == 0:
            return 0
        data = data + chunk
    if len(data) > 8192:
        oversized = await send_bytes(network_write, connection, response(b"431 Request Header Fields Too Large", b"request too large\n"))
        if oversized:
            return 431
        return 0
    request = parse_request(data)
    selected = route(request)
    if selected.code == 4:
        return await events(database_read, network_write, clock_wait, connection)
    body = b""
    if selected.code == 1:
        body = response(b"200 OK", b"ok\n")
    elif selected.code == 2:
        body = await lookup(database_read, selected.user_id)
    elif selected.code == 3:
        body = await update(database_write, selected.user_id, selected.amount)
    elif selected.code == 400:
        body = response(b"400 Bad Request", b"bad request\n")
    else:
        body = response(b"404 Not Found", b"route not found\n")
    sent = await send_bytes(network_write, connection, body)
    if sent:
        return selected.code
    return 0
