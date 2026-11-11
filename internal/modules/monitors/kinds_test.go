// SPDX-License-Identifier: Apache-2.0

package monitors

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/CemTbi/Signalbox/internal/modules/client"
	"github.com/CemTbi/Signalbox/internal/modules/runner"
	"github.com/CemTbi/Signalbox/internal/modules/testruns"
	"github.com/CemTbi/Signalbox/internal/modules/workspaces"
	"github.com/CemTbi/Signalbox/internal/platform/id"
	"github.com/CemTbi/Signalbox/internal/platform/validate"
)

// fakeRuns stands in for the test runs module: it answers as told, and
// keeps what it was asked.
type fakeRuns struct {
	mu     sync.Mutex
	asked  []testruns.MonitorRun
	answer func(testruns.MonitorRun) (testruns.MonitorResult, error)
}

func (r *fakeRuns) RunForMonitor(_ context.Context, mr testruns.MonitorRun) (testruns.MonitorResult, error) {
	r.mu.Lock()
	r.asked = append(r.asked, mr)
	answer := r.answer
	r.mu.Unlock()
	if answer == nil {
		return passedRun(mr)
	}
	return answer(mr)
}

func (r *fakeRuns) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.asked)
}

func run(status string) testruns.Run {
	end := time.Now()
	return testruns.Run{ID: id.New(), Status: status, StartedAt: end.Add(-120 * time.Millisecond), FinishedAt: end}
}

func passedRun(testruns.MonitorRun) (testruns.MonitorResult, error) {
	return testruns.MonitorResult{Run: run(testruns.StatusPassed)}, nil
}

func failedTests(testruns.MonitorRun) (testruns.MonitorResult, error) {
	r := run(testruns.StatusFailed)
	r.Totals = testruns.Totals{Requests: 2, Tests: 4, FailedTests: 1}
	step := &testruns.Step{
		Name: "Create order", Path: "Orders", Failed: true, TraceID: "4bf92f3577b34da6a3ce929d0e0e4736",
		Response: &testruns.Response{Status: 500},
		Scripts: &client.ScriptOutcome{Tests: []client.ScriptTest{
			{Name: "is JSON", Passed: true}, {Name: "is 201", Error: "expected 500 to equal 201"},
		}},
	}
	return testruns.MonitorResult{Run: r, FirstFailed: step}, nil
}

// collection makes a collection of the fixture's project, with a request
// and a folder holding another.
func (f *fixture) collection(t *testing.T, project uuid.UUID) (collectionID, folderID uuid.UUID) {
	t.Helper()
	col, err := f.client.CreateCollection(f.ctx, f.owner, project, "Smoke", "")
	if err != nil {
		t.Fatal(err)
	}
	req := &client.SavedRequest{Method: "GET", URL: "https://example.com/orders"}
	if _, err := f.client.CreateItem(f.ctx, f.owner, col.ID, client.ItemInput{Kind: client.KindRequest, Name: "List orders", Request: req}); err != nil {
		t.Fatal(err)
	}
	folder, err := f.client.CreateItem(f.ctx, f.owner, col.ID, client.ItemInput{Kind: client.KindFolder, Name: "Orders"})
	if err != nil {
		t.Fatal(err)
	}
	return col.ID, folder.ID
}

func fieldErrors(t *testing.T, err error) string {
	t.Helper()
	var fields validate.Errors
	if !errors.As(err, &fields) {
		t.Fatalf("err = %v, want field errors", err)
	}
	var out []string
	for _, e := range fields {
		out = append(out, e.Pointer)
	}
	return strings.Join(out, " ")
}

