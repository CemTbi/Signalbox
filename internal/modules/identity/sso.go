// SPDX-License-Identifier: Apache-2.0

package identity

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/jackc/pgx/v5"
	"golang.org/x/oauth2"

	"github.com/CemTbi/Signalbox/internal/platform/id"
	"github.com/CemTbi/Signalbox/internal/platform/token"
	"github.com/CemTbi/Signalbox/internal/platform/validate"
)

// Single sign-on through OpenID Connect: the authorization code flow with
// PKCE, a state bound to the browser that started the sign-in, and a nonce
// in the ID token. People are identified by the provider's issuer and
// subject; their email address is never used to find an existing account.
// See docs/adr/0013-single-sign-on-accounts.md.

// SSOConfig connects Signalbox to an OpenID Connect identity provider.
type SSOConfig struct {
	// Issuer is the provider's issuer URL. Single sign-on is off when it is
	// empty.
	Issuer       string
	ClientID     string
	ClientSecret string
	// Name is how the sign-in page names the provider, as in "Sign in with
	// Okta". Default "single sign-on".
	Name string
	// Scopes are requested besides openid. Default email and profile.
	Scopes []string
	// AllowedDomains limits which email domains may create an account on
	// their first sign-in. Empty allows every domain.
	AllowedDomains []string
	// TrustUnverifiedEmail accepts email addresses the provider doesn't
	// mark as verified, for providers that never send email_verified.
	TrustUnverifiedEmail bool
	// RedirectURL is where the provider sends people back, the public URL
	// followed by /api/v1/sso/callback.
	RedirectURL string
	// HTTPClient talks to the provider. Default: a 10-second timeout.
	HTTPClient *http.Client
}

// Errors from single sign-on.
var (
	// ErrSSODisabled means no identity provider is configured.
	ErrSSODisabled = errors.New("identity: single sign-on isn't configured")
	// ErrSSOUnavailable means the identity provider couldn't be reached.
	ErrSSOUnavailable = errors.New("identity: the identity provider can't be reached")
	// ErrSSOExpired means the sign-in took too long or was already used.
	ErrSSOExpired = errors.New("identity: the single sign-on attempt expired")
	// ErrSSOFailed means the provider's answer couldn't be verified, or the
	// sign-in was finished in another browser than the one that started it.
	ErrSSOFailed = errors.New("identity: single sign-on failed")
	// ErrSSOEmailUnverified means the provider didn't vouch for the email
	// address of someone without an account.
	ErrSSOEmailUnverified = errors.New("identity: the identity provider didn't verify the email address")
	// ErrSSONotAllowed means the email domain may not create an account.
	ErrSSONotAllowed = errors.New("identity: the email domain may not create an account")
	// ErrSSOAccountExists means an account has the email address but isn't
	// linked to the identity, and accounts are never linked by email alone.
	ErrSSOAccountExists = errors.New("identity: an account with the email address exists")
	// ErrSSOSetupRequired means the installation has no accounts yet. The
	// first account, its administrator, comes from first-run setup, so
	// whoever reaches a new installation first can't claim it.
	ErrSSOSetupRequired = errors.New("identity: single sign-on opens after first-run setup")
)

// ssoAttemptLifetime is how long someone has at the identity provider.
const ssoAttemptLifetime = 10 * time.Minute

type sso struct {
	cfg SSOConfig

	mu       sync.Mutex
	provider *oidc.Provider
	verifier *oidc.IDTokenVerifier
	oauth    *oauth2.Config
}

func newSSO(cfg SSOConfig) (*sso, error) {
	if cfg.ClientID == "" || cfg.RedirectURL == "" {
		return nil, errors.New("single sign-on needs a client ID and a redirect URL")
	}
	if u, err := url.Parse(cfg.Issuer); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, fmt.Errorf("single sign-on issuer %q: use the provider's https issuer URL", cfg.Issuer)
	}
	if cfg.Name == "" {
		cfg.Name = "single sign-on"
	}
	if len(cfg.Scopes) == 0 {
		cfg.Scopes = []string{"email", "profile"}
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	domains := make([]string, 0, len(cfg.AllowedDomains))
	for _, d := range cfg.AllowedDomains {
		if d = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(d), "@")); d != "" {
			domains = append(domains, d)
		}
	}
	cfg.AllowedDomains = domains
	return &sso{cfg: cfg}, nil
}

