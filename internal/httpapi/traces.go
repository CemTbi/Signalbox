// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/CemTbi/Signalbox/internal/modules/identity"
	"github.com/CemTbi/Signalbox/internal/modules/telemetry"
	"github.com/CemTbi/Signalbox/internal/platform/id"
	"github.com/CemTbi/Signalbox/internal/platform/validate"
)

// Traces is what the API needs from the telemetry module's reader.
type Traces interface {
	Search(ctx context.Context, userID, environmentID uuid.UUID, q telemetry.TraceQuery) ([]telemetry.TraceSummary, error)
	FindTrace(ctx context.Context, userID, projectID uuid.UUID, traceID telemetry.TraceID) (uuid.UUID, []telemetry.Span, error)
	Services(ctx context.Context, userID, environmentID uuid.UUID, from, to time.Time) ([]telemetry.ServiceStats, error)
	Graph(ctx context.Context, userID, environmentID uuid.UUID, from, to time.Time) ([]telemetry.Edge, error)
	Operations(ctx context.Context, userID, environmentID uuid.UUID, service string, from, to time.Time) ([]telemetry.OperationStats, error)
}

// EdgeJSON is a line of the service graph.
type EdgeJSON struct {
	From   string `json:"from"`
	To     string `json:"to"`
	ToKind string `json:"toKind"`
	// System is a database's db.system.
	System string  `json:"system,omitempty"`
	Calls  uint64  `json:"calls"`
	Errors uint64  `json:"errors"`
	MeanMs float64 `json:"meanMs"`
	P50Ms  float64 `json:"p50Ms"`
	P95Ms  float64 `json:"p95Ms"`
	P99Ms  float64 `json:"p99Ms"`
}

// OperationStatsJSON is an operation's traffic over a time range.
type OperationStatsJSON struct {
	Service   string  `json:"service"`
	Operation string  `json:"operation"`
	Kind      string  `json:"kind"`
	Calls     uint64  `json:"calls"`
	Errors    uint64  `json:"errors"`
	MeanMs    float64 `json:"meanMs"`
	P50Ms     float64 `json:"p50Ms"`
	P95Ms     float64 `json:"p95Ms"`
	P99Ms     float64 `json:"p99Ms"`
}

// TraceSummaryJSON is a trace in a search's results.
type TraceSummaryJSON struct {
	TraceID     string   `json:"traceId"`
	Start       string   `json:"start"`
	DurationMs  float64  `json:"durationMs"`
	Spans       int      `json:"spans"`
	Errors      int      `json:"errors"`
	Services    []string `json:"services"`
	RootService string   `json:"rootService"`
	RootName    string   `json:"rootName"`
	RootMissing bool     `json:"rootMissing"`
}

// TraceJSON is a whole trace.
type TraceJSON struct {
	TraceID       string     `json:"traceId"`
	EnvironmentID string     `json:"environmentId"`
	Spans         []SpanJSON `json:"spans"`
}

// SpanJSON is one span of a trace.
type SpanJSON struct {
	SpanID        string            `json:"spanId"`
	ParentSpanID  string            `json:"parentSpanId,omitempty"`
	Name          string            `json:"name"`
	Operation     string            `json:"operation"`
	Kind          string            `json:"kind"`
	Service       string            `json:"service"`
	Start         string            `json:"start"`
	DurationMs    float64           `json:"durationMs"`
	Status        string            `json:"status"`
	StatusMessage string            `json:"statusMessage,omitempty"`
	Attributes    map[string]string `json:"attributes"`
	Resource      map[string]string `json:"resource"`
	Scope         *SpanScopeJSON    `json:"scope,omitempty"`
	Events        []SpanEventJSON   `json:"events"`
	Links         []SpanLinkJSON    `json:"links"`
}

// SpanScopeJSON names the instrumentation that made a span.
type SpanScopeJSON struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// SpanEventJSON is something that happened during a span.
type SpanEventJSON struct {
	Time       string            `json:"time"`
	Name       string            `json:"name"`
	Attributes map[string]string `json:"attributes"`
}

// SpanLinkJSON points to a span in another trace.
type SpanLinkJSON struct {
	TraceID    string            `json:"traceId"`
	SpanID     string            `json:"spanId"`
	Attributes map[string]string `json:"attributes"`
}

