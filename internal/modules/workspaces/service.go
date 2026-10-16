// SPDX-License-Identifier: Apache-2.0

package workspaces

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/CemTbi/Signalbox/internal/platform/id"
	"github.com/CemTbi/Signalbox/internal/platform/postgres"
	"github.com/CemTbi/Signalbox/internal/platform/validate"
)

// WorkspaceInput is a new workspace.
type WorkspaceInput struct {
	Name string
	Slug string
}

// WorkspacePatch changes a workspace. Nil fields keep their value.
type WorkspacePatch struct {
	Name *string
	Slug *string
}

// ProjectInput is a new project.
type ProjectInput struct {
	Name        string
	Description string
}

// ProjectPatch changes a project. Nil fields keep their value; an empty
// description removes it.
type ProjectPatch struct {
	Name        *string
	Description *string
}

// defaultEnvironments are created with every project, in switcher order.
var defaultEnvironments = []struct{ name, slug, kind string }{
	{"Development", "development", "development"},
	{"Staging", "staging", "staging"},
	{"Production", "production", "production"},
}

type scanner interface {
	Scan(dest ...any) error
}

const workspaceColumns = `w.id, w.name, w.slug, m.role, w.version, w.created_at, w.updated_at`

func readWorkspace(row scanner) (Workspace, error) {
	var w Workspace
	err := row.Scan(&w.ID, &w.Name, &w.Slug, &w.Role, &w.Version, &w.CreatedAt, &w.UpdatedAt)
	return w, err
}

const projectColumns = `p.id, p.workspace_id, p.name, p.description, p.version, p.created_at, p.updated_at`

func readProject(row scanner) (Project, error) {
	var p Project
	err := row.Scan(&p.ID, &p.WorkspaceID, &p.Name, &p.Description, &p.Version, &p.CreatedAt, &p.UpdatedAt)
	return p, err
}

// ListWorkspaces lists the workspaces the user is a member of, oldest first.
func (s *Service) ListWorkspaces(ctx context.Context, userID uuid.UUID, p PageRequest) (Page[Workspace], error) {
	limit := p.limit()
	rows, err := s.db.Query(ctx, `
		SELECT `+workspaceColumns+`
		FROM workspace_members m JOIN workspaces w ON w.id = m.workspace_id
		WHERE m.user_id = $1 AND ($2::uuid IS NULL OR m.workspace_id > $2)
		ORDER BY m.workspace_id
		LIMIT $3`, userID, afterArg(p.After), limit+1)
	if err != nil {
		return Page[Workspace]{}, fmt.Errorf("list workspaces: %w", err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Workspace, error) { return readWorkspace(row) })
	if err != nil {
		return Page[Workspace]{}, fmt.Errorf("list workspaces: %w", err)
	}
	return page(items, limit), nil
}

// CreateWorkspace creates a workspace with the user as its owner.
func (s *Service) CreateWorkspace(ctx context.Context, userID uuid.UUID, in WorkspaceInput) (Workspace, error) {
	var errs validate.Errors
	w := Workspace{
		ID:   id.New(),
		Name: validate.Name(&errs, "/name", in.Name),
		Slug: validate.Slug(&errs, "/slug", in.Slug),
		Role: Owner,
	}
	if err := errs.Err(); err != nil {
		return Workspace{}, err
	}
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
			INSERT INTO workspaces (id, name, slug) VALUES ($1, $2, $3)
			RETURNING version, created_at, updated_at`, w.ID, w.Name, w.Slug,
		).Scan(&w.Version, &w.CreatedAt, &w.UpdatedAt)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO workspace_members (workspace_id, user_id, role) VALUES ($1, $2, $3)`, w.ID, userID, Owner)
		return err
	})
	if postgres.IsUniqueViolation(err, "workspaces_slug_key") {
		return Workspace{}, ErrSlugTaken
	}
	if err != nil {
		return Workspace{}, fmt.Errorf("create workspace: %w", err)
	}
	s.logger.InfoContext(ctx, "created workspace", id.LogAttr("workspace_id", id.Workspace, w.ID), id.LogAttr("user_id", id.User, userID))
	return w, nil
}

// GetWorkspace returns a workspace the user is a member of.
func (s *Service) GetWorkspace(ctx context.Context, userID, workspaceID uuid.UUID) (Workspace, error) {
	return getWorkspace(ctx, s.db, userID, workspaceID, "")
}