// ready discovers the provider the first time it is needed, so an
// unreachable provider doesn't keep the server from starting. A failed
// discovery is tried again on the next sign-in.
func (o *sso) ready() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.provider != nil {
		return nil
	}
	clientCtx := oidc.ClientContext(context.Background(), o.cfg.HTTPClient)
	discoverCtx, cancel := context.WithTimeout(clientCtx, 10*time.Second)
	defer cancel()
	p, err := oidc.NewProvider(discoverCtx, o.cfg.Issuer)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrSSOUnavailable, err)
	}
	o.provider = p
	o.verifier = p.VerifierContext(clientCtx, &oidc.Config{ClientID: o.cfg.ClientID})
	o.oauth = &oauth2.Config{
		ClientID:     o.cfg.ClientID,
		ClientSecret: o.cfg.ClientSecret,
		Endpoint:     p.Endpoint(),
		RedirectURL:  o.cfg.RedirectURL,
		Scopes:       append([]string{oidc.ScopeOpenID}, o.cfg.Scopes...),
	}
	return nil
}

func (o *sso) domainAllowed(email string) bool {
	if len(o.cfg.AllowedDomains) == 0 {
		return true
	}
	domain := strings.ToLower(email[strings.LastIndex(email, "@")+1:])
	for _, d := range o.cfg.AllowedDomains {
		if d == domain {
			return true
		}
	}
	return false
}

// SSOName returns the identity provider's name for the sign-in page, and
// whether single sign-on is configured.
func (s *Service) SSOName() (string, bool) {
	if s.sso == nil {
		return "", false
	}
	return s.sso.cfg.Name, true
}

// StartedSSO is a sign-in on its way to the identity provider.
type StartedSSO struct {
	// URL is where to send the browser.
	URL string
	// BrowserToken goes into a cookie; FinishSSO needs it back, from the
	// same browser.
	BrowserToken string
}

// StartSSO begins a sign-in. After it, the person comes back to
// returnPath, which must be a path on this site. It fails with
// ErrSSOSetupRequired until first-run setup has created the first account.
func (s *Service) StartSSO(ctx context.Context, returnPath string) (StartedSSO, error) {
	if s.sso == nil {
		return StartedSSO{}, ErrSSODisabled
	}
	if required, err := s.SetupRequired(ctx); err != nil {
		return StartedSSO{}, err
	} else if required {
		return StartedSSO{}, ErrSSOSetupRequired
	}
	if err := s.sso.ready(); err != nil {
		return StartedSSO{}, err
	}
	state, stateHash := token.New()
	browser, browserHash := token.New()
	nonce := token.Random()
	verifier := oauth2.GenerateVerifier()
	_, err := s.db.Exec(ctx, `
		INSERT INTO sso_attempts (state_hash, browser_hash, nonce, code_verifier, return_path, expires_at)
		VALUES ($1, $2, $3, $4, $5, now() + $6 * interval '1 second')`,
		stateHash, browserHash, nonce, verifier, SafeReturnPath(returnPath), int64(ssoAttemptLifetime/time.Second))
	if err != nil {
		return StartedSSO{}, fmt.Errorf("start single sign-on: %w", err)
	}
	return StartedSSO{
		URL:          s.sso.oauth.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier)),
		BrowserToken: browser,
	}, nil
}

