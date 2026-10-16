// SPDX-License-Identifier: Apache-2.0

package httpapi_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/CemTbi/Signalbox/internal/httpapi"
	"github.com/CemTbi/Signalbox/internal/modules/identity"
)

func TestFirstRunSetup(t *testing.T) {
	h := newHarness(t)

	var status httpapi.SetupStatusJSON
	r := h.do(call{method: http.MethodGet, path: "/api/v1/setup"})
	r.expect(t, http.StatusOK, "")
	if r.decode(t, &status); !status.Required {
		t.Fatal("a new installation should need setup")
	}
	h.do(call{method: http.MethodGet, path: "/api/v1/sessions/current"}).expect(t, http.StatusUnauthorized, "unauthenticated")

	h.do(call{method: http.MethodPost, path: "/api/v1/setup", body: map[string]string{
		"setupCode": "WRONG-CODE-0000", "name": "Mallory", "email": "m@example.com", "password": password,
	}}).expect(t, http.StatusForbidden, "invalid-setup-code")

	r = h.do(call{method: http.MethodPost, path: "/api/v1/setup", body: map[string]string{"name": "Ada"}, invalid: true})
	r.expect(t, http.StatusBadRequest, "validation-failed")
	var p httpapi.Problem
	r.decode(t, &p)
	if got := pointers(p); got != "/setupCode /email /password" {
		t.Errorf("missing fields = %s", got)
	}

	s := h.setUp()
	if s.User.Name != "Ada Lovelace" || !strings.HasPrefix(s.User.ID, "usr_") || len(s.CSRFToken) != 43 {
		t.Errorf("session = %+v", s)
	}

	r = h.do(call{method: http.MethodGet, path: "/api/v1/setup"})
	if r.decode(t, &status); status.Required {
		t.Error("setup is still required after it completed")
	}
	h.do(call{method: http.MethodPost, path: "/api/v1/setup", body: map[string]string{
		"setupCode": setupCode, "name": "Mallory", "email": "m@example.com", "password": password,
	}}).expect(t, http.StatusConflict, "setup-complete")

	var me httpapi.UserJSON
	r = h.do(call{method: http.MethodGet, path: "/api/v1/me"})
	r.expect(t, http.StatusOK, "")
	if r.decode(t, &me); me.ID != s.User.ID || me.Email != "ada@example.com" {
		t.Errorf("me = %+v", me)
	}
	var current httpapi.SessionJSON
	r = h.do(call{method: http.MethodGet, path: "/api/v1/sessions/current"})
	if r.decode(t, &current); current.CSRFToken != s.CSRFToken || current.User != s.User {
		t.Errorf("current session = %+v, want %+v", current, s)
	}
}

func TestSessionCookieIsLockedDown(t *testing.T) {
	h := newHarness(t)
	r := h.do(call{method: http.MethodPost, path: "/api/v1/setup", body: map[string]string{
		"setupCode": setupCode, "name": "Ada", "email": "ada@example.com", "password": password,
	}})
	r.expect(t, http.StatusCreated, "")
	cookie := r.header.Get("Set-Cookie")
	for _, want := range []string{"__Host-sbx_session=", "Path=/", "HttpOnly", "Secure", "SameSite=Lax", "Max-Age="} {
		if !strings.Contains(cookie, want) {
			t.Errorf("Set-Cookie %q lacks %q", cookie, want)
		}
	}
	if strings.Contains(cookie, "Domain=") {
		t.Errorf("Set-Cookie %q has a Domain, which __Host- cookies must not", cookie)
	}
}

