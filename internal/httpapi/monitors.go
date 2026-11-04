// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/CemTbi/Signalbox/internal/modules/client"
	"github.com/CemTbi/Signalbox/internal/modules/identity"
	"github.com/CemTbi/Signalbox/internal/modules/monitors"
	"github.com/CemTbi/Signalbox/internal/modules/workspaces"
	"github.com/CemTbi/Signalbox/internal/platform/id"
	"github.com/CemTbi/Signalbox/internal/platform/validate"
)

// Monitors is what the API needs from the monitors module.
type Monitors interface {
	Create(ctx context.Context, userID, projectID uuid.UUID, in monitors.Input) (monitors.Monitor, error)
	List(ctx context.Context, userID, projectID uuid.UUID, p workspaces.PageRequest) (workspaces.Page[monitors.Monitor], error)
	Get(ctx context.Context, userID, monitorID uuid.UUID) (monitors.Monitor, error)
	Update(ctx context.Context, userID, monitorID uuid.UUID, pre workspaces.Precondition, patch monitors.Patch) (monitors.Monitor, error)
	Delete(ctx context.Context, userID, monitorID uuid.UUID, pre workspaces.Precondition) error
	CheckNow(ctx context.Context, userID, monitorID uuid.UUID) (monitors.Check, error)
	Checks(ctx context.Context, userID, monitorID uuid.UUID, q monitors.CheckQuery) ([]monitors.Check, error)
	ProjectStats(ctx context.Context, userID, projectID uuid.UUID, from, to time.Time) ([]monitors.Stats, error)
}

// AssertionJSON is one thing a check's response must satisfy.
type AssertionJSON struct {
	Kind   string `json:"kind"`
	Target string `json:"target,omitempty"`
	Op     string `json:"op"`
	Value  string `json:"value,omitempty"`
}

// ComponentJSON is one part of a service, as its health check reports it.
type ComponentJSON struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// LastCheckJSON is what a monitor's last check found.
type LastCheckJSON struct {
	At             string          `json:"at"`
	Outcome        string          `json:"outcome"`
	Status         int             `json:"status,omitempty"`
	DurationMs     float64         `json:"durationMs"`
	FailureCode    string          `json:"failureCode,omitempty"`
	FailureMessage string          `json:"failureMessage,omitempty"`
	TraceID        string          `json:"traceId,omitempty"`
	Components     []ComponentJSON `json:"components"`
}

// MonitorJSON is a monitor.
type MonitorJSON struct {
	ID                     string           `json:"id"`
	ProjectID              string           `json:"projectId"`
	EnvironmentID          string           `json:"environmentId"`
	ServiceID              string           `json:"serviceId,omitempty"`
	Name                   string           `json:"name"`
	Kind                   string           `json:"kind"`
	Request                SavedRequestJSON `json:"request"`
	Assertions             []AssertionJSON  `json:"assertions"`
	IntervalSeconds        int              `json:"intervalSeconds"`
	TimeoutSeconds         int              `json:"timeoutSeconds"`
	FailuresToConfirm      int              `json:"failuresToConfirm"`
	CertificateWarningDays int              `json:"certificateWarningDays"`
	State                  string           `json:"state"`
	ConsecutiveFailures    int              `json:"consecutiveFailures"`
	NextCheckAt            string           `json:"nextCheckAt"`
	LastCheck              *LastCheckJSON   `json:"lastCheck,omitempty"`
	CertificateExpiresAt   string           `json:"certificateExpiresAt,omitempty"`
	CreatedAt              string           `json:"createdAt"`
	UpdatedAt              string           `json:"updatedAt"`
}

// CheckJSON is what one check found.
type CheckJSON struct {
	At                   string          `json:"at"`
	Outcome              string          `json:"outcome"`
	DurationMs           float64         `json:"durationMs"`
	Status               int             `json:"status,omitempty"`
	FailureCode          string          `json:"failureCode,omitempty"`
	FailureMessage       string          `json:"failureMessage,omitempty"`
	Components           []ComponentJSON `json:"components"`
	CertificateExpiresAt string          `json:"certificateExpiresAt,omitempty"`
	TraceID              string          `json:"traceId,omitempty"`
}

// MonitorStatsJSON is a monitor's checks over a time range.
type MonitorStatsJSON struct {
	MonitorID string  `json:"monitorId"`
	Checks    uint64  `json:"checks"`
	Failed    uint64  `json:"failed"`
	Uptime    float64 `json:"uptime"`
	MeanMs    float64 `json:"meanMs"`
	P95Ms     float64 `json:"p95Ms"`
}

