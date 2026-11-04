// SPDX-License-Identifier: Apache-2.0

package canvas

import (
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/CemTbi/Signalbox/internal/modules/apis"
	"github.com/CemTbi/Signalbox/internal/modules/monitors"
	"github.com/CemTbi/Signalbox/internal/modules/telemetry"
	"github.com/CemTbi/Signalbox/internal/platform/id"
)

func svc(n byte, slug, told string) apis.Service {
	return apis.Service{ID: uuid.UUID{15: n}, Slug: slug, TelemetryServiceName: told}
}

func nodeID(s apis.Service) string { return id.Format(id.Service, s.ID) }

func TestAssembleMatchesSpansToServices(t *testing.T) {
	orders := svc(1, "orders-api", "")
	pay := svc(2, "payments", "payments-api")
	quiet := svc(3, "quiet", "")
	s := assemble([]apis.Service{orders, pay, quiet}, nil, []telemetry.RecentService{
		{Service: "orders-api", Calls: 300, Errors: 3, P95: 128 * time.Millisecond},
		{Service: "payments-api", Calls: 150, P95: 40 * time.Millisecond},
		{Service: "cron", Calls: 0},
	}, 5*time.Minute, nil, 5*time.Minute)

	byID := map[string]Service{}
	for _, v := range s.services {
		byID[v.ID] = v
	}
	if got := byID[nodeID(orders)]; got.Rate != 1 || got.ErrorRate != 0.01 || got.P95Ms != 128 || !got.OnCanvas {
		t.Errorf("by slug: %+v", got)
	}
	if got := byID[nodeID(pay)]; got.Rate != 0.5 || got.Name != "payments-api" {
		t.Errorf("by telemetry name: %+v", got)
	}
	if got := byID[nodeID(quiet)]; got.Rate != 0 || got.P95Ms != 0 || !got.OnCanvas {
		t.Errorf("no traffic: %+v", got)
	}
	// Sends only client spans, so it handled nothing, but it's still seen.
	if got, ok := byID["seen:cron"]; !ok || got.OnCanvas || got.Rate != 0 {
		t.Errorf("seen: %+v, %v", got, ok)
	}
}

func TestAssembleGivesAContestedNameToOneService(t *testing.T) {
	bySlug := svc(1, "orders", "")
	told := svc(2, "other", "orders")
	alsoTold := svc(3, "third", "orders")
	s := assemble([]apis.Service{bySlug, told, alsoTold}, nil, []telemetry.RecentService{{Service: "orders", Calls: 300}}, 5*time.Minute, nil, 5*time.Minute)
	for _, v := range s.services {
		want := 0.0
		if v.ID == nodeID(told) {
			want = 1
		}
		if v.Rate != want {
			t.Errorf("%s has a rate of %v, want %v", v.ID, v.Rate, want)
		}
	}
}

func TestAssembleResolvesLines(t *testing.T) {
	orders := svc(1, "orders-api", "")
	pay := svc(2, "payments-api", "")
	s := assemble([]apis.Service{orders, pay}, nil, nil, 5*time.Minute, []telemetry.RecentEdge{
		{From: "orders-api", Target: "payments-api", TargetKind: telemetry.TargetService, Calls: 200, Errors: 2},
		// Before payments sent spans, calls to it were calls to an address.
		{From: "orders-api", Target: "payments-api", TargetKind: telemetry.TargetExternal, Calls: 100, Errors: 1},
		{From: "orders-api", Target: "shop-db", TargetKind: telemetry.TargetDatabase, System: "postgresql", Calls: 600},
		{From: "orders-api", Target: "cron", TargetKind: telemetry.TargetService, Calls: 3},
		{From: "cron", Target: "psp.example", TargetKind: telemetry.TargetExternal},
		// A service's name for itself isn't a line.
		{From: "orders-api", Target: "orders-api", TargetKind: telemetry.TargetExternal, Calls: 5},
	}, 5*time.Minute)

	byID := map[string]Line{}
	for _, l := range s.lines {
		byID[l.ID] = l
	}
	if len(byID) != 4 {
		t.Fatalf("lines = %+v", s.lines)
	}
	if got := byID[nodeID(orders)+">"+nodeID(pay)]; got.Rate != 1 || got.ErrorRate != 0.01 || got.ToKind != "service" {
		t.Errorf("merged: %+v", got)
	}
	if got := byID[nodeID(orders)+">database:shop-db"]; got.Rate != 2 || got.System != "postgresql" || got.To != "database:shop-db" {
		t.Errorf("database: %+v", got)
	}
	if got := byID[nodeID(orders)+">seen:cron"]; got.ToKind != "service" || got.Rate != 0.01 {
		t.Errorf("to a service not on the canvas: %+v", got)
	}
	if got := byID["seen:cron>external:psp.example"]; got.From != "seen:cron" || got.Rate != 0 {
		t.Errorf("from a service not on the canvas: %+v", got)
	}
}

