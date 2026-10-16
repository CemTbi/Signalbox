// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/CemTbi/Signalbox/internal/modules/identity"
	"github.com/CemTbi/Signalbox/internal/platform/id"
	"github.com/CemTbi/Signalbox/internal/platform/validate"
)

// UserJSON is a user in API responses.
type UserJSON struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

// SessionJSON is a session in API responses.
type SessionJSON struct {
	User      UserJSON `json:"user"`
	ExpiresAt string   `json:"expiresAt"`
	CSRFToken string   `json:"csrfToken"`
}

// SetupStatusJSON says whether the installation still needs first-run setup.
type SetupStatusJSON struct {
	Required bool `json:"required"`
}

func userJSON(u identity.User) UserJSON {
	return UserJSON{ID: id.Format(id.User, u.ID), Email: u.Email, Name: u.Name}
}

func sessionJSON(s identity.Session) SessionJSON {
	return SessionJSON{User: userJSON(s.User), ExpiresAt: timestamp(s.ExpiresAt), CSRFToken: s.CSRFToken}
}

// timestamp formats a time in RFC 3339 in UTC, to the second.
func timestamp(t time.Time) string {
	return t.UTC().Truncate(time.Second).Format(time.RFC3339)
}

func (a *api) getSetup(w http.ResponseWriter, r *http.Request) {
	required, err := a.Identity.SetupRequired(r.Context())
	if err != nil {
		a.fail(w, r, "check setup", err)
		return
	}
	writeJSON(w, http.StatusOK, SetupStatusJSON{Required: required})
}

func (a *api) completeSetup(w http.ResponseWriter, r *http.Request) {
	if !a.checkOrigin(w, r) {
		return
	}
	if ok, retry := a.setupByIP.Allow(clientIP(r, a.TrustedProxies).String()); !ok {
		writeRateLimited(w, r, retry)
		return
	}
	var body struct {
		SetupCode *string `json:"setupCode"`
		Name      *string `json:"name"`
		Email     *string `json:"email"`
		Password  *string `json:"password"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	var fields validate.Errors
	in := identity.SetupInput{
		Code:     required(&fields, "/setupCode", body.SetupCode),
		Name:     required(&fields, "/name", body.Name),
		Email:    required(&fields, "/email", body.Email),
		Password: required(&fields, "/password", body.Password),
	}
	if len(fields) > 0 {
		writeInvalid(w, r, fields)
		return
	}

	ns, err := a.Identity.CompleteSetup(r.Context(), in, a.client(r))
	switch {
	case errors.Is(err, identity.ErrSetupComplete):
		writeProblem(w, r, http.StatusConflict, "setup-complete", "Signalbox is already set up. Sign in instead.")
	case errors.Is(err, identity.ErrInvalidSetupCode):
		writeProblem(w, r, http.StatusForbidden, "invalid-setup-code",
			"The setup code is wrong. The server writes it to its log at startup; with Docker Compose, run: docker compose logs signalbox")
	case errors.As(err, &fields):
		writeInvalid(w, r, fields)
	case err != nil:
		a.fail(w, r, "complete setup", err)
	default:
		a.cookie.set(w, ns.Token, ns.ExpiresAt)
		writeJSON(w, http.StatusCreated, sessionJSON(ns.Session))
	}
}

func (a *api) createSession(w http.ResponseWriter, r *http.Request) {
	if !a.checkOrigin(w, r) {
		return
	}
	if ok, retry := a.signInByIP.Allow(clientIP(r, a.TrustedProxies).String()); !ok {
		writeRateLimited(w, r, retry)
		return
	}
	var body struct {
		Email    *string `json:"email"`
		Password *string `json:"password"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	var fields validate.Errors
	email := required(&fields, "/email", body.Email)
	password := required(&fields, "/password", body.Password)
	if len(fields) > 0 {
		writeInvalid(w, r, fields)
		return
	}
	// A second limit per account slows down guessing one person's password
	// from many addresses.
	if ok, retry := a.signInByAccount.Allow(strings.ToLower(strings.TrimSpace(email))); !ok {
		writeRateLimited(w, r, retry)
		return
	}

	ns, err := a.Identity.SignIn(r.Context(), email, password, a.client(r))
	switch {
	case errors.Is(err, identity.ErrInvalidCredentials):
		writeProblem(w, r, http.StatusUnauthorized, "invalid-credentials", "The email address or password is wrong.")
	case err != nil:
		a.fail(w, r, "sign in", err)
	default:
		a.cookie.set(w, ns.Token, ns.ExpiresAt)
		writeJSON(w, http.StatusCreated, sessionJSON(ns.Session))
	}
}

func (a *api) getCurrentSession(w http.ResponseWriter, _ *http.Request, s *identity.Session) {
	writeJSON(w, http.StatusOK, sessionJSON(*s))
}

func (a *api) deleteCurrentSession(w http.ResponseWriter, r *http.Request, _ *identity.Session) {
	if err := a.Identity.SignOut(r.Context(), a.cookie.read(r)); err != nil {
		a.fail(w, r, "sign out", err)
		return
	}
	a.cookie.clear(w)
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) getMe(w http.ResponseWriter, _ *http.Request, s *identity.Session) {
	writeJSON(w, http.StatusOK, userJSON(s.User))
}

// fail logs an unexpected error and answers 500.
func (a *api) fail(w http.ResponseWriter, r *http.Request, what string, err error) {
	a.Logger.ErrorContext(r.Context(), what+" failed", "error", err, "request_id", RequestID(r.Context()))
	writeInternalError(w, r)
}
