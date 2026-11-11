// SPDX-License-Identifier: Apache-2.0

package testruns_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/CemTbi/Signalbox/internal/modules/client"
	"github.com/CemTbi/Signalbox/internal/modules/testruns"
	"github.com/CemTbi/Signalbox/internal/modules/testruns/report"
	"github.com/CemTbi/Signalbox/internal/modules/workspaces"
	"github.com/CemTbi/Signalbox/internal/platform/id"
)

// monitor adds a collection monitor's row, which a monitor's runs belong to.
func (f fixture) monitor(t *testing.T, collectionID, environmentID uuid.UUID) uuid.UUID {
	t.Helper()
	m := id.New()
	if _, err := f.pool.Exec(f.ctx, `
		INSERT INTO monitors (id, project_id, environment_id, name, kind, request, interval_seconds, timeout_seconds, failures_to_confirm,
			certificate_warning_days, next_check_at, collection_id)
		VALUES ($1, $2, $3, 'Smoke', 'collection', '{}', 60, 60, 2, 0, now() + interval '1 hour', $4)`,
		m, f.project, environmentID, collectionID); err != nil {
		t.Fatal(err)
	}
	return m
}

func (f fixture) env(t *testing.T, slug string) workspaces.Environment {
	t.Helper()
	env, err := f.ws.Environment(f.ctx, f.environment(t, slug))
	if err != nil {
		t.Fatal(err)
	}
	return env
}

func TestRunForMonitor(t *testing.T) {
	f := setup(t)
	col, orders := f.shop(t)
	staging := f.env(t, "staging")
	if _, err := f.client.PutVariables(f.ctx, f.owner, staging.ID, workspaces.Precondition{}, []client.VariableInput{
		{Key: "apiKey", Value: ptr("k-secret-1"), Secret: true},
	}); err != nil {
		t.Fatal(err)
	}
	mon := f.monitor(t, col, staging.ID)

	res, err := f.runs.RunForMonitor(f.ctx, testruns.MonitorRun{MonitorID: mon, CollectionID: col, Environment: staging, Limit: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	r := res.Run
	if r.Status != report.Failed || r.MonitorID != mon || r.StartedBy != uuid.Nil || r.EnvironmentName != "Staging" || r.Totals.Requests != 2 || r.FinishedAt.IsZero() {
		t.Fatalf("run = %+v", r)
	}
	if res.FirstFailed == nil || res.FirstFailed.Name != "List orders" || res.FirstFailed.Path != "Orders" {
		t.Fatalf("first failed = %+v", res.FirstFailed)
	}
	// Kept as any run: people can read it, with secrets masked.
	steps, err := f.runs.Steps(f.ctx, f.owner, r.ID, 0, 100)
	if err != nil || len(steps.Items) != 2 {
		t.Fatalf("steps = %+v, %v", steps, err)
	}
	full, err := f.runs.Step(f.ctx, f.owner, r.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if raw, _ := json.Marshal(full); strings.Contains(string(raw), "k-secret-1") {
		t.Errorf("a monitor's run kept a secret: %s", raw)
	}
	// But it isn't one of the collection's runs.
	page, err := f.runs.List(f.ctx, f.owner, col, workspaces.PageRequest{})
	if err != nil || len(page.Items) != 0 {
		t.Errorf("the collection's runs = %+v, %v", page.Items, err)
	}

	// One folder.
	res, err = f.runs.RunForMonitor(f.ctx, testruns.MonitorRun{MonitorID: mon, CollectionID: col, FolderID: orders, Environment: staging, Limit: 10 * time.Second})
	if err != nil || res.Run.FolderID != orders || res.Run.Totals.Requests != 1 || res.Run.Name != "Orders" {
		t.Fatalf("folder run = %+v, %v", res.Run, err)
	}
}

func TestRunForMonitorTimeAndErrors(t *testing.T) {
	f := setup(t)
	col, _ := f.shop(t)
	staging := f.env(t, "staging")
	mon := f.monitor(t, col, staging.ID)

	// Out of time: failed, and says so.
	f.run.gate = make(chan struct{})
	res, err := f.runs.RunForMonitor(f.ctx, testruns.MonitorRun{MonitorID: mon, CollectionID: col, Environment: staging, Limit: 100 * time.Millisecond})
	f.run.gate = nil
	if err != nil || res.Run.Status != report.Failed || res.Run.Stopped != testruns.StoppedTime {
		t.Fatalf("out of time: %+v, %v", res.Run, err)
	}

	empty, err := f.client.CreateCollection(f.ctx, f.owner, f.project, "Empty", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.runs.RunForMonitor(f.ctx, testruns.MonitorRun{MonitorID: mon, CollectionID: empty.ID, Environment: staging, Limit: time.Second}); !errors.Is(err, testruns.ErrNothingToRun) {
		t.Errorf("an empty collection: %v", err)
	}
	if _, err := f.runs.RunForMonitor(f.ctx, testruns.MonitorRun{MonitorID: mon, CollectionID: id.New(), Environment: staging, Limit: time.Second}); !errors.Is(err, testruns.ErrNotFound) {
		t.Errorf("a missing collection: %v", err)
	}
	// Another project's environment can't run this project's collection.
	other := staging
	other.ProjectID = id.New()
	if _, err := f.runs.RunForMonitor(f.ctx, testruns.MonitorRun{MonitorID: mon, CollectionID: col, Environment: other, Limit: time.Second}); !errors.Is(err, testruns.ErrNotFound) {
		t.Errorf("another project's environment: %v", err)
	}
}

func TestAMonitorKeepsItsNewestRuns(t *testing.T) {
	f := setup(t)
	col, _ := f.shop(t)
	staging := f.env(t, "staging")
	mon := f.monitor(t, col, staging.ID)
	// Runs of the collection by a person aren't the monitor's to remove.
	mine, err := f.runs.Start(f.ctx, f.owner, col, testruns.Options{})
	if err != nil {
		t.Fatal(err)
	}
	f.wait(t, mine.ID)
	for i := range 120 {
		status := report.Passed
		if i%2 == 1 {
			status = report.Failed
		}
		if _, err := f.pool.Exec(f.ctx, `
			INSERT INTO test_runs (id, project_id, collection_id, name, status, options, monitor_id, finished_at)
			VALUES ($1, $2, $3, 'Shop API', $4, '{"iterations":1}', $5, now())`, id.New(), f.project, col, status, mon); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.runs.RunForMonitor(f.ctx, testruns.MonitorRun{MonitorID: mon, CollectionID: col, Environment: staging, Limit: 10 * time.Second}); err != nil {
		t.Fatal(err)
	}
	var passed, failed int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FILTER (WHERE status = 'passed'), count(*) FILTER (WHERE status = 'failed') FROM test_runs WHERE monitor_id = $1`, mon).
		Scan(&passed, &failed); err != nil {
		t.Fatal(err)
	}
	// 50 of each before the run, and the run, which failed.
	if passed != 50 || failed != 51 {
		t.Errorf("kept %d passed and %d failed", passed, failed)
	}
	if _, err := f.runs.Get(f.ctx, f.owner, mine.ID); err != nil {
		t.Errorf("a person's run: %v", err)
	}
}