func TestTagsFollowContent(t *testing.T) {
	orders := svc(1, "orders-api", "")
	traffic := []telemetry.RecentService{{Service: "orders-api", Calls: 300}}
	a := assemble([]apis.Service{orders}, nil, traffic, 5*time.Minute, nil, 5*time.Minute)
	if b := assemble([]apis.Service{orders}, nil, traffic, 5*time.Minute, nil, 5*time.Minute); a.tag != b.tag {
		t.Error("one state got two tags")
	}
	// Within the rounding, it's the same state.
	if b := assemble([]apis.Service{orders}, nil, []telemetry.RecentService{{Service: "orders-api", Calls: 300}}, 5*time.Minute+time.Millisecond, nil, 5*time.Minute); a.tag != b.tag {
		t.Error("noise changed the tag")
	}
	if b := assemble([]apis.Service{orders}, nil, []telemetry.RecentService{{Service: "orders-api", Calls: 330}}, 5*time.Minute, nil, 5*time.Minute); a.tag == b.tag {
		t.Error("a new rate kept the tag")
	}
}

func TestAssembleShowsHealth(t *testing.T) {
	orders := svc(1, "orders-api", "")
	s := assemble([]apis.Service{orders}, map[uuid.UUID]monitors.ServiceHealth{orders.ID: {State: monitors.StateDegraded, Detail: "Orders degraded: db warn"}}, nil, 5*time.Minute, nil, 5*time.Minute)
	if got := s.services[0]; got.Health != monitors.StateDegraded || got.HealthDetail != "Orders degraded: db warn" {
		t.Errorf("health = %+v", got)
	}
	// A change of health is a change of state.
	if b := assemble([]apis.Service{orders}, nil, nil, 5*time.Minute, nil, 5*time.Minute); b.tag == s.tag {
		t.Error("new health kept the tag")
	}
}

func TestChanges(t *testing.T) {
	a, b, c := Service{ID: "a", Rate: 1}, Service{ID: "b", Rate: 2}, Service{ID: "c", Rate: 3}
	l1, l2 := Line{ID: "1", Rate: 1}, Line{ID: "2", Rate: 2}
	old := state{tag: "old", services: []Service{a, b}, lines: []Line{l1, l2}}
	next := state{tag: "new", services: []Service{a, {ID: "b", Rate: 5}, c}, lines: []Line{l1}}
	got := changes(old, next)
	if got.Tag != "new" || got.Since != "old" {
		t.Errorf("tags: %+v", got)
	}
	if want := []Service{{ID: "b", Rate: 5}, c}; !reflect.DeepEqual(got.Services, want) {
		t.Errorf("services = %+v", got.Services)
	}
	if len(got.Lines) != 0 || !reflect.DeepEqual(got.RemovedLines, []string{"2"}) || len(got.RemovedServices) != 0 {
		t.Errorf("lines = %+v, removed %v and %v", got.Lines, got.RemovedLines, got.RemovedServices)
	}
}

func TestRounded(t *testing.T) {
	for in, want := range map[float64]float64{0: 0, 1.2345: 1.23, 1234: 1230, 0.012345: 0.0123, 99.96: 100} {
		if got := rounded(in); got != want {
			t.Errorf("rounded(%v) = %v, want %v", in, got, want)
		}
	}
}
