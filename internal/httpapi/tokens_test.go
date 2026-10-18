// SPDX-License-Identifier: Apache-2.0

package httpapi_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/CemTbi/Signalbox/internal/httpapi"
)

func TestAccessTokens(t *testing.T) {
	h := newHarness(t)
	h.setUp()

	r := h.do(call{method: http.MethodPost, path: "/api/v1/tokens", body: map[string]any{"name": "CI", "expiresInDays": 30}})
	r.expect(t, http.StatusCreated, "")
	var tok httpapi.NewAccessTokenJSON
	r.decode(t, &tok)
	if !strings.HasPrefix(tok.Token, "sbx_pat_") || !strings.HasPrefix(tok.ID, "tok_") || r.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("token = %+v, headers %v", tok, r.header)
	}
	var list struct {
		Data []httpapi.AccessTokenJSON `json:"data"`
	}
	h.do(call{method: http.MethodGet, path: "/api/v1/tokens"}).decode(t, &list)
	if len(list.Data) != 1 || list.Data[0].ID != tok.ID || strings.Contains(string(h.do(call{method: http.MethodGet, path: "/api/v1/tokens"}).body), tok.Token) {
		t.Errorf("list = %+v", list)
	}

	// With only the token: no cookie, no CSRF token, no Origin.
	h.signOutLocally()
	bearer := map[string]string{"Authorization": "Bearer " + tok.Token}
	var me httpapi.UserJSON
	h.do(call{method: http.MethodGet, path: "/api/v1/me", headers: bearer}).decode(t, &me)
	if me.Email != "ada@example.com" {
		t.Errorf("me = %+v", me)
	}
	h.do(call{method: http.MethodPost, path: "/api/v1/workspaces", headers: bearer, body: map[string]string{"name": "From CI", "slug": "from-ci"}}).
		expect(t, http.StatusCreated, "")
	// A token can't make tokens, or act as a browser session.
	h.do(call{method: http.MethodPost, path: "/api/v1/tokens", headers: bearer, body: map[string]any{"name": "More"}}).
		expect(t, http.StatusForbidden, "browser-session-required")
	h.do(call{method: http.MethodGet, path: "/api/v1/sessions/current", headers: bearer}).
		expect(t, http.StatusForbidden, "browser-session-required")

	h.signIn("ada@example.com")
	h.do(call{method: http.MethodDelete, path: "/api/v1/tokens/" + tok.ID}).expect(t, http.StatusNoContent, "")
	h.do(call{method: http.MethodDelete, path: "/api/v1/tokens/" + tok.ID}).expect(t, http.StatusNotFound, "not-found")
	h.signOutLocally()
	r = h.do(call{method: http.MethodGet, path: "/api/v1/me", headers: bearer})
	r.expect(t, http.StatusUnauthorized, "unauthenticated")
	if r.header.Get("WWW-Authenticate") == "" {
		t.Error("401 for a token without WWW-Authenticate")
	}
}

func TestAccessTokenInput(t *testing.T) {
	h := newHarness(t)
	h.setUp()
	h.do(call{method: http.MethodPost, path: "/api/v1/tokens", body: map[string]any{"name": "", "expiresInDays": 400}, invalid: true}).
		expect(t, http.StatusBadRequest, "validation-failed")
}