// ServiceStatsJSON is a service's traffic over a time range.
type ServiceStatsJSON struct {
	Service string  `json:"service"`
	Calls   uint64  `json:"calls"`
	Errors  uint64  `json:"errors"`
	MeanMs  float64 `json:"meanMs"`
	P50Ms   float64 `json:"p50Ms"`
	P95Ms   float64 `json:"p95Ms"`
	P99Ms   float64 `json:"p99Ms"`
}

func preciseTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func (a *api) tracesError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, telemetry.ErrNotFound):
		writeProblem(w, r, http.StatusNotFound, "not-found", "There is no project or environment with this ID.")
	case errors.Is(err, telemetry.ErrTraceNotFound):
		writeProblem(w, r, http.StatusNotFound, "trace-not-found",
			"No spans of this trace have arrived in the project's environments, or they are older than the retention period.")
	default:
		a.fail(w, r, "trace", err)
	}
}

// timeRange reads from and to, RFC 3339 times, defaulting to the last hour.
func timeRange(r *http.Request, fields *validate.Errors) (from, to time.Time) {
	to = time.Now()
	read := func(name string, def time.Time) time.Time {
		v := r.URL.Query().Get(name)
		if v == "" {
			return def
		}
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			fields.Add("/"+name, "format", "Send a time such as 2026-10-31T12:00:00Z.")
			return def
		}
		return t
	}
	to = read("to", to)
	from = read("from", to.Add(-time.Hour))
	switch {
	case len(*fields) > 0:
	case !from.Before(to):
		fields.Add("/from", "range", "Send a from before to.")
	case to.Sub(from) > telemetry.MaxSearchRange:
		fields.Add("/from", "range", "Search at most 31 days at once.")
	}
	return from, to
}

