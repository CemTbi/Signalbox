// SPDX-License-Identifier: Apache-2.0

package testruns_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/CemTbi/Signalbox/internal/modules/client"
	"github.com/CemTbi/Signalbox/internal/modules/client/convert"
	"github.com/CemTbi/Signalbox/internal/modules/runner"
	"github.com/CemTbi/Signalbox/internal/modules/scripts"
	"github.com/CemTbi/Signalbox/internal/modules/testruns"
	"github.com/CemTbi/Signalbox/internal/modules/testruns/report"
	"github.com/CemTbi/Signalbox/internal/modules/workspaces"
	"github.com/CemTbi/Signalbox/internal/platform/id"
	"github.com/CemTbi/Signalbox/internal/platform/metrics"
	"github.com/CemTbi/Signalbox/internal/platform/postgres/postgrestest"
	"github.com/CemTbi/Signalbox/internal/platform/secretbox"
	"github.com/CemTbi/Signalbox/internal/platform/validate"
)

// gateRunner answers 200 with the request's path as the body. While gate
// is set, each request waits for it, or for its context.
type gateRunner struct {
	mu   sync.Mutex
	gate chan struct{}
	sent int
}

func (g *gateRunner) Run(ctx context.Context, req runner.Request) runner.Result {
	g.mu.Lock()
	gate := g.gate
	g.sent++
	g.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return runner.Result{Failure: &runner.Failure{Code: "canceled", Message: "canceled"}}
		}
	}
	return runner.Result{
		Response: &runner.Response{Status: 200, StatusText: "OK", Body: []byte(`{"url":"` + req.URL + `"}`), URL: req.URL,
			Headers: []runner.Header{{Key: "Content-Type", Value: "application/json"}}},
		Timings: runner.Timings{Total: 5 * time.Millisecond},
	}
}

type fixture struct {
	ctx     context.Context
	pool    *pgxpool.Pool
	ws      *workspaces.Service
	client  *client.Module
	runs    *testruns.Module
	run     *gateRunner
	owner   uuid.UUID
	project uuid.UUID
}

