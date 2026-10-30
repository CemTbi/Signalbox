# 0040. Receiving spans: ingest keys, OTLP/HTTP and Zipkin

- Status: Accepted
- Date: 2026-10-30
- Deciders: project lead

## Context

[RFC 0002](../rfcs/0002-monitors-and-tracing.md) makes Signalbox a trace backend. Services send spans with any OpenTelemetry SDK or Collector, or with existing Zipkin instrumentation, and Signalbox stores them in ClickHouse ([ADR-0014](0014-span-storage.md), [ADR-0039](0039-clickhouse-in-production.md)).

Ingest is a public surface that takes untrusted data at high volume, so it must:
- know which project environment spans belong to;
- never lose spans it acknowledged;
- tell exporters when to retry, and when not to;
- keep secrets out of storage;
- bound what a request can cost.

This step covers OTLP over HTTP and Zipkin v2. OTLP over gRPC follows on the same pipeline.

## Decision

**The ingest role** has listeners of its own:
- **OTLP/HTTP:** `POST /v1/traces` on `:4318`, in protobuf or OTLP/JSON.
- **Zipkin v2:** `POST /api/v2/spans` on `:9411`, in JSON or protobuf.

`SIGNALBOX_OTLP_HTTP_ADDR` and `SIGNALBOX_ZIPKIN_ADDR` move them, and empty turns one off. `--roles=all` includes ingest. The role needs only PostgreSQL, to check keys, and ClickHouse, so it can also run on its own and scale with span volume.

**Ingest keys:**
- **Scope:** a key belongs to one project environment, and every span sent with it lands there, whatever its attributes say.
- **Format:** keys look like `sbx_ing_` and 43 random characters. Only their SHA-256 hash is stored, as for access tokens ([ADR-0026](0026-personal-access-tokens.md)).
- **Sending:** exporters send them as `Authorization: Bearer …`, such as through `OTEL_EXPORTER_OTLP_HEADERS`.
- **Who:** workspace owners and admins create and revoke keys, and every member sees them, without their secrets. An environment may have 50 keys. The web app's Ingest keys page shows a new key once, with the exporter settings that use it.
- **Caching:** a process caches a key it has checked for 10 seconds, so a busy exporter doesn't cost a query per request. Revoking takes effect at once in the process that handled it, and within 10 seconds elsewhere.
- **Guessing:** malformed keys never reach the database. Requests with keys that don't exist are limited per client address, to 20 at once and then one a second.

**Acknowledged means stored.**
- **Batching:** a writer per process collects spans from all requests into one insert a second, or sooner at 10,000 spans, as ADR-0014 measured.
- **Answering:** a request is answered only once its spans are stored. Exporters send in the background, so the second's wait costs them nothing, and a crash can lose only spans whose senders will retry.
- **Bounds:** at most 200,000 spans wait in a process. Past that, and when ClickHouse fails, requests are answered "retry later".
- **Shutdown:** stopping a process stops its listeners first, then writes what the writer holds.

**Answers follow each protocol, so exporters retry exactly when they should:**

| Situation | OTLP/HTTP | Zipkin |
|---|---|---|
| Stored | `200` with `ExportTraceServiceResponse` | `202` |
| Some spans invalid | `200` with `partial_success`: how many, and why the first was refused | `202`, with `X-Signalbox-Rejected-Spans` |
| No key, or an unknown one | `401` | `401` |
| Body malformed | `400` | `400` |
| Body too large | `413` | `413` |
| Media type or encoding not supported | `415` | `415` |
| Environment over its rate | `429` with `Retry-After` | `429` with `Retry-After` |
| Writer full, or ClickHouse failing | `503` with `Retry-After` | `503` with `Retry-After` |

OTLP errors carry a `google.rpc.Status`, in the request's own encoding.

**Decoding:**
- **OTLP protobuf** is read as `TracesData` from `go.opentelemetry.io/proto/otlp`, which has the same wire format as the export request. That keeps the collector package, and the gRPC it imports, out of this step.
- **OTLP/JSON** is read by hand-written types, because its trace and span IDs are hex where protobuf's JSON mapping expects base64. Enums are accepted as numbers or names.
- **Zipkin protobuf** is read field by field with `protowire`, since its message is small.
- **Zipkin's model** maps as the OpenTelemetry Collector's Zipkin receiver maps it:
  - a 64-bit trace ID gains eight zero bytes in front;
  - tags become attributes, and annotations events;
  - an `error` tag sets the error status;
  - the remote endpoint becomes `peer.service`, and, on client spans, `server.address`.

