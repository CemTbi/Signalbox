// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/CemTbi/Signalbox/internal/modules/identity"
)

// CSRFHeader carries the session's CSRF token on unsafe requests.
const CSRFHeader = "X-CSRF-Token"

// sessionCookie is the cookie that carries the session token. Over HTTPS it
// is __Host-sbx_session with Secure, so it can't be set by a subdomain or
// sent in clear text. Browsers drop Secure cookies on plain-HTTP sites other
// than localhost, so a plain-HTTP installation uses sbx_session instead,
// and the server warns at startup. See docs/adr/0010-first-run-setup.md.
type sessionCookie struct {
	name   string
	secure bool
}

func newSessionCookie(public *url.URL) sessionCookie {
	if public.Scheme == "https" {
		return sessionCookie{name: "__Host-sbx_session", secure: true}
	}
	return sessionCookie{name: "sbx_session"}
}

// Secure is set whenever the public URL is https. A plain-HTTP installation
// can't use Secure cookies at all; see sessionCookie.
func (c sessionCookie) set(w http.ResponseWriter, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: Secure follows the public URL's scheme.
		Name:     c.name,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(time.Until(expires).Seconds()),
		Secure:   c.secure,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func (c sessionCookie) clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: Secure follows the public URL's scheme.
		Name:     c.name,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Secure:   c.secure,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func (c sessionCookie) read(r *http.Request) string {
	ck, err := r.Cookie(c.name)
	if err != nil {
		return ""
	}
	return ck.Value
}

// origin returns the origin of a URL as browsers send it in the Origin
// header: scheme and host, without a default port.
func origin(u *url.URL) string {
	host := u.Host
	switch {
	case u.Scheme == "https" && strings.HasSuffix(host, ":443"):
		host = strings.TrimSuffix(host, ":443")
	case u.Scheme == "http" && strings.HasSuffix(host, ":80"):
		host = strings.TrimSuffix(host, ":80")
	}
	return strings.ToLower(u.Scheme + "://" + host)
}

func unsafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return true
}

// checkOrigin rejects unsafe requests that a browser sent from another
// site. It is the first CSRF defense and the only one for signing in, when
// there is no session yet.
func (a *api) checkOrigin(w http.ResponseWriter, r *http.Request) bool {
	got := r.Header.Get("Origin")
	switch {
	case got == "" && r.Header.Get("Sec-Fetch-Site") == "same-origin":
		return true
	case got == "":
		writeProblem(w, r, http.StatusForbidden, "origin-mismatch",
			"The request has no Origin header. Browsers send one; other clients must send Origin: "+a.publicOrigin+".")
		return false
	case strings.ToLower(got) != a.publicOrigin:
		a.Logger.WarnContext(r.Context(), "rejected a cross-origin request",
			"origin", got, "expected", a.publicOrigin, "request_id", RequestID(r.Context()))
		writeProblem(w, r, http.StatusForbidden, "origin-mismatch",
			"This request came from "+got+", but Signalbox is set up for "+a.publicOrigin+
				". If that is the address you use, set SIGNALBOX_PUBLIC_URL to it.")
		return false
	}
	return true
}

// authenticated wraps a handler that needs a signed-in user: someone with a
// browser session or a personal access token. A session comes from the
// cookie and, on unsafe methods, needs a same-origin request carrying its
// CSRF token. A token comes in an Authorization: Bearer header, which
// browsers never send on their own, so it needs neither.
func (a *api) authenticated(h func(http.ResponseWriter, *http.Request, *identity.Session)) http.Handler {
	return a.authenticate(h, true)
}

// browserOnly is authenticated without access tokens, for what a token
// mustn't do, such as making more tokens.
func (a *api) browserOnly(h func(http.ResponseWriter, *http.Request, *identity.Session)) http.Handler {
	return a.authenticate(h, false)
}

func (a *api) authenticate(h func(http.ResponseWriter, *http.Request, *identity.Session), tokens bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
			if !tokens {
				writeProblem(w, r, http.StatusForbidden, "browser-session-required",
					"Access tokens can't do this. Sign in to the web app to do it.")
				return
			}
			sess, err := a.Identity.AuthenticateAccessToken(r.Context(), strings.TrimSpace(bearer))
			if errors.Is(err, identity.ErrUnauthenticated) {
				w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
				writeProblem(w, r, http.StatusUnauthorized, "unauthenticated",
					"The access token is wrong, expired or revoked. Create a new one under Access tokens in the web app.")
				return
			}
			if err != nil {
				a.Logger.ErrorContext(r.Context(), "access token lookup failed", "error", err, "request_id", RequestID(r.Context()))
				writeInternalError(w, r)
				return
			}
			noteActor(r.Context(), sess)
			h(w, r, sess)
			return
		}
		token := a.cookie.read(r)
		if token == "" {
			writeProblem(w, r, http.StatusUnauthorized, "unauthenticated", "Sign in to continue.")
			return
		}
		sess, err := a.Identity.Authenticate(r.Context(), token)
		if errors.Is(err, identity.ErrUnauthenticated) {
			a.cookie.clear(w)
			writeProblem(w, r, http.StatusUnauthorized, "unauthenticated", "Your session has ended. Sign in again.")
			return
		}
		if err != nil {
			a.Logger.ErrorContext(r.Context(), "session lookup failed", "error", err, "request_id", RequestID(r.Context()))
			writeInternalError(w, r)
			return
		}
		if unsafeMethod(r.Method) {
			if !a.checkOrigin(w, r) {
				return
			}
			sent := r.Header.Get(CSRFHeader)
			if subtle.ConstantTimeCompare([]byte(sent), []byte(sess.CSRFToken)) != 1 {
				a.Logger.WarnContext(r.Context(), "rejected a request without a valid CSRF token",
					"user_id", sess.User.ID, "request_id", RequestID(r.Context()))
				writeProblem(w, r, http.StatusForbidden, "csrf-failed",
					"Send the session's CSRF token in the "+CSRFHeader+" header. If you are using the web app, reload the page.")
				return
			}
		}
		noteActor(r.Context(), sess)
		h(w, r, sess)
	})
}

// protect requires a signed-in user for a handler that doesn't need the
// session itself.
func (a *api) protect(h http.Handler) http.Handler {
	return a.authenticated(func(w http.ResponseWriter, r *http.Request, _ *identity.Session) {
		h.ServeHTTP(w, r)
	})
}

func (a *api) client(r *http.Request) identity.Client {
	return identity.Client{IP: clientIP(r, a.TrustedProxies), UserAgent: r.UserAgent()}
}