func componentsJSON(parts []monitors.Component) []ComponentJSON {
	out := make([]ComponentJSON, len(parts))
	for i, c := range parts {
		out[i] = ComponentJSON(c)
	}
	return out
}

func monitorJSON(m monitors.Monitor) MonitorJSON {
	out := MonitorJSON{
		ID: id.Format(id.Monitor, m.ID), ProjectID: id.Format(id.Project, m.ProjectID), EnvironmentID: id.Format(id.Environment, m.EnvironmentID),
		Name: m.Name, Kind: m.Kind, Request: SavedRequestJSON{Method: m.Request.Method, URL: m.Request.URL, Body: m.Request.Body, Auth: authJSON(m.Request.Auth)},
		Assertions: make([]AssertionJSON, len(m.Assertions)), IntervalSeconds: int(m.Interval / time.Second), TimeoutSeconds: int(m.Timeout / time.Second),
		FailuresToConfirm: m.FailuresToConfirm, CertificateWarningDays: m.CertificateWarningDays, State: m.State, ConsecutiveFailures: m.ConsecutiveFailures,
		NextCheckAt: timestamp(m.NextCheckAt), CreatedAt: timestamp(m.CreatedAt), UpdatedAt: timestamp(m.UpdatedAt),
	}
	if m.ServiceID != uuid.Nil {
		out.ServiceID = id.Format(id.Service, m.ServiceID)
	}
	for _, h := range m.Request.Headers {
		out.Request.Headers = append(out.Request.Headers, SavedHeaderJSON(h))
	}
	for i, a := range m.Assertions {
		out.Assertions[i] = AssertionJSON(a)
	}
	if !m.CertificateExpiresAt.IsZero() {
		out.CertificateExpiresAt = timestamp(m.CertificateExpiresAt)
	}
	if l := m.Last; l != nil {
		out.LastCheck = &LastCheckJSON{
			At: preciseTime(l.At), Outcome: l.Outcome, Status: l.Status, DurationMs: ms(l.Duration), FailureCode: l.FailureCode,
			FailureMessage: l.FailureMessage, TraceID: l.TraceID, Components: componentsJSON(l.Components),
		}
	}
	return out
}

func checkJSON(c monitors.Check) CheckJSON {
	out := CheckJSON{
		At: preciseTime(c.At), Outcome: c.Outcome, DurationMs: ms(c.Duration), Status: c.Status, FailureCode: c.FailureCode,
		FailureMessage: c.FailureMessage, Components: componentsJSON(c.Components), TraceID: c.TraceID,
	}
	if !c.CertificateExpiresAt.IsZero() {
		out.CertificateExpiresAt = timestamp(c.CertificateExpiresAt)
	}
	return out
}

func (a *api) monitorsError(w http.ResponseWriter, r *http.Request, err error) {
	var fields validate.Errors
	switch {
	case errors.As(err, &fields):
		writeInvalid(w, r, fields)
	case errors.Is(err, monitors.ErrNotFound):
		writeProblem(w, r, http.StatusNotFound, "not-found", "There is no monitor with this ID, or the project, environment or service it names isn't there.")
	case errors.Is(err, monitors.ErrForbidden):
		writeProblem(w, r, http.StatusForbidden, "permission-denied", "Your role in this workspace doesn't allow this. Ask a workspace owner or admin.")
	case errors.Is(err, monitors.ErrVersionMismatch):
		writeProblem(w, r, http.StatusPreconditionFailed, "edit-conflict", "Someone changed this monitor since you loaded it. Load it again and reapply your change.")
	case errors.Is(err, client.ErrRunnerUnavailable):
		writeProblem(w, r, http.StatusServiceUnavailable, "runner-unavailable", "This server has no runner to send the check with.")
	default:
		a.fail(w, r, "monitor", err)
	}
}

func assertionsIn(list []AssertionJSON) []monitors.Assertion {
	out := make([]monitors.Assertion, len(list))
	for i, v := range list {
		out[i] = monitors.Assertion(v)
	}
	return out
}

