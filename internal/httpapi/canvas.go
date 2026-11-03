// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/CemTbi/Signalbox/internal/modules/apis"
	"github.com/CemTbi/Signalbox/internal/modules/canvas"
	"github.com/CemTbi/Signalbox/internal/modules/identity"
	"github.com/CemTbi/Signalbox/internal/platform/id"
	"github.com/CemTbi/Signalbox/internal/platform/validate"
)

// Canvas is what the API needs to show a canvas live.
type Canvas interface {
	Live(ctx context.Context, userID, projectID, environmentID uuid.UUID, since string) (canvas.Answer, error)
}

// PlacementJSON is where a service's node goes on the canvas.
type PlacementJSON struct {
	ServiceID string  `json:"serviceId"`
	X         float64 `json:"x"`
	Y         float64 `json:"y"`
}

// TargetPlacementJSON is where a database's or outside host's node goes.
type TargetPlacementJSON struct {
	Kind string  `json:"kind"`
	Name string  `json:"name"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
}

// CanvasLayoutJSON is what's saved about a canvas beyond its services.
type CanvasLayoutJSON struct {
	Targets []TargetPlacementJSON `json:"targets"`
}

// LiveServiceJSON is a service's traffic.
type LiveServiceJSON struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	OnCanvas  bool    `json:"onCanvas"`
	Rate      float64 `json:"rate"`
	ErrorRate float64 `json:"errorRate"`
	P95Ms     float64 `json:"p95Ms,omitempty"`
}

// LiveLineJSON is who calls whom, and how much.
type LiveLineJSON struct {
	ID        string  `json:"id"`
	From      string  `json:"from"`
	To        string  `json:"to"`
	FromName  string  `json:"fromName"`
	ToName    string  `json:"toName"`
	ToKind    string  `json:"toKind"`
	System    string  `json:"system,omitempty"`
	Rate      float64 `json:"rate"`
	ErrorRate float64 `json:"errorRate"`
}

// LiveRemovedJSON lists what's gone since the state in changesSince.
type LiveRemovedJSON struct {
	Services []string `json:"services"`
	Lines    []string `json:"lines"`
}

// CanvasLiveJSON is what a canvas shows live, or what changed of it.
type CanvasLiveJSON struct {
	ChangesSince string            `json:"changesSince,omitempty"`
	Services     []LiveServiceJSON `json:"services"`
	Lines        []LiveLineJSON    `json:"lines"`
	Removed      LiveRemovedJSON   `json:"removed"`
}

// DependencyJSON is a line someone declared between two services.
type DependencyJSON struct {
	ID            string `json:"id"`
	FromServiceID string `json:"fromServiceId"`
	ToServiceID   string `json:"toServiceId"`
	CreatedAt     string `json:"createdAt"`
}

func dependencyJSON(d apis.Dependency) DependencyJSON {
	return DependencyJSON{
		ID: id.Format(id.Dependency, d.ID), FromServiceID: id.Format(id.Service, d.From), ToServiceID: id.Format(id.Service, d.To),
		CreatedAt: d.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// getCanvasLive answers the canvas's polling, every 5 seconds per browser.
// Its ETag names the whole state: sent back, it gets 304, or only what
// changed if the state is still known, or everything.
func (a *api) getCanvasLive(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	projectID, ok := pathID(w, r, "projectId", id.Project, "project")
	if !ok {
		return
	}
	envID, err := id.Parse(id.Environment, r.URL.Query().Get("environment"))
	if err != nil {
		writeInvalid(w, r, validate.Errors{{Pointer: "/environment", Code: "pattern", Detail: "Send an environment ID, such as env_01j9z3q1s3v5w7y9a1c3e5g7j9."}})
		return
	}
	got, err := a.Canvas.Live(r.Context(), s.User.ID, projectID, envID, r.Header.Get("If-None-Match"))
	if errors.Is(err, canvas.ErrNotFound) {
		writeProblem(w, r, http.StatusNotFound, "not-found", "There is no project with this ID, or the environment isn't in it.")
		return
	}
	if err != nil {
		a.fail(w, r, "canvas", err)
		return
	}
	w.Header().Set("ETag", got.Tag)
	// The body depends on the If-None-Match sent, so nothing may keep it.
	w.Header().Set("Cache-Control", "private, no-store")
	if got.NotModified {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	out := CanvasLiveJSON{
		ChangesSince: got.Since,
		Services:     make([]LiveServiceJSON, len(got.Services)),
		Lines:        make([]LiveLineJSON, len(got.Lines)),
		Removed:      LiveRemovedJSON{Services: orEmpty(got.RemovedServices), Lines: orEmpty(got.RemovedLines)},
	}
	for i, v := range got.Services {
		out.Services[i] = LiveServiceJSON{ID: v.ID, Name: v.Name, OnCanvas: v.OnCanvas, Rate: v.Rate, ErrorRate: v.ErrorRate, P95Ms: v.P95Ms}
	}
	for i, l := range got.Lines {
		out.Lines[i] = LiveLineJSON{ID: l.ID, From: l.From, To: l.To, FromName: l.FromName, ToName: l.ToName, ToKind: l.ToKind, System: l.System, Rate: l.Rate, ErrorRate: l.ErrorRate}
	}
	writeJSON(w, http.StatusOK, out)
}

func orEmpty(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func (a *api) getCanvas(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	projectID, ok := pathID(w, r, "projectId", id.Project, "project")
	if !ok {
		return
	}
	targets, err := a.APIs.CanvasTargets(r.Context(), s.User.ID, projectID)
	if err != nil {
		a.apisError(w, r, "project", err)
		return
	}
	out := CanvasLayoutJSON{Targets: make([]TargetPlacementJSON, len(targets))}
	for i, t := range targets {
		out.Targets[i] = TargetPlacementJSON{Kind: t.Kind, Name: t.Name, X: t.Position.X, Y: t.Position.Y}
	}
	writeJSON(w, http.StatusOK, out)
}

// placeServices moves nodes on a project's canvas. Layout isn't an edit of
// the services, so it needs no If-Match, and the last move wins.
func (a *api) placeServices(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	projectID, ok := pathID(w, r, "projectId", id.Project, "project")
	if !ok {
		return
	}
	var body struct {
		Positions []PlacementJSON       `json:"positions"`
		Targets   []TargetPlacementJSON `json:"targets"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	var fields validate.Errors
	placements := make([]apis.Placement, 0, len(body.Positions))
	for i, p := range body.Positions {
		serviceID, err := id.Parse(id.Service, p.ServiceID)
		if err != nil {
			fields.Add(fmt.Sprintf("/positions/%d/serviceId", i), "pattern", "Use a service ID, such as svc_01j9z3q1s3v5w7y9a1c3e5g7j9.")
			continue
		}
		placements = append(placements, apis.Placement{ServiceID: serviceID, Position: apis.Position{X: p.X, Y: p.Y}})
	}
	if len(fields) > 0 {
		writeInvalid(w, r, fields)
		return
	}
	targets := make([]apis.TargetPlacement, len(body.Targets))
	for i, t := range body.Targets {
		targets[i] = apis.TargetPlacement{Kind: t.Kind, Name: t.Name, Position: apis.Position{X: t.X, Y: t.Y}}
	}
	err := a.APIs.PlaceOnCanvas(r.Context(), s.User.ID, projectID, placements, targets)
	if errors.Is(err, apis.ErrNotFound) {
		writeProblem(w, r, http.StatusNotFound, "not-found",
			"There is no project with this ID, or one of the services isn't in it. Nothing was moved.")
		return
	}
	if err != nil {
		a.apisError(w, r, "project", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) listDependencies(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	projectID, ok := pathID(w, r, "projectId", id.Project, "project")
	if !ok {
		return
	}
	deps, err := a.APIs.ListDependencies(r.Context(), s.User.ID, projectID)
	if err != nil {
		a.apisError(w, r, "project", err)
		return
	}
	out := make([]DependencyJSON, len(deps))
	for i, d := range deps {
		out[i] = dependencyJSON(d)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

func (a *api) createDependency(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	projectID, ok := pathID(w, r, "projectId", id.Project, "project")
	if !ok {
		return
	}
	var body struct {
		FromServiceID string `json:"fromServiceId"`
		ToServiceID   string `json:"toServiceId"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	var fields validate.Errors
	from, err := id.Parse(id.Service, body.FromServiceID)
	if err != nil {
		fields.Add("/fromServiceId", "pattern", "Use a service ID, such as svc_01j9z3q1s3v5w7y9a1c3e5g7j9.")
	}
	to, err := id.Parse(id.Service, body.ToServiceID)
	if err != nil {
		fields.Add("/toServiceId", "pattern", "Use a service ID, such as svc_01j9z3q1s3v5w7y9a1c3e5g7j9.")
	}
	if len(fields) > 0 {
		writeInvalid(w, r, fields)
		return
	}
	d, err := a.APIs.CreateDependency(r.Context(), s.User.ID, projectID, from, to)
	if errors.Is(err, apis.ErrNotFound) {
		writeProblem(w, r, http.StatusNotFound, "not-found", "There is no project with this ID, or one of the services isn't in it.")
		return
	}
	if err != nil {
		a.apisError(w, r, "dependency", err)
		return
	}
	out := dependencyJSON(d)
	w.Header().Set("Location", "/api/v1/dependencies/"+out.ID)
	writeJSON(w, http.StatusCreated, out)
}

func (a *api) deleteDependency(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	dependencyID, ok := pathID(w, r, "dependencyId", id.Dependency, "dependency")
	if !ok {
		return
	}
	if err := a.APIs.DeleteDependency(r.Context(), s.User.ID, dependencyID); err != nil {
		a.apisError(w, r, "dependency", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
