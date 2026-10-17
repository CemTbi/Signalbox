// SPDX-License-Identifier: Apache-2.0

package identity_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/CemTbi/Signalbox/internal/modules/identity"
	"github.com/CemTbi/Signalbox/internal/platform/oidctest"
	"github.com/CemTbi/Signalbox/internal/platform/postgres/postgrestest"
)

// newSSOService returns a service with single sign-on through a fake
// provider, on an installation set up with Ada's admin account.
func newSSOService(t *testing.T, change func(*identity.SSOConfig)) (*identity.Service, *oidctest.Provider, *pgxpool.Pool) {
	t.Helper()
	svc, idp, pool := newSSOServiceBeforeSetup(t, change)
	setUp(t, svc)
	return svc, idp, pool
}

func newSSOServiceBeforeSetup(t *testing.T, change func(*identity.SSOConfig)) (*identity.Service, *oidctest.Provider, *pgxpool.Pool) {
	t.Helper()
	idp := oidctest.New(t)
	pool := postgrestest.New(t)
	cfg := identity.SSOConfig{
		Issuer: idp.Issuer, ClientID: idp.ClientID, ClientSecret: idp.ClientSecret, Name: "Test IdP",
		RedirectURL: "https://signalbox.test/api/v1/sso/callback",
	}
	if change != nil {
		change(&cfg)
	}
	svc, err := identity.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)),
		identity.Config{SetupCode: setupCode, Argon2: testArgon2, SSO: cfg})
	if err != nil {
		t.Fatal(err)
	}
	return svc, idp, pool
}

// signInWithSSO runs a whole sign-in through the fake provider.
func signInWithSSO(t *testing.T, svc *identity.Service, returnPath string) (*identity.NewSession, string, error) {
	t.Helper()
	started, err := svc.StartSSO(context.Background(), returnPath)
	if err != nil {
		t.Fatal(err)
	}
	state, code := oidctest.Authorize(t, started.URL)
	return svc.FinishSSO(context.Background(), state, code, started.BrowserToken, client)
}

