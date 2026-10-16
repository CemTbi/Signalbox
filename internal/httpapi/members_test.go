// SPDX-License-Identifier: Apache-2.0

package httpapi_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/CemTbi/Signalbox/internal/httpapi"
	"github.com/CemTbi/Signalbox/internal/modules/identity"
)

// newWorkspace creates a workspace as the signed-in user.
func (h *harness) newWorkspace(name, slug string) httpapi.WorkspaceJSON {
	h.t.Helper()
	var ws httpapi.WorkspaceJSON
	r := h.do(call{method: http.MethodPost, path: "/api/v1/workspaces", body: map[string]string{"name": name, "slug": slug}})
	r.expect(h.t, http.StatusCreated, "")
	r.decode(h.t, &ws)
	return ws
}

// invite creates an invitation as the signed-in user and returns it with
// the token from its link.
func (h *harness) invite(workspaceID, email, role string) (httpapi.NewInvitationJSON, string) {
	h.t.Helper()
	r := h.do(call{method: http.MethodPost, path: "/api/v1/workspaces/" + workspaceID + "/invitations",
		body: map[string]string{"email": email, "role": role}})
	r.expect(h.t, http.StatusCreated, "")
	var inv httpapi.NewInvitationJSON
	r.decode(h.t, &inv)
	_, token, found := strings.Cut(inv.URL, "/invite#")
	if !found || !strings.HasPrefix(inv.URL, h.srv.URL) {
		h.t.Fatalf("invitation URL %q isn't %s/invite#<token>", inv.URL, h.srv.URL)
	}
	return inv, token
}

func (h *harness) createUser(name, email string) {
	h.t.Helper()
	if _, err := h.ids.CreateUser(context.Background(), identity.UserInput{Name: name, Email: email, Password: password}); err != nil {
		h.t.Fatal(err)
	}
}

func TestJoiningThroughAnInvitationWithANewAccount(t *testing.T) {
	h := newHarness(t)
	h.setUp()
	ws := h.newWorkspace("Payments", "payments")

	inv, token := h.invite(ws.ID, "grace@example.com", "editor")
	if inv.Email != "grace@example.com" || inv.Role != "editor" || !strings.HasPrefix(inv.ID, "inv_") || inv.InvitedBy == "" {
		t.Errorf("invitation = %+v", inv)
	}
	var open httpapi.PageJSON[httpapi.InvitationJSON]
	h.do(call{method: http.MethodGet, path: "/api/v1/workspaces/" + ws.ID + "/invitations"}).decode(t, &open)
	if len(open.Data) != 1 || open.Data[0] != inv.InvitationJSON {
		t.Errorf("open invitations = %+v", open)
	}

	// Grace opens the link in her own browser.
	h.signOutLocally()
	var details httpapi.InvitationDetailsJSON
	r := h.do(call{method: http.MethodPost, path: "/api/v1/invitations/lookup", body: map[string]string{"token": token}})
	r.expect(t, http.StatusOK, "")
	r.decode(t, &details)
	if details.Workspace.Name != "Payments" || details.InvitedBy != "Ada Lovelace" || details.AccountExists || details.Role != "editor" {
		t.Errorf("details = %+v", details)
	}

	r = h.do(call{method: http.MethodPost, path: "/api/v1/invitations/sign-up",
		body: map[string]string{"token": token, "name": "Grace Hopper", "password": "short"}, invalid: true})
	r.expect(t, http.StatusBadRequest, "validation-failed")

	var session httpapi.SessionJSON
	r = h.do(call{method: http.MethodPost, path: "/api/v1/invitations/sign-up",
		body: map[string]string{"token": token, "name": "Grace Hopper", "password": password}})
	r.expect(t, http.StatusCreated, "")
	r.decode(t, &session)
	h.csrf = session.CSRFToken
	if session.User.Email != "grace@example.com" || session.User.Name != "Grace Hopper" {
		t.Errorf("session = %+v", session)
	}

	var joined httpapi.WorkspaceJSON
	h.do(call{method: http.MethodGet, path: "/api/v1/workspaces/" + ws.ID}).decode(t, &joined)
	if joined.Role != "editor" {
		t.Errorf("Grace's role = %q, want editor", joined.Role)
	}
	// The link works once, and editors can't invite.
	h.do(call{method: http.MethodPost, path: "/api/v1/invitations/lookup", body: map[string]string{"token": token}}).
		expect(t, http.StatusNotFound, "invitation-not-found")
	h.do(call{method: http.MethodPost, path: "/api/v1/workspaces/" + ws.ID + "/invitations",
		body: map[string]string{"email": "x@example.com", "role": "viewer"}}).expect(t, http.StatusForbidden, "permission-denied")
}

