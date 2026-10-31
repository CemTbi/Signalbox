// SPDX-License-Identifier: Apache-2.0

package telemetry

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/CemTbi/Signalbox/internal/platform/clickhouse"
	"github.com/CemTbi/Signalbox/internal/platform/metrics"
)

const (
	graphJob = "service-graph"
	// graphLag is how long after a minute ends the job builds it, so the
	// minute's spans, and the children of its last calls, have arrived:
	// exporters send every few seconds.
	graphLag = 90 * time.Second
	// graphCatchUp bounds how many minutes one tick builds, and how far
	// back a job that was stopped resumes. Older minutes stay without
	// lines.
	graphCatchUp = 60
)

// GraphJob builds the service graph a minute at a time into
// service_edges_1m. Every process with the worker role runs it; a minute
// is claimed in PostgreSQL, so each is built once.
type GraphJob struct {
	db     *pgxpool.Pool
	conn   clickhouse.Conn
	logger *slog.Logger
	now    func() time.Time

	built    *metrics.Counter
	failed   *metrics.Counter
	duration *metrics.Histogram
	behind   *metrics.Gauge
}

// NewGraphJob returns the job. Run runs it.
func NewGraphJob(db *pgxpool.Pool, conn clickhouse.Conn, logger *slog.Logger, reg *metrics.Registry) *GraphJob {
	return &GraphJob{
		db: db, conn: conn, logger: logger, now: time.Now,
		built:  reg.NewCounter("signalbox_graph_minutes_built_total", "Minutes of the service graph built."),
		failed: reg.NewCounter("signalbox_graph_minutes_failed_total", "Attempts to build a minute of the service graph that failed; the minute is tried again."),
		duration: reg.NewHistogram("signalbox_graph_minute_duration_seconds", "How long building one minute of the service graph took.",
			metrics.DurationBuckets),
		behind: reg.NewGauge("signalbox_graph_minutes_behind", "Minutes of the service graph due but not built yet."),
	}
}

