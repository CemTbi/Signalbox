// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/CemTbi/Signalbox/internal/modules/client"
	"github.com/CemTbi/Signalbox/internal/modules/identity"
	"github.com/CemTbi/Signalbox/internal/modules/workspaces"
	"github.com/CemTbi/Signalbox/internal/platform/id"
	"github.com/CemTbi/Signalbox/internal/platform/validate"
)

// Variables is what the API needs from the client module for environment
// variables.
type Variables interface {
	GetVariables(ctx context.Context, userID, environmentID uuid.UUID) (client.Variables, error)
	PutVariables(ctx context.Context, userID, environmentID uuid.UUID, pre workspaces.Precondition, in []client.VariableInput) (client.Variables, error)
}

// EnvironmentJSON is an environment in API responses.
type EnvironmentJSON struct {
	ID        string `json:"id"`
	ProjectID string `json:"projectId"`
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	Kind      string `json:"kind"`
	Position  int    `json:"position"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// VariableJSON is an environment variable. Secrets have no value.
type VariableJSON struct {
	Key    string  `json:"key"`
	Value  *string `json:"value,omitempty"`
	Secret bool    `json:"secret"`
}

// VariablesJSON is an environment's variables.
type VariablesJSON struct {
	EnvironmentID string         `json:"environmentId"`
	Variables     []VariableJSON `json:"variables"`
	UpdatedAt     string         `json:"updatedAt,omitempty"`
}

func environmentJSON(e workspaces.Environment) EnvironmentJSON {
	return EnvironmentJSON{
		ID: id.Format(id.Environment, e.ID), ProjectID: id.Format(id.Project, e.ProjectID), Name: e.Name, Slug: e.Slug,
		Kind: e.Kind, Position: e.Position, CreatedAt: timestamp(e.CreatedAt), UpdatedAt: timestamp(e.UpdatedAt),
	}
}

func variablesJSON(vs client.Variables) VariablesJSON {
	out := VariablesJSON{EnvironmentID: id.Format(id.Environment, vs.EnvironmentID), Variables: make([]VariableJSON, len(vs.Items))}
	if vs.Version > 0 {
		out.UpdatedAt = timestamp(vs.UpdatedAt)
	}
	for i, v := range vs.Items {
		out.Variables[i] = VariableJSON{Key: v.Key, Secret: v.Secret}
		if !v.Secret {
			out.Variables[i].Value = &v.Value
		}
	}
	return out
}

// variablesError answers for an error from the client module's variables.
func (a *api) variablesError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, client.ErrVersionMismatch):
		writeProblem(w, r, http.StatusPreconditionFailed, "edit-conflict",
			"Someone changed these variables since you loaded them. Load them again and reapply your change.")
	case errors.Is(err, client.ErrForbidden):
		writeProblem(w, r, http.StatusForbidden, "permission-denied",
			"Your role in this workspace doesn't allow changing variables. Ask a workspace owner or admin to make you an editor.")
	default:
		a.secretsError(w, r, "environment", err)
	}
}

// secretsError answers for the errors of sending with or storing
// variables.
func (a *api) secretsError(w http.ResponseWriter, r *http.Request, what string, err error) {
	var fields validate.Errors
	switch {
	case errors.As(err, &fields):
		writeInvalid(w, r, fields)
	case errors.Is(err, client.ErrNoSecretKey):
		writeProblem(w, r, http.StatusServiceUnavailable, "secrets-unavailable",
			"This server has no secret key, so it can't keep secrets. An administrator can set SIGNALBOX_SECRET_KEY to 32 random bytes in base64, such as the output of openssl rand -base64 32.")
	case errors.Is(err, client.ErrSecretKeyChanged):
		writeProblem(w, r, http.StatusServiceUnavailable, "secret-key-changed",
			"This workspace's secrets were stored with another secret key than the server has now. An administrator can restore the SIGNALBOX_SECRET_KEY they were stored with.")
	case errors.Is(err, client.ErrResolvedTooLarge):
		writeProblem(w, r, http.StatusUnprocessableEntity, "request-too-large",
			"Once its variables are filled in, the request is larger than the runner sends. Check for variables that name each other.")
	default:
		a.clientError(w, r, what, err)
	}
}

func (a *api) listEnvironments(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	projectID, ok := pathID(w, r, "projectId", id.Project, "project")
	if !ok {
		return
	}
	envs, err := a.Workspaces.ListEnvironments(r.Context(), s.User.ID, projectID)
	if err != nil {
		a.workspaceError(w, r, "project", err)
		return
	}
	out := make([]EnvironmentJSON, len(envs))
	for i, e := range envs {
		out[i] = environmentJSON(e)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

func (a *api) getEnvironment(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	environmentID, ok := pathID(w, r, "environmentId", id.Environment, "environment")
	if !ok {
		return
	}
	e, _, err := a.Workspaces.GetEnvironment(r.Context(), s.User.ID, environmentID)
	if err != nil {
		a.workspaceError(w, r, "environment", err)
		return
	}
	w.Header().Set("ETag", etag(e.Version))
	writeJSON(w, http.StatusOK, environmentJSON(e))
}

func (a *api) getVariables(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	environmentID, ok := pathID(w, r, "environmentId", id.Environment, "environment")
	if !ok {
		return
	}
	vs, err := a.Variables.GetVariables(r.Context(), s.User.ID, environmentID)
	if err != nil {
		a.variablesError(w, r, err)
		return
	}
	w.Header().Set("ETag", etag(vs.Version))
	writeJSON(w, http.StatusOK, variablesJSON(vs))
}

func (a *api) putVariables(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	environmentID, ok := pathID(w, r, "environmentId", id.Environment, "environment")
	if !ok {
		return
	}
	var body struct {
		Variables *[]VariableJSON `json:"variables"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Variables == nil {
		writeInvalid(w, r, validate.Errors{{Pointer: "/variables", Code: "required", Detail: "Send the variables, or an empty list to remove them all."}})
		return
	}
	in := make([]client.VariableInput, len(*body.Variables))
	for i, v := range *body.Variables {
		in[i] = client.VariableInput(v)
	}
	vs, err := a.Variables.PutVariables(r.Context(), s.User.ID, environmentID, precondition(r), in)
	if err != nil {
		a.variablesError(w, r, err)
		return
	}
	w.Header().Set("ETag", etag(vs.Version))
	writeJSON(w, http.StatusOK, variablesJSON(vs))
}
