"""Bounded mixed-load/SSE measurements of one local client+host+SQLite process."""

import argparse
import asyncio
from collections import Counter, deque
from dataclasses import dataclass
import json
import math
import os
from pathlib import Path
import platform
import random
import re
import resource
import sqlite3
import subprocess
import time
import tracemalloc

from host.database import Database
from host.main import Service


MAX_ERRORS = 20
MAX_PENDING_EVENTS = 8192
RESERVOIR_SIZE = 2048
MEMORY_HISTORY = 1200
SSE_EVENT = re.compile(rb"id: ([1-9][0-9]*)\nevent: balance\ndata: user=1 balance=([1-9][0-9]*)\n\n\Z")
USER_BODY = re.compile(rb"user=1 name=Ada balance=([0-9]+)\n\Z")
UPDATE_BODY = re.compile(rb"balance=([1-9][0-9]*)\n\Z")


@dataclass(frozen=True)
class Options:
    requests: int = 100
    connections: int = 20
    sse_connections: int = 2
    database_delay: float = 0.01
    duration: float | None = None
    write_every: int = 5
    sample_interval: float = 1.0
    timeout: float = 5.0
    drain_timeout: float = 10.0


def options_from(args):
    options = Options(**{name: getattr(args, name, field.default)
                         for name, field in Options.__dataclass_fields__.items()})
    for name, minimum, maximum in (("requests", 1, 1_000_000), ("connections", 1, 128),
                                   ("sse_connections", 0, 32), ("write_every", 2, 100)):
        value = getattr(options, name)
        if type(value) is not int or not minimum <= value <= maximum:
            raise ValueError(f"{name} must be an integer between {minimum} and {maximum}")
    for name, minimum, maximum in (("database_delay", 0, 1), ("sample_interval", 0.05, 60),
                                   ("timeout", 0.05, 60), ("drain_timeout", 0.05, 120)):
        value = getattr(options, name)
        if type(value) not in (int, float) or not math.isfinite(value) or not minimum <= value <= maximum:
            raise ValueError(f"{name} must be finite and between {minimum} and {maximum}")
    if options.duration is not None and (type(options.duration) not in (int, float)
                                        or not math.isfinite(options.duration)
                                        or not 0.05 <= options.duration <= 3600):
        raise ValueError("duration must be finite and between 0.05 and 3600 seconds")
    return options


class Distribution:
    """Exact totals/extrema and bounded deterministic reservoir percentiles."""

    def __init__(self, capacity=RESERVOIR_SIZE):
        self.capacity, self.count, self.total = capacity, 0, 0.0
        self.values = []
        self.minimum = self.maximum = None
        self.random = random.Random(0)

    def add(self, value):
        self.count += 1
        self.total += value
        self.minimum = value if self.minimum is None else min(self.minimum, value)
        self.maximum = value if self.maximum is None else max(self.maximum, value)
        if len(self.values) < self.capacity:
            self.values.append(value)
        else:
            index = self.random.randrange(self.count)
            if index < self.capacity:
                self.values[index] = value

    def report(self):
        ordered = sorted(self.values)

        def percentile(fraction):
            return round(ordered[max(0, math.ceil(len(ordered) * fraction) - 1)] * 1000, 3) if ordered else None

        return {"count": self.count, "sample_count": len(ordered),
                "percentiles": "bounded reservoir; totals/min/max are exact",
                "mean": round(self.total / self.count * 1000, 3) if self.count else None,
                "min": round(self.minimum * 1000, 3) if self.count else None,
                "median": percentile(0.5), "p95": percentile(0.95), "p99": percentile(0.99),
                "max": round(self.maximum * 1000, 3) if self.count else None}


class Errors:
    def __init__(self):
        self.count, self.examples, self.by_operation = 0, [], Counter()

    def add(self, operation, error):
        self.count += 1
        self.by_operation[operation] += 1
        if len(self.examples) < MAX_ERRORS:
            self.examples.append({"operation": operation, "error": f"{type(error).__name__}: {error}"[:500]})

    def report(self):
        return {"count": self.count, "by_operation": dict(self.by_operation),
                "examples": self.examples, "example_limit": MAX_ERRORS}


