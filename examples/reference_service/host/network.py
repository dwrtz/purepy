"""Opaque connection references and explicit socket operations."""

import asyncio


class NetworkRead:
    __slots__ = ()


class NetworkWrite:
    __slots__ = ()


class Connection:
    __slots__ = ("reader", "writer")

    def __init__(self, reader: asyncio.StreamReader, writer: asyncio.StreamWriter):
        self.reader = reader
        self.writer = writer


async def receive_bytes(network_read: NetworkRead, connection: Connection) -> bytes:
    if type(network_read) is not NetworkRead or type(connection) is not Connection:
        raise TypeError("NetworkRead and Connection required")
    try:
        return await connection.reader.read(4096)
    except (ConnectionError, OSError):
        return b""


async def send_bytes(network_write: NetworkWrite, connection: Connection, data: bytes) -> bool:
    if type(network_write) is not NetworkWrite or type(connection) is not Connection:
        raise TypeError("NetworkWrite and Connection required")
    if connection.reader.at_eof() or connection.writer.is_closing():
        return False
    try:
        connection.writer.write(data)
        await connection.writer.drain()
        return True
    except (ConnectionError, OSError):
        return False
