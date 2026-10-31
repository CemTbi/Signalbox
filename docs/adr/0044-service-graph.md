# 0044. The service graph, built a minute at a time

- Status: Accepted
- Date: 2026-10-31
- Deciders: project lead

## Context

The canvas draws lines between services, and between services and the databases and hosts they call, from what traces show ([ADR-0018](0018-live-canvas.md), [RFC 0002](../rfcs/0002-monitors-and-tracing.md)). Each line needs its rate, errors and latency. The demo shop's checkouts cross four services, a database and an outside payment provider.

A call is a pair of spans: the caller's client span, and the callee's server span, whose parent it is. The two arrive from different services, in different batches, at different times, and possibly at different ingest processes. Some never arrive: the callee isn't traced, its spans were sampled out, or they were refused. [ADR-0014](0014-span-storage.md) didn't measure building the graph, and RFC 0002 asked for a measurement at 5,000 spans a second.

## Decision

**A rollup of calls per minute.** `service_edges_1m` holds a row per minute, environment, caller and target. Each row has calls, errors, total duration and a latency histogram, like `span_metrics_1m`, so percentiles are within 19%. The calls are counted from the caller's client and producer spans, with their durations: what the caller waited.

**What a call went to:**
1. **A handler in another service:** if a server or consumer span of another service is the call's child, the call went to that service.
2. **No handler span:**
   - **A traced service:** if the call's `server.address` names a service that handled requests around then, the call went to that service. Its span was sampled out, refused or late.
   - **A database:** otherwise, a call with `db.system` went to a database, named by its address and its system.
   - **An external host:** otherwise, to a host outside the traced services, named by its address.
3. **Not lines:** calls within one service, and calls whose target has no name.

**Built in ClickHouse, by the worker, a minute at a time:**
- **The query:** one `INSERT … SELECT` joins a minute's client and producer spans with the server and consumer spans that started from 30 seconds before the minute to 150 seconds after it. That margin allows for clocks that disagree, and for handlers that start late in the minute.
- **The traced services** come from the per-minute rollup, which knows them from a few rows.
- **Delay:** a minute is built 90 seconds after it ends, so its last calls' handlers have been sent. Lines appear about two minutes after the calls.

**The worker role** is available, and runs the job.
- **Taking turns:** every process with the role runs it. A minute is claimed by locking a row in PostgreSQL (`telemetry_jobs`), and released with the minute built, so processes take turns and never build a minute twice.
- **Catching up:** a worker that was stopped catches up on at most the last hour; older minutes stay without lines.
- **Dependencies:** like ingest, the worker needs only PostgreSQL and ClickHouse, so it can run alone.
- **Metrics:** minutes built, minutes failed, how long a minute takes, and how far the job is behind.

**Why the join is in ClickHouse rather than at ingest:** RFC 0002 decided it. The OpenTelemetry Collector's service graph connector pairs spans in memory as they arrive, which works only when a trace's spans reach the same process within a time window. Here they may not, and a join over a minute in ClickHouse doesn't care which process received what.

**Reading:**
- `GET /api/v1/environments/{environmentId}/graph` returns the lines over a time range.
- `GET /api/v1/environments/{environmentId}/operations` returns operations' calls, errors and latency.
- The Traces page has a Services view: each service's requests, its operations on demand, and who called whom. The canvas draws the same lines next.
- Retention covers the new table, as it covers the others ([ADR-0039](0039-clickhouse-in-production.md)).

## Measurements (2026-10-31)

`tools/spanbench graph` built ten minutes of the graph over 30 minutes of the synthetic shop at 5,000 spans a second: 9 million spans, about 300,000 a minute. It ran on the machine and ClickHouse profile of ADR-0014, limited to 4 CPUs.

| | Per minute built |
|---|---|
| Time | 193 ms at the median, 243 ms at most |
| Rows read | 3.6 million |
| Memory | 95 MiB at most |
| Result | 29 lines in all, 1.9 million calls over the ten minutes |

**Cost:** a minute costs under half a percent of a minute of one CPU.

**Rows read:** the job reads about five times the minute's spans. Spans are sorted by service and name before time (ADR-0014), so each name's spans in the window cost whole 8,192-row granules. That grows with the number of span names, not with how much is stored.

**A first version** found the traced services by scanning server spans. It read 8.9 million rows and took 288 ms at the median. Reading them from the rollup halved that.

## Consequences

- The canvas, and anyone through the API, can see who calls whom, databases and outside hosts included, without declaring anything.
- Lines lag by about two minutes. The canvas's live numbers per service come from `span_metrics_1m`, which is current.
- A service whose spans never arrive appears as an external host under its address. Once it sends spans, its calls count as calls to it.
- Calls are counted where they start. A call whose handler took longer than the minute after is still counted, with the caller's duration.
- Addresses differ from `service.name` when instrumentation doesn't set `server.address` to it, such as calls through a load balancer's name. Those calls still pair through their handler spans, which is the usual case.

## Alternatives considered

- **Pair spans at ingest, in memory.** It needs a trace's spans at one process within a window, which several ingest processes don't guarantee.
- **A materialized view joining spans as they're inserted.** A view only sees the rows of one insert, and a call's two spans are rarely in the same one.
- **Count calls from server spans and their parents.** That counts only calls into traced services, and loses databases and outside hosts, which only the caller's spans name.
- **Build the graph on read, from spans.** A day of lines would join every span of the day on every view. The rollup reads a few rows a minute.