def current_rss():
    """Current process residency; ru_maxrss is a separate lifetime high-water."""
    try:
        if platform.system() == "Linux":
            return int(Path("/proc/self/statm").read_text().split()[1]) * os.sysconf("SC_PAGE_SIZE")
        result = subprocess.run(["ps", "-o", "rss=", "-p", str(os.getpid())],
                                capture_output=True, text=True, timeout=2, check=True)
        return int(result.stdout.strip()) * 1024
    except (OSError, ValueError, subprocess.SubprocessError):
        return None


def peak_rss():
    value = resource.getrusage(resource.RUSAGE_SELF).ru_maxrss
    return value if platform.system() == "Darwin" else value * 1024


def database_snapshot(database):
    with database.lock:
        return {"event_rows": database.connection.execute("SELECT count(*) FROM events").fetchone()[0],
                "balance": database.connection.execute("SELECT balance FROM users WHERE user_id = 1").fetchone()[0],
                "page_count": database.connection.execute("PRAGMA page_count").fetchone()[0],
                "page_size": database.connection.execute("PRAGMA page_size").fetchone()[0]}


class MemorySamples:
    def __init__(self, started):
        self.started, self.cpu_started = started, time.process_time()
        self.history = deque(maxlen=MEMORY_HISTORY)
        self.count, self.first, self.last = 0, None, None
        self.trends = {name: [0, 0.0, 0.0, 0.0, 0.0]
                       for name in ("resident_bytes", "traced_python_bytes", "sqlite_page_bytes")}

    async def take(self, database, phase):
        snapshot = await asyncio.to_thread(database_snapshot, database)
        rss = await asyncio.to_thread(current_rss)
        sample = {"seconds": round(time.perf_counter() - self.started, 4), "phase": phase,
                  "cpu_seconds": round(time.process_time() - self.cpu_started, 6), "resident_bytes": rss,
                  "traced_python_bytes": tracemalloc.get_traced_memory()[0],
                  "sqlite_page_bytes": snapshot["page_count"] * snapshot["page_size"], **snapshot}
        interval = sample["seconds"] - self.last["seconds"] if self.last else 0
        sample["cpu_percent_of_one_core"] = (round(100 * (sample["cpu_seconds"] - self.last["cpu_seconds"]) / interval, 3)
                                             if interval > 0 else None)
        self.count += 1
        self.history.append(sample)
        self.first, self.last = self.first or sample, sample
        if phase == "workload":
            for name, sums in self.trends.items():
                value = sample[name]
                if value is not None:
                    t = sample["seconds"]
                    sums[0] += 1
                    sums[1] += t
                    sums[2] += value
                    sums[3] += t * t
                    sums[4] += t * value
        return snapshot

    def report(self):
        trends = {}
        for name, (count, t, value, tt, tv) in self.trends.items():
            divisor = count * tt - t * t
            trends[name + "_per_minute"] = round(60 * (count * tv - t * value) / divisor, 2) if divisor > 0 else None
        return {"samples_taken": self.count, "history_limit": MEMORY_HISTORY,
                "history": list(self.history), "first": self.first, "last": self.last,
                "workload_linear_trend": trends,
                "interpretation": "Combined client+host+SQLite process. SQLite retains a row per committed write; page growth is retained workload data. Tracemalloc adds overhead and excludes SQLite/native/socket memory. Trends include warmup and do not establish a leak."}


async def close_writer(writer, registry):
    writer.close()
    try:
        await asyncio.wait_for(writer.wait_closed(), 2.0)
    except (ConnectionError, OSError, TimeoutError):
        pass
    finally:
        registry.discard(writer)


async def request(port, path, method="GET", *, timeout=5.0, registry=None):
    registry = set() if registry is None else registry
    writer = None
    try:
        async with asyncio.timeout(timeout):
            reader, writer = await asyncio.open_connection("127.0.0.1", port, limit=8192)
            registry.add(writer)
            writer.write(f"{method} {path} HTTP/1.1\r\nHost: localhost\r\n\r\n".encode("ascii"))
            await writer.drain()
            header = await reader.readuntil(b"\r\n\r\n")
            if not header.startswith(b"HTTP/1.1 200 OK\r\n"):
                raise RuntimeError(f"unsuccessful request: {header[:120]!r}")
            lengths = [line.split(b":", 1)[1].strip() for line in header.split(b"\r\n")
                       if line.lower().startswith(b"content-length:")]
            if len(lengths) != 1 or not lengths[0].isdigit() or not 0 <= int(lengths[0]) <= 1024:
                raise RuntimeError("invalid HTTP content length")
            body = await reader.readexactly(int(lengths[0]))
            if await reader.read(1):
                raise RuntimeError("unexpected bytes after HTTP response body")
            return body
    finally:
        if writer is not None:
            await close_writer(writer, registry)


