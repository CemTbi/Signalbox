-- The service graph: who calls whom, per minute. Owned by
-- internal/modules/telemetry. See docs/adr/0044-service-graph.md.
--
-- Each row counts the calls from client_service to a target in one minute:
-- another service (target_kind service), a database that isn't traced
-- (database, with its system in target_system), or a host outside the
-- traced services (external). Calls are counted from the caller's client
-- or producer spans, with their durations: what the caller waited.
-- A worker fills it a minute at a time; see telemetry.GraphJob.

-- +goose NO TRANSACTION
-- +goose Up
CREATE TABLE IF NOT EXISTS service_edges_1m
(
    environment_id    UUID,
    minute            DateTime CODEC(Delta(4), ZSTD(1)),
    client_service    LowCardinality(String),
    target_kind       Enum8('service' = 1, 'database' = 2, 'external' = 3),
    target            LowCardinality(String),
    target_system     LowCardinality(String),
    calls             SimpleAggregateFunction(sum, UInt64) CODEC(T64, ZSTD(1)),
    errors            SimpleAggregateFunction(sum, UInt64) CODEC(T64, ZSTD(1)),
    duration_ns_sum   SimpleAggregateFunction(sum, UInt64) CODEC(T64, ZSTD(1)),
    latency_histogram SimpleAggregateFunction(sumMap, Tuple(Array(UInt8), Array(UInt64))) CODEC(ZSTD(1))
)
ENGINE = AggregatingMergeTree
PARTITION BY toDate(minute)
ORDER BY (environment_id, client_service, target_kind, target, target_system, minute)
TTL toDate(minute) + INTERVAL 7 DAY
SETTINGS ttl_only_drop_parts = 1;

-- +goose Down
DROP TABLE IF EXISTS service_edges_1m;