func (a *api) createMonitor(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	projectID, ok := pathID(w, r, "projectId", id.Project, "project")
	if !ok {
		return
	}
	var body struct {
		Name                   *string           `json:"name"`
		Kind                   string            `json:"kind"`
		EnvironmentID          *string           `json:"environmentId"`
		ServiceID              string            `json:"serviceId"`
		Request                *SavedRequestJSON `json:"request"`
		Assertions             []AssertionJSON   `json:"assertions"`
		IntervalSeconds        int               `json:"intervalSeconds"`
		TimeoutSeconds         int               `json:"timeoutSeconds"`
		FailuresToConfirm      *int              `json:"failuresToConfirm"`
		CertificateWarningDays *int              `json:"certificateWarningDays"`
		Paused                 bool              `json:"paused"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	var fields validate.Errors
	in := monitors.Input{
		Name: required(&fields, "/name", body.Name), Kind: body.Kind, Assertions: assertionsIn(body.Assertions),
		Interval: time.Duration(body.IntervalSeconds) * time.Second, Timeout: time.Duration(body.TimeoutSeconds) * time.Second,
		FailuresToConfirm: body.FailuresToConfirm, CertificateWarningDays: body.CertificateWarningDays, Paused: body.Paused,
	}
	var err error
	if in.EnvironmentID, err = id.Parse(id.Environment, required(&fields, "/environmentId", body.EnvironmentID)); err != nil && body.EnvironmentID != nil {
		fields.Add("/environmentId", "pattern", "Use an environment ID, such as env_01j9z3q1s3v5w7y9a1c3e5g7j9.")
	}
	if body.ServiceID != "" {
		if in.ServiceID, err = id.Parse(id.Service, body.ServiceID); err != nil {
			fields.Add("/serviceId", "pattern", "Use a service ID, such as svc_01j9z3q1s3v5w7y9a1c3e5g7j9.")
		}
	}
	if body.Request == nil {
		fields.Add("/request", "required", "This field is required.")
	} else {
		in.Request = *savedRequest(body.Request)
	}
	if len(fields) > 0 {
		writeInvalid(w, r, fields)
		return
	}
	m, err := a.Monitors.Create(r.Context(), s.User.ID, projectID, in)
	if err != nil {
		a.monitorsError(w, r, err)
		return
	}
	out := monitorJSON(m)
	w.Header().Set("Location", "/api/v1/monitors/"+out.ID)
	w.Header().Set("ETag", etag(m.Version))
	writeJSON(w, http.StatusCreated, out)
}

func (a *api) listMonitors(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	projectID, ok := pathID(w, r, "projectId", id.Project, "project")
	if !ok {
		return
	}
	p, ok := pageRequest(w, r, id.Monitor)
	if !ok {
		return
	}
	page, err := a.Monitors.List(r.Context(), s.User.ID, projectID, p)
	if err != nil {
		a.monitorsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, pageJSON(page, id.Monitor, func(m monitors.Monitor) uuid.UUID { return m.ID }, monitorJSON))
}

func (a *api) getMonitor(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	monitorID, ok := pathID(w, r, "monitorId", id.Monitor, "monitor")
	if !ok {
		return
	}
	m, err := a.Monitors.Get(r.Context(), s.User.ID, monitorID)
	if err != nil {
		a.monitorsError(w, r, err)
		return
	}
	w.Header().Set("ETag", etag(m.Version))
	writeJSON(w, http.StatusOK, monitorJSON(m))
}

func (a *api) updateMonitor(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	monitorID, ok := pathID(w, r, "monitorId", id.Monitor, "monitor")
	if !ok {
		return
	}
	patch, ok := decodeMergePatch(w, r, "name", "kind", "serviceId", "request", "assertions", "intervalSeconds", "timeoutSeconds",
		"failuresToConfirm", "certificateWarningDays", "paused")
	if !ok {
		return
	}
	var fields validate.Errors
	change := monitors.Patch{Name: patch.str(&fields, "name", false), Kind: patch.str(&fields, "kind", false)}
	if v := patch.str(&fields, "serviceId", true); v != nil {
		var service uuid.UUID
		if *v != "" {
			var err error
			if service, err = id.Parse(id.Service, *v); err != nil {
				fields.Add("/serviceId", "pattern", "Use a service ID, such as svc_01j9z3q1s3v5w7y9a1c3e5g7j9.")
			}
		}
		change.ServiceID = &service
	}
	if raw, ok := patch["request"]; ok {
		var req SavedRequestJSON
		if string(raw) == "null" || strictJSON(raw, &req) != nil {
			fields.Add("/request", "type", "Send the request as an object.")
		} else {
			change.Request = savedRequest(&req)
		}
	}
	if raw, ok := patch["assertions"]; ok {
		var list []AssertionJSON
		if string(raw) == "null" || strictJSON(raw, &list) != nil {
			fields.Add("/assertions", "type", "Send the assertions as an array.")
		} else {
			in := assertionsIn(list)
			change.Assertions = &in
		}
	}
	change.Interval = patch.seconds(&fields, "intervalSeconds")
	change.Timeout = patch.seconds(&fields, "timeoutSeconds")
	change.FailuresToConfirm = patch.integer(&fields, "failuresToConfirm")
	change.CertificateWarningDays = patch.integer(&fields, "certificateWarningDays")
	if raw, ok := patch["paused"]; ok {
		var paused bool
		if json.Unmarshal(raw, &paused) != nil || string(raw) == "null" {
			fields.Add("/paused", "type", "Send true or false.")
		} else {
			change.Paused = &paused
		}
	}
	if len(fields) > 0 {
		writeInvalid(w, r, fields)
		return
	}
	m, err := a.Monitors.Update(r.Context(), s.User.ID, monitorID, precondition(r), change)
	if err != nil {
		a.monitorsError(w, r, err)
		return
	}
	w.Header().Set("ETag", etag(m.Version))
	writeJSON(w, http.StatusOK, monitorJSON(m))
}

func strictJSON(raw json.RawMessage, into any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	return dec.Decode(into)
}

func (p mergePatch) integer(errs *validate.Errors, field string) *int {
	raw, ok := p[field]
	if !ok {
		return nil
	}
	var v int
	if string(raw) == "null" || json.Unmarshal(raw, &v) != nil {
		errs.Add("/"+field, "type", "Use a whole number here.")
		return nil
	}
	return &v
}

func (p mergePatch) seconds(errs *validate.Errors, field string) *time.Duration {
	v := p.integer(errs, field)
	if v == nil {
		return nil
	}
	d := time.Duration(*v) * time.Second
	return &d
}

func (a *api) deleteMonitor(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	monitorID, ok := pathID(w, r, "monitorId", id.Monitor, "monitor")
	if !ok {
		return
	}
	if err := a.Monitors.Delete(r.Context(), s.User.ID, monitorID, precondition(r)); err != nil {
		a.monitorsError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// checkMonitor runs a check at once. It sends a request, so it counts
// against the person's sends.
func (a *api) checkMonitor(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	monitorID, ok := pathID(w, r, "monitorId", id.Monitor, "monitor")
	if !ok {
		return
	}
	if ok, retry := a.sendsByUser.Allow(s.User.ID.String()); !ok {
		writeRateLimited(w, r, retry)
		return
	}
	c, err := a.Monitors.CheckNow(r.Context(), s.User.ID, monitorID)
	if err != nil {
		a.monitorsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, checkJSON(c))
}

func (a *api) listChecks(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	monitorID, ok := pathID(w, r, "monitorId", id.Monitor, "monitor")
	if !ok {
		return
	}
	var fields validate.Errors
	q := monitors.CheckQuery{}
	q.From, q.To = timeRange(r, &fields)
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > monitors.MaxCheckLimit {
			fields.Add("/limit", "range", fmt.Sprintf("Send a limit from 1 to %d.", monitors.MaxCheckLimit))
		}
		q.Limit = n
	}
	if len(fields) > 0 {
		writeInvalid(w, r, fields)
		return
	}
	checks, err := a.Monitors.Checks(r.Context(), s.User.ID, monitorID, q)
	if err != nil {
		a.monitorsError(w, r, err)
		return
	}
	out := make([]CheckJSON, len(checks))
	for i, c := range checks {
		out[i] = checkJSON(c)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

func (a *api) getMonitorStats(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	projectID, ok := pathID(w, r, "projectId", id.Project, "project")
	if !ok {
		return
	}
	var fields validate.Errors
	from, to := timeRange(r, &fields)
	if r.URL.Query().Get("from") == "" {
		from = to.Add(-24 * time.Hour)
	}
	if len(fields) > 0 {
		writeInvalid(w, r, fields)
		return
	}
	stats, err := a.Monitors.ProjectStats(r.Context(), s.User.ID, projectID, from, to)
	if err != nil {
		a.monitorsError(w, r, err)
		return
	}
	out := make([]MonitorStatsJSON, len(stats))
	for i, st := range stats {
		out[i] = MonitorStatsJSON{MonitorID: id.Format(id.Monitor, st.MonitorID), Checks: st.Checks, Failed: st.Failed, MeanMs: ms(st.Mean), P95Ms: ms(st.P95)}
		if st.Checks > 0 {
			out[i].Uptime = float64(st.Checks-st.Failed) / float64(st.Checks)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}
