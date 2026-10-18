// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/CemTbi/Signalbox/internal/modules/identity"
	"github.com/CemTbi/Signalbox/internal/platform/id"
	"github.com/CemTbi/Signalbox/internal/platform/validate"
)

// AccessTokenJSON is a personal access token in API responses, without its
// secret.
type AccessTokenJSON struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Hint       string `json:"hint"`
	CreatedAt  string `json:"createdAt"`
	ExpiresAt  string `json:"expiresAt"`
	LastUsedAt string `json:"lastUsedAt,omitempty"`
	Expired    bool   `json:"expired"`
}

// NewAccessTokenJSON is a token that was just created, with its secret,
// which is shown this once.
type NewAccessTokenJSON struct {
	AccessTokenJSON
	Token string `json:"token"`
}

func accessTokenJSON(t identity.AccessToken, now time.Time) AccessTokenJSON {
	out := AccessTokenJSON{
		ID: id.Format(id.AccessToken, t.ID), Name: t.Name, Hint: t.Hint,
		CreatedAt: timestamp(t.CreatedAt), ExpiresAt: timestamp(t.ExpiresAt), Expired: !t.ExpiresAt.After(now),
	}
	if t.LastUsedAt != nil {
		out.LastUsedAt = timestamp(*t.LastUsedAt)
	}
	return out
}

func (a *api) listAccessTokens(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	tokens, err := a.Identity.ListAccessTokens(r.Context(), s.User.ID)
	if err != nil {
		a.fail(w, r, "access tokens", err)
		return
	}
	now := time.Now()
	out := make([]AccessTokenJSON, len(tokens))
	for i, t := range tokens {
		out[i] = accessTokenJSON(t, now)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

func (a *api) createAccessToken(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	var body struct {
		Name          *string `json:"name"`
		ExpiresInDays *int    `json:"expiresInDays"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	var fields validate.Errors
	name := required(&fields, "/name", body.Name)
	if body.ExpiresInDays != nil && (*body.ExpiresInDays < 1 || *body.ExpiresInDays > 366) {
		fields.Add("/expiresInDays", "maximum", "Choose between 1 and 366 days.")
	}
	if len(fields) > 0 {
		writeInvalid(w, r, fields)
		return
	}
	var lifetime time.Duration
	if body.ExpiresInDays != nil {
		lifetime = time.Duration(*body.ExpiresInDays) * 24 * time.Hour
	}
	t, err := a.Identity.CreateAccessToken(r.Context(), s.User.ID, name, lifetime)
	var invalid validate.Errors
	switch {
	case errors.As(err, &invalid):
		writeInvalid(w, r, invalid)
	case errors.Is(err, identity.ErrTooManyTokens):
		writeProblem(w, r, http.StatusConflict, "too-many-tokens", "You have 50 access tokens, the most anyone may. Revoke one you don't use.")
	case err != nil:
		a.fail(w, r, "access token", err)
	default:
		out := NewAccessTokenJSON{AccessTokenJSON: accessTokenJSON(t.AccessToken, time.Now()), Token: t.Token}
		w.Header().Set("Location", "/api/v1/tokens/"+out.ID)
		// The secret appears in this response only; nothing may keep it.
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusCreated, out)
	}
}

func (a *api) revokeAccessToken(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	tokenID, ok := pathID(w, r, "tokenId", id.AccessToken, "access token")
	if !ok {
		return
	}
	err := a.Identity.RevokeAccessToken(r.Context(), s.User.ID, tokenID)
	switch {
	case errors.Is(err, identity.ErrTokenNotFound):
		writeProblem(w, r, http.StatusNotFound, "not-found", "You have no access token with this ID.")
	case err != nil:
		a.fail(w, r, "access token", err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
