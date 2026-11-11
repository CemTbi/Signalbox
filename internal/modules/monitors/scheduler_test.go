// SPDX-License-Identifier: Apache-2.0

package monitors

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/CemTbi/Signalbox/internal/modules/apis"
	"github.com/CemTbi/Signalbox/internal/modules/client"
	"github.com/CemTbi/Signalbox/internal/modules/runner"
	"github.com/CemTbi/Signalbox/internal/modules/workspaces"
	"github.com/CemTbi/Signalbox/internal/platform/clickhouse"
	"github.com/CemTbi/Signalbox/internal/platform/clickhouse/clickhousetest"
	"github.com/CemTbi/Signalbox/internal/platform/id"
	"github.com/CemTbi/Signalbox/internal/platform/postgres/postgrestest"
)

// fakeProber answers checks as its answer function says, and counts them.
type fakeProber struct {
	calls  atomic.Int64
	answer func(ctx context.Context) (client.Entry, error)
}

func (p *fakeProber) Probe(ctx context.Context, _ workspaces.Environment, _ client.SavedRequest, _ time.Duration) (client.Entry, error) {
	p.calls.Add(1)
	return p.answer(ctx)
}

func ok(context.Context) (client.Entry, error) {
	return client.Entry{Result: runner.Result{Response: &runner.Response{Status: 200}}}, nil
}

func failing(context.Context) (client.Entry, error) {
	return client.Entry{Result: runner.Result{Failure: &runner.Failure{Code: "connect", Message: "refused"}}}, nil
}

type fixture struct {
	ctx     context.Context
	pool    *pgxpool.Pool
	conn    clickhouse.Conn
	mod     *Module
	prober  *fakeProber
	owner   uuid.UUID
	project uuid.UUID
	env     uuid.UUID
	ws      *workspaces.Service
	apis    *apis.Module
	client  *client.Module
	runs    *fakeRuns
}

