// SPDX-License-Identifier: Apache-2.0

package telemetry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/CemTbi/Signalbox/internal/modules/workspaces"
	"github.com/CemTbi/Signalbox/internal/platform/clickhouse"
)

// Bounds on what one search reads and returns.
const (
	DefaultTraceLimit = 20
	MaxTraceLimit     = 100
	// MaxSearchRange is the longest time range one search covers.
	MaxSearchRange = 31 * 24 * time.Hour
)

// Traces reads spans for people: every member of a project may read its
// traces, as they may its test runs.
type Traces struct {
	conn   clickhouse.Conn
	access Access
}

// NewTraces returns the reader.
func NewTraces(conn clickhouse.Conn, access Access) *Traces {
	return &Traces{conn: conn, access: access}
}

// TraceQuery selects traces by their spans: a trace matches when one of
// its spans does. From and To are required; the rest filter when set.
type TraceQuery struct {
	From, To    time.Time
	Service     string
	Operation   string
	OnlyErrors  bool
	MinDuration time.Duration
	// Attribute is "key=value".
	Attribute string
	// Limit caps the traces returned. Default 20, at most 100.
	Limit int
}

// TraceSummary describes a trace for a list of them.
type TraceSummary struct {
	TraceID  TraceID
	Start    time.Time
	Duration time.Duration
	Spans    int
	Errors   int
	// Services lists the services with spans in the trace, by name.
	Services []string
	// RootService and RootName name the span that started the trace, or
	// the earliest span when the root hasn't arrived.
	RootService, RootName string
	RootMissing           bool
}

func (t *Traces) canRead(ctx context.Context, userID, environmentID uuid.UUID) error {
	_, _, err := t.access.GetEnvironment(ctx, userID, environmentID)
	if errors.Is(err, workspaces.ErrNotFound) {
		return ErrNotFound
	}
	return err
}

// Search returns the newest traces with a span that matches q, newest
// first.
func (t *Traces) Search(ctx context.Context, userID, environmentID uuid.UUID, q TraceQuery) ([]TraceSummary, error) {
	if err := t.canRead(ctx, userID, environmentID); err != nil {
		return nil, err
	}
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultTraceLimit
	}
	limit = min(limit, MaxTraceLimit)
	// Several matching spans often share a trace, so ask for more spans
	// than traces.
	spans, err := Search(ctx, t.conn, environmentID, SearchFilter{
		From: q.From, To: q.To, Service: q.Service, Operation: q.Operation, OnlyErrors: q.OnlyErrors,
		MinDuration: q.MinDuration, Attribute: q.Attribute, Limit: limit * 5,
	})
	if err != nil {
		return nil, err
	}
	var ids []TraceID
	seen := map[TraceID]bool{}
	for _, s := range spans {
		if !seen[s.TraceID] && len(ids) < limit {
			seen[s.TraceID] = true
			ids = append(ids, s.TraceID)
		}
	}
	return t.summaries(ctx, environmentID, ids)
}

// summaries describes traces, keeping their order.
func (t *Traces) summaries(ctx context.Context, env uuid.UUID, ids []TraceID) ([]TraceSummary, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	hex := make([]string, len(ids))
	for i, id := range ids {
		hex[i] = id.String()
	}
	// trace_ids bounds the seconds the traces cover, so the spans query
	// reads only those.
	var from, to time.Time
	if err := t.conn.QueryRow(ctx, `
		SELECT min(start), max(end) FROM trace_ids
		WHERE environment_id = {env:UUID} AND trace_id IN (SELECT unhex(arrayJoin({ids:Array(String)})))`,
		clickhouse.Named("env", env), clickhouse.Named("ids", hex)).Scan(&from, &to); err != nil {
		return nil, fmt.Errorf("find the traces' times: %w", err)
	}
	rows, err := t.conn.Query(ctx, `
		SELECT hex(trace_id), min(timestamp), max(timestamp + toIntervalNanosecond(duration_ns)), count(),
		       countIf(status_code = 'error'), arraySort(groupUniqArray(service_name)),
		       countIf(parent_span_id = unhex('0000000000000000')),
		       argMin(service_name, (parent_span_id != unhex('0000000000000000'), timestamp)),
		       argMin(span_name, (parent_span_id != unhex('0000000000000000'), timestamp))
		FROM spans
		WHERE environment_id = {env:UUID} AND timestamp >= {from:DateTime64(9)} AND timestamp < {to:DateTime64(9)}
		  AND trace_id IN (SELECT unhex(arrayJoin({ids:Array(String)})))
		GROUP BY trace_id`,
		clickhouse.Named("env", env), clickhouse.Named("from", from), clickhouse.Named("to", to), clickhouse.Named("ids", hex))
	if err != nil {
		return nil, fmt.Errorf("summarize traces: %w", err)
	}
	defer rows.Close()
	byID := map[TraceID]TraceSummary{}
	for rows.Next() {
		var (
			s            TraceSummary
			id           string
			start, end   time.Time
			spans, errs  uint64
			roots        uint64
			rootSvc, nme string
		)
		if err := rows.Scan(&id, &start, &end, &spans, &errs, &s.Services, &roots, &rootSvc, &nme); err != nil {
			return nil, fmt.Errorf("summarize traces: %w", err)
		}
		if s.TraceID, err = ParseTraceID(strings.ToLower(id)); err != nil {
			return nil, err
		}
		s.Start, s.Duration = start, end.Sub(start)
		s.Spans, s.Errors = int(spans), int(errs) //nolint:gosec // G115: counts within one trace
		s.RootService, s.RootName, s.RootMissing = rootSvc, nme, roots == 0
		byID[s.TraceID] = s
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("summarize traces: %w", err)
	}
	out := make([]TraceSummary, 0, len(ids))
	for _, id := range ids {
		if s, ok := byID[id]; ok { // retention may have dropped it since
			out = append(out, s)
		}
	}
	return out, nil
}

