// SPDX-License-Identifier: Apache-2.0

package workspaces

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/CemTbi/Signalbox/internal/platform/id"
	"github.com/CemTbi/Signalbox/internal/platform/token"
	"github.com/CemTbi/Signalbox/internal/platform/validate"
)

// Invitations bring people into a workspace without email: the inviter
// gets a link with a secret token, valid once for InvitationLifetime, and
// sends it however they like. The invitation names the email address that
// may accept it.

// InvitationLifetime is how long an invitation link works.
const InvitationLifetime = 7 * 24 * time.Hour

// Invitation is an open invitation to join a workspace.
type Invitation struct {
	ID          uuid.UUID
	WorkspaceID uuid.UUID
	Email       string
	Role        Role
	// InvitedBy is uuid.Nil when the inviter's account was deleted.
	InvitedBy uuid.UUID
	CreatedAt time.Time
	ExpiresAt time.Time
}

// NewInvitation is an invitation that was just created, with the token for
// its link. The token isn't stored and can't be retrieved later.
type NewInvitation struct {
	Invitation
	Token string
}

// InvitationDetails is what the person holding an invitation link may see.
type InvitationDetails struct {
	Invitation
	WorkspaceName string
}

// InvitationInput is a new invitation.
type InvitationInput struct {
	Email string
	Role  Role
}

const invitationColumns = `i.id, i.workspace_id, i.email, i.role, i.invited_by, i.created_at, i.expires_at`

func readInvitation(row scanner, extra ...any) (Invitation, error) {
	var (
		inv       Invitation
		invitedBy *uuid.UUID
	)
	dest := append([]any{&inv.ID, &inv.WorkspaceID, &inv.Email, &inv.Role, &invitedBy, &inv.CreatedAt, &inv.ExpiresAt}, extra...)
	if err := row.Scan(dest...); err != nil {
		return Invitation{}, err
	}
	if invitedBy != nil {
		inv.InvitedBy = *invitedBy
	}
	return inv, nil
}

// Invite creates an invitation, replacing any open one for the same email
// address. Owners and admins may invite; only owners may invite owners.
func (s *Service) Invite(ctx context.Context, actorID, workspaceID uuid.UUID, in InvitationInput) (NewInvitation, error) {
	var errs validate.Errors
	email := validate.Email(&errs, "/email", in.Email)
	if !in.Role.Valid() {
		errs.Add("/role", "enum", "Choose owner, admin, editor or viewer.")
	}
	if err := errs.Err(); err != nil {
		return NewInvitation{}, err
	}

	secret, hash := token.New()
	inv := NewInvitation{Token: secret, Invitation: Invitation{
		ID: id.New(), WorkspaceID: workspaceID, Email: email, Role: in.Role, InvitedBy: actorID,
	}}
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		actorRole, err := role(ctx, tx, actorID, workspaceID, "FOR SHARE")
		if err != nil {
			return err
		}
		if !actorRole.allows(manageMembers) || (in.Role == Owner && actorRole != Owner) {
			return ErrForbidden
		}
		if _, err := tx.Exec(ctx, `DELETE FROM workspace_invitations WHERE workspace_id = $1 AND lower(email) = lower($2)`,
			workspaceID, email); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `
			INSERT INTO workspace_invitations (id, workspace_id, email, role, token_hash, invited_by, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, now() + $7 * interval '1 second')
			RETURNING created_at, expires_at`,
			inv.ID, workspaceID, email, in.Role, hash, actorID, int64(InvitationLifetime/time.Second),
		).Scan(&inv.CreatedAt, &inv.ExpiresAt)
	})
	if err != nil {
		return NewInvitation{}, wrapUnexpected("invite", err)
	}
	s.logger.InfoContext(ctx, "invited someone to a workspace", id.LogAttr("invitation_id", id.Invitation, inv.ID),
		id.LogAttr("workspace_id", id.Workspace, workspaceID), "role", in.Role, id.LogAttr("user_id", id.User, actorID))
	return inv, nil
}

// ListInvitations lists a workspace's open invitations, oldest first.
// Owners and admins may.
func (s *Service) ListInvitations(ctx context.Context, actorID, workspaceID uuid.UUID, p PageRequest) (Page[Invitation], error) {
	r, err := role(ctx, s.db, actorID, workspaceID, "")
	if err != nil {
		return Page[Invitation]{}, err
	}
	if !r.allows(manageMembers) {
		return Page[Invitation]{}, ErrForbidden
	}
	limit := p.limit()
	rows, err := s.db.Query(ctx, `
		SELECT `+invitationColumns+` FROM workspace_invitations i
		WHERE i.workspace_id = $1 AND i.expires_at > now() AND ($2::uuid IS NULL OR i.id > $2)
		ORDER BY i.id
		LIMIT $3`, workspaceID, afterArg(p.After), limit+1)
	if err != nil {
		return Page[Invitation]{}, fmt.Errorf("list invitations: %w", err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Invitation, error) { return readInvitation(row) })
	if err != nil {
		return Page[Invitation]{}, fmt.Errorf("list invitations: %w", err)
	}
	return page(items, limit), nil
}

