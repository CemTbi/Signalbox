// SPDX-License-Identifier: Apache-2.0

package identity

import (
	"context"
	"fmt"

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
	err = s.db.QueryRow(ctx, `
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
