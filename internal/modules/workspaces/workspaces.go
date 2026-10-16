// SPDX-License-Identifier: Apache-2.0

// Package workspaces owns workspaces, their members, projects and project
// environments, and decides who may do what with them.
//
// Every method takes the acting user's ID and checks their workspace role
// before it touches data: this is the authorization layer from ADR-0004.
// Someone who isn't a member gets ErrNotFound, never ErrForbidden, so IDs
// don't reveal which workspaces exist.
package workspaces

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Errors the service returns. Validation problems come back as
// validate.Errors.
var (
	// ErrNotFound means the resource doesn't exist or the user can't see it.
	ErrNotFound = errors.New("workspaces: not found")
	// ErrForbidden means the user can see the resource but their role
	// doesn't allow the change.
	ErrForbidden = errors.New("workspaces: not allowed")
	// ErrSlugTaken means another workspace already uses the slug.
	ErrSlugTaken = errors.New("workspaces: slug taken")
	// ErrVersionMismatch means the resource changed since the version in
	// the precondition.
	ErrVersionMismatch = errors.New("workspaces: version mismatch")
	// ErrMemberNotFound means the person isn't a member of the workspace.
	ErrMemberNotFound = errors.New("workspaces: member not found")
	// ErrLastOwner means the change would leave the workspace without an
	// owner.
	ErrLastOwner = errors.New("workspaces: last owner")
	// ErrInvitationNotFound means the invitation doesn't exist, has
	// expired or was used.
	ErrInvitationNotFound = errors.New("workspaces: invitation not found")
	// ErrEmailMismatch means someone tried to accept an invitation that
	// was sent to another email address.
	ErrEmailMismatch = errors.New("workspaces: invitation is for another email address")
)

// Role is a member's role in a workspace.
type Role string

// Roles, from most to least powerful.
const (
	Owner  Role = "owner"
	Admin  Role = "admin"
	Editor Role = "editor"
	Viewer Role = "viewer"
)

// action is something a role may be allowed to do.
type action int

const (
	updateWorkspace action = iota
	deleteWorkspace
	createProject
	updateProject
	deleteProject
	// manageMembers covers inviting people, changing roles and removing
	// members. Only owners may grant, take away or invite the owner role.
	manageMembers
)

// allows says whether the role may take the action. Every member may read
// the workspace, its projects and its members.
func (r Role) allows(a action) bool {
	switch a {
	case deleteWorkspace:
		return r == Owner
	case updateWorkspace, deleteProject, manageMembers:
		return r == Owner || r == Admin
	case createProject, updateProject:
		return r == Owner || r == Admin || r == Editor
	}
	return false
}

// Valid reports whether r is one of the four roles.
func (r Role) Valid() bool {
	switch r {
	case Owner, Admin, Editor, Viewer:
		return true
	}
	return false
}

// Workspace is a workspace as one member sees it.
type Workspace struct {
	ID   uuid.UUID
	Name string
	Slug string
	// Role is the acting user's role in the workspace.
	Role Role
	// Version increments on every change; the API sends it as the ETag.
	Version   int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Project is one system in a workspace, shown as one canvas.
type Project struct {
	ID          uuid.UUID
	WorkspaceID uuid.UUID
	Name        string
	Description string
	Version     int64
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// PageRequest asks for the items after a given ID, in ID order. IDs are
// UUIDv7, so this is creation order.
type PageRequest struct {
	// After is the last ID of the previous page, or uuid.Nil for the first.
	After uuid.UUID
	// Limit is the page size, from 1 to MaxLimit.
	Limit int
}

// Page sizes.
const (
	DefaultLimit = 50
	MaxLimit     = 200
)

func (p PageRequest) limit() int {
	if p.Limit < 1 {
		return DefaultLimit
	}
	return min(p.Limit, MaxLimit)
}

// Page is one page of results.
type Page[T any] struct {
	Items []T
	// More reports whether there are items after the last one.
	More bool
}

// Precondition is the If-Match of a change: the versions the client's copy
// may have. An empty precondition matches any version.
type Precondition struct {
	Versions []int64
}

func (p Precondition) matches(version int64) bool {
	if len(p.Versions) == 0 {
		return true
	}
	for _, v := range p.Versions {
		if v == version {
			return true
		}
	}
	return false
}

// Service is the workspaces module.
type Service struct {
	db     *pgxpool.Pool
	logger *slog.Logger
}

// New returns the workspaces service.
func New(db *pgxpool.Pool, logger *slog.Logger) *Service {
	return &Service{db: db, logger: logger}
}

// querier is what both a pool and a transaction can do.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// page trims the extra row fetched to detect a next page.
func page[T any](items []T, limit int) Page[T] {
	if len(items) > limit {
		return Page[T]{Items: items[:limit], More: true}
	}
	if items == nil {
		items = []T{}
	}
	return Page[T]{Items: items}
}

// afterArg turns uuid.Nil into SQL NULL, so "id > $n" filters only when a
// cursor was given.
func afterArg(after uuid.UUID) *uuid.UUID {
	if after == uuid.Nil {
		return nil
	}
	return &after
}
