-- Each span's operation: what the per-minute rollup groups by, instead of
-- the span's own name. Ingest names a server span with an HTTP route
-- "METHOD /route", and folds a service's operations past 1,000 into
-- "(other)", so a service that names spans after URLs can't make the rollup
-- slower than the spans it summarizes. The span keeps its own name.
-- Owned by internal/modules/telemetry. See docs/adr/0040-span-ingest.md.
--
-- span_metrics_1m keeps its span_name column, which now holds the
-- operation. Recreating the view leaves no gap that matters: ingest starts
-- with this release, so no spans arrive while it runs.

-- +goose NO TRANSACTION
-- +goose Up
ALTER TABLE spans ADD COLUMN IF NOT EXISTS operation LowCardinality(String) DEFAULT span_name CODEC(ZSTD(1)) AFTER span_name;

DROP TABLE IF EXISTS span_metrics_1m_mv;

CREATE MATERIALIZED VIEW IF NOT EXISTS span_metrics_1m_mv TO span_metrics_1m AS
SELECT
    environment_id,
    service_name,
    operation AS span_name,
    span_kind,
    toStartOfMinute(timestamp) AS minute,
    count() AS calls,
    countIf(status_code = 'error') AS errors,
    sum(duration_ns) AS duration_ns_sum,
    sumMap([toUInt8(least(255, greatest(0, floor(4 * log2(greatest(duration_ns, 1000) / 1000)))))], [toUInt64(1)]) AS latency_histogram
FROM spans
GROUP BY environment_id, service_name, operation, span_kind, minute;

-- +goose Down
DROP TABLE IF EXISTS span_metrics_1m_mv;

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

ALTER TABLE spans DROP COLUMN IF EXISTS operation;
