// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/CemTbi/Signalbox/internal/modules/identity"
	"github.com/CemTbi/Signalbox/internal/modules/telemetry"
	"github.com/CemTbi/Signalbox/internal/platform/id"
	"github.com/CemTbi/Signalbox/internal/platform/validate"
)

// IngestKeys is what the API needs from the telemetry module's key store.
type IngestKeys interface {
	Create(ctx context.Context, userID, environmentID uuid.UUID, name string) (*telemetry.NewIngestKey, error)
	List(ctx context.Context, userID, environmentID uuid.UUID) ([]telemetry.IngestKey, error)
	Revoke(ctx context.Context, userID, keyID uuid.UUID) error
}

// IngestKeyJSON is an ingest key in API responses, without its secret.
type IngestKeyJSON struct {
	ID            string  `json:"id"`
	EnvironmentID string  `json:"environmentId"`
	Name          string  `json:"name"`
	Hint          string  `json:"hint"`
	CreatedBy     *string `json:"createdBy"`
	CreatedAt     string  `json:"createdAt"`
	LastUsedAt    *string `json:"lastUsedAt"`
}

// NewIngestKeyJSON is a key just created, with its secret, shown once.
type NewIngestKeyJSON struct {
	IngestKeyJSON
	Key string `json:"key"`
}

func ingestKeyJSON(k telemetry.IngestKey) IngestKeyJSON {
	out := IngestKeyJSON{
		ID: id.Format(id.IngestKey, k.ID), EnvironmentID: id.Format(id.Environment, k.EnvironmentID),
		Name: k.Name, Hint: k.Hint, CreatedAt: timestamp(k.CreatedAt),
	}
	if k.CreatedBy != uuid.Nil {
		by := id.Format(id.User, k.CreatedBy)
		out.CreatedBy = &by
	}
	if k.LastUsedAt != nil {
		used := timestamp(*k.LastUsedAt)
		out.LastUsedAt = &used
	}
	return out
}

func (a *api) ingestKeysError(w http.ResponseWriter, r *http.Request, err error) {
	var fields validate.Errors
	switch {
	case errors.As(err, &fields):
		writeInvalid(w, r, fields)
	case errors.Is(err, telemetry.ErrNotFound):
		writeProblem(w, r, http.StatusNotFound, "not-found", "There is no environment or ingest key with this ID.")
	case errors.Is(err, telemetry.ErrForbidden):
		writeProblem(w, r, http.StatusForbidden, "permission-denied",
			"Only workspace owners and admins manage ingest keys, since they let services write to the project.")
	case errors.Is(err, telemetry.ErrTooManyKeys):
		writeProblem(w, r, http.StatusConflict, "too-many-keys", "This environment has 50 ingest keys, the most it may. Revoke one nothing uses.")
	default:
		a.fail(w, r, "ingest key", err)
	}
}

func (a *api) listIngestKeys(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	envID, ok := pathID(w, r, "environmentId", id.Environment, "environment")
	if !ok {
		return
	}
	keys, err := a.IngestKeys.List(r.Context(), s.User.ID, envID)
	if err != nil {
		a.ingestKeysError(w, r, err)
		return
	}
	out := make([]IngestKeyJSON, len(keys))
	for i, k := range keys {
		out[i] = ingestKeyJSON(k)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

func (a *api) createIngestKey(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	envID, ok := pathID(w, r, "environmentId", id.Environment, "environment")
	if !ok {
		return
	}
	var body struct {
		Name *string `json:"name"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	var fields validate.Errors
	name := required(&fields, "/name", body.Name)
	if len(fields) > 0 {
		writeInvalid(w, r, fields)
		return
	}
	k, err := a.IngestKeys.Create(r.Context(), s.User.ID, envID, name)
	if err != nil {
		a.ingestKeysError(w, r, err)
		return
	}
	out := NewIngestKeyJSON{IngestKeyJSON: ingestKeyJSON(k.IngestKey), Key: k.Key}
	w.Header().Set("Location", "/api/v1/ingest-keys/"+out.ID)
	// The secret appears in this response only; nothing may keep it.
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, out)
}

func (a *api) revokeIngestKey(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	keyID, ok := pathID(w, r, "keyId", id.IngestKey, "ingest key")
	if !ok {
		return
	}
	if err := a.IngestKeys.Revoke(r.Context(), s.User.ID, keyID); err != nil {
		a.ingestKeysError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
