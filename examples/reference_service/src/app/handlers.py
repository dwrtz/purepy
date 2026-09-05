from app.domain import valid_update
from app.render import render_event, render_update, render_user, response
from app.types import UpdatePlan
from host.clock import ClockWait, sleep
from host.database import DatabaseRead, DatabaseWrite, execute_update, load_event_page, load_user
from host.network import Connection, NetworkWrite, send_bytes


async def lookup(database_read: DatabaseRead, user_id: int) -> bytes:
    result = await load_user(database_read, user_id)
    return render_user(result)


async def update(database_write: DatabaseWrite, user_id: int, amount: int) -> bytes:
    plan = UpdatePlan(user_id, amount)
    if valid_update(plan) == False:
        return response(b"400 Bad Request", b"increment must be between 1 and 1000\n")
    result = await execute_update(database_write, plan)
    return render_update(result)


async def events(database_read: DatabaseRead, network_write: NetworkWrite, clock_wait: ClockWait, connection: Connection) -> int:
    active = await send_bytes(network_write, connection, b"HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nCache-Control: no-cache\r\nConnection: close\r\n\r\n")
    cursor = 0
    while active:
        page = await load_event_page(database_read, cursor)
        for event in page.events:
            active = await send_bytes(network_write, connection, render_event(event))
            if active == False:
                break
            cursor = event.event_id
        if active:
            active = await send_bytes(network_write, connection, b": keep-alive\n\n")
        if active:
            await sleep(clock_wait, 0.05)
    return cursor
