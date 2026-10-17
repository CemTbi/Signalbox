// SPDX-License-Identifier: Apache-2.0

package identity

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
)

// SignIn checks an email address and password and starts a session.
func (s *Service) SignIn(ctx context.Context, email, password string, client Client) (*NewSession, error) {
	var (
		u    User
		hash *string
	)
	err := s.db.QueryRow(ctx, `
		SELECT id, email, name, admin, created_at, updated_at, password_hash
		FROM users WHERE lower(email) = lower($1)`, strings.TrimSpace(email),
	).Scan(&u.ID, &u.Email, &u.Name, &u.Admin, &u.CreatedAt, &u.UpdatedAt, &hash)
	switch {
	case errors.Is(err, pgx.ErrNoRows) || err == nil && hash == nil:
		s.passwords.verifyDummy(ctx, password)
		s.logger.WarnContext(ctx, "sign-in failed", "reason", "no password account for the email address", "client_ip", client.IP)
		return nil, ErrInvalidCredentials
	case err != nil:
		return nil, fmt.Errorf("look up account: %w", err)
	}

	match, rehash, err := s.passwords.verify(ctx, password, *hash)
	if err != nil {
		return nil, fmt.Errorf("verify password of %s: %w", u.ID, err)
	}
	if !match {
		s.logger.WarnContext(ctx, "sign-in failed", "reason", "wrong password", id.LogAttr("user_id", id.User, u.ID), "client_ip", client.IP)
		return nil, ErrInvalidCredentials
	}
	if rehash {
		s.upgradeHash(ctx, u, password)
	}

	ns, err := s.createSession(ctx, s.db, u, client)
	if err != nil {
		return nil, err
	}
	s.logger.InfoContext(ctx, "signed in", id.LogAttr("user_id", id.User, u.ID), "client_ip", client.IP)
	return ns, nil
}

// upgradeHash re-hashes a password with the current parameters. Failing to
// do so doesn't block the sign-in; it is tried again next time.
func (s *Service) upgradeHash(ctx context.Context, u User, password string) {
	newHash, err := s.passwords.hash(ctx, password)
	if err == nil {
		_, err = s.db.Exec(ctx, `UPDATE users SET password_hash = $2 WHERE id = $1`, u.ID, newHash)
	}
	if err != nil {
		s.logger.WarnContext(ctx, "couldn't upgrade password hash", id.LogAttr("user_id", id.User, u.ID), "error", err)
		return
	}
	s.logger.InfoContext(ctx, "upgraded password hash to current parameters", id.LogAttr("user_id", id.User, u.ID))
}

func (s *Service) createSession(ctx context.Context, q querier, u User, client Client) (*NewSession, error) {
	secret, hash := token.New()
	ns := &NewSession{Token: secret, Session: Session{User: u, CSRFToken: token.Random()}}
	var ip *string
	if client.IP.IsValid() {
		v := client.IP.String()
		ip = &v
	}
	err := q.QueryRow(ctx, `
		INSERT INTO sessions (token_hash, user_id, csrf_token, expires_at, client_ip, user_agent)
		VALUES ($1, $2, $3, now() + $4 * interval '1 second', $5::inet, $6)
		RETURNING created_at, expires_at`,
		hash, u.ID, ns.CSRFToken, int64(s.cfg.AbsoluteTimeout/time.Second), ip, truncate(client.UserAgent, 512),
	).Scan(&ns.CreatedAt, &ns.ExpiresAt)
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	return ns, nil
}

// Authenticate returns the session for a token from a session cookie.
func (s *Service) Authenticate(ctx context.Context, sessionToken string) (*Session, error) {
	if !token.WellFormed(sessionToken) {
		return nil, ErrUnauthenticated
	}
	hash := token.Hash(sessionToken)
	var (
		sess  Session
		stale bool
	)
	err := s.db.QueryRow(ctx, `
		SELECT u.id, u.email, u.name, u.admin, u.created_at, u.updated_at,
		       s.csrf_token, s.created_at, s.expires_at,
		       s.last_seen_at < now() - $3 * interval '1 second'
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1
		  AND s.expires_at > now()
		  AND s.last_seen_at > now() - $2 * interval '1 second'`,
		hash, int64(s.cfg.IdleTimeout/time.Second), int64(touchAfter/time.Second),
	).Scan(&sess.User.ID, &sess.User.Email, &sess.User.Name, &sess.User.Admin, &sess.User.CreatedAt, &sess.User.UpdatedAt,
		&sess.CSRFToken, &sess.CreatedAt, &sess.ExpiresAt, &stale)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrUnauthenticated
	}
	if err != nil {
		return nil, fmt.Errorf("look up session: %w", err)
	}
	if stale {
		if _, err := s.db.Exec(ctx, `UPDATE sessions SET last_seen_at = now() WHERE token_hash = $1`, hash); err != nil {
			// The session stays valid; its idle clock just isn't reset.
			s.logger.WarnContext(ctx, "couldn't record session use", id.LogAttr("user_id", id.User, sess.User.ID), "error", err)
		}
	}
	return &sess, nil
}

// SignOut ends the session that the token belongs to. Signing out of a
// session that doesn't exist isn't an error.
func (s *Service) SignOut(ctx context.Context, sessionToken string) error {
	if !token.WellFormed(sessionToken) {
		return nil
	}
	var userID uuid.UUID
	err := s.db.QueryRow(ctx, `DELETE FROM sessions WHERE token_hash = $1 RETURNING user_id`, token.Hash(sessionToken)).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	s.logger.InfoContext(ctx, "signed out", id.LogAttr("user_id", id.User, userID))
	return nil
}

// RemoveExpiredSessions deletes sessions that can no longer be used and
// returns how many it deleted.
func (s *Service) RemoveExpiredSessions(ctx context.Context) (int64, error) {
	tag, err := s.db.Exec(ctx, `
		DELETE FROM sessions
		WHERE expires_at <= now() OR last_seen_at <= now() - $1 * interval '1 second'`,
		int64(s.cfg.IdleTimeout/time.Second))
	if err != nil {
		return 0, fmt.Errorf("remove expired sessions: %w", err)
	}
	return tag.RowsAffected(), nil
}

// Run removes expired sessions every hour until ctx ends. Every api process
// can run it; the deletes don't conflict.
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		if n, err := s.RemoveExpiredSessions(ctx); err != nil && ctx.Err() == nil {
			s.logger.WarnContext(ctx, "couldn't remove expired sessions", "error", err)
		} else if n > 0 {
			s.logger.InfoContext(ctx, "removed expired sessions", "count", n)
		}
		if _, err := s.removeExpiredSSOAttempts(ctx); err != nil && ctx.Err() == nil {
			s.logger.WarnContext(ctx, "couldn't remove expired single sign-on attempts", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	// Cut on a rune boundary so the column stays valid UTF-8.
	for n > 0 && !utf8RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func utf8RuneStart(b byte) bool { return b&0xC0 != 0x80 }
