// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/CemTbi/Signalbox/internal/modules/identity"
	"github.com/CemTbi/Signalbox/internal/modules/testruns"
	"github.com/CemTbi/Signalbox/internal/modules/testruns/report"
	"github.com/CemTbi/Signalbox/internal/modules/workspaces"
	"github.com/CemTbi/Signalbox/internal/platform/id"
	"github.com/CemTbi/Signalbox/internal/platform/validate"
)

// Runs is what the API needs from the test runs module.
type Runs interface {
	Start(ctx context.Context, userID, collectionID uuid.UUID, opts testruns.Options) (testruns.Run, error)
	List(ctx context.Context, userID, collectionID uuid.UUID, p workspaces.PageRequest) (testruns.Page, error)
	Get(ctx context.Context, userID, runID uuid.UUID) (testruns.Run, error)
	Cancel(ctx context.Context, userID, runID uuid.UUID) (testruns.Run, error)
	Steps(ctx context.Context, userID, runID uuid.UUID, after, limit int) (testruns.StepsPage, error)
	Step(ctx context.Context, userID, runID uuid.UUID, seq int) (report.Step, error)
	Report(ctx context.Context, userID, runID uuid.UUID) (report.Run, error)
}

// startRunLimit bounds a start request: a 5 MiB data file, quoted in JSON.
const startRunLimit = 12 << 20

// RunJSON is a test run, without its steps.
type RunJSON struct {
	ID            string `json:"id"`
	ProjectID     string `json:"projectId"`
	CollectionID  string `json:"collectionId"`
	FolderID      string `json:"folderId,omitempty"`
	EnvironmentID string `json:"environmentId,omitempty"`
	StartedBy     string `json:"startedBy,omitempty"`
	// AgentID and Agent are the agent the requests went through; absent
	// when they went from the server.
	AgentID     string        `json:"agentId,omitempty"`
	Agent       string        `json:"agent,omitempty"`
	Name        string        `json:"name"`
	Environment string        `json:"environment,omitempty"`
	Status      string        `json:"status"`
	Stopped     string        `json:"stopped,omitempty"`
	Iterations  int           `json:"iterations"`
	DataRows    int           `json:"dataRows"`
	DelayMs     int64         `json:"delayMs"`
	Bail        bool          `json:"bail"`
	Totals      report.Totals `json:"totals"`
	CreatedAt   string        `json:"createdAt"`
	StartedAt   string        `json:"startedAt,omitempty"`
	FinishedAt  string        `json:"finishedAt,omitempty"`
	DurationMs  float64       `json:"durationMs"`
}

func runJSON(r testruns.Run) RunJSON {
	out := RunJSON{
		ID: id.Format(id.Run, r.ID), ProjectID: id.Format(id.Project, r.ProjectID), CollectionID: id.Format(id.Collection, r.CollectionID),
		Name: r.Name, Environment: r.EnvironmentName, Status: r.Status, Stopped: r.Stopped, Iterations: r.Iterations,
		DataRows: r.DataRows, DelayMs: r.Delay.Milliseconds(), Bail: r.Bail, Totals: r.Totals, CreatedAt: timestamp(r.CreatedAt),
	}
	if r.FolderID != uuid.Nil {
		out.FolderID = id.Format(id.Item, r.FolderID)
	}
	if r.EnvironmentID != uuid.Nil {
		out.EnvironmentID = id.Format(id.Environment, r.EnvironmentID)
	}
	if r.StartedBy != uuid.Nil {
		out.StartedBy = id.Format(id.User, r.StartedBy)
	}
	if r.AgentID != uuid.Nil {
		out.AgentID = id.Format(id.Agent, r.AgentID)
	}
	out.Agent = r.AgentName
	if !r.StartedAt.IsZero() {
		out.StartedAt = timestamp(r.StartedAt)
		end := r.FinishedAt
		if end.IsZero() {
			end = time.Now()
		}
		out.DurationMs = ms(end.Sub(r.StartedAt))
	}
	if !r.FinishedAt.IsZero() {
		out.FinishedAt = timestamp(r.FinishedAt)
	}
	return out
}

// RunStepJSON is a step of a run. Lists leave out headers, console
// output, variable changes and the response body.
type RunStepJSON struct {
	Seq        int                `json:"seq"`
	Iteration  int                `json:"iteration"`
	ItemID     string             `json:"itemId,omitempty"`
	Name       string             `json:"name"`
	Path       string             `json:"path,omitempty"`
	Method     string             `json:"method"`
	URL        string             `json:"url"`
	Failed     bool               `json:"failed"`
	DurationMs float64            `json:"durationMs"`
	Headers    []HeaderJSON       `json:"headers,omitempty"`
	Unresolved []string           `json:"unresolved,omitempty"`
	Failure    *FailureJSON       `json:"failure,omitempty"`
	Response   *RunResponseJSON   `json:"response,omitempty"`
	Scripts    *ScriptOutcomeJSON `json:"scripts,omitempty"`
	Note       string             `json:"note,omitempty"`
}

