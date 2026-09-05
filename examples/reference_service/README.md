# PurePy reference service

This small service keeps parsing, routing, validation, response rendering, database
operation selection, and the SSE loop in verified `src/app/`. The host owns SQLite,
sockets, worker threads, concurrent invocation, cancellation, and cleanup.

From the repository root, create the uv-managed Python 3.14 virtual environment
and check the application:

```sh
make setup
make example
```

Run the service from the repository root:

```sh
make serve
```

`--database ./demo.sqlite` persists the sample database; its default is in-memory
SQLite seeded with users 1 (Ada) and 2 (Grace). All database operations execute in
worker threads; a lock protects the SQLite connection. Each increment and its
event insert commit in one transaction. Cancellation can stop the waiting
invocation while a started transaction completes; the host retains ownership and
waits before closing the database. Clients should not retry writes blindly.

```sh
curl http://127.0.0.1:8080/health
curl http://127.0.0.1:8080/users/1
curl -X POST http://127.0.0.1:8080/users/1/increment/5
curl -N http://127.0.0.1:8080/events
```

The protocol is deliberately HTTP-like: one HTTP/1.1 request per connection,
GET or POST, no request body, ASCII paths, and at most 8192 bytes of headers.
Writes encode their positive increment (1–1000) in the route. It is a teaching
workload, not a complete HTTP server. SSE sends historical events from cursor 0,
then polls every 50 ms with keep-alives. Closing a connection ends its invocation;
stopping the host cancels all outstanding invocations and closes their resources.

Run the runtime tests, socket integration tests, and load harness from the
repository root. The load target also builds the verifier and records its timings:

```sh
make python-test service-test
make loadtest
```

For custom options, use the same virtual environment directly:

```sh
cd examples/reference_service
PYTHONPATH=src ../../.venv/bin/python -m host.main --port 8080 --database ./demo.sqlite
PYTHONPATH=src ../../.venv/bin/python -m loadtest.load --requests 100 --connections 20 --sse-connections 2 --verifier ../../bin/purepy
```

The harness starts and cleans up a localhost listener, measures request throughput,
latency, CPU, memory, overlapping database reads, and SSE delivery. Its default
10 ms simulated database latency makes overlap visible. Numbers include Python
tracing overhead and are for viability, not framework comparisons. The optional
`--verifier` records cold and warm checks after stopping the server, keeping
analysis and server timings independent. Omit it to run the workload alone.

[A recorded sample](loadtest/sample.json) on Apple M4 with 16 GiB RAM, macOS
26.5.2, and uv-managed Python 3.14.7 handled 100 requests at 20-way concurrency
with 20 overlapping reads, roughly 1293 requests/s, and 17.5 ms p95 latency.
Two SSE streams received all six expected deliveries within 51.3 ms of their
updates. The same run measured 26.6 ms with PurePy's cache disabled and 6.7 ms
with its cache primed. These are single-run observations including process startup;
they do not flush operating-system caches or establish a performance guarantee.

The trusted surface is listed in `manifests/host.purepy.toml`. The only trusted-pure
external is exact UTF-8 encoding. No application framework is installed, and this
example is not a dependency of the Python runtime package.
