// SPDX-License-Identifier: Apache-2.0

// Package testruns is the module for test runs: running a collection, or a
// folder of it, as a test suite, any number of iterations, with a data
// file; keeping every step's results; and reporting them. The client
// module's engine sends the requests. See docs/adr/0033-collection-runs.md.
package testruns

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/CemTbi/Signalbox/internal/modules/client"
	"github.com/CemTbi/Signalbox/internal/modules/testruns/report"
	"github.com/CemTbi/Signalbox/internal/modules/workspaces"
	"github.com/CemTbi/Signalbox/internal/platform/id"
	"github.com/CemTbi/Signalbox/internal/platform/metrics"
	"github.com/CemTbi/Signalbox/internal/platform/validate"
)

// Errors the module returns.
var (
	// ErrNotFound means the run, collection, folder or environment doesn't
	// exist, or the user can't see it.
	ErrNotFound = errors.New("testruns: not found")
	// ErrForbidden means the user's role doesn't allow running requests.
	ErrForbidden = errors.New("testruns: not allowed")
	// ErrRunnerUnavailable means this process can't send requests.
	ErrRunnerUnavailable = errors.New("testruns: no runner available")
)

// Limits and how long runs are kept.
const (
	// concurrentRuns is how many runs a process runs at once; more wait.
	concurrentRuns = 2
	maxRunTime     = time.Hour
	keepDays       = 90
	keepPerSuite   = 100
	maxStepsPage   = 500
	// A run's process beats every heartbeat; a run that missed a few is
	// one whose process stopped.
	heartbeat = 15 * time.Second
	stale     = 2 * time.Minute
)

// Why a run ended early, besides client.StoppedBail and StoppedSteps.
const (
	StoppedTime    = "time"
	StoppedError   = "error"
	StoppedRestart = "restart"
)

// Access tells the module what a user may do in a project.
type Access interface {
	ProjectRole(ctx context.Context, userID, projectID uuid.UUID) (workspaces.Role, error)
}

// Client plans runs and sends their requests; *client.Module is one.
type Client interface {
	PlanRun(ctx context.Context, userID, collectionID, folderID uuid.UUID) (client.Collection, client.Plan, error)
	RunEnvironment(ctx context.Context, userID, projectID, environmentID uuid.UUID) (client.RunEnvironment, error)
	EngineFor(ctx context.Context, userID, agentID uuid.UUID) (*client.Engine, error)
}

