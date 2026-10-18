// SPDX-License-Identifier: Apache-2.0

package client_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/CemTbi/Signalbox/internal/modules/client"
	"github.com/CemTbi/Signalbox/internal/modules/runner"
	"github.com/CemTbi/Signalbox/internal/modules/workspaces"
	"github.com/CemTbi/Signalbox/internal/platform/id"
	"github.com/CemTbi/Signalbox/internal/platform/postgres/postgrestest"
)

// fakeRunner answers every request with the same response and remembers
// what it was asked.
type fakeRunner struct {
	got  []runner.Request
	body []byte
}

func (f *fakeRunner) Run(_ context.Context, req runner.Request) runner.Result {
	f.got = append(f.got, req)
	return runner.Result{
		Response: &runner.Response{Status: 200, StatusText: "OK", Proto: "HTTP/1.1", Headers: []runner.Header{{Key: "Content-Type", Value: "application/json"}}, Body: f.body, URL: req.URL},
		Timings:  runner.Timings{Connect: 2 * time.Millisecond, Total: 12 * time.Millisecond},
	}
}

type fixture struct {
	ctx     context.Context
	pool    *pgxpool.Pool
	ws      *workspaces.Service
	owner   uuid.UUID
	project uuid.UUID
	run     *fakeRunner
	mod     *client.Module
}

func setup(t *testing.T) fixture {
	t.Helper()
	pool := postgrestest.New(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ws := workspaces.New(pool, logger)
	run := &fakeRunner{body: []byte(`{"ok":true}`)}
	f := fixture{ctx: context.Background(), pool: pool, ws: ws, run: run, mod: client.New(pool, logger, ws, run)}
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

func TestSendKeepsHistory(t *testing.T) {
	f := setup(t)
	req := runner.Request{
		Method: "post", URL: " https://api.example.com/orders ", Body: []byte(`{"sku":"A-1"}`),
		Headers: []runner.Header{{Key: "Authorization", Value: "Bearer secret"}, {Key: "cookie", Value: "s=1"}, {Key: "X-Trace", Value: "abc"}},
	}
	e, err := f.mod.Send(f.ctx, f.owner, f.project, req)
	if err != nil {
		t.Fatal(err)
	}
	// The runner gets the request as typed, credentials and all.
	if len(f.run.got) != 1 || f.run.got[0].Headers[0].Value != "Bearer secret" {
		t.Errorf("runner got %+v", f.run.got)
	}
	if e.Result.Response == nil || string(e.Result.Response.Body) != `{"ok":true}` || e.Method != http.MethodPost || e.URL != "https://api.example.com/orders" {
		t.Errorf("entry = %+v", e)
	}

	page, err := f.mod.History(f.ctx, f.owner, f.project, workspaces.PageRequest{})
	if err != nil || len(page.Items) != 1 || page.Items[0].Status != 200 || page.Items[0].Duration != 12*time.Millisecond {
		t.Fatalf("history = %+v, %v", page, err)
	}
	got, err := f.mod.HistoryEntry(f.ctx, f.owner, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	// History keeps no credentials.
	if got.Headers[0].Value != "•••" || got.Headers[1].Value != "•••" || got.Headers[2].Value != "abc" {
		t.Errorf("stored headers = %+v", got.Headers)
	}
	if string(got.Body) != `{"sku":"A-1"}` || string(got.Result.Response.Body) != `{"ok":true}` ||
		got.Result.Timings.Connect != 2*time.Millisecond || got.Result.Response.Headers[0].Key != "Content-Type" {
		t.Errorf("stored = %+v", got)
	}
	var raw string
	if err := f.pool.QueryRow(f.ctx, `SELECT record::text FROM request_history`).Scan(&raw); err != nil || strings.Contains(raw, "secret") {
		t.Errorf("a credential is stored: %s, %v", raw, err)
	}
}

func TestOnlyEditorsSendAndPeopleSeeTheirOwnHistory(t *testing.T) {
	f := setup(t)
	viewer := f.member(t, "linus", workspaces.Viewer)
	editor := f.member(t, "grace", workspaces.Editor)
	stranger := f.user(t, "mallory")
	req := runner.Request{Method: "GET", URL: "https://api.example.com/"}

	if _, err := f.mod.Send(f.ctx, viewer, f.project, req); !errors.Is(err, client.ErrForbidden) {
		t.Errorf("viewer sends: %v", err)
	}
	if _, err := f.mod.Send(f.ctx, stranger, f.project, req); !errors.Is(err, client.ErrNotFound) {
		t.Errorf("stranger sends: %v", err)
	}
	e, err := f.mod.Send(f.ctx, editor, f.project, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.mod.HistoryEntry(f.ctx, f.owner, e.ID); !errors.Is(err, client.ErrNotFound) {
		t.Errorf("someone else's entry: %v", err)
	}
	if page, err := f.mod.History(f.ctx, f.owner, f.project, workspaces.PageRequest{}); err != nil || len(page.Items) != 0 {
		t.Errorf("owner sees %+v, %v", page, err)
	}
	if _, err := f.mod.History(f.ctx, viewer, f.project, workspaces.PageRequest{}); err != nil {
		t.Errorf("a viewer can't read their own (empty) history: %v", err)
	}

	noRunner := client.New(f.pool, slog.New(slog.DiscardHandler), f.ws, nil)
	if _, err := noRunner.Send(f.ctx, f.owner, f.project, req); !errors.Is(err, client.ErrRunnerUnavailable) {
		t.Errorf("without a runner: %v", err)
	}
}

func TestHistoryIsBounded(t *testing.T) {
	f := setup(t)
	f.run.body = []byte(strings.Repeat("x", 2<<20))
	e, err := f.mod.Send(f.ctx, f.owner, f.project, runner.Request{Method: "GET", URL: "https://api.example.com/big"})
	if err != nil {
		t.Fatal(err)
	}
	// The sender gets the whole body; history keeps the first megabyte.
	if len(e.Result.Response.Body) != 2<<20 {
		t.Errorf("returned %d bytes", len(e.Result.Response.Body))
	}
	got, err := f.mod.HistoryEntry(f.ctx, f.owner, e.ID)
	if err != nil || len(got.Result.Response.Body) != 1<<20 || !got.Result.Response.Truncated {
		t.Errorf("kept %d bytes, truncated %v, %v", len(got.Result.Response.Body), got.Result.Response.Truncated, err)
	}

	if _, err := f.pool.Exec(f.ctx, `UPDATE request_history SET sent_at = now() - interval '31 days'`); err != nil {
		t.Fatal(err)
	}
	if page, _ := f.mod.History(f.ctx, f.owner, f.project, workspaces.PageRequest{}); len(page.Items) != 0 {
		t.Errorf("old entries listed: %+v", page)
	}
	if n, err := f.mod.RemoveOldHistory(f.ctx); err != nil || n != 1 {
		t.Errorf("removed %d, %v", n, err)
	}
}
