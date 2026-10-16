// SPDX-License-Identifier: Apache-2.0

package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/CemTbi/Signalbox/internal/platform/id"
	"github.com/CemTbi/Signalbox/internal/platform/validate"
)

// First-run setup creates the first admin account. It needs a setup code,
// so whoever reaches a new installation first can't claim it; the code is
// written to the server log, which only the operator can read. See
// docs/adr/0010-first-run-setup.md.

const (
	// setupCodeAlphabet is Crockford's base32 in upper case: no I, L, O or
	// U, so a code read from a log is hard to mistype.
	setupCodeAlphabet  = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	minSetupCodeLength = 12
	// setupLockID serializes concurrent setup attempts across processes.
	setupLockID = 7_313_026_401
)

// SetupInput is what the setup page sends.
type SetupInput struct {
	Code     string
	Name     string
	Email    string
	Password string
}

// SetupRequired reports whether the installation has no accounts yet.
func (s *Service) SetupRequired(ctx context.Context) (bool, error) {
	var exists bool
	if err := s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users)`).Scan(&exists); err != nil {
		return false, fmt.Errorf("check for accounts: %w", err)
	}
	return !exists, nil
}

// PrepareSetup opens first-run setup when the installation has no accounts.
// It logs where to go and, unless the operator chose the code with
// Config.SetupCode, the code to enter. Call it once at startup.
func (s *Service) PrepareSetup(ctx context.Context, setupURL string) error {
	required, err := s.SetupRequired(ctx)
	if err != nil || !required {
		return err
	}
	code := s.cfg.SetupCode
	if code == "" {
		code = newSetupCode()
		s.logger.WarnContext(ctx, "Signalbox has no accounts yet. Open the setup page and enter the setup code to create the first admin account.",
			"setup_url", setupURL, "setup_code", code)
	} else {
		s.logger.WarnContext(ctx, "Signalbox has no accounts yet. Open the setup page and enter the setup code from SIGNALBOX_SETUP_CODE to create the first admin account.",
			"setup_url", setupURL)
	}
	digest := sha256.Sum256([]byte(normalizeSetupCode(code)))
	s.setup.Store(&digest)
	return nil
}

// CompleteSetup creates the first admin account and signs it in.
func (s *Service) CompleteSetup(ctx context.Context, in SetupInput, client Client) (*NewSession, error) {
	want := s.setup.Load()
	if want == nil {
		if required, err := s.SetupRequired(ctx); err != nil {
			return nil, err
		} else if !required {
			return nil, ErrSetupComplete
		}
		return nil, ErrInvalidSetupCode // setup wasn't prepared in this process
	}
	got := sha256.Sum256([]byte(normalizeSetupCode(in.Code)))
	if subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
		s.logger.WarnContext(ctx, "setup attempt with a wrong setup code", "client_ip", client.IP)
		return nil, ErrInvalidSetupCode
	}

	var errs validate.Errors
	name := validate.Name(&errs, "/name", in.Name)
	email := validate.Email(&errs, "/email", in.Email)
	validate.NewPassword(&errs, "/password", in.Password)
	if err := errs.Err(); err != nil {
		return nil, err
	}
	hash, err := s.passwords.hash(ctx, in.Password)
	if err != nil {
		return nil, err
	}

	var ns *NewSession
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, setupLockID); err != nil {
			return err
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users)`).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return ErrSetupComplete
		}
		u := User{ID: id.New(), Email: email, Name: name, Admin: true}
		err := tx.QueryRow(ctx, `
			INSERT INTO users (id, email, name, password_hash, admin) VALUES ($1, $2, $3, $4, true)
			RETURNING created_at, updated_at`, u.ID, u.Email, u.Name, hash,
		).Scan(&u.CreatedAt, &u.UpdatedAt)
		if err != nil {
			return fmt.Errorf("create the first account: %w", err)
		}
		ns, err = s.createSession(ctx, tx, u, client)
		return err
	})
	if errors.Is(err, ErrSetupComplete) {
		s.setup.Store(nil)
		return nil, err
	}
	if err != nil {
		return nil, fmt.Errorf("complete setup: %w", err)
	}
	s.setup.Store(nil)
	s.logger.InfoContext(ctx, "setup complete: created the first admin account", id.LogAttr("user_id", id.User, ns.User.ID), "client_ip", client.IP)
	return ns, nil
}

// newSetupCode returns 16 random characters, about 80 bits, in groups of
// four such as "K7QM-2XWP-9D4H-TR6B".
func newSetupCode() string {
	var b strings.Builder
	for i := range 16 {
		if i > 0 && i%4 == 0 {
			b.WriteByte('-')
		}
		b.WriteByte(setupCodeAlphabet[randIntn(len(setupCodeAlphabet))])
	}
	return b.String()
}

func randIntn(n int) int {
	var buf [1]byte
	for {
		_, _ = rand.Read(buf[:])
		// len(setupCodeAlphabet) divides 256, so there is no modulo bias.
		if v := int(buf[0]); v < 256-256%n {
			return v % n
		}
	}
}

// normalizeSetupCode makes codes compare equal however they are typed:
// case, spaces and hyphens don't matter.
func normalizeSetupCode(code string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '-' || r == ' ' || r == '\t':
			return -1
		case 'a' <= r && r <= 'z':
			return r - 'a' + 'A'
		}
		return r
	}, code)
}
