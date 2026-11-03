// SPDX-License-Identifier: Apache-2.0

package httpapi_test

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/CemTbi/Signalbox/internal/httpapi"
	"github.com/CemTbi/Signalbox/internal/modules/telemetry"
	"github.com/CemTbi/Signalbox/internal/platform/clickhouse"
	"github.com/CemTbi/Signalbox/internal/platform/clickhouse/clickhousetest"
	"github.com/CemTbi/Signalbox/internal/platform/id"
)

func TestDeclaringDependencies(t *testing.T) {
	h := newHarness(t)
	h.setUp()
	p := h.newProject("shop")
	services := "/api/v1/projects/" + p.ID + "/services"
	var a, b httpapi.ServiceJSON
	h.do(call{method: http.MethodPost, path: services, body: map[string]string{"name": "A", "slug": "a"}}).decode(t, &a)
	h.do(call{method: http.MethodPost, path: services, body: map[string]string{"name": "B", "slug": "b"}}).decode(t, &b)

	dependencies := "/api/v1/projects/" + p.ID + "/dependencies"
	var made httpapi.DependencyJSON
	r := h.do(call{method: http.MethodPost, path: dependencies, body: map[string]string{"fromServiceId": a.ID, "toServiceId": b.ID}})
	r.decode(t, &made)
	if made.FromServiceID != a.ID || made.ToServiceID != b.ID || r.header.Get("Location") != "/api/v1/dependencies/"+made.ID {
		t.Errorf("made %+v at %q", made, r.header.Get("Location"))
	}
	h.do(call{method: http.MethodPost, path: dependencies, body: map[string]string{"fromServiceId": a.ID, "toServiceId": b.ID}}).
		expect(t, http.StatusConflict, "dependency-exists")
	h.do(call{method: http.MethodPost, path: dependencies, body: map[string]string{"fromServiceId": a.ID, "toServiceId": a.ID}}).
		expect(t, http.StatusBadRequest, "validation-failed")
	h.do(call{method: http.MethodPost, path: dependencies, invalid: true, body: map[string]string{"fromServiceId": "nope", "toServiceId": b.ID}}).
		expect(t, http.StatusBadRequest, "validation-failed")
	h.do(call{method: http.MethodPost, path: dependencies, body: map[string]string{"fromServiceId": a.ID, "toServiceId": "svc_01j9z3q1s3v5w7y9a1c3e5g7j9"}}).
		expect(t, http.StatusNotFound, "not-found")

	// The other way round is another line.
	h.do(call{method: http.MethodPost, path: dependencies, body: map[string]string{"fromServiceId": b.ID, "toServiceId": a.ID}}).expect(t, http.StatusCreated, "")
	var list struct {
		Data []httpapi.DependencyJSON `json:"data"`
	}
	h.do(call{method: http.MethodGet, path: dependencies}).decode(t, &list)
	if len(list.Data) != 2 || list.Data[0].ID != made.ID {
		t.Errorf("list = %+v", list.Data)
	}

	h.do(call{method: http.MethodDelete, path: "/api/v1/dependencies/" + made.ID}).expect(t, http.StatusNoContent, "")
	h.do(call{method: http.MethodDelete, path: "/api/v1/dependencies/" + made.ID}).expect(t, http.StatusNotFound, "not-found")
	// Deleting a service removes its lines.
	h.do(call{method: http.MethodDelete, path: "/api/v1/services/" + b.ID}).expect(t, http.StatusNoContent, "")
	h.do(call{method: http.MethodGet, path: dependencies}).decode(t, &list)
	if len(list.Data) != 0 {
		t.Errorf("after deleting a service: %+v", list.Data)
	}
}

