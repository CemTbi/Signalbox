// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/CemTbi/Signalbox/internal/modules/apis"
	"github.com/CemTbi/Signalbox/internal/modules/identity"
	"github.com/CemTbi/Signalbox/internal/modules/workspaces"
	"github.com/CemTbi/Signalbox/internal/platform/id"
	"github.com/CemTbi/Signalbox/internal/platform/validate"
	"github.com/CemTbi/Signalbox/pkg/refdocs"
	"github.com/CemTbi/Signalbox/pkg/specengine"
)

// specBodyLimit bounds JSON bodies that carry a document: JSON escaping can
// double the size of the text, and the module checks the document's own
// size.
const specBodyLimit = 2*apis.MaxSpecBytes + 64<<10

// PositionJSON is a node's place on the canvas.
type PositionJSON struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// ServiceJSON is a service in API responses.
type ServiceJSON struct {
	ID                   string       `json:"id"`
	ProjectID            string       `json:"projectId"`
	Name                 string       `json:"name"`
	Slug                 string       `json:"slug"`
	Description          string       `json:"description,omitempty"`
	TelemetryServiceName string       `json:"telemetryServiceName,omitempty"`
	Position             PositionJSON `json:"position"`
	CreatedAt            string       `json:"createdAt"`
	UpdatedAt            string       `json:"updatedAt"`
}

// SpecJSON is a spec in API responses.
type SpecJSON struct {
	ID                string `json:"id"`
	ServiceID         string `json:"serviceId"`
	Name              string `json:"name"`
	CurrentRevisionID string `json:"currentRevisionId,omitempty"`
	CreatedAt         string `json:"createdAt"`
	UpdatedAt         string `json:"updatedAt"`
}

// RevisionJSON is a revision in API responses.
type RevisionJSON struct {
	ID         string            `json:"id"`
	SpecID     string            `json:"specId"`
	Number     int               `json:"number"`
	Message    string            `json:"message,omitempty"`
	Format     string            `json:"format"`
	Version    string            `json:"version"`
	Title      string            `json:"title,omitempty"`
	APIVersion string            `json:"apiVersion,omitempty"`
	SizeBytes  int               `json:"sizeBytes"`
	SHA256     string            `json:"sha256"`
	Counts     specengine.Counts `json:"counts"`
	CreatedBy  string            `json:"createdBy"`
	CreatedAt  string            `json:"createdAt"`
}

// VersionJSON is a published version in API responses.
type VersionJSON struct {
	ID             string `json:"id"`
	SpecID         string `json:"specId"`
	Name           string `json:"name"`
	RevisionID     string `json:"revisionId"`
	RevisionNumber int    `json:"revisionNumber"`
	APIVersion     string `json:"apiVersion,omitempty"`
	Notes          string `json:"notes,omitempty"`
	PublishedBy    string `json:"publishedBy"`
	PublishedAt    string `json:"publishedAt"`
}

func versionJSON(v apis.Version) VersionJSON {
	return VersionJSON{
		ID: id.Format(id.Version, v.ID), SpecID: id.Format(id.Spec, v.SpecID), Name: v.Name,
		RevisionID: id.Format(id.Revision, v.RevisionID), RevisionNumber: v.RevisionNumber, APIVersion: v.APIVersion,
		Notes: v.Notes, PublishedBy: id.Format(id.User, v.PublishedBy), PublishedAt: timestamp(v.PublishedAt),
	}
}

func serviceJSON(s apis.Service) ServiceJSON {
	return ServiceJSON{
		ID: id.Format(id.Service, s.ID), ProjectID: id.Format(id.Project, s.ProjectID),
		Name: s.Name, Slug: s.Slug, Description: s.Description, TelemetryServiceName: s.TelemetryServiceName,
		Position:  PositionJSON{X: s.Position.X, Y: s.Position.Y},
		CreatedAt: timestamp(s.CreatedAt), UpdatedAt: timestamp(s.UpdatedAt),
	}
}

