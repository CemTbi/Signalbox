// SPDX-License-Identifier: Apache-2.0

package workspaces

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/CemTbi/Signalbox/internal/platform/id"
	"github.com/CemTbi/Signalbox/internal/platform/validate"
)

// Member is someone's membership of a workspace. The identity module knows
// who the user is.
type Member struct {
	UserID   uuid.UUID
	Role     Role
	JoinedAt time.Time
}

// ListMembers lists a workspace's members in user ID order. Every member
// may.
func (s *Service) ListMembers(ctx context.Context, userID, workspaceID uuid.UUID, p PageRequest) (Page[Member], error) {
	if _, err := role(ctx, s.db, userID, workspaceID, ""); err != nil {
		return Page[Member]{}, err
	}
	limit := p.limit()
	rows, err := s.db.Query(ctx, `
		SELECT user_id, role, created_at FROM workspace_members
		WHERE workspace_id = $1 AND ($2::uuid IS NULL OR user_id > $2)
		ORDER BY user_id
		LIMIT $3`, workspaceID, afterArg(p.After), limit+1)
	if err != nil {
		return Page[Member]{}, fmt.Errorf("list members: %w", err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Member, error) {
		var m Member
		err := row.Scan(&m.UserID, &m.Role, &m.JoinedAt)
		return m, err
	})
	if err != nil {
		return Page[Member]{}, fmt.Errorf("list members: %w", err)
	}
	return page(items, limit), nil
}

// MemberRole returns someone's role in a workspace and whether they are a
// member. It checks nothing about the caller; the API uses it to tell an
// inviter that someone already belongs.
func (s *Service) MemberRole(ctx context.Context, workspaceID, userID uuid.UUID) (Role, bool, error) {
	r, err := role(ctx, s.db, userID, workspaceID, "")
	if errors.Is(err, ErrNotFound) {
		return "", false, nil
	}
	return r, err == nil, err
}

// ChangeRole gives a member another role. Owners and admins may change
// roles, but only owners may make someone an owner or change an owner's
// role. A workspace always keeps at least one owner.
func (s *Service) ChangeRole(ctx context.Context, actorID, workspaceID, memberID uuid.UUID, newRole Role) (Member, error) {
	if !newRole.Valid() {
		var errs validate.Errors
		errs.Add("/role", "enum", "Choose owner, admin, editor or viewer.")
		return Member{}, errs
	}
	var m Member
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if err := lockMemberships(ctx, tx, workspaceID); err != nil {
			return err
		}
		actorRole, err := role(ctx, tx, actorID, workspaceID, "")
		if err != nil {
			return err
		}
		if !actorRole.allows(manageMembers) {
			return ErrForbidden
		}
		if m, err = lockMember(ctx, tx, workspaceID, memberID); err != nil {
			return err
		}
		if (m.Role == Owner || newRole == Owner) && actorRole != Owner {
			return ErrForbidden
		}
		if m.Role == Owner && newRole != Owner {
			if err := keepAnOwner(ctx, tx, workspaceID); err != nil {
				return err
			}
		}
		m.Role = newRole
		_, err = tx.Exec(ctx, `UPDATE workspace_members SET role = $3 WHERE workspace_id = $1 AND user_id = $2`,
			workspaceID, memberID, newRole)
		return err
	})
	if err != nil {
		return Member{}, wrapUnexpected("change role", err)
	}
	s.logger.InfoContext(ctx, "changed member role", id.LogAttr("workspace_id", id.Workspace, workspaceID),
		id.LogAttr("member_id", id.User, memberID), "role", newRole, id.LogAttr("user_id", id.User, actorID))
	return m, nil
}

// RemoveMember takes someone out of a workspace. Owners and admins may
// remove others, though only owners may remove an owner, and anyone may
// leave. A workspace always keeps at least one owner.
func (s *Service) RemoveMember(ctx context.Context, actorID, workspaceID, memberID uuid.UUID) error {
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if err := lockMemberships(ctx, tx, workspaceID); err != nil {
			return err
		}
		actorRole, err := role(ctx, tx, actorID, workspaceID, "")
		if err != nil {
			return err
		}
		leaving := actorID == memberID
		if !leaving && !actorRole.allows(manageMembers) {
			return ErrForbidden
		}
		m, err := lockMember(ctx, tx, workspaceID, memberID)
		if err != nil {
			return err
		}
		if m.Role == Owner && !leaving && actorRole != Owner {
			return ErrForbidden
		}
		if m.Role == Owner {
			if err := keepAnOwner(ctx, tx, workspaceID); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `DELETE FROM workspace_members WHERE workspace_id = $1 AND user_id = $2`, workspaceID, memberID)
		return err
	})
	if err != nil {
		return wrapUnexpected("remove member", err)
	}
	s.logger.InfoContext(ctx, "removed member", id.LogAttr("workspace_id", id.Workspace, workspaceID),
		id.LogAttr("member_id", id.User, memberID), id.LogAttr("user_id", id.User, actorID))
	return nil
}

// lockMemberships makes changes to a workspace's members take turns, so two
// owners can't demote each other at the same moment and leave nobody. The
// lock is FOR NO KEY UPDATE, which doesn't block the key-share locks that
// foreign keys take when projects are created.
func lockMemberships(ctx context.Context, q querier, workspaceID uuid.UUID) error {
	var one int
	err := q.QueryRow(ctx, `SELECT 1 FROM workspaces WHERE id = $1 FOR NO KEY UPDATE`, workspaceID).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func lockMember(ctx context.Context, q querier, workspaceID, userID uuid.UUID) (Member, error) {
	m := Member{UserID: userID}
	err := q.QueryRow(ctx, `
		SELECT role, created_at FROM workspace_members WHERE workspace_id = $1 AND user_id = $2 FOR UPDATE`,
		workspaceID, userID).Scan(&m.Role, &m.JoinedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Member{}, ErrMemberNotFound
	}
	return m, err
}

// keepAnOwner fails when the workspace has only one owner, who is about to
// be demoted or removed. Callers hold lockMemberships.
func keepAnOwner(ctx context.Context, q querier, workspaceID uuid.UUID) error {
	var owners int
	err := q.QueryRow(ctx, `SELECT count(*) FROM workspace_members WHERE workspace_id = $1 AND role = 'owner'`,
		workspaceID).Scan(&owners)
	if err != nil {
		return err
	}
	if owners <= 1 {
		return ErrLastOwner
	}
	return nil
}

// wrapUnexpected adds context to errors that aren't one of the service's
// own, which callers compare with errors.Is.
func wrapUnexpected(what string, err error) error {
	if isServiceError(err) {
		return err
	}
	var fields validate.Errors
	if errors.As(err, &fields) {
		return err
	}
	return fmt.Errorf("%s: %w", what, err)
}
