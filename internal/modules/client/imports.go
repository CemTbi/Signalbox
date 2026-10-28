// SPDX-License-Identifier: Apache-2.0

package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/CemTbi/Signalbox/internal/modules/client/convert"
	"github.com/CemTbi/Signalbox/internal/modules/workspaces"
	"github.com/CemTbi/Signalbox/internal/platform/id"
)

// ImportCollection stores a converted collection in a project, with all
// its folders and requests, in one transaction. Owners, admins and editors
// may.
func (m *Module) ImportCollection(ctx context.Context, userID, projectID uuid.UUID, in convert.Collection) (Collection, error) {
	if err := m.authorizeRole(ctx, userID, projectID, edit); err != nil {
		return Collection{}, err
	}
	vars := make([]CollectionVariable, len(in.Variables))
	for i, v := range in.Variables {
		vars[i] = CollectionVariable{Key: v.Key, Value: v.Value}
	}
	variables, err := json.Marshal(vars)
	if err != nil {
		return Collection{}, err
	}
	scripts, err := json.Marshal(scriptsFrom(in.Scripts))
	if err != nil {
		return Collection{}, err
	}
	var c Collection
	err = pgx.BeginFunc(ctx, m.db, func(tx pgx.Tx) error {
		var err error
		c, err = readCollection(tx.QueryRow(ctx, `
			INSERT INTO collections (id, project_id, name, description, variables, scripts, created_by) VALUES ($1, $2, $3, $4, $5, $6, $7)
			RETURNING `+collectionColumns, id.New(), projectID, in.Name, in.Description, variables, scripts, userID))
		if err != nil {
			return err
		}
		var rows [][]any
		if err := importItems(&rows, c.ID, uuid.Nil, in.Items); err != nil {
			return err
		}
		_, err = tx.CopyFrom(ctx, pgx.Identifier{"collection_items"},
			[]string{"id", "collection_id", "parent_id", "rank", "kind", "name", "description", "request", "scripts"}, pgx.CopyFromRows(rows))
		return err
	})
	if err != nil {
		return Collection{}, fmt.Errorf("import collection: %w", err)
	}
	m.logger.InfoContext(ctx, "imported collection", id.LogAttr("collection_id", id.Collection, c.ID), id.LogAttr("user_id", id.User, userID))
	return c, nil
}

// importItems adds rows for items, parents before their children, which
// the foreign key needs within one COPY.
func importItems(rows *[][]any, collectionID, parent uuid.UUID, items []convert.Item) error {
	rank := ""
	for _, it := range items {
		rank = after(rank)
		itemID := id.New()
		kind, req := KindFolder, []byte(nil)
		if it.Request != nil {
			kind = KindRequest
			var err error
			if req, err = json.Marshal(savedFrom(it.Request)); err != nil {
				return err
			}
		}
		scripts, err := json.Marshal(scriptsFrom(it.Scripts))
		if err != nil {
			return err
		}
		*rows = append(*rows, []any{itemID, collectionID, nullID(parent), rank, kind, it.Name, it.Description, req, scripts})
		if err := importItems(rows, collectionID, itemID, it.Items); err != nil {
			return err
		}
	}
	return nil
}

func scriptsFrom(s convert.Scripts) Scripts {
	return Scripts{PreRequest: s.PreRequest, Test: s.Test}
}

func savedFrom(r *convert.Request) SavedRequest {
	out := SavedRequest{Method: r.Method, URL: r.URL, Body: r.Body, Headers: make([]SavedHeader, len(r.Headers))}
	for i, h := range r.Headers {
		out.Headers[i] = SavedHeader(h)
	}
	if a := r.Auth; a != nil {
		auth := Auth(*a)
		out.Auth = &auth
	}
	return out
}

