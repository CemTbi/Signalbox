// SPDX-License-Identifier: Apache-2.0

package telemetry_test

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/CemTbi/Signalbox/internal/modules/telemetry"
	"github.com/CemTbi/Signalbox/internal/modules/workspaces"
	"github.com/CemTbi/Signalbox/internal/platform/id"
	"github.com/CemTbi/Signalbox/internal/platform/postgres/postgrestest"
)

func TestTraces(t *testing.T) {
	conn := newStore(t)
	pool := postgrestest.New(t)
	ctx := context.Background()
	ws := workspaces.New(pool, slog.New(slog.DiscardHandler))
	user := func(name string) uuid.UUID {
		u := id.New()
		if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, name) VALUES ($1, $2, $3)`, u, name+"@example.com", name); err != nil {
			t.Fatal(err)
		}
		return u
	}
	owner, stranger := user("ada"), user("eve")
	w, err := ws.CreateWorkspace(ctx, owner, workspaces.WorkspaceInput{Name: "Shop", Slug: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := ws.CreateProject(ctx, owner, w.ID, workspaces.ProjectInput{Name: "Checkout"})
	if err != nil {
		t.Fatal(err)
	}
	envs, err := ws.ListEnvironments(ctx, owner, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	prod := envs[2].ID

	// Two traces: a checkout through three services that failed at
	// payments, and a product list whose root hasn't arrived.
	start := recently()
	prep := telemetry.NewPreparer()
	span := func(trace, sp, parent byte, offset, dur time.Duration, service, name string, kind telemetry.SpanKind, status telemetry.StatusCode, attrs map[string]string) telemetry.Span {
		s := telemetry.Span{EnvironmentID: prod, TraceID: traceID(trace), SpanID: spanID(sp), Start: start.Add(offset), Duration: dur,
			Service: service, Name: name, Kind: kind, Status: status, Attributes: attrs}
		if parent != 0 {
			s.ParentSpanID = spanID(parent)
		}
		prep.Prepare(&s)
		return s
	}
	spans := []telemetry.Span{
		span(1, 1, 0, 0, 300*time.Millisecond, "storefront-web", "POST /api/checkout", telemetry.KindServer, telemetry.StatusError, nil),
		span(1, 2, 1, 10*time.Millisecond, 250*time.Millisecond, "orders-api", "POST /orders", telemetry.KindServer, telemetry.StatusError,
			map[string]string{"http.request.method": "POST", "http.route": "/orders"}),
		span(1, 3, 2, 20*time.Millisecond, 200*time.Millisecond, "payments-api", "POST /payments", telemetry.KindServer, telemetry.StatusError,
			map[string]string{"app.order": "ord_1"}),
		span(2, 4, 9, time.Second, 40*time.Millisecond, "inventory-api", "GET /products", telemetry.KindServer, telemetry.StatusUnset, nil),
	}
	if err := telemetry.WriteSpans(ctx, conn, spans); err != nil {
		t.Fatal(err)
	}
	traces := telemetry.NewTraces(conn, ws)
	q := telemetry.TraceQuery{From: start.Add(-time.Hour), To: start.Add(time.Hour)}

	got, err := traces.Search(ctx, owner, prod, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].TraceID != traceID(2) || got[1].TraceID != traceID(1) {
		t.Fatalf("traces = %+v", got)
	}
	checkout := got[1]
	if checkout.Spans != 3 || checkout.Errors != 3 || checkout.Duration != 300*time.Millisecond || checkout.RootMissing ||
		checkout.RootService != "storefront-web" || checkout.RootName != "POST /api/checkout" ||
		!slices.Equal(checkout.Services, []string{"orders-api", "payments-api", "storefront-web"}) {
		t.Errorf("checkout = %+v", checkout)
	}
	if !got[0].RootMissing || got[0].RootService != "inventory-api" {
		t.Errorf("a trace without its root = %+v", got[0])
	}

	// Filters match traces by any of their spans.
	for name, tc := range map[string]struct {
		q    telemetry.TraceQuery
		want []telemetry.TraceID
	}{
		"service":   {telemetry.TraceQuery{Service: "payments-api"}, []telemetry.TraceID{traceID(1)}},
		"operation": {telemetry.TraceQuery{Operation: "POST /orders"}, []telemetry.TraceID{traceID(1)}},
		"errors":    {telemetry.TraceQuery{OnlyErrors: true}, []telemetry.TraceID{traceID(1)}},
		"duration":  {telemetry.TraceQuery{MinDuration: 100 * time.Millisecond}, []telemetry.TraceID{traceID(1)}},
		"attribute": {telemetry.TraceQuery{Attribute: "app.order=ord_1"}, []telemetry.TraceID{traceID(1)}},
		"none":      {telemetry.TraceQuery{Service: "nobody"}, nil},
	} {
		tc.q.From, tc.q.To = q.From, q.To
		got, err := traces.Search(ctx, owner, prod, tc.q)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var ids []telemetry.TraceID
		for _, s := range got {
			ids = append(ids, s.TraceID)
		}
		if !slices.Equal(ids, tc.want) {
			t.Errorf("%s: %v, want %v", name, ids, tc.want)
		}
	}
	if _, err := traces.Search(ctx, stranger, prod, q); !errors.Is(err, telemetry.ErrNotFound) {
		t.Errorf("a stranger searches: %v", err)
	}

	// A trace is found by its ID among the project's environments.
	env, found, err := traces.FindTrace(ctx, owner, p.ID, traceID(1))
	if err != nil || env != prod || len(found) != 3 || found[1].Operation != "POST /orders" {
		t.Errorf("find = %s, %d spans, %v", env, len(found), err)
	}
	if _, _, err := traces.FindTrace(ctx, owner, p.ID, traceID(7)); !errors.Is(err, telemetry.ErrTraceNotFound) {
		t.Errorf("a trace nobody sent: %v", err)
	}
	if _, _, err := traces.FindTrace(ctx, stranger, p.ID, traceID(1)); !errors.Is(err, telemetry.ErrNotFound) {
		t.Errorf("a stranger finds: %v", err)
	}

	// Services count the requests they handled.
	stats, err := traces.Services(ctx, owner, prod, q.From, q.To)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 4 {
		t.Fatalf("services = %+v", stats)
	}
	for _, s := range stats {
		if s.Calls != 1 || s.P95 <= 0 {
			t.Errorf("%s = %+v", s.Service, s)
		}
		if s.Service == "payments-api" && (s.Errors != 1 || s.Mean != 200*time.Millisecond) {
			t.Errorf("payments = %+v", s)
		}
	}
}
