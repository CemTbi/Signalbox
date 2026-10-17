# 0014. Span storage in ClickHouse

- Status: Accepted
- Date: 2026-10-17
- Deciders: project lead

## Context

The tracing module stores spans that services send over OTLP and Zipkin, and the trace UI, the service graph and the canvas read them back. [ADR-0002](0002-data-stores.md) puts spans in ClickHouse. The build plan sets the targets a single machine with 4 vCPUs, 16 GB of memory and an SSD must meet:

- ingest 5,000 spans per second, sustained;
- show a trace by its ID with a p95 under 1 second;
- search 24 hours of spans with a p95 under 3 seconds.

Spike 2 had to show that a schema meets them, and what it costs in disk space, before the ingest role is built on it.

## Decision

The schema is in `internal/modules/telemetry/schema.go`; the code that writes and reads it is beside it.

**The `spans` table** is a MergeTree partitioned by day, with a TTL that drops whole days:

- Sorted by environment, service, span name and start time, as ClickHouse's own OpenTelemetry schema does. Every query names an environment, and within each service and span name the start times are in order, so a time range narrows every group to the few granules it covers.
- Trace and span IDs are binary `FixedString` columns, not hex text, which would double their size.
- The resource, the same for every span a process sends, is a JSON object in a `LowCardinality(String)` column. Each part stores each resource once, and a span stores only its index in that dictionary.
- The attributes Signalbox filters and groups by have columns of their own: HTTP method, route and status code, the server address a client calls, and the database system. They aren't repeated in `span_attributes`, a `Map` written with its keys sorted, so equal attribute sets compress as one.
- A text index on the `key=value` items of `span_attributes` finds spans by any attribute. A bloom filter on the trace ID and a minmax index on the duration skip the rest of the data.

**The `trace_ids` table** maps a trace ID to the seconds its spans cover, filled by a materialized view. Fetching a trace reads one row there, then only those seconds of `spans`, however long spans are kept.

**The `span_metrics_1m` table** keeps each operation's calls, errors, total duration and a latency histogram per minute, filled by a materialized view. The histogram's buckets are a quarter of a power of two wide, so a percentile read from it is at most 19% too high. The canvas and dashboards read this table instead of the spans.

**Writes** come in batches: one insert a second, or every few thousand spans, whichever comes first. ClickHouse handles few large inserts well and many small ones badly.

**Searches** return the newest matches. They look at the last five minutes first and reach further back, four times as far each time, until they have enough spans, so a search over a day reads only as much of it as it needs.

## Spike results (2026-10-17)

`tools/spanbench` generates the traces of a web shop: twelve services in four languages, PostgreSQL, Redis, Kafka and two outside providers, 10.5 spans per trace on average. Spans carry the attributes OpenTelemetry instrumentation sends, with real cardinality: routes repeat, while paths, IDs, client addresses and query texts vary. Trace and span IDs are random, and 0.5% of server spans fail with a stack trace.

ClickHouse 26.8 ran in Docker Desktop on Windows 10 (WSL 2) with the Compose stack's small-machine profile, limited to 4 CPUs of an AMD Ryzen 5 3600, with data on a SATA SSD. The benchmark ran next to it on the same Docker network, as the ingest role will.