func TestPlacingDatabasesAndHostsOnTheCanvas(t *testing.T) {
	h := newHarness(t)
	h.setUp()
	p := h.newProject("shop")
	canvas := "/api/v1/projects/" + p.ID + "/canvas"
	put := func(targets ...map[string]any) call {
		return call{method: http.MethodPut, path: canvas, body: map[string]any{"targets": targets}}
	}
	h.do(put(map[string]any{"kind": "database", "name": "shop-db", "x": 10, "y": 20}, map[string]any{"kind": "external", "name": "psp.example", "x": 30, "y": 40})).
		expect(t, http.StatusNoContent, "")
	// Moved again, it stays one node.
	h.do(put(map[string]any{"kind": "database", "name": "shop-db", "x": 11, "y": 21})).expect(t, http.StatusNoContent, "")
	var got httpapi.CanvasLayoutJSON
	h.do(call{method: http.MethodGet, path: canvas}).decode(t, &got)
	if len(got.Targets) != 2 || got.Targets[0].Name != "shop-db" || got.Targets[0].X != 11 || got.Targets[1].Kind != "external" {
		t.Errorf("layout = %+v", got)
	}

	bad := put(map[string]any{"kind": "planet", "name": "x", "x": 1, "y": 1})
	bad.invalid = true
	h.do(bad).expect(t, http.StatusBadRequest, "validation-failed")
	bad = put(map[string]any{"kind": "database", "name": " ", "x": 1, "y": 1})
	h.do(bad).expect(t, http.StatusBadRequest, "validation-failed")
	// PostgreSQL refuses NUL in text, so it's a bad request, not a failure.
	bad = put(map[string]any{"kind": "database", "name": "shop\x00db", "x": 1, "y": 1})
	h.do(bad).expect(t, http.StatusBadRequest, "validation-failed")
	h.do(call{method: http.MethodPost, path: "/api/v1/projects/" + p.ID + "/services", body: map[string]string{"name": "A", "slug": "a", "telemetryServiceName": "a\x00b"}}).
		expect(t, http.StatusBadRequest, "validation-failed")
	// Nothing to move at all.
	bad = call{method: http.MethodPut, path: canvas, body: map[string]any{}}
	h.do(bad).expect(t, http.StatusBadRequest, "validation-failed")
}