class Events:
    """Reconcile bounded acknowledgments with continuously drained SSE."""

    def __init__(self, streams):
        self.streams = streams
        self.last_ids, self.counts = [0] * streams, [0] * streams
        self.last_frames, self.last_heartbeats = [None] * streams, [None] * streams
        self.pending, self.pending_peak = {}, 0
        self.ack_floor, self.ack_max, self.ack_count = 0, 0, 0
        self.acks_ahead = set()
        self.early_deliveries = 0
        self.delivery, self.after_ack = Distribution(), Distribution()
        self.cadence, self.heartbeat_cadence = Distribution(), Distribution()
        self.changed = asyncio.Event()

    def _entry(self, identifier):
        if identifier not in self.pending:
            if len(self.pending) >= MAX_PENDING_EVENTS:
                raise RuntimeError("SSE reconciliation backlog exceeded bounded event ledger")
            self.pending[identifier] = {"start": None, "ack": None, "arrivals": {}}
            self.pending_peak = max(self.pending_peak, len(self.pending))
        return self.pending[identifier]

    def _reconcile(self, identifier):
        entry = self.pending[identifier]
        if entry["ack"] is None:
            return
        if any(arrival is not None and arrival < entry["start"] for arrival in entry["arrivals"].values()):
            raise RuntimeError(f"SSE event {identifier} arrived before its matching write was dispatched")
        for arrival in entry["arrivals"].values():
            if arrival is not None:
                self.delivery.add(arrival - entry["start"])
                self.after_ack.add(max(0, arrival - entry["ack"]))
                self.early_deliveries += arrival < entry["ack"]
        entry["arrivals"] = {stream: None for stream in entry["arrivals"]}
        if len(entry["arrivals"]) == self.streams:
            del self.pending[identifier]

    def acknowledge(self, identifier, started, acknowledged):
        if identifier <= self.ack_floor or identifier in self.acks_ahead:
            raise RuntimeError(f"duplicate write acknowledgment for balance/event {identifier}")
        if len(self.acks_ahead) >= MAX_PENDING_EVENTS:
            raise RuntimeError("out-of-order acknowledgments exceeded bounded ledger")
        self.acks_ahead.add(identifier)
        self.ack_max, self.ack_count = max(self.ack_max, identifier), self.ack_count + 1
        while self.ack_floor + 1 in self.acks_ahead:
            self.ack_floor += 1
            self.acks_ahead.remove(self.ack_floor)
        if self.streams:
            entry = self._entry(identifier)
            entry["start"], entry["ack"] = started, acknowledged
            self._reconcile(identifier)
        self.changed.set()

    def frame(self, stream, frame, arrived):
        previous = self.last_frames[stream]
        if previous is not None:
            self.cadence.add(arrived - previous)
        self.last_frames[stream] = arrived
        if frame == b": keep-alive\n\n":
            previous = self.last_heartbeats[stream]
            if previous is not None:
                self.heartbeat_cadence.add(arrived - previous)
            self.last_heartbeats[stream] = arrived
            return
        match = SSE_EVENT.fullmatch(frame)
        if match is None:
            raise RuntimeError(f"malformed SSE event: {frame[:120]!r}")
        identifier, balance = map(int, match.groups())
        if identifier != self.last_ids[stream] + 1 or balance != identifier:
            raise RuntimeError(f"SSE stream {stream} stale, duplicate, skipped, or mismatched event: id={identifier}, balance={balance}, previous={self.last_ids[stream]}")
        self.last_ids[stream], self.counts[stream] = identifier, self.counts[stream] + 1
        self._entry(identifier)["arrivals"][stream] = arrived
        self._reconcile(identifier)
        self.changed.set()

    async def drain(self, timeout):
        async with asyncio.timeout(timeout):
            while self.pending or any(identifier != self.ack_max for identifier in self.last_ids):
                self.changed.clear()
                await self.changed.wait()

    def report(self):
        return {"connections": self.streams, "expected_events_per_stream": self.ack_count,
                "events_per_stream": self.counts, "last_event_ids": self.last_ids,
                "events_received": sum(self.counts), "pending_events": len(self.pending),
                "pending_peak": self.pending_peak, "pending_limit": MAX_PENDING_EVENTS,
                "arrived_before_http_ack": self.early_deliveries,
                "write_start_to_event_ms": self.delivery.report(),
                "nonnegative_ack_to_event_ms": self.after_ack.report(),
                "frame_interval_ms": self.cadence.report(),
                "heartbeat_interval_ms": self.heartbeat_cadence.report(), "poll_interval_ms": 50,
                "identity_rule": "Fresh database, only user 1 increments of 1: every stream must deliver id=balance=1..successful writes exactly once. Keep-alives never satisfy a delivery."}


