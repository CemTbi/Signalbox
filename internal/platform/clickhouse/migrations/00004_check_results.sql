-- What each monitor's checks found. Owned by internal/modules/monitors. See
-- docs/adr/0046-monitors.md. PostgreSQL keeps only each monitor's current
-- state; uptime and response times over a period are read from here.

-- +goose NO TRANSACTION
-- +goose Up
CREATE TABLE IF NOT EXISTS check_results
(
    environment_id  UUID,
    monitor_id      UUID,
    time            DateTime64(3) CODEC(Delta(8), ZSTD(1)),
    outcome         Enum8('passed' = 1, 'degraded' = 2, 'failed' = 3, 'skipped' = 4),
    duration_ns     UInt64 CODEC(T64, ZSTD(1)),
    status_code     UInt16,
    failure_code    LowCardinality(String),
    failure_message String CODEC(ZSTD(1)),
    -- The health components, as JSON.
    components      String CODEC(ZSTD(1)),
    certificate_expires_at Nullable(DateTime),
    trace_id        String CODEC(ZSTD(1))
)
ENGINE = MergeTree
PARTITION BY toDate(time)
ORDER BY (monitor_id, time)
TTL toDate(time) + toIntervalDay(90);

-- +goose Down
DROP TABLE IF EXISTS check_results;
