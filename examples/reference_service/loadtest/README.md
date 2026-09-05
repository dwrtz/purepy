# Reference service load harness

Run from `examples/reference_service`, using the repository's Python 3.14 virtual
environment after `make setup`:

```sh
PYTHONPATH=src ../../.venv/bin/python -m loadtest.load
PYTHONPATH=src ../../.venv/bin/python -m loadtest.load \
  --duration 300 --connections 20 --sse-connections 4 \
  --database-delay .01 --sample-interval 1 --output /tmp/purepy-soak.json
```

The default remains 100 requests. Schema 2 makes this a mixed workload: every
fifth operation increments user 1 by one and all other operations read that
user. `--write-every` changes the mix. `--duration` keeps a fixed worker pool
issuing requests until the duration expires, then joins in-flight requests.
Each SSE connection continuously consumes events throughout the workload.

Successful exit requires valid HTTP responses, plausible fresh reads, unique
write acknowledgments, complete SSE delivery, a matching final SQLite ledger,
no host errors, and completed resource cleanup. An error stops new request
dispatch, preserves the JSON report, and exits nonzero. `--timeout` bounds each
request/stream read and `--drain-timeout` bounds final SSE catch-up. The run closes
clients, joins its tasks, shuts the listener and host invocations down, and checks
that SQLite is closed, including on setup failures and cancellation.

The fresh database and increment-by-one workload give each update an exact
identity: event ID equals returned balance. Every SSE reader must receive IDs
1 through the successful write count in order, with the matching user and
balance. Keep-alives cannot stand in for an update. The correlation ledger also
handles an SSE arrival preceding the HTTP response. Read balances must be at
least the largest write acknowledged before the read began and cannot exceed
the total writes dispatched when its response is checked.

The report separates startup, the actual request window, SSE drain, and cleanup.
Throughput divides successful operations by the request window, including
in-flight completion after the dispatch deadline. Latencies begin after worker
admission and include connection setup, complete HTTP response, and client close.
Separate read/write totals, failures, and latency distributions are recorded.
SSE reports write-start-to-arrival latency, delay remaining after acknowledgment,
the number delivered before acknowledgment, and frame/keep-alive intervals.

CPU and memory cover **one combined local process** containing the load clients,
host, verified application, SQLite worker threads, and tracemalloc. They are not
standalone server measurements. CPU excludes the external `ps` helper used for
RSS sampling on platforms without `/proc`; unavailable current RSS is `null`.
`peak_resident_bytes` is the process-lifetime high-water mark and is labeled
separately from sampled current residency. Traced memory excludes native SQLite
and socket allocations. Tracing and sampling themselves add overhead.

SQLite retains one event row per write. The report samples event rows and page
bytes alongside RSS/traced memory and reports workload linear trends. Increasing
retained pages and allocator residency do not themselves establish a leak; the
harness imposes no arbitrary steady-memory pass threshold. Warmup remains part
of the trend. A separate baseline or longer steady workload is needed for a
performance regression decision.

Statistics remain bounded: each latency metric keeps a 2,048-item deterministic
reservoir, memory history keeps the latest 1,200 samples plus first/last and
online trend totals, and error examples cap at 20. Percentiles are approximate;
counts, totals, minima, and maxima are exact. The correlation ledger caps at
8,192 outstanding events and fails explicitly if readers fall behind that far.
The service emits at most 32 events before each 50 ms poll sleep, so sustained
writes above roughly 640 events/second can build real application backlog.
Artificial database delay affects user reads only, not writes or SSE polls.

The optional `--verifier PATH` records cold/warm checks after service shutdown,
outside workload measurements. `--output PATH` saves the same JSON printed on
stdout, including failed runs.
