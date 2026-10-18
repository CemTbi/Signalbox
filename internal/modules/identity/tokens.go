// SPDX-License-Identifier: Apache-2.0

package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/CemTbi/Signalbox/internal/platform/id"
	"github.com/CemTbi/Signalbox/internal/platform/token"
	"github.com/CemTbi/Signalbox/internal/platform/validate"
)

// AccessTokenPrefix starts every personal access token, so secret scanners
// and people can tell one apart from other secrets.
const AccessTokenPrefix = "sbx_pat_"

// Access tokens' lifetimes, and how many one person may have.
const (
	DefaultAccessTokenLifetime = 90 * 24 * time.Hour
	MaxAccessTokenLifetime     = 366 * 24 * time.Hour
	maxAccessTokens            = 50
	maxAccessTokenName         = 100
	// Expired tokens stay listed this long, so people see what stopped
	// working and why.
	expiredTokensKept = 30 * 24 * time.Hour
)

// ErrTokenNotFound means the person has no access token with the ID.
var ErrTokenNotFound = errors.New("identity: no such access token")

// ErrTooManyTokens means the person already has as many access tokens as
// anyone may.
var ErrTooManyTokens = errors.New("identity: too many access tokens")

// AccessToken is a personal access token, without its secret.
type AccessToken struct {
	ID     uuid.UUID
	UserID uuid.UUID
	Name   string
	// Hint is the token's last four characters, to tell tokens apart.
	Hint       string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	LastUsedAt *time.Time
}

// NewAccessToken is an access token that was just created, with its
// secret. The secret isn't stored and can't be retrieved later.
type NewAccessToken struct {
	AccessToken
	Token string
}

// CreateAccessToken gives a person a new access token, which acts as them
// until it expires or they revoke it. lifetime 0 means the default.
func (s *Service) CreateAccessToken(ctx context.Context, userID uuid.UUID, name string, lifetime time.Duration) (*NewAccessToken, error) {
	var errs validate.Errors
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		errs.Add("/name", "required", "Name the token after where it's used, such as GitHub Actions.")
	case utf8.RuneCountInString(name) > maxAccessTokenName:
		errs.Add("/name", "maxLength", fmt.Sprintf("Use at most %d characters.", maxAccessTokenName))
	}
	if lifetime == 0 {
		lifetime = DefaultAccessTokenLifetime
	}
	if lifetime < 24*time.Hour || lifetime > MaxAccessTokenLifetime {
		errs.Add("/expiresInDays", "maximum", "Choose between 1 and 366 days.")
	}
	if err := errs.Err(); err != nil {
		return nil, err
	}

	secret := AccessTokenPrefix + token.Random()
	hash := token.Hash(secret)
	t := &NewAccessToken{Token: secret, AccessToken: AccessToken{
		ID: id.New(), UserID: userID, Name: name, Hint: secret[len(secret)-4:],
	}}
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		// Serializes a person's token creations, so the count holds.
		if _, err := tx.Exec(ctx, `SELECT 1 FROM users WHERE id = $1 FOR UPDATE`, userID); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM access_tokens WHERE user_id = $1`, userID).Scan(&n); err != nil {
			return err
		}
		if n >= maxAccessTokens {
			return ErrTooManyTokens
		}
		return tx.QueryRow(ctx, `
			INSERT INTO access_tokens (id, user_id, name, token_hash, hint, expires_at)
			VALUES ($1, $2, $3, $4, $5, now() + $6 * interval '1 second')
			RETURNING created_at, expires_at`,
			t.ID, userID, name, hash, t.Hint, int64(lifetime/time.Second),
		).Scan(&t.CreatedAt, &t.ExpiresAt)
	})
	if errors.Is(err, ErrTooManyTokens) {
		return nil, err
	}
	if err != nil {
		return nil, fmt.Errorf("create access token: %w", err)
	}
	s.logger.InfoContext(ctx, "created access token", id.LogAttr("token_id", id.AccessToken, t.ID),
		id.LogAttr("user_id", id.User, userID), "expires_at", t.ExpiresAt)
	return t, nil
}

const accessTokenColumns = `id, user_id, name, hint, created_at, expires_at, last_used_at` //nolint:gosec // G101: column names, not a credential.

func readAccessToken(row pgx.Row) (AccessToken, error) {
	var t AccessToken
	err := row.Scan(&t.ID, &t.UserID, &t.Name, &t.Hint, &t.CreatedAt, &t.ExpiresAt, &t.LastUsedAt)
	return t, err
}

// ListAccessTokens lists a person's access tokens, newest first, including
// those that expired in the last 30 days.
func (s *Service) ListAccessTokens(ctx context.Context, userID uuid.UUID) ([]AccessToken, error) {
	rows, err := s.db.Query(ctx, `SELECT `+accessTokenColumns+` FROM access_tokens WHERE user_id = $1 ORDER BY id DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list access tokens: %w", err)
	}
	tokens, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (AccessToken, error) { return readAccessToken(row) })
	if err != nil {
		return nil, fmt.Errorf("list access tokens: %w", err)
	}
	return tokens, nil
}

