// SPDX-License-Identifier: Apache-2.0

package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/CemTbi/Signalbox/internal/platform/clickhouse"
)

// ErrTraceNotFound means no spans of the trace are stored.
var ErrTraceNotFound = errors.New("telemetry: trace not found")

const spanColumns = `trace_id, span_id, parent_span_id, trace_state, timestamp, duration_ns,
    service_name, span_name, operation, span_kind, status_code, status_message,
    http_method, http_route, http_status_code, server_address, db_system,
    scope_name, scope_version, resource, span_attributes,
    ` + "`events.timestamp`, `events.name`, `events.attributes`," + `
    ` + "`links.trace_id`, `links.span_id`, `links.trace_state`, `links.attributes`"

// TraceTimes returns the seconds a trace's spans cover, from the trace_ids
// table: every span starts at or after start, and before end.
func TraceTimes(ctx context.Context, conn clickhouse.Conn, env uuid.UUID, id TraceID) (start, end time.Time, err error) {
	var n uint64
	err = conn.QueryRow(ctx, `
		SELECT count(), min(start), max(end) FROM trace_ids
		WHERE environment_id = {env:UUID} AND trace_id = unhex({trace:String})`,
		clickhouse.Named("env", env), clickhouse.Named("trace", id.String()),
	).Scan(&n, &start, &end)
	if err != nil {
		return start, end, fmt.Errorf("look up trace %s: %w", id, err)
	}
	if n == 0 {
		return start, end, ErrTraceNotFound
	}
	return start, end, nil
}