// Module starts runs, runs them in the background, and keeps them.
type Module struct {
	db     *pgxpool.Pool
	logger *slog.Logger
	access Access
	client Client
	slots  chan struct{}
	// waiting counts runs waiting for a slot.
	waiting  atomic.Int64
	finished *metrics.Counter
	// ctx ends when the module closes, which interrupts runs.
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New returns the module. Call Recover at startup, and Close at shutdown.
func New(db *pgxpool.Pool, logger *slog.Logger, access Access, c Client) *Module {
	ctx, cancel := context.WithCancel(context.Background())
	return &Module{db: db, logger: logger, access: access, client: c, slots: make(chan struct{}, concurrentRuns), ctx: ctx, cancel: cancel}
}

// UseMetrics counts the runs this process runs in reg.
func (m *Module) UseMetrics(reg *metrics.Registry) {
	m.finished = reg.NewCounter("signalbox_test_runs_total",
		"Test runs this process ran to the end, by status: passed, failed, canceled or interrupted.", "status")
	reg.NewGaugeFunc("signalbox_test_runs_running", "Test runs running in this process.",
		func() float64 { return float64(len(m.slots)) })
	reg.NewGaugeFunc("signalbox_test_runs_queued", "Test runs in this process waiting for one of its run slots.",
		func() float64 { return float64(m.waiting.Load()) })
}

// Recover marks runs whose process stopped, leaving them queued or
// running, as interrupted. Runs run in the process that started them, and
// that process keeps their heartbeat; other processes' live runs aren't
// touched.
func (m *Module) Recover(ctx context.Context) error {
	tag, err := m.db.Exec(ctx, `
		UPDATE test_runs SET status = 'interrupted', stopped = $1, finished_at = now()
		WHERE status IN ('queued', 'running') AND heartbeat_at < now() - $2 * interval '1 second'`, StoppedRestart, stale.Seconds())
	if err != nil {
		return fmt.Errorf("recover test runs: %w", err)
	}
	if n := tag.RowsAffected(); n > 0 {
		m.logger.InfoContext(ctx, "marked test runs whose process stopped as interrupted", "runs", n)
	}
	return nil
}

// Run recovers runs whose process stopped, and removes old runs, until ctx
// ends.
func (m *Module) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		if err := m.Recover(ctx); err != nil && ctx.Err() == nil {
			m.logger.WarnContext(ctx, "couldn't recover test runs", "error", err)
		}
		if tag, err := m.db.Exec(ctx, `DELETE FROM test_runs WHERE created_at < now() - $1 * interval '1 day'`, keepDays); err != nil {
			if ctx.Err() == nil {
				m.logger.WarnContext(ctx, "couldn't remove old test runs", "error", err)
			}
		} else if n := tag.RowsAffected(); n > 0 {
			m.logger.InfoContext(ctx, "removed old test runs", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Close interrupts runs in progress and waits for them to stop.
func (m *Module) Close() {
	m.cancel()
	m.wg.Wait()
}

// Options are how a run runs.
type Options struct {
	// FolderID runs one folder of the collection; uuid.Nil runs it all.
	FolderID uuid.UUID
	// EnvironmentID is an environment of the project, or uuid.Nil.
	EnvironmentID uuid.UUID
	// AgentID is one of the user's agents to send the requests through,
	// or uuid.Nil to send them from the server.
	AgentID uuid.UUID
	// Iterations is how many times the requests run: 0 runs them once per
	// data row, or once.
	Iterations int
	// Data are the rows of a data file; see ParseData.
	Data  []map[string]any
	Delay time.Duration
	Bail  bool
}

// storedOptions is what a run keeps of its options.
type storedOptions struct {
	Iterations int   `json:"iterations"`
	DataRows   int   `json:"dataRows,omitempty"`
	DelayMs    int64 `json:"delayMs,omitempty"`
	Bail       bool  `json:"bail,omitempty"`
}

// Run is a run, without its steps.
type Run struct {
	ID              uuid.UUID
	ProjectID       uuid.UUID
	CollectionID    uuid.UUID
	FolderID        uuid.UUID
	EnvironmentID   uuid.UUID
	StartedBy       uuid.UUID
	Name            string
	EnvironmentName string
	// AgentID and AgentName are the agent the requests went through, or
	// uuid.Nil and "" for the server.
	AgentID    uuid.UUID
	AgentName  string
	Status     string
	Stopped    string
	Iterations int
	DataRows   int
	Delay      time.Duration
	Bail       bool
	Totals     report.Totals
	CreatedAt  time.Time
	// StartedAt and FinishedAt are zero until then.
	StartedAt  time.Time
	FinishedAt time.Time
}

// Finished reports whether the run has ended.
func (r Run) Finished() bool { return r.Status != report.Queued && r.Status != report.Running }

func (m *Module) authorize(ctx context.Context, userID, projectID uuid.UUID, mayEdit bool) error {
	role, err := m.access.ProjectRole(ctx, userID, projectID)
	if errors.Is(err, workspaces.ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("check access: %w", err)
	}
	if mayEdit && !role.CanEdit() {
		return ErrForbidden
	}
	return nil
}

func clientError(err error) error {
	switch {
	case errors.Is(err, client.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, client.ErrForbidden):
		return ErrForbidden
	}
	return err
}

// Start starts a run of a collection, or a folder of it. Owners, admins
// and editors may: it sends requests. The run waits for a slot, then runs
// in the background; Get and Steps follow it.
func (m *Module) Start(ctx context.Context, userID, collectionID uuid.UUID, opts Options) (Run, error) {
	var errs validate.Errors
	if opts.Iterations < 0 || opts.Iterations > client.MaxIterations {
		errs.Add("/iterations", "range", fmt.Sprintf("Run from 1 to %d iterations.", client.MaxIterations))
	}
	if opts.Delay < 0 || opts.Delay > client.MaxRunDelay {
		errs.Add("/delayMs", "range", "Wait from 0 to 60,000 milliseconds between requests.")
	}
	if len(opts.Data) > client.MaxDataRows {
		errs.Add("/data", "maxItems", fmt.Sprintf("Use a data file of at most %d rows.", client.MaxDataRows))
	}
	if len(errs) > 0 {
		return Run{}, errs
	}
	col, plan, err := m.client.PlanRun(ctx, userID, collectionID, opts.FolderID)
	if err != nil {
		return Run{}, clientError(err)
	}
	engine, err := m.client.EngineFor(ctx, userID, opts.AgentID)
	if err != nil {
		return Run{}, clientError(err)
	}
	if !engine.CanSend() {
		return Run{}, ErrRunnerUnavailable
	}
	iterations := opts.Iterations
	if iterations == 0 {
		iterations = max(len(opts.Data), 1)
	}
	switch {
	case len(plan.Steps) == 0:
		errs.Add("/folderId", "empty", "There are no requests to run here. Add a request first.")
	case len(plan.Steps)*iterations > client.MaxRunSteps:
		errs.Add("/iterations", "range", fmt.Sprintf("A run sends at most %d requests: %d requests × %d iterations is more. Run fewer iterations, or a folder.",
			client.MaxRunSteps, len(plan.Steps), iterations))
	}
	if len(errs) > 0 {
		return Run{}, errs
	}
	var env client.RunEnvironment
	if opts.EnvironmentID != uuid.Nil {
		if env, err = m.client.RunEnvironment(ctx, userID, col.ProjectID, opts.EnvironmentID); err != nil {
			return Run{}, clientError(err)
		}
	}
	r := Run{
		ID: id.New(), ProjectID: col.ProjectID, CollectionID: col.ID, FolderID: opts.FolderID, EnvironmentID: opts.EnvironmentID,
		StartedBy: userID, Name: plan.Name, EnvironmentName: env.Name, Status: report.Queued, AgentID: opts.AgentID, AgentName: engine.Via(),
		Iterations: iterations, DataRows: len(opts.Data), Delay: opts.Delay, Bail: opts.Bail,
	}
	stored, err := json.Marshal(storedOptions{Iterations: iterations, DataRows: r.DataRows, DelayMs: r.Delay.Milliseconds(), Bail: r.Bail})
	if err != nil {
		return Run{}, err
	}
	err = pgx.BeginFunc(ctx, m.db, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			INSERT INTO test_runs (id, project_id, collection_id, folder_id, environment_id, started_by, name, environment_name, status, options, agent_id, agent_name)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12) RETURNING created_at`,
			r.ID, r.ProjectID, r.CollectionID, nullID(r.FolderID), nullID(r.EnvironmentID), userID, r.Name, r.EnvironmentName, r.Status, stored,
			nullID(r.AgentID), r.AgentName,
		).Scan(&r.CreatedAt); err != nil {
			return err
		}
		// Each collection keeps its newest runs, for a while.
		_, err := tx.Exec(ctx, `
			DELETE FROM test_runs WHERE collection_id = $1 AND (created_at < now() - $2 * interval '1 day' OR id IN (
				SELECT id FROM test_runs WHERE collection_id = $1 ORDER BY id DESC OFFSET $3))`, r.CollectionID, keepDays, keepPerSuite)
		return err
	})
	if err != nil {
		return Run{}, fmt.Errorf("start a run: %w", err)
	}
	ro := client.RunOptions{Iterations: iterations, Data: opts.Data, Environment: env.Values, Secrets: env.Secrets, Delay: opts.Delay, Bail: opts.Bail, ProjectID: col.ProjectID}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		stop := m.beat(r.ID)
		defer stop()
		m.execute(r.ID, engine, plan, ro)
	}()
	return r, nil
}

func nullID(u uuid.UUID) any {
	if u == uuid.Nil {
		return nil
	}
	return u
}

// beat keeps a run's heartbeat until stop is called.
func (m *Module) beat(runID uuid.UUID) (stop func()) {
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		ticker := time.NewTicker(heartbeat)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if _, err := m.db.Exec(m.ctx, `UPDATE test_runs SET heartbeat_at = now() WHERE id = $1`, runID); err != nil && m.ctx.Err() == nil {
					m.logger.WarnContext(m.ctx, "couldn't keep a test run's heartbeat", "error", err, id.LogAttr("run_id", id.Run, runID))
				}
			}
		}
	}()
	return func() {
		close(done)
		<-finished
	}
}

// execute runs a run when a slot is free, keeping each step as it ends.
func (m *Module) execute(runID uuid.UUID, engine *client.Engine, plan client.Plan, opts client.RunOptions) {
	ctx := m.ctx
	log := m.logger.With(id.LogAttr("run_id", id.Run, runID))
	m.waiting.Add(1)
	select {
	case m.slots <- struct{}{}:
		m.waiting.Add(-1)
		defer func() { <-m.slots }()
	case <-ctx.Done():
		m.waiting.Add(-1)
		m.finish(runID, report.Interrupted, StoppedRestart)
		return
	}
	// A run cancelled while it waited is already finished.
	tag, err := m.db.Exec(ctx, `UPDATE test_runs SET status = 'running', started_at = now() WHERE id = $1 AND status = 'queued'`, runID)
	if err != nil || tag.RowsAffected() == 0 {
		if err != nil {
			log.ErrorContext(ctx, "couldn't start a test run", "error", err)
			m.finish(runID, report.Failed, StoppedError)
		} else {
			m.finished.Inc(report.Canceled) // while it waited
		}
		return
	}
	runCtx, cancel := context.WithTimeout(ctx, maxRunTime)
	defer cancel()
	seq := 0
	failed, canceled := false, false
	stopped, err := engine.Run(runCtx, plan, opts, func(res client.StepResult) error {
		seq++
		s := report.FromResult(seq, res, plan)
		failed = failed || s.Failed
		cancelRequested, err := m.saveStep(ctx, runID, plan.Steps[res.Index].ItemID, s)
		if err != nil {
			return err
		}
		if cancelRequested {
			canceled = true
			return client.ErrStopRun
		}
		return nil
	})
	status := report.Passed
	if failed {
		status = report.Failed
	}
	switch {
	case canceled:
		status = report.Canceled
	case err == nil:
	case ctx.Err() != nil:
		status, stopped = report.Interrupted, StoppedRestart
	case errors.Is(runCtx.Err(), context.DeadlineExceeded):
		status, stopped = report.Failed, StoppedTime
	default:
		log.ErrorContext(ctx, "a test run stopped on an error", "error", err)
		status, stopped = report.Failed, StoppedError
	}
	m.finish(runID, status, stopped)
}

// saveStep keeps a step and counts it, and reports whether someone asked
// to cancel the run.
func (m *Module) saveStep(ctx context.Context, runID, itemID uuid.UUID, s report.Step) (bool, error) {
	var body []byte
	if s.Response != nil && s.Response.Body != "" {
		body = []byte(s.Response.Body)
		r := *s.Response
		r.Body = ""
		s.Response = &r
	}
	record, err := json.Marshal(s)
	if err != nil {
		return false, err
	}
	var t report.Totals
	t.Add(s)
	var status *int
	if s.Response != nil {
		status = &s.Response.Status
	}
	var cancelRequested bool
	err = pgx.BeginFunc(ctx, m.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO test_run_steps (run_id, seq, iteration, item_id, name, path, method, url, status, failed, duration_us, record, response_body)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
			runID, s.Seq, s.Iteration, nullID(itemID), s.Name, s.Path, s.Method, s.URL, status, s.Failed, s.Duration.Microseconds(), record, body); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `
			UPDATE test_runs SET heartbeat_at = now(), requests = requests + 1, failed_requests = failed_requests + $2, tests = tests + $3, failed_tests = failed_tests + $4
			WHERE id = $1 RETURNING cancel_requested`, runID, t.FailedRequests, t.Tests, t.FailedTests).Scan(&cancelRequested)
	})
	if err != nil {
		return false, fmt.Errorf("keep a step: %w", err)
	}
	return cancelRequested, nil
}

// finish records how a run ended. It uses its own context, so a run
// interrupted by shutdown is still marked.
func (m *Module) finish(runID uuid.UUID, status, stopped string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(m.ctx), 10*time.Second)
	defer cancel()
	tag, err := m.db.Exec(ctx, `
		UPDATE test_runs SET status = $2, stopped = $3, finished_at = now()
		WHERE id = $1 AND status IN ('queued', 'running')`, runID, status, stopped)
	if err != nil {
		m.logger.ErrorContext(ctx, "couldn't record how a test run ended", "error", err, id.LogAttr("run_id", id.Run, runID))
		return
	}
	if tag.RowsAffected() > 0 {
		m.finished.Inc(status)
	}
}

// Cancel cancels a run. A queued run ends at once; a running one stops
// after its current step. Cancelling a finished run changes nothing.
func (m *Module) Cancel(ctx context.Context, userID, runID uuid.UUID) (Run, error) {
	r, err := m.get(ctx, runID)
	if err != nil {
		return Run{}, err
	}
	if err := m.authorize(ctx, userID, r.ProjectID, true); err != nil {
		return Run{}, err
	}
	if _, err := m.db.Exec(ctx, `
		UPDATE test_runs SET cancel_requested = true,
			status = CASE WHEN status = 'queued' THEN 'canceled' ELSE status END,
			finished_at = CASE WHEN status = 'queued' THEN now() ELSE finished_at END
		WHERE id = $1 AND status IN ('queued', 'running')`, runID); err != nil {
		return Run{}, fmt.Errorf("cancel a run: %w", err)
	}
	return m.get(ctx, runID)
}

const runColumns = `id, project_id, collection_id, coalesce(folder_id, '00000000-0000-0000-0000-000000000000'),
	coalesce(environment_id, '00000000-0000-0000-0000-000000000000'), coalesce(started_by, '00000000-0000-0000-0000-000000000000'),
	name, environment_name, status, stopped, options, requests, failed_requests, tests, failed_tests, created_at, started_at, finished_at,
	coalesce(agent_id, '00000000-0000-0000-0000-000000000000'), agent_name`

func readRun(row pgx.Row) (Run, error) {
	var (
		r                 Run
		opts              []byte
		started, finished *time.Time
	)
	err := row.Scan(&r.ID, &r.ProjectID, &r.CollectionID, &r.FolderID, &r.EnvironmentID, &r.StartedBy, &r.Name, &r.EnvironmentName,
		&r.Status, &r.Stopped, &opts, &r.Totals.Requests, &r.Totals.FailedRequests, &r.Totals.Tests, &r.Totals.FailedTests,
		&r.CreatedAt, &started, &finished, &r.AgentID, &r.AgentName)
	if err != nil {
		return Run{}, err
	}
	var o storedOptions
	if err := json.Unmarshal(opts, &o); err != nil {
		return Run{}, fmt.Errorf("read run options: %w", err)
	}
	r.Iterations, r.DataRows, r.Delay, r.Bail = o.Iterations, o.DataRows, time.Duration(o.DelayMs)*time.Millisecond, o.Bail
	if started != nil {
		r.StartedAt = *started
	}
	if finished != nil {
		r.FinishedAt = *finished
	}
	return r, nil
}

func (m *Module) get(ctx context.Context, runID uuid.UUID) (Run, error) {
	r, err := readRun(m.db.QueryRow(ctx, `SELECT `+runColumns+` FROM test_runs WHERE id = $1`, runID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	if err != nil {
		return Run{}, fmt.Errorf("get run: %w", err)
	}
	return r, nil
}

// Get returns a run. Every member of its project may see it.
func (m *Module) Get(ctx context.Context, userID, runID uuid.UUID) (Run, error) {
	r, err := m.get(ctx, runID)
	if err != nil {
		return Run{}, err
	}
	if err := m.authorize(ctx, userID, r.ProjectID, false); err != nil {
		return Run{}, err
	}
	return r, nil
}

// Page is a page of runs, newest first.
type Page = workspaces.Page[Run]

// List lists a collection's runs, newest first.
func (m *Module) List(ctx context.Context, userID, collectionID uuid.UUID, p workspaces.PageRequest) (Page, error) {
	var projectID uuid.UUID
	err := m.db.QueryRow(ctx, `SELECT project_id FROM collections WHERE id = $1`, collectionID).Scan(&projectID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Page{}, ErrNotFound
	}
	if err != nil {
		return Page{}, fmt.Errorf("list runs: %w", err)
	}
	if err := m.authorize(ctx, userID, projectID, false); err != nil {
		return Page{}, err
	}
	limit := p.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var after any
	if p.After != uuid.Nil {
		after = p.After
	}
	rows, err := m.db.Query(ctx, `SELECT `+runColumns+` FROM test_runs
		WHERE collection_id = $1 AND ($2::uuid IS NULL OR id < $2) ORDER BY id DESC LIMIT $3`, collectionID, after, limit+1)
	if err != nil {
		return Page{}, fmt.Errorf("list runs: %w", err)
	}
	runs, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Run, error) { return readRun(row) })
	if err != nil {
		return Page{}, fmt.Errorf("list runs: %w", err)
	}
	page := Page{Items: runs}
	if len(runs) > limit {
		page.Items, page.More = runs[:limit], true
	}
	return page, nil
}

// StepsPage is a page of a run's steps, in order.
type StepsPage struct {
	Items []report.Step
	More  bool
}

// Steps lists a run's steps after seq after, in order, as summaries:
// without headers, console output, variable changes and bodies.
func (m *Module) Steps(ctx context.Context, userID, runID uuid.UUID, after, limit int) (StepsPage, error) {
	if _, err := m.Get(ctx, userID, runID); err != nil {
		return StepsPage{}, err
	}
	if limit <= 0 || limit > maxStepsPage {
		limit = maxStepsPage
	}
	steps, err := m.steps(ctx, runID, after, limit+1, false)
	if err != nil {
		return StepsPage{}, err
	}
	page := StepsPage{Items: steps}
	if len(steps) > limit {
		page.Items, page.More = steps[:limit], true
	}
	for i, s := range page.Items {
		page.Items[i] = summary(s)
	}
	return page, nil
}

func summary(s report.Step) report.Step {
	s.Headers = nil
	if s.Response != nil {
		r := *s.Response
		r.Headers, r.Body = nil, ""
		s.Response = &r
	}
	if s.Scripts != nil {
		sc := *s.Scripts
		sc.Logs, sc.Changes = nil, nil
		s.Scripts = &sc
	}
	return s
}

// Step returns one step of a run in full.
func (m *Module) Step(ctx context.Context, userID, runID uuid.UUID, seq int) (report.Step, error) {
	if _, err := m.Get(ctx, userID, runID); err != nil {
		return report.Step{}, err
	}
	steps, err := m.steps(ctx, runID, seq-1, 1, true)
	if err != nil {
		return report.Step{}, err
	}
	if len(steps) == 0 || steps[0].Seq != seq {
		return report.Step{}, ErrNotFound
	}
	return steps[0], nil
}

func (m *Module) steps(ctx context.Context, runID uuid.UUID, after, limit int, bodies bool) ([]report.Step, error) {
	rows, err := m.db.Query(ctx, `
		SELECT seq, iteration, coalesce(item_id::text, ''), duration_us, record, CASE WHEN $4 THEN response_body END
		FROM test_run_steps WHERE run_id = $1 AND seq > $2 ORDER BY seq LIMIT $3`, runID, after, limit, bodies)
	if err != nil {
		return nil, fmt.Errorf("list steps: %w", err)
	}
	steps, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (report.Step, error) {
		var (
			s      report.Step
			item   string
			us     int64
			record []byte
			body   []byte
		)
		if err := row.Scan(&s.Seq, &s.Iteration, &item, &us, &record, &body); err != nil {
			return s, err
		}
		seq, iteration := s.Seq, s.Iteration
		if err := json.Unmarshal(record, &s); err != nil {
			return s, err
		}
		s.Seq, s.Iteration, s.Duration = seq, iteration, time.Duration(us)*time.Microsecond
		s.ItemID = ""
		if u, err := uuid.Parse(item); err == nil {
			s.ItemID = id.Format(id.Item, u)
		}
		if s.Response != nil && body != nil {
			s.Response.Body = string(body)
		}
		return s, nil
	})
	if err != nil {
		return nil, fmt.Errorf("list steps: %w", err)
	}
	return steps, nil
}

// Report returns a run with all its steps, for a report.
func (m *Module) Report(ctx context.Context, userID, runID uuid.UUID) (report.Run, error) {
	r, err := m.Get(ctx, userID, runID)
	if err != nil {
		return report.Run{}, err
	}
	steps, err := m.steps(ctx, runID, 0, client.MaxRunSteps, true)
	if err != nil {
		return report.Run{}, err
	}
	return ToReport(r, steps), nil
}

// ToReport is a run as reports show it.
func ToReport(r Run, steps []report.Step) report.Run {
	out := report.Run{
		ID: id.Format(id.Run, r.ID), Name: r.Name, Environment: r.EnvironmentName, Status: r.Status,
		Iterations: r.Iterations, Stopped: r.Stopped, Totals: r.Totals, StartedAt: r.StartedAt, Steps: steps,
	}
	if out.StartedAt.IsZero() {
		out.StartedAt = r.CreatedAt
	}
	end := r.FinishedAt
	if end.IsZero() {
		end = time.Now()
	}
	if !r.StartedAt.IsZero() {
		out.Duration = end.Sub(r.StartedAt)
	}
	if out.Steps == nil {
		out.Steps = []report.Step{}
	}
	return out
}
