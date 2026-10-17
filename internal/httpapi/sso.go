// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/CemTbi/Signalbox/internal/modules/identity"
)

// Single sign-on happens in browser navigations, not API calls: the start
// endpoint redirects to the identity provider, which redirects back to the
// callback. Failures send the browser to the sign-in page with a reason in
// the sso parameter, which the web app explains, or to the setup page on an
// installation without accounts.

// SignInOptionsJSON lists the ways to sign in besides a password.
type SignInOptionsJSON struct {
	SSO *SSOOptionJSON `json:"sso,omitempty"`
}

// SSOOptionJSON describes the configured identity provider.
type SSOOptionJSON struct {
	Name string `json:"name"`
}

// ssoCookie ties a sign-in to the browser that started it. It lives only
// while the person is at the identity provider. SameSite=Lax lets it
// travel with the provider's redirect back, a top-level navigation.
type ssoCookie struct {
	name   string
	secure bool
}

func newSSOCookie(session sessionCookie) ssoCookie {
	if session.secure {
		return ssoCookie{name: "__Host-sbx_sso", secure: true}
	}
	return ssoCookie{name: "sbx_sso"}
}

func (c ssoCookie) set(w http.ResponseWriter, value string, maxAge time.Duration) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: Secure follows the public URL's scheme.
		Name: c.name, Value: value, Path: "/", MaxAge: int(maxAge / time.Second),
		Secure: c.secure, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
}

func (c ssoCookie) read(r *http.Request) string {
	ck, err := r.Cookie(c.name)
	if err != nil {
		return ""
	}
	return ck.Value
}

func (a *api) getSignInOptions(w http.ResponseWriter, _ *http.Request) {
	var out SignInOptionsJSON
	if name, ok := a.Identity.SSOName(); ok {
		out.SSO = &SSOOptionJSON{Name: name}
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *api) startSSO(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.Identity.SSOName(); !ok {
		writeProblem(w, r, http.StatusNotFound, "sso-disabled", "Single sign-on isn't set up on this installation.")
		return
	}
	if ok, _ := a.ssoByIP.Allow(clientIP(r, a.TrustedProxies).String()); !ok {
		toSignIn(w, r, "rate-limited")
		return
	}
	started, err := a.Identity.StartSSO(r.Context(), r.URL.Query().Get("return"))
	if err != nil {
		if !errors.Is(err, identity.ErrSSOSetupRequired) {
			a.Logger.ErrorContext(r.Context(), "couldn't start single sign-on", "error", err, "request_id", RequestID(r.Context()))
		}
		ssoFailed(w, r, err)
		return
	}
	a.ssoCookie.set(w, started.BrowserToken, 10*time.Minute)
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, started.URL, http.StatusFound)
}

func (a *api) finishSSO(w http.ResponseWriter, r *http.Request) {
	a.ssoCookie.set(w, "", -time.Second) // used once, whatever happens
	q := r.URL.Query()
	if q.Has("error") {
		// The provider refused or the person cancelled. The description
		// comes from the query string, so it is only logged, shortened.
		a.Logger.WarnContext(r.Context(), "the identity provider returned an error",
			"error", truncate(q.Get("error"), 64), "description", truncate(q.Get("error_description"), 200))
		reason := "failed"
		if q.Get("error") == "access_denied" {
			reason = "denied"
		}
		toSignIn(w, r, reason)
		return
	}
	ns, back, err := a.Identity.FinishSSO(r.Context(), q.Get("state"), q.Get("code"), a.ssoCookie.read(r), a.client(r))
	if err != nil {
		ssoFailed(w, r, err)
		return
	}
	a.cookie.set(w, ns.Token, ns.ExpiresAt)
	w.Header().Set("Cache-Control", "no-store")
	// SafeReturnPath keeps only paths on this site.
	http.Redirect(w, r, identity.SafeReturnPath(back), http.StatusFound) //nolint:gosec // G710: limited to this site.
}

// ssoFailed sends the browser to the page that explains err.
func ssoFailed(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, identity.ErrSSOSetupRequired) {
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, "/setup", http.StatusFound)
		return
	}
	toSignIn(w, r, ssoReason(err))
}

// ssoReason names a failure for the sign-in page.
func ssoReason(err error) string {
	switch {
	case errors.Is(err, identity.ErrSSOExpired):
		return "expired"
	case errors.Is(err, identity.ErrSSOUnavailable):
		return "unavailable"
	case errors.Is(err, identity.ErrSSOEmailUnverified):
		return "email-unverified"
	case errors.Is(err, identity.ErrSSONotAllowed):
		return "not-allowed"
	case errors.Is(err, identity.ErrSSOAccountExists):
		return "account-exists"
	}
	return "failed"
}

func toSignIn(w http.ResponseWriter, r *http.Request, reason string) {
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, "/sign-in?"+url.Values{"sso": {reason}}.Encode(), http.StatusFound)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