// RevokeInvitation deletes an open invitation, so its link stops working.
// Owners and admins of its workspace may.
func (s *Service) RevokeInvitation(ctx context.Context, actorID, invitationID uuid.UUID) error {
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var workspaceID uuid.UUID
		err := tx.QueryRow(ctx, `SELECT workspace_id FROM workspace_invitations WHERE id = $1 FOR UPDATE`, invitationID).Scan(&workspaceID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvitationNotFound
		}
		if err != nil {
			return err
		}
		r, err := role(ctx, tx, actorID, workspaceID, "")
		if errors.Is(err, ErrNotFound) {
			return ErrInvitationNotFound // don't reveal invitations of other workspaces
		}
		if err != nil {
			return err
		}
		if !r.allows(manageMembers) {
			return ErrForbidden
		}
		_, err = tx.Exec(ctx, `DELETE FROM workspace_invitations WHERE id = $1`, invitationID)
		return err
	})
	if err != nil {
		return wrapUnexpected("revoke invitation", err)
	}
	s.logger.InfoContext(ctx, "revoked an invitation", id.LogAttr("invitation_id", id.Invitation, invitationID),
		id.LogAttr("user_id", id.User, actorID))
	return nil
}

// LookupInvitation returns the open invitation behind a link's token.
func (s *Service) LookupInvitation(ctx context.Context, secret string) (InvitationDetails, error) {
	if !token.WellFormed(secret) {
		return InvitationDetails{}, ErrInvitationNotFound
	}
	var d InvitationDetails
	inv, err := readInvitation(s.db.QueryRow(ctx, `
		SELECT `+invitationColumns+`, w.name
		FROM workspace_invitations i JOIN workspaces w ON w.id = i.workspace_id
		WHERE i.token_hash = $1 AND i.expires_at > now()`, token.Hash(secret)), &d.WorkspaceName)
	if errors.Is(err, pgx.ErrNoRows) {
		return InvitationDetails{}, ErrInvitationNotFound
	}
	if err != nil {
		return InvitationDetails{}, fmt.Errorf("look up invitation: %w", err)
	}
	d.Invitation = inv
	return d, nil
}

// AcceptInvitation makes the user a member with the invitation's role and
// uses the invitation up. The user's email address must be the one the
// invitation was sent to. A user who is already a member keeps their role.
func (s *Service) AcceptInvitation(ctx context.Context, secret string, userID uuid.UUID, email string) (Workspace, error) {
	if !token.WellFormed(secret) {
		return Workspace{}, ErrInvitationNotFound
	}
	var w Workspace
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		inv, err := readInvitation(tx.QueryRow(ctx, `
			SELECT `+invitationColumns+` FROM workspace_invitations i
			WHERE i.token_hash = $1 AND i.expires_at > now() FOR UPDATE`, token.Hash(secret)))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvitationNotFound
		}
		if err != nil {
			return err
		}
		if !strings.EqualFold(inv.Email, strings.TrimSpace(email)) {
			return ErrEmailMismatch
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO workspace_members (workspace_id, user_id, role) VALUES ($1, $2, $3)
			ON CONFLICT (workspace_id, user_id) DO NOTHING`, inv.WorkspaceID, userID, inv.Role); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM workspace_invitations WHERE id = $1`, inv.ID); err != nil {
			return err
		}
		w, err = getWorkspace(ctx, tx, userID, inv.WorkspaceID, "")
		return err
	})
	if err != nil {
		return Workspace{}, wrapUnexpected("accept invitation", err)
	}
	s.logger.InfoContext(ctx, "accepted an invitation", id.LogAttr("workspace_id", id.Workspace, w.ID),
		"role", w.Role, id.LogAttr("user_id", id.User, userID))
	return w, nil
}

// RemoveExpiredInvitations deletes invitations whose links no longer work
// and returns how many it deleted.
func (s *Service) RemoveExpiredInvitations(ctx context.Context) (int64, error) {
	tag, err := s.db.Exec(ctx, `DELETE FROM workspace_invitations WHERE expires_at <= now()`)
	if err != nil {
		return 0, fmt.Errorf("remove expired invitations: %w", err)
	}
	return tag.RowsAffected(), nil
}

// Run removes expired invitations every hour until ctx ends.
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		if n, err := s.RemoveExpiredInvitations(ctx); err != nil && ctx.Err() == nil {
			s.logger.WarnContext(ctx, "couldn't remove expired invitations", "error", err)
		} else if n > 0 {
			s.logger.InfoContext(ctx, "removed expired invitations", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
