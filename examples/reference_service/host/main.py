"""Run from the repository root with make serve (uv-managed Python 3.14)."""

import argparse
import asyncio
import contextlib

from app.server import handle_connection
from host.clock import ClockWait
from host.database import Database, DatabaseRead, DatabaseWrite
from host.network import Connection, NetworkRead, NetworkWrite


class Service:
    """Host-only testable listener; all application choices live under src/."""

    def __init__(self, database):
        self.database = database
        self.server = None
        self.tasks = set()
        self.errors = []
        self.completed = 0
        self.cancelled = 0
        self.max_active = 0

    async def start(self, address="127.0.0.1", port=0):
        self.server = await asyncio.start_server(self._accept, address, port)
        return self.server.sockets[0].getsockname()[1]

    def _accept(self, reader, writer):
        task = asyncio.create_task(self._invoke(reader, writer))
        self.tasks.add(task)
        self.max_active = max(self.max_active, len(self.tasks))
        task.add_done_callback(self.tasks.discard)

    async def _invoke(self, reader, writer):
        try:
            await handle_connection(
                NetworkRead(), NetworkWrite(), DatabaseRead(self.database),
                DatabaseWrite(self.database), ClockWait(), Connection(reader, writer),
            )
            self.completed += 1
        except asyncio.CancelledError:
            self.cancelled += 1
            raise
        except Exception as error:
            # The trusted supervisor observes invocation failure, without exposing
            # exception objects or tracebacks to verified application code.
            self.errors.append(type(error).__name__)
        finally:
            writer.close()
            with contextlib.suppress(ConnectionError, OSError):
                await writer.wait_closed()

    async def close(self):
        if self.server is not None:
            self.server.close()
        tasks = list(self.tasks)
        for task in tasks:
            task.cancel()
        await asyncio.gather(*tasks, return_exceptions=True)
        self.tasks.clear()
        # Python 3.13+ waits for active transports as well as the listener.
        # Cancel invocations first so their finalizers close those transports.
        if self.server is not None:
            await self.server.wait_closed()
        await asyncio.to_thread(self.database.close)


async def run(address, port, database_path, delay):
    service = Service(Database(database_path, delay))
    bound_port = await service.start(address, port)
    print(f"PurePy reference service listening on http://{address}:{bound_port}", flush=True)
    try:
        await service.server.serve_forever()
    finally:
        await service.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--address", default="127.0.0.1")
    parser.add_argument("--port", type=int, default=8080)
    parser.add_argument("--database", default=":memory:")
    parser.add_argument("--database-delay", type=float, default=0.0)
    args = parser.parse_args()
    try:
        asyncio.run(run(args.address, args.port, args.database, args.database_delay))
    except KeyboardInterrupt:
        pass


if __name__ == "__main__":
    main()
