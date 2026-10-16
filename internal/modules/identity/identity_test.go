// SPDX-License-Identifier: Apache-2.0

package identity_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/netip"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/CemTbi/Signalbox/internal/modules/identity"
	"github.com/CemTbi/Signalbox/internal/platform/postgres/postgrestest"
	"github.com/CemTbi/Signalbox/internal/platform/validate"
)

// Cheap hashing parameters keep the tests fast. Production uses DefaultArgon2.
var testArgon2 = identity.Argon2Params{Memory: 64, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32}

const (
	setupCode = "TEST-SETU-PCOD-E123"
	password  = "correct horse battery staple"
)

var client = identity.Client{IP: netip.MustParseAddr("192.0.2.10"), UserAgent: "identity_test"}

func newService(t *testing.T, cfg identity.Config) (*identity.Service, *pgxpool.Pool, *bytes.Buffer) {
	t.Helper()
	pool := postgrestest.New(t)
	var logs bytes.Buffer
	if cfg.Argon2 == (identity.Argon2Params{}) {
		cfg.Argon2 = testArgon2
	}
	svc, err := identity.New(pool, slog.New(slog.NewTextHandler(&logs, nil)), cfg)
	if err != nil {
		t.Fatal(err)
	}
	return svc, pool, &logs
}

// setUp completes first-run setup and returns the admin's session.
func setUp(t *testing.T, svc *identity.Service) *identity.NewSession {
	t.Helper()
	ctx := context.Background()
	if err := svc.PrepareSetup(ctx, "http://localhost:8080/setup"); err != nil {
		t.Fatal(err)
	}
	ns, err := svc.CompleteSetup(ctx, identity.SetupInput{
		Code: setupCode, Name: "Ada Lovelace", Email: "Ada@Example.com", Password: password,
	}, client)
	if err != nil {
		t.Fatalf("CompleteSetup: %v", err)
	}
	return ns
}

func TestSetupCreatesAdminAndSignsIn(t *testing.T) {
	svc, _, logs := newService(t, identity.Config{SetupCode: setupCode})
	ctx := context.Background()

	if required, err := svc.SetupRequired(ctx); err != nil || !required {
		t.Fatalf("SetupRequired = %v, %v before setup", required, err)
	}
	ns := setUp(t, svc)
	if !ns.User.Admin || ns.User.Name != "Ada Lovelace" || ns.User.Email != "Ada@Example.com" {
		t.Errorf("user = %+v", ns.User)
	}
	if len(ns.Token) != 43 || len(ns.CSRFToken) != 43 || ns.Token == ns.CSRFToken {
		t.Errorf("token %q and CSRF token %q should be distinct 256-bit values", ns.Token, ns.CSRFToken)
	}
	if got := ns.ExpiresAt.Sub(ns.CreatedAt); got != identity.DefaultAbsoluteTimeout {
		t.Errorf("session lasts %s, want %s", got, identity.DefaultAbsoluteTimeout)
	}
	if required, _ := svc.SetupRequired(ctx); required {
		t.Error("SetupRequired is still true after setup")
	}
	if strings.Contains(logs.String(), setupCode) {
		t.Error("a setup code chosen by the operator must not be logged")
	}

	// Setup can't run twice, even with the right code.
	_, err := svc.CompleteSetup(ctx, identity.SetupInput{Code: setupCode, Name: "Mallory", Email: "m@example.com", Password: password}, client)
	if !errors.Is(err, identity.ErrSetupComplete) {
		t.Errorf("second CompleteSetup error = %v, want ErrSetupComplete", err)
	}
}

func TestSetupLogsAGeneratedCode(t *testing.T) {
	svc, _, logs := newService(t, identity.Config{})
	ctx := context.Background()
	if err := svc.PrepareSetup(ctx, "http://localhost:8080/setup"); err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`setup_code=([0-9A-Z]{4}(?:-[0-9A-Z]{4}){3})`).FindStringSubmatch(logs.String())
	if m == nil {
		t.Fatalf("no setup code in the log:\n%s", logs.String())
	}
	// Case and hyphens don't matter.
	typed := strings.ToLower(strings.ReplaceAll(m[1], "-", " "))
	if _, err := svc.CompleteSetup(ctx, identity.SetupInput{Code: typed, Name: "Ada", Email: "ada@example.com", Password: password}, client); err != nil {
		t.Fatalf("CompleteSetup with the logged code: %v", err)
	}
}

