// SPDX-License-Identifier: Apache-2.0

package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/CemTbi/Signalbox/internal/modules/workspaces"
	"github.com/CemTbi/Signalbox/internal/platform/id"
	"github.com/CemTbi/Signalbox/internal/platform/validate"
)

// ErrVersionMismatch means the collection or item changed since the version
// in the precondition.
var ErrVersionMismatch = errors.New("client: version mismatch")

// Limits on collections.
const (
	maxItems       = 10_000
	maxDepth       = 20
	maxItemName    = 200
	maxDescription = 10_000
	maxHeaders     = 200
	maxBody        = 10 << 20
)

// Collection is a project's named set of saved requests, in folders.
type Collection struct {
	ID          uuid.UUID
	ProjectID   uuid.UUID
	Name        string
	Description string
	// Variables fill in {{name}} in the collection's requests; an
	// environment's variables win over them.
	Variables []CollectionVariable
	Scripts   Scripts
	Version   int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

// CollectionVariable is one of a collection's variables. They aren't
// secret: keep secrets in environments.
type CollectionVariable struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Scripts are the JavaScript a collection, folder or request runs before
// a request and after its response, as Postman has them.
type Scripts struct {
	PreRequest string `json:"prerequest,omitempty"`
	Test       string `json:"test,omitempty"`
}

// SavedHeader is a header of a saved request. Unticked headers are kept
// but not sent.
type SavedHeader struct {
	Key     string `json:"key"`
	Value   string `json:"value"`
	Enabled bool   `json:"enabled"`
}

// SavedRequest is a request as a collection keeps it. The URL, headers and
// body may hold {{variables}}.
type SavedRequest struct {
	Method  string        `json:"method"`
	URL     string        `json:"url"`
	Headers []SavedHeader `json:"headers"`
	Body    string        `json:"body"`
	// Auth is nil for none.
	Auth *Auth `json:"auth,omitempty"`
}

// Item kinds.
const (
	KindFolder  = "folder"
	KindRequest = "request"
)

// Item is a folder or a request in a collection.
type Item struct {
	ID           uuid.UUID
	CollectionID uuid.UUID
	// ParentID is the folder it's in; uuid.Nil at the top.
	ParentID    uuid.UUID
	Rank        string
	Kind        string
	Name        string
	Description string
	// Request is set for requests. Lists carry only its method and URL.
	Request *SavedRequest
	// Scripts are empty in lists.
	Scripts   Scripts
	Version   int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Placement says where an item goes: into a folder (uuid.Nil for the top
// of the collection), before one of its items, or at its end when Before
// is uuid.Nil.
type Placement struct {
	Parent uuid.UUID
	Before uuid.UUID
}

// ItemInput is a new item.
type ItemInput struct {
	Kind        string
	Name        string
	Description string
	Request     *SavedRequest
	Placement   Placement
}

// ItemPatch changes the fields that aren't nil.
type ItemPatch struct {
	Name        *string
	Description *string
	Request     *SavedRequest
	Move        *Placement
}

// CollectionPatch changes the fields that aren't nil.
type CollectionPatch struct {
	Name        *string
	Description *string
	Variables   *[]CollectionVariable
}

const (
	projectOfCollection = `SELECT project_id FROM collections WHERE id = $1`
	projectOfItem       = `SELECT c.project_id FROM collection_items i JOIN collections c ON c.id = i.collection_id WHERE i.id = $1`
)

// need says what a user must be allowed to do.
type need int

const (
	read need = iota
	edit
	remove
)

func (m *Module) authorizeRole(ctx context.Context, userID, projectID uuid.UUID, n need) error {
	role, err := m.access.ProjectRole(ctx, userID, projectID)
	if errors.Is(err, workspaces.ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("check access: %w", err)
	}
	switch {
	case n == edit && !role.CanEdit(), n == remove && !role.CanDelete():
		return ErrForbidden
	}
	return nil
}

// authorizeFor finds the project of a collection or item and checks the
// user's role there.
func (m *Module) authorizeFor(ctx context.Context, userID uuid.UUID, query string, resource uuid.UUID, n need) error {
	var project uuid.UUID
	err := m.db.QueryRow(ctx, query, resource).Scan(&project)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return m.authorizeRole(ctx, userID, project, n)
}

func isModuleError(err error) bool {
	var fields validate.Errors
	return errors.Is(err, ErrNotFound) || errors.Is(err, ErrForbidden) || errors.Is(err, ErrVersionMismatch) || errors.As(err, &fields)
}

func collectionName(errs *validate.Errors, name string) string {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		errs.Add("/name", "required", "Name the collection, such as Orders API.")
	case utf8.RuneCountInString(name) > 100:
		errs.Add("/name", "maxLength", "Use at most 100 characters.")
	}
	return name
}

func description(errs *validate.Errors, d string, limit int) string {
	d = strings.TrimSpace(d)
	if utf8.RuneCountInString(d) > limit {
		errs.Add("/description", "maxLength", fmt.Sprintf("Use at most %d characters.", limit))
	}
	return d
}

func itemName(errs *validate.Errors, name string) string {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		errs.Add("/name", "required", "Name it, such as Get an order.")
	case utf8.RuneCountInString(name) > maxItemName:
		errs.Add("/name", "maxLength", fmt.Sprintf("Use at most %d characters.", maxItemName))
	}
	return name
}

// checkRequest checks a saved request's sizes, which match the send API's.
func checkRequest(errs *validate.Errors, r *SavedRequest) {
	r.Method = strings.ToUpper(strings.TrimSpace(r.Method))
	switch {
	case r.Method == "":
		errs.Add("/request/method", "required", "Choose a method, such as GET.")
	case len(r.Method) > 32:
		errs.Add("/request/method", "maxLength", "Use a method of at most 32 characters.")
	}
	if len(r.URL) > 8192 {
		errs.Add("/request/url", "maxLength", "Use a URL of at most 8,192 characters.")
	}
	if len(r.Headers) > maxHeaders {
		errs.Add("/request/headers", "maxItems", fmt.Sprintf("Keep at most %d headers.", maxHeaders))
	}
	for i, h := range r.Headers {
		if len(h.Key) > 1000 {
			errs.Add(fmt.Sprintf("/request/headers/%d/key", i), "maxLength", "Use a header name of at most 1,000 characters.")
		}
		if len(h.Value) > 65536 {
			errs.Add(fmt.Sprintf("/request/headers/%d/value", i), "maxLength", "Use a header value of at most 64 KiB.")
		}
	}
	if r.Headers == nil {
		r.Headers = []SavedHeader{}
	}
	if len(r.Body) > maxBody {
		errs.Add("/request/body", "maxLength", "Keep a body of at most 10 MiB.")
	}
	r.Auth = checkAuth(errs, "/request/auth", r.Auth)
}

const collectionColumns = `id, project_id, name, description, variables, scripts, version, created_at, updated_at`

func readCollection(row pgx.Row) (Collection, error) {
	var c Collection
	err := row.Scan(&c.ID, &c.ProjectID, &c.Name, &c.Description, &c.Variables, &c.Scripts, &c.Version, &c.CreatedAt, &c.UpdatedAt)
	if c.Variables == nil {
		c.Variables = []CollectionVariable{}
	}
	return c, err
}

// checkCollectionVariables validates a collection's variables: names a
// {{name}} can use, once each, and values of at most 64 KiB.
func checkCollectionVariables(errs *validate.Errors, vs []CollectionVariable) {
	if len(vs) > maxVariables {
		errs.Add("/variables", "maxItems", fmt.Sprintf("Keep at most %d variables in a collection.", maxVariables))
		return
	}
	seen := map[string]bool{}
	for i, v := range vs {
		at := fmt.Sprintf("/variables/%d", i)
		switch {
		case !validKey(v.Key) || utf8.RuneCountInString(v.Key) > maxVariableKey:
			errs.Add(at+"/key", "pattern", "Use a name without spaces or braces that doesn't start with $, such as baseUrl.")
		case seen[v.Key]:
			errs.Add(at+"/key", "unique", "Another variable has this name. Give each variable its own.")
		}
		seen[v.Key] = true
		if utf8.RuneCountInString(v.Value) > maxVariableValue {
			errs.Add(at+"/value", "maxLength", "Use a value of at most 65,536 characters.")
		}
	}
}

// ListCollections lists a project's collections by name. Every member may.
func (m *Module) ListCollections(ctx context.Context, userID, projectID uuid.UUID) ([]Collection, error) {
	if err := m.authorizeRole(ctx, userID, projectID, read); err != nil {
		return nil, err
	}
	rows, err := m.db.Query(ctx, `SELECT `+collectionColumns+` FROM collections WHERE project_id = $1 ORDER BY lower(name), id`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list collections: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Collection, error) { return readCollection(row) })
	if err != nil {
		return nil, fmt.Errorf("list collections: %w", err)
	}
	return out, nil
}