func specJSON(s apis.Spec) SpecJSON {
	out := SpecJSON{
		ID: id.Format(id.Spec, s.ID), ServiceID: id.Format(id.Service, s.ServiceID), Name: s.Name,
		CreatedAt: timestamp(s.CreatedAt), UpdatedAt: timestamp(s.UpdatedAt),
	}
	if s.CurrentRevisionID != uuid.Nil {
		out.CurrentRevisionID = id.Format(id.Revision, s.CurrentRevisionID)
	}
	return out
}

func revisionJSON(r apis.Revision) RevisionJSON {
	return RevisionJSON{
		ID: id.Format(id.Revision, r.ID), SpecID: id.Format(id.Spec, r.SpecID), Number: r.Number, Message: r.Message,
		Format: string(r.Format), Version: r.OpenAPIVersion, Title: r.Title, APIVersion: r.APIVersion,
		SizeBytes: r.SizeBytes, SHA256: hex.EncodeToString(r.SHA256[:]), Counts: r.Counts,
		CreatedBy: id.Format(id.User, r.CreatedBy), CreatedAt: timestamp(r.CreatedAt),
	}
}

// apisError answers for an error from the apis module.
func (a *api) apisError(w http.ResponseWriter, r *http.Request, what string, err error) {
	var (
		fields     validate.Errors
		unreadable *apis.UnreadableError
		hasErrors  *apis.HasErrorsError
	)
	switch {
	case errors.As(err, &hasErrors):
		noun := "errors"
		if hasErrors.Errors == 1 {
			noun = "error"
		}
		writeProblem(w, r, http.StatusUnprocessableEntity, "spec-has-errors",
			fmt.Sprintf("This revision has %d %s. Fix them and save a new revision, then publish it.", hasErrors.Errors, noun))
	case errors.Is(err, apis.ErrVersionExists):
		writeProblem(w, r, http.StatusConflict, "version-exists", "This spec already has a version with this name. Choose another.")
	case errors.As(err, &fields):
		writeInvalid(w, r, fields)
	case errors.As(err, &unreadable):
		detail := "The document can't be read as OpenAPI or Swagger."
		if len(unreadable.Report.Diagnostics) > 0 {
			d := unreadable.Report.Diagnostics[0]
			detail = d.Message
			if d.HowToFix != "" {
				detail += " " + d.HowToFix
			}
		}
		writeProblem(w, r, http.StatusBadRequest, "unreadable-document", detail)
	case errors.Is(err, apis.ErrNotFound):
		writeProblem(w, r, http.StatusNotFound, "not-found", "There is no "+what+" with this ID.")
	case errors.Is(err, apis.ErrForbidden):
		writeProblem(w, r, http.StatusForbidden, "permission-denied",
			"Your role in this workspace doesn't allow this. Ask a workspace owner or admin.")
	case errors.Is(err, apis.ErrSlugTaken):
		writeProblem(w, r, http.StatusConflict, "slug-taken", "Another service in this project already uses this slug. Choose another.")
	case errors.Is(err, apis.ErrVersionMismatch):
		writeProblem(w, r, http.StatusPreconditionFailed, "edit-conflict",
			"Someone changed this "+what+" since you loaded it. Load it again and reapply your change.")
	case errors.Is(err, apis.ErrPreconditionRequired):
		writeProblem(w, r, http.StatusPreconditionRequired, "precondition-required",
			"Send the spec's ETag in If-Match, so a save can't overwrite someone else's.")
	case errors.Is(err, apis.ErrCheckTimeout):
		writeProblem(w, r, http.StatusUnprocessableEntity, "check-timeout", "Checking the document took too long.")
	default:
		a.fail(w, r, what, err)
	}
}

func (a *api) listServices(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	projectID, ok := pathID(w, r, "projectId", id.Project, "project")
	if !ok {
		return
	}
	req, ok := pageRequest(w, r, id.Service)
	if !ok {
		return
	}
	page, err := a.APIs.ListServices(r.Context(), s.User.ID, projectID, req)
	if err != nil {
		a.apisError(w, r, "project", err)
		return
	}
	writeJSON(w, http.StatusOK, pageJSON(page, id.Service, func(s apis.Service) uuid.UUID { return s.ID }, serviceJSON))
}