func TestAcceptingAnInvitationWithAnExistingAccount(t *testing.T) {
	h := newHarness(t)
	h.setUp()
	ws := h.newWorkspace("Payments", "payments")
	h.createUser("Grace Hopper", "grace@example.com")
	_, token := h.invite(ws.ID, "grace@example.com", "viewer")
	_, othersToken := h.invite(ws.ID, "someone@example.com", "viewer")

	var details httpapi.InvitationDetailsJSON
	h.do(call{method: http.MethodPost, path: "/api/v1/invitations/lookup", body: map[string]string{"token": token}}).decode(t, &details)
	if !details.AccountExists {
		t.Error("accountExists is false for an address with an account")
	}
	// Someone with an account can't create a second one from the link.
	h.signOutLocally()
	h.do(call{method: http.MethodPost, path: "/api/v1/invitations/sign-up",
		body: map[string]string{"token": token, "name": "Grace", "password": password}}).expect(t, http.StatusConflict, "account-exists")

	h.signIn("grace@example.com")
	h.do(call{method: http.MethodPost, path: "/api/v1/invitations/accept", body: map[string]string{"token": othersToken}}).
		expect(t, http.StatusForbidden, "invitation-email-mismatch")
	var joined httpapi.WorkspaceJSON
	r := h.do(call{method: http.MethodPost, path: "/api/v1/invitations/accept", body: map[string]string{"token": token}})
	r.expect(t, http.StatusOK, "")
	r.decode(t, &joined)
	if joined.ID != ws.ID || joined.Role != "viewer" {
		t.Errorf("joined = %+v", joined)
	}
	h.do(call{method: http.MethodPost, path: "/api/v1/invitations/accept", body: map[string]string{"token": "not-a-token"}}).
		expect(t, http.StatusNotFound, "invitation-not-found")
}

func TestInvitationRules(t *testing.T) {
	h := newHarness(t)
	h.setUp()
	ws := h.newWorkspace("Payments", "payments")

	h.do(call{method: http.MethodPost, path: "/api/v1/workspaces/" + ws.ID + "/invitations",
		body: map[string]string{"email": "ADA@example.com", "role": "viewer"}}).expect(t, http.StatusConflict, "already-member")
	r := h.do(call{method: http.MethodPost, path: "/api/v1/workspaces/" + ws.ID + "/invitations",
		body: map[string]string{"email": "not-an-email", "role": "superuser"}, invalid: true})
	r.expect(t, http.StatusBadRequest, "validation-failed")

	inv, token := h.invite(ws.ID, "grace@example.com", "editor")
	h.do(call{method: http.MethodDelete, path: "/api/v1/invitations/" + inv.ID}).expect(t, http.StatusNoContent, "")
	h.do(call{method: http.MethodPost, path: "/api/v1/invitations/lookup", body: map[string]string{"token": token}}).
		expect(t, http.StatusNotFound, "invitation-not-found")
	h.do(call{method: http.MethodDelete, path: "/api/v1/invitations/" + inv.ID}).expect(t, http.StatusNotFound, "invitation-not-found")

	// Reading a link from another site is refused like any unsafe request.
	h.do(call{method: http.MethodPost, path: "/api/v1/invitations/lookup", headers: map[string]string{"Origin": "https://evil.example"},
		body: map[string]string{"token": token}}).expect(t, http.StatusForbidden, "origin-mismatch")
}

func TestManagingMembers(t *testing.T) {
	h := newHarness(t)
	ada := h.setUp()
	ws := h.newWorkspace("Payments", "payments")
	h.createUser("Grace Hopper", "grace@example.com")
	_, token := h.invite(ws.ID, "grace@example.com", "editor")
	adaCSRF := h.csrf

	// Grace joins in her own browser; then Ada manages her.
	adaJar := h.client.Jar
	h.signOutLocally()
	grace := h.signIn("grace@example.com")
	h.do(call{method: http.MethodPost, path: "/api/v1/invitations/accept", body: map[string]string{"token": token}}).expect(t, http.StatusOK, "")
	h.client.Jar, h.csrf = adaJar, adaCSRF

	var members httpapi.PageJSON[httpapi.MemberJSON]
	h.do(call{method: http.MethodGet, path: "/api/v1/workspaces/" + ws.ID + "/members"}).decode(t, &members)
	roles := map[string]string{}
	for _, m := range members.Data {
		roles[m.User.Email] = m.Role
	}
	if len(roles) != 2 || roles["ada@example.com"] != "owner" || roles["grace@example.com"] != "editor" {
		t.Fatalf("members = %+v", members.Data)
	}

	graceURL := "/api/v1/workspaces/" + ws.ID + "/members/" + grace.User.ID
	var m httpapi.MemberJSON
	r := h.do(call{method: http.MethodPatch, path: graceURL, contentType: "application/merge-patch+json", body: map[string]string{"role": "admin"}})
	r.expect(t, http.StatusOK, "")
	r.decode(t, &m)
	if m.Role != "admin" || m.User.ID != grace.User.ID {
		t.Errorf("updated member = %+v", m)
	}

	adaURL := "/api/v1/workspaces/" + ws.ID + "/members/" + ada.User.ID
	h.do(call{method: http.MethodPatch, path: adaURL, contentType: "application/merge-patch+json", body: map[string]string{"role": "viewer"}}).
		expect(t, http.StatusConflict, "last-owner")
	h.do(call{method: http.MethodDelete, path: adaURL}).expect(t, http.StatusConflict, "last-owner")
	h.do(call{method: http.MethodPatch, path: graceURL, contentType: "application/merge-patch+json", body: map[string]string{"role": "chief"}, invalid: true}).
		expect(t, http.StatusBadRequest, "validation-failed")

	h.do(call{method: http.MethodDelete, path: graceURL}).expect(t, http.StatusNoContent, "")
	h.do(call{method: http.MethodDelete, path: graceURL}).expect(t, http.StatusNotFound, "not-found")
	h.do(call{method: http.MethodGet, path: "/api/v1/workspaces/" + ws.ID + "/members"}).decode(t, &members)
	if len(members.Data) != 1 {
		t.Errorf("%d members after removing Grace, want 1", len(members.Data))
	}
}
