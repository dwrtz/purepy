"""Self-contained localhost load and SSE demonstration; no benchmark dependency."""

import argparse
import asyncio
import json
import platform
from pathlib import Path
import resource
import statistics
import subprocess
import time
import tracemalloc

from host.database import Database
from host.main import Service


async def request(port, path, method="GET"):
    start = time.perf_counter()
    reader, writer = await asyncio.open_connection("127.0.0.1", port)
    try:
        writer.write(f"{method} {path} HTTP/1.1\r\nHost: localhost\r\n\r\n".encode())
        await writer.drain()
        response = await asyncio.wait_for(reader.read(), 10.0)
        if not response.startswith(b"HTTP/1.1 200 OK"):
            raise RuntimeError(f"unsuccessful request: {response[:100]!r}")
        return time.perf_counter() - start
    finally:
        writer.close()
        await writer.wait_closed()


async def run(args):
    tracemalloc.start()
    service = Service(Database(delay=args.database_delay))
    port = await service.start()
    samples = []
    semaphore = asyncio.Semaphore(args.connections)

    async def measured():
        async with semaphore:
            samples.append(await request(port, "/users/1"))

    streams = []
    cpu_start = time.process_time()
    start = time.perf_counter()
    try:
        for _ in range(args.sse_connections):
            reader, writer = await asyncio.open_connection("127.0.0.1", port)
            writer.write(b"GET /events HTTP/1.1\r\nHost: localhost\r\n\r\n")
            await writer.drain()
            await asyncio.wait_for(reader.readuntil(b"\r\n\r\n"), 5.0)
            streams.append((reader, writer))
        await asyncio.gather(*(measured() for _ in range(args.requests)))
        elapsed = time.perf_counter() - start
        cadence = []
        for _ in range(3):
            update_start = time.perf_counter()
            await request(port, "/users/1/increment/1", "POST")
            for reader, _ in streams:
                async with asyncio.timeout(5.0):
                    while True:
                        event = await reader.readuntil(b"\n\n")
                        if b"event: balance" in event:
                            cadence.append(time.perf_counter() - update_start)
                            break
        sorted_samples = sorted(samples)
        rss = resource.getrusage(resource.RUSAGE_SELF).ru_maxrss
        if platform.system() != "Darwin":
            rss *= 1024
        return {
            "python": platform.python_version(), "platform": platform.platform(),
            "requests": args.requests, "concurrent_connections": args.connections,
            "observed_max_invocations": service.max_active,
            "observed_overlapping_database_reads": service.database.max_active_reads,
            "throughput_requests_per_second": round(args.requests / elapsed, 2),
            "latency_ms": {
                "median": round(statistics.median(samples) * 1000, 3),
                "p95": round(sorted_samples[min(len(samples) - 1, int(len(samples) * 0.95))] * 1000, 3),
                "max": round(max(samples) * 1000, 3),
            },
            "sse_connections": len(streams), "sse_events_received": len(cadence),
            "sse_poll_interval_ms": 50,
            "sse_update_delivery_max_ms": round(max(cadence, default=0) * 1000, 3),
            "database": "SQLite :memory:, worker thread, serialized atomic transactions",
            "simulated_database_latency_ms": args.database_delay * 1000,
            "cpu_seconds": round(time.process_time() - cpu_start, 4),
            "peak_resident_bytes": rss,
            "peak_traced_python_bytes": tracemalloc.get_traced_memory()[1],
            "host_errors": service.errors,
        }
    finally:
        for _, writer in streams:
            writer.close()
            await writer.wait_closed()
        await service.close()
        tracemalloc.stop()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--requests", type=int, default=100)
    parser.add_argument("--connections", type=int, default=20)
    parser.add_argument("--sse-connections", type=int, default=2)
    parser.add_argument("--database-delay", type=float, default=0.01)
    parser.add_argument("--verifier", help="optional built purepy executable for cold/warm analysis timings")
    args = parser.parse_args()
    if args.requests < 1 or args.connections < 1 or args.sse_connections < 0 or args.database_delay < 0:
        parser.error("request and connection counts must be positive; SSE and delay must be nonnegative")
    report = asyncio.run(run(args))
    report["verifier_milliseconds"] = None
    if args.verifier:
        project = str(Path(__file__).resolve().parents[1])
        command = [str(Path(args.verifier).resolve()), "check", project]

        def verify(extra):
            start = time.perf_counter()
            result = subprocess.run(command + extra, capture_output=True, text=True)
            if result.returncode != 0:
                raise RuntimeError(f"verification failed: {result.stdout}{result.stderr}")
            return round((time.perf_counter() - start) * 1000, 3)

        cold = verify(["--no-cache"])
        verify([])
        report["verifier_milliseconds"] = {"cold": cold, "warm": verify([])}
    print(json.dumps(report, indent=2, sort_keys=True))


if __name__ == "__main__":
    main()