// Trace returns a trace's spans in the order they started.
func Trace(ctx context.Context, conn clickhouse.Conn, env uuid.UUID, id TraceID) ([]Span, error) {
	start, end, err := TraceTimes(ctx, conn, env, id)
	if err != nil {
		return nil, err
	}
	rows, err := conn.Query(ctx, `
		SELECT `+spanColumns+` FROM spans
		WHERE environment_id = {env:UUID}
		  AND timestamp >= {start:DateTime64(9)} AND timestamp < {end:DateTime64(9)}
		  AND trace_id = unhex({trace:String})
		ORDER BY timestamp`,
		clickhouse.Named("env", env), clickhouse.Named("start", start), clickhouse.Named("end", end),
		clickhouse.Named("trace", id.String()))
	if err != nil {
		return nil, fmt.Errorf("read trace %s: %w", id, err)
	}
	defer rows.Close()
	var spans []Span
	for rows.Next() {
		s, err := scanSpan(rows)
		if err != nil {
			return nil, fmt.Errorf("read trace %s: %w", id, err)
		}
		s.EnvironmentID = env
		spans = append(spans, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read trace %s: %w", id, err)
	}
	if len(spans) == 0 {
		return nil, ErrTraceNotFound // removed by retention since the lookup
	}
	return spans, nil
}

type scanner interface{ Scan(dest ...any) error }

func scanSpan(rows scanner) (Span, error) {
	var (
		s                                 Span
		traceID, spanID, parentID         string
		resource                          string
		durationNS                        uint64
		kind, status                      string
		eventTimes                        []time.Time
		eventNames                        []string
		eventAttrs, linkAttrs             []map[string]string
		linkTraces, linkSpans, linkStates []string
	)
	err := rows.Scan(&traceID, &spanID, &parentID, &s.TraceState, &s.Start, &durationNS,
		&s.Service, &s.Name, &s.Operation, &kind, &status, &s.StatusMessage,
		&s.HTTPMethod, &s.HTTPRoute, &s.HTTPStatusCode, &s.ServerAddress, &s.DBSystem,
		&s.ScopeName, &s.ScopeVersion, &resource, &s.Attributes,
		&eventTimes, &eventNames, &eventAttrs,
		&linkTraces, &linkSpans, &linkStates, &linkAttrs)
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal([]byte(resource), &s.ResourceAttributes); err != nil {
		return s, fmt.Errorf("read the resource: %w", err)
	}
	copy(s.TraceID[:], traceID)
	copy(s.SpanID[:], spanID)
	copy(s.ParentSpanID[:], parentID)
	s.Duration = fromNS(durationNS)
	s.Kind, s.Status = parseKind(kind), parseStatus(status)
	restoreAttributes(&s)
	for i := range eventTimes {
		s.Events = append(s.Events, Event{Time: eventTimes[i], Name: eventNames[i], Attributes: eventAttrs[i]})
	}
	for i := range linkTraces {
		l := Link{TraceState: linkStates[i], Attributes: linkAttrs[i]}
		copy(l.TraceID[:], linkTraces[i])
		copy(l.SpanID[:], linkSpans[i])
		s.Links = append(s.Links, l)
	}
	return s, nil
}

var kindNames = []string{"unspecified", "internal", "server", "client", "producer", "consumer"}

func parseKind(name string) SpanKind {
	for i, n := range kindNames {
		if n == name {
			return SpanKind(i)
		}
	}
	return KindUnspecified
}

func (k SpanKind) String() string {
	if int(k) < len(kindNames) {
		return kindNames[k]
	}
	return "unspecified"
}

var statusNames = []string{"unset", "ok", "error"}

func (c StatusCode) String() string {
	if int(c) < len(statusNames) {
		return statusNames[c]
	}
	return "unset"
}

func parseStatus(name string) StatusCode {
	switch name {
	case "ok":
		return StatusOK
	case "error":
		return StatusError
	}
	return StatusUnset
}

// SearchFilter selects spans. From and To are required; the other fields
// filter only when set.
type SearchFilter struct {
	From, To       time.Time
	Service        string
	Name           string
	Operation      string
	OnlyErrors     bool
	MinDuration    time.Duration
	HTTPRoute      string
	HTTPStatusCode uint16
	// Attribute is "key=value" and matches spans with that attribute.
	Attribute string
	// Limit caps the result. Default 50.
	Limit int
}

// promotedColumns are the columns that hold the promoted attributes, as
// text for comparison with a filter's value.
var promotedColumns = map[string]string{
	"http.request.method":       "http_method",
	"http.route":                "http_route",
	"http.response.status_code": "toString(http_status_code)",
	"server.address":            "server_address",
	"db.system.name":            "db_system",
}

// SpanSummary is a span as a search lists it.
type SpanSummary struct {
	TraceID        TraceID
	SpanID         SpanID
	Start          time.Time
	Duration       time.Duration
	Service        string
	Name           string
	Kind           SpanKind
	Status         StatusCode
	HTTPMethod     string
	HTTPRoute      string
	HTTPStatusCode uint16
}

// Search returns the newest spans that match f. It searches the last five
// minutes of the range first and reaches further back, four times as far
// each time, until it has enough spans. The newest matches are usually
// recent, so ClickHouse reads a small part of a long range instead of all
// of it.
func Search(ctx context.Context, conn clickhouse.Conn, env uuid.UUID, f SearchFilter) ([]SpanSummary, error) {
	if f.From.IsZero() || f.To.IsZero() || !f.From.Before(f.To) {
		return nil, errors.New("telemetry: a search needs a time range")
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	where, args, err := searchConditions(env, f)
	if err != nil {
		return nil, err
	}
	var out []SpanSummary
	end, step := f.To, 5*time.Minute
	for len(out) < limit && end.After(f.From) {
		start := end.Add(-step)
		if start.Before(f.From) {
			start = f.From
		}
		found, err := searchWindow(ctx, conn, where, args, start, end, limit-len(out))
		if err != nil {
			return nil, err
		}
		out = append(out, found...)
		end, step = start, 4*step
	}
	return out, nil
}

// searchConditions turns a filter, apart from its time range, into SQL
// conditions and their parameters.
func searchConditions(env uuid.UUID, f SearchFilter) ([]string, []any, error) {
	where := []string{"environment_id = {env:UUID}"}
	args := []any{clickhouse.Named("env", env)}
	add := func(cond, name string, value any) {
		where = append(where, cond)
		args = append(args, clickhouse.Named(name, value))
	}
	if f.Service != "" {
		add("service_name = {service:String}", "service", f.Service)
	}
	if f.Name != "" {
		add("span_name = {name:String}", "name", f.Name)
	}
	if f.Operation != "" {
		add("operation = {operation:String}", "operation", f.Operation)
	}
	if f.OnlyErrors {
		where = append(where, "status_code = 'error'")
	}
	if f.MinDuration > 0 {
		add("duration_ns >= {min_duration:UInt64}", "min_duration", durationNS(f.MinDuration))
	}
	if f.HTTPRoute != "" {
		add("http_route = {route:String}", "route", f.HTTPRoute)
	}
	if f.HTTPStatusCode != 0 {
		add("http_status_code = {status:UInt16}", "status", f.HTTPStatusCode)
	}
	if f.Attribute != "" {
		key, value, ok := strings.Cut(f.Attribute, "=")
		if !ok {
			return nil, nil, errors.New("telemetry: an attribute filter is key=value")
		}
		// Attributes with a column of their own aren't in the map.
		if column, ok := promotedColumns[key]; ok {
			add(column+" = {attribute:String}", "attribute", value)
		} else {
			add("has(span_attribute_items, {attribute:String})", "attribute", f.Attribute)
		}
	}
	return where, args, nil
}

// searchWindow returns the newest spans in [from, to) that meet the
// conditions.
func searchWindow(ctx context.Context, conn clickhouse.Conn, where []string, args []any, from, to time.Time, limit int) ([]SpanSummary, error) {
	args = append(slices.Clip(args), clickhouse.Named("from", from), clickhouse.Named("to", to),
		clickhouse.Named("limit", limit))
	rows, err := conn.Query(ctx, `
		SELECT trace_id, span_id, timestamp, duration_ns, service_name, span_name, span_kind, status_code,
		       http_method, http_route, http_status_code
		FROM spans
		WHERE `+strings.Join(where, " AND ")+`
		  AND timestamp >= {from:DateTime64(9)} AND timestamp < {to:DateTime64(9)}
		ORDER BY timestamp DESC
		LIMIT {limit:UInt32}`, args...)
	if err != nil {
		return nil, fmt.Errorf("search spans: %w", err)
	}
	defer rows.Close()
	var out []SpanSummary
	for rows.Next() {
		var (
			s               SpanSummary
			traceID, spanID string
			durationNS      uint64
			kind, status    string
		)
		if err := rows.Scan(&traceID, &spanID, &s.Start, &durationNS, &s.Service, &s.Name, &kind, &status,
			&s.HTTPMethod, &s.HTTPRoute, &s.HTTPStatusCode); err != nil {
			return nil, fmt.Errorf("search spans: %w", err)
		}
		copy(s.TraceID[:], traceID)
		copy(s.SpanID[:], spanID)
		s.Duration = fromNS(durationNS)
		s.Kind, s.Status = parseKind(kind), parseStatus(status)
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("search spans: %w", err)
	}
	return out, nil
}

// OperationStats are an operation's rate, errors and latency over a time
// range, from the per-minute rollup.
type OperationStats struct {
	Service, Name string
	Kind          SpanKind
	Calls, Errors uint64
	Mean          time.Duration
	// P50, P95 and P99 come from a histogram whose buckets are 19% wide:
	// each is the upper edge of the bucket holding that percentile.
	P50, P95, P99 time.Duration
}

// Operations returns the statistics of every operation of a service, or
// of every service when service is "", busiest first.
func Operations(ctx context.Context, conn clickhouse.Conn, env uuid.UUID, service string, from, to time.Time) ([]OperationStats, error) {
	where := "environment_id = {env:UUID} AND minute >= toStartOfMinute({from:DateTime64(9)}) AND minute < {to:DateTime64(9)}"
	args := []any{clickhouse.Named("env", env), clickhouse.Named("from", from), clickhouse.Named("to", to)}
	if service != "" {
		where += " AND service_name = {service:String}"
		args = append(args, clickhouse.Named("service", service))
	}
	rows, err := conn.Query(ctx, `
		SELECT service_name, span_name, span_kind, sum(calls), sum(errors), sum(duration_ns_sum),
		       tupleElement(sumMap(latency_histogram), 1), tupleElement(sumMap(latency_histogram), 2)
		FROM span_metrics_1m
		WHERE `+where+`
		GROUP BY service_name, span_name, span_kind`, args...)
	if err != nil {
		return nil, fmt.Errorf("read operation statistics: %w", err)
	}
	defer rows.Close()
	var out []OperationStats
	for rows.Next() {
		var (
			o       OperationStats
			kind    string
			sumNS   uint64
			buckets []uint8
			counts  []uint64
		)
		if err := rows.Scan(&o.Service, &o.Name, &kind, &o.Calls, &o.Errors, &sumNS, &buckets, &counts); err != nil {
			return nil, fmt.Errorf("read operation statistics: %w", err)
		}
		o.Kind = parseKind(kind)
		if o.Calls > 0 {
			o.Mean = fromNS(sumNS / o.Calls)
		}
		o.P50, o.P95, o.P99 = percentiles(buckets, counts)
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read operation statistics: %w", err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Calls > out[j].Calls })
	return out, nil
}

// percentiles reads the 50th, 95th and 99th percentiles from a latency
// histogram, as the upper edges of their buckets. Buckets arrive sorted.
func percentiles(buckets []uint8, counts []uint64) (p50, p95, p99 time.Duration) {
	var total uint64
	for _, c := range counts {
		total += c
	}
	if total == 0 {
		return 0, 0, 0
	}
	edge := func(b uint8) time.Duration {
		return time.Duration(math.Pow(2, float64(b+1)/4) * float64(time.Microsecond))
	}
	var seen uint64
	targets := []float64{0.50, 0.95, 0.99}
	results := make([]time.Duration, len(targets))
	next := 0
	for i, c := range counts {
		seen += c
		for next < len(targets) && float64(seen) >= targets[next]*float64(total) {
			results[next] = edge(buckets[i])
			next++
		}
	}
	return results[0], results[1], results[2]
}