func TestCanvasLive(t *testing.T) {
	ch, chURL := clickhousetest.NewWithURL(t)
	ctx := context.Background()
	if err := clickhouse.Migrate(ctx, chURL, nil, slog.New(slog.DiscardHandler)); err != nil {
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
	var orders, quiet httpapi.ServiceJSON
	services := "/api/v1/projects/" + p.ID + "/services"
	h.do(call{method: http.MethodPost, path: services, body: map[string]string{"name": "Orders", "slug": "orders-api"}}).decode(t, &orders)
	h.do(call{method: http.MethodPost, path: services, body: map[string]string{"name": "Quiet", "slug": "quiet"}}).decode(t, &quiet)
	live := "/api/v1/projects/" + p.ID + "/canvas/live?environment=" + envs.Data[2].ID

	// With no spans, the canvas's services show, without traffic.
	var first httpapi.CanvasLiveJSON
	r := h.do(call{method: http.MethodGet, path: live})
	r.decode(t, &first)
	tag := r.header.Get("ETag")
	if len(first.Services) != 2 || first.Services[0].ID != orders.ID || !first.Services[0].OnCanvas || first.Services[0].Rate != 0 || first.ChangesSince != "" || tag == "" {
		t.Fatalf("no spans: %+v, ETag %q", first, tag)
	}
	if got := r.header.Get("Cache-Control"); !strings.Contains(got, "no-store") {
		t.Errorf("Cache-Control = %q", got)
	}
	h.do(call{method: http.MethodGet, path: live, headers: map[string]string{"If-None-Match": tag}}).expect(t, http.StatusNotModified, "")
	// A tag it doesn't know gets everything.
	var full httpapi.CanvasLiveJSON
	h.do(call{method: http.MethodGet, path: live, headers: map[string]string{"If-None-Match": `"unknown"`}}).decode(t, &full)
	if len(full.Services) != 2 || full.ChangesSince != "" {
		t.Errorf("an unknown tag: %+v", full)
	}

	for _, bad := range []string{"", "?environment=nope"} {
		h.do(call{method: http.MethodGet, path: "/api/v1/projects/" + p.ID + "/canvas/live" + bad, invalid: true}).expect(t, http.StatusBadRequest, "validation-failed")
	}
	// Another project's environment isn't this one's.
	other := h.newProject("other")
	h.do(call{method: http.MethodGet, path: "/api/v1/projects/" + other.ID + "/canvas/live?environment=" + envs.Data[2].ID}).expect(t, http.StatusNotFound, "not-found")

	// Calls from a few minutes ago, whose minute the graph has built.
	start := time.Now().Add(-4 * time.Minute).UTC().Truncate(time.Minute)
	trace, err := telemetry.ParseTraceID(strings.Repeat("ab", 16))
	if err != nil {
		t.Fatal(err)
	}
	spans := []telemetry.Span{
		{EnvironmentID: prod, TraceID: trace, SpanID: telemetry.SpanID{1}, Start: start.Add(time.Second), Duration: 80 * time.Millisecond,
			Service: "orders-api", Name: "POST /orders", Kind: telemetry.KindServer},
		{EnvironmentID: prod, TraceID: trace, SpanID: telemetry.SpanID{2}, ParentSpanID: telemetry.SpanID{1}, Start: start.Add(2 * time.Second), Duration: 30 * time.Millisecond,
			Service: "orders-api", Name: "INSERT orders", Kind: telemetry.KindClient, Status: telemetry.StatusError, DBSystem: "postgresql", ServerAddress: "shop-db"},
		{EnvironmentID: prod, TraceID: trace, SpanID: telemetry.SpanID{3}, Start: start.Add(3 * time.Second), Duration: 20 * time.Millisecond,
			Service: "cron", Name: "GET /orders", Kind: telemetry.KindServer},
	}
	if err := telemetry.WriteSpans(ctx, ch, spans); err != nil {
		t.Fatal(err)
	}
	if err := telemetry.BuildGraphMinute(ctx, ch, start); err != nil {
		t.Fatal(err)
	}

	// The answer is cached for 5 seconds, so the spans show after that, as
	// changes to the state the browser has.
	var changed httpapi.CanvasLiveJSON
	for deadline := time.Now().Add(15 * time.Second); ; {
		r = h.do(call{method: http.MethodGet, path: live, headers: map[string]string{"If-None-Match": tag}})
		if r.status == http.StatusOK {
			r.decode(t, &changed)
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the spans never showed: %d", r.status)
		}
		time.Sleep(500 * time.Millisecond)
	}
	if changed.ChangesSince != tag {
		t.Errorf("changesSince = %q, want %q", changed.ChangesSince, tag)
	}
	// Orders changed and Cron is new; Quiet didn't change, so it isn't sent.
	byID := map[string]httpapi.LiveServiceJSON{}
	for _, v := range changed.Services {
		byID[v.ID] = v
	}
	if _, sent := byID[quiet.ID]; sent || len(byID) != 2 {
		t.Errorf("changes = %+v", changed.Services)
	}
	if got := byID[orders.ID]; got.Rate <= 0 || got.P95Ms <= 0 || got.ErrorRate != 0 {
		t.Errorf("orders = %+v", got)
	}
	if got, ok := byID["seen:cron"]; !ok || got.OnCanvas || got.Name != "cron" || got.Rate <= 0 {
		t.Errorf("cron = %+v, %v", got, ok)
	}
	if len(changed.Lines) != 1 || changed.Lines[0].From != orders.ID || changed.Lines[0].To != "database:shop-db" ||
		changed.Lines[0].System != "postgresql" || changed.Lines[0].ErrorRate != 1 || changed.Lines[0].Rate <= 0 {
		t.Errorf("lines = %+v", changed.Lines)
	}
}

func TestPlacingServicesOnTheCanvas(t *testing.T) {
	h := newHarness(t)
	h.setUp()
	p := h.newProject("shop")
	var a, b httpapi.ServiceJSON
	h.do(call{method: http.MethodPost, path: "/api/v1/projects/" + p.ID + "/services", body: map[string]string{"name": "A", "slug": "a"}}).decode(t, &a)
	r := h.do(call{method: http.MethodPost, path: "/api/v1/projects/" + p.ID + "/services", body: map[string]string{"name": "B", "slug": "b"}})
	r.decode(t, &b)
	tagB := r.header.Get("ETag")
	if b.Position.X != 288 || b.Position.Y != 0 {
		t.Errorf("B was placed at %+v, want the next grid spot", b.Position)
	}

	canvas := "/api/v1/projects/" + p.ID + "/canvas"
	h.do(call{method: http.MethodPut, path: canvas, body: map[string]any{"positions": []map[string]any{
		{"serviceId": a.ID, "x": 40, "y": 80}, {"serviceId": b.ID, "x": 400, "y": 80},
	}}}).expect(t, http.StatusNoContent, "")
	r = h.do(call{method: http.MethodGet, path: "/api/v1/services/" + b.ID})
	var moved httpapi.ServiceJSON
	r.decode(t, &moved)
	// The move kept B's ETag, so an edit begun before it still applies.
	if moved.Position.X != 400 || r.header.Get("ETag") != tagB {
		t.Errorf("B = %+v, ETag %q (was %q)", moved.Position, r.header.Get("ETag"), tagB)
	}
	h.do(call{method: http.MethodPatch, path: "/api/v1/services/" + b.ID, contentType: "application/merge-patch+json",
		headers: map[string]string{"If-Match": tagB}, body: map[string]string{"name": "B2"}}).expect(t, http.StatusOK, "")

	h.do(call{method: http.MethodPut, path: canvas, invalid: true, body: map[string]any{"positions": []map[string]any{{"serviceId": "nope", "x": 1, "y": 1}}}}).
		expect(t, http.StatusBadRequest, "validation-failed")
	h.do(call{method: http.MethodPut, path: canvas, body: map[string]any{"positions": []map[string]any{{"serviceId": "svc_01j9z3q1s3v5w7y9a1c3e5g7j9", "x": 1, "y": 1}}}}).
		expect(t, http.StatusNotFound, "not-found")
}