// RevokeAccessToken deletes one of a person's access tokens. It stops
// working at once.
func (s *Service) RevokeAccessToken(ctx context.Context, userID, tokenID uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM access_tokens WHERE id = $1 AND user_id = $2`, tokenID, userID)
	if err != nil {
		return fmt.Errorf("revoke access token: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrTokenNotFound
	}
	s.logger.InfoContext(ctx, "revoked access token", id.LogAttr("token_id", id.AccessToken, tokenID), id.LogAttr("user_id", id.User, userID))
	return nil
}

// tokenTouchAfter limits how often a token's last use is written.
const tokenTouchAfter = time.Minute

// AuthenticateAccessToken returns the session an access token stands for:
// its person, with AccessTokenID set and no CSRF token, because a token
// isn't sent by a browser on its own.
func (s *Service) AuthenticateAccessToken(ctx context.Context, secret string) (*Session, error) {
	rest, ok := strings.CutPrefix(secret, AccessTokenPrefix)
	if !ok || !token.WellFormed(rest) {
		return nil, ErrUnauthenticated
	}
	hash := token.Hash(secret)
	var (
		sess  Session
		stale bool
	)
	err := s.db.QueryRow(ctx, `
		SELECT u.id, u.email, u.name, u.admin, u.created_at, u.updated_at,
		       t.id, t.created_at, t.expires_at,
		       t.last_used_at IS NULL OR t.last_used_at < now() - $2 * interval '1 second'
		FROM access_tokens t JOIN users u ON u.id = t.user_id
		WHERE t.token_hash = $1 AND t.expires_at > now()`,
		hash, int64(tokenTouchAfter/time.Second),
	).Scan(&sess.User.ID, &sess.User.Email, &sess.User.Name, &sess.User.Admin, &sess.User.CreatedAt, &sess.User.UpdatedAt,
		&sess.AccessTokenID, &sess.CreatedAt, &sess.ExpiresAt, &stale)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrUnauthenticated
	}
	if err != nil {
		return nil, fmt.Errorf("look up access token: %w", err)
	}
	if stale {
		if _, err := s.db.Exec(ctx, `UPDATE access_tokens SET last_used_at = now() WHERE id = $1`, sess.AccessTokenID); err != nil {
			s.logger.WarnContext(ctx, "couldn't record access token use", id.LogAttr("token_id", id.AccessToken, sess.AccessTokenID), "error", err)
		}
	}
	return &sess, nil
}

// RemoveExpiredAccessTokens deletes tokens that expired more than 30 days
// ago, and returns how many.
func (s *Service) RemoveExpiredAccessTokens(ctx context.Context) (int64, error) {
	tag, err := s.db.Exec(ctx, `DELETE FROM access_tokens WHERE expires_at < now() - $1 * interval '1 second'`, int64(expiredTokensKept/time.Second))
	if err != nil {
		return 0, fmt.Errorf("remove expired access tokens: %w", err)
	}
	return tag.RowsAffected(), nil
}