// FinishSSO completes a sign-in when the identity provider sends the
// person back with a code. It returns their new session and the path to
// return to. Someone signing in for the first time gets an account, if
// their email address is verified and allowed and no account has it.
func (s *Service) FinishSSO(ctx context.Context, state, code, browserToken string, client Client) (*NewSession, string, error) {
	if s.sso == nil {
		return nil, "", ErrSSODisabled
	}
	if !token.WellFormed(state) || code == "" {
		return nil, "", ErrSSOFailed
	}
	// Use the attempt up before anything else, so a state works only once.
	var (
		browserHash                 []byte
		nonce, verifier, returnPath string
		fresh                       bool
	)
	err := s.db.QueryRow(ctx, `
		DELETE FROM sso_attempts WHERE state_hash = $1
		RETURNING browser_hash, nonce, code_verifier, return_path, expires_at > now()`,
		token.Hash(state)).Scan(&browserHash, &nonce, &verifier, &returnPath, &fresh)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !fresh {
		return nil, "", ErrSSOExpired
	}
	if err != nil {
		return nil, "", fmt.Errorf("finish single sign-on: %w", err)
	}
	if !token.WellFormed(browserToken) || subtle.ConstantTimeCompare(token.Hash(browserToken), browserHash) != 1 {
		s.logger.WarnContext(ctx, "single sign-on finished in another browser than it started in", "client_ip", client.IP)
		return nil, "", ErrSSOFailed
	}
	if err := s.sso.ready(); err != nil {
		return nil, "", err
	}

	httpCtx := oidc.ClientContext(ctx, s.sso.cfg.HTTPClient)
	tok, err := s.sso.oauth.Exchange(httpCtx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		s.logger.WarnContext(ctx, "single sign-on: the code exchange failed", "error", err)
		return nil, "", ErrSSOFailed
	}
	raw, ok := tok.Extra("id_token").(string)
	if !ok {
		s.logger.WarnContext(ctx, "single sign-on: the token response has no ID token")
		return nil, "", ErrSSOFailed
	}
	idToken, err := s.sso.verifier.Verify(httpCtx, raw)
	if err != nil {
		s.logger.WarnContext(ctx, "single sign-on: the ID token didn't verify", "error", err)
		return nil, "", ErrSSOFailed
	}
	if subtle.ConstantTimeCompare([]byte(idToken.Nonce), []byte(nonce)) != 1 {
		s.logger.WarnContext(ctx, "single sign-on: the ID token's nonce doesn't match")
		return nil, "", ErrSSOFailed
	}
	claims, err := s.sso.claims(httpCtx, idToken, tok)
	if err != nil {
		s.logger.WarnContext(ctx, "single sign-on: couldn't read the identity's claims", "error", err)
		return nil, "", ErrSSOFailed
	}
	ns, err := s.signInWithIdentity(ctx, idToken.Issuer, idToken.Subject, claims, client)
	if err != nil {
		return nil, "", err
	}
	return ns, returnPath, nil
}

// ssoClaims are the claims Signalbox reads about a person.
type ssoClaims struct {
	Email             string   `json:"email"`
	EmailVerified     flexBool `json:"email_verified"`
	Name              string   `json:"name"`
	GivenName         string   `json:"given_name"`
	FamilyName        string   `json:"family_name"`
	PreferredUsername string   `json:"preferred_username"`
}

// flexBool reads true and false, and the strings "true" and "false" that
// some providers send instead.
type flexBool bool

func (b *flexBool) UnmarshalJSON(data []byte) error {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	switch v := v.(type) {
	case bool:
		*b = flexBool(v)
	case string:
		*b = flexBool(strings.EqualFold(v, "true"))
	default:
		*b = false
	}
	return nil
}

// claims reads the ID token's claims, and asks the userinfo endpoint when
// the ID token has no email address, as some providers send it only there.
func (o *sso) claims(ctx context.Context, idToken *oidc.IDToken, tok *oauth2.Token) (ssoClaims, error) {
	var c ssoClaims
	if err := idToken.Claims(&c); err != nil {
		return c, err
	}
	if c.Email != "" || o.provider.UserInfoEndpoint() == "" {
		return c, nil
	}
	info, err := o.provider.UserInfo(ctx, oauth2.StaticTokenSource(tok))
	if err != nil {
		return c, fmt.Errorf("userinfo: %w", err)
	}
	if info.Subject != idToken.Subject {
		return c, errors.New("userinfo is about another subject")
	}
	var more ssoClaims
	if err := info.Claims(&more); err != nil {
		return c, err
	}
	c.Email, c.EmailVerified = more.Email, more.EmailVerified
	if c.Name == "" {
		c.Name, c.GivenName, c.FamilyName, c.PreferredUsername = more.Name, more.GivenName, more.FamilyName, more.PreferredUsername
	}
	return c, nil
}

// displayName picks a name for a new account from the claims.
func (c ssoClaims) displayName() string {
	local := c.Email
	if at := strings.LastIndex(local, "@"); at > 0 {
		local = local[:at]
	}
	for _, n := range []string{c.Name, c.GivenName + " " + c.FamilyName, c.PreferredUsername, local} {
		if n = cleanName(n); n != "" {
			return n
		}
	}
	return "Signalbox user"
}

// cleanName drops control characters and cuts a name to the 100 characters
// an account name may have.
func cleanName(s string) string {
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s))
	for utf8.RuneCountInString(s) > validate.MaxName {
		_, size := utf8.DecodeLastRuneInString(s)
		s = s[:len(s)-size]
	}
	return strings.TrimSpace(s)
}