// CreateCollection adds a collection to a project. Owners, admins and
// editors may.
func (m *Module) CreateCollection(ctx context.Context, userID, projectID uuid.UUID, name, desc string) (Collection, error) {
	var errs validate.Errors
	name = collectionName(&errs, name)
	desc = description(&errs, desc, 2000)
	if err := errs.Err(); err != nil {
		return Collection{}, err
	}
	if err := m.authorizeRole(ctx, userID, projectID, edit); err != nil {
		return Collection{}, err
	}
	c, err := readCollection(m.db.QueryRow(ctx, `
		INSERT INTO collections (id, project_id, name, description, created_by) VALUES ($1, $2, $3, $4, $5)
		RETURNING `+collectionColumns, id.New(), projectID, name, desc, userID))
	if err != nil {
		return Collection{}, fmt.Errorf("create collection: %w", err)
	}
	return c, nil
}

// GetCollection returns a collection.
func (m *Module) GetCollection(ctx context.Context, userID, collectionID uuid.UUID) (Collection, error) {
	if err := m.authorizeFor(ctx, userID, projectOfCollection, collectionID, read); err != nil {
		return Collection{}, err
	}
	c, err := readCollection(m.db.QueryRow(ctx, `SELECT `+collectionColumns+` FROM collections WHERE id = $1`, collectionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Collection{}, ErrNotFound
	}
	if err != nil {
		return Collection{}, fmt.Errorf("get collection: %w", err)
	}
	return c, nil
}

// lockCollection locks a collection's row for the transaction, which
// serializes changes to its items' order, and checks the precondition.
func lockCollection(ctx context.Context, tx pgx.Tx, collectionID uuid.UUID, pre *workspaces.Precondition) error {
	var version int64
	err := tx.QueryRow(ctx, `SELECT version FROM collections WHERE id = $1 FOR UPDATE`, collectionID).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if pre != nil && !pre.Matches(version) {
		return ErrVersionMismatch
	}
	return nil
}

// UpdateCollection renames or describes a collection.
func (m *Module) UpdateCollection(ctx context.Context, userID, collectionID uuid.UUID, pre workspaces.Precondition, patch CollectionPatch) (Collection, error) {
	var errs validate.Errors
	if patch.Name != nil {
		*patch.Name = collectionName(&errs, *patch.Name)
	}
	if patch.Description != nil {
		*patch.Description = description(&errs, *patch.Description, 2000)
	}
	var variables []byte
	if patch.Variables != nil {
		checkCollectionVariables(&errs, *patch.Variables)
		if *patch.Variables == nil {
			*patch.Variables = []CollectionVariable{}
		}
		variables, _ = json.Marshal(*patch.Variables)
	}
	if err := errs.Err(); err != nil {
		return Collection{}, err
	}
	if err := m.authorizeFor(ctx, userID, projectOfCollection, collectionID, edit); err != nil {
		return Collection{}, err
	}
	var c Collection
	err := pgx.BeginFunc(ctx, m.db, func(tx pgx.Tx) error {
		if err := lockCollection(ctx, tx, collectionID, &pre); err != nil {
			return err
		}
		var err error
		c, err = readCollection(tx.QueryRow(ctx, `
			UPDATE collections SET name = coalesce($2, name), description = coalesce($3, description),
				variables = coalesce($4, variables), version = version + 1, updated_at = now()
			WHERE id = $1 RETURNING `+collectionColumns, collectionID, patch.Name, patch.Description, variables))
		return err
	})
	if isModuleError(err) {
		return Collection{}, err
	}
	if err != nil {
		return Collection{}, fmt.Errorf("update collection: %w", err)
	}
	return c, nil
}

// DeleteCollection deletes a collection with everything in it. Owners and
// admins may.
func (m *Module) DeleteCollection(ctx context.Context, userID, collectionID uuid.UUID, pre workspaces.Precondition) error {
	if err := m.authorizeFor(ctx, userID, projectOfCollection, collectionID, remove); err != nil {
		return err
	}
	err := pgx.BeginFunc(ctx, m.db, func(tx pgx.Tx) error {
		if err := lockCollection(ctx, tx, collectionID, &pre); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM collections WHERE id = $1`, collectionID)
		return err
	})
	if isModuleError(err) {
		return err
	}
	if err != nil {
		return fmt.Errorf("delete collection: %w", err)
	}
	m.logger.InfoContext(ctx, "deleted collection", id.LogAttr("collection_id", id.Collection, collectionID), id.LogAttr("user_id", id.User, userID))
	return nil
}

const itemColumns = `id, collection_id, coalesce(parent_id, '00000000-0000-0000-0000-000000000000'), rank, kind, name, description, request, scripts, version, created_at, updated_at`

func readItem(row pgx.Row) (Item, error) {
	var it Item
	var req []byte
	err := row.Scan(&it.ID, &it.CollectionID, &it.ParentID, &it.Rank, &it.Kind, &it.Name, &it.Description, &req, &it.Scripts, &it.Version, &it.CreatedAt, &it.UpdatedAt)
	if err != nil {
		return it, err
	}
	if req != nil {
		it.Request = &SavedRequest{}
		if err := json.Unmarshal(req, it.Request); err != nil {
			return it, fmt.Errorf("read request: %w", err)
		}
	}
	return it, nil
}

// Items lists a collection's folders and requests, each folder's items in
// order. Requests carry only their method and URL; GetItem has the rest.
func (m *Module) Items(ctx context.Context, userID, collectionID uuid.UUID) ([]Item, error) {
	if err := m.authorizeFor(ctx, userID, projectOfCollection, collectionID, read); err != nil {
		return nil, err
	}
	rows, err := m.db.Query(ctx, `
		SELECT id, collection_id, coalesce(parent_id, '00000000-0000-0000-0000-000000000000'), rank, kind, name, description,
			CASE WHEN request IS NULL THEN NULL ELSE jsonb_build_object('method', request->'method', 'url', request->'url') END,
			'{}'::jsonb, version, created_at, updated_at
		FROM collection_items WHERE collection_id = $1
		ORDER BY parent_id NULLS FIRST, rank`, collectionID)
	if err != nil {
		return nil, fmt.Errorf("list items: %w", err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Item, error) { return readItem(row) })
	if err != nil {
		return nil, fmt.Errorf("list items: %w", err)
	}
	return items, nil
}

// GetItem returns a folder or a whole request.
func (m *Module) GetItem(ctx context.Context, userID, itemID uuid.UUID) (Item, error) {
	if err := m.authorizeFor(ctx, userID, projectOfItem, itemID, read); err != nil {
		return Item{}, err
	}
	it, err := readItem(m.db.QueryRow(ctx, `SELECT `+itemColumns+` FROM collection_items WHERE id = $1`, itemID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Item{}, ErrNotFound
	}
	if err != nil {
		return Item{}, fmt.Errorf("get item: %w", err)
	}
	return it, nil
}

// CreateItem adds a folder or a request to a collection. Owners, admins and
// editors may.
func (m *Module) CreateItem(ctx context.Context, userID, collectionID uuid.UUID, in ItemInput) (Item, error) {
	var errs validate.Errors
	in.Name = itemName(&errs, in.Name)
	in.Description = description(&errs, in.Description, maxDescription)
	switch in.Kind {
	case KindFolder:
		if in.Request != nil {
			errs.Add("/request", "type", "A folder has no request.")
		}
	case KindRequest:
		if in.Request == nil {
			errs.Add("/request", "required", "Send the request to save.")
		} else {
			checkRequest(&errs, in.Request)
		}
	default:
		errs.Add("/kind", "enum", "Choose folder or request.")
	}
	if err := errs.Err(); err != nil {
		return Item{}, err
	}
	if err := m.authorizeFor(ctx, userID, projectOfCollection, collectionID, edit); err != nil {
		return Item{}, err
	}
	var it Item
	err := pgx.BeginFunc(ctx, m.db, func(tx pgx.Tx) error {
		if err := lockCollection(ctx, tx, collectionID, nil); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM collection_items WHERE collection_id = $1`, collectionID).Scan(&n); err != nil {
			return err
		}
		if n >= maxItems {
			return validate.Errors{{Pointer: "", Code: "maxItems", Detail: fmt.Sprintf("A collection holds at most %d items. Start another one.", maxItems)}}
		}
		rank, err := place(ctx, tx, collectionID, uuid.Nil, in.Placement)
		if err != nil {
			return err
		}
		var req []byte
		if in.Request != nil {
			if req, err = json.Marshal(in.Request); err != nil {
				return err
			}
		}
		it, err = readItem(tx.QueryRow(ctx, `
			INSERT INTO collection_items (id, collection_id, parent_id, rank, kind, name, description, request)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING `+itemColumns,
			id.New(), collectionID, nullID(in.Placement.Parent), rank, in.Kind, in.Name, in.Description, req))
		return err
	})
	if isModuleError(err) {
		return Item{}, err
	}
	if err != nil {
		return Item{}, fmt.Errorf("create item: %w", err)
	}
	return it, nil
}