func TestSetupRejectsWrongCodeAndInvalidFields(t *testing.T) {
	svc, _, _ := newService(t, identity.Config{SetupCode: setupCode})
	ctx := context.Background()
	if err := svc.PrepareSetup(ctx, "http://localhost:8080/setup"); err != nil {
		t.Fatal(err)
	}

	_, err := svc.CompleteSetup(ctx, identity.SetupInput{Code: "WRONG-CODE-1234", Name: "Ada", Email: "ada@example.com", Password: password}, client)
	if !errors.Is(err, identity.ErrInvalidSetupCode) {
		t.Errorf("wrong code: error = %v, want ErrInvalidSetupCode", err)
	}

	_, err = svc.CompleteSetup(ctx, identity.SetupInput{Code: setupCode, Name: " ", Email: "not-an-email", Password: "short"}, client)
	var fields validate.Errors
	if !errors.As(err, &fields) || len(fields) != 3 {
		t.Fatalf("invalid fields: error = %v, want three field errors", err)
	}
	if required, _ := svc.SetupRequired(ctx); !required {
		t.Error("a failed setup must not create an account")
	}
}

func TestConcurrentSetupCreatesOneAdmin(t *testing.T) {
	svc, pool, _ := newService(t, identity.Config{SetupCode: setupCode})
	ctx := context.Background()
	if err := svc.PrepareSetup(ctx, "http://localhost:8080/setup"); err != nil {
		t.Fatal(err)
	}
	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		succeeded int
	)
	for i := range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.CompleteSetup(ctx, identity.SetupInput{
				Code: setupCode, Name: "Admin", Email: "admin" + string(rune('a'+i)) + "@example.com", Password: password,
			}, client)
			if err == nil {
				mu.Lock()
				succeeded++
				mu.Unlock()
			} else if !errors.Is(err, identity.ErrSetupComplete) && !errors.Is(err, identity.ErrInvalidSetupCode) {
				t.Errorf("CompleteSetup: %v", err)
			}
		}()
	}
	wg.Wait()
	var users int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM users").Scan(&users); err != nil {
		t.Fatal(err)
	}
	if succeeded != 1 || users != 1 {
		t.Errorf("%d setups succeeded and %d users exist, want 1 and 1", succeeded, users)
	}
}

func TestSignInAndAuthenticate(t *testing.T) {
	svc, _, logs := newService(t, identity.Config{SetupCode: setupCode})
	ctx := context.Background()
	admin := setUp(t, svc)

	// Email addresses match regardless of case and surrounding spaces.
	ns, err := svc.SignIn(ctx, "  ada@EXAMPLE.com ", password, client)
	if err != nil {
		t.Fatalf("SignIn: %v", err)
	}
	if ns.User.ID != admin.User.ID || ns.Token == admin.Token {
		t.Errorf("SignIn returned user %s token %q, want the admin with a new token", ns.User.ID, ns.Token)
	}

	sess, err := svc.Authenticate(ctx, ns.Token)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if sess.User.ID != admin.User.ID || sess.CSRFToken != ns.CSRFToken {
		t.Errorf("Authenticate = %+v", sess)
	}

	for _, tt := range []struct{ email, password string }{
		{"ada@example.com", "wrong password!"},
		{"nobody@example.com", password},
	} {
		if _, err := svc.SignIn(ctx, tt.email, tt.password, client); !errors.Is(err, identity.ErrInvalidCredentials) {
			t.Errorf("SignIn(%s) error = %v, want ErrInvalidCredentials", tt.email, err)
		}
	}
	if strings.Contains(logs.String(), password) {
		t.Error("a password reached the log")
	}
	if !strings.Contains(logs.String(), "reason=\"wrong password\"") {
		t.Errorf("failed sign-ins should be logged:\n%s", logs.String())
	}
}

func TestAuthenticateRejectsUnknownMalformedAndExpiredTokens(t *testing.T) {
	svc, pool, _ := newService(t, identity.Config{SetupCode: setupCode})
	ctx := context.Background()
	ns := setUp(t, svc)

	for _, token := range []string{"", "short", strings.Repeat("A", 43), ns.Token + "x", strings.Repeat("!", 43)} {
		if _, err := svc.Authenticate(ctx, token); !errors.Is(err, identity.ErrUnauthenticated) {
			t.Errorf("Authenticate(%q) error = %v, want ErrUnauthenticated", token, err)
		}
	}

	// Idle for longer than the idle timeout.
	if _, err := pool.Exec(ctx, "UPDATE sessions SET last_seen_at = now() - interval '13 hours'"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authenticate(ctx, ns.Token); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Errorf("idle session: error = %v, want ErrUnauthenticated", err)
	}

	// Past the absolute expiry, even when recently used.
	if _, err := pool.Exec(ctx, "UPDATE sessions SET last_seen_at = now(), expires_at = now() - interval '1 second'"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authenticate(ctx, ns.Token); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Errorf("expired session: error = %v, want ErrUnauthenticated", err)
	}

	removed, err := svc.RemoveExpiredSessions(ctx)
	if err != nil || removed != 1 {
		t.Errorf("RemoveExpiredSessions = %d, %v, want 1", removed, err)
	}
}

