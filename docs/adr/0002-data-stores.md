# 0002. PostgreSQL, ClickHouse, NATS and object storage

- Status: Accepted
- Date: 2026-10-16
- Deciders: project lead

## Context

Signalbox stores two very different kinds of data:

- **State people create and configure:** accounts, workspaces, specs and revisions, collections, tests, monitors, incidents, audit logs. It is relational, needs transactions, and is modest in volume.
- **What the system observes:** spans, monitor check results, test run results, request and mock logs. It is append-mostly, large (a busy installation ingests tens of thousands of spans per second), queried by time range and aggregated, and kept for a limited time.

Roles also need to hand work to each other (scheduler to runner, api to worker) and push live updates to browsers. Every extra service makes self-hosting harder.

## Decision

| Store | Holds | Notes |
|---|---|---|
| **PostgreSQL 16+** | All state | Row-level security as a second line of access control. Migrations with goose, run at start under an advisory lock. |
| **ClickHouse** (current LTS) | Spans, service-graph and latency rollups, check and run results, request and mock logs | MergeTree tables partitioned by day, TTL per table for retention, ZSTD compression, materialized views for rollups. |
| **NATS JetStream** | Job queues, events between roles, live updates, leases | Runs embedded inside `signalbox-server` on a single machine; a three-node cluster on Kubernetes. |
| **Files or S3-compatible storage** | Large response bodies, reports, generated code, docs exports | Local disk on a single machine; any S3-compatible service in larger installations. |

PostgreSQL and ClickHouse are the only services an installation must run. There is no Redis and no Kafka. Caches stay in process, and coordination uses NATS key-value.

## Consequences

- A single-machine install is three containers: `signalbox-server`, PostgreSQL and ClickHouse.
- Operators back up two databases. The runbooks cover PostgreSQL point-in-time recovery and ClickHouse `BACKUP` to S3.
- ClickHouse needs about 2 GB of memory at minimum. The Compose file ships a small-memory profile.
- Two query languages, both SQL. ClickHouse access lives only in the telemetry module and the results stores.
- If ingestion ever outgrows direct batch inserts, Kafka can be added in front of ClickHouse without changing the ingest API.

## Alternatives considered

- **PostgreSQL only, with time-series extensions.** Rejected. Trace volume and aggregate queries over billions of rows are what columnar storage is for, and the leading extension's license is not open source.
- **Elasticsearch or OpenSearch for traces.** Rejected. Heavier to run, and less efficient for aggregates.
- **Grafana Tempo for traces.** Rejected. It is AGPL-licensed, and its object-storage design makes ad-hoc attribute queries and spec drift analysis harder.
- **Cassandra, as older Jaeger setups use.** Rejected. Heavy to operate and weak at aggregation.
- **Redis or Valkey for queues and caches.** Rejected. NATS covers queues, pub/sub and key-value in one small, embeddable server.