func TestCollectionMonitor(t *testing.T) {
	f := setup(t)
	collectionID, folderID := f.collection(t, f.project)
	m, err := f.mod.Create(f.ctx, f.owner, f.project, Input{Name: "Smoke", Kind: KindCollection, EnvironmentID: f.env, CollectionID: collectionID})
	if err != nil {
		t.Fatal(err)
	}
	if m.Timeout != time.Minute || m.FailuresToConfirm != 2 || m.CertificateWarningDays != 0 || m.CollectionID != collectionID || m.FolderID != uuid.Nil {
		t.Fatalf("defaults = %+v", m)
	}

	f.due(t, m)
	f.tick(t)
	if f.runs.count() != 1 {
		t.Fatalf("ran %d times", f.runs.count())
	}
	asked := f.runs.asked[0]
	if asked.MonitorID != m.ID || asked.CollectionID != collectionID || asked.Environment.ID != f.env || asked.Limit != time.Minute {
		t.Errorf("asked %+v", asked)
	}
	got := f.get(t, m)
	if got.State != StateUp || got.Last == nil || got.Last.RunID == uuid.Nil || got.Last.Duration != 120*time.Millisecond {
		t.Fatalf("after a passing run: %+v, last %+v", got, got.Last)
	}
	if f.prober.calls.Load() != 0 {
		t.Error("a collection monitor sent a request of its own")
	}

	// Its tests failing is the service failing, confirmed as any failure.
	f.runs.answer = failedTests
	for range 2 {
		f.due(t, m)
		f.tick(t)
	}
	got = f.get(t, m)
	if got.State != StateDown || got.Last.FailureCode != "tests" || got.Last.Status != 500 || got.Last.TraceID == "" {
		t.Fatalf("after failing runs: %s, last %+v", got.State, got.Last)
	}
	if want := `1 of 4 tests failed, first "is 201" in Orders / Create order: expected 500 to equal 201.`; got.Last.FailureMessage != want {
		t.Errorf("message = %q", got.Last.FailureMessage)
	}
	if err := f.mod.Flush(f.ctx); err != nil {
		t.Fatal(err)
	}
	checks, err := f.mod.Checks(f.ctx, f.owner, m.ID, CheckQuery{From: time.Now().Add(-time.Hour), To: time.Now().Add(time.Minute)})
	if err != nil || len(checks) != 3 || checks[0].RunID == uuid.Nil {
		t.Fatalf("checks = %+v, %v", checks, err)
	}

	// An empty collection says nothing about the service.
	f.runs.answer = func(testruns.MonitorRun) (testruns.MonitorResult, error) {
		return testruns.MonitorResult{}, testruns.ErrNothingToRun
	}
	f.due(t, m)
	f.tick(t)
	if got := f.get(t, m); got.State != StateDown || got.Last.Outcome != OutcomeSkipped || got.Last.FailureCode != "nothing-to-run" {
		t.Errorf("after nothing to run: %s, last %+v", got.State, got.Last)
	}

	// Running one folder; a new collection forgets the folder.
	folder := folderID
	m, err = f.mod.Update(f.ctx, f.owner, m.ID, workspaces.Precondition{}, Patch{FolderID: &folder})
	if err != nil || m.FolderID != folderID {
		t.Fatalf("folder = %v, %v", m.FolderID, err)
	}
	other, _ := f.collection(t, f.project)
	m, err = f.mod.Update(f.ctx, f.owner, m.ID, workspaces.Precondition{}, Patch{CollectionID: &other})
	if err != nil || m.CollectionID != other || m.FolderID != uuid.Nil {
		t.Fatalf("collection = %v, folder %v, %v", m.CollectionID, m.FolderID, err)
	}
	// A folder of another collection isn't there.
	if _, err := f.mod.Update(f.ctx, f.owner, m.ID, workspaces.Precondition{}, Patch{FolderID: &folder}); !errors.Is(err, ErrNotFound) {
		t.Errorf("another collection's folder: %v", err)
	}

	// Deleting the collection deletes the monitor.
	if err := f.client.DeleteCollection(f.ctx, f.owner, other, workspaces.Precondition{}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.mod.Get(f.ctx, f.owner, m.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("monitor of a deleted collection: %v", err)
	}
}

func TestCollectionMonitorsAreChecked(t *testing.T) {
	f := setup(t)
	collectionID, _ := f.collection(t, f.project)
	cases := []struct {
		name string
		in   Input
		want string
	}{
		{"no collection", Input{}, "/collectionId"},
		{"a request", Input{CollectionID: collectionID, Request: client.SavedRequest{URL: "https://example.com"}}, "/request"},
		{"assertions", Input{CollectionID: collectionID, Assertions: []Assertion{{Kind: AssertStatus, Op: OpEquals, Value: "200"}}}, "/assertions"},
		{"a long run", Input{CollectionID: collectionID, Timeout: 3 * time.Minute}, "/timeoutSeconds"},
		{"grace", Input{CollectionID: collectionID, Grace: new(time.Minute)}, "/graceSeconds"},
	}
	for _, c := range cases {
		c.in.Name, c.in.Kind, c.in.EnvironmentID = "Smoke", KindCollection, f.env
		_, err := f.mod.Create(f.ctx, f.owner, f.project, c.in)
		if got := fieldErrors(t, err); got != c.want {
			t.Errorf("%s: errors at %s, want %s", c.name, got, c.want)
		}
	}
	// Another project's collection isn't there.
	other, err := f.ws.CreateProject(f.ctx, f.owner, f.workspace(t), workspaces.ProjectInput{Name: "Billing"})
	if err != nil {
		t.Fatal(err)
	}
	theirs, _ := f.collection(t, other.ID)
	if _, err := f.mod.Create(f.ctx, f.owner, f.project, Input{Name: "Smoke", Kind: KindCollection, EnvironmentID: f.env, CollectionID: theirs}); !errors.Is(err, ErrNotFound) {
		t.Errorf("another project's collection: %v", err)
	}
	// An HTTP monitor can't become a collection monitor.
	m := f.monitor(t, "Orders")
	kind := KindCollection
	if _, err := f.mod.Update(f.ctx, f.owner, m.ID, workspaces.Precondition{}, Patch{Kind: &kind}); fieldErrors(t, err) != "/kind" {
		t.Errorf("changing kind: %v", err)
	}
}

func (f *fixture) workspace(t *testing.T) uuid.UUID {
	t.Helper()
	p, err := f.ws.GetProject(f.ctx, f.owner, f.project)
	if err != nil {
		t.Fatal(err)
	}
	return p.WorkspaceID
}

func (f *fixture) heartbeat(t *testing.T) Monitor {
	t.Helper()
	m, err := f.mod.Create(f.ctx, f.owner, f.project, Input{Name: "Nightly backup", Kind: KindHeartbeat, EnvironmentID: f.env})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func near(a, b time.Time) bool { return a.Sub(b).Abs() < 5*time.Second }

func TestHeartbeatMonitor(t *testing.T) {
	f := setup(t)
	m := f.heartbeat(t)
	if m.Interval != 24*time.Hour || m.Grace != time.Hour || m.Timeout != 0 || m.FailuresToConfirm != 1 || m.State != StatePending {
		t.Fatalf("defaults = %+v", m)
	}
	if !strings.HasPrefix(m.HeartbeatToken, HeartbeatTokenPrefix) || !near(m.NextCheckAt, time.Now().Add(25*time.Hour)) {
		t.Fatalf("token %q, next %v", m.HeartbeatToken, m.NextCheckAt)
	}
	if got := f.get(t, m); got.HeartbeatToken != "" {
		t.Error("the token can be read back")
	}

	if err := f.mod.Heartbeat(f.ctx, m.HeartbeatToken); err != nil {
		t.Fatal(err)
	}
	got := f.get(t, m)
	if got.State != StateUp || !near(got.LastPingAt, time.Now()) || !near(got.NextCheckAt, time.Now().Add(25*time.Hour)) || got.Last.Outcome != OutcomePassed {
		t.Fatalf("after a call: %+v", got)
	}
	for _, bad := range []string{"", "sbx_hb_short", HeartbeatTokenPrefix + strings.Repeat("a", 43), strings.TrimPrefix(m.HeartbeatToken, HeartbeatTokenPrefix)} {
		if err := f.mod.Heartbeat(f.ctx, bad); !errors.Is(err, ErrNotFound) {
			t.Errorf("token %q: %v", bad, err)
		}
	}

	// No call within the interval and grace: down at once.
	if _, err := f.pool.Exec(f.ctx, `UPDATE monitors SET last_ping_at = now() - interval '26 hours', next_check_at = now() - interval '1 hour' WHERE id = $1`, m.ID); err != nil {
		t.Fatal(err)
	}
	f.tick(t)
	got = f.get(t, m)
	if got.State != StateDown || got.Last.FailureCode != FailureNoHeartbeat || !strings.HasPrefix(got.Last.FailureMessage, "No call came within 1 day 1 hour. The last came at ") {
		t.Fatalf("after a missed call: %s, last %+v", got.State, got.Last)
	}
	if f.prober.calls.Load() != 0 {
		t.Error("a heartbeat sent a request")
	}
	if !got.NextCheckAt.After(time.Now()) {
		t.Errorf("next = %v", got.NextCheckAt)
	}
	// One call brings it back.
	if err := f.mod.Heartbeat(f.ctx, m.HeartbeatToken); err != nil {
		t.Fatal(err)
	}
	if got := f.get(t, m); got.State != StateUp {
		t.Errorf("after a call: %s", got.State)
	}

	if _, err := f.mod.CheckNow(f.ctx, f.owner, m.ID); !errors.Is(err, ErrNotCheckable) {
		t.Errorf("check now: %v", err)
	}
}

func TestACallWhileAHeartbeatExpiresWins(t *testing.T) {
	f := setup(t)
	m := f.heartbeat(t)
	// Claimed as missed, and then a call came before it was recorded.
	scheduled := time.Now().Add(-time.Second)
	if err := f.mod.Heartbeat(f.ctx, m.HeartbeatToken); err != nil {
		t.Fatal(err)
	}
	if err := f.mod.expire(f.ctx, claimed{monitor: m, scheduled: scheduled}); err != nil {
		t.Fatal(err)
	}
	if got := f.get(t, m); got.State != StateUp {
		t.Errorf("a call that came in time was missed: %s, last %+v", got.State, got.Last)
	}
}

func TestHeartbeatsPausedAndResumed(t *testing.T) {
	f := setup(t)
	m := f.heartbeat(t)
	secret := m.HeartbeatToken
	paused := true
	m, err := f.mod.Update(f.ctx, f.owner, m.ID, workspaces.Precondition{}, Patch{Paused: &paused})
	if err != nil {
		t.Fatal(err)
	}
	// A paused heartbeat takes calls, and changes nothing.
	if err := f.mod.Heartbeat(f.ctx, secret); err != nil {
		t.Fatal(err)
	}
	if got := f.get(t, m); got.State != StatePaused || !got.LastPingAt.IsZero() {
		t.Fatalf("paused, after a call: %+v", got)
	}
	// Resumed, its job has a whole interval and grace to call.
	if _, err := f.pool.Exec(f.ctx, `UPDATE monitors SET last_ping_at = now() - interval '3 days' WHERE id = $1`, m.ID); err != nil {
		t.Fatal(err)
	}
	paused = false
	m, err = f.mod.Update(f.ctx, f.owner, m.ID, workspaces.Precondition{}, Patch{Paused: &paused})
	if err != nil || m.State != StatePending || !near(m.NextCheckAt, time.Now().Add(25*time.Hour)) {
		t.Fatalf("resumed: %+v, %v", m, err)
	}
	// A shorter grace counts from the last call, which is long gone.
	grace := time.Minute
	if m, err = f.mod.Update(f.ctx, f.owner, m.ID, workspaces.Precondition{}, Patch{Grace: &grace}); err != nil || !near(m.NextCheckAt, time.Now()) {
		t.Fatalf("grace changed: next %v, %v", m.NextCheckAt, err)
	}
}

func TestHeartbeatTokensRotate(t *testing.T) {
	f := setup(t)
	m := f.heartbeat(t)
	fresh, err := f.mod.RotateHeartbeatToken(f.ctx, f.owner, m.ID)
	if err != nil || fresh == m.HeartbeatToken || !strings.HasPrefix(fresh, HeartbeatTokenPrefix) {
		t.Fatalf("rotated to %q, %v", fresh, err)
	}
	if err := f.mod.Heartbeat(f.ctx, m.HeartbeatToken); !errors.Is(err, ErrNotFound) {
		t.Errorf("the old token: %v", err)
	}
	if err := f.mod.Heartbeat(f.ctx, fresh); err != nil {
		t.Errorf("the new token: %v", err)
	}
	if got := f.get(t, m); got.Version != m.Version+1 {
		t.Errorf("version %d after %d", got.Version, m.Version)
	}
	if _, err := f.mod.RotateHeartbeatToken(f.ctx, f.owner, f.monitor(t, "Orders").ID); !errors.Is(err, ErrNoHeartbeat) {
		t.Errorf("an HTTP monitor's token: %v", err)
	}
}

func TestHeartbeatsAreChecked(t *testing.T) {
	f := setup(t)
	cases := []struct {
		name string
		in   Input
		want string
	}{
		{"too often", Input{Interval: 30 * time.Second}, "/intervalSeconds"},
		{"too rarely", Input{Interval: 31 * 24 * time.Hour}, "/intervalSeconds"},
		{"a timeout", Input{Timeout: 10 * time.Second}, "/timeoutSeconds"},
		{"confirmations", Input{FailuresToConfirm: new(2)}, "/failuresToConfirm"},
		{"a long grace", Input{Grace: new(8 * 24 * time.Hour)}, "/graceSeconds"},
		{"a request", Input{Request: client.SavedRequest{Method: "GET"}}, "/request"},
		{"certificates", Input{CertificateWarningDays: new(14)}, "/certificateWarningDays"},
	}
	for _, c := range cases {
		c.in.Name, c.in.Kind, c.in.EnvironmentID = "Nightly", KindHeartbeat, f.env
		_, err := f.mod.Create(f.ctx, f.owner, f.project, c.in)
		if got := fieldErrors(t, err); got != c.want {
			t.Errorf("%s: errors at %s, want %s", c.name, got, c.want)
		}
	}
	// A month between calls, with no grace, is fine.
	m, err := f.mod.Create(f.ctx, f.owner, f.project, Input{Name: "Monthly", Kind: KindHeartbeat, EnvironmentID: f.env, Interval: 30 * 24 * time.Hour, Grace: new(time.Duration(0))})
	if err != nil || m.Grace != 0 {
		t.Fatalf("monthly: %+v, %v", m, err)
	}
	// An HTTP monitor has no grace.
	if _, err := f.mod.Create(f.ctx, f.owner, f.project, Input{Name: "Orders", EnvironmentID: f.env, Request: client.SavedRequest{Method: "GET", URL: "https://example.com"},
		Grace: new(time.Minute)}); fieldErrors(t, err) != "/graceSeconds" {
		t.Errorf("an HTTP monitor's grace: %v", err)
	}
}

func TestEvaluateRun(t *testing.T) {
	mon := &Monitor{ID: id.New(), Kind: KindCollection, Timeout: 90 * time.Second}
	step := func(s testruns.Step) testruns.MonitorResult {
		r := run(testruns.StatusFailed)
		return testruns.MonitorResult{Run: r, FirstFailed: &s}
	}
	timedOut := run(testruns.StatusFailed)
	timedOut.Stopped = testruns.StoppedTime
	cases := []struct {
		name          string
		res           testruns.MonitorResult
		outcome, code string
		message       string
	}{
		{"passed", testruns.MonitorResult{Run: run(testruns.StatusPassed)}, OutcomePassed, "", ""},
		{"canceled", testruns.MonitorResult{Run: run(testruns.StatusCanceled)}, OutcomeSkipped, "canceled", "Someone canceled the run, so this check says nothing about the service."},
		{"too long", testruns.MonitorResult{Run: timedOut}, OutcomeFailed, "timeout", "The run took longer than its timeout of 1 minute 30 seconds."},
		{"no response", step(testruns.Step{Name: "Ping", Failed: true, Failure: &runner.Failure{Code: "dns", Message: "No such host."}}), OutcomeFailed, "dns", "Ping: No such host."},
		{"a script error", step(testruns.Step{Name: "Ping", Path: "A / B", Failed: true, Scripts: &client.ScriptOutcome{Errors: []client.ScriptError{{Message: "x is not defined"}}}}),
			OutcomeFailed, "script", "A / B / Ping: x is not defined"},
	}
	for _, c := range cases {
		got := evaluateRun(mon, c.res, time.Now())
		if got.Outcome != c.outcome || got.FailureCode != c.code || got.FailureMessage != c.message || got.RunID != c.res.Run.ID {
			t.Errorf("%s: %s %s %q", c.name, got.Outcome, got.FailureCode, got.FailureMessage)
		}
	}
	long := step(testruns.Step{Name: "Ping", Failed: true, Failure: &runner.Failure{Code: "tls", Message: strings.Repeat("é", 1000)}})
	if got := evaluateRun(mon, long, time.Now()); len(got.FailureMessage) > maxRunMessage+len("…") || !strings.HasSuffix(got.FailureMessage, "…") {
		t.Errorf("a long message is %d bytes", len(got.FailureMessage))
	}
}

func TestWords(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0: "0 seconds", time.Second: "1 second", 90 * time.Second: "1 minute 30 seconds", 25 * time.Hour: "1 day 1 hour", 30 * 24 * time.Hour: "30 days",
	} {
		if got := words(d); got != want {
			t.Errorf("words(%v) = %q, want %q", d, got, want)
		}
	}
}
