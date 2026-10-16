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

// InvitationJSON is an open invitation in API responses.
type InvitationJSON struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspaceId"`
	Email       string `json:"email"`
	Role        string `json:"role"`
	InvitedBy   string `json:"invitedBy,omitempty"`
	CreatedAt   string `json:"createdAt"`
	ExpiresAt   string `json:"expiresAt"`
}

// NewInvitationJSON is an invitation that was just created, with its link.
type NewInvitationJSON struct {
	InvitationJSON
	URL string `json:"url"`
}

// WorkspaceSummaryJSON names a workspace for someone who isn't a member.
type WorkspaceSummaryJSON struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// InvitationDetailsJSON is what an invitation link offers.
type InvitationDetailsJSON struct {
	Workspace     WorkspaceSummaryJSON `json:"workspace"`
	Email         string               `json:"email"`
	Role          string               `json:"role"`
	InvitedBy     string               `json:"invitedBy,omitempty"`
	ExpiresAt     string               `json:"expiresAt"`
	AccountExists bool                 `json:"accountExists"`
}

func invitationJSON(inv workspaces.Invitation) InvitationJSON {
	out := InvitationJSON{
		ID: id.Format(id.Invitation, inv.ID), WorkspaceID: id.Format(id.Workspace, inv.WorkspaceID),
		Email: inv.Email, Role: string(inv.Role),
		CreatedAt: timestamp(inv.CreatedAt), ExpiresAt: timestamp(inv.ExpiresAt),
	}
	if inv.InvitedBy != uuid.Nil {
		out.InvitedBy = id.Format(id.User, inv.InvitedBy)
	}
	return out
}

// invitationURL is the link that opens the invitation page. The token goes
// after "#", which browsers never send to servers, so it can't end up in
// access logs or proxies.
func (a *api) invitationURL(token string) string {
	return a.PublicURL.JoinPath("invite").String() + "#" + token
}

func (a *api) listInvitations(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	wsID, ok := pathID(w, r, "workspaceId", id.Workspace, "workspace")
	if !ok {
		return
	}
	req, ok := pageRequest(w, r, id.Invitation)
	if !ok {
		return
	}
	page, err := a.Workspaces.ListInvitations(r.Context(), s.User.ID, wsID, req)
	if err != nil {
		a.workspaceError(w, r, "workspace", err)
		return
	}
	writeJSON(w, http.StatusOK, pageJSON(page, id.Invitation,
		func(inv workspaces.Invitation) uuid.UUID { return inv.ID }, invitationJSON))
}

