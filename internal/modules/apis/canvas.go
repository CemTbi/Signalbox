// SPDX-License-Identifier: Apache-2.0

package apis

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/CemTbi/Signalbox/internal/platform/id"
	"github.com/CemTbi/Signalbox/internal/platform/validate"
)

// The kinds of target that have a place on the canvas of their own.
// Services called by other services are services.
const (
	TargetDatabase = "database"
	TargetExternal = "external"
)

const (
	// maxTargetName matches the column's limit, which keeps names short
	// enough for its index.
	maxTargetName = 1024
	// maxDependencies bounds a project's declared dependencies, so listing
	// them needs no pages.
	maxDependencies = 1000
)

// TargetPlacement is where a database or an outside host goes on a canvas.
// They come from traffic, by name, so nothing but their place is stored.
type TargetPlacement struct {
	Kind     string
	Name     string
	Position Position
}

// Dependency is a line someone declared between two services, because
// traces haven't shown it, or can't.
type Dependency struct {
	ID        uuid.UUID
	ProjectID uuid.UUID
	From, To  uuid.UUID
	CreatedAt time.Time
}

// CanvasTargets returns the databases and outside hosts someone placed on
// a project's canvas. Every member of the workspace may read them.
func (m *Module) CanvasTargets(ctx context.Context, userID, projectID uuid.UUID) ([]TargetPlacement, error) {
	if err := m.authorize(ctx, userID, projectID, read); err != nil {
		return nil, err
	}
	rows, err := m.db.Query(ctx, `
		SELECT kind, name, position_x, position_y FROM canvas_targets
		WHERE project_id = $1 ORDER BY kind, name`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list canvas targets: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (TargetPlacement, error) {
		var t TargetPlacement
		err := row.Scan(&t.Kind, &t.Name, &t.Position.X, &t.Position.Y)
		return t, err
	})
	if err != nil {
		return nil, fmt.Errorf("list canvas targets: %w", err)
	}
	return out, nil
}

// PlaceOnCanvas moves services, databases and outside hosts on their
// project's canvas, all or nothing. Owners, admins and editors may. Layout
// isn't an edit of the services: their versions stay, so moving a node
// never conflicts with someone renaming it, and the last move wins.
// ErrNotFound means a service isn't in the project.
func (m *Module) PlaceOnCanvas(ctx context.Context, userID, projectID uuid.UUID, placements []Placement, targets []TargetPlacement) error {
	var errs validate.Errors
	switch {
	case len(placements)+len(targets) == 0:
		errs.Add("/positions", "minItems", "Send at least one position.")
	case len(placements) > maxPlacements:
		errs.Add("/positions", "maxItems", fmt.Sprintf("Send at most %d positions at once.", maxPlacements))
	case len(targets) > maxPlacements:
		errs.Add("/targets", "maxItems", fmt.Sprintf("Send at most %d targets at once.", maxPlacements))
	}
	ids := make([]uuid.UUID, len(placements))
	xs := make([]float64, len(placements))
	ys := make([]float64, len(placements))
	seen := map[uuid.UUID]bool{}
	for i, p := range placements {
		pointer := fmt.Sprintf("/positions/%d", i)
		if seen[p.ServiceID] {
			errs.Add(pointer+"/serviceId", "uniqueItems", "Send each service once.")
		}
		seen[p.ServiceID] = true
		var perrs validate.Errors
		position(&perrs, p.Position)
		for _, e := range perrs {
			errs.Add(pointer, e.Code, e.Detail)
		}
		ids[i], xs[i], ys[i] = p.ServiceID, p.Position.X, p.Position.Y
	}
	kinds := make([]string, len(targets))
	names := make([]string, len(targets))
	txs := make([]float64, len(targets))
	tys := make([]float64, len(targets))
	seenTargets := map[string]bool{}
	for i, t := range targets {
		pointer := fmt.Sprintf("/targets/%d", i)
		if t.Kind != TargetDatabase && t.Kind != TargetExternal {
			errs.Add(pointer+"/kind", "enum", "Use database or external.")
		}
		switch {
		case strings.TrimSpace(t.Name) == "":
			errs.Add(pointer+"/name", "required", "Name the database or host.")
		case len(t.Name) > maxTargetName:
			errs.Add(pointer+"/name", "maxLength", fmt.Sprintf("Use at most %d bytes.", maxTargetName))
		}
		key := t.Kind + "\x00" + t.Name
		if seenTargets[key] {
			errs.Add(pointer+"/name", "uniqueItems", "Send each target once.")
		}
		seenTargets[key] = true
		var perrs validate.Errors
		position(&perrs, t.Position)
		for _, e := range perrs {
			errs.Add(pointer, e.Code, e.Detail)
		}
		kinds[i], names[i], txs[i], tys[i] = t.Kind, t.Name, t.Position.X, t.Position.Y
	}
	if err := errs.Err(); err != nil {
		return err
	}
	if err := m.authorize(ctx, userID, projectID, edit); err != nil {
		return err
	}
	err := pgx.BeginFunc(ctx, m.db, func(tx pgx.Tx) error {
		if len(placements) > 0 {
			tag, err := tx.Exec(ctx, `
				UPDATE services s SET position_x = p.x, position_y = p.y
				FROM unnest($2::uuid[], $3::float8[], $4::float8[]) AS p(id, x, y)
				WHERE s.id = p.id AND s.project_id = $1`, projectID, ids, xs, ys)
			if err != nil {
				return err
			}
			// All or nothing: a service from elsewhere undoes the rest.
			if tag.RowsAffected() != int64(len(placements)) {
				return ErrNotFound
			}
		}
		if len(targets) > 0 {
			if _, err := tx.Exec(ctx, `
				INSERT INTO canvas_targets (project_id, kind, name, position_x, position_y)
				SELECT $1, t.kind, t.name, t.x, t.y
				FROM unnest($2::text[], $3::text[], $4::float8[], $5::float8[]) AS t(kind, name, x, y)
				ON CONFLICT (project_id, kind, name) DO UPDATE
				SET position_x = excluded.position_x, position_y = excluded.position_y, updated_at = now()`,
				projectID, kinds, names, txs, tys); err != nil {
				return err
			}
		}
		return nil
	})
	if errors.Is(err, ErrNotFound) {
		return err
	}
	if err != nil {
		return fmt.Errorf("place on the canvas: %w", err)
	}
	return nil
}