func setup(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	pool := postgrestest.New(t)
	conn, url := clickhousetest.NewWithURL(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := clickhouse.Migrate(ctx, url, nil, logger); err != nil {
		t.Fatal(err)
	}
	ws := workspaces.New(pool, logger)
	services := apis.New(pool, logger, ws)
	f := &fixture{ctx: ctx, pool: pool, conn: conn, prober: &fakeProber{answer: ok}, ws: ws, apis: services}
	f.client = client.New(pool, logger, ws, nil)
	f.runs = &fakeRuns{}
	f.mod = New(pool, conn, logger, ws, services, f.prober, f.client, f.runs, nil)
	f.owner = id.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, name) VALUES ($1, 'ada@example.com', 'Ada')`, f.owner); err != nil {
		t.Fatal(err)
	}
	w, err := ws.CreateWorkspace(ctx, f.owner, workspaces.WorkspaceInput{Name: "Shop", Slug: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := ws.CreateProject(ctx, f.owner, w.ID, workspaces.ProjectInput{Name: "Checkout"})
	if err != nil {
		t.Fatal(err)
	}
	f.project = p.ID
	envs, err := ws.ListEnvironments(ctx, f.owner, p.ID)
	if err != nil || len(envs) == 0 {
		t.Fatalf("environments = %v, %v", envs, err)
	}
	f.env = envs[0].ID
	return f
}

func (f *fixture) monitor(t *testing.T, name string) Monitor {
	t.Helper()
	m, err := f.mod.Create(f.ctx, f.owner, f.project, Input{
		Name: name, EnvironmentID: f.env, Request: client.SavedRequest{Method: "GET", URL: "https://example.com/health"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// due makes a monitor's scheduled check due now.
func (f *fixture) due(t *testing.T, m Monitor) {
	t.Helper()
	if _, err := f.pool.Exec(f.ctx, `UPDATE monitors SET next_check_at = now() - interval '1 second' WHERE id = $1`, m.ID); err != nil {
		t.Fatal(err)
	}
}

// tick starts what's due and waits for it to end.
func (f *fixture) tick(t *testing.T) int {
	t.Helper()
	n, err := f.mod.Tick(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	f.mod.running.Wait()
	return n
}

func (f *fixture) get(t *testing.T, m Monitor) Monitor {
	t.Helper()
	got, err := f.mod.Get(f.ctx, f.owner, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestSchedulerMovesStateAlong(t *testing.T) {
	f := setup(t)
	m := f.monitor(t, "Orders")
	if m.State != StatePending || m.Interval != time.Minute || m.Timeout != 10*time.Second || m.FailuresToConfirm != 2 {
		t.Fatalf("defaults = %+v", m)
	}
	// Nothing is due yet: its first check is at its place in the interval.
	if m.NextCheckAt.Before(time.Now()) {
		t.Fatalf("next check %s is already due", m.NextCheckAt)
	}

	f.due(t, m)
	if n := f.tick(t); n != 1 {
		t.Fatalf("started %d checks", n)
	}
	got := f.get(t, m)
	if got.State != StateUp || got.Last == nil || got.Last.Outcome != OutcomePassed || got.Last.Status != 200 {
		t.Fatalf("after a success = %+v", got)
	}
	// It moved on to a time in the future, at the same phase.
	if !got.NextCheckAt.After(time.Now()) {
		t.Errorf("next check %s isn't ahead", got.NextCheckAt)
	}
	if n := f.tick(t); n != 0 {
		t.Errorf("started %d more checks with nothing due", n)
	}

	// One failure only starts a confirmation, 10 seconds later.
	f.prober.answer = failing
	f.due(t, m)
	f.tick(t)
	got = f.get(t, m)
	if got.State != StateUp || got.ConsecutiveFailures != 1 {
		t.Fatalf("after one failure = %+v", got)
	}
	var confirm *time.Time
	if err := f.pool.QueryRow(f.ctx, `SELECT confirm_at FROM monitors WHERE id = $1`, m.ID).Scan(&confirm); err != nil {
		t.Fatal(err)
	}
	if confirm == nil || time.Until(*confirm) > confirmAfter {
		t.Fatalf("confirm at %v", confirm)
	}
	if n := f.tick(t); n != 0 {
		t.Errorf("started %d checks before the confirmation was due", n)
	}

	// The confirmation doesn't move the schedule, and makes it down.
	if _, err := f.pool.Exec(f.ctx, `UPDATE monitors SET confirm_at = now() - interval '1 second' WHERE id = $1`, m.ID); err != nil {
		t.Fatal(err)
	}
	scheduled := got.NextCheckAt
	if n := f.tick(t); n != 1 {
		t.Fatalf("started %d confirmations", n)
	}
	got = f.get(t, m)
	if got.State != StateDown || got.ConsecutiveFailures != 2 || got.Last.FailureCode != "connect" {
		t.Fatalf("after the confirmation = %+v", got)
	}
	if !got.NextCheckAt.Equal(scheduled) {
		t.Errorf("a confirmation moved the schedule from %s to %s", scheduled, got.NextCheckAt)
	}

	// One success brings it back.
	f.prober.answer = ok
	f.due(t, m)
	f.tick(t)
	if got = f.get(t, m); got.State != StateUp || got.ConsecutiveFailures != 0 {
		t.Fatalf("after recovering = %+v", got)
	}

	// Every check is there to read, in order, and in the statistics.
	if err := f.mod.Flush(f.ctx); err != nil {
		t.Fatal(err)
	}
	checks, err := f.mod.Checks(f.ctx, f.owner, m.ID, CheckQuery{From: time.Now().Add(-time.Hour), To: time.Now().Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	var outcomes []string
	for _, c := range checks {
		outcomes = append(outcomes, c.Outcome)
	}
	if want := []string{OutcomePassed, OutcomeFailed, OutcomeFailed, OutcomePassed}; !slices.Equal(outcomes, want) {
		t.Errorf("outcomes, newest first = %v, want %v", outcomes, want)
	}
	stats, err := f.mod.ProjectStats(f.ctx, f.owner, f.project, time.Now().Add(-time.Hour), time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 || stats[0].MonitorID != m.ID || stats[0].Checks != 4 || stats[0].Failed != 2 {
		t.Errorf("stats = %+v", stats)
	}
}

func TestMissedTimesAreSkippedNotCaughtUp(t *testing.T) {
	f := setup(t)
	m := f.monitor(t, "Orders")
	// The scheduler was stopped for an hour.
	if _, err := f.pool.Exec(f.ctx, `UPDATE monitors SET next_check_at = next_check_at - interval '1 hour' WHERE id = $1`, m.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(f.ctx, `UPDATE monitors SET next_check_at = next_check_at - interval '1 minute' WHERE id = $1 AND next_check_at > now()`, m.ID); err != nil {
		t.Fatal(err)
	}
	if n := f.tick(t); n != 1 {
		t.Fatalf("started %d checks", n)
	}
	if n := f.tick(t); n != 0 {
		t.Errorf("caught up with %d more", n)
	}
	got := f.get(t, m)
	if got.NextCheckAt.Sub(m.NextCheckAt)%time.Minute != 0 {
		t.Errorf("it left its phase: %s, was %s", got.NextCheckAt, m.NextCheckAt)
	}
}

func TestACheckStillRunningIsSkipped(t *testing.T) {
	f := setup(t)
	m := f.monitor(t, "Orders")
	f.due(t, m)
	if _, err := f.pool.Exec(f.ctx, `UPDATE monitors SET running_since = now() WHERE id = $1`, m.ID); err != nil {
		t.Fatal(err)
	}
	f.tick(t)
	if n := f.prober.calls.Load(); n != 0 {
		t.Errorf("sent %d checks beside the one running", n)
	}
	if err := f.mod.Flush(f.ctx); err != nil {
		t.Fatal(err)
	}
	checks, err := f.mod.Checks(f.ctx, f.owner, m.ID, CheckQuery{From: time.Now().Add(-time.Hour), To: time.Now().Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if len(checks) != 1 || checks[0].Outcome != OutcomeSkipped || checks[0].FailureCode != "still-running" {
		t.Errorf("checks = %+v", checks)
	}
	if got := f.get(t, m); got.State != StatePending {
		t.Errorf("a skipped check changed the state to %s", got.State)
	}

	// A process that stopped mid-check doesn't hold it up for ever.
	f.due(t, m)
	if _, err := f.pool.Exec(f.ctx, `UPDATE monitors SET running_since = now() - interval '1 hour' WHERE id = $1`, m.ID); err != nil {
		t.Fatal(err)
	}
	f.tick(t)
	if n := f.prober.calls.Load(); n != 1 {
		t.Errorf("sent %d checks after the process stopped", n)
	}
}

func TestCheckingByHandLeavesAScheduledCheckRunning(t *testing.T) {
	f := setup(t)
	m := f.monitor(t, "Orders")
	if _, err := f.pool.Exec(f.ctx, `UPDATE monitors SET running_since = now() WHERE id = $1`, m.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.mod.CheckNow(f.ctx, f.owner, m.ID); err != nil {
		t.Fatal(err)
	}
	if n := f.prober.calls.Load(); n != 1 {
		t.Fatalf("sent %d checks by hand", n)
	}
	// The scheduled check is still running, so the next is skipped.
	f.due(t, m)
	f.tick(t)
	if n := f.prober.calls.Load(); n != 1 {
		t.Errorf("a scheduled check started beside the one running: %d sent", n)
	}
}

func TestSignalboxFailingIsNotTheServiceFailing(t *testing.T) {
	f := setup(t)
	m := f.monitor(t, "Orders")
	f.prober.answer = func(context.Context) (client.Entry, error) { return client.Entry{}, errors.New("no key for secrets") }
	f.due(t, m)
	f.tick(t)
	got := f.get(t, m)
	if got.State != StatePending || got.ConsecutiveFailures != 0 {
		t.Errorf("state = %+v", got)
	}
	var running *time.Time
	if err := f.pool.QueryRow(f.ctx, `SELECT running_since FROM monitors WHERE id = $1`, m.ID).Scan(&running); err != nil || running != nil {
		t.Errorf("running since %v, %v", running, err)
	}
}

func TestSeveralSchedulersNeverStartTheSameCheck(t *testing.T) {
	f := setup(t)
	const count = 300
	for i := range count {
		m := f.monitor(t, fmt.Sprintf("Monitor %d", i))
		f.due(t, m)
	}
	// The same database, three schedulers.
	other := New(f.pool, f.conn, f.mod.logger, f.ws, f.apis, f.prober, f.client, f.runs, nil)
	third := New(f.pool, f.conn, f.mod.logger, f.ws, f.apis, f.prober, f.client, f.runs, nil)
	var wg sync.WaitGroup
	for _, s := range []*Module{f.mod, other, third} {
		wg.Go(func() {
			if _, err := s.Tick(f.ctx); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	for _, s := range []*Module{f.mod, other, third} {
		s.running.Wait()
	}
	if n := f.prober.calls.Load(); n != count {
		t.Errorf("sent %d checks of %d monitors", n, count)
	}
}

func TestPausedMonitorsAreLeftAlone(t *testing.T) {
	f := setup(t)
	m := f.monitor(t, "Orders")
	f.due(t, m)
	paused := true
	got, err := f.mod.Update(f.ctx, f.owner, m.ID, workspaces.Precondition{}, Patch{Paused: &paused})
	if err != nil || got.State != StatePaused {
		t.Fatalf("paused = %+v, %v", got, err)
	}
	if n := f.tick(t); n != 0 {
		t.Errorf("started %d checks of a paused monitor", n)
	}

	// Checked by hand, it says what it found and stays paused.
	if _, err := f.mod.CheckNow(f.ctx, f.owner, m.ID); err != nil {
		t.Fatal(err)
	}
	if got = f.get(t, m); got.State != StatePaused || got.Last == nil {
		t.Errorf("after checking by hand = %+v", got)
	}

	// Resumed, it's pending again, with a time ahead of it.
	paused = false
	got, err = f.mod.Update(f.ctx, f.owner, m.ID, workspaces.Precondition{}, Patch{Paused: &paused})
	if err != nil || got.State != StatePending || !got.NextCheckAt.After(time.Now()) {
		t.Errorf("resumed = %+v, %v", got, err)
	}
}

func TestPausingWhileACheckRunsKeepsItPaused(t *testing.T) {
	f := setup(t)
	m := f.monitor(t, "Orders")
	f.due(t, m)
	f.prober.answer = func(ctx context.Context) (client.Entry, error) {
		paused := true
		if _, err := f.mod.Update(ctx, f.owner, m.ID, workspaces.Precondition{}, Patch{Paused: &paused}); err != nil {
			t.Error(err)
		}
		return failing(ctx)
	}
	f.tick(t)
	if got := f.get(t, m); got.State != StatePaused {
		t.Errorf("state = %s", got.State)
	}
}

func TestStoppingMidCheckLearnsNothing(t *testing.T) {
	f := setup(t)
	m := f.monitor(t, "Orders")
	f.due(t, m)
	ctx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	f.prober.answer = func(context.Context) (client.Entry, error) {
		cancel()
		return failing(ctx)
	}
	if _, err := f.mod.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	f.mod.running.Wait()
	got := f.get(t, m)
	if got.State != StatePending || got.ConsecutiveFailures != 0 {
		t.Errorf("state = %+v", got)
	}
	var running *time.Time
	if err := f.pool.QueryRow(f.ctx, `SELECT running_since FROM monitors WHERE id = $1`, m.ID).Scan(&running); err != nil || running != nil {
		t.Errorf("running since %v, %v", running, err)
	}
}

func TestEnvironmentHealthIsTheWorstOfAServicesMonitors(t *testing.T) {
	f := setup(t)
	svc, err := f.apis.CreateService(f.ctx, f.owner, f.project, apis.ServiceInput{Name: "Orders", Slug: "orders"})
	if err != nil {
		t.Fatal(err)
	}
	create := func(name string) Monitor {
		m, err := f.mod.Create(f.ctx, f.owner, f.project, Input{
			Name: name, EnvironmentID: f.env, ServiceID: svc.ID, Kind: KindHealth,
			Request: client.SavedRequest{Method: "GET", URL: "https://example.com/health"},
		})
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	a, b := create("A"), create("B")

	// Nothing checked yet says nothing.
	health, err := f.mod.EnvironmentHealth(f.ctx, f.env)
	if err != nil || len(health) != 0 {
		t.Fatalf("before any check: %+v, %v", health, err)
	}

	f.prober.answer = func(context.Context) (client.Entry, error) {
		return client.Entry{Result: runner.Result{Response: &runner.Response{Status: 200, Body: []byte(`{"status":"warn","checks":{"db":[{"status":"warn"}]}}`)}}}, nil
	}
	if _, err := f.mod.CheckNow(f.ctx, f.owner, a.ID); err != nil {
		t.Fatal(err)
	}
	f.prober.answer = ok
	if _, err := f.mod.CheckNow(f.ctx, f.owner, b.ID); err != nil {
		t.Fatal(err)
	}
	health, err = f.mod.EnvironmentHealth(f.ctx, f.env)
	if err != nil {
		t.Fatal(err)
	}
	if got := health[svc.ID]; got.State != StateDegraded || got.Detail != "A degraded: db warn" {
		t.Errorf("health = %+v", got)
	}
}

// TestSchedulerKeepsUp measures how late checks start, against RFC 0002's
// target: within 2 seconds at p99, for 1,000 monitors at a 1-minute interval
// on one machine. It takes minutes, so it runs when asked:
// SIGNALBOX_MONITOR_BENCH=1 go test ./internal/modules/monitors -run KeepsUp -v
func TestSchedulerKeepsUp(t *testing.T) {
	if os.Getenv("SIGNALBOX_MONITOR_BENCH") == "" {
		t.Skip("set SIGNALBOX_MONITOR_BENCH to measure")
	}
	f := setup(t)
	const count = 1000
	for i := range count {
		f.monitor(t, fmt.Sprintf("Monitor %d", i))
	}
	// Each check takes as long as a modest API.
	f.prober.answer = func(ctx context.Context) (client.Entry, error) {
		time.Sleep(100 * time.Millisecond)
		return ok(ctx)
	}
	var (
		mu     sync.Mutex
		delays []time.Duration
	)
	f.mod.observeDelay = func(d time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		delays = append(delays, d)
	}
	ctx, cancel := context.WithTimeout(f.ctx, 2*time.Minute+5*time.Second)
	defer cancel()
	go f.mod.Run(ctx)
	f.mod.Schedule(ctx)

	mu.Lock()
	defer mu.Unlock()
	slices.Sort(delays)
	if len(delays) < count {
		t.Fatalf("only %d checks started", len(delays))
	}
	at := func(q float64) time.Duration { return delays[int(float64(len(delays)-1)*q)] }
	t.Logf("%d checks started: p50 %s, p99 %s, max %s", len(delays), at(0.5), at(0.99), at(1))
	if at(0.99) > 2*time.Second {
		t.Errorf("p99 start delay of %s is over the target of 2 s", at(0.99))
	}
}
