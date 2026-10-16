// SPDX-License-Identifier: Apache-2.0

package identity

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

// UserInput is a new account with a password.
type UserInput struct {
	Name     string
	Email    string
	Password string
	Admin    bool
}

// CreateUser creates an account that signs in with a password. Its email
// address must not belong to another account, whatever its case.
func (s *Service) CreateUser(ctx context.Context, in UserInput) (User, error) {
	return s.insertUser(ctx, s.db, in)
}

// CreateUserAndSignIn creates an account and signs it in, in one
// transaction, for people who join through an invitation.
func (s *Service) CreateUserAndSignIn(ctx context.Context, in UserInput, client Client) (*NewSession, error) {
	var ns *NewSession
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		u, err := s.insertUser(ctx, tx, in)
		if err != nil {
			return err
		}
		ns, err = s.createSession(ctx, tx, u, client)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.logger.InfoContext(ctx, "signed in", id.LogAttr("user_id", id.User, ns.User.ID), "client_ip", client.IP)
	return ns, nil
}

func (s *Service) insertUser(ctx context.Context, q querier, in UserInput) (User, error) {
	var errs validate.Errors
	u := User{
		ID:    id.New(),
		Name:  validate.Name(&errs, "/name", in.Name),
		Email: validate.Email(&errs, "/email", in.Email),
		Admin: in.Admin,
	}
	validate.NewPassword(&errs, "/password", in.Password)
	if err := errs.Err(); err != nil {
		return User{}, err
	}
	hash, err := s.passwords.hash(ctx, in.Password)
	if err != nil {
		return User{}, err
	}
	err = q.QueryRow(ctx, `
		INSERT INTO users (id, email, name, password_hash, admin) VALUES ($1, $2, $3, $4, $5)
		RETURNING created_at, updated_at`, u.ID, u.Email, u.Name, hash, u.Admin,
	).Scan(&u.CreatedAt, &u.UpdatedAt)
	if postgres.IsUniqueViolation(err, "users_email_key") {
		return User{}, ErrEmailTaken
	}
	if err != nil {
		return User{}, fmt.Errorf("create user: %w", err)
	}
	s.logger.InfoContext(ctx, "created account", id.LogAttr("user_id", id.User, u.ID), "admin", u.Admin)
	return u, nil
}

// UserByEmail returns the account with the email address, whatever its
// case, and whether there is one.
func (s *Service) UserByEmail(ctx context.Context, email string) (User, bool, error) {
	var u User
	err := s.db.QueryRow(ctx, `
		SELECT id, email, name, admin, created_at, updated_at FROM users WHERE lower(email) = lower($1)`, email,
	).Scan(&u.ID, &u.Email, &u.Name, &u.Admin, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, false, nil
	}
	if err != nil {
		return User{}, false, fmt.Errorf("look up account by email: %w", err)
	}
	return u, true, nil
}

// Users returns the accounts with the given IDs. IDs without an account,
// such as those of deleted users, are missing from the map.
func (s *Service) Users(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]User, error) {
	out := make(map[uuid.UUID]User, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, email, name, admin, created_at, updated_at FROM users WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, fmt.Errorf("look up accounts: %w", err)
	}
	users, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (User, error) {
		var u User
		err := row.Scan(&u.ID, &u.Email, &u.Name, &u.Admin, &u.CreatedAt, &u.UpdatedAt)
		return u, err
	})
	if err != nil {
		return nil, fmt.Errorf("look up accounts: %w", err)
	}
	for _, u := range users {
		out[u.ID] = u
	}
	return out, nil
}