**Limits per request and per span:**
- **Bodies** are at most 16 MiB once decompressed. gzip and zstd are read through bounded readers, so a small request can't expand into gigabytes.
- **Spans** keep at most 128 attributes, chosen by key order so the same span keeps the same ones, and 128 events and 128 links. Values are cut to 4 KiB, and names to 1 KiB. What was left out is counted in metrics.
- **Start times** must fall within the retention period and at most an hour ahead. Each distinct day makes a ClickHouse partition, so spans from far-off times would cost far more than they're worth.
- **Rate:** each environment may send `SIGNALBOX_INGEST_SPANS_PER_SECOND` spans a second, 5,000 by default, with twice that at once.

**Before storing, ingest:**
- **Fills fields from attributes:** the fields Signalbox filters by are filled from the current semantic conventions, and from the older names (`http.method`, `http.status_code`, `net.peer.name`, `db.system`) that much instrumentation still sends.
- **Redacts credentials:**
  - **Attributes:** values whose names contain a credential word are replaced by `•••`. The words are `authorization`, `auth`, `cookie`, `token`, `secret`, `password`, `passwd`, `pwd`, `apikey`, `api` followed by `key`, `credential(s)`, `signature` and `sig`. Words are whole words between dots, dashes and underscores: `access_token` is redacted, `input_tokens` isn't.
  - **URLs:** in URL attributes (`url.full`, `url.query`, `http.url`, `http.target`, `url.original`), such query parameters are redacted too, and so is one named `key`.
  - **Database statements** are kept as sent.
- **Names operations:**
  - **What groups the rollup:** a new `operation` column ([migration 00002](../../internal/platform/clickhouse/migrations/00002_span_operations.sql)) is what the per-minute rollup groups by. It is `METHOD /route` for a server span with a route, and the span's name otherwise. The span keeps its own name.
  - **Folding:** a service's operations past 1,000 fold into `(other)`, so a service that names spans after URLs can't make the rollup slower than raw spans. Each process counts what it has seen.
- **Leaves services as sent:** spans keep their `service.name`, or `unknown_service` without one. Matching them to the canvas's services by their telemetry name happens when they're read, so renaming a service or adding one later needs no rewrite of stored spans.

**Metrics:** requests by protocol and outcome; spans accepted, refused by reason, and written; inserts that failed and how long inserts take; spans pending; and parts of spans dropped to stay within the limits.

## Consequences

- Any OpenTelemetry SDK or Collector, and any Zipkin reporter that can set a header, can send to Signalbox today. Tools that can't set headers go through a Collector or proxy that adds the key, as RFC 0002 decided.
- The Compose stack publishes ports 4318 and 9411. An operator who exposes them beyond the network that sends spans relies on the keys and limits above.
- The binary gains `go.opentelemetry.io/proto/otlp`'s message packages. gRPC arrives with the next step.
- Because spans arrive only after a key exists, and keys belong to environments, deleting an environment or project leaves its spans in ClickHouse until they expire. Deleting them at once comes with the trace reading work.
- Acknowledging only stored spans makes a request wait up to a second. Exporters batch in the background, so this costs throughput nothing, but a synchronous client, such as a test sending one span, waits.

## Alternatives considered

- **Acknowledge on receipt, store afterwards.** Answers faster, but a crash or a ClickHouse outage loses spans the exporter thinks were delivered. The RFC chose acknowledged means stored.
- **Keys in the URL**, for Zipkin reporters that can't set headers. Rejected in RFC 0002: URLs end up in access logs.
- **Match spans to services at ingest.** A stored match goes stale when services are renamed, added or removed. Matching when reading costs a small join against a handful of services.
- **The collector's protobuf package, `collector/trace/v1`.** It holds the same request type but imports gRPC and grpc-gateway. `TracesData` decodes the same bytes.
- **protojson for OTLP/JSON.** It reads IDs as base64, which OTLP/JSON doesn't use, so every ID would come out wrong.