func setup(t *testing.T) fixture {
	t.Helper()
	pool := postgrestest.New(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ws := workspaces.New(pool, logger)
	run := &gateRunner{}
	c := client.New(pool, logger, ws, run)
	sp := scripts.NewPool(1, scripts.Options{Send: c.ScriptSender(), MaxMemory: -1})
	t.Cleanup(sp.Close)
	c.UseScripts(sp)
	box, err := secretbox.New(secretbox.NewKey())
	if err != nil {
		t.Fatal(err)
	}
	c.UseSecretKey(box)
	runs := testruns.New(pool, logger, ws, c)
	t.Cleanup(runs.Close)
	f := fixture{ctx: context.Background(), pool: pool, ws: ws, client: c, runs: runs, run: run}
	f.owner = f.user(t, "ada")
	w, err := ws.CreateWorkspace(f.ctx, f.owner, workspaces.WorkspaceInput{Name: "Shop", Slug: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := ws.CreateProject(f.ctx, f.owner, w.ID, workspaces.ProjectInput{Name: "Checkout"})
	if err != nil {
		t.Fatal(err)
	}
	f.project = p.ID
	return f
}

func (f fixture) user(t *testing.T, name string) uuid.UUID {
	t.Helper()
	u := id.New()
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO users (id, email, name) VALUES ($1, $2, $3)`, u, name+"@example.com", name); err != nil {
		t.Fatal(err)
	}
	return u
}

func (f fixture) member(t *testing.T, name string, role workspaces.Role) uuid.UUID {
	t.Helper()
	u := f.user(t, name)
	p, err := f.ws.GetProject(f.ctx, f.owner, f.project)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO workspace_members (workspace_id, user_id, role) VALUES ($1, $2, $3)`, p.WorkspaceID, u, role); err != nil {
		t.Fatal(err)
	}
	return u
}

func (f fixture) environment(t *testing.T, slug string) uuid.UUID {
	t.Helper()
	envs, err := f.ws.ListEnvironments(f.ctx, f.owner, f.project)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range envs {
		if e.Slug == slug {
			return e.ID
		}
	}
	t.Fatalf("no environment %s", slug)
	return uuid.Nil
}

// shop imports a collection: a login whose test stores a token, and an
// orders folder whose request fails a test.
func (f fixture) shop(t *testing.T) (collection, orders uuid.UUID) {
	t.Helper()
	c, err := f.client.ImportCollection(f.ctx, f.owner, f.project, convert.Collection{
		Name:      "Shop API",
		Variables: []convert.Variable{{Key: "base", Value: "https://api.example.com"}},
		Items: []convert.Item{
			{Name: "Log in", Request: &convert.Request{Method: "POST", URL: "{{base}}/login?key={{apiKey}}&user={{user}}"},
				Scripts: convert.Scripts{Test: `pm.environment.set("session", "s-" + pm.info.iteration); console.log("key", pm.environment.get("apiKey")); pm.test("logged in", () => pm.response.to.have.status(200));`}},
			{Name: "Orders", Items: []convert.Item{
				{Name: "List orders", Request: &convert.Request{Method: "GET", URL: "{{base}}/orders?session={{session}}"},
					Scripts: convert.Scripts{Test: `pm.test("created", () => pm.response.to.have.status(201));`}},
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	items, err := f.client.Items(f.ctx, f.owner, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.Name == "Orders" {
			orders = it.ID
		}
	}
	return c.ID, orders
}

func (f fixture) wait(t *testing.T, runID uuid.UUID) testruns.Run {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		r, err := f.runs.Get(f.ctx, f.owner, runID)
		if err != nil {
			t.Fatal(err)
		}
		if r.Finished() {
			return r
		}
		if time.Now().After(deadline) {
			t.Fatalf("run still %s", r.Status)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestRunAndReport(t *testing.T) {
	f := setup(t)
	col, _ := f.shop(t)
	staging := f.environment(t, "staging")
	if _, err := f.client.PutVariables(f.ctx, f.owner, staging, workspaces.Precondition{}, []client.VariableInput{
		{Key: "apiKey", Value: ptr("k-secret-1"), Secret: true},
	}); err != nil {
		t.Fatal(err)
	}
	data, err := testruns.ParseData(testruns.DataCSV, []byte("user\nada\nbob\n"))
	if err != nil {
		t.Fatal(err)
	}
	r, err := f.runs.Start(f.ctx, f.owner, col, testruns.Options{EnvironmentID: staging, Data: data})
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != report.Queued || r.Iterations != 2 || r.Name != "Shop API" || r.EnvironmentName == "" || r.DataRows != 2 {
		t.Errorf("started = %+v", r)
	}
	r = f.wait(t, r.ID)
	want := report.Totals{Requests: 4, FailedRequests: 2, Tests: 4, FailedTests: 2}
	if r.Status != report.Failed || r.Totals != want || r.StartedAt.IsZero() || r.FinishedAt.IsZero() {
		t.Errorf("finished = %s %+v", r.Status, r.Totals)
	}

	page, err := f.runs.Steps(f.ctx, f.owner, r.ID, 0, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 3 || !page.More || page.Items[0].Name != "Log in" || page.Items[1].Path != "Orders" || page.Items[2].Iteration != 1 {
		t.Fatalf("steps = %+v", page)
	}
	first := page.Items[0]
	if first.URL != "https://api.example.com/login?key=•••&user=ada" || first.Scripts.Logs != nil || first.Response.Headers != nil || !strings.HasPrefix(first.ItemID, "item_") {
		t.Errorf("summary = %+v", first)
	}
	if page.Items[1].URL != "https://api.example.com/orders?session=s-0" {
		t.Errorf("the token didn't carry: %s", page.Items[1].URL)
	}
	rest, err := f.runs.Steps(f.ctx, f.owner, r.ID, 3, 0)
	if err != nil || len(rest.Items) != 1 || rest.More || rest.Items[0].Seq != 4 {
		t.Errorf("rest = %+v, %v", rest, err)
	}

	full, err := f.runs.Step(f.ctx, f.owner, r.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(full.Scripts.Logs) != 1 || full.Scripts.Logs[0].Message != "key •••" || full.Response.Body != "" {
		t.Errorf("first step = %+v", full.Scripts)
	}
	failed, err := f.runs.Step(f.ctx, f.owner, r.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !failed.Failed || !strings.Contains(failed.Response.Body, "orders") || len(failed.Response.Headers) != 1 {
		t.Errorf("failed step = %+v", failed.Response)
	}
	if _, err := f.runs.Step(f.ctx, f.owner, r.ID, 9); !errors.Is(err, testruns.ErrNotFound) {
		t.Errorf("step 9: %v", err)
	}

	rep, err := f.runs.Report(f.ctx, f.owner, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := report.Write(&buf, report.JUnit, rep); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `<testsuite name="Orders / List orders (iteration 2)"`) || strings.Contains(buf.String(), "k-secret-1") {
		t.Errorf("junit:\n%s", buf.String())
	}

	list, err := f.runs.List(f.ctx, f.owner, col, workspaces.PageRequest{})
	if err != nil || len(list.Items) != 1 || list.Items[0].ID != r.ID {
		t.Errorf("list = %+v, %v", list, err)
	}
}

func TestFoldersBailAndPassing(t *testing.T) {
	f := setup(t)
	col, orders := f.shop(t)
	r, err := f.runs.Start(f.ctx, f.owner, col, testruns.Options{Iterations: 3, Bail: true})
	if err != nil {
		t.Fatal(err)
	}
	if r = f.wait(t, r.ID); r.Status != report.Failed || r.Stopped != client.StoppedBail || r.Totals.Requests != 2 {
		t.Errorf("bail = %s %s %+v", r.Status, r.Stopped, r.Totals)
	}
	// Login alone passes.
	if _, err := f.pool.Exec(f.ctx, `DELETE FROM collection_items WHERE id = $1`, orders); err != nil {
		t.Fatal(err)
	}
	r, err = f.runs.Start(f.ctx, f.owner, col, testruns.Options{Iterations: 2, Delay: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if r = f.wait(t, r.ID); r.Status != report.Passed || r.Totals.Requests != 2 || r.Delay != 10*time.Millisecond {
		t.Errorf("passing run = %+v", r)
	}
}

func TestStartChecks(t *testing.T) {
	f := setup(t)
	col, orders := f.shop(t)
	viewer := f.member(t, "linus", workspaces.Viewer)
	stranger := f.user(t, "eve")
	for _, tt := range []struct {
		name string
		user uuid.UUID
		col  uuid.UUID
		opts testruns.Options
		want error
		ptr  string
	}{
		{"viewer", viewer, col, testruns.Options{}, testruns.ErrForbidden, ""},
		{"stranger", stranger, col, testruns.Options{}, testruns.ErrNotFound, ""},
		{"no collection", f.owner, id.New(), testruns.Options{}, testruns.ErrNotFound, ""},
		{"no folder", f.owner, col, testruns.Options{FolderID: id.New()}, testruns.ErrNotFound, ""},
		{"no environment", f.owner, col, testruns.Options{EnvironmentID: id.New()}, testruns.ErrNotFound, ""},
		{"iterations", f.owner, col, testruns.Options{Iterations: 1001}, nil, "/iterations"},
		{"delay", f.owner, col, testruns.Options{Delay: 2 * time.Minute}, nil, "/delayMs"},
	} {
		_, err := f.runs.Start(f.ctx, tt.user, tt.col, tt.opts)
		if tt.want != nil {
			if !errors.Is(err, tt.want) {
				t.Errorf("%s: %v", tt.name, err)
			}
			continue
		}
		var ve validate.Errors
		if !errors.As(err, &ve) || ve[0].Pointer != tt.ptr {
			t.Errorf("%s: %v", tt.name, err)
		}
	}
	// At most 10,000 requests: 11 requests × 1,000 iterations is more.
	for i := range 9 {
		if _, err := f.client.CreateItem(f.ctx, f.owner, col, client.ItemInput{Kind: client.KindRequest, Name: fmt.Sprint("Extra ", i),
			Request: &client.SavedRequest{Method: "GET", URL: "https://api.example.com/"}}); err != nil {
			t.Fatal(err)
		}
	}
	var ve validate.Errors
	if _, err := f.runs.Start(f.ctx, f.owner, col, testruns.Options{Iterations: 1000}); !errors.As(err, &ve) || !strings.Contains(ve[0].Detail, "11 requests × 1000 iterations") {
		t.Errorf("too many steps: %v", err)
	}
	// An empty folder has nothing to run.
	empty, err := f.client.CreateItem(f.ctx, f.owner, col, client.ItemInput{Kind: client.KindFolder, Name: "Empty", Placement: client.Placement{Parent: orders}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.runs.Start(f.ctx, f.owner, col, testruns.Options{FolderID: empty.ID}); !errors.As(err, &ve) || ve[0].Pointer != "/folderId" {
		t.Errorf("empty folder: %v", err)
	}

	// Viewers read runs, but don't cancel them; strangers see nothing.
	r, err := f.runs.Start(f.ctx, f.owner, col, testruns.Options{FolderID: orders})
	if err != nil {
		t.Fatal(err)
	}
	f.wait(t, r.ID)
	if _, err := f.runs.Get(f.ctx, viewer, r.ID); err != nil {
		t.Errorf("viewer reads: %v", err)
	}
	if _, err := f.runs.Cancel(f.ctx, viewer, r.ID); !errors.Is(err, testruns.ErrForbidden) {
		t.Errorf("viewer cancels: %v", err)
	}
	if _, err := f.runs.Steps(f.ctx, stranger, r.ID, 0, 0); !errors.Is(err, testruns.ErrNotFound) {
		t.Errorf("stranger reads: %v", err)
	}
	if _, err := f.runs.List(f.ctx, stranger, col, workspaces.PageRequest{}); !errors.Is(err, testruns.ErrNotFound) {
		t.Errorf("stranger lists: %v", err)
	}
	// Cancelling a finished run changes nothing.
	if got, err := f.runs.Cancel(f.ctx, f.owner, r.ID); err != nil || got.Status != report.Failed {
		t.Errorf("cancel finished = %s, %v", got.Status, err)
	}
}

func TestCancelQueueAndShutdown(t *testing.T) {
	f := setup(t)
	reg := metrics.NewRegistry()
	f.runs.UseMetrics(reg)
	col, _ := f.shop(t)
	f.run.gate = make(chan struct{})
	// Two runs take both slots and wait on their first request.
	a, err := f.runs.Start(f.ctx, f.owner, col, testruns.Options{Iterations: 50})
	if err != nil {
		t.Fatal(err)
	}
	b, err := f.runs.Start(f.ctx, f.owner, col, testruns.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for f.running(t) < 2 {
		time.Sleep(10 * time.Millisecond)
	}
	// A third waits for a slot, and cancelling it ends it at once. It
	// starts only now: runs take free slots in whatever order their
	// goroutines get to them.
	c, err := f.runs.Start(f.ctx, f.owner, col, testruns.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for !strings.Contains(scrape(reg), "signalbox_test_runs_queued 1\n") {
		time.Sleep(10 * time.Millisecond)
	}
	if got := scrape(reg); !strings.Contains(got, "signalbox_test_runs_running 2\n") {
		t.Errorf("while two run:\n%s", got)
	}
	if got, err := f.runs.Cancel(f.ctx, f.owner, c.ID); err != nil || got.Status != report.Canceled {
		t.Errorf("cancel queued = %s, %v", got.Status, err)
	}
	// A running run stops after its step.
	if _, err := f.runs.Cancel(f.ctx, f.owner, a.ID); err != nil {
		t.Fatal(err)
	}
	f.run.gate <- struct{}{}
	f.run.gate <- struct{}{}
	if got := f.wait(t, a.ID); got.Status != report.Canceled || got.Totals.Requests != 1 {
		t.Errorf("canceled run = %s after %d steps", got.Status, got.Totals.Requests)
	}
	// Shutting down interrupts the rest.
	f.runs.Close()
	if got := f.wait(t, b.ID); got.Status != report.Interrupted || got.Stopped != testruns.StoppedRestart {
		t.Errorf("after close = %s %s", got.Status, got.Stopped)
	}
	// The run cancelled while it waited counts once its turn comes.
	got := scrape(reg)
	for _, want := range []string{
		`signalbox_test_runs_total{status="canceled"} 2`,
		`signalbox_test_runs_total{status="interrupted"} 1`,
		"signalbox_test_runs_running 0",
		"signalbox_test_runs_queued 0",
	} {
		if !strings.Contains(got, want+"\n") {
			t.Errorf("lacks %s:\n%s", want, got)
		}
	}

	// A process that died leaves runs that Recover marks.
	// Another process's live run isn't touched.
	if _, err := f.pool.Exec(f.ctx, `UPDATE test_runs SET status = 'running', finished_at = NULL, heartbeat_at = now() - interval '3 minutes' WHERE id = $1`, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(f.ctx, `UPDATE test_runs SET status = 'running', finished_at = NULL, heartbeat_at = now() WHERE id = $1`, c.ID); err != nil {
		t.Fatal(err)
	}
	if err := testruns.New(f.pool, slog.New(slog.NewTextHandler(io.Discard, nil)), f.ws, f.client).Recover(f.ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.wait(t, b.ID); got.Status != report.Interrupted {
		t.Errorf("recovered = %s", got.Status)
	}
	if got, err := f.runs.Get(f.ctx, f.owner, c.ID); err != nil || got.Status != report.Running {
		t.Errorf("live run = %s, %v", got.Status, err)
	}
}

func (f fixture) running(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM test_runs WHERE status = 'running'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestNoRunner(t *testing.T) {
	f := setup(t)
	col, _ := f.shop(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	runs := testruns.New(f.pool, logger, f.ws, client.New(f.pool, logger, f.ws, nil))
	if _, err := runs.Start(f.ctx, f.owner, col, testruns.Options{}); !errors.Is(err, testruns.ErrRunnerUnavailable) {
		t.Errorf("no runner: %v", err)
	}
}

func ptr(s string) *string { return &s }

func scrape(reg *metrics.Registry) string {
	var b bytes.Buffer
	reg.WriteTo(&b)
	return b.String()
}