func getWorkspace(ctx context.Context, q querier, userID, workspaceID uuid.UUID, lock string) (Workspace, error) {
	w, err := readWorkspace(q.QueryRow(ctx, `
		SELECT `+workspaceColumns+`
		FROM workspaces w JOIN workspace_members m ON m.workspace_id = w.id AND m.user_id = $2
		WHERE w.id = $1 `+lock, workspaceID, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Workspace{}, ErrNotFound
	}
	if err != nil {
		return Workspace{}, fmt.Errorf("get workspace: %w", err)
	}
	return w, nil
}

// UpdateWorkspace changes a workspace's name or slug. Owners and admins may.
func (s *Service) UpdateWorkspace(ctx context.Context, userID, workspaceID uuid.UUID, pre Precondition, patch WorkspacePatch) (Workspace, error) {
	var errs validate.Errors
	if patch.Name != nil {
		v := validate.Name(&errs, "/name", *patch.Name)
		patch.Name = &v
	}
	if patch.Slug != nil {
		v := validate.Slug(&errs, "/slug", *patch.Slug)
		patch.Slug = &v
	}
	if err := errs.Err(); err != nil {
		return Workspace{}, err
	}

	var w Workspace
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var err error
		if w, err = getWorkspace(ctx, tx, userID, workspaceID, "FOR UPDATE OF w"); err != nil {
			return err
		}
		if !w.Role.allows(updateWorkspace) {
			return ErrForbidden
		}
		if !pre.matches(w.Version) {
			return ErrVersionMismatch
		}
		if patch.Name != nil {
			w.Name = *patch.Name
		}
		if patch.Slug != nil {
			w.Slug = *patch.Slug
		}
		return tx.QueryRow(ctx, `
			UPDATE workspaces SET name = $2, slug = $3, version = version + 1, updated_at = now()
			WHERE id = $1 RETURNING version, updated_at`, workspaceID, w.Name, w.Slug,
		).Scan(&w.Version, &w.UpdatedAt)
	})
	switch {
	case postgres.IsUniqueViolation(err, "workspaces_slug_key"):
		return Workspace{}, ErrSlugTaken
	case isServiceError(err):
		return Workspace{}, err
	case err != nil:
		return Workspace{}, fmt.Errorf("update workspace: %w", err)
	}
	s.logger.InfoContext(ctx, "updated workspace", id.LogAttr("workspace_id", id.Workspace, workspaceID), id.LogAttr("user_id", id.User, userID))
	return w, nil
}

// DeleteWorkspace deletes a workspace with everything in it. Only owners may.
func (s *Service) DeleteWorkspace(ctx context.Context, userID, workspaceID uuid.UUID, pre Precondition) error {
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		w, err := getWorkspace(ctx, tx, userID, workspaceID, "FOR UPDATE OF w")
		if err != nil {
			return err
		}
		if !w.Role.allows(deleteWorkspace) {
			return ErrForbidden
		}
		if !pre.matches(w.Version) {
			return ErrVersionMismatch
		}
		_, err = tx.Exec(ctx, `DELETE FROM workspaces WHERE id = $1`, workspaceID)
		return err
	})
	switch {
	case isServiceError(err):
		return err
	case err != nil:
		return fmt.Errorf("delete workspace: %w", err)
	}
	s.logger.InfoContext(ctx, "deleted workspace", id.LogAttr("workspace_id", id.Workspace, workspaceID), id.LogAttr("user_id", id.User, userID))
	return nil
}

// role returns the user's role in the workspace, or ErrNotFound if they
// aren't a member. Inside a transaction, lock with "FOR SHARE" so the
// membership can't be removed until it commits.
func role(ctx context.Context, q querier, userID, workspaceID uuid.UUID, lock string) (Role, error) {
	var r Role
	err := q.QueryRow(ctx, `SELECT role FROM workspace_members WHERE workspace_id = $1 AND user_id = $2 `+lock,
		workspaceID, userID).Scan(&r)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("look up membership: %w", err)
	}
	return r, nil
}

// ListProjects lists a workspace's projects, oldest first.
func (s *Service) ListProjects(ctx context.Context, userID, workspaceID uuid.UUID, p PageRequest) (Page[Project], error) {
	if _, err := role(ctx, s.db, userID, workspaceID, ""); err != nil {
		return Page[Project]{}, err
	}
	limit := p.limit()
	rows, err := s.db.Query(ctx, `
		SELECT `+projectColumns+`
		FROM projects p
		WHERE p.workspace_id = $1 AND ($2::uuid IS NULL OR p.id > $2)
		ORDER BY p.id
		LIMIT $3`, workspaceID, afterArg(p.After), limit+1)
	if err != nil {
		return Page[Project]{}, fmt.Errorf("list projects: %w", err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Project, error) { return readProject(row) })
	if err != nil {
		return Page[Project]{}, fmt.Errorf("list projects: %w", err)
	}
	return page(items, limit), nil
}

