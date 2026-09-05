"""Bounded load statistics, exact SSE correlation, and localhost cleanup."""

import asyncio
from contextlib import redirect_stderr, redirect_stdout
from dataclasses import replace
import io
import json
from pathlib import Path
import sqlite3
import tempfile
import time
import tracemalloc
import unittest
from unittest.mock import AsyncMock, patch

from host.database import Database
from host.main import Service
from loadtest import load


def event(identifier, balance=None):
    balance = identifier if balance is None else balance
    return f"id: {identifier}\nevent: balance\ndata: user=1 balance={balance}\n\n".encode()


class StatisticsTests(unittest.TestCase):
    def test_latency_reservoir_is_bounded_with_exact_counts_and_extrema(self):
        sample = load.Distribution(capacity=8)
        for index in range(10_000):
            sample.add(index / 1000)
        report = sample.report()
        self.assertEqual(len(sample.values), 8)
        self.assertEqual(report['count'], 10_000)
        self.assertEqual(report['min'], 0)
        self.assertEqual(report['max'], 9999)
        self.assertEqual(report['mean'], 4999.5)
        self.assertEqual(load.Distribution().report()['max'], None)

    def test_error_examples_are_bounded_but_counts_are_not_lost(self):
        errors = load.Errors()
        for _ in range(100):
            errors.add('read', RuntimeError('injected'))
        self.assertEqual(errors.count, 100)
        self.assertEqual(len(errors.examples), load.MAX_ERRORS)
        self.assertEqual(errors.by_operation['read'], 100)

    def test_cli_options_have_finite_resource_bounds(self):
        for changes in ({'duration': float('nan')}, {'duration': 0}, {'duration': 3601},
                        {'connections': 129}, {'connections': True}, {'sse_connections': -1},
                        {'write_every': 1}, {'requests': 0}, {'sample_interval': 0.001},
                        {'database_delay': float('inf')}, {'timeout': 0}):
            with self.subTest(changes=changes), self.assertRaises(ValueError):
                load.options_from(replace(load.Options(), **changes))
        for flag, value in (('--duration', 'nan'), ('--connections', '129')):
            with self.subTest(flag=flag), redirect_stderr(io.StringIO()), self.assertRaises(SystemExit) as error:
                load.main([flag, value])
            self.assertEqual(error.exception.code, 2)

    def test_cli_preserves_failed_json_report_and_nonzero_status(self):
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / 'nested' / 'report.json'
            with patch.object(load, 'run', new=AsyncMock(return_value={'ok': False})), redirect_stdout(io.StringIO()):
                status = load.main(['--output', str(output)])
            self.assertEqual(status, 1)
            self.assertEqual(json.loads(output.read_text()), {'ok': False, 'verifier_milliseconds': None})


