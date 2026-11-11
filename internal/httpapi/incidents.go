// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/CemTbi/Signalbox/internal/modules/identity"
	"github.com/CemTbi/Signalbox/internal/modules/monitors"
	"github.com/CemTbi/Signalbox/internal/platform/id"
	"github.com/CemTbi/Signalbox/internal/platform/validate"
)

// PersonJSON is who did something.
type PersonJSON struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// FirstCheckJSON is the check that took a monitor down.
type FirstCheckJSON struct {
	At             string `json:"at"`
	Status         int    `json:"status,omitempty"`
	FailureCode    string `json:"failureCode,omitempty"`
	FailureMessage string `json:"failureMessage,omitempty"`
	TraceID        string `json:"traceId,omitempty"`
	RunID          string `json:"runId,omitempty"`
}

// IncidentJSON is an incident.
type IncidentJSON struct {
	ID             string         `json:"id"`
	ProjectID      string         `json:"projectId"`
	MonitorID      string         `json:"monitorId,omitempty"`
	EnvironmentID  string         `json:"environmentId,omitempty"`
	MonitorName    string         `json:"monitorName"`
	State          string         `json:"state"`
	OpenedAt       string         `json:"openedAt"`
	FirstCheck     FirstCheckJSON `json:"firstCheck"`
	AcknowledgedAt string         `json:"acknowledgedAt,omitempty"`
	AcknowledgedBy *PersonJSON    `json:"acknowledgedBy,omitempty"`
	ResolvedAt     string         `json:"resolvedAt,omitempty"`
	ResolvedBy     *PersonJSON    `json:"resolvedBy,omitempty"`
	Resolution     string         `json:"resolution,omitempty"`
}

// IncidentEventJSON is one line of an incident's timeline.
type IncidentEventJSON struct {
	At     string      `json:"at"`
	Kind   string      `json:"kind"`
	Actor  *PersonJSON `json:"actor,omitempty"`
	From   string      `json:"from,omitempty"`
	To     string      `json:"to,omitempty"`
	Detail string      `json:"detail,omitempty"`
}

// IncidentDetailJSON is an incident with its timeline.
type IncidentDetailJSON struct {
	IncidentJSON
	Timeline []IncidentEventJSON `json:"timeline"`
}

func personJSON(p monitors.Person) *PersonJSON {
	if p.ID == uuid.Nil {
		return nil
	}
	return &PersonJSON{ID: id.Format(id.User, p.ID), Name: p.Name}
}

func optionalID(prefix id.Prefix, v uuid.UUID) string {
	if v == uuid.Nil {
		return ""
	}
	return id.Format(prefix, v)
}

func incidentJSON(in monitors.Incident) IncidentJSON {
	out := IncidentJSON{
		ID: id.Format(id.Incident, in.ID), ProjectID: id.Format(id.Project, in.ProjectID), MonitorID: optionalID(id.Monitor, in.MonitorID),
		EnvironmentID: optionalID(id.Environment, in.EnvironmentID), MonitorName: in.MonitorName, State: in.State, OpenedAt: preciseTime(in.OpenedAt),
		FirstCheck: FirstCheckJSON{
			At: preciseTime(in.First.At), Status: in.First.Status, FailureCode: in.First.FailureCode, FailureMessage: in.First.FailureMessage,
			TraceID: in.First.TraceID, RunID: runIDJSON(in.First.RunID),
		},
		AcknowledgedBy: personJSON(in.AcknowledgedBy), ResolvedBy: personJSON(in.ResolvedBy), Resolution: in.Resolution,
	}
	if !in.AcknowledgedAt.IsZero() {
		out.AcknowledgedAt = preciseTime(in.AcknowledgedAt)
	}
	if !in.ResolvedAt.IsZero() {
		out.ResolvedAt = preciseTime(in.ResolvedAt)
	}
	return out
}

func (a *api) incidentsError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, monitors.ErrNotFound):
		writeProblem(w, r, http.StatusNotFound, "not-found", "There is no incident with this ID, or you can't see it.")
	case errors.Is(err, monitors.ErrIncidentResolved):
		writeProblem(w, r, http.StatusConflict, "incident-resolved", "This incident is resolved, so there's nothing to acknowledge.")
	default:
		a.monitorsError(w, r, err)
	}
}

func (a *api) listIncidents(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	projectID, ok := pathID(w, r, "projectId", id.Project, "project")
	if !ok {
		return
	}
	p, ok := pageRequest(w, r, id.Incident)
	if !ok {
		return
	}
	q := monitors.IncidentQuery{State: r.URL.Query().Get("state"), Page: p}
	var fields validate.Errors
	if q.State != "" && q.State != monitors.IncidentOpen && q.State != monitors.IncidentResolved {
		fields.Add("/state", "enum", "Use open or resolved.")
	}
	if v := r.URL.Query().Get("monitorId"); v != "" {
		var err error
		if q.MonitorID, err = id.Parse(id.Monitor, v); err != nil {
			fields.Add("/monitorId", "pattern", "Use a monitor ID, such as mon_01j9z3w5y7a9c1e3g5j7m9p1r3.")
		}
	}
	if len(fields) > 0 {
		writeInvalid(w, r, fields)
		return
	}
	page, err := a.Monitors.ListIncidents(r.Context(), s.User.ID, projectID, q)
	if err != nil {
		a.incidentsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, pageJSON(page, id.Incident, func(in monitors.Incident) uuid.UUID { return in.ID }, incidentJSON))
}

func (a *api) getIncident(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	incidentID, ok := pathID(w, r, "incidentId", id.Incident, "incident")
	if !ok {
		return
	}
	in, events, err := a.Monitors.GetIncident(r.Context(), s.User.ID, incidentID)
	if err != nil {
		a.incidentsError(w, r, err)
		return
	}
	out := IncidentDetailJSON{IncidentJSON: incidentJSON(in), Timeline: make([]IncidentEventJSON, len(events))}
	for i, e := range events {
		out.Timeline[i] = IncidentEventJSON{At: preciseTime(e.At), Kind: e.Kind, Actor: personJSON(e.Actor), From: e.From, To: e.To, Detail: e.Detail}
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *api) acknowledgeIncident(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	a.actOnIncident(w, r, s, a.Monitors.Acknowledge)
}

func (a *api) resolveIncident(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	a.actOnIncident(w, r, s, a.Monitors.Resolve)
}

func (a *api) actOnIncident(w http.ResponseWriter, r *http.Request, s *identity.Session,
	act func(ctx context.Context, userID, incidentID uuid.UUID) (monitors.Incident, error),
) {
	incidentID, ok := pathID(w, r, "incidentId", id.Incident, "incident")
	if !ok {
		return
	}
	in, err := act(r.Context(), s.User.ID, incidentID)
	if err != nil {
		a.incidentsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, incidentJSON(in))
}