// CreateProject creates a project with development, staging and production
// environments. Owners, admins and editors may.
func (s *Service) CreateProject(ctx context.Context, userID, workspaceID uuid.UUID, in ProjectInput) (Project, error) {
	var errs validate.Errors
	p := Project{
		ID:          id.New(),
		WorkspaceID: workspaceID,
		Name:        validate.Name(&errs, "/name", in.Name),
		Description: validate.Description(&errs, "/description", in.Description),
	}
	if err := errs.Err(); err != nil {
		return Project{}, err
	}
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		r, err := role(ctx, tx, userID, workspaceID, "FOR SHARE")
		if err != nil {
			return err
		}
		if !r.allows(createProject) {
			return ErrForbidden
		}
		err = tx.QueryRow(ctx, `
			INSERT INTO projects (id, workspace_id, name, description) VALUES ($1, $2, $3, $4)
			RETURNING version, created_at, updated_at`, p.ID, p.WorkspaceID, p.Name, p.Description,
		).Scan(&p.Version, &p.CreatedAt, &p.UpdatedAt)
		if err != nil {
			return err
		}
		batch := &pgx.Batch{}
		for i, env := range defaultEnvironments {
			batch.Queue(`INSERT INTO environments (id, project_id, name, slug, kind, position) VALUES ($1, $2, $3, $4, $5, $6)`,
				id.New(), p.ID, env.name, env.slug, env.kind, i)
		}
		return tx.SendBatch(ctx, batch).Close()
	})
	switch {
	case isServiceError(err):
		return Project{}, err
	case err != nil:
		return Project{}, fmt.Errorf("create project: %w", err)
	}
	s.logger.InfoContext(ctx, "created project", id.LogAttr("project_id", id.Project, p.ID), id.LogAttr("workspace_id", id.Workspace, workspaceID), id.LogAttr("user_id", id.User, userID))
	return p, nil
}

// GetProject returns a project in a workspace the user is a member of.
func (s *Service) GetProject(ctx context.Context, userID, projectID uuid.UUID) (Project, error) {
	p, _, err := getProject(ctx, s.db, userID, projectID, "")
	return p, err
}

func getProject(ctx context.Context, q querier, userID, projectID uuid.UUID, lock string) (Project, Role, error) {
	var (
		p Project
		r Role
	)
	err := q.QueryRow(ctx, `
		SELECT `+projectColumns+`, m.role
		FROM projects p JOIN workspace_members m ON m.workspace_id = p.workspace_id AND m.user_id = $2
		WHERE p.id = $1 `+lock, projectID, userID,
	).Scan(&p.ID, &p.WorkspaceID, &p.Name, &p.Description, &p.Version, &p.CreatedAt, &p.UpdatedAt, &r)
	if errors.Is(err, pgx.ErrNoRows) {
		return Project{}, "", ErrNotFound
	}
	if err != nil {
		return Project{}, "", fmt.Errorf("get project: %w", err)
	}
	return p, r, nil
}

// UpdateProject changes a project's name or description. Owners, admins and
// editors may.
func (s *Service) UpdateProject(ctx context.Context, userID, projectID uuid.UUID, pre Precondition, patch ProjectPatch) (Project, error) {
	var errs validate.Errors
	if patch.Name != nil {
		v := validate.Name(&errs, "/name", *patch.Name)
		patch.Name = &v
	}
	if patch.Description != nil {
		v := validate.Description(&errs, "/description", *patch.Description)
		patch.Description = &v
	}
	if err := errs.Err(); err != nil {
		return Project{}, err
	}

	var p Project
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var (
			r   Role
			err error
		)
		if p, r, err = getProject(ctx, tx, userID, projectID, "FOR UPDATE OF p"); err != nil {
			return err
		}
		if !r.allows(updateProject) {
			return ErrForbidden
		}
		if !pre.matches(p.Version) {
			return ErrVersionMismatch
		}
		if patch.Name != nil {
			p.Name = *patch.Name
		}
		if patch.Description != nil {
			p.Description = *patch.Description
		}
		return tx.QueryRow(ctx, `
			UPDATE projects SET name = $2, description = $3, version = version + 1, updated_at = now()
			WHERE id = $1 RETURNING version, updated_at`, projectID, p.Name, p.Description,
		).Scan(&p.Version, &p.UpdatedAt)
	})
	switch {
	case isServiceError(err):
		return Project{}, err
	case err != nil:
		return Project{}, fmt.Errorf("update project: %w", err)
	}
	s.logger.InfoContext(ctx, "updated project", id.LogAttr("project_id", id.Project, projectID), id.LogAttr("user_id", id.User, userID))
	return p, nil
}

// DeleteProject deletes a project with its environments. Owners and admins
// may.
func (s *Service) DeleteProject(ctx context.Context, userID, projectID uuid.UUID, pre Precondition) error {
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		p, r, err := getProject(ctx, tx, userID, projectID, "FOR UPDATE OF p")
		if err != nil {
			return err
		}
		if !r.allows(deleteProject) {
			return ErrForbidden
		}
		if !pre.matches(p.Version) {
			return ErrVersionMismatch
		}
		_, err = tx.Exec(ctx, `DELETE FROM projects WHERE id = $1`, projectID)
		return err
	})
	switch {
	case isServiceError(err):
		return err
	case err != nil:
		return fmt.Errorf("delete project: %w", err)
	}
	s.logger.InfoContext(ctx, "deleted project", id.LogAttr("project_id", id.Project, projectID), id.LogAttr("user_id", id.User, userID))
	return nil
}

func isServiceError(err error) bool {
	return errors.Is(err, ErrNotFound) || errors.Is(err, ErrForbidden) ||
		errors.Is(err, ErrVersionMismatch) || errors.Is(err, ErrSlugTaken)
}
