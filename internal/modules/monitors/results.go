// SPDX-License-Identifier: Apache-2.0

package monitors

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/CemTbi/Signalbox/internal/platform/clickhouse"
)

const (
	// flushEvery is how often results are written. ClickHouse prefers a batch
	// a second to a row per check.
	flushEvery = time.Second
	// maxPendingResults bounds what's held while ClickHouse can't take it:
	// about 15 minutes of a thousand one-minute monitors.
	maxPendingResults = 15000

	// DefaultCheckLimit and MaxCheckLimit bound a list of results.
	DefaultCheckLimit = 100
	MaxCheckLimit     = 500
)

// Run writes results as they come, until ctx ends. Close writes the last.
func (m *Module) Run(ctx context.Context) {
	tick := time.NewTicker(flushEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if err := m.Flush(ctx); err != nil && ctx.Err() == nil {
				m.logger.WarnContext(ctx, "couldn't write check results; they'll be tried again", "error", err)
			}
		}
	}
}

// Close writes what's left, once checks have stopped.
func (m *Module) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := m.Flush(ctx); err != nil {
		m.logger.WarnContext(ctx, "couldn't write the last check results", "error", err)
	}
}

// record holds a result for the next flush, and counts it.
func (m *Module) record(c Check) {
	m.checks.Inc(c.Outcome)
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.pending) >= maxPendingResults {
		m.dropped.Inc()
		return
	}
	m.pending = append(m.pending, c)
}

// Flush writes the results held. If it fails they stay, for the next try.
func (m *Module) Flush(ctx context.Context) error {
	m.mu.Lock()
	batch := m.pending
	m.pending = nil
	m.mu.Unlock()
	if len(batch) == 0 {
		return nil
	}
	if err := writeChecks(ctx, m.conn, batch); err != nil {
		m.mu.Lock()
		defer m.mu.Unlock()
		keep := min(len(batch), maxPendingResults-min(len(m.pending), maxPendingResults))
		m.pending = append(batch[:keep:keep], m.pending...)
		for range len(batch) - keep {
			m.dropped.Inc()
		}
		return err
	}
	return nil
}

func writeChecks(ctx context.Context, conn clickhouse.Conn, checks []Check) error {
	batch, err := conn.PrepareBatch(ctx, `INSERT INTO check_results
		(environment_id, monitor_id, time, outcome, duration_ns, status_code, failure_code, failure_message, components, certificate_expires_at, trace_id)`)
	if err != nil {
		return fmt.Errorf("prepare the results' insert: %w", err)
	}
	defer func() { _ = batch.Abort() }() // a no-op once sent
	for _, c := range checks {
		components := ""
		if len(c.Components) > 0 {
			raw, err := json.Marshal(c.Components)
			if err != nil {
				return fmt.Errorf("encode components: %w", err)
			}
			components = string(raw)
		}
		var certificate *time.Time
		if !c.CertificateExpiresAt.IsZero() {
			certificate = &c.CertificateExpiresAt
		}
		status := uint16(min(max(c.Status, 0), 65535)) //nolint:gosec // G115: clamped
		duration := uint64(max(c.Duration, 0))         //nolint:gosec // G115: not negative
		if err := batch.Append(c.EnvironmentID, c.MonitorID, c.At, c.Outcome, duration, status,
			c.FailureCode, c.FailureMessage, components, certificate, c.TraceID); err != nil {
			return fmt.Errorf("add a result to the insert: %w", err)
		}
	}
	if err := batch.Send(); err != nil {
		return fmt.Errorf("write results: %w", err)
	}
	return nil
}

// CheckQuery selects a monitor's results, newest first. From and To are
// required.
type CheckQuery struct {
	From, To time.Time
	// Limit caps the results. Default 100, at most 500.
	Limit int
}