func (a *api) createInvitation(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	wsID, ok := pathID(w, r, "workspaceId", id.Workspace, "workspace")
	if !ok {
		return
	}
	var body struct {
		Email *string `json:"email"`
		Role  *string `json:"role"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	var fields validate.Errors
	in := workspaces.InvitationInput{
		Email: required(&fields, "/email", body.Email),
		Role:  workspaces.Role(required(&fields, "/role", body.Role)),
	}
	if len(fields) > 0 {
		writeInvalid(w, r, fields)
		return
	}

	// Tell the inviter when the person already belongs. Check the
	// inviter's own access first, so this can't reveal who is a member to
	// someone who isn't allowed to manage members.
	ws, err := a.Workspaces.GetWorkspace(r.Context(), s.User.ID, wsID)
	if err != nil {
		a.workspaceError(w, r, "workspace", err)
		return
	}
	if ws.Role == workspaces.Owner || ws.Role == workspaces.Admin {
		u, exists, err := a.Identity.UserByEmail(r.Context(), in.Email)
		if err != nil {
			a.fail(w, r, "look up invitee", err)
			return
		}
		if exists {
			if _, member, err := a.Workspaces.MemberRole(r.Context(), wsID, u.ID); err != nil {
				a.fail(w, r, "look up invitee", err)
				return
			} else if member {
				writeProblem(w, r, http.StatusConflict, "already-member", u.Email+" is already a member of this workspace.")
				return
			}
		}
	}

	inv, err := a.Workspaces.Invite(r.Context(), s.User.ID, wsID, in)
	if err != nil {
		a.workspaceError(w, r, "workspace", err)
		return
	}
	writeJSON(w, http.StatusCreated, NewInvitationJSON{InvitationJSON: invitationJSON(inv.Invitation), URL: a.invitationURL(inv.Token)})
}

func (a *api) revokeInvitation(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	invID, ok := pathID(w, r, "invitationId", id.Invitation, "invitation")
	if !ok {
		return
	}
	if err := a.Workspaces.RevokeInvitation(r.Context(), s.User.ID, invID); err != nil {
		a.workspaceError(w, r, "invitation", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// readInvitationToken checks the origin and rate limit of a request that
// carries an invitation token without a session, and reads the token.
func (a *api) readInvitationToken(w http.ResponseWriter, r *http.Request, body any, token func() *string, more ...func(*validate.Errors)) bool {
	if !a.checkOrigin(w, r) {
		return false
	}
	if ok, retry := a.invitationsByIP.Allow(clientIP(r, a.TrustedProxies).String()); !ok {
		writeRateLimited(w, r, retry)
		return false
	}
	if !decodeJSON(w, r, body) {
		return false
	}
	var fields validate.Errors
	required(&fields, "/token", token())
	for _, check := range more {
		check(&fields)
	}
	if len(fields) > 0 {
		writeInvalid(w, r, fields)
		return false
	}
	return true
}

func (a *api) lookUpInvitation(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token *string `json:"token"`
	}
	if !a.readInvitationToken(w, r, &body, func() *string { return body.Token }) {
		return
	}
	d, err := a.Workspaces.LookupInvitation(r.Context(), *body.Token)
	if err != nil {
		a.workspaceError(w, r, "invitation", err)
		return
	}
	out := InvitationDetailsJSON{
		Workspace: WorkspaceSummaryJSON{ID: id.Format(id.Workspace, d.WorkspaceID), Name: d.WorkspaceName},
		Email:     d.Email, Role: string(d.Role), ExpiresAt: timestamp(d.ExpiresAt),
	}
	if d.InvitedBy != uuid.Nil {
		users, err := a.Identity.Users(r.Context(), []uuid.UUID{d.InvitedBy})
		if err != nil {
			a.fail(w, r, "look up inviter", err)
			return
		}
		out.InvitedBy = users[d.InvitedBy].Name
	}
	if _, out.AccountExists, err = a.Identity.UserByEmail(r.Context(), d.Email); err != nil {
		a.fail(w, r, "look up invitee", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *api) acceptInvitation(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	var body struct {
		Token *string `json:"token"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	var fields validate.Errors
	secret := required(&fields, "/token", body.Token)
	if len(fields) > 0 {
		writeInvalid(w, r, fields)
		return
	}
	ws, err := a.Workspaces.AcceptInvitation(r.Context(), secret, s.User.ID, s.User.Email)
	if errors.Is(err, workspaces.ErrEmailMismatch) {
		writeProblem(w, r, http.StatusForbidden, "invitation-email-mismatch",
			"This invitation was sent to another email address than "+s.User.Email+
				". Sign out and open the link again, or ask for an invitation to your address.")
		return
	}
	if err != nil {
		a.workspaceError(w, r, "invitation", err)
		return
	}
	writeJSON(w, http.StatusOK, workspaceJSON(ws))
}

func (a *api) signUpWithInvitation(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token    *string `json:"token"`
		Name     *string `json:"name"`
		Password *string `json:"password"`
	}
	if !a.readInvitationToken(w, r, &body, func() *string { return body.Token }, func(fields *validate.Errors) {
		required(fields, "/name", body.Name)
		required(fields, "/password", body.Password)
	}) {
		return
	}
	d, err := a.Workspaces.LookupInvitation(r.Context(), *body.Token)
	if err != nil {
		a.workspaceError(w, r, "invitation", err)
		return
	}
	ns, err := a.Identity.CreateUserAndSignIn(r.Context(),
		identity.UserInput{Name: *body.Name, Email: d.Email, Password: *body.Password}, a.client(r))
	var fields validate.Errors
	switch {
	case errors.Is(err, identity.ErrEmailTaken):
		writeProblem(w, r, http.StatusConflict, "account-exists",
			"An account with this email address exists. Sign in, then open the invitation link again.")
		return
	case errors.As(err, &fields):
		writeInvalid(w, r, fields)
		return
	case err != nil:
		a.fail(w, r, "sign up with an invitation", err)
		return
	}
	// The account exists now. If the invitation was revoked or used in the
	// meantime, the person can still sign in with it, but isn't a member.
	if _, err := a.Workspaces.AcceptInvitation(r.Context(), *body.Token, ns.User.ID, ns.User.Email); err != nil {
		_ = a.Identity.SignOut(r.Context(), ns.Token)
		a.workspaceError(w, r, "invitation", err)
		return
	}
	a.cookie.set(w, ns.Token, ns.ExpiresAt)
	writeJSON(w, http.StatusCreated, sessionJSON(ns.Session))
}
