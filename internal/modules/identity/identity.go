// SPDX-License-Identifier: Apache-2.0

// Package identity owns accounts and browser sessions: first-run setup,
// signing in with a password, and checking the session behind a request.
// See docs/adr/0004-authentication-and-sessions.md.
//
// Other modules identify people by user ID and never read this module's
// tables.
package identity

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Errors the service returns. Validation problems come back as
// validate.Errors.
var (
	// ErrInvalidCredentials means the email address or password is wrong.
	// It never says which, so it can't be used to find accounts.
	ErrInvalidCredentials = errors.New("identity: wrong email address or password")
	// ErrUnauthenticated means the session token is unknown or expired.
	ErrUnauthenticated = errors.New("identity: no valid session")
	// ErrSetupComplete means the installation already has accounts.
	ErrSetupComplete = errors.New("identity: setup is already complete")
	// ErrInvalidSetupCode means the setup code doesn't match.
	ErrInvalidSetupCode = errors.New("identity: wrong setup code")
	// ErrEmailTaken means another account has the email address.
	ErrEmailTaken = errors.New("identity: email address taken")
)

// User is a person with an account.
type User struct {
	ID    uuid.UUID
	Email string
	Name  string
	// Admin users manage the installation.
	Admin     bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Session is a signed-in browser session.
type Session struct {
	User User
	// CSRFToken must accompany every unsafe request made with the session.
	CSRFToken string
	CreatedAt time.Time
	// ExpiresAt is when the session ends at the latest. It also ends after
	// Config.IdleTimeout without use.
	ExpiresAt time.Time
	// AccessTokenID is the personal access token the request came with,
	// or uuid.Nil for a browser session.
	AccessTokenID uuid.UUID
}

// NewSession is a session that was just created, with the token for the
// session cookie. The token isn't stored and can't be retrieved later.
type NewSession struct {
	Session
	Token string
}

// Client describes where a request came from, for session records and logs.
type Client struct {
	IP        netip.Addr
	UserAgent string
}

// Config tunes the service. The zero value of each field means its default.
type Config struct {
	// IdleTimeout ends sessions that go unused this long. Default 12 hours.
	IdleTimeout time.Duration
	// AbsoluteTimeout ends sessions this long after sign-in. Default 7 days.
	AbsoluteTimeout time.Duration
	// SetupCode, when set, is the code that unlocks first-run setup instead
	// of a random one written to the log.
	SetupCode string
	// Argon2 sets the password hashing cost. Default DefaultArgon2.
	Argon2 Argon2Params
	// MaxConcurrentHashes bounds password hashes in flight, each of which
	// holds Argon2.Memory. Default 2.
	MaxConcurrentHashes int
	// SSO connects an OpenID Connect identity provider. Off when its Issuer
	// is empty.
	SSO SSOConfig
}

// Session lifetimes from ADR-0004.
const (
	DefaultIdleTimeout     = 12 * time.Hour
	DefaultAbsoluteTimeout = 7 * 24 * time.Hour
)

// touchAfter is how stale a session's last use may get before it is
// recorded again, which saves a write on most requests.
const touchAfter = time.Minute

// Service is the identity module.
type Service struct {
	db        *pgxpool.Pool
	logger    *slog.Logger
	cfg       Config
	passwords *passwords
	sso       *sso // nil without an identity provider
	// setup holds the hash of the current setup code while setup is open.
	setup atomic.Pointer[[32]byte]
}

// New returns the identity service.
func New(db *pgxpool.Pool, logger *slog.Logger, cfg Config) (*Service, error) {
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = DefaultIdleTimeout
	}
	if cfg.AbsoluteTimeout <= 0 {
		cfg.AbsoluteTimeout = DefaultAbsoluteTimeout
	}
	if cfg.Argon2 == (Argon2Params{}) {
		cfg.Argon2 = DefaultArgon2
	}
	if cfg.MaxConcurrentHashes <= 0 {
		cfg.MaxConcurrentHashes = 2
	}
	if cfg.SetupCode != "" && len(normalizeSetupCode(cfg.SetupCode)) < minSetupCodeLength {
		return nil, fmt.Errorf("the setup code must have at least %d letters or digits", minSetupCodeLength)
	}
	pw, err := newPasswords(cfg.Argon2, cfg.MaxConcurrentHashes)
	if err != nil {
		return nil, err
	}
	svc := &Service{db: db, logger: logger, cfg: cfg, passwords: pw}
	if cfg.SSO.Issuer != "" {
		if svc.sso, err = newSSO(cfg.SSO); err != nil {
			return nil, err
		}
	}
	return svc, nil
}

// querier is what both a pool and a transaction can do.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}
