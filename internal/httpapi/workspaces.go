// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/CemTbi/Signalbox/internal/modules/identity"
	"github.com/CemTbi/Signalbox/internal/modules/workspaces"
	"github.com/CemTbi/Signalbox/internal/platform/id"
	"github.com/CemTbi/Signalbox/internal/platform/validate"
)

// WorkspaceJSON is a workspace in API responses.
type WorkspaceJSON struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	Role      string `json:"role"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// ProjectJSON is a project in API responses.
type ProjectJSON struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspaceId"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
}

// PageJSON is one page of a list.
type PageJSON[T any] struct {
	Data       []T    `json:"data"`
	NextCursor string `json:"nextCursor,omitempty"`
}

func workspaceJSON(w workspaces.Workspace) WorkspaceJSON {
	return WorkspaceJSON{
		ID: id.Format(id.Workspace, w.ID), Name: w.Name, Slug: w.Slug, Role: string(w.Role),
		CreatedAt: timestamp(w.CreatedAt), UpdatedAt: timestamp(w.UpdatedAt),
	}
}

func projectJSON(p workspaces.Project) ProjectJSON {
	return ProjectJSON{
		ID: id.Format(id.Project, p.ID), WorkspaceID: id.Format(id.Workspace, p.WorkspaceID),
		Name: p.Name, Description: p.Description,
		CreatedAt: timestamp(p.CreatedAt), UpdatedAt: timestamp(p.UpdatedAt),
	}
}

// pageJSON converts a page of results and adds the cursor for the next one.
func pageJSON[T, J any](p workspaces.Page[T], prefix id.Prefix, idOf func(T) uuid.UUID, conv func(T) J) PageJSON[J] {
	out := PageJSON[J]{Data: make([]J, 0, len(p.Items))}
	for _, item := range p.Items {
		out.Data = append(out.Data, conv(item))
	}
	if p.More && len(p.Items) > 0 {
		out.NextCursor = nextCursor(prefix, idOf(p.Items[len(p.Items)-1]))
	}
	return out
}

// pathID reads an ID from the path. A malformed ID can't name anything, so
// it is a 404 like any other unknown ID.
func pathID(w http.ResponseWriter, r *http.Request, name string, prefix id.Prefix, what string) (uuid.UUID, bool) {
	u, err := id.Parse(prefix, r.PathValue(name))
	if err != nil {
		writeProblem(w, r, http.StatusNotFound, "not-found", "There is no "+what+" with this ID.")
		return uuid.Nil, false
	}
	return u, true
}

// workspaceError answers for an error from the workspaces module.
func (a *api) workspaceError(w http.ResponseWriter, r *http.Request, what string, err error) {
	var fields validate.Errors
	switch {
	case errors.As(err, &fields):
		writeInvalid(w, r, fields)
	case errors.Is(err, workspaces.ErrNotFound):
		writeProblem(w, r, http.StatusNotFound, "not-found", "There is no "+what+" with this ID.")
	case errors.Is(err, workspaces.ErrForbidden):
		writeProblem(w, r, http.StatusForbidden, "permission-denied",
			"Your role in this workspace doesn't allow this. Ask a workspace owner or admin.")
	case errors.Is(err, workspaces.ErrSlugTaken):
		writeProblem(w, r, http.StatusConflict, "slug-taken", "Another workspace already uses this slug. Choose another.")
	case errors.Is(err, workspaces.ErrVersionMismatch):
		writeProblem(w, r, http.StatusPreconditionFailed, "edit-conflict",
			"Someone changed this "+what+" since you loaded it. Load it again and reapply your change.")
	case errors.Is(err, workspaces.ErrMemberNotFound):
		writeProblem(w, r, http.StatusNotFound, "not-found", "There is no member with this ID in the workspace.")
	case errors.Is(err, workspaces.ErrLastOwner):
		writeProblem(w, r, http.StatusConflict, "last-owner",
			"A workspace needs at least one owner. Make someone else an owner first.")
	case errors.Is(err, workspaces.ErrInvitationNotFound):
		writeProblem(w, r, http.StatusNotFound, "invitation-not-found",
			"This invitation link is wrong, expired or already used. Ask for a new one.")
	default:
		a.fail(w, r, what, err)
	}
}

func (a *api) listWorkspaces(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	req, ok := pageRequest(w, r, id.Workspace)
	if !ok {
		return
	}
	page, err := a.Workspaces.ListWorkspaces(r.Context(), s.User.ID, req)
	if err != nil {
		a.workspaceError(w, r, "workspace", err)
		return
	}
	writeJSON(w, http.StatusOK, pageJSON(page, id.Workspace,
		func(ws workspaces.Workspace) uuid.UUID { return ws.ID }, workspaceJSON))
}

func (a *api) createWorkspace(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	var body struct {
		Name *string `json:"name"`
		Slug *string `json:"slug"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	var fields validate.Errors
	in := workspaces.WorkspaceInput{Name: required(&fields, "/name", body.Name), Slug: required(&fields, "/slug", body.Slug)}
	if len(fields) > 0 {
		writeInvalid(w, r, fields)
		return
	}
	ws, err := a.Workspaces.CreateWorkspace(r.Context(), s.User.ID, in)
	if err != nil {
		a.workspaceError(w, r, "workspace", err)
		return
	}
	out := workspaceJSON(ws)
	w.Header().Set("Location", "/api/v1/workspaces/"+out.ID)
	w.Header().Set("ETag", etag(ws.Version))
	writeJSON(w, http.StatusCreated, out)
}

