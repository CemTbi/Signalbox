// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/CemTbi/Signalbox/internal/modules/apis"
	"github.com/CemTbi/Signalbox/internal/modules/identity"
	"github.com/CemTbi/Signalbox/internal/platform/id"
	"github.com/CemTbi/Signalbox/internal/platform/validate"
)

// PlacementJSON is where a service's node goes on the canvas.
type PlacementJSON struct {
	ServiceID string  `json:"serviceId"`
	X         float64 `json:"x"`
	Y         float64 `json:"y"`
}

// placeServices moves services' nodes on a project's canvas. Layout isn't
// an edit of the services, so it needs no If-Match, and the last move wins.
func (a *api) placeServices(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	projectID, ok := pathID(w, r, "projectId", id.Project, "project")
	if !ok {
		return
	}
	var body struct {
		Positions []PlacementJSON `json:"positions"`
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
	err := a.APIs.PlaceServices(r.Context(), s.User.ID, projectID, placements)
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