func TestSignInAndOut(t *testing.T) {
	h := newHarness(t)
	first := h.setUp()

	r := h.do(call{method: http.MethodDelete, path: "/api/v1/sessions/current"})
	r.expect(t, http.StatusNoContent, "")
	if c := r.header.Get("Set-Cookie"); !strings.Contains(c, "Max-Age=0") {
		t.Errorf("signing out doesn't clear the cookie: %q", c)
	}
	h.do(call{method: http.MethodGet, path: "/api/v1/me"}).expect(t, http.StatusUnauthorized, "unauthenticated")

	h.do(call{method: http.MethodPost, path: "/api/v1/sessions", body: map[string]string{
		"email": "ada@example.com", "password": "not the password",
	}}).expect(t, http.StatusUnauthorized, "invalid-credentials")
	h.do(call{method: http.MethodPost, path: "/api/v1/sessions", body: map[string]string{
		"email": "nobody@example.com", "password": password,
	}}).expect(t, http.StatusUnauthorized, "invalid-credentials")

	second := h.signIn("ADA@example.com")
	if second.User.ID != first.User.ID || second.CSRFToken == first.CSRFToken {
		t.Errorf("signed in as %+v, want the same user with a new session", second)
	}
	h.do(call{method: http.MethodGet, path: "/api/v1/me"}).expect(t, http.StatusOK, "")
}

func TestUnsafeRequestsNeedTheRightOriginAndCSRFToken(t *testing.T) {
	h := newHarness(t)
	h.setUp()
	create := func(headers map[string]string) result {
		return h.do(call{method: http.MethodPost, path: "/api/v1/workspaces",
			body: map[string]string{"name": "Payments", "slug": "payments"}, headers: headers})
	}

	create(map[string]string{httpapi.CSRFHeader: ""}).expect(t, http.StatusForbidden, "csrf-failed")
	create(map[string]string{httpapi.CSRFHeader: "guessed-token"}).expect(t, http.StatusForbidden, "csrf-failed")
	create(map[string]string{"Origin": "https://evil.example"}).expect(t, http.StatusForbidden, "origin-mismatch")
	create(map[string]string{"Origin": ""}).expect(t, http.StatusForbidden, "origin-mismatch")
	// Browsers that leave out Origin on same-origin requests send this.
	create(map[string]string{"Origin": "", "Sec-Fetch-Site": "same-origin"}).expect(t, http.StatusCreated, "")

	// Signing in from another site is refused too (login CSRF).
	h.do(call{method: http.MethodPost, path: "/api/v1/sessions", headers: map[string]string{"Origin": "https://evil.example"},
		body: map[string]string{"email": "ada@example.com", "password": password}}).expect(t, http.StatusForbidden, "origin-mismatch")

	// Reads need no token.
	h.csrf = ""
	h.do(call{method: http.MethodGet, path: "/api/v1/workspaces"}).expect(t, http.StatusOK, "")
}

func TestEndpointsNeedASession(t *testing.T) {
	h := newHarness(t)
	for _, c := range []call{
		{method: http.MethodGet, path: "/api/v1/me"},
		{method: http.MethodGet, path: "/api/v1/workspaces"},
		{method: http.MethodPost, path: "/api/v1/workspaces", body: map[string]string{"name": "X", "slug": "x"}},
		{method: http.MethodGet, path: "/api/v1/projects/prj_01j9z3m2p4r6t8v0x2z4b6d8f0"},
		{method: http.MethodPost, path: "/api/v1/spec-checks", body: "openapi: 3.1.0", contentType: "application/yaml"},
	} {
		h.do(c).expect(t, http.StatusUnauthorized, "unauthenticated")
	}
	h.do(call{method: http.MethodGet, path: "/api/v1/me", headers: map[string]string{"Authorization": "Bearer sbx_pat_x"}}).
		expect(t, http.StatusUnauthorized, "unauthenticated")
}

