// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/CemTbi/Signalbox/internal/modules/client"
	"github.com/CemTbi/Signalbox/internal/modules/identity"
	"github.com/CemTbi/Signalbox/internal/modules/workspaces"
	"github.com/CemTbi/Signalbox/internal/platform/id"
	"github.com/CemTbi/Signalbox/internal/platform/validate"
)

// Collections is what the API needs from the client module for collections.
type Collections interface {
	ListCollections(ctx context.Context, userID, projectID uuid.UUID) ([]client.Collection, error)
	CreateCollection(ctx context.Context, userID, projectID uuid.UUID, name, description string) (client.Collection, error)
	GetCollection(ctx context.Context, userID, collectionID uuid.UUID) (client.Collection, error)
	UpdateCollection(ctx context.Context, userID, collectionID uuid.UUID, pre workspaces.Precondition, patch client.CollectionPatch) (client.Collection, error)
	DeleteCollection(ctx context.Context, userID, collectionID uuid.UUID, pre workspaces.Precondition) error
	Items(ctx context.Context, userID, collectionID uuid.UUID) ([]client.Item, error)
	CreateItem(ctx context.Context, userID, collectionID uuid.UUID, in client.ItemInput) (client.Item, error)
	GetItem(ctx context.Context, userID, itemID uuid.UUID) (client.Item, error)
	UpdateItem(ctx context.Context, userID, itemID uuid.UUID, pre workspaces.Precondition, patch client.ItemPatch) (client.Item, error)
	DeleteItem(ctx context.Context, userID, itemID uuid.UUID, pre workspaces.Precondition) error
}

// CollectionJSON is a collection in API responses.
type CollectionJSON struct {
	ID          string                   `json:"id"`
	ProjectID   string                   `json:"projectId"`
	Name        string                   `json:"name"`
	Description string                   `json:"description,omitempty"`
	Variables   []CollectionVariableJSON `json:"variables"`
	Scripts     *ScriptsJSON             `json:"scripts,omitempty"`
	CreatedAt   string                   `json:"createdAt"`
	UpdatedAt   string                   `json:"updatedAt"`
}

// CollectionVariableJSON is a collection variable.
type CollectionVariableJSON struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// ScriptsJSON are a collection's, folder's or request's scripts.
type ScriptsJSON struct {
	PreRequest string `json:"prerequest,omitempty"`
	Test       string `json:"test,omitempty"`
}

func scriptsJSON(s client.Scripts) *ScriptsJSON {
	if s == (client.Scripts{}) {
		return nil
	}
	out := ScriptsJSON(s)
	return &out
}

// SavedHeaderJSON is a saved request's header.
type SavedHeaderJSON struct {
	Key     string `json:"key"`
	Value   string `json:"value"`
	Enabled bool   `json:"enabled"`
}

