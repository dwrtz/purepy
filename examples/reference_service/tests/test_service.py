"""Exercise verified application behavior over actual localhost sockets."""

import asyncio
import threading
import unittest

from app.http_parse import decimal, header_end, parse_request
from app.router import route
from host.database import Database
from host.main import Service


async def request(port, path, method="GET"):
    reader, writer = await asyncio.open_connection("127.0.0.1", port)
    writer.write(f"{method} {path} HTTP/1.1\r\nHost: localhost\r\n\r\n".encode())
    await writer.drain()
    try:
        return await asyncio.wait_for(reader.read(), 5.0)
    finally:
        writer.close()
        await writer.wait_closed()


class ParsingTests(unittest.TestCase):
    def test_parse_and_routes(self):
        for path, method, code in [("/health", "GET", 1), ("/users/1", "GET", 2),
                                   ("/users/1/increment/5", "POST", 3),
                                   ("/events", "GET", 4), ("/other", "GET", 404),
                                   ("/users/-1", "GET", 400),
                                   ("/users/1/increment/no", "POST", 400)]:
            with self.subTest(path=path):
                parsed = parse_request(f"{method} {path} HTTP/1.1\r\n\r\n".encode())
                self.assertEqual(route(parsed).code, code)

    def test_malformed_requests_are_values(self):
        for data in (b"", b"GET / HTTP/1.1", b"GET / HTTP/1.0\r\n\r\n",
                     b"GET  / HTTP/1.1\r\n\r\n", b"GET /\x01 HTTP/1.1\r\n\r\n",
                     b"GET /\xff HTTP/1.1\r\n\r\n", b"PUT / HTTP/1.1\r\n\r\n",
                     b"GET missing-slash HTTP/1.1\r\n\r\n"):
            with self.subTest(data=data):
                self.assertFalse(parse_request(data).valid)
        self.assertEqual(header_end(b"GET / HTTP/1.1\r\n\r\nrest"), 18)
        self.assertEqual(decimal(b"999999999"), 999999999)
        for data in (b"", b"1234567890", b"-1", b"1x"):
            self.assertEqual(decimal(data), -1)


class ServiceTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.database = Database(delay=0.04)
        self.service = Service(self.database)
        self.port = await self.service.start()

    async def asyncTearDown(self):
        if self.service.server is not None:
            await self.service.close()
            self.service.server = None

    async def test_health_lookup_and_errors(self):
        health = await request(self.port, "/health")
        self.assertTrue(health.startswith(b"HTTP/1.1 200"))
        self.assertTrue(health.endswith(b"ok\n"))
        self.assertIn(b"name=Ada balance=0", await request(self.port, "/users/1"))
        self.assertIn(b"404 Not Found", await request(self.port, "/users/99"))
        self.assertIn(b"400 Bad Request", await request(self.port, "/users/no"))
        self.assertIn(b"404 Not Found", await request(self.port, "/missing"))
        self.assertEqual(self.service.errors, [])

    async def test_fragmented_request(self):
        reader, writer = await asyncio.open_connection("127.0.0.1", self.port)
        writer.write(b"GET /hea")
        await writer.drain()
        await asyncio.sleep(0.01)
        writer.write(b"lth HTTP/1.1\r\nHost: localhost\r\n\r\n")
        await writer.drain()
        response = await asyncio.wait_for(reader.read(), 2.0)
        writer.close()
        await writer.wait_closed()
        self.assertTrue(response.endswith(b"ok\n"))

    async def test_large_header_bounded(self):
        reader, writer = await asyncio.open_connection("127.0.0.1", self.port)
        writer.write(b"GET /health HTTP/1.1\r\nX: " + b"a" * 9000 + b"\r\n\r\n")
        await writer.drain()
        response = await asyncio.wait_for(reader.read(), 2.0)
        writer.close()
        await writer.wait_closed()
        self.assertIn(b"431 Request Header Fields Too Large", response)

    async def test_reads_overlap_across_host_invocations(self):
        responses = await asyncio.gather(*(request(self.port, "/users/1") for _ in range(12)))
        self.assertTrue(all(b"200 OK" in result for result in responses))
        self.assertGreater(self.database.max_active_reads, 1)
        self.assertGreater(self.service.max_active, 1)

    async def test_atomic_updates_under_concurrency(self):
        responses = await asyncio.gather(*(
            request(self.port, "/users/1/increment/1", "POST") for _ in range(24)
        ))
        self.assertTrue(all(b"200 OK" in result for result in responses))
        self.assertIn(b"balance=24\n", await request(self.port, "/users/1"))
        balances = sorted(int(result.rsplit(b"balance=", 1)[1]) for result in responses)
        self.assertEqual(balances, list(range(1, 25)))
        self.assertEqual(self.database.connection.execute("SELECT count(*) FROM events").fetchone()[0], 24)
        self.assertIn(b"400 Bad Request", await request(self.port, "/users/1/increment/1001", "POST"))
        self.assertIn(b"balance=24\n", await request(self.port, "/users/1"))

    async def test_update_rolls_back_if_event_insert_fails(self):
        self.database.connection.execute("""
            CREATE TRIGGER deny_events BEFORE INSERT ON events BEGIN
                SELECT RAISE(ABORT, 'injected storage error');
            END
        """)
        response = await request(self.port, "/users/1/increment/10", "POST")
        self.assertIn(b"503 Service Unavailable", response)
        self.assertIn(b"balance=0\n", await request(self.port, "/users/1"))

    async def test_expected_database_failure_is_a_value(self):
        self.database.connection.execute("DROP TABLE users")
        self.assertIn(b"503 Service Unavailable", await request(self.port, "/users/1"))
        self.assertEqual(self.service.errors, [])

    async def test_cancelled_write_finishes_before_database_closes(self):
        started = threading.Event()
        release = threading.Event()
        results = []
        original = self.database._update

        def blocked_update(plan):
            started.set()
            if not release.wait(3.0):
                raise TimeoutError("test did not release database worker")
            result = original(plan)
            results.append(result)
            return result

        self.database._update = blocked_update
        caller = asyncio.create_task(request(self.port, "/users/1/increment/7", "POST"))
        self.assertTrue(await asyncio.to_thread(started.wait, 2.0))
        closing = asyncio.create_task(self.service.close())
        await asyncio.sleep(0.01)
        self.assertFalse(closing.done())
        release.set()
        await asyncio.wait_for(closing, 3.0)
        self.service.server = None
        self.assertEqual(await caller, b"")
        self.assertEqual(len(results), 1)
        self.assertTrue(results[0].applied)
        self.assertEqual(results[0].balance, 7)
        self.assertEqual(self.service.cancelled, 1)

    async def test_sse_repeated_events_and_host_cancellation(self):
        reader, writer = await asyncio.open_connection("127.0.0.1", self.port)
        writer.write(b"GET /events HTTP/1.1\r\nHost: localhost\r\n\r\n")
        await writer.drain()
        header = await asyncio.wait_for(reader.readuntil(b"\r\n\r\n"), 2.0)
        self.assertIn(b"Content-Type: text/event-stream", header)
        for amount, balance in [(2, 2), (3, 5)]:
            await request(self.port, f"/users/1/increment/{amount}", "POST")
            event = b""
            for _ in range(20):
                event = await asyncio.wait_for(reader.readuntil(b"\n\n"), 2.0)
                if b"event: balance" in event:
                    break
            self.assertIn(f"balance={balance}".encode(), event)
        await asyncio.wait_for(self.service.close(), 3.0)
        self.service.server = None
        self.assertGreaterEqual(self.service.cancelled, 1)
        self.assertEqual(self.service.tasks, set())
        await asyncio.wait_for(reader.read(), 2.0)
        self.assertTrue(reader.at_eof())
        writer.close()
        await writer.wait_closed()


if __name__ == "__main__":
    unittest.main()