const dependencyColumns = `id, project_id, from_service_id, to_service_id, created_at`

func readDependency(row pgx.Row) (Dependency, error) {
	var d Dependency
	err := row.Scan(&d.ID, &d.ProjectID, &d.From, &d.To, &d.CreatedAt)
	return d, err
}

// ListDependencies lists the dependencies declared in a project, oldest
// first. Every member of the workspace may.
func (m *Module) ListDependencies(ctx context.Context, userID, projectID uuid.UUID) ([]Dependency, error) {
	if err := m.authorize(ctx, userID, projectID, read); err != nil {
		return nil, err
	}
	rows, err := m.db.Query(ctx, `SELECT `+dependencyColumns+` FROM canvas_dependencies WHERE project_id = $1 ORDER BY id`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list dependencies: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Dependency, error) { return readDependency(row) })
	if err != nil {
		return nil, fmt.Errorf("list dependencies: %w", err)
	}
	return out, nil
}

// CreateDependency declares that one service depends on another. Owners,
// admins and editors may. ErrNotFound means a service isn't in the project,
// and ErrDependencyExists that the line is already declared.
func (m *Module) CreateDependency(ctx context.Context, userID, projectID, from, to uuid.UUID) (Dependency, error) {
	if from == to {
		var errs validate.Errors
		errs.Add("/toServiceId", "different", "Choose two different services.")
		return Dependency{}, errs.Err()
	}
	if err := m.authorize(ctx, userID, projectID, edit); err != nil {
		return Dependency{}, err
	}
	var d Dependency
	err := pgx.BeginFunc(ctx, m.db, func(tx pgx.Tx) error {
		var services, declared int
		if err := tx.QueryRow(ctx, `
			SELECT (SELECT count(*) FROM services WHERE project_id = $1 AND id = ANY($2::uuid[])),
			       (SELECT count(*) FROM canvas_dependencies WHERE project_id = $1)`,
			projectID, []uuid.UUID{from, to}).Scan(&services, &declared); err != nil {
			return err
		}
		if services != 2 {
			return ErrNotFound
		}
		if declared >= maxDependencies {
			var errs validate.Errors
			errs.Add("/fromServiceId", "maxItems", fmt.Sprintf("A project can declare at most %d dependencies. Remove one first.", maxDependencies))
			return errs.Err()
		}
		var err error
		d, err = readDependency(tx.QueryRow(ctx, `
			INSERT INTO canvas_dependencies (id, project_id, from_service_id, to_service_id, created_by)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (project_id, from_service_id, to_service_id) DO NOTHING
			RETURNING `+dependencyColumns, id.New(), projectID, from, to, userID))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrDependencyExists
		}
		return err
	})
	var fields validate.Errors
	if isModuleError(err) || errors.As(err, &fields) {
		return Dependency{}, err
	}
	if err != nil {
		return Dependency{}, fmt.Errorf("declare a dependency: %w", err)
	}
	return d, nil
}

// DeleteDependency removes a declared dependency. Owners, admins and
// editors may.
func (m *Module) DeleteDependency(ctx context.Context, userID, dependencyID uuid.UUID) error {
	if err := m.authorizeFor(ctx, userID, `SELECT project_id FROM canvas_dependencies WHERE id = $1`, dependencyID, edit); err != nil {
		return err
	}
	if _, err := m.db.Exec(ctx, `DELETE FROM canvas_dependencies WHERE id = $1`, dependencyID); err != nil {
		return fmt.Errorf("remove a dependency: %w", err)
	}
	return nil
}
