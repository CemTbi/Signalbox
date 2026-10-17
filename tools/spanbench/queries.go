// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/google/uuid"

	"github.com/CemTbi/Signalbox/internal/modules/telemetry"
	"github.com/CemTbi/Signalbox/internal/platform/clickhouse"
)

// benchQuery is one kind of question the trace UI asks.
type benchQuery struct {
	name string
	run  func(ctx context.Context, r *rand.Rand) error
}

type queryResult struct {
	name      string
	first     time.Duration
	times     []time.Duration
	readRows  uint64
	readBytes uint64
	memory    uint64
}

// queries measures each benchQuery: one first run, then iterations more,
// each with new random parameters. The window is the time searches cover,
// ending at end.
func queries(ctx context.Context, conn clickhouse.Conn, out io.Writer, env uuid.UUID, end time.Time, window time.Duration, iterations int) error {
	from := end.Add(-window)
	traceIDs, err := sampleTraceIDs(ctx, conn, env, from, end)
	if err != nil {
		return err
	}
	if len(traceIDs) == 0 {
		return errors.New("no traces in the window; load some first")
	}
	search := func(f telemetry.SearchFilter) func(context.Context, *rand.Rand) error {
		return func(ctx context.Context, _ *rand.Rand) error {
			f.From, f.To = from, end
			_, err := telemetry.Search(ctx, conn, env, f)
			return err
		}
	}
	suite := []benchQuery{
		{"trace by ID", func(ctx context.Context, r *rand.Rand) error {
			_, err := telemetry.Trace(ctx, conn, env, traceIDs[r.IntN(len(traceIDs))])
			return err
		}},
		{"trace by ID, no bloom filter", func(ctx context.Context, r *rand.Rand) error {
			ctx = ch.Context(ctx, ch.WithSettings(ch.Settings{"ignore_data_skipping_indices": "idx_trace_id"}))
			_, err := telemetry.Trace(ctx, conn, env, traceIDs[r.IntN(len(traceIDs))])
			return err
		}},
		{"trace by ID, unknown ID", func(ctx context.Context, r *rand.Rand) error {
			var id telemetry.TraceID
			binary.BigEndian.PutUint64(id[:8], r.Uint64())
			binary.BigEndian.PutUint64(id[8:], r.Uint64())
			_, err := telemetry.Trace(ctx, conn, env, id)
			switch {
			case errors.Is(err, telemetry.ErrTraceNotFound):
				return nil
			case err != nil:
				return fmt.Errorf("an unknown trace: %w", err)
			}
			return fmt.Errorf("the random trace ID %s matched a trace", id)
		}},
		{"trace by ID, no trace_ids table", func(ctx context.Context, r *rand.Rand) error {
			// Without trace_ids, the time a trace covers is unknown, and every
			// granule's bloom filter in the window has to be checked.
			id := traceIDs[r.IntN(len(traceIDs))]
			rows, err := conn.Query(ctx, `
				SELECT span_id FROM spans
				WHERE environment_id = {env:UUID} AND timestamp >= {from:DateTime64(9)} AND trace_id = unhex({trace:String})`,
				clickhouse.Named("env", env), clickhouse.Named("from", from), clickhouse.Named("trace", id.String()))
			if err != nil {
				return err
			}
			defer rows.Close()
			n := 0
			for rows.Next() {
				n++
			}
			if n == 0 {
				return fmt.Errorf("trace %s has no spans", id)
			}
			return rows.Err()
		}},
		{"search: newest spans of a service", search(telemetry.SearchFilter{Service: "orders-api"})},
		{"search: errors of a service", search(telemetry.SearchFilter{Service: "payments-api", OnlyErrors: true})},
		{"search: errors, every service", search(telemetry.SearchFilter{OnlyErrors: true})},
		{"search: errors, every service, one query", func(ctx context.Context, _ *rand.Rand) error {
			// What Search would cost if it asked for the whole window at once.
			rows, err := conn.Query(ctx, `
				SELECT trace_id, span_id, timestamp, duration_ns, service_name, span_name
				FROM spans
				WHERE environment_id = {env:UUID} AND timestamp >= {from:DateTime64(9)} AND timestamp < {to:DateTime64(9)}
				  AND status_code = 'error'
				ORDER BY timestamp DESC
				LIMIT 50`,
				clickhouse.Named("env", env), clickhouse.Named("from", from), clickhouse.Named("to", end))
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
			}
			return rows.Err()
		}},
		{"search: slower than 1 s, every service", search(telemetry.SearchFilter{MinDuration: time.Second})},
		{"search: route and status code", search(telemetry.SearchFilter{HTTPRoute: "/payments", HTTPStatusCode: 502})},
		{"search: one product's URL path", func(ctx context.Context, r *rand.Rand) error {
			_, err := telemetry.Search(ctx, conn, env, telemetry.SearchFilter{From: from, To: end,
				Attribute: "url.path=/products/" + strconv.Itoa(100000+r.IntN(900000))})
			return err
		}},
		{"search: one product's URL path, no text index", func(ctx context.Context, r *rand.Rand) error {
			ctx = ch.Context(ctx, ch.WithSettings(ch.Settings{"ignore_data_skipping_indices": "idx_span_attribute_items"}))
			_, err := telemetry.Search(ctx, conn, env, telemetry.SearchFilter{From: from, To: end,
				Attribute: "url.path=/products/" + strconv.Itoa(100000+r.IntN(900000))})
			return err
		}},
		{"search: a common attribute value", search(telemetry.SearchFilter{Attribute: "db.operation.name=INSERT"})},
		{"operations from the rollup", func(ctx context.Context, _ *rand.Rand) error {
			_, err := telemetry.Operations(ctx, conn, env, "", from, end)
			return err
		}},
		{"operations from raw spans", func(ctx context.Context, _ *rand.Rand) error {
			rows, err := conn.Query(ctx, `
				SELECT service_name, span_name, count(), countIf(status_code = 'error'), quantiles(0.5, 0.95, 0.99)(duration_ns)
				FROM spans
				WHERE environment_id = {env:UUID} AND timestamp >= {from:DateTime64(9)} AND timestamp < {to:DateTime64(9)}
				GROUP BY service_name, span_name`,
				clickhouse.Named("env", env), clickhouse.Named("from", from), clickhouse.Named("to", end))
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
			}
			return rows.Err()
		}},
	}

	r := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // G404: seeded, so runs pick the same parameters.
	run := uuid.NewString()[:8]
	var results []queryResult
	for qi, q := range suite {
		res := queryResult{name: q.name}
		for i := 0; i <= iterations; i++ {
			qctx := ch.Context(ctx, ch.WithQueryID(fmt.Sprintf("spanbench-%s-%d-%d", run, qi, i)))
			started := time.Now()
			if err := q.run(qctx, r); err != nil {
				return fmt.Errorf("%s: %w", q.name, err)
			}
			took := time.Since(started)
			if i == 0 {
				res.first = took
			} else {
				res.times = append(res.times, took)
			}
		}
		results = append(results, res)
	}
	if err := readQueryLog(ctx, conn, run, results); err != nil {
		fmt.Fprintf(out, "(couldn't read system.query_log: %v)\n", err)
	}

	fmt.Fprintf(out, "%d trace IDs sampled; window %s ending %s; %d runs after a first one\n\n",
		len(traceIDs), window, end.Format(time.RFC3339), iterations)
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(tw, "query\tfirst\tp50\tp95\tmax\trows read\tMB read\tMB memory\t")
	for _, res := range results {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%d\t%.1f\t%.1f\t\n", res.name, ms(res.first),
			ms(percentile(res.times, 0.5)), ms(percentile(res.times, 0.95)), ms(percentile(res.times, 1)),
			res.readRows, float64(res.readBytes)/1e6, float64(res.memory)/1e6)
	}
	return tw.Flush()
}