class EventTests(unittest.IsolatedAsyncioTestCase):
    async def test_arrival_before_acknowledgment_is_measured_once(self):
        events = load.Events(2)
        events.frame(0, event(1), 1.2)
        self.assertEqual(events.delivery.count, 0)
        events.acknowledge(1, 1.0, 1.4)
        events.frame(1, event(1), 1.5)
        await events.drain(.1)
        self.assertEqual(events.pending, {})
        self.assertEqual(events.delivery.count, 2)
        self.assertAlmostEqual(events.delivery.total, .7)
        self.assertEqual(events.early_deliveries, 1)
        self.assertEqual(events.counts, [1, 1])

    async def test_out_of_order_http_responses_keep_contiguous_event_identity(self):
        events = load.Events(1)
        events.acknowledge(2, 1.0, 1.2)
        events.frame(0, event(1), 1.3)
        events.frame(0, event(2), 1.4)
        self.assertEqual(events.ack_floor, 0)
        events.acknowledge(1, .9, 1.5)
        await events.drain(.1)
        self.assertEqual(events.ack_floor, 2)
        self.assertEqual(events.delivery.count, 2)
        with self.assertRaisesRegex(RuntimeError, 'duplicate write'):
            events.acknowledge(1, 2, 3)

    async def test_event_before_write_dispatch_is_not_a_valid_delivery(self):
        events = load.Events(1)
        events.frame(0, event(1), .5)
        with self.assertRaisesRegex(RuntimeError, 'before its matching write'):
            events.acknowledge(1, 1.0, 1.4)
        self.assertEqual(events.delivery.count, 0)
        self.assertEqual(events.after_ack.count, 0)
        self.assertEqual(events.early_deliveries, 0)

    async def test_keepalives_and_stale_events_cannot_satisfy_update_delivery(self):
        events = load.Events(1)
        events.acknowledge(1, 1, 2)
        events.frame(0, b': keep-alive\n\n', 2.1)
        with self.assertRaises(TimeoutError):
            await events.drain(.05)
        self.assertEqual(events.counts, [0])
        events.frame(0, event(1), 2.2)
        for bad in (event(1), event(3), event(2, 1), b'event: balance\n\n'):
            with self.subTest(bad=bad), self.assertRaises(RuntimeError):
                events.frame(0, bad, 2.3)

    async def test_pending_ledger_fails_instead_of_growing_without_bound(self):
        with patch.object(load, 'MAX_PENDING_EVENTS', 2):
            events = load.Events(1)
            events.acknowledge(1, 0, 1)
            events.acknowledge(2, 0, 1)
            with self.assertRaisesRegex(RuntimeError, 'backlog'):
                events.acknowledge(3, 0, 1)
            self.assertEqual(len(events.pending), 2)

    async def test_memory_history_keeps_bounded_history_and_full_sample_count(self):
        snapshot = {'event_rows': 0, 'balance': 0, 'page_count': 4, 'page_size': 4096}
        with patch.object(load, 'MEMORY_HISTORY', 4), patch.object(load, 'database_snapshot', return_value=snapshot), patch.object(load, 'current_rss', return_value=123456):
            samples = load.MemorySamples(time.perf_counter())
            for _ in range(12):
                await samples.take(None, 'workload')
        report = samples.report()
        self.assertEqual(report['samples_taken'], 12)
        self.assertEqual(len(report['history']), 4)
        self.assertEqual(report['first']['resident_bytes'], 123456)
        self.assertEqual(report['last']['sqlite_page_bytes'], 16384)
        self.assertNotEqual(report['first']['seconds'], report['last']['seconds'])


