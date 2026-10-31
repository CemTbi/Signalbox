// SPDX-License-Identifier: Apache-2.0

package telemetry_test

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/CemTbi/Signalbox/internal/modules/telemetry"
	"github.com/CemTbi/Signalbox/internal/modules/workspaces"
	"github.com/CemTbi/Signalbox/internal/platform/id"
	"github.com/CemTbi/Signalbox/internal/platform/metrics"
	"github.com/CemTbi/Signalbox/internal/platform/postgres/postgrestest"
)

func TestServiceGraph(t *testing.T) {
	conn := newStore(t)
	pool := postgrestest.New(t)
	ctx := context.Background()
	ws := workspaces.New(pool, slog.New(slog.DiscardHandler))
	owner := id.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, name) VALUES ($1, 'ada@example.com', 'Ada')`, owner); err != nil {
		t.Fatal(err)
	}
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
	env := envs[2].ID

	// One checkout, in a minute an hour or more ago.
	minute := recently()
	next := byte(0)
	span := func(trace byte, parent telemetry.SpanID, offset time.Duration, service, name string, kind telemetry.SpanKind) telemetry.Span {
		next++
		return telemetry.Span{EnvironmentID: env, TraceID: traceID(trace), SpanID: spanID(next), ParentSpanID: parent,
			Start: minute.Add(offset), Duration: 10 * time.Millisecond, Service: service, Name: name, Kind: kind}
	}
	root := span(1, telemetry.SpanID{}, time.Second, "web", "POST /checkout", telemetry.KindServer)
	toOrders := span(1, root.SpanID, 2*time.Second, "web", "HTTP POST", telemetry.KindClient)
	toOrders.Status = telemetry.StatusError
	orders := span(1, toOrders.SpanID, 3*time.Second, "orders", "POST /orders", telemetry.KindServer)
	insert := span(1, orders.SpanID, 4*time.Second, "orders", "INSERT orders", telemetry.KindClient)
	insert.DBSystem, insert.ServerAddress = "postgresql", "shop-db"
	// The payments service's clock runs ten seconds early.
	toPayments := span(1, orders.SpanID, 5*time.Second, "orders", "HTTP POST", telemetry.KindClient)
	payments := span(1, toPayments.SpanID, -5*time.Second, "payments", "POST /payments", telemetry.KindServer)
	toPSP := span(1, payments.SpanID, 6*time.Second, "payments", "HTTP POST", telemetry.KindClient)
	toPSP.ServerAddress = "psp.example"
	// A call within one service, and one whose target can't be named.
	toSelf := span(1, root.SpanID, 7*time.Second, "web", "HTTP GET", telemetry.KindClient)
	self := span(1, toSelf.SpanID, 8*time.Second, "web", "GET /cache", telemetry.KindServer)
	nowhere := span(1, root.SpanID, 9*time.Second, "web", "mystery", telemetry.KindClient)
	// A call whose handler's span never arrived goes to the service its
	// address names, since that service sent spans.
	lost := span(1, root.SpanID, 10*time.Second, "web", "HTTP POST", telemetry.KindClient)
	lost.ServerAddress = "orders"
	// A call in the next minute belongs to that minute.
	later := span(2, telemetry.SpanID{}, 70*time.Second, "web", "HTTP POST", telemetry.KindClient)
	later.ServerAddress = "psp.example"
	if err := telemetry.WriteSpans(ctx, conn, []telemetry.Span{root, toOrders, orders, insert, toPayments, payments, toPSP, toSelf, self, nowhere, lost, later}); err != nil {
		t.Fatal(err)
	}

	// Two processes run the job at once; the minute is built once.
	reg := metrics.NewRegistry()
	jobs := []*telemetry.GraphJob{telemetry.NewGraphJob(pool, conn, slog.New(slog.DiscardHandler), reg), telemetry.NewGraphJob(pool, conn, slog.New(slog.DiscardHandler), nil)}
	at := minute.Add(time.Minute + 91*time.Second) // the minute is due, the next isn't
	for _, j := range jobs {
		telemetry.SetGraphNow(j, func() time.Time { return at })
	}
	var wg sync.WaitGroup
	built := make([]int, len(jobs))
	for i, j := range jobs {
		wg.Go(func() {
			n, err := j.Tick(ctx)
			if err != nil {
				t.Error(err)
			}
			built[i] = n
		})
	}
	wg.Wait()
	if built[0]+built[1] != 1 {
		t.Errorf("built %v minutes, want 1 in all", built)
	}

	edges, err := telemetry.NewTraces(conn, ws).Graph(ctx, owner, env, minute.Add(-time.Hour), minute.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	type line struct {
		from, to string
		kind     telemetry.TargetKind
		system   string
		calls    uint64
		errors   uint64
	}
	got := map[line]bool{}
	for _, e := range edges {
		got[line{e.From, e.Target, e.TargetKind, e.System, e.Calls, e.Errors}] = true
		if e.P95 <= 0 || e.Mean != 10*time.Millisecond {
			t.Errorf("%s → %s latency = %+v", e.From, e.Target, e)
		}
	}
	for _, want := range []line{
		{"web", "orders", telemetry.TargetService, "", 2, 1},
		{"orders", "payments", telemetry.TargetService, "", 1, 0},
		{"orders", "shop-db", telemetry.TargetDatabase, "postgresql", 1, 0},
		{"payments", "psp.example", telemetry.TargetExternal, "", 1, 0},
	} {
		if !got[want] {
			t.Errorf("no line %+v in %+v", want, edges)
		}
	}
	if len(edges) != 4 {
		t.Errorf("%d lines, want 4: %+v", len(edges), edges)
	}

	// The next minute is built once it's due, and the job doesn't repeat a
	// minute.
	at = at.Add(time.Minute)
	if n, err := jobs[1].Tick(ctx); err != nil || n != 1 {
		t.Errorf("next minute: built %d, %v", n, err)
	}
	if n, err := jobs[0].Tick(ctx); err != nil || n != 0 {
		t.Errorf("again: built %d, %v", n, err)
	}
	edges, err = telemetry.NewTraces(conn, ws).Graph(ctx, owner, env, minute.Add(-time.Hour), minute.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range edges {
		if e.From == "web" && e.Target == "psp.example" && e.Calls != 1 {
			t.Errorf("the later call = %+v", e)
		}
	}
	if _, err := telemetry.NewTraces(conn, ws).Graph(ctx, uuid.New(), env, minute, minute.Add(time.Minute)); err == nil {
		t.Error("a stranger read the graph")
	}
}