func (a *api) createService(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	projectID, ok := pathID(w, r, "projectId", id.Project, "project")
	if !ok {
		return
	}
	var body struct {
		Name                 *string       `json:"name"`
		Slug                 *string       `json:"slug"`
		Description          *string       `json:"description"`
		TelemetryServiceName *string       `json:"telemetryServiceName"`
		Position             *PositionJSON `json:"position"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	var fields validate.Errors
	in := apis.ServiceInput{Name: required(&fields, "/name", body.Name), Slug: required(&fields, "/slug", body.Slug)}
	if body.Description != nil {
		in.Description = *body.Description
	}
	if body.TelemetryServiceName != nil {
		in.TelemetryServiceName = *body.TelemetryServiceName
	}
	if body.Position != nil {
		in.Position = &apis.Position{X: body.Position.X, Y: body.Position.Y}
	}
	if len(fields) > 0 {
		writeInvalid(w, r, fields)
		return
	}
	svc, err := a.APIs.CreateService(r.Context(), s.User.ID, projectID, in)
	if err != nil {
		a.apisError(w, r, "project", err)
		return
	}
	out := serviceJSON(svc)
	w.Header().Set("Location", "/api/v1/services/"+out.ID)
	w.Header().Set("ETag", etag(svc.Version))
	writeJSON(w, http.StatusCreated, out)
}

func (a *api) getService(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	serviceID, ok := pathID(w, r, "serviceId", id.Service, "service")
	if !ok {
		return
	}
	svc, err := a.APIs.GetService(r.Context(), s.User.ID, serviceID)
	if err != nil {
		a.apisError(w, r, "service", err)
		return
	}
	w.Header().Set("ETag", etag(svc.Version))
	writeJSON(w, http.StatusOK, serviceJSON(svc))
}

func (a *api) updateService(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	serviceID, ok := pathID(w, r, "serviceId", id.Service, "service")
	if !ok {
		return
	}
	patch, ok := decodeMergePatch(w, r, "name", "slug", "description", "telemetryServiceName", "position")
	if !ok {
		return
	}
	var fields validate.Errors
	change := apis.ServicePatch{
		Name:                 patch.str(&fields, "name", false),
		Slug:                 patch.str(&fields, "slug", false),
		Description:          patch.str(&fields, "description", true),
		TelemetryServiceName: patch.str(&fields, "telemetryServiceName", true),
	}
	if raw, ok := patch["position"]; ok {
		var p PositionJSON
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if string(raw) == "null" || dec.Decode(&p) != nil || !hasXY(raw) {
			fields.Add("/position", "type", "Send the position as an object with numbers x and y.")
		} else {
			change.Position = &apis.Position{X: p.X, Y: p.Y}
		}
	}
	if len(fields) > 0 {
		writeInvalid(w, r, fields)
		return
	}
	svc, err := a.APIs.UpdateService(r.Context(), s.User.ID, serviceID, precondition(r), change)
	if err != nil {
		a.apisError(w, r, "service", err)
		return
	}
	w.Header().Set("ETag", etag(svc.Version))
	writeJSON(w, http.StatusOK, serviceJSON(svc))
}

// hasXY reports whether a JSON object has both x and y.
func hasXY(raw json.RawMessage) bool {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return false
	}
	_, x := m["x"]
	_, y := m["y"]
	return x && y
}

func (a *api) deleteService(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	serviceID, ok := pathID(w, r, "serviceId", id.Service, "service")
	if !ok {
		return
	}
	if err := a.APIs.DeleteService(r.Context(), s.User.ID, serviceID, precondition(r)); err != nil {
		a.apisError(w, r, "service", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) listSpecs(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	serviceID, ok := pathID(w, r, "serviceId", id.Service, "service")
	if !ok {
		return
	}
	req, ok := pageRequest(w, r, id.Spec)
	if !ok {
		return
	}
	page, err := a.APIs.ListSpecs(r.Context(), s.User.ID, serviceID, req)
	if err != nil {
		a.apisError(w, r, "service", err)
		return
	}
	writeJSON(w, http.StatusOK, pageJSON(page, id.Spec, func(s apis.Spec) uuid.UUID { return s.ID }, specJSON))
}

func (a *api) createSpec(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	serviceID, ok := pathID(w, r, "serviceId", id.Service, "service")
	if !ok {
		return
	}
	var body struct {
		Name    *string `json:"name"`
		Content *string `json:"content"`
	}
	if !decodeJSONUpTo(w, r, &body, specBodyLimit) {
		return
	}
	var fields validate.Errors
	name := required(&fields, "/name", body.Name)
	content := required(&fields, "/content", body.Content)
	if len(fields) > 0 {
		writeInvalid(w, r, fields)
		return
	}
	spec, _, err := a.APIs.CreateSpec(r.Context(), s.User.ID, serviceID, name, content)
	if err != nil {
		a.apisError(w, r, "service", err)
		return
	}
	out := specJSON(spec)
	w.Header().Set("Location", "/api/v1/specs/"+out.ID)
	w.Header().Set("ETag", etag(spec.Version))
	writeJSON(w, http.StatusCreated, out)
}

func (a *api) getSpec(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	specID, ok := pathID(w, r, "specId", id.Spec, "spec")
	if !ok {
		return
	}
	spec, err := a.APIs.GetSpec(r.Context(), s.User.ID, specID)
	if err != nil {
		a.apisError(w, r, "spec", err)
		return
	}
	w.Header().Set("ETag", etag(spec.Version))
	writeJSON(w, http.StatusOK, specJSON(spec))
}

func (a *api) updateSpec(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	specID, ok := pathID(w, r, "specId", id.Spec, "spec")
	if !ok {
		return
	}
	patch, ok := decodeMergePatch(w, r, "name")
	if !ok {
		return
	}
	var fields validate.Errors
	name := patch.str(&fields, "name", false)
	if len(fields) > 0 {
		writeInvalid(w, r, fields)
		return
	}
	spec, err := a.APIs.RenameSpec(r.Context(), s.User.ID, specID, precondition(r), *name)
	if err != nil {
		a.apisError(w, r, "spec", err)
		return
	}
	w.Header().Set("ETag", etag(spec.Version))
	writeJSON(w, http.StatusOK, specJSON(spec))
}

func (a *api) deleteSpec(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	specID, ok := pathID(w, r, "specId", id.Spec, "spec")
	if !ok {
		return
	}
	if err := a.APIs.DeleteSpec(r.Context(), s.User.ID, specID, precondition(r)); err != nil {
		a.apisError(w, r, "spec", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) listRevisions(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	specID, ok := pathID(w, r, "specId", id.Spec, "spec")
	if !ok {
		return
	}
	req, ok := pageRequest(w, r, id.Revision)
	if !ok {
		return
	}
	page, err := a.APIs.ListRevisions(r.Context(), s.User.ID, specID, req)
	if err != nil {
		a.apisError(w, r, "spec", err)
		return
	}
	writeJSON(w, http.StatusOK, pageJSON(page, id.Revision, func(r apis.Revision) uuid.UUID { return r.ID }, revisionJSON))
}

func (a *api) createRevision(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	specID, ok := pathID(w, r, "specId", id.Spec, "spec")
	if !ok {
		return
	}
	var body struct {
		Content *string `json:"content"`
		Message *string `json:"message"`
	}
	if !decodeJSONUpTo(w, r, &body, specBodyLimit) {
		return
	}
	var fields validate.Errors
	content := required(&fields, "/content", body.Content)
	if len(fields) > 0 {
		writeInvalid(w, r, fields)
		return
	}
	msg := ""
	if body.Message != nil {
		msg = *body.Message
	}
	spec, rev, err := a.APIs.CreateRevision(r.Context(), s.User.ID, specID, precondition(r), content, msg)
	if err != nil {
		a.apisError(w, r, "spec", err)
		return
	}
	w.Header().Set("Location", "/api/v1/revisions/"+id.Format(id.Revision, rev.ID))
	// The ETag is the spec's, which the next save must send.
	w.Header().Set("ETag", etag(spec.Version))
	writeJSON(w, http.StatusCreated, revisionJSON(rev))
}

func (a *api) getRevision(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	revID, ok := pathID(w, r, "revisionId", id.Revision, "revision")
	if !ok {
		return
	}
	rev, err := a.APIs.GetRevision(r.Context(), s.User.ID, revID)
	if err != nil {
		a.apisError(w, r, "revision", err)
		return
	}
	writeJSON(w, http.StatusOK, revisionJSON(rev))
}

func (a *api) getRevisionContent(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	revID, ok := pathID(w, r, "revisionId", id.Revision, "revision")
	if !ok {
		return
	}
	content, fileType, err := a.APIs.RevisionContent(r.Context(), s.User.ID, revID)
	if err != nil {
		a.apisError(w, r, "revision", err)
		return
	}
	ct := "application/yaml; charset=utf-8"
	if fileType == "json" {
		ct = "application/json; charset=utf-8"
	}
	w.Header().Set("Content-Type", ct)
	// Revisions never change.
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.WriteHeader(http.StatusOK)
	// A YAML or JSON type, nosniff and the CSP from securityHeaders keep
	// browsers from rendering the document as a page.
	_, _ = w.Write([]byte(content)) //nolint:gosec // G705: served as YAML or JSON with nosniff, never as HTML.
}

func (a *api) getRevisionReport(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	revID, ok := pathID(w, r, "revisionId", id.Revision, "revision")
	if !ok {
		return
	}
	report, err := a.APIs.RevisionReport(r.Context(), s.User.ID, revID)
	if err != nil {
		a.apisError(w, r, "revision", err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (a *api) listVersions(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	specID, ok := pathID(w, r, "specId", id.Spec, "spec")
	if !ok {
		return
	}
	req, ok := pageRequest(w, r, id.Version)
	if !ok {
		return
	}
	page, err := a.APIs.ListVersions(r.Context(), s.User.ID, specID, req)
	if err != nil {
		a.apisError(w, r, "spec", err)
		return
	}
	writeJSON(w, http.StatusOK, pageJSON(page, id.Version, func(v apis.Version) uuid.UUID { return v.ID }, versionJSON))
}

func (a *api) publishVersion(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	specID, ok := pathID(w, r, "specId", id.Spec, "spec")
	if !ok {
		return
	}
	var body struct {
		Name       *string `json:"name"`
		RevisionID *string `json:"revisionId"`
		Notes      *string `json:"notes"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	var in apis.VersionInput
	if body.Name != nil {
		in.Name = *body.Name
	}
	if body.Notes != nil {
		in.Notes = *body.Notes
	}
	if body.RevisionID != nil {
		rev, err := id.Parse(id.Revision, *body.RevisionID)
		if err != nil {
			var fields validate.Errors
			fields.Add("/revisionId", "pattern", "Use a revision ID, such as rev_01j9z3s7v9x1z3b5d7f9h1k3m5.")
			writeInvalid(w, r, fields)
			return
		}
		in.RevisionID = rev
	}
	v, err := a.APIs.PublishVersion(r.Context(), s.User.ID, specID, in)
	if err != nil {
		a.apisError(w, r, "spec or revision", err)
		return
	}
	out := versionJSON(v)
	w.Header().Set("Location", "/api/v1/versions/"+out.ID)
	writeJSON(w, http.StatusCreated, out)
}

func (a *api) getVersion(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	versionID, ok := pathID(w, r, "versionId", id.Version, "version")
	if !ok {
		return
	}
	v, err := a.APIs.GetVersion(r.Context(), s.User.ID, versionID)
	if err != nil {
		a.apisError(w, r, "version", err)
		return
	}
	writeJSON(w, http.StatusOK, versionJSON(v))
}

// getReference answers with a published version's reference docs.
func (a *api) getReference(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	versionID, ok := pathID(w, r, "versionId", id.Version, "version")
	if !ok {
		return
	}
	v, err := a.APIs.GetVersion(r.Context(), s.User.ID, versionID)
	if err != nil {
		a.apisError(w, r, "version", err)
		return
	}
	// A version never changes, so its revision tags the reference.
	tag := `"` + id.Format(id.Revision, v.RevisionID) + `"`
	w.Header().Set("ETag", tag)
	w.Header().Set("Cache-Control", "private, no-cache")
	if r.Header.Get("If-None-Match") == tag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	ref, err := a.referenceCache.get(v.RevisionID, func() (string, error) {
		content, _, err := a.APIs.RevisionContent(r.Context(), s.User.ID, v.RevisionID)
		return content, err
	})
	switch {
	case errors.Is(err, refdocs.ErrUnsupported):
		writeProblem(w, r, http.StatusNotImplemented, "reference-unsupported",
			"This version is a Swagger 2.0 document. Reference docs need OpenAPI 3.0 or later.")
		return
	case errors.Is(err, apis.ErrNotFound), errors.Is(err, apis.ErrForbidden):
		a.apisError(w, r, "version", err)
		return
	case err != nil:
		a.fail(w, r, "reference", err)
		return
	}
	writeJSON(w, http.StatusOK, ref)
}

// APIs is what the API needs from the apis module.
type APIs interface {
	ListServices(ctx context.Context, userID, projectID uuid.UUID, p workspaces.PageRequest) (workspaces.Page[apis.Service], error)
	CreateService(ctx context.Context, userID, projectID uuid.UUID, in apis.ServiceInput) (apis.Service, error)
	GetService(ctx context.Context, userID, serviceID uuid.UUID) (apis.Service, error)
	PlaceServices(ctx context.Context, userID, projectID uuid.UUID, placements []apis.Placement) error
	UpdateService(ctx context.Context, userID, serviceID uuid.UUID, pre workspaces.Precondition, patch apis.ServicePatch) (apis.Service, error)
	DeleteService(ctx context.Context, userID, serviceID uuid.UUID, pre workspaces.Precondition) error
	ListSpecs(ctx context.Context, userID, serviceID uuid.UUID, p workspaces.PageRequest) (workspaces.Page[apis.Spec], error)
	CreateSpec(ctx context.Context, userID, serviceID uuid.UUID, name, content string) (apis.Spec, apis.Revision, error)
	GetSpec(ctx context.Context, userID, specID uuid.UUID) (apis.Spec, error)
	RenameSpec(ctx context.Context, userID, specID uuid.UUID, pre workspaces.Precondition, name string) (apis.Spec, error)
	DeleteSpec(ctx context.Context, userID, specID uuid.UUID, pre workspaces.Precondition) error
	ListRevisions(ctx context.Context, userID, specID uuid.UUID, p workspaces.PageRequest) (workspaces.Page[apis.Revision], error)
	CreateRevision(ctx context.Context, userID, specID uuid.UUID, pre workspaces.Precondition, content, msg string) (apis.Spec, apis.Revision, error)
	GetRevision(ctx context.Context, userID, revisionID uuid.UUID) (apis.Revision, error)
	RevisionContent(ctx context.Context, userID, revisionID uuid.UUID) (string, string, error)
	RevisionReport(ctx context.Context, userID, revisionID uuid.UUID) (specengine.Report, error)
	PublishVersion(ctx context.Context, userID, specID uuid.UUID, in apis.VersionInput) (apis.Version, error)
	ListVersions(ctx context.Context, userID, specID uuid.UUID, p workspaces.PageRequest) (workspaces.Page[apis.Version], error)
	GetVersion(ctx context.Context, userID, versionID uuid.UUID) (apis.Version, error)
}
