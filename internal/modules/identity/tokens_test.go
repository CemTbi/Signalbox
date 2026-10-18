// SPDX-License-Identifier: Apache-2.0

package identity_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/CemTbi/Signalbox/internal/modules/identity"
	"github.com/CemTbi/Signalbox/internal/platform/validate"
)

func TestAccessTokens(t *testing.T) {
	svc, pool, logs := newService(t, identity.Config{SetupCode: setupCode})
	ctx := context.Background()
	admin := setUp(t, svc).User

	tok, err := svc.CreateAccessToken(ctx, admin.ID, " GitHub Actions ", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tok.Token, identity.AccessTokenPrefix) || len(tok.Token) != len(identity.AccessTokenPrefix)+43 ||
		tok.Name != "GitHub Actions" || tok.Hint != tok.Token[len(tok.Token)-4:] {
		t.Errorf("token = %+v", tok)
	}
	if d := tok.ExpiresAt.Sub(tok.CreatedAt); d != identity.DefaultAccessTokenLifetime {
		t.Errorf("lifetime = %v", d)
	}
	// The secret is never stored or logged.
	var stored int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM access_tokens WHERE token_hash = convert_to($1, 'UTF8')`, tok.Token).Scan(&stored); err != nil || stored != 0 {
		t.Errorf("the token is stored in the clear: %d, %v", stored, err)
	}
	if strings.Contains(logs.String(), tok.Token) {
		t.Error("the token was logged")
	}

	sess, err := svc.AuthenticateAccessToken(ctx, tok.Token)
	if err != nil || sess.User.ID != admin.ID || sess.AccessTokenID != tok.ID || sess.CSRFToken != "" {
		t.Fatalf("authenticate = %+v, %v", sess, err)
	}
	list, err := svc.ListAccessTokens(ctx, admin.ID)
	if err != nil || len(list) != 1 || list[0].LastUsedAt == nil {
		t.Errorf("list = %+v, %v", list, err)
	}

	for _, bad := range []string{"", "sbx_pat_", "sbx_pat_" + strings.Repeat("A", 43), tok.Token[len(identity.AccessTokenPrefix):], tok.Token + "x"} {
		if _, err := svc.AuthenticateAccessToken(ctx, bad); !errors.Is(err, identity.ErrUnauthenticated) {
			t.Errorf("%q authenticated: %v", bad, err)
		}
	}

	// Someone else can't revoke it.
	other, err := svc.CreateUser(ctx, identity.UserInput{Name: "Grace", Email: "grace@example.com", Password: password})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.RevokeAccessToken(ctx, other.ID, tok.ID); !errors.Is(err, identity.ErrTokenNotFound) {
		t.Errorf("revoked someone else's token: %v", err)
	}
	if err := svc.RevokeAccessToken(ctx, admin.ID, tok.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AuthenticateAccessToken(ctx, tok.Token); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Errorf("a revoked token works: %v", err)
	}
	if err := svc.RevokeAccessToken(ctx, admin.ID, uuid.New()); !errors.Is(err, identity.ErrTokenNotFound) {
		t.Errorf("revoking nothing: %v", err)
	}
}

func TestAccessTokensExpire(t *testing.T) {
	svc, pool, _ := newService(t, identity.Config{SetupCode: setupCode})
	ctx := context.Background()
	admin := setUp(t, svc).User
	tok, err := svc.CreateAccessToken(ctx, admin.ID, "Old", 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE access_tokens SET created_at = now() - interval '40 days', expires_at = now() - interval '35 days'`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AuthenticateAccessToken(ctx, tok.Token); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Errorf("an expired token works: %v", err)
	}
	if n, err := svc.RemoveExpiredAccessTokens(ctx); err != nil || n != 1 {
		t.Errorf("removed %d, %v", n, err)
	}
}

func TestAccessTokenInput(t *testing.T) {
	svc, _, _ := newService(t, identity.Config{SetupCode: setupCode})
	ctx := context.Background()
	admin := setUp(t, svc).User
	var verrs validate.Errors
	if _, err := svc.CreateAccessToken(ctx, admin.ID, " ", 400*24*time.Hour); !errors.As(err, &verrs) || len(verrs) != 2 {
		t.Errorf("bad input: %v", err)
	}
	for i := range 50 {
		if _, err := svc.CreateAccessToken(ctx, admin.ID, "T", 24*time.Hour); err != nil {
			t.Fatalf("token %d: %v", i, err)
		}
	}
	if _, err := svc.CreateAccessToken(ctx, admin.ID, "One too many", 0); !errors.Is(err, identity.ErrTooManyTokens) {
		t.Errorf("51st token: %v", err)
	}
}