// SavedRequestJSON is a request as a collection keeps it. In lists it has
// only the method and URL.
type SavedRequestJSON struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers []SavedHeaderJSON `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
	Auth    *AuthJSON         `json:"auth,omitempty"`
}

// ItemJSON is a folder or request in API responses.
type ItemJSON struct {
	ID           string            `json:"id"`
	CollectionID string            `json:"collectionId"`
	ParentID     string            `json:"parentId,omitempty"`
	Kind         string            `json:"kind"`
	Name         string            `json:"name"`
	Description  string            `json:"description,omitempty"`
	Request      *SavedRequestJSON `json:"request,omitempty"`
	Scripts      *ScriptsJSON      `json:"scripts,omitempty"`
	CreatedAt    string            `json:"createdAt"`
	UpdatedAt    string            `json:"updatedAt"`
}

func collectionJSON(c client.Collection) CollectionJSON {
	out := CollectionJSON{
		ID: id.Format(id.Collection, c.ID), ProjectID: id.Format(id.Project, c.ProjectID), Name: c.Name, Description: c.Description,
		Variables: make([]CollectionVariableJSON, len(c.Variables)), Scripts: scriptsJSON(c.Scripts),
		CreatedAt: timestamp(c.CreatedAt), UpdatedAt: timestamp(c.UpdatedAt),
	}
	for i, v := range c.Variables {
		out.Variables[i] = CollectionVariableJSON(v)
	}
	return out
}

func itemJSON(it client.Item) ItemJSON {
	out := ItemJSON{
		ID: id.Format(id.Item, it.ID), CollectionID: id.Format(id.Collection, it.CollectionID), Kind: it.Kind, Name: it.Name,
		Description: it.Description, Scripts: scriptsJSON(it.Scripts), CreatedAt: timestamp(it.CreatedAt), UpdatedAt: timestamp(it.UpdatedAt),
	}
	if it.ParentID != uuid.Nil {
		out.ParentID = id.Format(id.Item, it.ParentID)
	}
	if r := it.Request; r != nil {
		out.Request = &SavedRequestJSON{Method: r.Method, URL: r.URL, Body: r.Body, Auth: authJSON(r.Auth)}
		for _, h := range r.Headers {
			out.Request.Headers = append(out.Request.Headers, SavedHeaderJSON(h))
		}
	}
	return out
}

func savedRequest(r *SavedRequestJSON) *client.SavedRequest {
	if r == nil {
		return nil
	}
	out := &client.SavedRequest{Method: r.Method, URL: r.URL, Body: r.Body, Headers: []client.SavedHeader{}, Auth: authIn(r.Auth)}
	for _, h := range r.Headers {
		out.Headers = append(out.Headers, client.SavedHeader(h))
	}
	return out
}

// collectionsError answers for an error from the client module's
// collections.
func (a *api) collectionsError(w http.ResponseWriter, r *http.Request, what string, err error) {
	var fields validate.Errors
	switch {
	case errors.As(err, &fields):
		writeInvalid(w, r, fields)
	case errors.Is(err, client.ErrVersionMismatch):
		writeProblem(w, r, http.StatusPreconditionFailed, "edit-conflict",
			"Someone changed this "+what+" since you loaded it. Load it again and reapply your change.")
	case errors.Is(err, client.ErrForbidden):
		writeProblem(w, r, http.StatusForbidden, "permission-denied",
			"Your role in this workspace doesn't allow this. Ask a workspace owner or admin.")
	default:
		a.clientError(w, r, what, err)
	}
}

func (a *api) listCollections(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	projectID, ok := pathID(w, r, "projectId", id.Project, "project")
	if !ok {
		return
	}
	cs, err := a.Collections.ListCollections(r.Context(), s.User.ID, projectID)
	if err != nil {
		a.collectionsError(w, r, "project", err)
		return
	}
	out := make([]CollectionJSON, len(cs))
	for i, c := range cs {
		out[i] = collectionJSON(c)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

func (a *api) createCollection(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	projectID, ok := pathID(w, r, "projectId", id.Project, "project")
	if !ok {
		return
	}
	var body struct {
		Name        *string `json:"name"`
		Description string  `json:"description"`
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
	c, err := a.Collections.CreateCollection(r.Context(), s.User.ID, projectID, name, body.Description)
	if err != nil {
		a.collectionsError(w, r, "project", err)
		return
	}
	out := collectionJSON(c)
	w.Header().Set("Location", "/api/v1/collections/"+out.ID)
	w.Header().Set("ETag", etag(c.Version))
	writeJSON(w, http.StatusCreated, out)
}

func (a *api) getCollection(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	collectionID, ok := pathID(w, r, "collectionId", id.Collection, "collection")
	if !ok {
		return
	}
	c, err := a.Collections.GetCollection(r.Context(), s.User.ID, collectionID)
	if err != nil {
		a.collectionsError(w, r, "collection", err)
		return
	}
	w.Header().Set("ETag", etag(c.Version))
	writeJSON(w, http.StatusOK, collectionJSON(c))
}

func (a *api) updateCollection(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	collectionID, ok := pathID(w, r, "collectionId", id.Collection, "collection")
	if !ok {
		return
	}
	patch, ok := decodeMergePatch(w, r, "name", "description", "variables")
	if !ok {
		return
	}
	var fields validate.Errors
	change := client.CollectionPatch{Name: patch.str(&fields, "name", false), Description: patch.str(&fields, "description", true)}
	if raw, ok := patch["variables"]; ok {
		var vs []client.CollectionVariable
		if string(raw) != "null" {
			var in []CollectionVariableJSON
			if err := json.Unmarshal(raw, &in); err != nil {
				fields.Add("/variables", "type", "Send the variables as a list of keys and values.")
			}
			for _, v := range in {
				vs = append(vs, client.CollectionVariable(v))
			}
		}
		if vs == nil {
			vs = []client.CollectionVariable{}
		}
		change.Variables = &vs
	}
	if len(fields) > 0 {
		writeInvalid(w, r, fields)
		return
	}
	c, err := a.Collections.UpdateCollection(r.Context(), s.User.ID, collectionID, precondition(r), change)
	if err != nil {
		a.collectionsError(w, r, "collection", err)
		return
	}
	w.Header().Set("ETag", etag(c.Version))
	writeJSON(w, http.StatusOK, collectionJSON(c))
}

func (a *api) deleteCollection(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	collectionID, ok := pathID(w, r, "collectionId", id.Collection, "collection")
	if !ok {
		return
	}
	if err := a.Collections.DeleteCollection(r.Context(), s.User.ID, collectionID, precondition(r)); err != nil {
		a.collectionsError(w, r, "collection", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) listItems(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	collectionID, ok := pathID(w, r, "collectionId", id.Collection, "collection")
	if !ok {
		return
	}
	items, err := a.Collections.Items(r.Context(), s.User.ID, collectionID)
	if err != nil {
		a.collectionsError(w, r, "collection", err)
		return
	}
	out := make([]ItemJSON, len(items))
	for i, it := range items {
		out[i] = itemJSON(it)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

// placementFrom reads parentId and before, item IDs or null.
func placementFrom(fields *validate.Errors, parent, before *string) client.Placement {
	var p client.Placement
	read := func(pointer string, v *string) uuid.UUID {
		if v == nil || *v == "" {
			return uuid.Nil
		}
		u, err := id.Parse(id.Item, *v)
		if err != nil {
			fields.Add(pointer, "pattern", "Use an item ID, such as item_01j9z3w5y7a9c1e3g5j7m9p1r3.")
		}
		return u
	}
	p.Parent = read("/parentId", parent)
	p.Before = read("/before", before)
	return p
}

func (a *api) createItem(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	collectionID, ok := pathID(w, r, "collectionId", id.Collection, "collection")
	if !ok {
		return
	}
	var body struct {
		Kind        *string           `json:"kind"`
		Name        *string           `json:"name"`
		Description string            `json:"description"`
		Request     *SavedRequestJSON `json:"request"`
		Scripts     *ScriptsJSON      `json:"scripts"`
		ParentID    *string           `json:"parentId"`
		Before      *string           `json:"before"`
	}
	if !decodeJSONUpTo(w, r, &body, sendBodyLimit) {
		return
	}
	var fields validate.Errors
	in := client.ItemInput{
		Kind: required(&fields, "/kind", body.Kind), Name: required(&fields, "/name", body.Name), Description: body.Description,
		Request: savedRequest(body.Request), Placement: placementFrom(&fields, body.ParentID, body.Before),
	}
	if sc := scriptsIn(body.Scripts); sc != nil {
		in.Scripts = *sc
	}
	if len(fields) > 0 {
		writeInvalid(w, r, fields)
		return
	}
	it, err := a.Collections.CreateItem(r.Context(), s.User.ID, collectionID, in)
	if err != nil {
		a.collectionsError(w, r, "collection", err)
		return
	}
	out := itemJSON(it)
	w.Header().Set("Location", "/api/v1/items/"+out.ID)
	w.Header().Set("ETag", etag(it.Version))
	writeJSON(w, http.StatusCreated, out)
}

func (a *api) getItem(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	itemID, ok := pathID(w, r, "itemId", id.Item, "item")
	if !ok {
		return
	}
	it, err := a.Collections.GetItem(r.Context(), s.User.ID, itemID)
	if err != nil {
		a.collectionsError(w, r, "item", err)
		return
	}
	w.Header().Set("ETag", etag(it.Version))
	writeJSON(w, http.StatusOK, itemJSON(it))
}

func (a *api) updateItem(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	itemID, ok := pathID(w, r, "itemId", id.Item, "item")
	if !ok {
		return
	}
	var patch mergePatch
	if !decodeJSONUpTo(w, r, &patch, sendBodyLimit, mergePatchMediaType, jsonMediaType) {
		return
	}
	var fields validate.Errors
	for key := range patch {
		switch key {
		case "name", "description", "request", "scripts", "parentId", "before":
		default:
			fields.Add("/"+key, "additionalProperties", "Remove this field; it can't be changed.")
		}
	}
	if len(patch) == 0 {
		fields.Add("", "minProperties", "Send at least one field to change.")
	}
	change := client.ItemPatch{Name: patch.str(&fields, "name", false), Description: patch.str(&fields, "description", true)}
	if raw, ok := patch["scripts"]; ok {
		var sc ScriptsJSON
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if string(raw) != "null" && dec.Decode(&sc) != nil {
			fields.Add("/scripts", "type", "Send the scripts as prerequest and test.")
		}
		change.Scripts = scriptsIn(&sc)
	}
	if raw, ok := patch["request"]; ok {
		var req SavedRequestJSON
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if string(raw) == "null" || dec.Decode(&req) != nil {
			fields.Add("/request", "type", "Send the whole request: method, url, headers and body.")
		} else {
			change.Request = savedRequest(&req)
		}
	}
	_, moveParent := patch["parentId"]
	_, moveBefore := patch["before"]
	if moveParent || moveBefore {
		var parent, before *string
		for key, dst := range map[string]**string{"parentId": &parent, "before": &before} {
			raw, ok := patch[key]
			if !ok || string(raw) == "null" {
				continue
			}
			var v string
			if json.Unmarshal(raw, &v) != nil {
				fields.Add("/"+key, "type", "Send an item ID, or null.")
				continue
			}
			*dst = &v
		}
		p := placementFrom(&fields, parent, before)
		change.Move = &p
	}
	if len(fields) > 0 {
		writeInvalid(w, r, fields)
		return
	}
	it, err := a.Collections.UpdateItem(r.Context(), s.User.ID, itemID, precondition(r), change)
	if err != nil {
		a.collectionsError(w, r, "item", err)
		return
	}
	w.Header().Set("ETag", etag(it.Version))
	writeJSON(w, http.StatusOK, itemJSON(it))
}

func (a *api) deleteItem(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	itemID, ok := pathID(w, r, "itemId", id.Item, "item")
	if !ok {
		return
	}
	if err := a.Collections.DeleteItem(r.Context(), s.User.ID, itemID, precondition(r)); err != nil {
		a.collectionsError(w, r, "item", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