func TestAuthenticateKeepsActiveSessionsAlive(t *testing.T) {
	svc, pool, _ := newService(t, identity.Config{SetupCode: setupCode})
	ctx := context.Background()
	ns := setUp(t, svc)

	if _, err := pool.Exec(ctx, "UPDATE sessions SET last_seen_at = now() - interval '11 hours'"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authenticate(ctx, ns.Token); err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	var idleSeconds int64
	if err := pool.QueryRow(ctx, "SELECT extract(epoch FROM now() - last_seen_at)::bigint FROM sessions").Scan(&idleSeconds); err != nil {
		t.Fatal(err)
	}
	if idle := time.Duration(idleSeconds) * time.Second; idle > time.Minute {
		t.Errorf("last use is %s ago after Authenticate, want it reset", idle)
	}
}

func TestSignOutEndsTheSession(t *testing.T) {
	svc, _, _ := newService(t, identity.Config{SetupCode: setupCode})
	ctx := context.Background()
	ns := setUp(t, svc)

	if err := svc.SignOut(ctx, ns.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authenticate(ctx, ns.Token); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Errorf("after sign-out: error = %v, want ErrUnauthenticated", err)
	}
	if err := svc.SignOut(ctx, ns.Token); err != nil {
		t.Errorf("signing out twice: %v", err)
	}
}

func TestSignInUpgradesOutdatedHashes(t *testing.T) {
	svc, pool, logs := newService(t, identity.Config{SetupCode: setupCode})
	ctx := context.Background()
	setUp(t, svc)

	stronger := testArgon2
	stronger.Iterations = 2
	upgraded, err := identity.New(pool, slog.New(slog.NewTextHandler(logs, nil)), identity.Config{Argon2: stronger})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := upgraded.SignIn(ctx, "ada@example.com", password, client); err != nil {
		t.Fatalf("SignIn: %v", err)
	}
	var hash string
	if err := pool.QueryRow(ctx, "SELECT password_hash FROM users").Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(hash, ",t=2,") {
		t.Errorf("hash %s wasn't upgraded to t=2", hash)
	}
	// The old service still verifies the upgraded hash.
	if _, err := svc.SignIn(ctx, "ada@example.com", password, client); err != nil {
		t.Errorf("SignIn after upgrade: %v", err)
	}
}

func TestCreateUser(t *testing.T) {
	svc, _, _ := newService(t, identity.Config{SetupCode: setupCode})
	ctx := context.Background()
	setUp(t, svc)

	u, err := svc.CreateUser(ctx, identity.UserInput{Name: " Grace Hopper ", Email: "grace@example.com", Password: password})
	if err != nil {
		t.Fatal(err)
	}
	if u.Name != "Grace Hopper" || u.Admin {
		t.Errorf("CreateUser = %+v", u)
	}
	if _, err := svc.SignIn(ctx, "grace@example.com", password, client); err != nil {
		t.Errorf("the new account can't sign in: %v", err)
	}
	_, err = svc.CreateUser(ctx, identity.UserInput{Name: "Imposter", Email: "GRACE@example.com", Password: password})
	if !errors.Is(err, identity.ErrEmailTaken) {
		t.Errorf("same email in other case: error = %v, want ErrEmailTaken", err)
	}
	_, err = svc.CreateUser(ctx, identity.UserInput{Name: "", Email: "x", Password: "short"})
	var fields validate.Errors
	if !errors.As(err, &fields) || len(fields) != 3 {
		t.Errorf("invalid input: error = %v, want three field errors", err)
	}
}

func TestNewRejectsWeakSetupCode(t *testing.T) {
	if _, err := identity.New(nil, slog.New(slog.NewTextHandler(io.Discard, nil)), identity.Config{SetupCode: "1234"}); err == nil {
		t.Error("New accepted a four-character setup code")
	}
}