func countUsers(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM users`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSSOCreatesAnAccountOnFirstSignIn(t *testing.T) {
	svc, idp, pool := newSSOService(t, nil)
	ctx := context.Background()
	if name, ok := svc.SSOName(); !ok || name != "Test IdP" {
		t.Errorf("SSOName = %q, %v", name, ok)
	}
	idp.SignInAs(oidctest.Identity{Subject: "grace-1", Email: "grace@example.com", EmailVerified: true, Name: "Grace Hopper"})

	ns, back, err := signInWithSSO(t, svc, "/projects/prj_1")
	if err != nil {
		t.Fatal(err)
	}
	if ns.User.Email != "grace@example.com" || ns.User.Name != "Grace Hopper" || ns.User.Admin || back != "/projects/prj_1" {
		t.Errorf("first sign-in = %+v, back to %q", ns.User, back)
	}
	if _, err := svc.Authenticate(ctx, ns.Token); err != nil {
		t.Errorf("the new session doesn't work: %v", err)
	}

	again, _, err := signInWithSSO(t, svc, "/")
	if err != nil || again.User.ID != ns.User.ID {
		t.Fatalf("second sign-in = %+v, %v; want the same account", again, err)
	}
	if n := countUsers(t, pool); n != 2 {
		t.Errorf("%d accounts after two sign-ins, want Ada's and Grace's", n)
	}
	// An account made through single sign-on has no password.
	if _, err := svc.SignIn(ctx, "grace@example.com", "", client); !errors.Is(err, identity.ErrInvalidCredentials) {
		t.Errorf("password sign-in for an SSO account: error = %v", err)
	}
}

func TestSSONeverLinksAnAccountByEmail(t *testing.T) {
	svc, idp, _ := newSSOService(t, nil) // ada@example.com has a password account
	idp.SignInAs(oidctest.Identity{Subject: "someone", Email: "ADA@example.com", EmailVerified: true, Name: "Not Ada"})
	if _, _, err := signInWithSSO(t, svc, "/"); !errors.Is(err, identity.ErrSSOAccountExists) {
		t.Errorf("error = %v, want ErrSSOAccountExists", err)
	}
}

func TestSSOEmailRules(t *testing.T) {
	tests := []struct {
		name     string
		config   func(*identity.SSOConfig)
		email    string
		verified any
		want     error
	}{
		{"unverified", nil, "a@example.com", false, identity.ErrSSOEmailUnverified},
		{"no verified claim", nil, "a@example.com", nil, identity.ErrSSOEmailUnverified},
		{"verified as a string", nil, "a@example.com", "true", nil},
		{"trusted provider", func(c *identity.SSOConfig) { c.TrustUnverifiedEmail = true }, "a@example.com", nil, nil},
		{"domain not allowed", func(c *identity.SSOConfig) { c.AllowedDomains = []string{"example.com"} }, "a@other.org", true, identity.ErrSSONotAllowed},
		{"allowed domain", func(c *identity.SSOConfig) { c.AllowedDomains = []string{" @Example.COM"} }, "a@example.com", true, nil},
		{"no email", nil, "", true, identity.ErrSSOFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, idp, _ := newSSOService(t, tt.config)
			idp.SignInAs(oidctest.Identity{Subject: "s", Email: tt.email, EmailVerified: tt.verified, Name: "A"})
			_, _, err := signInWithSSO(t, svc, "/")
			if tt.want == nil && err != nil || tt.want != nil && !errors.Is(err, tt.want) {
				t.Errorf("error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestSSOAttemptsWorkOnceAndOnlyInTheirBrowser(t *testing.T) {
	svc, idp, pool := newSSOService(t, nil)
	ctx := context.Background()
	idp.SignInAs(oidctest.Identity{Subject: "g", Email: "grace@example.com", EmailVerified: true, Name: "Grace"})

	started, err := svc.StartSSO(ctx, "/")
	if err != nil {
		t.Fatal(err)
	}
	state, code := oidctest.Authorize(t, started.URL)
	otherBrowser, _ := svc.StartSSO(ctx, "/")
	if _, _, err := svc.FinishSSO(ctx, state, code, otherBrowser.BrowserToken, client); !errors.Is(err, identity.ErrSSOFailed) {
		t.Errorf("another browser's token: error = %v, want ErrSSOFailed", err)
	}
	// The attempt is used up even though it failed.
	if _, _, err := svc.FinishSSO(ctx, state, code, started.BrowserToken, client); !errors.Is(err, identity.ErrSSOExpired) {
		t.Errorf("reusing a state: error = %v, want ErrSSOExpired", err)
	}

	late, err := svc.StartSSO(ctx, "/")
	if err != nil {
		t.Fatal(err)
	}
	state, code = oidctest.Authorize(t, late.URL)
	if _, err := pool.Exec(ctx, `UPDATE sso_attempts SET expires_at = now() - interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.FinishSSO(ctx, state, code, late.BrowserToken, client); !errors.Is(err, identity.ErrSSOExpired) {
		t.Errorf("an expired attempt: error = %v, want ErrSSOExpired", err)
	}
	if _, _, err := svc.FinishSSO(ctx, "not-a-state", "code", late.BrowserToken, client); !errors.Is(err, identity.ErrSSOFailed) {
		t.Errorf("a malformed state: error = %v, want ErrSSOFailed", err)
	}
}

func TestSSORefusesAnIDTokenWithAnotherNonce(t *testing.T) {
	svc, idp, pool := newSSOService(t, nil)
	idp.SignInAs(oidctest.Identity{Subject: "g", Email: "grace@example.com", EmailVerified: true, Name: "Grace"})
	idp.SendWrongNonce(true)
	if _, _, err := signInWithSSO(t, svc, "/"); !errors.Is(err, identity.ErrSSOFailed) {
		t.Errorf("error = %v, want ErrSSOFailed", err)
	}
	if n := countUsers(t, pool); n != 1 {
		t.Errorf("%d accounts after a refused sign-in, want only Ada's", n)
	}
}