func TestWorkspaceLifecycle(t *testing.T) {
	h := newHarness(t)
	h.setUp()

	r := h.do(call{method: http.MethodPost, path: "/api/v1/workspaces", body: map[string]string{"name": "Payments", "slug": "payments"}})
	r.expect(t, http.StatusCreated, "")
	var ws httpapi.WorkspaceJSON
	r.decode(t, &ws)
	if ws.Role != "owner" || r.header.Get("ETag") != `"1"` || r.header.Get("Location") != "/api/v1/workspaces/"+ws.ID {
		t.Errorf("created %+v with ETag %s and Location %s", ws, r.header.Get("ETag"), r.header.Get("Location"))
	}

	h.do(call{method: http.MethodPost, path: "/api/v1/workspaces", body: map[string]string{"name": "Other", "slug": "payments"}}).
		expect(t, http.StatusConflict, "slug-taken")

	var page httpapi.PageJSON[httpapi.WorkspaceJSON]
	h.do(call{method: http.MethodGet, path: "/api/v1/workspaces"}).decode(t, &page)
	if len(page.Data) != 1 || page.Data[0] != ws || page.NextCursor != "" {
		t.Errorf("list = %+v", page)
	}

	path := "/api/v1/workspaces/" + ws.ID
	r = h.do(call{method: http.MethodGet, path: path})
	r.expect(t, http.StatusOK, "")
	if r.header.Get("ETag") != `"1"` {
		t.Errorf("ETag = %s", r.header.Get("ETag"))
	}

	r = h.do(call{method: http.MethodPatch, path: path, contentType: "application/merge-patch+json",
		headers: map[string]string{"If-Match": `"1"`}, body: map[string]string{"name": "Payments platform"}})
	r.expect(t, http.StatusOK, "")
	var updated httpapi.WorkspaceJSON
	r.decode(t, &updated)
	if updated.Name != "Payments platform" || updated.Slug != "payments" || r.header.Get("ETag") != `"2"` {
		t.Errorf("updated %+v, ETag %s", updated, r.header.Get("ETag"))
	}

	h.do(call{method: http.MethodPatch, path: path, contentType: "application/merge-patch+json",
		headers: map[string]string{"If-Match": `"1"`}, body: map[string]string{"slug": "pay"}}).
		expect(t, http.StatusPreconditionFailed, "edit-conflict")
	h.do(call{method: http.MethodPatch, path: path, contentType: "application/merge-patch+json",
		headers: map[string]string{"If-Match": `W/"2"`}, body: map[string]string{"slug": "pay"}}).
		expect(t, http.StatusPreconditionFailed, "edit-conflict")

	r = h.do(call{method: http.MethodPatch, path: path, contentType: "application/merge-patch+json",
		body: map[string]any{"slug": "Not A Slug", "owner": "me"}, invalid: true})
	r.expect(t, http.StatusBadRequest, "validation-failed")
	var p httpapi.Problem
	r.decode(t, &p)
	if got := pointers(p); got != "/owner" {
		t.Errorf("invalid patch fields = %s, want /owner first", got)
	}
	r = h.do(call{method: http.MethodPatch, path: path, contentType: "application/merge-patch+json",
		body: map[string]any{"name": nil}, invalid: true})
	r.expect(t, http.StatusBadRequest, "validation-failed")

	h.do(call{method: http.MethodDelete, path: path, headers: map[string]string{"If-Match": `"1"`}}).
		expect(t, http.StatusPreconditionFailed, "edit-conflict")
	h.do(call{method: http.MethodDelete, path: path, headers: map[string]string{"If-Match": `"2"`}}).
		expect(t, http.StatusNoContent, "")
	h.do(call{method: http.MethodGet, path: path}).expect(t, http.StatusNotFound, "not-found")
}

