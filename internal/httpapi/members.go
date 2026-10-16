// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/CemTbi/Signalbox/internal/modules/identity"
	"github.com/CemTbi/Signalbox/internal/modules/workspaces"
	"github.com/CemTbi/Signalbox/internal/platform/id"
	"github.com/CemTbi/Signalbox/internal/platform/validate"
)

// MemberJSON is a workspace member in API responses.
type MemberJSON struct {
	User     UserJSON `json:"user"`
	Role     string   `json:"role"`
	JoinedAt string   `json:"joinedAt"`
}

func memberJSON(m workspaces.Member, u identity.User) MemberJSON {
	return MemberJSON{User: userJSON(u), Role: string(m.Role), JoinedAt: timestamp(m.JoinedAt)}
}

func (a *api) listMembers(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	wsID, ok := pathID(w, r, "workspaceId", id.Workspace, "workspace")
	if !ok {
		return
	}
	req, ok := pageRequest(w, r, id.User)
	if !ok {
		return
	}
	page, err := a.Workspaces.ListMembers(r.Context(), s.User.ID, wsID, req)
	if err != nil {
		a.workspaceError(w, r, "workspace", err)
		return
	}
	ids := make([]uuid.UUID, len(page.Items))
	for i, m := range page.Items {
		ids[i] = m.UserID
	}
	users, err := a.Identity.Users(r.Context(), ids)
	if err != nil {
		a.fail(w, r, "look up members", err)
		return
	}
	out := PageJSON[MemberJSON]{Data: make([]MemberJSON, 0, len(page.Items))}
	for _, m := range page.Items {
		if u, ok := users[m.UserID]; ok {
			out.Data = append(out.Data, memberJSON(m, u))
		}
	}
	if page.More && len(page.Items) > 0 {
		out.NextCursor = nextCursor(id.User, page.Items[len(page.Items)-1].UserID)
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *api) updateMember(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	wsID, ok := pathID(w, r, "workspaceId", id.Workspace, "workspace")
	if !ok {
		return
	}
	userID, ok := pathID(w, r, "userId", id.User, "member")
	if !ok {
		return
	}
	patch, ok := decodeMergePatch(w, r, "role")
	if !ok {
		return
	}
	var fields validate.Errors
	role := patch.str(&fields, "role", false)
	if len(fields) > 0 {
		writeInvalid(w, r, fields)
		return
	}
	m, err := a.Workspaces.ChangeRole(r.Context(), s.User.ID, wsID, userID, workspaces.Role(*role))
	if err != nil {
		a.workspaceError(w, r, "workspace", err)
		return
	}
	a.writeMember(w, r, http.StatusOK, m)
}

func (a *api) removeMember(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	wsID, ok := pathID(w, r, "workspaceId", id.Workspace, "workspace")
	if !ok {
		return
	}
	userID, ok := pathID(w, r, "userId", id.User, "member")
	if !ok {
		return
	}
	if err := a.Workspaces.RemoveMember(r.Context(), s.User.ID, wsID, userID); err != nil {
		a.workspaceError(w, r, "workspace", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) writeMember(w http.ResponseWriter, r *http.Request, status int, m workspaces.Member) {
	users, err := a.Identity.Users(r.Context(), []uuid.UUID{m.UserID})
	if err != nil {
		a.fail(w, r, "look up member", err)
		return
	}
	u, ok := users[m.UserID]
	if !ok {
		writeProblem(w, r, http.StatusNotFound, "not-found", "There is no member with this ID in the workspace.")
		return
	}
	writeJSON(w, status, memberJSON(m, u))
}