// Run builds minutes as they become due, every 15 seconds, until ctx ends.
func (j *GraphJob) Run(ctx context.Context) {
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		if _, err := j.Tick(ctx); err != nil && ctx.Err() == nil {
			j.logger.WarnContext(ctx, "couldn't build the service graph; it will try again", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// Tick builds every minute that's due, at most graphCatchUp of them, and
// says how many it built.
func (j *GraphJob) Tick(ctx context.Context) (int, error) {
	built := 0
	for built < graphCatchUp {
		ok, err := j.next(ctx)
		if err != nil {
			return built, err
		}
		if !ok {
			return built, nil
		}
		built++
	}
	return built, nil
}

// next claims and builds the next due minute, and says whether there was
// one.
func (j *GraphJob) next(ctx context.Context) (bool, error) {
	due := j.now().Add(-graphLag).Truncate(time.Minute) // minutes ending by now are due
	var done bool
	err := pgx.BeginFunc(ctx, j.db, func(tx pgx.Tx) error {
		// The row's lock makes other processes wait for this minute, then
		// see it done.
		if _, err := tx.Exec(ctx, `INSERT INTO telemetry_jobs (name, done_through) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			graphJob, due.Add(-time.Minute)); err != nil {
			return err
		}
		var through time.Time
		if err := tx.QueryRow(ctx, `SELECT done_through FROM telemetry_jobs WHERE name = $1 FOR UPDATE`, graphJob).Scan(&through); err != nil {
			return err
		}
		if earliest := due.Add(-graphCatchUp * time.Minute); through.Before(earliest) {
			through = earliest // stopped for long: resume an hour back
		}
		minute := through.UTC()
		j.behind.Set(float64(due.Sub(minute) / time.Minute))
		if !minute.Before(due) {
			done = true
			return nil
		}
		start := j.now()
		if err := BuildGraphMinute(ctx, j.conn, minute); err != nil {
			j.failed.Inc()
			return err
		}
		j.duration.Observe(time.Since(start).Seconds())
		j.built.Inc()
		_, err := tx.Exec(ctx, `UPDATE telemetry_jobs SET done_through = $2, updated_at = now() WHERE name = $1`, graphJob, minute.Add(time.Minute))
		return err
	})
	if err != nil {
		return false, fmt.Errorf("build the service graph: %w", err)
	}
	return !done, nil
}

// BuildGraphMinute adds one minute's calls to the service graph, from the
// spans that started in it. Each client or producer span is a call. If a
// server or consumer span of another service is its child, the call went
// to that service. If it has no such child, its server address names the
// target: a traced service, when one by that name sent spans around then
// (its span may be missing: sampled out, refused or late), otherwise a
// database, with its db.system, or a host outside the traced services.
// Calls within a service, and calls whose target can't be named, aren't
// lines.
//
// Children may start up to 30 seconds before their parent, for clocks
// that disagree, and up to 90 seconds after the minute.
func BuildGraphMinute(ctx context.Context, conn clickhouse.Conn, minute time.Time) error {
	err := conn.Exec(ctx, `
		INSERT INTO service_edges_1m
		WITH
			calls AS (
				SELECT environment_id, trace_id, span_id, service_name, duration_ns, status_code, db_system, server_address
				FROM spans
				WHERE timestamp >= {minute:DateTime} AND timestamp < {minute:DateTime} + INTERVAL 1 MINUTE
				  AND span_kind IN ('client', 'producer')
			),
			handlers AS (
				SELECT environment_id, trace_id, parent_span_id, any(service_name) AS service_name
				FROM spans
				WHERE timestamp >= {minute:DateTime} - INTERVAL 30 SECOND AND timestamp < {minute:DateTime} + INTERVAL 150 SECOND
				  AND span_kind IN ('server', 'consumer')
				  AND parent_span_id != unhex('0000000000000000')
				GROUP BY environment_id, trace_id, parent_span_id
			),
			-- Services that handled requests around then. The per-minute
			-- rollup knows them from a few rows instead of every span.
			traced AS (
				SELECT DISTINCT environment_id, service_name
				FROM span_metrics_1m
				WHERE minute >= {minute:DateTime} - INTERVAL 1 MINUTE AND minute <= {minute:DateTime} + INTERVAL 2 MINUTE
				  AND span_kind IN ('server', 'consumer')
			)
		SELECT
			c.environment_id,
			{minute:DateTime} AS minute,
			c.service_name AS client_service,
			multiIf(h.service_name != '', 'service', t.service_name != '', 'service', c.db_system != '', 'database', 'external') AS target_kind,
			multiIf(h.service_name != '', h.service_name, c.server_address != '', c.server_address, c.db_system) AS target,
			if(h.service_name = '' AND t.service_name = '', c.db_system, '') AS target_system,
			count() AS calls,
			countIf(c.status_code = 'error') AS errors,
			sum(c.duration_ns) AS duration_ns_sum,
			sumMap([toUInt8(least(255, greatest(0, floor(4 * log2(greatest(c.duration_ns, 1000) / 1000)))))], [toUInt64(1)]) AS latency_histogram
		FROM calls AS c
		LEFT JOIN handlers AS h
			ON c.environment_id = h.environment_id AND c.trace_id = h.trace_id AND c.span_id = h.parent_span_id
		LEFT JOIN traced AS t
			ON c.environment_id = t.environment_id AND c.server_address = t.service_name
		WHERE (h.service_name != '' AND h.service_name != c.service_name)
		   OR (h.service_name = '' AND c.server_address != c.service_name AND (c.db_system != '' OR c.server_address != ''))
		GROUP BY c.environment_id, client_service, target_kind, target, target_system`,
		clickhouse.Named("minute", minute.UTC().Truncate(time.Minute)))
	if err != nil {
		return fmt.Errorf("build the service graph for %s: %w", minute.Format(time.RFC3339), err)
	}
	return nil
}