func (a *api) getWorkspace(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	wsID, ok := pathID(w, r, "workspaceId", id.Workspace, "workspace")
	if !ok {
		return
	}
	ws, err := a.Workspaces.GetWorkspace(r.Context(), s.User.ID, wsID)
	if err != nil {
		a.workspaceError(w, r, "workspace", err)
		return
	}
	w.Header().Set("ETag", etag(ws.Version))
	writeJSON(w, http.StatusOK, workspaceJSON(ws))
}

func (a *api) updateWorkspace(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	wsID, ok := pathID(w, r, "workspaceId", id.Workspace, "workspace")
	if !ok {
		return
	}
	patch, ok := decodeMergePatch(w, r, "name", "slug")
	if !ok {
		return
	}
	var fields validate.Errors
	change := workspaces.WorkspacePatch{Name: patch.str(&fields, "name", false), Slug: patch.str(&fields, "slug", false)}
	if len(fields) > 0 {
		writeInvalid(w, r, fields)
		return
	}
	ws, err := a.Workspaces.UpdateWorkspace(r.Context(), s.User.ID, wsID, precondition(r), change)
	if err != nil {
		a.workspaceError(w, r, "workspace", err)
		return
	}
	w.Header().Set("ETag", etag(ws.Version))
	writeJSON(w, http.StatusOK, workspaceJSON(ws))
}

func (a *api) deleteWorkspace(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	wsID, ok := pathID(w, r, "workspaceId", id.Workspace, "workspace")
	if !ok {
		return
	}
	if err := a.Workspaces.DeleteWorkspace(r.Context(), s.User.ID, wsID, precondition(r)); err != nil {
		a.workspaceError(w, r, "workspace", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) listProjects(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	wsID, ok := pathID(w, r, "workspaceId", id.Workspace, "workspace")
	if !ok {
		return
	}
	req, ok := pageRequest(w, r, id.Project)
	if !ok {
		return
	}
	page, err := a.Workspaces.ListProjects(r.Context(), s.User.ID, wsID, req)
	if err != nil {
		a.workspaceError(w, r, "workspace", err)
		return
	}
	writeJSON(w, http.StatusOK, pageJSON(page, id.Project,
		func(p workspaces.Project) uuid.UUID { return p.ID }, projectJSON))
}

func (a *api) createProject(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	wsID, ok := pathID(w, r, "workspaceId", id.Workspace, "workspace")
	if !ok {
		return
	}
	var body struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	var fields validate.Errors
	in := workspaces.ProjectInput{Name: required(&fields, "/name", body.Name)}
	if body.Description != nil {
		in.Description = *body.Description
	}
	if len(fields) > 0 {
		writeInvalid(w, r, fields)
		return
	}
	p, err := a.Workspaces.CreateProject(r.Context(), s.User.ID, wsID, in)
	if err != nil {
		a.workspaceError(w, r, "workspace", err)
		return
	}
	out := projectJSON(p)
	w.Header().Set("Location", "/api/v1/projects/"+out.ID)
	w.Header().Set("ETag", etag(p.Version))
	writeJSON(w, http.StatusCreated, out)
}

func (a *api) getProject(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	prjID, ok := pathID(w, r, "projectId", id.Project, "project")
	if !ok {
		return
	}
	p, err := a.Workspaces.GetProject(r.Context(), s.User.ID, prjID)
	if err != nil {
		a.workspaceError(w, r, "project", err)
		return
	}
	w.Header().Set("ETag", etag(p.Version))
	writeJSON(w, http.StatusOK, projectJSON(p))
}

func (a *api) updateProject(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	prjID, ok := pathID(w, r, "projectId", id.Project, "project")
	if !ok {
		return
	}
	patch, ok := decodeMergePatch(w, r, "name", "description")
	if !ok {
		return
	}
	var fields validate.Errors
	change := workspaces.ProjectPatch{Name: patch.str(&fields, "name", false), Description: patch.str(&fields, "description", true)}
	if len(fields) > 0 {
		writeInvalid(w, r, fields)
		return
	}
	p, err := a.Workspaces.UpdateProject(r.Context(), s.User.ID, prjID, precondition(r), change)
	if err != nil {
		a.workspaceError(w, r, "project", err)
		return
	}
	w.Header().Set("ETag", etag(p.Version))
	writeJSON(w, http.StatusOK, projectJSON(p))
}

func (a *api) deleteProject(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	prjID, ok := pathID(w, r, "projectId", id.Project, "project")
	if !ok {
		return
	}
	if err := a.Workspaces.DeleteProject(r.Context(), s.User.ID, prjID, precondition(r)); err != nil {
		a.workspaceError(w, r, "project", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
