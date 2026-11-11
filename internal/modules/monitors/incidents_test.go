// SPDX-License-Identifier: Apache-2.0

package monitors

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/CemTbi/Signalbox/internal/modules/workspaces"
	"github.com/CemTbi/Signalbox/internal/platform/id"
)

// check runs one scheduled check of the monitor now.
func (f *fixture) check(t *testing.T, m Monitor) {
	t.Helper()
	f.due(t, m)
	f.tick(t)
}

func (f *fixture) incidents(t *testing.T, q IncidentQuery) []Incident {
	t.Helper()
	page, err := f.mod.ListIncidents(f.ctx, f.owner, f.project, q)
	if err != nil {
		t.Fatal(err)
	}
	return page.Items
}

func (f *fixture) timeline(t *testing.T, incidentID uuid.UUID) []string {
	t.Helper()
	_, events, err := f.mod.GetIncident(f.ctx, f.owner, incidentID)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, e := range events {
		kinds = append(kinds, e.Kind+":"+e.To)
	}
	return kinds
}

func (f *fixture) member(t *testing.T, role workspaces.Role) uuid.UUID {
	t.Helper()
	u := id.New()
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO users (id, email, name) VALUES ($1, $2, 'Member')`, u, u.String()+"@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO workspace_members (workspace_id, user_id, role) VALUES ($1, $2, $3)`, f.workspace(t), u, role); err != nil {
		t.Fatal(err)
	}
	return u
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestAMonitorGoingDownOpensAnIncident(t *testing.T) {
	f := setup(t)
	m := f.monitor(t, "Orders")
	f.check(t, m)
	if len(f.incidents(t, IncidentQuery{})) != 0 {
		t.Fatal("an incident opened for a monitor that's up")
	}
	f.prober.answer = failing
	f.check(t, m) // a failure, to be confirmed
	if len(f.incidents(t, IncidentQuery{})) != 0 {
		t.Fatal("an incident opened before the failure was confirmed")
	}
	f.check(t, m)
	list := f.incidents(t, IncidentQuery{})
	if len(list) != 1 {
		t.Fatalf("incidents = %+v", list)
	}
	in := list[0]
	if in.State != IncidentOpen || in.MonitorID != m.ID || in.MonitorName != "Orders" || in.First.FailureCode != "connect" || in.First.FailureMessage != "refused" {
		t.Fatalf("incident = %+v", in)
	}
	if got := f.get(t, m); got.OpenIncidentID != in.ID {
		t.Errorf("the monitor's open incident = %v", got.OpenIncidentID)
	}
	// Still down: the same incident, and nothing more on its timeline.
	f.check(t, m)
	if list := f.incidents(t, IncidentQuery{}); len(list) != 1 {
		t.Errorf("a second incident: %+v", list)
	}

	// Back up: resolved, as recovered.
	f.prober.answer = ok
	f.check(t, m)
	in, _, err := f.mod.GetIncident(f.ctx, f.owner, in.ID)
	if err != nil {
		t.Fatal(err)
	}
	if in.State != IncidentResolved || in.Resolution != ResolvedRecovered || in.ResolvedAt.IsZero() || in.ResolvedBy.ID != uuid.Nil {
		t.Errorf("after recovering = %+v", in)
	}
	if got := f.timeline(t, in.ID); !equal(got, []string{"opened:down", "resolved:up"}) {
		t.Errorf("timeline = %v", got)
	}
	if got := f.get(t, m); got.OpenIncidentID != uuid.Nil {
		t.Errorf("a resolved incident is still open: %v", got.OpenIncidentID)
	}

	// Down again: a new incident, listed before the resolved one.
	f.prober.answer = failing
	f.check(t, m)
	f.check(t, m)
	list = f.incidents(t, IncidentQuery{})
	if len(list) != 2 || list[0].State != IncidentOpen || list[1].ID != in.ID {
		t.Fatalf("incidents = %+v", list)
	}
	if open := f.incidents(t, IncidentQuery{State: IncidentResolved}); len(open) != 1 || open[0].ID != in.ID {
		t.Errorf("resolved = %+v", open)
	}
	// One at a time, in order.
	page, err := f.mod.ListIncidents(f.ctx, f.owner, f.project, IncidentQuery{Page: workspaces.PageRequest{Limit: 1}})
	if err != nil || len(page.Items) != 1 || !page.More || page.Items[0].ID != list[0].ID {
		t.Fatalf("first page = %+v, %v", page, err)
	}
	page, err = f.mod.ListIncidents(f.ctx, f.owner, f.project, IncidentQuery{Page: workspaces.PageRequest{Limit: 1, After: page.Items[0].ID}})
	if err != nil || len(page.Items) != 1 || page.More || page.Items[0].ID != in.ID {
		t.Fatalf("second page = %+v, %v", page, err)
	}
}