class LoadIntegrationTests(unittest.IsolatedAsyncioTestCase):
    def assert_clean(self, report):
        resources = report['resources_after_cleanup']
        for name in ('client_writers_remaining', 'client_tasks_remaining', 'host_tasks_remaining', 'database_reads_remaining'):
            self.assertEqual(resources[name], 0, report)
        self.assertTrue(resources['listener_closed'], report)
        self.assertTrue(resources['database_closed'], report)

    async def test_sustained_mixed_requests_drain_sse_concurrently(self):
        options = load.Options(duration=.3, connections=4, sse_connections=2,
                               database_delay=.005, sample_interval=.05)
        report = await load.run(options)
        self.assertTrue(report['ok'], report['errors'])
        self.assertGreaterEqual(report['request_window_seconds'], .3)
        self.assertGreater(report['operations']['read']['succeeded'], 0)
        writes = report['operations']['write']['succeeded']
        self.assertGreater(writes, 0)
        self.assertEqual(report['requests'], report['operations']['read']['succeeded'] + writes)
        self.assertEqual(report['sse']['events_per_stream'], [writes, writes])
        self.assertEqual(report['sse']['write_start_to_event_ms']['count'], writes * 2)
        self.assertEqual(report['sse']['pending_events'], 0)
        self.assertGreater(report['sse']['heartbeat_interval_ms']['count'], 0)
        self.assertEqual(report['database_final']['balance'], writes)
        self.assertEqual(report['database_final']['event_rows'], writes)
        self.assertGreater(report['observed_overlapping_database_reads'], 1)
        self.assertGreaterEqual(report['memory']['samples_taken'], 4)
        expected = report['requests'] / report['request_window_seconds']
        self.assertAlmostEqual(report['throughput_requests_per_second'], expected, delta=.1)
        self.assert_clean(report)

    async def test_fixed_count_default_and_no_streams(self):
        report = await load.run(load.Options(requests=20, connections=3, sse_connections=0, database_delay=.001))
        self.assertTrue(report['ok'], report['errors'])
        self.assertEqual(report['requests'], 20)
        self.assertEqual(report['operations']['read']['succeeded'], 16)
        self.assertEqual(report['operations']['write']['succeeded'], 4)
        self.assertEqual(report['sse_events_received'], 0)
        self.assert_clean(report)

    async def test_request_failure_is_reported_after_full_cleanup(self):
        async def failed_request(*_args, **_kwargs):
            raise RuntimeError('injected request failure')
        report = await load.run(load.Options(requests=20, connections=3, sse_connections=1), request_fn=failed_request)
        self.assertFalse(report['ok'])
        self.assertGreater(report['operations']['read']['failed'], 0)
        self.assertIn('injected request failure', str(report['errors']))
        self.assert_clean(report)

    async def test_impossible_read_is_not_accepted_as_fresh(self):
        async def impossible_read(*_args, **_kwargs):
            return b'user=1 name=Ada balance=999999\n'
        report = await load.run(load.Options(requests=1, connections=1, sse_connections=0), request_fn=impossible_read)
        self.assertFalse(report['ok'])
        self.assertEqual(report['operations']['read']['failed'], 1)
        self.assert_clean(report)

    async def test_failed_sse_handshake_closes_owned_socket(self):
        class BadHandshake(Service):
            async def _invoke(self, reader, writer):
                try:
                    await reader.readuntil(b'\r\n\r\n')
                    writer.write(b'HTTP/1.1 503 Unavailable\r\n\r\n')
                    await writer.drain()
                finally:
                    writer.close()
                    await writer.wait_closed()
        report = await load.run(load.Options(requests=5, sse_connections=1),
                                service_factory=lambda: BadHandshake(Database()))
        self.assertFalse(report['ok'])
        self.assertEqual(report['requests'], 0)
        self.assertIn('invalid SSE handshake', str(report['errors']))
        self.assert_clean(report)

    async def test_failed_listener_start_still_closes_database(self):
        class FailedStart(Service):
            async def start(self):
                raise OSError('injected listener failure')
        report = await load.run(load.Options(), service_factory=lambda: FailedStart(Database()))
        self.assertFalse(report['ok'])
        self.assertEqual(report['requests'], 0)
        self.assert_clean(report)

    async def test_cancellation_closes_listener_invocations_and_database(self):
        service = Service(Database(delay=.05))
        request_started = asyncio.Event()
        tracing_before = tracemalloc.is_tracing()

        async def observed_request(*args, **kwargs):
            request_started.set()
            return await load.request(*args, **kwargs)

        task = asyncio.create_task(load.run(load.Options(duration=10, connections=3, sse_connections=1),
                                            service_factory=lambda: service, request_fn=observed_request))
        await asyncio.wait_for(request_started.wait(), 2)
        task.cancel()
        with self.assertRaises(asyncio.CancelledError):
            await asyncio.wait_for(task, 3)
        self.assertEqual(service.tasks, set())
        self.assertEqual(service.database.active_reads, 0)
        self.assertFalse(service.server.is_serving())
        with self.assertRaises(sqlite3.ProgrammingError):
            service.database.connection.execute('SELECT 1')
        self.assertEqual(tracemalloc.is_tracing(), tracing_before)


if __name__ == '__main__':
    unittest.main()
