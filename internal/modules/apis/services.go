// SPDX-License-Identifier: Apache-2.0

package apis

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/CemTbi/Signalbox/internal/modules/workspaces"
	"github.com/CemTbi/Signalbox/internal/platform/id"
	"github.com/CemTbi/Signalbox/internal/platform/postgres"
	"github.com/CemTbi/Signalbox/internal/platform/validate"
)

// Service is one service on a project's canvas.
type Service struct {
	ID          uuid.UUID
	ProjectID   uuid.UUID
	Name        string
	Slug        string
	Description string
	// TelemetryServiceName is the service.name in its traces; "" if unset.
	TelemetryServiceName string
	Position             Position
	// Version increments on every change; the API sends it as the ETag.
	Version   int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Position is a node's place on the canvas.
type Position struct {
	X, Y float64
}

// ServiceInput is a new service.
type ServiceInput struct {
	Name                 string
	Slug                 string
	Description          string
	TelemetryServiceName string
	Position             Position
}

// ServicePatch changes the fields that aren't nil.
type ServicePatch struct {
	Name                 *string
	Slug                 *string
	Description          *string
	TelemetryServiceName *string // "" clears it
	Position             *Position
}

const maxTelemetryName = 255

func telemetryName(errs *validate.Errors, v string) string {
	v = strings.TrimSpace(v)
	if utf8.RuneCountInString(v) > maxTelemetryName {
		errs.Add("/telemetryServiceName", "maxLength", fmt.Sprintf("Use at most %d characters.", maxTelemetryName))
	}
	return v
}

func position(errs *validate.Errors, p Position) Position {
	for _, v := range []float64{p.X, p.Y} {
		if math.IsNaN(v) || math.Abs(v) > 1e9 {
			errs.Add("/position", "maximum", "Use coordinates between -1,000,000,000 and 1,000,000,000.")
			break
		}
	}
	return p
}

const serviceColumns = `id, project_id, name, slug, description, coalesce(telemetry_service_name, ''), position_x, position_y, version, created_at, updated_at`

func readService(row pgx.Row) (Service, error) {
	var s Service
	err := row.Scan(&s.ID, &s.ProjectID, &s.Name, &s.Slug, &s.Description, &s.TelemetryServiceName,
		&s.Position.X, &s.Position.Y, &s.Version, &s.CreatedAt, &s.UpdatedAt)
	return s, err
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// ListServices lists a project's services, oldest first. Every member of
// the workspace may.
func (m *Module) ListServices(ctx context.Context, userID, projectID uuid.UUID, p workspaces.PageRequest) (workspaces.Page[Service], error) {
	if err := m.authorize(ctx, userID, projectID, read); err != nil {
		return workspaces.Page[Service]{}, err
	}
	limit := limitOf(p)
	rows, err := m.db.Query(ctx, `
		SELECT `+serviceColumns+` FROM services
		WHERE project_id = $1 AND ($2::uuid IS NULL OR id > $2)
		ORDER BY id LIMIT $3`, projectID, afterArg(p.After), limit+1)
	if err != nil {
		return workspaces.Page[Service]{}, fmt.Errorf("list services: %w", err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Service, error) { return readService(row) })
	if err != nil {
		return workspaces.Page[Service]{}, fmt.Errorf("list services: %w", err)
	}
	return page(items, limit), nil
}

// CreateService adds a service to a project. Owners, admins and editors may.
func (m *Module) CreateService(ctx context.Context, userID, projectID uuid.UUID, in ServiceInput) (Service, error) {
	var errs validate.Errors
	s := Service{
		ID:                   id.New(),
		ProjectID:            projectID,
		Name:                 validate.Name(&errs, "/name", in.Name),
		Slug:                 validate.Slug(&errs, "/slug", in.Slug),
		Description:          validate.Description(&errs, "/description", in.Description),
		TelemetryServiceName: telemetryName(&errs, in.TelemetryServiceName),
		Position:             position(&errs, in.Position),
	}
	if err := errs.Err(); err != nil {
		return Service{}, err
	}
	if err := m.authorize(ctx, userID, projectID, edit); err != nil {
		return Service{}, err
	}
	err := m.db.QueryRow(ctx, `
		INSERT INTO services (id, project_id, name, slug, description, telemetry_service_name, position_x, position_y)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING version, created_at, updated_at`,
		s.ID, s.ProjectID, s.Name, s.Slug, s.Description, nullable(s.TelemetryServiceName), s.Position.X, s.Position.Y,
	).Scan(&s.Version, &s.CreatedAt, &s.UpdatedAt)
	switch {
	case postgres.IsUniqueViolation(err, "services_project_slug_key"):
		return Service{}, ErrSlugTaken
	case postgres.IsForeignKeyViolation(err):
		return Service{}, ErrNotFound // the project was deleted meanwhile
	case err != nil:
		return Service{}, fmt.Errorf("create service: %w", err)
	}
	m.logger.InfoContext(ctx, "created service", id.LogAttr("service_id", id.Service, s.ID),
		id.LogAttr("project_id", id.Project, projectID), id.LogAttr("user_id", id.User, userID))
	return s, nil
}

// GetService returns a service.
func (m *Module) GetService(ctx context.Context, userID, serviceID uuid.UUID) (Service, error) {
	if err := m.authorizeFor(ctx, userID, projectOfService, serviceID, read); err != nil {
		return Service{}, err
	}
	s, err := readService(m.db.QueryRow(ctx, `SELECT `+serviceColumns+` FROM services WHERE id = $1`, serviceID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Service{}, ErrNotFound
	}
	if err != nil {
		return Service{}, fmt.Errorf("get service: %w", err)
	}
	return s, nil
}

// UpdateService changes a service. Owners, admins and editors may.
func (m *Module) UpdateService(ctx context.Context, userID, serviceID uuid.UUID, pre workspaces.Precondition, patch ServicePatch) (Service, error) {
	var errs validate.Errors
	if patch.Name != nil {
		v := validate.Name(&errs, "/name", *patch.Name)
		patch.Name = &v
	}
	if patch.Slug != nil {
		v := validate.Slug(&errs, "/slug", *patch.Slug)
		patch.Slug = &v
	}
	if patch.Description != nil {
		v := validate.Description(&errs, "/description", *patch.Description)
		patch.Description = &v
	}
	if patch.TelemetryServiceName != nil {
		v := telemetryName(&errs, *patch.TelemetryServiceName)
		patch.TelemetryServiceName = &v
	}
	if patch.Position != nil {
		v := position(&errs, *patch.Position)
		patch.Position = &v
	}
	if err := errs.Err(); err != nil {
		return Service{}, err
	}
	if err := m.authorizeFor(ctx, userID, projectOfService, serviceID, edit); err != nil {
		return Service{}, err
	}
	var s Service
	err := pgx.BeginFunc(ctx, m.db, func(tx pgx.Tx) error {
		cur, err := readService(tx.QueryRow(ctx, `SELECT `+serviceColumns+` FROM services WHERE id = $1 FOR UPDATE`, serviceID))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if !pre.Matches(cur.Version) {
			return ErrVersionMismatch
		}
		if patch.Name != nil {
			cur.Name = *patch.Name
		}
		if patch.Slug != nil {
			cur.Slug = *patch.Slug
		}
		if patch.Description != nil {
			cur.Description = *patch.Description
		}
		if patch.TelemetryServiceName != nil {
			cur.TelemetryServiceName = *patch.TelemetryServiceName
		}
		if patch.Position != nil {
			cur.Position = *patch.Position
		}
		s, err = readService(tx.QueryRow(ctx, `
			UPDATE services SET name = $2, slug = $3, description = $4, telemetry_service_name = $5,
				position_x = $6, position_y = $7, version = version + 1, updated_at = now()
			WHERE id = $1 RETURNING `+serviceColumns,
			serviceID, cur.Name, cur.Slug, cur.Description, nullable(cur.TelemetryServiceName), cur.Position.X, cur.Position.Y))
		return err
	})
	switch {
	case postgres.IsUniqueViolation(err, "services_project_slug_key"):
		return Service{}, ErrSlugTaken
	case isModuleError(err):
		return Service{}, err
	case err != nil:
		return Service{}, fmt.Errorf("update service: %w", err)
	}
	return s, nil
}

// DeleteService removes a service with its specs and revisions. Owners and
// admins may.
func (m *Module) DeleteService(ctx context.Context, userID, serviceID uuid.UUID, pre workspaces.Precondition) error {
	if err := m.authorizeFor(ctx, userID, projectOfService, serviceID, remove); err != nil {
		return err
	}
	err := pgx.BeginFunc(ctx, m.db, func(tx pgx.Tx) error {
		var version int64
		err := tx.QueryRow(ctx, `SELECT version FROM services WHERE id = $1 FOR UPDATE`, serviceID).Scan(&version)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if !pre.Matches(version) {
			return ErrVersionMismatch
		}
		_, err = tx.Exec(ctx, `DELETE FROM services WHERE id = $1`, serviceID)
		return err
	})
	switch {
	case isModuleError(err):
		return err
	case err != nil:
		return fmt.Errorf("delete service: %w", err)
	}
	m.logger.InfoContext(ctx, "deleted service", id.LogAttr("service_id", id.Service, serviceID), id.LogAttr("user_id", id.User, userID))
	return nil
}