// ExportCollection returns a collection with all its folders and requests,
// as convert takes it. Every member of the workspace may.
func (m *Module) ExportCollection(ctx context.Context, userID, collectionID uuid.UUID) (convert.Collection, error) {
	c, err := m.GetCollection(ctx, userID, collectionID)
	if err != nil {
		return convert.Collection{}, err
	}
	rows, err := m.db.Query(ctx, `SELECT `+itemColumns+` FROM collection_items WHERE collection_id = $1 ORDER BY parent_id NULLS FIRST, rank`, collectionID)
	if err != nil {
		return convert.Collection{}, fmt.Errorf("export collection: %w", err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Item, error) { return readItem(row) })
	if err != nil {
		return convert.Collection{}, fmt.Errorf("export collection: %w", err)
	}
	children := map[uuid.UUID][]Item{}
	for _, it := range items {
		children[it.ParentID] = append(children[it.ParentID], it)
	}
	out := convert.Collection{Name: c.Name, Description: c.Description, Scripts: convert.Scripts(c.Scripts), Items: exportItems(children, uuid.Nil)}
	for _, v := range c.Variables {
		out.Variables = append(out.Variables, convert.Variable{Key: v.Key, Value: v.Value})
	}
	return out, nil
}

func exportItems(children map[uuid.UUID][]Item, parent uuid.UUID) []convert.Item {
	list := children[parent]
	out := make([]convert.Item, len(list))
	for i, it := range list {
		e := convert.Item{Name: it.Name, Description: it.Description, Scripts: convert.Scripts(it.Scripts)}
		if r := it.Request; r != nil {
			req := &convert.Request{Method: r.Method, URL: r.URL, Body: r.Body, Headers: make([]convert.Header, len(r.Headers))}
			for j, h := range r.Headers {
				req.Headers[j] = convert.Header(h)
			}
			if r.Auth != nil {
				a := convert.Auth(*r.Auth)
				req.Auth = &a
			}
			e.Request = req
		} else {
			e.Items = exportItems(children, it.ID)
		}
		out[i] = e
	}
	return out
}

// ImportVariables adds variables to an environment: one with the name of
// a variable it has replaces it in place, and the others go at the end.
// Owners, admins and editors may.
func (m *Module) ImportVariables(ctx context.Context, userID, environmentID uuid.UUID, in []convert.Variable) (Variables, error) {
	for attempt := 0; ; attempt++ {
		current, err := m.GetVariables(ctx, userID, environmentID)
		if err != nil {
			return Variables{}, err
		}
		merged := make([]VariableInput, len(current.Items))
		index := map[string]int{}
		for i, v := range current.Items {
			// A secret without a value keeps the one it has.
			merged[i] = VariableInput{Key: v.Key, Secret: v.Secret}
			if !v.Secret {
				value := v.Value
				merged[i].Value = &value
			}
			index[v.Key] = i
		}
		for _, v := range in {
			value := v.Value
			nv := VariableInput{Key: v.Key, Value: &value, Secret: v.Secret}
			if i, ok := index[v.Key]; ok {
				merged[i] = nv
			} else {
				index[v.Key] = len(merged)
				merged = append(merged, nv)
			}
		}
		out, err := m.PutVariables(ctx, userID, environmentID, workspaces.Precondition{Versions: []int64{current.Version}}, merged)
		// Someone changed them meanwhile: merge into theirs.
		if errors.Is(err, ErrVersionMismatch) && attempt < 2 {
			continue
		}
		return out, err
	}
}

// MayImport reports whether the user may import collections into the
// project: owners, admins and editors may.
func (m *Module) MayImport(ctx context.Context, userID, projectID uuid.UUID) error {
	return m.authorizeRole(ctx, userID, projectID, edit)
}

// MayChangeVariables reports whether the user may change the
// environment's variables: owners, admins and editors may.
func (m *Module) MayChangeVariables(ctx context.Context, userID, environmentID uuid.UUID) error {
	_, err := m.environment(ctx, userID, environmentID, edit)
	return err
}
