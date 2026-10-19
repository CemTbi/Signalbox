// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"github.com/CemTbi/Signalbox/internal/modules/client"
	"github.com/CemTbi/Signalbox/internal/modules/client/convert"
	"github.com/CemTbi/Signalbox/internal/modules/identity"
	"github.com/CemTbi/Signalbox/internal/platform/id"
	"github.com/CemTbi/Signalbox/internal/platform/validate"
)

// Imports is what the API needs from the client module for imports and
// exports.
type Imports interface {
	ImportCollection(ctx context.Context, userID, projectID uuid.UUID, in convert.Collection) (client.Collection, error)
	ExportCollection(ctx context.Context, userID, collectionID uuid.UUID) (convert.Collection, error)
	ImportVariables(ctx context.Context, userID, environmentID uuid.UUID, in []convert.Variable) (client.Variables, error)
	MayImport(ctx context.Context, userID, projectID uuid.UUID) error
	MayChangeVariables(ctx context.Context, userID, environmentID uuid.UUID) error
}

// importLimit bounds a Postman file, as JSON.
const importLimit = convert.MaxFileSize

// ReportJSON is what an import found.
type ReportJSON struct {
	Clean     bool         `json:"clean"`
	Folders   int          `json:"folders"`
	Requests  int          `json:"requests"`
	Variables int          `json:"variables"`
	Notes     []ReportNote `json:"notes"`
}

// ReportNote is one line of a report.
type ReportNote struct {
	Item    string `json:"item,omitempty"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

func reportJSON(r convert.Report) ReportJSON {
	out := ReportJSON{Clean: r.Clean(), Folders: r.Folders, Requests: r.Requests, Variables: r.Variables, Notes: make([]ReportNote, len(r.Notes))}
	for i, n := range r.Notes {
		out.Notes[i] = ReportNote{Item: n.Item, Level: string(n.Level), Message: n.Message}
	}
	return out
}

// dryRun reads ?dryRun=true, which checks an import without storing it.
func dryRun(w http.ResponseWriter, r *http.Request) (bool, bool) {
	switch r.URL.Query().Get("dryRun") {
	case "", "false":
		return false, true
	case "true":
		return true, true
	default:
		writeInvalid(w, r, validate.Errors{{Pointer: "dryRun", Code: "type", Detail: "Use true or false."}})
		return false, false
	}
}

// readFile reads a Postman file sent as the request body.
func readFile(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	var raw json.RawMessage
	if !decodeJSONUpTo(w, r, &raw, importLimit) {
		return nil, false
	}
	return raw, true
}

func (a *api) importError(w http.ResponseWriter, r *http.Request, what string, err error) {
	switch {
	case errors.Is(err, convert.ErrNotPostman):
		writeProblem(w, r, http.StatusUnprocessableEntity, "not-postman", "Signalbox can't import this file: "+strings.TrimPrefix(err.Error(), convert.ErrNotPostman.Error()+": ")+".")
	case errors.Is(err, convert.ErrNotCurl):
		writeProblem(w, r, http.StatusUnprocessableEntity, "not-curl", "Signalbox can't read this curl command: "+strings.TrimPrefix(err.Error(), convert.ErrNotCurl.Error()+": ")+".")
	default:
		a.collectionsError(w, r, what, err)
	}
}

func (a *api) importCollection(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	projectID, ok := pathID(w, r, "projectId", id.Project, "project")
	if !ok {
		return
	}
	dry, ok := dryRun(w, r)
	if !ok {
		return
	}
	data, ok := readFile(w, r)
	if !ok {
		return
	}
	in, report, err := convert.ReadPostmanCollection(data)
	if err != nil {
		a.importError(w, r, "project", err)
		return
	}
	if dry {
		// Checking needs the same access as importing, so it doesn't reveal
		// projects.
		if err := a.Imports.MayImport(r.Context(), s.User.ID, projectID); err != nil {
			a.importError(w, r, "project", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"report": reportJSON(report)})
		return
	}
	c, err := a.Imports.ImportCollection(r.Context(), s.User.ID, projectID, in)
	if err != nil {
		a.importError(w, r, "project", err)
		return
	}
	out := collectionJSON(c)
	w.Header().Set("Location", "/api/v1/collections/"+out.ID)
	w.Header().Set("ETag", etag(c.Version))
	writeJSON(w, http.StatusCreated, map[string]any{"collection": out, "report": reportJSON(report)})
}

var unsafeFileName = regexp.MustCompile(`[^A-Za-z0-9._ -]+`)

func (a *api) exportCollection(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	collectionID, ok := pathID(w, r, "collectionId", id.Collection, "collection")
	if !ok {
		return
	}
	c, err := a.Imports.ExportCollection(r.Context(), s.User.ID, collectionID)
	if err != nil {
		a.importError(w, r, "collection", err)
		return
	}
	doc, err := convert.ExportPostman(collectionID.String(), c)
	if err != nil {
		a.fail(w, r, "collection", err)
		return
	}
	name := strings.TrimSpace(unsafeFileName.ReplaceAllString(c.Name, ""))
	if name == "" {
		name = "collection"
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`.postman_collection.json"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(doc) //nolint:gosec // G705: JSON served as an attachment, which browsers don't render.
}

func (a *api) importVariables(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	environmentID, ok := pathID(w, r, "environmentId", id.Environment, "environment")
	if !ok {
		return
	}
	dry, ok := dryRun(w, r)
	if !ok {
		return
	}
	data, ok := readFile(w, r)
	if !ok {
		return
	}
	env, report, err := convert.ReadPostmanEnvironment(data)
	if err != nil {
		a.importError(w, r, "environment", err)
		return
	}
	if dry {
		if err := a.Imports.MayChangeVariables(r.Context(), s.User.ID, environmentID); err != nil {
			a.variablesError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"report": reportJSON(report)})
		return
	}
	vs, err := a.Imports.ImportVariables(r.Context(), s.User.ID, environmentID, env.Variables)
	if err != nil {
		a.variablesError(w, r, err)
		return
	}
	w.Header().Set("ETag", etag(vs.Version))
	writeJSON(w, http.StatusOK, map[string]any{"variables": variablesJSON(vs), "report": reportJSON(report)})
}

func (a *api) importCurl(w http.ResponseWriter, r *http.Request, _ *identity.Session) {
	var body struct {
		Command *string `json:"command"`
	}
	if !decodeJSONUpTo(w, r, &body, 2*convert.MaxCurl) {
		return
	}
	var fields validate.Errors
	command := required(&fields, "/command", body.Command)
	if len(fields) > 0 {
		writeInvalid(w, r, fields)
		return
	}
	req, report, err := convert.ReadCurl(command)
	if err != nil {
		a.importError(w, r, "command", err)
		return
	}
	out := SavedRequestJSON{Method: req.Method, URL: req.URL, Body: req.Body}
	for _, h := range req.Headers {
		out.Headers = append(out.Headers, SavedHeaderJSON(h))
	}
	if a := req.Auth; a != nil {
		out.Auth = &AuthJSON{Type: a.Type, Username: a.Username, Password: a.Password, Token: a.Token, Key: a.Key, Value: a.Value, In: a.In}
	}
	writeJSON(w, http.StatusOK, map[string]any{"request": out, "report": reportJSON(report)})
}