// RunResponseJSON is what a step got back.
type RunResponseJSON struct {
	Status     int          `json:"status"`
	StatusText string       `json:"statusText,omitempty"`
	Headers    []HeaderJSON `json:"headers,omitempty"`
	Size       int          `json:"size"`
	// Body is the start of the body, kept when the step failed.
	Body string `json:"body,omitempty"`
}

func runStepJSON(s report.Step) RunStepJSON {
	out := RunStepJSON{
		Seq: s.Seq, Iteration: s.Iteration, ItemID: s.ItemID, Name: s.Name, Path: s.Path, Method: s.Method, URL: s.URL,
		Failed: s.Failed, DurationMs: ms(s.Duration), Unresolved: s.Unresolved, Scripts: scriptOutcomeJSON(s.Scripts), Note: s.Note,
	}
	if len(s.Headers) > 0 {
		out.Headers = headersJSON(s.Headers)
	}
	if f := s.Failure; f != nil {
		out.Failure = &FailureJSON{Code: f.Code, Message: f.Message}
	}
	if r := s.Response; r != nil {
		out.Response = &RunResponseJSON{Status: r.Status, StatusText: r.StatusText, Size: r.Size, Body: r.Body}
		if len(r.Headers) > 0 {
			out.Response.Headers = headersJSON(r.Headers)
		}
	}
	return out
}

func (a *api) runsError(w http.ResponseWriter, r *http.Request, what string, err error) {
	var fields validate.Errors
	var data *testruns.DataError
	switch {
	case errors.As(err, &fields):
		writeInvalid(w, r, fields)
	case errors.As(err, &data):
		writeInvalid(w, r, validate.Errors{{Pointer: "/data/content", Code: "format", Detail: data.Message}})
	case errors.Is(err, testruns.ErrNotFound):
		writeProblem(w, r, http.StatusNotFound, "not-found", "There is no "+what+" with this ID.")
	case errors.Is(err, testruns.ErrForbidden):
		writeProblem(w, r, http.StatusForbidden, "permission-denied",
			"Your role in this workspace doesn't allow running requests. Ask a workspace owner or admin to make you an editor.")
	case errors.Is(err, testruns.ErrRunnerUnavailable):
		writeProblem(w, r, http.StatusServiceUnavailable, "runner-unavailable",
			"This server doesn't run the runner role, which sends requests. Run signalbox-server with --roles=all.")
	default:
		a.secretsError(w, r, what, err)
	}
}

func (a *api) startRun(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	collectionID, ok := pathID(w, r, "collectionId", id.Collection, "collection")
	if !ok {
		return
	}
	if ok, retry := a.sendsByUser.Allow(s.User.ID.String()); !ok {
		writeRateLimited(w, r, retry)
		return
	}
	var body struct {
		FolderID      string `json:"folderId"`
		EnvironmentID string `json:"environmentId"`
		AgentID       string `json:"agentId"`
		Iterations    int    `json:"iterations"`
		Data          *struct {
			Format  string `json:"format"`
			Content string `json:"content"`
		} `json:"data"`
		DelayMs int64 `json:"delayMs"`
		Bail    bool  `json:"bail"`
	}
	if !decodeJSONUpTo(w, r, &body, startRunLimit) {
		return
	}
	var fields validate.Errors
	opts := testruns.Options{Iterations: body.Iterations, Delay: time.Duration(body.DelayMs) * time.Millisecond, Bail: body.Bail}
	if body.FolderID != "" {
		var err error
		if opts.FolderID, err = id.Parse(id.Item, body.FolderID); err != nil {
			fields.Add("/folderId", "pattern", "Use a folder's ID, such as item_01j9z3w5y7a9c1e3g5j7m9p1r3.")
		}
	}
	if body.EnvironmentID != "" {
		var err error
		if opts.EnvironmentID, err = id.Parse(id.Environment, body.EnvironmentID); err != nil {
			fields.Add("/environmentId", "pattern", "Use an environment ID, such as env_01j9z3n5q7s9v1w3y5a7c9e1g3.")
		}
	}
	if body.AgentID != "" {
		var err error
		if opts.AgentID, err = id.Parse(id.Agent, body.AgentID); err != nil {
			fields.Add("/agentId", "pattern", "Use one of your agents' IDs, such as agt_01j9z3w5y7a9c1e3g5j7m9p1r3.")
		}
	}
	if body.DelayMs < 0 || body.DelayMs > 60_000 {
		fields.Add("/delayMs", "range", "Wait from 0 to 60,000 milliseconds between requests.")
	}
	if d := body.Data; d != nil && d.Format != testruns.DataCSV && d.Format != testruns.DataJSON {
		fields.Add("/data/format", "enum", "Use a csv or json data file.")
	}
	if len(fields) > 0 {
		writeInvalid(w, r, fields)
		return
	}
	if d := body.Data; d != nil {
		rows, err := testruns.ParseData(d.Format, []byte(d.Content))
		if err != nil {
			a.runsError(w, r, "collection", err)
			return
		}
		opts.Data = rows
	}
	run, err := a.Runs.Start(r.Context(), s.User.ID, collectionID, opts)
	if err != nil {
		a.runsError(w, r, "collection, folder, environment or agent", err)
		return
	}
	out := runJSON(run)
	w.Header().Set("Location", "/api/v1/runs/"+out.ID)
	writeJSON(w, http.StatusAccepted, out)
}

