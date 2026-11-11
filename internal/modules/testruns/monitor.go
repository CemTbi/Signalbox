// SPDX-License-Identifier: Apache-2.0

package testruns

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/CemTbi/Signalbox/internal/modules/client"
	"github.com/CemTbi/Signalbox/internal/modules/testruns/report"
	"github.com/CemTbi/Signalbox/internal/modules/workspaces"
	"github.com/CemTbi/Signalbox/internal/platform/id"
)

// A monitor keeps this many of its newest passed runs, and as many failed
// ones, so the run behind a failure outlasts the passes after it.
const keepPerMonitor = 50

// What a monitor reads of its run, from the report package, so other
// modules need only this one.
type (
	// Step is one request of a run.
	Step = report.Step
	// Response is what a step got back.
	Response = report.Response
	// Totals counts a run's requests and tests.
	Totals = report.Totals
)

// The statuses a monitor's run ends in.
const (
	StatusPassed   = report.Passed
	StatusFailed   = report.Failed
	StatusCanceled = report.Canceled
)

// ErrNothingToRun means the collection or folder has no requests, or more
// than a run may send.
var ErrNothingToRun = errors.New("testruns: nothing to run")

// MonitorRun is a run a monitor asks for.
type MonitorRun struct {
	MonitorID    uuid.UUID
	CollectionID uuid.UUID
	// FolderID runs one folder; uuid.Nil runs the whole collection.
	FolderID    uuid.UUID
	Environment workspaces.Environment
	// Limit is how long the whole run may take.
	Limit time.Duration
}

// MonitorResult is how a monitor's run ended.
type MonitorResult struct {
	Run Run
	// FirstFailed is the first step that failed; nil when none did.
	FirstFailed *Step
}

// RunForMonitor runs a collection, or a folder of it, for a monitor: for no
// one, through the server's runner, in the caller's goroutine and outside
// the run slots people's runs wait for. The run is kept as the monitor's,
// with every step, as any run is.
//
// An error means Signalbox couldn't run it, which says nothing about the
// API: the collection is gone (ErrNotFound), it has nothing to run
// (ErrNothingToRun), there's no runner (ErrRunnerUnavailable), or the run
// stopped on an error of its own. A run cut short by ctx ending is marked
// interrupted, and the error is ctx's.
func (m *Module) RunForMonitor(ctx context.Context, mr MonitorRun) (MonitorResult, error) {
	col, plan, err := m.client.PlanFor(ctx, mr.CollectionID, mr.FolderID)
	if errors.Is(err, client.ErrNotFound) {
		return MonitorResult{}, ErrNotFound
	}
	if err != nil {
		return MonitorResult{}, fmt.Errorf("plan the run: %w", err)
	}
	if col.ProjectID != mr.Environment.ProjectID {
		return MonitorResult{}, ErrNotFound
	}
	if len(plan.Steps) == 0 || len(plan.Steps) > client.MaxRunSteps {
		return MonitorResult{}, ErrNothingToRun
	}
	engine := m.client.Engine()
	if !engine.CanSend() {
		return MonitorResult{}, ErrRunnerUnavailable
	}
	env, err := m.client.EnvironmentOf(ctx, mr.Environment)
	if err != nil {
		return MonitorResult{}, fmt.Errorf("read the environment: %w", err)
	}
	r := Run{
		ID: id.New(), ProjectID: col.ProjectID, CollectionID: col.ID, FolderID: mr.FolderID, EnvironmentID: mr.Environment.ID,
		Name: plan.Name, EnvironmentName: env.Name, Status: report.Running, AgentName: engine.Via(), MonitorID: mr.MonitorID, Iterations: 1,
	}
	err = pgx.BeginFunc(ctx, m.db, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			INSERT INTO test_runs (id, project_id, collection_id, folder_id, environment_id, name, environment_name, status, options, agent_name, monitor_id, started_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, '{"iterations":1}', $9, $10, now()) RETURNING created_at, started_at`,
			r.ID, r.ProjectID, r.CollectionID, nullID(r.FolderID), r.EnvironmentID, r.Name, r.EnvironmentName, r.Status, r.AgentName, mr.MonitorID,
		).Scan(&r.CreatedAt, &r.StartedAt); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			DELETE FROM test_runs WHERE id IN (
				SELECT id FROM (
					SELECT id, row_number() OVER (PARTITION BY status = 'passed' ORDER BY id DESC) AS n
					FROM test_runs WHERE monitor_id = $1 AND status NOT IN ('queued', 'running')
				) kept WHERE n > $2)`, mr.MonitorID, keepPerMonitor)
		return err
	})
	if err != nil {
		return MonitorResult{}, fmt.Errorf("start a monitor's run: %w", err)
	}
	stop := m.beat(r.ID)
	ro := client.RunOptions{Iterations: 1, Environment: env.Values, Secrets: env.Secrets, ProjectID: col.ProjectID}
	status, stopped, first, runErr := m.drive(ctx, r.ID, engine, plan, ro, mr.Limit)
	stop()
	m.finish(r.ID, status, stopped)
	if status == report.Interrupted {
		return MonitorResult{}, errors.Join(ctx.Err(), runErr)
	}
	if runErr != nil {
		return MonitorResult{}, fmt.Errorf("run the collection: %w", runErr)
	}
	done, err := m.get(context.WithoutCancel(ctx), r.ID)
	if err != nil {
		return MonitorResult{}, err
	}
	return MonitorResult{Run: done, FirstFailed: first}, nil
}
