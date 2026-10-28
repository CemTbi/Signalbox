// SPDX-License-Identifier: Apache-2.0

package client

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/CemTbi/Signalbox/internal/modules/client/convert"
	"github.com/CemTbi/Signalbox/internal/modules/runner"
)

// PlanRun returns the plan for running a collection, or one folder of it
// when folderID isn't uuid.Nil: its requests depth first, in the order the
// sidebar shows, each with the scripts of the collection and the folders
// above it. Running sends requests, so owners, admins and editors may.
func (m *Module) PlanRun(ctx context.Context, userID, collectionID, folderID uuid.UUID) (Collection, Plan, error) {
	if err := m.authorizeFor(ctx, userID, projectOfCollection, collectionID, edit); err != nil {
		return Collection{}, Plan{}, err
	}
	col, err := readCollection(m.db.QueryRow(ctx, `SELECT `+collectionColumns+` FROM collections WHERE id = $1`, collectionID))
	if err != nil {
		return Collection{}, Plan{}, fmt.Errorf("get collection: %w", err)
	}
	rows, err := m.db.Query(ctx, `
		SELECT id, collection_id, coalesce(parent_id, '00000000-0000-0000-0000-000000000000'), rank, kind, name, description,
			request, scripts, version, created_at, updated_at
		FROM collection_items WHERE collection_id = $1 ORDER BY rank`, collectionID)
	if err != nil {
		return Collection{}, Plan{}, fmt.Errorf("list items: %w", err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Item, error) { return readItem(row) })
	if err != nil {
		return Collection{}, Plan{}, fmt.Errorf("list items: %w", err)
	}
	p := planOf(col, items, folderID)
	if p == nil {
		return Collection{}, Plan{}, ErrNotFound
	}
	return col, *p, nil
}

// planOf builds the plan from a collection's items, sorted by rank. It
// returns nil when folderID isn't one of the collection's folders.
func planOf(col Collection, items []Item, folderID uuid.UUID) *Plan {
	byID := map[uuid.UUID]Item{}
	children := map[uuid.UUID][]Item{}
	for _, it := range items {
		byID[it.ID] = it
		children[it.ParentID] = append(children[it.ParentID], it)
	}
	p := &Plan{Name: col.Name, Variables: map[string]string{}}
	for _, v := range col.Variables {
		p.Variables[v.Key] = v.Value
	}
	top := []Link{{From: col.Name, Scripts: col.Scripts}}
	var path []string
	if folderID != uuid.Nil {
		f, ok := byID[folderID]
		if !ok || f.Kind != KindFolder {
			return nil
		}
		// The folders above it, outermost first.
		var above []Item
		for at := f; ; {
			above = append([]Item{at}, above...)
			parent, ok := byID[at.ParentID]
			if !ok {
				break
			}
			at = parent
		}
		for _, a := range above {
			top = append(top, Link{From: a.Name, Scripts: a.Scripts})
			path = append(path, a.Name)
		}
		p.Name = f.Name
	}
	var walk func(parent uuid.UUID, chain []Link, path []string)
	walk = func(parent uuid.UUID, chain []Link, path []string) {
		for _, it := range children[parent] {
			links := append(chain[:len(chain):len(chain)], Link{From: it.Name, Scripts: it.Scripts})
			if it.Kind == KindFolder {
				walk(it.ID, links, append(path[:len(path):len(path)], it.Name))
				continue
			}
			if it.Request == nil {
				continue
			}
			p.Steps = append(p.Steps, Step{
				ItemID: it.ID, Name: it.Name, Path: strings.Join(path, " / "),
				Request: it.Request.toRunner(), Auth: it.Request.Auth, Chain: links, Contract: it.Request.Contract,
			})
		}
	}
	walk(folderID, top, path)
	return p
}

// toRunner is the request as the runner sends it: ticked headers only.
func (r SavedRequest) toRunner() runner.Request {
	out := runner.Request{Method: r.Method, URL: r.URL}
	for _, h := range r.Headers {
		if h.Enabled && strings.TrimSpace(h.Key) != "" {
			out.Headers = append(out.Headers, runner.Header{Key: h.Key, Value: h.Value})
		}
	}
	if r.Body != "" {
		out.Body = []byte(r.Body)
	}
	return out
}

// RunEnvironment is an environment as a run uses it.
type RunEnvironment struct {
	Name   string
	Values map[string]string
	// Secrets names the variables that are secret.
	Secrets []string
}

// RunEnvironment returns an environment's variables for a run. The
// environment must be the project's.
func (m *Module) RunEnvironment(ctx context.Context, userID, projectID, environmentID uuid.UUID) (RunEnvironment, error) {
	env, err := m.environment(ctx, userID, environmentID, read)
	if err != nil {
		return RunEnvironment{}, err
	}
	if env.ProjectID != projectID {
		return RunEnvironment{}, ErrNotFound
	}
	values, secrets, err := m.environmentValues(ctx, env)
	if err != nil {
		return RunEnvironment{}, err
	}
	return RunEnvironment{Name: env.Name, Values: values, Secrets: secrets}, nil
}

// ErrNoFolder means a converted collection has no folder of the name asked
// for.
var ErrNoFolder = errors.New("client: no folder of that name")

// PlanCollection returns the plan for running a converted collection, such
// as a Postman file read offline, or the first folder in it, depth first,
// named folder. It runs as PlanRun's plans do, without a database.
func PlanCollection(c convert.Collection, folder string) (Plan, error) {
	p := Plan{Name: c.Name, Variables: map[string]string{}}
	for _, v := range c.Variables {
		p.Variables[v.Key] = v.Value
	}
	top := []Link{{From: c.Name, Scripts: scriptsFrom(c.Scripts)}}
	items := c.Items
	var path []string
	if folder != "" {
		var find func(items []convert.Item, chain []Link, path []string) bool
		find = func(list []convert.Item, chain []Link, at []string) bool {
			for _, it := range list {
				if it.Request != nil {
					continue
				}
				links := append(chain[:len(chain):len(chain)], Link{From: it.Name, Scripts: scriptsFrom(it.Scripts)})
				here := append(at[:len(at):len(at)], it.Name)
				if it.Name == folder {
					top, items, path, p.Name = links, it.Items, here, it.Name
					return true
				}
				if find(it.Items, links, here) {
					return true
				}
			}
			return false
		}
		if !find(c.Items, top, nil) {
			return Plan{}, ErrNoFolder
		}
	}
	var walk func(list []convert.Item, chain []Link, path []string)
	walk = func(list []convert.Item, chain []Link, path []string) {
		for _, it := range list {
			links := append(chain[:len(chain):len(chain)], Link{From: it.Name, Scripts: scriptsFrom(it.Scripts)})
			if it.Request == nil {
				walk(it.Items, links, append(path[:len(path):len(path)], it.Name))
				continue
			}
			saved := savedFrom(it.Request)
			p.Steps = append(p.Steps, Step{Name: it.Name, Path: strings.Join(path, " / "), Request: saved.toRunner(), Auth: saved.Auth, Chain: links})
		}
	}
	walk(items, top, path)
	return p, nil
}