func TestAcknowledgingAndResolvingByHand(t *testing.T) {
	f := setup(t)
	m := f.monitor(t, "Orders")
	f.prober.answer = failing
	f.check(t, m)
	f.check(t, m)
	in := f.incidents(t, IncidentQuery{})[0]

	viewer := f.member(t, workspaces.Viewer)
	if _, err := f.mod.Acknowledge(f.ctx, viewer, in.ID); !errors.Is(err, ErrForbidden) {
		t.Errorf("a viewer acknowledged: %v", err)
	}
	if _, _, err := f.mod.GetIncident(f.ctx, viewer, in.ID); err != nil {
		t.Errorf("a viewer can't read it: %v", err)
	}
	if _, _, err := f.mod.GetIncident(f.ctx, id.New(), in.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("someone outside the workspace: %v", err)
	}
	acked, err := f.mod.Acknowledge(f.ctx, f.owner, in.ID)
	if err != nil || acked.AcknowledgedBy.ID != f.owner || acked.AcknowledgedBy.Name != "Ada" || acked.AcknowledgedAt.IsZero() {
		t.Fatalf("acknowledged = %+v, %v", acked, err)
	}
	if again, err := f.mod.Acknowledge(f.ctx, f.owner, in.ID); err != nil || !again.AcknowledgedAt.Equal(acked.AcknowledgedAt) {
		t.Errorf("acknowledged again = %+v, %v", again, err)
	}

	// Paused and resumed, with who did it.
	paused := true
	if _, err := f.mod.Update(f.ctx, f.owner, m.ID, workspaces.Precondition{}, Patch{Paused: &paused}); err != nil {
		t.Fatal(err)
	}
	paused = false
	if _, err := f.mod.Update(f.ctx, f.owner, m.ID, workspaces.Precondition{}, Patch{Paused: &paused}); err != nil {
		t.Fatal(err)
	}

	// Resolved by hand while still down: no new incident at the next check.
	resolved, err := f.mod.Resolve(f.ctx, f.owner, in.ID)
	if err != nil || resolved.State != IncidentResolved || resolved.Resolution != ResolvedManual || resolved.ResolvedBy.ID != f.owner {
		t.Fatalf("resolved = %+v, %v", resolved, err)
	}
	_, events, err := f.mod.GetIncident(f.ctx, f.owner, in.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.timeline(t, in.ID); !equal(got, []string{"opened:down", "acknowledged:", "paused:paused", "resumed:pending", "resolved:"}) {
		t.Errorf("timeline = %v", got)
	}
	if events[1].Actor.ID != f.owner || events[1].Actor.Name != "Ada" || events[0].Actor.ID != uuid.Nil {
		t.Errorf("actors = %+v, %+v", events[0].Actor, events[1].Actor)
	}
	if _, err := f.mod.Acknowledge(f.ctx, f.owner, in.ID); !errors.Is(err, ErrIncidentResolved) {
		t.Errorf("acknowledging a resolved incident: %v", err)
	}
	if _, err := f.mod.Resolve(f.ctx, f.owner, in.ID); err != nil {
		t.Errorf("resolving it again: %v", err)
	}
	for range 3 {
		f.check(t, m)
	}
	if got := f.get(t, m); got.State != StateDown || got.OpenIncidentID != uuid.Nil {
		t.Fatalf("held off: %s, open %v", got.State, got.OpenIncidentID)
	}
	// Up once, the next time it goes down opens one.
	f.prober.answer = ok
	f.check(t, m)
	f.prober.answer = failing
	f.check(t, m)
	f.check(t, m)
	if got := f.get(t, m); got.OpenIncidentID == uuid.Nil {
		t.Error("no incident after the monitor had been up")
	}
}

func TestADeletedMonitorsIncidentsStay(t *testing.T) {
	f := setup(t)
	m := f.monitor(t, "Orders")
	f.prober.answer = failing
	f.check(t, m)
	f.check(t, m)
	in := f.incidents(t, IncidentQuery{})[0]
	if err := f.mod.Delete(f.ctx, f.owner, m.ID, workspaces.Precondition{}); err != nil {
		t.Fatal(err)
	}
	got, _, err := f.mod.GetIncident(f.ctx, f.owner, in.ID)
	if err != nil || got.State != IncidentResolved || got.Resolution != ResolvedDeleted || got.MonitorID != uuid.Nil || got.MonitorName != "Orders" {
		t.Errorf("after deleting the monitor = %+v, %v", got, err)
	}
	if err := f.mod.Delete(f.ctx, f.owner, f.monitor(t, "Stale").ID, workspaces.Precondition{Versions: []int64{99}}); !errors.Is(err, ErrVersionMismatch) {
		t.Errorf("a stale delete: %v", err)
	}
}

func TestAMissedHeartbeatOpensAnIncident(t *testing.T) {
	f := setup(t)
	m := f.heartbeat(t)
	if _, err := f.pool.Exec(f.ctx, `UPDATE monitors SET next_check_at = now() - interval '1 second' WHERE id = $1`, m.ID); err != nil {
		t.Fatal(err)
	}
	f.tick(t)
	got := f.get(t, m)
	if got.State != StateDown || got.OpenIncidentID == uuid.Nil {
		t.Fatalf("missed: %s, open %v", got.State, got.OpenIncidentID)
	}
	if err := f.mod.Heartbeat(f.ctx, m.HeartbeatToken); err != nil {
		t.Fatal(err)
	}
	in, _, err := f.mod.GetIncident(f.ctx, f.owner, got.OpenIncidentID)
	if err != nil || in.State != IncidentResolved || in.First.FailureCode != FailureNoHeartbeat {
		t.Errorf("after the call = %+v, %v", in, err)
	}
}