func nullID(u uuid.UUID) any {
	if u == uuid.Nil {
		return nil
	}
	return u
}

// place finds the rank for an item at p, in a transaction holding the
// collection's lock. moving is the item being moved, or uuid.Nil for a new
// one.
func place(ctx context.Context, tx pgx.Tx, collectionID, moving uuid.UUID, p Placement) (string, error) {
	invalid := func(pointer, detail string) error {
		return validate.Errors{{Pointer: pointer, Code: "invalid", Detail: detail}}
	}
	if p.Parent != uuid.Nil {
		// The parent's kind, how deep it is, and whether the moving item is
		// the parent or one of its ancestors: a folder can't go inside
		// itself.
		var (
			kind  string
			depth int
			cycle bool
		)
		err := tx.QueryRow(ctx, `
			WITH RECURSIVE up AS (
				SELECT id, parent_id, 1 AS depth FROM collection_items WHERE id = $1 AND collection_id = $2
				UNION ALL
				SELECT i.id, i.parent_id, up.depth + 1 FROM collection_items i JOIN up ON i.id = up.parent_id
			)
			SELECT coalesce((SELECT kind FROM collection_items WHERE id = $1 AND collection_id = $2), ''),
				coalesce((SELECT max(depth) FROM up), 0),
				EXISTS (SELECT 1 FROM up WHERE id = $3)`,
			p.Parent, collectionID, moving).Scan(&kind, &depth, &cycle)
		if err != nil {
			return "", err
		}
		switch {
		case kind == "":
			return "", invalid("/parentId", "There's no folder with this ID in the collection.")
		case kind != KindFolder:
			return "", invalid("/parentId", "Requests can't hold items; choose a folder.")
		case cycle:
			return "", invalid("/parentId", "A folder can't go inside itself.")
		}
		// The item lands one level below the parent, and its own subtree
		// comes with it.
		height := 1
		if moving != uuid.Nil {
			if err := tx.QueryRow(ctx, `
				WITH RECURSIVE down AS (
					SELECT id, 1 AS level FROM collection_items WHERE id = $1
					UNION ALL
					SELECT i.id, down.level + 1 FROM collection_items i JOIN down ON i.parent_id = down.id
				)
				SELECT max(level) FROM down`, moving).Scan(&height); err != nil {
				return "", err
			}
		}
		if depth+height > maxDepth {
			return "", invalid("/parentId", fmt.Sprintf("Folders nest at most %d deep.", maxDepth))
		}
	}
	siblings := `collection_id = $1 AND parent_id IS NOT DISTINCT FROM $2 AND id <> $3`
	if p.Before == uuid.Nil {
		var last string
		if err := tx.QueryRow(ctx, `SELECT coalesce(max(rank), '') FROM collection_items WHERE `+siblings,
			collectionID, nullID(p.Parent), moving).Scan(&last); err != nil {
			return "", err
		}
		return rankBetween(last, "")
	}
	var before string
	err := tx.QueryRow(ctx, `SELECT rank FROM collection_items WHERE id = $4 AND `+siblings,
		collectionID, nullID(p.Parent), moving, p.Before).Scan(&before)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", invalid("/before", "There's no item with this ID in that folder.")
	}
	if err != nil {
		return "", err
	}
	var prev string
	if err := tx.QueryRow(ctx, `SELECT coalesce(max(rank), '') FROM collection_items WHERE `+siblings+` AND rank < $4`,
		collectionID, nullID(p.Parent), moving, before).Scan(&prev); err != nil {
		return "", err
	}
	return rankBetween(prev, before)
}