// FindTrace returns a trace of the project by its ID, and the environment
// it was sent to. A request's trace lands in whichever environment its
// services send to, so every environment of the project is looked in.
func (t *Traces) FindTrace(ctx context.Context, userID, projectID uuid.UUID, id TraceID) (uuid.UUID, []Span, error) {
	envs, err := t.access.ListEnvironments(ctx, userID, projectID)
	if errors.Is(err, workspaces.ErrNotFound) {
		return uuid.Nil, nil, ErrNotFound
	}
	if err != nil {
		return uuid.Nil, nil, err
	}
	ids := make([]uuid.UUID, len(envs))
	for i, e := range envs {
		ids[i] = e.ID
	}
	var env uuid.UUID
	err = t.conn.QueryRow(ctx, `
		SELECT environment_id FROM trace_ids
		WHERE environment_id IN {envs:Array(UUID)} AND trace_id = unhex({trace:String})
		GROUP BY environment_id ORDER BY count() DESC LIMIT 1`,
		clickhouse.Named("envs", ids), clickhouse.Named("trace", id.String())).Scan(&env)
	if errors.Is(err, sql.ErrNoRows) || err == nil && env == uuid.Nil {
		return uuid.Nil, nil, ErrTraceNotFound
	}
	if err != nil {
		return uuid.Nil, nil, fmt.Errorf("find trace %s: %w", id, err)
	}
	spans, err := Trace(ctx, t.conn, env, id)
	return env, spans, err
}

// ServiceStats are a service's calls, errors and latency over a time
// range, from its server and consumer spans: the requests it handled.
type ServiceStats struct {
	Service       string
	Calls, Errors uint64
	Mean          time.Duration
	P50, P95, P99 time.Duration
}

// Services lists the services that sent spans to an environment in a time
// range, busiest first.
func (t *Traces) Services(ctx context.Context, userID, environmentID uuid.UUID, from, to time.Time) ([]ServiceStats, error) {
	if err := t.canRead(ctx, userID, environmentID); err != nil {
		return nil, err
	}
	rows, err := t.conn.Query(ctx, `
		SELECT service_name,
		       sumIf(calls, span_kind IN ('server', 'consumer')), sumIf(errors, span_kind IN ('server', 'consumer')),
		       sumIf(duration_ns_sum, span_kind IN ('server', 'consumer')),
		       tupleElement(sumMapIf(latency_histogram, span_kind IN ('server', 'consumer')), 1),
		       tupleElement(sumMapIf(latency_histogram, span_kind IN ('server', 'consumer')), 2)
		FROM span_metrics_1m
		WHERE environment_id = {env:UUID} AND minute >= toStartOfMinute({from:DateTime64(9)}) AND minute < {to:DateTime64(9)}
		GROUP BY service_name`,
		clickhouse.Named("env", environmentID), clickhouse.Named("from", from), clickhouse.Named("to", to))
	if err != nil {
		return nil, fmt.Errorf("read service statistics: %w", err)
	}
	defer rows.Close()
	var out []ServiceStats
	for rows.Next() {
		var (
			s       ServiceStats
			sumNS   uint64
			buckets []uint8
			counts  []uint64
		)
		if err := rows.Scan(&s.Service, &s.Calls, &s.Errors, &sumNS, &buckets, &counts); err != nil {
			return nil, fmt.Errorf("read service statistics: %w", err)
		}
		if s.Calls > 0 {
			s.Mean = fromNS(sumNS / s.Calls)
		}
		s.P50, s.P95, s.P99 = percentiles(buckets, counts)
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read service statistics: %w", err)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Calls != out[j].Calls {
			return out[i].Calls > out[j].Calls
		}
		return out[i].Service < out[j].Service
	})
	return out, nil
}
