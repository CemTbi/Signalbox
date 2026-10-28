// SPDX-License-Identifier: Apache-2.0

// Package apis is the module for a project's services and their OpenAPI
// specs. A spec's document lives in revisions: every save checks the
// document and keeps it as a new revision that never changes. Mocks, docs
// and code generation read these revisions. See
// docs/adr/0019-spec-revisions.md.
package apis

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/CemTbi/Signalbox/internal/modules/workspaces"
	"github.com/CemTbi/Signalbox/pkg/specengine"
)

// Errors the module returns. Validation problems come back as
// validate.Errors.
var (
	// ErrNotFound means the resource doesn't exist or the user can't see it.
	ErrNotFound = errors.New("apis: not found")
	// ErrForbidden means the user can see the resource but their role
	// doesn't allow the change.
	ErrForbidden = errors.New("apis: not allowed")
	// ErrSlugTaken means another service in the project uses the slug.
	ErrSlugTaken = errors.New("apis: slug taken")
	// ErrVersionMismatch means the resource changed since the version in
	// the precondition.
	ErrVersionMismatch = errors.New("apis: version mismatch")
	// ErrPreconditionRequired means a change that needs If-Match came
	// without it.
	ErrPreconditionRequired = errors.New("apis: precondition required")
	// ErrCheckTimeout means checking the document took too long.
	ErrCheckTimeout = errors.New("apis: checking the document took too long")
)

// UnreadableError means a document couldn't be read as OpenAPI or Swagger
// at all, so it can't become a revision. Report says why.
type UnreadableError struct {
	Report specengine.Report
}

func (e *UnreadableError) Error() string {
	if len(e.Report.Diagnostics) > 0 {
		return "apis: the document can't be read: " + e.Report.Diagnostics[0].Message
	}
	return "apis: the document can't be read"
}

// Access tells the module what a user may do in a project. The workspaces
// module provides it.
type Access interface {
	ProjectRole(ctx context.Context, userID, projectID uuid.UUID) (workspaces.Role, error)
}

// Module manages services, specs and revisions.
type Module struct {
	db     *pgxpool.Pool
	logger *slog.Logger
	access Access
	// checkTimeout bounds checking one document.
	checkTimeout time.Duration
	// contracts keeps the contract checkers of versions used last.
	contracts checkers
}

// New returns the module.
func New(db *pgxpool.Pool, logger *slog.Logger, access Access) *Module {
	return &Module{db: db, logger: logger, access: access, checkTimeout: 30 * time.Second}
}

// need describes what a user must be allowed to do.
type need int

const (
	read need = iota
	edit
	remove
)

// authorize checks the user's role in the project. The role is read outside
// the caller's transaction, so a member removed at that moment may still
// complete one change.
func (m *Module) authorize(ctx context.Context, userID, projectID uuid.UUID, n need) error {
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

// projectOf returns the project a service, spec or revision belongs to.
func (m *Module) projectOf(ctx context.Context, query string, arg uuid.UUID) (uuid.UUID, error) {
	var project uuid.UUID
	err := m.db.QueryRow(ctx, query, arg).Scan(&project)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	if err != nil {
		return uuid.Nil, err
	}
	return project, nil
}

const (
	projectOfService  = `SELECT project_id FROM services WHERE id = $1`
	projectOfSpec     = `SELECT s.project_id FROM specs p JOIN services s ON s.id = p.service_id WHERE p.id = $1`
	projectOfRevision = `SELECT s.project_id FROM spec_revisions r JOIN specs p ON p.id = r.spec_id JOIN services s ON s.id = p.service_id WHERE r.id = $1`
)

// authorizeFor finds the project of a resource and checks the user's role
// there.
func (m *Module) authorizeFor(ctx context.Context, userID uuid.UUID, query string, resource uuid.UUID, n need) error {
	project, err := m.projectOf(ctx, query, resource)
	if err != nil {
		return err
	}
	return m.authorize(ctx, userID, project, n)
}

func isModuleError(err error) bool {
	var unreadable *UnreadableError
	if errors.As(err, &unreadable) {
		return true
	}
	var hasErrors *HasErrorsError
	if errors.As(err, &hasErrors) {
		return true
	}
	for _, e := range []error{ErrNotFound, ErrForbidden, ErrSlugTaken, ErrVersionMismatch, ErrPreconditionRequired, ErrCheckTimeout, ErrVersionExists} {
		if errors.Is(err, e) {
			return true
		}
	}
	return false
}

// page trims the extra row fetched to detect a next page.
func page[T any](items []T, limit int) workspaces.Page[T] {
	if len(items) > limit {
		return workspaces.Page[T]{Items: items[:limit], More: true}
	}
	if items == nil {
		items = []T{}
	}
	return workspaces.Page[T]{Items: items}
}

func limitOf(p workspaces.PageRequest) int {
	if p.Limit < 1 {
		return workspaces.DefaultLimit
	}
	return min(p.Limit, workspaces.MaxLimit)
}

func afterArg(after uuid.UUID) *uuid.UUID {
	if after == uuid.Nil {
		return nil
	}
	return &after
}
