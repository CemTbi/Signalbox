// SPDX-License-Identifier: Apache-2.0

package httpapi_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/CemTbi/Signalbox/internal/httpapi"
	"github.com/CemTbi/Signalbox/internal/modules/telemetry"
	"github.com/CemTbi/Signalbox/internal/platform/clickhouse"
	"github.com/CemTbi/Signalbox/internal/platform/clickhouse/clickhousetest"
	"github.com/CemTbi/Signalbox/internal/platform/id"
)

func TestTraces(t *testing.T) {
	ch, chURL := clickhousetest.NewWithURL(t)
	if err := clickhouse.Migrate(context.Background(), chURL, nil, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	h := newHarnessWithTraces(t, ch)
	h.setUp()
	p := h.newProject("shop")
	var envs struct {
		Data []httpapi.EnvironmentJSON `json:"data"`
	}
	h.do(call{method: http.MethodGet, path: "/api/v1/projects/" + p.ID + "/environments"}).decode(t, &envs)
	prod, err := id.Parse(id.Environment, envs.Data[2].ID)
	if err != nil {
		t.Fatal(err)
	}

	// A request sent from Signalbox carries a traceparent, and the API it
	// called sends its span in that trace.
	var traceparent string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceparent = r.Header.Get("traceparent")
		w.WriteHeader(http.StatusAccepted)
	}))
	defer target.Close()
	var sent httpapi.HistoryEntryJSON
	h.do(call{method: http.MethodPost, path: "/api/v1/projects/" + p.ID + "/send", body: map[string]any{"method": "POST", "url": target.URL + "/orders"}}).decode(t, &sent)
	if len(sent.TraceID) != 32 || !strings.Contains(traceparent, "-"+sent.TraceID+"-") {
		t.Fatalf("sent in trace %q with traceparent %q", sent.TraceID, traceparent)
	}
	var again httpapi.HistoryEntryJSON
	h.do(call{method: http.MethodGet, path: "/api/v1/history/" + sent.ID}).decode(t, &again)
	if again.TraceID != sent.TraceID {
		t.Errorf("history keeps trace %q, want %q", again.TraceID, sent.TraceID)
	}

	trace, err := telemetry.ParseTraceID(sent.TraceID)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(-time.Minute).UTC()
	spans := []telemetry.Span{
		{EnvironmentID: prod, TraceID: trace, SpanID: telemetry.SpanID{1}, Start: start, Duration: 80 * time.Millisecond,
			Service: "orders-api", Name: "POST /orders", Operation: "POST /orders", Kind: telemetry.KindServer,
			Attributes: map[string]string{"http.route": "/orders"}, ResourceAttributes: map[string]string{"service.name": "orders-api"},
			ScopeName: "otelhttp", Events: []telemetry.Event{{Time: start.Add(time.Millisecond), Name: "validated"}}},
		{EnvironmentID: prod, TraceID: trace, SpanID: telemetry.SpanID{2}, ParentSpanID: telemetry.SpanID{1}, Start: start.Add(5 * time.Millisecond),
			Duration: 30 * time.Millisecond, Service: "orders-api", Name: "INSERT orders", Kind: telemetry.KindClient, Status: telemetry.StatusError,
			StatusMessage: "deadlock", DBSystem: "postgresql", Attributes: map[string]string{"db.system.name": "postgresql"}},
	}
	if err := telemetry.WriteSpans(context.Background(), ch, spans); err != nil {
		t.Fatal(err)
	}

	// The response's trace opens from the project, whichever environment
	// the spans went to.
	var got httpapi.TraceJSON
	h.do(call{method: http.MethodGet, path: "/api/v1/projects/" + p.ID + "/traces/" + sent.TraceID}).decode(t, &got)
	if got.EnvironmentID != envs.Data[2].ID || len(got.Spans) != 2 {
		t.Fatalf("trace = %+v", got)
	}
	root, child := got.Spans[0], got.Spans[1]
	if root.ParentSpanID != "" || root.Kind != "server" || root.Scope == nil || len(root.Events) != 1 || root.Attributes["http.route"] != "/orders" ||
		child.ParentSpanID != root.SpanID || child.Status != "error" || child.StatusMessage != "deadlock" || child.DurationMs != 30 {
		t.Errorf("spans = %+v", got.Spans)
	}
	h.do(call{method: http.MethodGet, path: "/api/v1/projects/" + p.ID + "/traces/" + strings.Repeat("ab", 16)}).expect(t, http.StatusNotFound, "trace-not-found")
	h.do(call{method: http.MethodGet, path: "/api/v1/projects/" + p.ID + "/traces/nope", invalid: true}).expect(t, http.StatusBadRequest, "validation-failed")

	// Searching finds it by any of its spans.
	traces := "/api/v1/environments/" + envs.Data[2].ID + "/traces"
	var found struct {
		Data []httpapi.TraceSummaryJSON `json:"data"`
	}
	h.do(call{method: http.MethodGet, path: traces + "?errors=true&service=orders-api"}).decode(t, &found)
	if len(found.Data) != 1 || found.Data[0].TraceID != sent.TraceID || found.Data[0].Spans != 2 || found.Data[0].Errors != 1 ||
		found.Data[0].RootName != "POST /orders" || found.Data[0].RootMissing {
		t.Errorf("found = %+v", found.Data)
	}
	q := url.Values{"attribute": {"db.system.name=postgresql"}, "from": {start.Add(-time.Hour).Format(time.RFC3339)}, "to": {time.Now().UTC().Format(time.RFC3339)}}
	h.do(call{method: http.MethodGet, path: traces + "?" + q.Encode()}).decode(t, &found)
	if len(found.Data) != 1 {
		t.Errorf("by attribute = %+v", found.Data)
	}
	h.do(call{method: http.MethodGet, path: traces + "?service=nobody"}).decode(t, &found)
	if len(found.Data) != 0 {
		t.Errorf("nobody's traces = %+v", found.Data)
	}
	for _, bad := range []string{"?from=yesterday", "?minDurationMs=-1", "?errors=maybe", "?attribute=nokey", "?limit=500",
		"?from=2026-10-31T12:00:00Z&to=2026-10-31T11:00:00Z", "?from=2026-01-01T00:00:00Z&to=2026-10-31T00:00:00Z"} {
		h.do(call{method: http.MethodGet, path: traces + bad, invalid: true}).expect(t, http.StatusBadRequest, "validation-failed")
	}

	var services struct {
		Data []httpapi.ServiceStatsJSON `json:"data"`
	}
	h.do(call{method: http.MethodGet, path: "/api/v1/environments/" + envs.Data[2].ID + "/services"}).decode(t, &services)
	if len(services.Data) != 1 || services.Data[0].Service != "orders-api" || services.Data[0].Calls != 1 || services.Data[0].MeanMs != 80 {
		t.Errorf("services = %+v", services.Data)
	}
	h.do(call{method: http.MethodGet, path: "/api/v1/environments/env_01h455vb4pex5vsknk084sn02q/traces"}).expect(t, http.StatusNotFound, "not-found")

	// The minute's graph: orders-api calls a database it names by its
	// system, since the span has no address.
	// Both minutes, in case the database span began just after the first
	// ended.
	for _, m := range []time.Time{start.Truncate(time.Minute), start.Truncate(time.Minute).Add(time.Minute)} {
		if err := telemetry.BuildGraphMinute(context.Background(), ch, m); err != nil {
			t.Fatal(err)
		}
	}
	var graph struct {
		Edges []httpapi.EdgeJSON `json:"edges"`
	}
	h.do(call{method: http.MethodGet, path: "/api/v1/environments/" + envs.Data[2].ID + "/graph"}).decode(t, &graph)
	if len(graph.Edges) != 1 || graph.Edges[0].From != "orders-api" || graph.Edges[0].To != "postgresql" || graph.Edges[0].ToKind != "database" ||
		graph.Edges[0].System != "postgresql" || graph.Edges[0].Errors != 1 || graph.Edges[0].MeanMs != 30 {
		t.Errorf("graph = %+v", graph.Edges)
	}
	var ops struct {
		Data []httpapi.OperationStatsJSON `json:"data"`
	}
	h.do(call{method: http.MethodGet, path: "/api/v1/environments/" + envs.Data[2].ID + "/operations?service=orders-api"}).decode(t, &ops)
	if len(ops.Data) != 2 {
		t.Errorf("operations = %+v", ops.Data)
	}
	for _, o := range ops.Data {
		if o.Operation == "POST /orders" && (o.Kind != "server" || o.Calls != 1) {
			t.Errorf("POST /orders = %+v", o)
		}
	}
	h.do(call{method: http.MethodGet, path: "/api/v1/environments/" + envs.Data[2].ID + "/graph?from=soon", invalid: true}).expect(t, http.StatusBadRequest, "validation-failed")
}
