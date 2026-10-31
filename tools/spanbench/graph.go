// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/CemTbi/Signalbox/internal/modules/telemetry"
	"github.com/CemTbi/Signalbox/internal/platform/clickhouse"
)

// graph builds the service graph for the last minutes of the history, as
// the worker does a minute at a time, and reports how long each took and
// what it found.
func graph(ctx context.Context, conn clickhouse.Conn, out io.Writer, end time.Time, minutes int) error {
	if err := conn.Exec(ctx, "TRUNCATE TABLE IF EXISTS service_edges_1m"); err != nil {
		return err
	}
	var took []time.Duration
	// The newest minutes lack the children that start after them, so the
	// worker waits; so does this.
	for m := end.Add(-time.Duration(minutes+2) * time.Minute); m.Before(end.Add(-2 * time.Minute)); m = m.Add(time.Minute) {
		var spans uint64
		if err := conn.QueryRow(ctx, `SELECT count() FROM spans WHERE timestamp >= {m:DateTime} AND timestamp < {m:DateTime} + INTERVAL 1 MINUTE`,
			clickhouse.Named("m", m)).Scan(&spans); err != nil {
			return err
		}
		start := time.Now()
		if err := telemetry.BuildGraphMinute(ctx, conn, m); err != nil {
			return err
		}
		took = append(took, time.Since(start))
		fmt.Fprintf(out, "%s  %8d spans  %v\n", m.Format("15:04"), spans, took[len(took)-1].Round(time.Millisecond))
	}
	if len(took) == 0 {
		return fmt.Errorf("no whole minutes before %s", end.Format(time.RFC3339))
	}
	slices.Sort(took)
	fmt.Fprintf(out, "minutes %d  p50 %v  p95 %v  max %v\n", len(took),
		took[len(took)/2].Round(time.Millisecond), took[len(took)*95/100].Round(time.Millisecond), took[len(took)-1].Round(time.Millisecond))
	var edges, calls uint64
	if err := conn.QueryRow(ctx, `SELECT uniqExact(client_service, target, target_kind), sum(calls) FROM service_edges_1m`).Scan(&edges, &calls); err != nil {
		return err
	}
	fmt.Fprintf(out, "lines %d  calls %d\n", edges, calls)
	rows, err := conn.Query(ctx, `
		SELECT client_service, toString(target_kind), target, sum(calls) c FROM service_edges_1m
		GROUP BY client_service, target_kind, target ORDER BY c DESC LIMIT 12`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var from, kind, to string
		var n uint64
		if err := rows.Scan(&from, &kind, &to, &n); err != nil {
			return err
		}
		fmt.Fprintf(out, "  %-20s → %-9s %-24s %d\n", from, kind, to, n)
	}
	return rows.Err()
}
