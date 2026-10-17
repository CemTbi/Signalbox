// SPDX-License-Identifier: Apache-2.0

package httpapi_test

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"

	"github.com/CemTbi/Signalbox/internal/httpapi"
	"github.com/CemTbi/Signalbox/internal/platform/oidctest"
)

// ssoRoundTrip starts a sign-in, passes through the fake provider like a
// browser, and returns the callback path the provider sends it back to.
func (h *harness) ssoRoundTrip(returnPath string) string {
	h.t.Helper()
	r := h.do(call{method: http.MethodGet, path: "/api/v1/sso/start?return=" + url.QueryEscape(returnPath), stopAtRedirect: true})
	r.expect(h.t, http.StatusFound, "")
	if c := r.header.Get("Set-Cookie"); !strings.Contains(c, "__Host-sbx_sso=") || !strings.Contains(c, "HttpOnly") || !strings.Contains(c, "SameSite=Lax") {
		h.t.Errorf("start sets %q, want the HttpOnly, SameSite=Lax browser cookie", c)
	}
	state, code := oidctest.Authorize(h.t, r.header.Get("Location"))
	return "/api/v1/sso/callback?" + url.Values{"state": {state}, "code": {code}}.Encode()
}

func TestSingleSignOn(t *testing.T) {
	idp := oidctest.New(t)
	h := newHarnessWithSSO(t, idp)
	h.setUp()
	h.signOutLocally()

	var options httpapi.SignInOptionsJSON
	h.do(call{method: http.MethodGet, path: "/api/v1/sign-in-options"}).decode(t, &options)
	if options.SSO == nil || options.SSO.Name != "Test IdP" {
		t.Fatalf("sign-in options = %+v", options)
	}

	idp.SignInAs(oidctest.Identity{Subject: "grace", Email: "grace@example.com", EmailVerified: true, Name: "Grace Hopper"})
	callback := h.ssoRoundTrip("/projects/prj_01j9z3m2p4r6t8v0x2z4b6d8f0")
	r := h.do(call{method: http.MethodGet, path: callback, stopAtRedirect: true})
	r.expect(t, http.StatusFound, "")
	if loc := r.header.Get("Location"); loc != "/projects/prj_01j9z3m2p4r6t8v0x2z4b6d8f0" {
		t.Errorf("after signing in, Location = %q", loc)
	}
	var me httpapi.UserJSON
	h.do(call{method: http.MethodGet, path: "/api/v1/me"}).decode(t, &me)
	if me.Email != "grace@example.com" || me.Name != "Grace Hopper" {
		t.Errorf("signed in as %+v", me)
	}

	// The same callback can't be used twice.
	r = h.do(call{method: http.MethodGet, path: callback, stopAtRedirect: true})
	if loc := r.header.Get("Location"); loc != "/sign-in?sso=expired" {
		t.Errorf("reused callback goes to %q", loc)
	}
}

func TestSingleSignOnFailuresGoToTheSignInPage(t *testing.T) {
	idp := oidctest.New(t)
	h := newHarnessWithSSO(t, idp)
	h.setUp() // ada@example.com has a password account
	h.signOutLocally()

	location := func(c call) string {
		t.Helper()
		c.stopAtRedirect = true
		r := h.do(c)
		r.expect(t, http.StatusFound, "")
		return r.header.Get("Location")
	}

	// Never linked by email address.
	idp.SignInAs(oidctest.Identity{Subject: "someone", Email: "ada@example.com", EmailVerified: true, Name: "Ada?"})
	if loc := location(call{method: http.MethodGet, path: h.ssoRoundTrip("/")}); loc != "/sign-in?sso=account-exists" {
		t.Errorf("an existing email goes to %q", loc)
	}

	// A callback in a browser that didn't start the sign-in.
	idp.SignInAs(oidctest.Identity{Subject: "grace", Email: "grace@example.com", EmailVerified: true, Name: "Grace"})
	callback := h.ssoRoundTrip("/")
	h.client.Jar, _ = cookiejar.New(nil)
	if loc := location(call{method: http.MethodGet, path: callback}); loc != "/sign-in?sso=failed" {
		t.Errorf("another browser's callback goes to %q", loc)
	}

	// The person cancels at the provider.
	if loc := location(call{method: http.MethodGet, path: "/api/v1/sso/callback?error=access_denied&error_description=cancelled"}); loc != "/sign-in?sso=denied" {
		t.Errorf("a cancelled sign-in goes to %q", loc)
	}
	// The return path can't leave the site.
	if loc := location(call{method: http.MethodGet, path: h.ssoRoundTrip("https://evil.example/")}); loc != "/" {
		t.Errorf("a sign-in that asked to return to another site goes to %q, want /", loc)
	}
}

func TestSingleSignOnOpensAfterSetup(t *testing.T) {
	h := newHarnessWithSSO(t, oidctest.New(t))
	r := h.do(call{method: http.MethodGet, path: "/api/v1/sso/start", stopAtRedirect: true})
	r.expect(t, http.StatusFound, "")
	if loc := r.header.Get("Location"); loc != "/setup" {
		t.Errorf("before setup, single sign-on goes to %q, want /setup", loc)
	}
	if c := r.header.Get("Set-Cookie"); c != "" {
		t.Errorf("before setup, single sign-on sets a cookie: %q", c)
	}
}

func TestSingleSignOnIsOffWithoutAProvider(t *testing.T) {
	h := newHarness(t)
	var options httpapi.SignInOptionsJSON
	h.do(call{method: http.MethodGet, path: "/api/v1/sign-in-options"}).decode(t, &options)
	if options.SSO != nil {
		t.Errorf("sign-in options offer SSO without a provider: %+v", options)
	}
	h.do(call{method: http.MethodGet, path: "/api/v1/sso/start", stopAtRedirect: true}).expect(t, http.StatusNotFound, "sso-disabled")
}
