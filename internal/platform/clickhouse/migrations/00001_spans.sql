-- Spans, the traces they form, and each operation's calls, errors and
-- latency per minute. Owned by internal/modules/telemetry.
-- See docs/adr/0014-span-storage.md.
--
--   - spans holds every span, sorted by environment, service, span name and
--     start time. Attributes that Signalbox filters on often have their own
--     columns and aren't repeated in span_attributes, whose key=value items
--     have a text index. The resource, the same for every span a process
--     sends, is a JSON object in a LowCardinality column: each part stores
--     it once, and a span only an index into that dictionary.
--   - trace_ids maps a trace ID to the seconds its spans cover, so a trace
--     can be fetched by ID without searching the whole retention period.
--   - span_metrics_1m counts calls and errors and keeps a latency histogram
--     per operation and minute, for the canvas and dashboards.
--
-- Every table keeps 7 days here; the server sets the retention it's
-- configured with after migrating (telemetry.ApplyRetention).

-- +goose NO TRANSACTION
-- +goose Up
CREATE TABLE IF NOT EXISTS spans
(
    environment_id      UUID,
    timestamp           DateTime64(9) CODEC(Delta(8), ZSTD(1)),
    trace_id            FixedString(16) CODEC(ZSTD(1)),
    span_id             FixedString(8) CODEC(ZSTD(1)),
    parent_span_id      FixedString(8) CODEC(ZSTD(1)),
    trace_state         String CODEC(ZSTD(1)),
    service_name        LowCardinality(String) CODEC(ZSTD(1)),
    span_name           LowCardinality(String) CODEC(ZSTD(1)),
    span_kind           Enum8('unspecified' = 0, 'internal' = 1, 'server' = 2, 'client' = 3, 'producer' = 4, 'consumer' = 5) CODEC(ZSTD(1)),
    duration_ns         UInt64 CODEC(T64, ZSTD(1)),
    status_code         Enum8('unset' = 0, 'ok' = 1, 'error' = 2) CODEC(ZSTD(1)),
    status_message      String CODEC(ZSTD(1)),
    http_method         LowCardinality(String) CODEC(ZSTD(1)),
    http_route          LowCardinality(String) CODEC(ZSTD(1)),
    http_status_code    UInt16 CODEC(T64, ZSTD(1)),
    server_address      LowCardinality(String) CODEC(ZSTD(1)),
    db_system           LowCardinality(String) CODEC(ZSTD(1)),
    scope_name          LowCardinality(String) CODEC(ZSTD(1)),
    scope_version       LowCardinality(String) CODEC(ZSTD(1)),
    resource            LowCardinality(String) CODEC(ZSTD(1)),
    span_attributes     Map(LowCardinality(String), String) CODEC(ZSTD(1)),
    `events.timestamp`  Array(DateTime64(9)) CODEC(ZSTD(1)),
    `events.name`       Array(LowCardinality(String)) CODEC(ZSTD(1)),
    `events.attributes` Array(Map(LowCardinality(String), String)) CODEC(ZSTD(1)),
    `links.trace_id`    Array(FixedString(16)) CODEC(ZSTD(1)),
    `links.span_id`     Array(FixedString(8)) CODEC(ZSTD(1)),
    `links.trace_state` Array(String) CODEC(ZSTD(1)),
    `links.attributes`  Array(Map(LowCardinality(String), String)) CODEC(ZSTD(1)),
    span_attribute_items Array(String) ALIAS arrayMap(kv -> concat(kv.1, '=', kv.2), CAST(span_attributes, 'Array(Tuple(String, String))')),
    INDEX idx_trace_id trace_id TYPE bloom_filter(0.001) GRANULARITY 1,
    INDEX idx_duration duration_ns TYPE minmax GRANULARITY 1,
    INDEX idx_span_attribute_keys mapKeys(span_attributes) TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_span_attribute_items span_attribute_items TYPE text(tokenizer = 'array')
)
ENGINE = MergeTree
PARTITION BY toDate(timestamp)
ORDER BY (environment_id, service_name, span_name, timestamp)
TTL toDate(timestamp) + INTERVAL 7 DAY
SETTINGS index_granularity = 8192, ttl_only_drop_parts = 1;

CREATE TABLE IF NOT EXISTS trace_ids
(
    environment_id UUID,
    trace_id       FixedString(16) CODEC(ZSTD(1)),
    start          DateTime CODEC(ZSTD(1)),
    end            DateTime CODEC(ZSTD(1))
)
ENGINE = MergeTree
PARTITION BY toDate(start)
ORDER BY (environment_id, trace_id)
TTL toDate(start) + INTERVAL 7 DAY
SETTINGS index_granularity = 8192, ttl_only_drop_parts = 1;

CREATE MATERIALIZED VIEW IF NOT EXISTS trace_ids_mv TO trace_ids AS
SELECT
    environment_id,
    trace_id,
    toDateTime(min(timestamp)) AS start,
    toDateTime(max(timestamp + toIntervalNanosecond(duration_ns))) + 1 AS end
FROM spans
GROUP BY environment_id, trace_id;

-- latency_histogram maps a bucket to a count. Bucket b holds durations from
-- 2^(b/4) to 2^((b+1)/4) microseconds, so a percentile read from it is
-- within 19% of the true value.
CREATE TABLE IF NOT EXISTS span_metrics_1m
(
    environment_id    UUID,
    service_name      LowCardinality(String),
    span_name         LowCardinality(String),
    span_kind         Enum8('unspecified' = 0, 'internal' = 1, 'server' = 2, 'client' = 3, 'producer' = 4, 'consumer' = 5),
    minute            DateTime CODEC(Delta(4), ZSTD(1)),
    calls             SimpleAggregateFunction(sum, UInt64) CODEC(T64, ZSTD(1)),
    errors            SimpleAggregateFunction(sum, UInt64) CODEC(T64, ZSTD(1)),
    duration_ns_sum   SimpleAggregateFunction(sum, UInt64) CODEC(T64, ZSTD(1)),
    latency_histogram SimpleAggregateFunction(sumMap, Tuple(Array(UInt8), Array(UInt64))) CODEC(ZSTD(1))
)
ENGINE = AggregatingMergeTree
PARTITION BY toDate(minute)
ORDER BY (environment_id, service_name, span_name, span_kind, minute)
TTL toDate(minute) + INTERVAL 7 DAY
SETTINGS ttl_only_drop_parts = 1;

CREATE MATERIALIZED VIEW IF NOT EXISTS span_metrics_1m_mv TO span_metrics_1m AS
SELECT
    environment_id,
    service_name,
    span_name,
    span_kind,
    toStartOfMinute(timestamp) AS minute,
    count() AS calls,
    countIf(status_code = 'error') AS errors,
    sum(duration_ns) AS duration_ns_sum,
    sumMap([toUInt8(least(255, greatest(0, floor(4 * log2(greatest(duration_ns, 1000) / 1000)))))], [toUInt64(1)]) AS latency_histogram
FROM spans
GROUP BY environment_id, service_name, span_name, span_kind, minute;

-- +goose Down
DROP TABLE IF EXISTS span_metrics_1m_mv;
DROP TABLE IF EXISTS span_metrics_1m;
DROP TABLE IF EXISTS trace_ids_mv;
DROP TABLE IF EXISTS trace_ids;
DROP TABLE IF EXISTS spans;