async def run(args, *, service_factory=None, request_fn=request):
    options = options_from(args)
    session_start, cpu_session_start = time.perf_counter(), time.process_time()
    own_tracing = not tracemalloc.is_tracing()
    if own_tracing:
        tracemalloc.start()
    service, writers, tasks, sampler_task = None, set(), [], None
    stop, sample_stop = asyncio.Event(), asyncio.Event()
    errors, events, memory = Errors(), Events(options.sse_connections), MemorySamples(session_start)
    distributions = {name: Distribution() for name in ("read", "write", "all")}
    operations = {name: {"attempted": 0, "succeeded": 0, "failed": 0} for name in ("read", "write")}
    started = ended = cpu_start = cpu_end = None
    drain_seconds, phase, database_final, resources, issued = 0.0, "startup", None, {}, 0

    async def sse_reader(stream, reader):
        try:
            while True:
                frame = await asyncio.wait_for(reader.readuntil(b"\n\n"), options.timeout)
                events.frame(stream, frame, time.perf_counter())
        except asyncio.CancelledError:
            raise
        except Exception as error:
            errors.add("sse", error)
            stop.set()

    async def sampler():
        while not sample_stop.is_set():
            try:
                await memory.take(service.database, phase)
            except Exception as error:
                errors.add("sampling", error)
                stop.set()
                return
            try:
                await asyncio.wait_for(sample_stop.wait(), options.sample_interval)
            except TimeoutError:
                pass

    async def worker():
        nonlocal issued
        while not stop.is_set():
            if options.duration is not None:
                if time.perf_counter() >= started + options.duration:
                    return
            elif issued >= options.requests:
                return
            number, issued = issued, issued + 1
            operation = "write" if (number + 1) % options.write_every == 0 else "read"
            operations[operation]["attempted"] += 1
            watermark, request_start = events.ack_max, time.perf_counter()
            try:
                if operation == "write":
                    body = await request_fn(port, "/users/1/increment/1", "POST", timeout=options.timeout, registry=writers)
                    match = UPDATE_BODY.fullmatch(body)
                    if match is None:
                        raise RuntimeError(f"malformed update response: {body[:120]!r}")
                    events.acknowledge(int(match[1]), request_start, time.perf_counter())
                else:
                    body = await request_fn(port, "/users/1", timeout=options.timeout, registry=writers)
                    match = USER_BODY.fullmatch(body)
                    if (match is None or not watermark <= int(match[1]) <= operations["write"]["attempted"]):
                        raise RuntimeError(f"stale, impossible, or malformed read after acknowledged balance {watermark}: {body[:120]!r}")
            except asyncio.CancelledError:
                raise
            except Exception as error:
                operations[operation]["failed"] += 1
                errors.add(operation, error)
                stop.set()
            else:
                operations[operation]["succeeded"] += 1
            finally:
                elapsed = time.perf_counter() - request_start
                distributions[operation].add(elapsed)
                distributions["all"].add(elapsed)

    try:
        service = service_factory() if service_factory else Service(Database(delay=options.database_delay))
        port = await service.start()
        for stream in range(options.sse_connections):
            async with asyncio.timeout(options.timeout):
                reader, writer = await asyncio.open_connection("127.0.0.1", port, limit=8192)
                writers.add(writer)  # Own it before a possibly failing handshake.
                writer.write(b"GET /events HTTP/1.1\r\nHost: localhost\r\n\r\n")
                await writer.drain()
                header = await reader.readuntil(b"\r\n\r\n")
                if not header.startswith(b"HTTP/1.1 200 OK\r\n") or b"Content-Type: text/event-stream\r\n" not in header:
                    raise RuntimeError("invalid SSE handshake")
            tasks.append(asyncio.create_task(sse_reader(stream, reader)))
        await memory.take(service.database, "startup")
        started, cpu_start, phase = time.perf_counter(), time.process_time(), "workload"
        sampler_task = asyncio.create_task(sampler())
        tasks.append(sampler_task)
        workers = [asyncio.create_task(worker()) for _ in range(options.connections)]
        tasks.extend(workers)
        await asyncio.gather(*workers)
        ended, cpu_end, phase = time.perf_counter(), time.process_time(), "drain"
        drain_start = time.perf_counter()
        if not stop.is_set():
            try:
                await events.drain(options.drain_timeout)
            except TimeoutError as error:
                errors.add("sse_drain", error)
        drain_seconds = time.perf_counter() - drain_start
        database_final = await memory.take(service.database, "drain")
        writes = operations["write"]["succeeded"]
        if database_final["balance"] != writes or database_final["event_rows"] != writes or events.ack_floor != writes:
            errors.add("ledger", RuntimeError(f"committed balance/events/acknowledgments disagree with {writes} writes: {database_final}, ack_floor={events.ack_floor}"))
        if any(identifier != writes for identifier in events.last_ids) or events.pending:
            errors.add("sse_ledger", RuntimeError("SSE readers did not receive every acknowledged update exactly once"))
    except asyncio.CancelledError:
        raise
    except Exception as error:
        errors.add(phase, error)
    finally:
        cleanup_start = time.perf_counter()
        stop.set()
        sample_stop.set()
        for task in tasks:
            # A sampler's to_thread snapshot/ps call must finish before SQLite
            # closes; cancelling its coroutine would leave the thread running.
            if task is not sampler_task:
                task.cancel()
        await asyncio.gather(*tasks, return_exceptions=True)
        for writer in list(writers):
            try:
                await close_writer(writer, writers)
            except Exception as error:
                errors.add("client_cleanup", error)
        if service is not None:
            try:
                await service.close()
            except Exception as error:
                errors.add("host_cleanup", error)
            database_closed = False
            try:
                service.database.connection.execute("SELECT 1")
            except sqlite3.ProgrammingError:
                database_closed = True
            resources = {"client_writers_remaining": len(writers),
                         "client_tasks_remaining": sum(not task.done() for task in tasks),
                         "host_tasks_remaining": len(service.tasks),
                         "database_reads_remaining": service.database.active_reads,
                         "listener_closed": service.server is None or not service.server.is_serving(),
                         "database_closed": database_closed,
                         "host_completed": service.completed, "host_cancelled": service.cancelled}
            if (resources["client_writers_remaining"] or resources["client_tasks_remaining"]
                    or resources["host_tasks_remaining"] or resources["database_reads_remaining"]
                    or not resources["listener_closed"] or not database_closed):
                errors.add("resources", RuntimeError(f"resources remain after shutdown: {resources}"))
            for host_error in service.errors[:MAX_ERRORS]:
                errors.add("host", RuntimeError(host_error))
        cleanup_seconds = time.perf_counter() - cleanup_start
        traced_current, traced_peak = tracemalloc.get_traced_memory()
        if own_tracing:
            tracemalloc.stop()

    window = ended - started if ended is not None and started is not None else 0.0
    successful = sum(operation["succeeded"] for operation in operations.values())
    for name, operation in operations.items():
        operation["throughput_per_second"] = round(operation["succeeded"] / window, 3) if window else 0
        operation["latency_ms"] = distributions[name].report()
    return {
        "schema": 2, "ok": errors.count == 0, "python": platform.python_version(),
        "platform": platform.platform(), "mode": "duration" if options.duration is not None else "requests",
        "measurement_scope": "One local process: clients + host + application + SQLite worker threads + tracemalloc. CPU excludes the optional external ps sampler and later verifier processes.",
        "options": {name: getattr(options, name) for name in Options.__dataclass_fields__},
        "requests": issued, "concurrent_connections": options.connections,
        "observed_max_invocations": service.max_active if service else 0,
        "observed_overlapping_database_reads": service.database.max_active_reads if service else 0,
        "request_window_seconds": round(window, 6),
        "startup_seconds": round(started - session_start, 6) if started else None,
        "sse_drain_seconds": round(drain_seconds, 6), "cleanup_seconds": round(cleanup_seconds, 6),
        "session_seconds": round(time.perf_counter() - session_start, 6),
        "throughput_requests_per_second": round(successful / window, 3) if window else 0,
        "latency_definition": "Connection open through full response and client close, after acquiring worker slot. Request window includes in-flight completion after dispatch deadline; excludes startup and final SSE drain.",
        "latency_ms": distributions["all"].report(), "operations": operations,
        "sse": events.report(), "sse_connections": options.sse_connections,
        "sse_events_received": sum(events.counts), "sse_poll_interval_ms": 50,
        "sse_update_delivery_max_ms": events.delivery.report()["max"],
        "database": "SQLite :memory:, worker thread, serialized atomic transactions; append-only event history",
        "database_final": database_final, "simulated_database_latency_ms": options.database_delay * 1000,
        "simulated_database_latency_scope": "Read-user calls only; writes and SSE polls have no artificial delay.",
        "cpu_seconds": round(cpu_end - cpu_start, 6) if cpu_end is not None else 0,
        "cpu_percent_of_one_core": round(100 * (cpu_end - cpu_start) / window, 3) if window else None,
        "session_cpu_seconds": round(time.process_time() - cpu_session_start, 6),
        "peak_resident_bytes": peak_rss(), "peak_resident_scope": "process lifetime high-water mark",
        "peak_traced_python_bytes": traced_peak, "traced_python_bytes_after_cleanup": traced_current,
        "tracing_started_by_harness": own_tracing, "memory": memory.report(),
        "resources_after_cleanup": resources, "errors": errors.report(),
        "host_errors": list(service.errors[:MAX_ERRORS]) if service else [],
        "host_error_count": len(service.errors) if service else 0,
        "stats_bounds": {"latency_reservoir_per_metric": RESERVOIR_SIZE, "memory_history": MEMORY_HISTORY,
                         "error_examples": MAX_ERRORS, "pending_event_ledger": MAX_PENDING_EVENTS},
    }


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--requests", type=int, default=100)
    parser.add_argument("--duration", type=float, help="dispatch mixed requests for this many seconds")
    parser.add_argument("--connections", type=int, default=20)
    parser.add_argument("--sse-connections", type=int, default=2)
    parser.add_argument("--database-delay", type=float, default=0.01)
    parser.add_argument("--write-every", type=int, default=5, help="every Nth request increments; others read")
    parser.add_argument("--sample-interval", type=float, default=1.0)
    parser.add_argument("--timeout", type=float, default=5.0)
    parser.add_argument("--drain-timeout", type=float, default=10.0)
    parser.add_argument("--output", type=Path, help="also write the complete JSON report to this file")
    parser.add_argument("--verifier", help="optional built purepy executable for cold/warm timings")
    args = parser.parse_args(argv)
    try:
        options_from(args)
    except ValueError as error:
        parser.error(str(error))
    report = asyncio.run(run(args))
    report["verifier_milliseconds"] = None
    if args.verifier:
        command = [str(Path(args.verifier).resolve()), "check", str(Path(__file__).resolve().parents[1])]

        def verify(extra):
            started = time.perf_counter()
            result = subprocess.run(command + extra, capture_output=True, text=True, timeout=60)
            if result.returncode:
                raise RuntimeError(f"verification failed: {result.stdout}{result.stderr}")
            return round((time.perf_counter() - started) * 1000, 3)

        try:
            cold = verify(["--no-cache"])
            verify([])
            report["verifier_milliseconds"] = {"cold": cold, "warm": verify([])}
        except (OSError, RuntimeError, subprocess.SubprocessError) as error:
            report["ok"], report["verifier_error"] = False, f"{type(error).__name__}: {error}"
    output = json.dumps(report, indent=2, sort_keys=True, allow_nan=False) + "\n"
    if args.output:
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(output, encoding="utf-8")
    print(output, end="")
    return 0 if report["ok"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