func TestSSOReadsTheEmailFromUserinfo(t *testing.T) {
	svc, idp, _ := newSSOService(t, nil)
	idp.SignInAs(oidctest.Identity{Subject: "g", Email: "grace@example.com", EmailVerified: true, Name: "Grace"})
	idp.SendEmailOnlyInUserinfo(true)
	ns, _, err := signInWithSSO(t, svc, "/")
	if err != nil || ns.User.Email != "grace@example.com" {
		t.Errorf("sign-in = %+v, %v", ns, err)
	}
}

func TestSSOOpensAfterFirstRunSetup(t *testing.T) {
	svc, idp, pool := newSSOServiceBeforeSetup(t, nil)
	ctx := context.Background()
	if _, err := svc.StartSSO(ctx, "/"); !errors.Is(err, identity.ErrSSOSetupRequired) {
		t.Errorf("StartSSO before setup: error = %v, want ErrSSOSetupRequired", err)
	}

	// An attempt started while the installation had accounts can't create
	// the first one once they are gone.
	setUp(t, svc)
	idp.SignInAs(oidctest.Identity{Subject: "g", Email: "grace@example.com", EmailVerified: true, Name: "Grace"})
	started, err := svc.StartSSO(ctx, "/")
	if err != nil {
		t.Fatal(err)
	}
	state, code := oidctest.Authorize(t, started.URL)
	if _, err := pool.Exec(ctx, `DELETE FROM users`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.FinishSSO(ctx, state, code, started.BrowserToken, client); !errors.Is(err, identity.ErrSSOSetupRequired) {
		t.Errorf("FinishSSO without accounts: error = %v, want ErrSSOSetupRequired", err)
	}
	if n := countUsers(t, pool); n != 0 {
		t.Errorf("%d accounts, want none", n)
	}
}

func TestSSOWithoutAProvider(t *testing.T) {
	svc, _, _ := newService(t, identity.Config{SetupCode: setupCode})
	if _, ok := svc.SSOName(); ok {
		t.Error("SSOName reports single sign-on without a provider")
	}
	if _, err := svc.StartSSO(context.Background(), "/"); !errors.Is(err, identity.ErrSSODisabled) {
		t.Errorf("StartSSO error = %v, want ErrSSODisabled", err)
	}

	unreachable, _, _ := newSSOService(t, func(c *identity.SSOConfig) { c.Issuer = "http://127.0.0.1:1/idp" })
	if _, err := unreachable.StartSSO(context.Background(), "/"); !errors.Is(err, identity.ErrSSOUnavailable) {
		t.Errorf("unreachable provider: error = %v, want ErrSSOUnavailable", err)
	}
}

func TestNewRejectsIncompleteSSOConfig(t *testing.T) {
	for _, cfg := range []identity.SSOConfig{
		{Issuer: "https://idp.example.com", RedirectURL: "https://s/cb"},           // no client ID
		{Issuer: "idp.example.com", ClientID: "c", RedirectURL: "https://s/cb"},    // not a URL
		{Issuer: "ftp://idp.example.com", ClientID: "c", RedirectURL: "https://s"}, // wrong scheme
	} {
		if _, err := identity.New(nil, slog.New(slog.NewTextHandler(io.Discard, nil)), identity.Config{SSO: cfg}); err == nil {
			t.Errorf("New accepted %+v", cfg)
		}
	}
}

func TestSafeReturnPath(t *testing.T) {
	for in, want := range map[string]string{
		"/projects/prj_1?tab=x":         "/projects/prj_1?tab=x",
		"/invite":                       "/invite",
		"":                              "/",
		"//evil.example":                "/",
		"/\\evil.example":               "/",
		"https://evil.example":          "/",
		"/ok\r\nSet-Cookie: x":          "/",
		"/" + strings.Repeat("a", 3000): "/",
	} {
		if got := identity.SafeReturnPath(in); got != want {
			t.Errorf("SafeReturnPath(%q) = %q, want %q", in, got, want)
		}
	}
}
