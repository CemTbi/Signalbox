// SPDX-License-Identifier: Apache-2.0

package workspaces

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Environment is one of a project's environments, such as staging, where
// requests can point with their own variables.
type Environment struct {
	ID          uuid.UUID
	ProjectID   uuid.UUID
	WorkspaceID uuid.UUID
	Name        string
	Slug        string
	Kind        string
	Position    int
	Version     int64
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

const environmentColumns = `e.id, e.project_id, p.workspace_id, e.name, e.slug, e.kind, e.position, e.version, e.created_at, e.updated_at`

const environmentsFrom = ` FROM environments e JOIN projects p ON p.id = e.project_id `

func readEnvironment(row pgx.Row) (Environment, error) {
	var e Environment
	err := row.Scan(&e.ID, &e.ProjectID, &e.WorkspaceID, &e.Name, &e.Slug, &e.Kind, &e.Position, &e.Version, &e.CreatedAt, &e.UpdatedAt)
	return e, err
}

// ListEnvironments lists a project's environments in switcher order. Every
// member of the workspace may.
func (s *Service) ListEnvironments(ctx context.Context, userID, projectID uuid.UUID) ([]Environment, error) {
	if _, err := s.ProjectRole(ctx, userID, projectID); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, `SELECT `+environmentColumns+environmentsFrom+`WHERE e.project_id = $1 ORDER BY e.position, e.id`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list environments: %w", err)
	}
	envs, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Environment, error) { return readEnvironment(row) })
	if err != nil {
		return nil, fmt.Errorf("list environments: %w", err)
	}
	return envs, nil
}

// GetEnvironment returns an environment, with the user's role in its
// project, so other modules can authorize work in it.
func (s *Service) GetEnvironment(ctx context.Context, userID, environmentID uuid.UUID) (Environment, Role, error) {
	e, err := readEnvironment(s.db.QueryRow(ctx, `SELECT `+environmentColumns+environmentsFrom+`WHERE e.id = $1`, environmentID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Environment{}, "", ErrNotFound
	}
	if err != nil {
		return Environment{}, "", fmt.Errorf("get environment: %w", err)
	}
	role, err := s.ProjectRole(ctx, userID, e.ProjectID)
	if err != nil {
		return Environment{}, "", err
	}
	return e, role, nil
}
