// SPDX-License-Identifier: Apache-2.0

package telemetry_test

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/CemTbi/Signalbox/internal/modules/telemetry"
	"github.com/CemTbi/Signalbox/internal/platform/clickhouse"
	"github.com/CemTbi/Signalbox/internal/platform/clickhouse/clickhousetest"
)

func newStore(t *testing.T) clickhouse.Conn {
	t.Helper()
	conn, url := clickhousetest.NewWithURL(t)
	if err := clickhouse.Migrate(context.Background(), url, nil, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	return conn
}

// recently is a few hours ago, on the hour. Spans older than the 7-day
// retention are dropped on insert, so tests can't use fixed dates.
func recently() time.Time {
	return time.Now().UTC().Truncate(time.Hour).Add(-3 * time.Hour)
}

// traceID and spanID return IDs whose bytes all equal b.
func traceID(b byte) (id telemetry.TraceID) {
	for i := range id {
		id[i] = b
	}
	return id
}

func spanID(b byte) (id telemetry.SpanID) {
	for i := range id {
		id[i] = b
	}
	return id
}

// checkout is a trace of three spans: a server span, a database call and a
// failed call to a payment provider.
func checkout(env uuid.UUID, start time.Time) []telemetry.Span {
	trace := traceID(0xa1)
	root, db, psp := spanID(0x01), spanID(0x02), spanID(0x03)
	resource := map[string]string{"service.name": "orders-api", "service.version": "2.3.0"}
	return []telemetry.Span{
		{
			EnvironmentID: env, TraceID: trace, SpanID: root, Start: start, Duration: 640 * time.Millisecond,
			Service: "orders-api", Name: "POST /orders", Kind: telemetry.KindServer, Status: telemetry.StatusError,
			StatusMessage: "payment failed", HTTPMethod: "POST", HTTPRoute: "/orders", HTTPStatusCode: 502,
			ScopeName: "otelhttp", ScopeVersion: "0.60.0", ResourceAttributes: resource,
			Attributes: map[string]string{"http.request.method": "POST", "http.route": "/orders", "url.path": "/orders",
				"http.response.status_code": "502", "tenant": "acme"},
			Events: []telemetry.Event{{
				Time: start.Add(630 * time.Millisecond), Name: "exception",
				Attributes: map[string]string{"exception.type": "PaymentError", "exception.message": "card declined"},
			}},
		},
		{
			EnvironmentID: env, TraceID: trace, SpanID: db, ParentSpanID: root, Start: start.Add(5 * time.Millisecond),
			Duration: 4 * time.Millisecond, Service: "orders-api", Name: "INSERT orders", Kind: telemetry.KindClient,
			ServerAddress: "orders-db", DBSystem: "postgresql", ResourceAttributes: resource,
			Attributes: map[string]string{"db.system.name": "postgresql", "db.query.text": "INSERT INTO orders VALUES ($1)",
				"server.address": "orders-db"},
		},
		{
			EnvironmentID: env, TraceID: trace, SpanID: psp, ParentSpanID: root, Start: start.Add(20 * time.Millisecond),
			Duration: 600 * time.Millisecond, Service: "orders-api", Name: "POST", Kind: telemetry.KindClient,
			Status: telemetry.StatusError, HTTPMethod: "POST", HTTPStatusCode: 402, ServerAddress: "psp.example",
			ResourceAttributes: resource, Attributes: map[string]string{"server.address": "psp.example",
				"http.request.method": "POST", "http.response.status_code": "402"},
			Links: []telemetry.Link{{TraceID: traceID(0xb2), SpanID: spanID(0x09),
				TraceState: "vendor=1", Attributes: map[string]string{"link.reason": "retry"}}},
		},
	}
}

func TestWriteAndReadATrace(t *testing.T) {
	conn := newStore(t)
	ctx := context.Background()
	env := uuid.New()
	start := recently().Add(123456789 * time.Nanosecond)
	want := checkout(env, start)
	if err := telemetry.WriteSpans(ctx, conn, want); err != nil {
		t.Fatal(err)
	}

	got, err := telemetry.Trace(ctx, conn, env, want[0].TraceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("got %d spans, want %d", len(got), len(want))
	}
	for i := range want {
		g, w := got[i], want[i]
		if g.SpanID != w.SpanID || g.ParentSpanID != w.ParentSpanID || !g.Start.Equal(w.Start) ||
			g.Duration != w.Duration || g.Service != w.Service || g.Name != w.Name || g.Kind != w.Kind ||
			g.Status != w.Status || g.StatusMessage != w.StatusMessage || g.HTTPStatusCode != w.HTTPStatusCode ||
			g.ServerAddress != w.ServerAddress || g.DBSystem != w.DBSystem || g.ScopeName != w.ScopeName ||
			len(g.Attributes) != len(w.Attributes) || g.ResourceAttributes["service.version"] != "2.3.0" {
			t.Errorf("span %d:\n got %+v\nwant %+v", i, g, w)
		}
	}
	if ev := got[0].Events; len(ev) != 1 || ev[0].Name != "exception" || ev[0].Attributes["exception.type"] != "PaymentError" {
		t.Errorf("events = %+v", ev)
	}
	if l := got[2].Links; len(l) != 1 || l[0].TraceID != want[2].Links[0].TraceID || l[0].TraceState != "vendor=1" {
		t.Errorf("links = %+v", l)
	}

	// Attributes with a column of their own are stored once, and
	// every map is stored with its keys in order, so equal maps
	// compress as one.
	var stored []string
	if err := conn.QueryRow(ctx, `SELECT mapKeys(span_attributes) FROM spans WHERE span_id = unhex({id:String})`,
		clickhouse.Named("id", want[0].SpanID.String())).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(stored, []string{"tenant", "url.path"}) {
		t.Errorf("stored attribute keys = %q, want [tenant url.path]", stored)
	}

	// Another environment's trace with the same ID stays apart.
	if _, err := telemetry.Trace(ctx, conn, uuid.New(), want[0].TraceID); !errors.Is(err, telemetry.ErrTraceNotFound) {
		t.Errorf("another environment: error = %v, want ErrTraceNotFound", err)
	}
}

func TestSearch(t *testing.T) {
	conn := newStore(t)
	ctx := context.Background()
	env := uuid.New()
	start := recently()
	spans := checkout(env, start)
	// The same trace again an hour later, with new IDs.
	later := checkout(env, start.Add(time.Hour))
	for i := range later {
		later[i].TraceID = traceID(0xc3)
	}
	if err := telemetry.WriteSpans(ctx, conn, append(spans, later...)); err != nil {
		t.Fatal(err)
	}

	window := telemetry.SearchFilter{From: start.Add(-time.Minute), To: start.Add(2 * time.Hour)}
	tests := []struct {
		name   string
		change func(*telemetry.SearchFilter)
		want   []string // span names, newest first
	}{
		{"everything", func(*telemetry.SearchFilter) {}, []string{"POST", "INSERT orders", "POST /orders", "POST", "INSERT orders", "POST /orders"}},
		{"limit", func(f *telemetry.SearchFilter) { f.Limit = 2 }, []string{"POST", "INSERT orders"}},
		{"time range", func(f *telemetry.SearchFilter) { f.To = start.Add(time.Minute) }, []string{"POST", "INSERT orders", "POST /orders"}},
		{"errors", func(f *telemetry.SearchFilter) { f.OnlyErrors = true; f.Limit = 2 }, []string{"POST", "POST /orders"}},
		{"duration", func(f *telemetry.SearchFilter) { f.MinDuration = 620 * time.Millisecond }, []string{"POST /orders", "POST /orders"}},
		{"name", func(f *telemetry.SearchFilter) { f.Name = "INSERT orders" }, []string{"INSERT orders", "INSERT orders"}},
		{"route", func(f *telemetry.SearchFilter) { f.HTTPRoute = "/orders"; f.Limit = 1 }, []string{"POST /orders"}},
		{"status code", func(f *telemetry.SearchFilter) { f.HTTPStatusCode = 402; f.Limit = 1 }, []string{"POST"}},
		{"attribute", func(f *telemetry.SearchFilter) { f.Attribute = "tenant=acme" }, []string{"POST /orders", "POST /orders"}},
		{"missing attribute", func(f *telemetry.SearchFilter) { f.Attribute = "tenant=globex" }, nil},
		{"attribute with a column", func(f *telemetry.SearchFilter) { f.Attribute = "server.address=orders-db" }, []string{"INSERT orders", "INSERT orders"}},
		{"status code attribute", func(f *telemetry.SearchFilter) { f.Attribute = "http.response.status_code=402" }, []string{"POST", "POST"}},
		{"service", func(f *telemetry.SearchFilter) { f.Service = "payments-api" }, nil},
	}
	for _, tt := range tests {
		f := window
		tt.change(&f)
		got, err := telemetry.Search(ctx, conn, env, f)
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		var names []string
		for _, s := range got {
			names = append(names, s.Name)
		}
		if len(names) != len(tt.want) {
			t.Errorf("%s: got %q, want %q", tt.name, names, tt.want)
			continue
		}
		for i := range names {
			if names[i] != tt.want[i] {
				t.Errorf("%s: got %q, want %q", tt.name, names, tt.want)
				break
			}
		}
	}

	if _, err := telemetry.Search(ctx, conn, env, telemetry.SearchFilter{}); err == nil {
		t.Error("a search without a time range was accepted")
	}
	if _, err := telemetry.Search(ctx, conn, env, telemetry.SearchFilter{From: window.From, To: window.To, Attribute: "no-equals"}); err == nil {
		t.Error("an attribute filter without = was accepted")
	}
}

func TestOperations(t *testing.T) {
	conn := newStore(t)
	ctx := context.Background()
	env := uuid.New()
	start := recently()
	// 100 calls taking 1 ms to 100 ms, and every tenth one fails.
	var spans []telemetry.Span
	for i := 1; i <= 100; i++ {
		s := telemetry.Span{
			EnvironmentID: env, TraceID: traceID(byte(i)), SpanID: spanID(byte(i)),
			Start: start.Add(time.Duration(i) * time.Second), Duration: time.Duration(i) * time.Millisecond,
			Service: "orders-api", Name: "GET /orders/{id}", Kind: telemetry.KindServer,
		}
		if i%10 == 0 {
			s.Status = telemetry.StatusError
		}
		spans = append(spans, s)
	}
	if err := telemetry.WriteSpans(ctx, conn, spans); err != nil {
		t.Fatal(err)
	}

	ops, err := telemetry.Operations(ctx, conn, env, "orders-api", start, start.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 {
		t.Fatalf("got %d operations, want 1: %+v", len(ops), ops)
	}
	o := ops[0]
	if o.Calls != 100 || o.Errors != 10 || o.Mean != 50500*time.Microsecond || o.Kind != telemetry.KindServer {
		t.Errorf("stats = %+v", o)
	}
	// The histogram's buckets are 19% wide, and a percentile is its
	// bucket's upper edge.
	for _, p := range []struct {
		name      string
		got, want time.Duration
	}{{"p50", o.P50, 50 * time.Millisecond}, {"p95", o.P95, 95 * time.Millisecond}, {"p99", o.P99, 99 * time.Millisecond}} {
		if p.got < p.want || float64(p.got) > 1.19*float64(p.want) {
			t.Errorf("%s = %s, want %s to %s", p.name, p.got, p.want, time.Duration(1.19*float64(p.want)))
		}
	}
}

func TestRetention(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.DiscardHandler)
	// Before the migrations, it says what to do.
	if err := telemetry.ApplyRetention(ctx, clickhousetest.New(t), 7, log); err == nil || !strings.Contains(err.Error(), "migrate") {
		t.Errorf("without tables: %v", err)
	}
	conn := newStore(t)
	for _, days := range []int{0, -1, telemetry.MaxRetentionDays + 1} {
		if err := telemetry.ApplyRetention(ctx, conn, days, log); err == nil {
			t.Errorf("ApplyRetention accepted %d days", days)
		}
	}
	ttl := func(table string) string {
		t.Helper()
		var engine string
		if err := conn.QueryRow(ctx, `SELECT engine_full FROM system.tables WHERE database = currentDatabase() AND name = {name:String}`,
			clickhouse.Named("name", table)).Scan(&engine); err != nil {
			t.Fatal(err)
		}
		return engine
	}
	if got := ttl("spans"); !strings.Contains(got, "toIntervalDay(7)") && !strings.Contains(got, "INTERVAL 7 DAY") {
		t.Fatalf("a new schema keeps 7 days: %s", got)
	}
	// Applying it twice is harmless, and the second time changes nothing.
	for range 2 {
		if err := telemetry.ApplyRetention(ctx, conn, 30, log); err != nil {
			t.Fatal(err)
		}
	}
	for _, table := range []string{"spans", "trace_ids", "span_metrics_1m"} {
		if got := ttl(table); !strings.Contains(got, "toIntervalDay(30)") && !strings.Contains(got, "INTERVAL 30 DAY") {
			t.Errorf("%s: %s", table, got)
		}
	}
}