func ms(d time.Duration) string { return fmt.Sprintf("%.1f ms", float64(d.Microseconds())/1000) }

// percentile returns the q-quantile of d by the nearest-rank method.
func percentile(d []time.Duration, q float64) time.Duration {
	if len(d) == 0 {
		return 0
	}
	s := slices.Clone(d)
	slices.Sort(s)
	return s[max(0, int(math.Ceil(q*float64(len(s))))-1)]
}

// sampleTraceIDs picks up to 500 traces from the window, spread over it.
func sampleTraceIDs(ctx context.Context, conn clickhouse.Conn, env uuid.UUID, from, to time.Time) ([]telemetry.TraceID, error) {
	rows, err := conn.Query(ctx, `
		SELECT hex(trace_id) FROM trace_ids
		WHERE environment_id = {env:UUID} AND start >= {from:DateTime64(9)} AND start < {to:DateTime64(9)}
		ORDER BY cityHash64(trace_id)
		LIMIT 500`,
		clickhouse.Named("env", env), clickhouse.Named("from", from), clickhouse.Named("to", to))
	if err != nil {
		return nil, fmt.Errorf("sample trace IDs: %w", err)
	}
	defer rows.Close()
	var ids []telemetry.TraceID
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		id, err := telemetry.ParseTraceID(strings.ToLower(h))
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// readQueryLog adds what ClickHouse read for each query, the mean over the
// runs after the first, from system.query_log.
func readQueryLog(ctx context.Context, conn clickhouse.Conn, run string, results []queryResult) error {
	if err := conn.Exec(ctx, "SYSTEM FLUSH LOGS"); err != nil {
		return err
	}
	rows, err := conn.Query(ctx, `
		SELECT extract(query_id, '^spanbench-[^-]+-(\\d+)-') AS q, sum(read_rows), sum(read_bytes), max(memory_usage), uniqExact(query_id)
		FROM system.query_log
		WHERE type = 'QueryFinish' AND startsWith(query_id, {prefix:String}) AND NOT endsWith(query_id, '-0')
		  AND event_date >= yesterday()
		GROUP BY q`, clickhouse.Named("prefix", "spanbench-"+run+"-"))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			q               string
			rowsRead, bytes uint64
			memory          uint64
			runs            uint64
		)
		if err := rows.Scan(&q, &rowsRead, &bytes, &memory, &runs); err != nil {
			return err
		}
		i, err := strconv.Atoi(q)
		if err != nil || i >= len(results) || runs == 0 {
			continue
		}
		results[i].readRows = rowsRead / runs
		results[i].readBytes = bytes / runs
		results[i].memory = memory
	}
	return rows.Err()
}