| Question | Result |
|---|---|
| Sustained ingest | 5,000 spans a second for 15 minutes, in one insert a second: ClickHouse used 0.13 of a CPU on average, 0.29 at p95 and at most 1.2 while merging, and at most 617 MiB of memory. Inserts took 81 ms at p50, 87 ms at p95 and 106 ms at p99, 318 ms at most. Merges kept the table at 4 parts. |
| Headroom | Loading history with four writers reached 218,000 spans a second for 3 hours of it and 160,000 for 12 hours, as merges took a growing share: 30 to 40 times the target. |
| Disk | 55 to 56 bytes per span for all three tables: 24 GB a day at 5,000 spans a second. The random trace, span and parent IDs take 26 of them, attributes 10, their text index 8.6, start times and durations 6.7, and the resource 0.3. |
| Trace by ID | With 216 million spans stored, 12 hours at the target rate, in 11.9 GB: p50 42 to 71 ms, p95 96 to 155 ms. |
| Searches | Over 1, 6 and 12 hours of those spans: p95 8 to 25 ms for a service's newest spans, errors, slow spans, a route and status code, or a common attribute value. Finding one URL path through the text index took 35, 83 and 100 ms at p95. The slowest grow at most in proportion to the range, so a search over 24 hours stays under a quarter of a second. |
| Operation statistics | From the rollup: 22, 40 and 60 ms at p50 over 1, 6 and 12 hours. From raw spans: 0.4, 2.0 and 3.8 s. |
| Messy span names | With a fifth of server spans named after their URL, 1.56 million distinct span names in 3 hours, searches and trace lookups stayed as fast. The per-minute rollup grew to 2.45 million rows and took 4.6 s to read, slower than raw spans. |

What the spike changed on the way, measured on the same data:

- **Resources** took 21 bytes a span as a `Map`, 15 once its keys were sorted, and 0.3 as a `LowCardinality` JSON string.
- **Maps with sorted keys.** Go's map order is random, so the same attributes arrived in different orders: the attribute map took 19 bytes a span unsorted and 12 sorted.
- **Promoted attributes stored once** saved 2 more bytes a span.
- **The full start time as the last sort key**, rather than the second, made start times 12% smaller.
- **Searching newest first:** finding the newest errors of every service in one query over the whole range read 19, 108 and 213 million rows for 1, 6 and 12 hours and took 94, 468 and 912 ms. Searching from the newest five minutes backwards read 2.3 million and took 20 ms each time.
- In all, a span went from 93 bytes to 55.

## Consequences

- At 5,000 spans a second, a single machine needs about 24 GB of disk a day of retention, 170 GB for the default week. CPU and memory are no constraint. The installation guide must say so, and the ingest role should offer sampling.
- The ingest role must batch spans, at most an insert a second per writer, and bound span-name cardinality before spans reach the rollup: name server spans by method and route, and fold the names of a service beyond a limit into one. Otherwise a service that names spans after URLs makes the rollup slower than raw spans.
- ClickHouse schema changes need migrations. They come with the ingest role, which is the first code to write to ClickHouse in production.
- The service graph, which pairs client and server spans across services, isn't covered by this spike.
- The data is synthetic. Real instrumentation sends longer URLs, query texts with literals and more attributes, so expect more bytes per span. The demo shop's real OpenTelemetry data should be measured with the same tool before v0.3.

## Alternatives considered

- **Sorting by 30-minute bucket first**, as SigNoz does, so that time ranges prune before anything else. With 3 hours stored it was no faster than the chosen order, also with 1.56 million span names, and with ordinary names it loaded 10% slower. Rejected.
- **Resource attributes in a `Map`**, as ClickHouse's own schema does: 15 to 21 bytes a span instead of 0.3. Rejected.
- **A resources table, with a resource ID in each span**, as SigNoz does: 0.6 bytes a span, and every query that filters by resource needs a second lookup. Rejected; the `LowCardinality` column is smaller and simpler.
- **Finding traces with the bloom filter alone**, without `trace_ids`: 20 ms over one hour, but 91 ms over twelve, since every granule's filter in the range must be checked, and a lookup without a time hint has to search all of retention. Rejected.
- **No index on attributes**, which saves 8.6 bytes a span: finding one product's URL path took 2.0 s over one hour, 7.9 s over six and 13.6 s over twelve, against 29 to 78 ms with the text index. Rejected.
- **The JSON type for attributes** and **ZSTD level 3** for the attribute map, which made it 16% smaller, weren't measured end to end. Either can be tried with `tools/spanbench` later.