func (a *api) searchTraces(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	envID, ok := pathID(w, r, "environmentId", id.Environment, "environment")
	if !ok {
		return
	}
	var fields validate.Errors
	q := telemetry.TraceQuery{
		Service: r.URL.Query().Get("service"), Operation: r.URL.Query().Get("operation"),
		Attribute: r.URL.Query().Get("attribute"),
	}
	q.From, q.To = timeRange(r, &fields)
	switch v := r.URL.Query().Get("errors"); v {
	case "", "false":
	case "true":
		q.OnlyErrors = true
	default:
		fields.Add("/errors", "type", "Send true or false.")
	}
	if v := r.URL.Query().Get("minDurationMs"); v != "" {
		ms, err := strconv.ParseFloat(v, 64)
		if err != nil || ms < 0 || ms > 24*3600*1000 {
			fields.Add("/minDurationMs", "range", "Send milliseconds, from 0 to a day.")
		} else {
			q.MinDuration = time.Duration(ms * float64(time.Millisecond))
		}
	}
	if q.Attribute != "" && !strings.Contains(q.Attribute, "=") {
		fields.Add("/attribute", "format", "Send an attribute as key=value, such as http.route=/orders.")
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > telemetry.MaxTraceLimit {
			fields.Add("/limit", "range", fmt.Sprintf("Send a limit from 1 to %d.", telemetry.MaxTraceLimit))
		}
		q.Limit = n
	}
	if len(fields) > 0 {
		writeInvalid(w, r, fields)
		return
	}
	found, err := a.Traces.Search(r.Context(), s.User.ID, envID, q)
	if err != nil {
		a.tracesError(w, r, err)
		return
	}
	out := make([]TraceSummaryJSON, len(found))
	for i, t := range found {
		out[i] = TraceSummaryJSON{
			TraceID: t.TraceID.String(), Start: preciseTime(t.Start), DurationMs: ms(t.Duration), Spans: t.Spans, Errors: t.Errors,
			Services: t.Services, RootService: t.RootService, RootName: t.RootName, RootMissing: t.RootMissing,
		}
		if out[i].Services == nil {
			out[i].Services = []string{}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

func (a *api) getTrace(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	projectID, ok := pathID(w, r, "projectId", id.Project, "project")
	if !ok {
		return
	}
	traceID, err := telemetry.ParseTraceID(r.PathValue("traceId"))
	if err != nil || traceID.IsZero() {
		writeInvalid(w, r, validate.Errors{{Pointer: "/traceId", Code: "format", Detail: "Send a trace ID of 32 lowercase hex digits."}})
		return
	}
	env, spans, err := a.Traces.FindTrace(r.Context(), s.User.ID, projectID, traceID)
	if err != nil {
		a.tracesError(w, r, err)
		return
	}
	out := TraceJSON{TraceID: traceID.String(), EnvironmentID: id.Format(id.Environment, env), Spans: make([]SpanJSON, len(spans))}
	for i, sp := range spans {
		out.Spans[i] = spanJSON(sp)
	}
	writeJSON(w, http.StatusOK, out)
}

func spanJSON(s telemetry.Span) SpanJSON {
	out := SpanJSON{
		SpanID: s.SpanID.String(), Name: s.Name, Operation: s.Operation, Kind: s.Kind.String(), Service: s.Service,
		Start: preciseTime(s.Start), DurationMs: ms(s.Duration), Status: s.Status.String(), StatusMessage: s.StatusMessage,
		Attributes: nonNil(s.Attributes), Resource: nonNil(s.ResourceAttributes),
		Events: make([]SpanEventJSON, len(s.Events)), Links: make([]SpanLinkJSON, len(s.Links)),
	}
	if !s.ParentSpanID.IsZero() {
		out.ParentSpanID = s.ParentSpanID.String()
	}
	if s.ScopeName != "" {
		out.Scope = &SpanScopeJSON{Name: s.ScopeName, Version: s.ScopeVersion}
	}
	for i, e := range s.Events {
		out.Events[i] = SpanEventJSON{Time: preciseTime(e.Time), Name: e.Name, Attributes: nonNil(e.Attributes)}
	}
	for i, l := range s.Links {
		out.Links[i] = SpanLinkJSON{TraceID: hex.EncodeToString(l.TraceID[:]), SpanID: hex.EncodeToString(l.SpanID[:]), Attributes: nonNil(l.Attributes)}
	}
	return out
}

func nonNil(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

func (a *api) listTracedServices(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	envID, ok := pathID(w, r, "environmentId", id.Environment, "environment")
	if !ok {
		return
	}
	var fields validate.Errors
	from, to := timeRange(r, &fields)
	if len(fields) > 0 {
		writeInvalid(w, r, fields)
		return
	}
	stats, err := a.Traces.Services(r.Context(), s.User.ID, envID, from, to)
	if err != nil {
		a.tracesError(w, r, err)
		return
	}
	out := make([]ServiceStatsJSON, len(stats))
	for i, st := range stats {
		out[i] = ServiceStatsJSON{Service: st.Service, Calls: st.Calls, Errors: st.Errors,
			MeanMs: ms(st.Mean), P50Ms: ms(st.P50), P95Ms: ms(st.P95), P99Ms: ms(st.P99)}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

func (a *api) getGraph(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	envID, ok := pathID(w, r, "environmentId", id.Environment, "environment")
	if !ok {
		return
	}
	var fields validate.Errors
	from, to := timeRange(r, &fields)
	if len(fields) > 0 {
		writeInvalid(w, r, fields)
		return
	}
	edges, err := a.Traces.Graph(r.Context(), s.User.ID, envID, from, to)
	if err != nil {
		a.tracesError(w, r, err)
		return
	}
	out := make([]EdgeJSON, len(edges))
	for i, e := range edges {
		out[i] = EdgeJSON{From: e.From, To: e.Target, ToKind: string(e.TargetKind), System: e.System, Calls: e.Calls, Errors: e.Errors,
			MeanMs: ms(e.Mean), P50Ms: ms(e.P50), P95Ms: ms(e.P95), P99Ms: ms(e.P99)}
	}
	writeJSON(w, http.StatusOK, map[string]any{"edges": out})
}

func (a *api) listOperations(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	envID, ok := pathID(w, r, "environmentId", id.Environment, "environment")
	if !ok {
		return
	}
	var fields validate.Errors
	from, to := timeRange(r, &fields)
	if len(fields) > 0 {
		writeInvalid(w, r, fields)
		return
	}
	ops, err := a.Traces.Operations(r.Context(), s.User.ID, envID, r.URL.Query().Get("service"), from, to)
	if err != nil {
		a.tracesError(w, r, err)
		return
	}
	out := make([]OperationStatsJSON, len(ops))
	for i, o := range ops {
		out[i] = OperationStatsJSON{Service: o.Service, Operation: o.Name, Kind: o.Kind.String(), Calls: o.Calls, Errors: o.Errors,
			MeanMs: ms(o.Mean), P50Ms: ms(o.P50), P95Ms: ms(o.P95), P99Ms: ms(o.P99)}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}