func (a *api) listRuns(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	collectionID, ok := pathID(w, r, "collectionId", id.Collection, "collection")
	if !ok {
		return
	}
	req, ok := pageRequest(w, r, id.Run)
	if !ok {
		return
	}
	page, err := a.Runs.List(r.Context(), s.User.ID, collectionID, req)
	if err != nil {
		a.runsError(w, r, "collection", err)
		return
	}
	writeJSON(w, http.StatusOK, pageJSON(page, id.Run, func(r testruns.Run) uuid.UUID { return r.ID }, runJSON))
}

func (a *api) getRun(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	runID, ok := pathID(w, r, "runId", id.Run, "run")
	if !ok {
		return
	}
	run, err := a.Runs.Get(r.Context(), s.User.ID, runID)
	if err != nil {
		a.runsError(w, r, "run", err)
		return
	}
	writeJSON(w, http.StatusOK, runJSON(run))
}

func (a *api) cancelRun(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	runID, ok := pathID(w, r, "runId", id.Run, "run")
	if !ok {
		return
	}
	run, err := a.Runs.Cancel(r.Context(), s.User.ID, runID)
	if err != nil {
		a.runsError(w, r, "run", err)
		return
	}
	writeJSON(w, http.StatusOK, runJSON(run))
}

// RunStepsJSON is a page of a run's steps, in order.
type RunStepsJSON struct {
	Data []RunStepJSON `json:"data"`
	// More says there are steps after the last one.
	More bool `json:"more"`
}

func (a *api) listRunSteps(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	runID, ok := pathID(w, r, "runId", id.Run, "run")
	if !ok {
		return
	}
	q := r.URL.Query()
	var fields validate.Errors
	after := intParam(&fields, q.Get("after"), "/after", 0, 1<<30)
	limit := intParam(&fields, q.Get("limit"), "/limit", 1, 500)
	if len(fields) > 0 {
		writeInvalid(w, r, fields)
		return
	}
	page, err := a.Runs.Steps(r.Context(), s.User.ID, runID, after, limit)
	if err != nil {
		a.runsError(w, r, "run", err)
		return
	}
	out := RunStepsJSON{Data: make([]RunStepJSON, len(page.Items)), More: page.More}
	for i, st := range page.Items {
		out.Data[i] = runStepJSON(st)
	}
	writeJSON(w, http.StatusOK, out)
}

// intParam reads an optional integer query parameter; 0 when it's absent.
func intParam(fields *validate.Errors, v, pointer string, lo, hi int) int {
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < lo || n > hi {
		fields.Add(pointer, "range", fmt.Sprintf("Use a whole number from %d to %d.", lo, hi))
		return 0
	}
	return n
}

func (a *api) getRunStep(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	runID, ok := pathID(w, r, "runId", id.Run, "run")
	if !ok {
		return
	}
	seq, err := strconv.Atoi(r.PathValue("seq"))
	if err != nil || seq < 1 {
		writeProblem(w, r, http.StatusNotFound, "not-found", "There is no step with this number.")
		return
	}
	st, err := a.Runs.Step(r.Context(), s.User.ID, runID, seq)
	if err != nil {
		a.runsError(w, r, "run or step", err)
		return
	}
	writeJSON(w, http.StatusOK, runStepJSON(st))
}

func (a *api) getRunReport(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	runID, ok := pathID(w, r, "runId", id.Run, "run")
	if !ok {
		return
	}
	format := r.URL.Query().Get("format")
	if format == "" {
		format = report.JSON
	}
	if format != report.JUnit && format != report.JSON && format != report.HTML {
		writeInvalid(w, r, validate.Errors{{Pointer: "/format", Code: "enum", Detail: "Use junit, json or html."}})
		return
	}
	rep, err := a.Runs.Report(r.Context(), s.User.ID, runID)
	if err != nil {
		a.runsError(w, r, "run", err)
		return
	}
	w.Header().Set("Content-Type", report.ContentType(format))
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-%s.%s"`, report.Slug(rep.Name), rep.ID, report.Ext(format)))
	// The HTML report runs nothing and loads nothing.
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	w.Header().Set("Cache-Control", "no-store")
	if err := report.Write(w, format, rep); err != nil {
		a.Logger.WarnContext(r.Context(), "couldn't write a run report", "error", err)
	}
}