// signInWithIdentity signs in the account linked to the identity, or
// creates one on a first sign-in. An advisory lock on the identity keeps
// two simultaneous first sign-ins from both creating an account.
func (s *Service) signInWithIdentity(ctx context.Context, issuer, subject string, c ssoClaims, client Client) (*NewSession, error) {
	var (
		ns      *NewSession
		created bool
	)
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($2, hashtextextended($1, 0)))`, issuer, subject); err != nil {
			return err
		}
		var u User
		err := tx.QueryRow(ctx, `
			WITH linked AS (
				UPDATE user_identities SET last_sign_in_at = now()
				WHERE issuer = $1 AND subject = $2 RETURNING user_id
			)
			SELECT u.id, u.email, u.name, u.admin, u.created_at, u.updated_at
			FROM users u JOIN linked l ON l.user_id = u.id`, issuer, subject,
		).Scan(&u.ID, &u.Email, &u.Name, &u.Admin, &u.CreatedAt, &u.UpdatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			u, err = s.createSSOUser(ctx, tx, issuer, subject, c)
			created = err == nil
		}
		if err != nil {
			return err
		}
		ns, err = s.createSession(ctx, tx, u, client)
		return err
	})
	if err != nil {
		if !isSSOError(err) {
			err = fmt.Errorf("sign in with single sign-on: %w", err)
		}
		s.logger.WarnContext(ctx, "single sign-on refused", "reason", err.Error(), "client_ip", client.IP)
		return nil, err
	}
	s.logger.InfoContext(ctx, "signed in with single sign-on", id.LogAttr("user_id", id.User, ns.User.ID),
		"new_account", created, "client_ip", client.IP)
	return ns, nil
}

func (s *Service) createSSOUser(ctx context.Context, tx pgx.Tx, issuer, subject string, c ssoClaims) (User, error) {
	var errs validate.Errors
	email := validate.Email(&errs, "/email", c.Email)
	if errs.Err() != nil {
		return User{}, fmt.Errorf("%w: the provider sent no usable email address; request the email scope", ErrSSOFailed)
	}
	if !bool(c.EmailVerified) && !s.sso.cfg.TrustUnverifiedEmail {
		return User{}, ErrSSOEmailUnverified
	}
	if !s.sso.domainAllowed(email) {
		return User{}, ErrSSONotAllowed
	}
	var setUp, taken bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM users), EXISTS (SELECT 1 FROM users WHERE lower(email) = lower($1))`,
		email).Scan(&setUp, &taken); err != nil {
		return User{}, err
	}
	if !setUp {
		return User{}, ErrSSOSetupRequired
	}
	if taken {
		return User{}, ErrSSOAccountExists
	}
	u := User{ID: id.New(), Email: email, Name: c.displayName()}
	err := tx.QueryRow(ctx, `
		INSERT INTO users (id, email, name) VALUES ($1, $2, $3) RETURNING created_at, updated_at`,
		u.ID, u.Email, u.Name).Scan(&u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return User{}, fmt.Errorf("create account: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO user_identities (issuer, subject, user_id) VALUES ($1, $2, $3)`,
		issuer, subject, u.ID); err != nil {
		return User{}, fmt.Errorf("link identity: %w", err)
	}
	s.logger.InfoContext(ctx, "created account through single sign-on", id.LogAttr("user_id", id.User, u.ID))
	return u, nil
}

func isSSOError(err error) bool {
	for _, e := range []error{ErrSSODisabled, ErrSSOUnavailable, ErrSSOExpired, ErrSSOFailed,
		ErrSSOEmailUnverified, ErrSSONotAllowed, ErrSSOAccountExists, ErrSSOSetupRequired} {
		if errors.Is(err, e) {
			return true
		}
	}
	return false
}

// SafeReturnPath keeps a path to return to after signing in only if it is
// a path on this site, so a crafted link can't send people elsewhere.
func SafeReturnPath(p string) string {
	if len(p) > 2048 || !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.HasPrefix(p, "/\\") ||
		strings.ContainsAny(p, "\r\n") {
		return "/"
	}
	return p
}

// removeExpiredSSOAttempts deletes sign-ins that never came back.
func (s *Service) removeExpiredSSOAttempts(ctx context.Context) (int64, error) {
	tag, err := s.db.Exec(ctx, `DELETE FROM sso_attempts WHERE expires_at <= now()`)
	if err != nil {
		return 0, fmt.Errorf("remove expired single sign-on attempts: %w", err)
	}
	return tag.RowsAffected(), nil
}