// Checks returns a monitor's results. Every member of the workspace may read
// them. The last second's may not be there yet.
func (m *Module) Checks(ctx context.Context, userID, monitorID uuid.UUID, q CheckQuery) ([]Check, error) {
	if _, err := m.Get(ctx, userID, monitorID); err != nil {
		return nil, err
	}
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultCheckLimit
	}
	limit = min(limit, MaxCheckLimit)
	rows, err := m.conn.Query(ctx, `
		SELECT environment_id, monitor_id, time, toString(outcome), duration_ns, status_code, failure_code, failure_message,
		       components, certificate_expires_at, trace_id
		FROM check_results
		WHERE monitor_id = {monitor:UUID} AND time >= {from:DateTime64(3)} AND time < {to:DateTime64(3)}
		ORDER BY time DESC
		LIMIT {limit:UInt32}`,
		clickhouse.Named("monitor", monitorID), clickhouse.Named("from", q.From), clickhouse.Named("to", q.To),
		clickhouse.Named("limit", uint32(limit))) //nolint:gosec // G115: at most MaxCheckLimit
	if err != nil {
		return nil, fmt.Errorf("read check results: %w", err)
	}
	defer rows.Close()
	var out []Check
	for rows.Next() {
		var (
			c           Check
			duration    uint64
			status      uint16
			components  string
			certificate *time.Time
		)
		if err := rows.Scan(&c.EnvironmentID, &c.MonitorID, &c.At, &c.Outcome, &duration, &status, &c.FailureCode, &c.FailureMessage, &components, &certificate, &c.TraceID); err != nil {
			return nil, fmt.Errorf("read check results: %w", err)
		}
		c.Duration = time.Duration(duration) //nolint:gosec // G115: a check takes seconds
		c.Status = int(status)
		if components != "" {
			if err := json.Unmarshal([]byte(components), &c.Components); err != nil {
				return nil, fmt.Errorf("read check results: %w", err)
			}
		}
		if certificate != nil {
			c.CertificateExpiresAt = *certificate
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read check results: %w", err)
	}
	return out, nil
}

// Stats are a monitor's checks over a time range. Skipped checks say nothing
// of the service, so they don't count.
type Stats struct {
	MonitorID uuid.UUID
	Checks    uint64
	// Failed is how many of them failed; degraded ones count as up.
	Failed uint64
	Mean   time.Duration
	P95    time.Duration
}

// ProjectStats returns the stats of a project's monitors that have results
// in a time range. Every member of the workspace may read them.
func (m *Module) ProjectStats(ctx context.Context, userID, projectID uuid.UUID, from, to time.Time) ([]Stats, error) {
	if err := m.authorize(ctx, userID, projectID, false); err != nil {
		return nil, err
	}
	rows, err := m.db.Query(ctx, `SELECT id FROM monitors WHERE project_id = $1`, projectID)
	if err != nil {
		return nil, fmt.Errorf("read the project's monitors: %w", err)
	}
	var ids []uuid.UUID
	for rows.Next() {
		var v uuid.UUID
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return nil, fmt.Errorf("read the project's monitors: %w", err)
		}
		ids = append(ids, v)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read the project's monitors: %w", err)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	results, err := m.conn.Query(ctx, `
		SELECT monitor_id, count(), countIf(outcome = 'failed'), avg(duration_ns), quantile(0.95)(duration_ns)
		FROM check_results
		WHERE monitor_id IN {ids:Array(UUID)} AND time >= {from:DateTime64(3)} AND time < {to:DateTime64(3)} AND outcome != 'skipped'
		GROUP BY monitor_id`,
		clickhouse.Named("ids", ids), clickhouse.Named("from", from), clickhouse.Named("to", to))
	if err != nil {
		return nil, fmt.Errorf("read monitors' statistics: %w", err)
	}
	defer results.Close()
	var out []Stats
	for results.Next() {
		var (
			s         Stats
			mean, p95 float64
		)
		if err := results.Scan(&s.MonitorID, &s.Checks, &s.Failed, &mean, &p95); err != nil {
			return nil, fmt.Errorf("read monitors' statistics: %w", err)
		}
		s.Mean, s.P95 = time.Duration(mean), time.Duration(p95)
		out = append(out, s)
	}
	if err := results.Err(); err != nil {
		return nil, fmt.Errorf("read monitors' statistics: %w", err)
	}
	return out, nil
}