func TestProjectLifecycle(t *testing.T) {
	h := newHarness(t)
	h.setUp()
	var ws httpapi.WorkspaceJSON
	h.do(call{method: http.MethodPost, path: "/api/v1/workspaces", body: map[string]string{"name": "Payments", "slug": "payments"}}).decode(t, &ws)

	projects := "/api/v1/workspaces/" + ws.ID + "/projects"
	r := h.do(call{method: http.MethodPost, path: projects, body: map[string]string{"name": "Shop", "description": "Checkout and orders."}})
	r.expect(t, http.StatusCreated, "")
	var p httpapi.ProjectJSON
	r.decode(t, &p)
	if p.WorkspaceID != ws.ID || p.Description != "Checkout and orders." || r.header.Get("Location") != "/api/v1/projects/"+p.ID {
		t.Errorf("created %+v, Location %s", p, r.header.Get("Location"))
	}

	var page httpapi.PageJSON[httpapi.ProjectJSON]
	h.do(call{method: http.MethodGet, path: projects}).decode(t, &page)
	if len(page.Data) != 1 || page.Data[0] != p {
		t.Errorf("list = %+v", page)
	}

	path := "/api/v1/projects/" + p.ID
	h.do(call{method: http.MethodGet, path: path}).expect(t, http.StatusOK, "")
	r = h.do(call{method: http.MethodPatch, path: path, contentType: "application/merge-patch+json", body: map[string]any{"description": nil}})
	r.expect(t, http.StatusOK, "")
	if strings.Contains(string(r.body), "description") {
		t.Errorf("a removed description is still in the response: %s", r.body)
	}
	h.do(call{method: http.MethodDelete, path: path}).expect(t, http.StatusNoContent, "")
	h.do(call{method: http.MethodGet, path: path}).expect(t, http.StatusNotFound, "not-found")
	h.do(call{method: http.MethodGet, path: "/api/v1/projects/not-an-id", invalid: true}).expect(t, http.StatusNotFound, "not-found")
}

func TestOtherPeoplesWorkspacesAreInvisible(t *testing.T) {
	h := newHarness(t)
	h.setUp()
	var ws httpapi.WorkspaceJSON
	h.do(call{method: http.MethodPost, path: "/api/v1/workspaces", body: map[string]string{"name": "Payments", "slug": "payments"}}).decode(t, &ws)
	var p httpapi.ProjectJSON
	h.do(call{method: http.MethodPost, path: "/api/v1/workspaces/" + ws.ID + "/projects", body: map[string]string{"name": "Shop"}}).decode(t, &p)

	if _, err := h.ids.CreateUser(context.Background(), identity.UserInput{Name: "Eve", Email: "eve@example.com", Password: password}); err != nil {
		t.Fatal(err)
	}
	h.signOutLocally()
	h.signIn("eve@example.com")

	var page httpapi.PageJSON[httpapi.WorkspaceJSON]
	h.do(call{method: http.MethodGet, path: "/api/v1/workspaces"}).decode(t, &page)
	if len(page.Data) != 0 {
		t.Errorf("Eve sees %+v", page.Data)
	}
	for _, c := range []call{
		{method: http.MethodGet, path: "/api/v1/workspaces/" + ws.ID},
		{method: http.MethodPatch, path: "/api/v1/workspaces/" + ws.ID, contentType: "application/merge-patch+json", body: map[string]string{"name": "Mine"}},
		{method: http.MethodDelete, path: "/api/v1/workspaces/" + ws.ID},
		{method: http.MethodGet, path: "/api/v1/workspaces/" + ws.ID + "/projects"},
		{method: http.MethodPost, path: "/api/v1/workspaces/" + ws.ID + "/projects", body: map[string]string{"name": "Mine"}},
		{method: http.MethodGet, path: "/api/v1/projects/" + p.ID},
		{method: http.MethodDelete, path: "/api/v1/projects/" + p.ID},
	} {
		h.do(c).expect(t, http.StatusNotFound, "not-found")
	}
}