// UpdateItem renames, describes, changes or moves an item. Owners, admins
// and editors may.
func (m *Module) UpdateItem(ctx context.Context, userID, itemID uuid.UUID, pre workspaces.Precondition, patch ItemPatch) (Item, error) {
	var errs validate.Errors
	if patch.Name != nil {
		*patch.Name = itemName(&errs, *patch.Name)
	}
	if patch.Description != nil {
		*patch.Description = description(&errs, *patch.Description, maxDescription)
	}
	if patch.Request != nil {
		checkRequest(&errs, patch.Request)
	}
	if err := errs.Err(); err != nil {
		return Item{}, err
	}
	if err := m.authorizeFor(ctx, userID, projectOfItem, itemID, edit); err != nil {
		return Item{}, err
	}
	var it Item
	err := pgx.BeginFunc(ctx, m.db, func(tx pgx.Tx) error {
		var collectionID uuid.UUID
		var kind string
		var version int64
		err := tx.QueryRow(ctx, `SELECT collection_id, kind, version FROM collection_items WHERE id = $1`, itemID).Scan(&collectionID, &kind, &version)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if patch.Request != nil && kind != KindRequest {
			return validate.Errors{{Pointer: "/request", Code: "type", Detail: "A folder has no request."}}
		}
		if err := lockCollection(ctx, tx, collectionID, nil); err != nil {
			return err
		}
		// Read again under the lock, which orders this change after others.
		if err := tx.QueryRow(ctx, `SELECT version FROM collection_items WHERE id = $1`, itemID).Scan(&version); err != nil {
			return err
		}
		if !pre.Matches(version) {
			return ErrVersionMismatch
		}
		var rank, parent any
		moved := patch.Move != nil
		if moved {
			r, err := place(ctx, tx, collectionID, itemID, *patch.Move)
			if err != nil {
				return err
			}
			rank, parent = r, nullID(patch.Move.Parent)
		}
		var req []byte
		if patch.Request != nil {
			if req, err = json.Marshal(patch.Request); err != nil {
				return err
			}
		}
		it, err = readItem(tx.QueryRow(ctx, `
			UPDATE collection_items SET
				name = coalesce($2, name), description = coalesce($3, description), request = coalesce($4, request),
				parent_id = CASE WHEN $5 THEN $6::uuid ELSE parent_id END, rank = CASE WHEN $5 THEN $7 ELSE rank END,
				version = version + 1, updated_at = now()
			WHERE id = $1 RETURNING `+itemColumns,
			itemID, patch.Name, patch.Description, req, moved, parent, rank))
		return err
	})
	if isModuleError(err) {
		return Item{}, err
	}
	if err != nil {
		return Item{}, fmt.Errorf("update item: %w", err)
	}
	return it, nil
}

// DeleteItem deletes an item, and a folder's contents with it. Owners,
// admins and editors may.
func (m *Module) DeleteItem(ctx context.Context, userID, itemID uuid.UUID, pre workspaces.Precondition) error {
	if err := m.authorizeFor(ctx, userID, projectOfItem, itemID, edit); err != nil {
		return err
	}
	tag, err := m.db.Exec(ctx, `DELETE FROM collection_items WHERE id = $1 AND ($2::bigint[] IS NULL OR version = ANY($2))`, itemID, versionsArg(pre))
	if err != nil {
		return fmt.Errorf("delete item: %w", err)
	}
	if tag.RowsAffected() == 0 {
		var exists bool
		if err := m.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM collection_items WHERE id = $1)`, itemID).Scan(&exists); err == nil && exists {
			return ErrVersionMismatch
		}
		return ErrNotFound
	}
	return nil
}

func versionsArg(p workspaces.Precondition) any {
	if len(p.Versions) == 0 {
		return nil
	}
	return p.Versions
}