func TestPagination(t *testing.T) {
	h := newHarness(t)
	h.setUp()
	var created []string
	for i := range 3 {
		var ws httpapi.WorkspaceJSON
		h.do(call{method: http.MethodPost, path: "/api/v1/workspaces", body: map[string]string{
			"name": fmt.Sprintf("Team %d", i), "slug": fmt.Sprintf("team-%d", i),
		}}).decode(t, &ws)
		created = append(created, ws.ID)
	}

	var first, second httpapi.PageJSON[httpapi.WorkspaceJSON]
	h.do(call{method: http.MethodGet, path: "/api/v1/workspaces?limit=2"}).decode(t, &first)
	if len(first.Data) != 2 || first.NextCursor == "" {
		t.Fatalf("first page = %+v", first)
	}
	h.do(call{method: http.MethodGet, path: "/api/v1/workspaces?limit=2&cursor=" + first.NextCursor}).decode(t, &second)
	if len(second.Data) != 1 || second.NextCursor != "" {
		t.Fatalf("second page = %+v", second)
	}
	if got := []string{first.Data[0].ID, first.Data[1].ID, second.Data[0].ID}; fmt.Sprint(got) != fmt.Sprint(created) {
		t.Errorf("pages list %v, want %v", got, created)
	}

	h.do(call{method: http.MethodGet, path: "/api/v1/workspaces?limit=0", invalid: true}).expect(t, http.StatusBadRequest, "invalid-parameter")
	h.do(call{method: http.MethodGet, path: "/api/v1/workspaces?limit=201", invalid: true}).expect(t, http.StatusBadRequest, "invalid-parameter")
	h.do(call{method: http.MethodGet, path: "/api/v1/workspaces?cursor=garbage"}).expect(t, http.StatusBadRequest, "invalid-cursor")
	// A cursor from one list doesn't work in another.
	var projects httpapi.PageJSON[httpapi.ProjectJSON]
	for i := range 2 {
		h.do(call{method: http.MethodPost, path: "/api/v1/workspaces/" + created[0] + "/projects",
			body: map[string]string{"name": fmt.Sprintf("P%d", i)}}).expect(t, http.StatusCreated, "")
	}
	h.do(call{method: http.MethodGet, path: "/api/v1/workspaces/" + created[0] + "/projects?limit=1"}).decode(t, &projects)
	h.do(call{method: http.MethodGet, path: "/api/v1/workspaces?cursor=" + projects.NextCursor}).expect(t, http.StatusBadRequest, "invalid-cursor")
}

func TestMalformedBodies(t *testing.T) {
	h := newHarness(t)
	h.setUp()
	post := func(body, contentType string) result {
		return h.do(call{method: http.MethodPost, path: "/api/v1/workspaces", body: body, contentType: contentType, invalid: true})
	}
	post(`{"name": "Payments", "slug": "payments"`, "application/json").expect(t, http.StatusBadRequest, "invalid-json")
	post(`{"name": "Payments", "slug": "payments"} {}`, "application/json").expect(t, http.StatusBadRequest, "invalid-json")
	post(`{"name": 7, "slug": "payments"}`, "application/json").expect(t, http.StatusBadRequest, "validation-failed")
	post(`[1, 2]`, "application/json").expect(t, http.StatusBadRequest, "validation-failed")
	post(`name=Payments`, "application/x-www-form-urlencoded").expect(t, http.StatusUnsupportedMediaType, "unsupported-media-type")
	post(`{"name": "`+strings.Repeat("x", 70<<10)+`", "slug": "x"}`, "application/json").expect(t, http.StatusRequestEntityTooLarge, "body-too-large")
}

func TestSignInIsRateLimited(t *testing.T) {
	h := newHarness(t)
	h.setUp()
	attempt := func(email string) result {
		return h.do(call{method: http.MethodPost, path: "/api/v1/sessions", body: map[string]string{"email": email, "password": "wrong password!"}})
	}
	// Five attempts on one account, then that account is limited.
	for i := range 5 {
		attempt("ada@example.com").expect(t, http.StatusUnauthorized, "invalid-credentials")
		_ = i
	}
	r := attempt("ada@example.com")
	r.expect(t, http.StatusTooManyRequests, "rate-limited")
	if r.header.Get("Retry-After") == "" {
		t.Error("429 without Retry-After")
	}
	// Ten attempts from one address in all, then the address is limited.
	for i := range 4 {
		attempt(fmt.Sprintf("guess%d@example.com", i)).expect(t, http.StatusUnauthorized, "invalid-credentials")
	}
	attempt("guess@example.com").expect(t, http.StatusTooManyRequests, "rate-limited")
}

func pointers(p httpapi.Problem) string {
	var out []string
	for _, e := range p.Errors {
		out = append(out, e.Pointer)
	}
	return strings.Join(out, " ")
}
