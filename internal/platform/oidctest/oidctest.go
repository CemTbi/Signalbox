// SPDX-License-Identifier: Apache-2.0

// Package oidctest runs a small OpenID Connect provider for tests. It has no
// login page: each authorization signs in the identity the test chose with
// SignInAs. It checks client credentials, redirect URIs and PKCE like a real
// provider, and can be told to misbehave.
package oidctest

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
)

// Identity is a person at the provider.
type Identity struct {
	Subject string
	Email   string
	// EmailVerified is sent as the email_verified claim: true, false, a
	// string such as "true", or nil to leave the claim out.
	EmailVerified any
	Name          string
}

// Provider is a running fake identity provider.
type Provider struct {
	// Issuer is the provider's issuer URL.
	Issuer       string
	ClientID     string
	ClientSecret string

	signer jose.Signer
	jwks   jose.JSONWebKeySet

	mu                  sync.Mutex
	next                Identity
	codes               map[string]grant
	tokens              map[string]Identity
	wrongNonce          bool
	emailOnlyInUserinfo bool
}

type grant struct {
	identity    Identity
	nonce       string
	challenge   string
	redirectURI string
}

// New starts a provider that stops when the test ends.
func New(tb testing.TB) *Provider {
	tb.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		tb.Fatal(err)
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "test-key"))
	if err != nil {
		tb.Fatal(err)
	}
	p := &Provider{
		ClientID:     "signalbox",
		ClientSecret: "test-client-secret",
		signer:       signer,
		jwks:         jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "test-key", Algorithm: "RS256", Use: "sig"}}},
		codes:        map[string]grant{},
		tokens:       map[string]Identity{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", p.discovery)
	mux.HandleFunc("GET /keys", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, p.jwks) })
	mux.HandleFunc("GET /authorize", p.authorize)
	mux.HandleFunc("POST /token", p.token)
	mux.HandleFunc("GET /userinfo", p.userinfo)
	srv := httptest.NewServer(mux)
	tb.Cleanup(srv.Close)
	p.Issuer = srv.URL
	return p
}

// SignInAs chooses who the next authorizations sign in.
func (p *Provider) SignInAs(id Identity) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.next = id
}

// SendWrongNonce makes ID tokens carry a nonce other than the client asked
// for, as an attacker replaying a token would.
func (p *Provider) SendWrongNonce(on bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.wrongNonce = on
}

// SendEmailOnlyInUserinfo leaves the email out of ID tokens, as some
// providers do, so clients must ask the userinfo endpoint.
func (p *Provider) SendEmailOnlyInUserinfo(on bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.emailOnlyInUserinfo = on
}

func (p *Provider) discovery(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{
		"issuer":                                p.Issuer,
		"authorization_endpoint":                p.Issuer + "/authorize",
		"token_endpoint":                        p.Issuer + "/token",
		"userinfo_endpoint":                     p.Issuer + "/userinfo",
		"jwks_uri":                              p.Issuer + "/keys",
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"code_challenge_methods_supported":      []string{"S256"},
	})
}

func (p *Provider) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	switch {
	case q.Get("client_id") != p.ClientID:
		http.Error(w, "unknown client", http.StatusBadRequest)
		return
	case q.Get("response_type") != "code":
		http.Error(w, "unsupported response_type", http.StatusBadRequest)
		return
	case q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "":
		http.Error(w, "PKCE with S256 is required", http.StatusBadRequest)
		return
	case !strings.Contains(" "+q.Get("scope")+" ", " openid "):
		http.Error(w, "the openid scope is required", http.StatusBadRequest)
		return
	}
	code := rand.Text()
	p.mu.Lock()
	p.codes[code] = grant{identity: p.next, nonce: q.Get("nonce"), challenge: q.Get("code_challenge"), redirectURI: q.Get("redirect_uri")}
	p.mu.Unlock()

	// Like a real provider, only send people back to a registered
	// address: Signalbox's callback path.
	back, err := url.Parse(q.Get("redirect_uri"))
	if err != nil || back.Path != "/api/v1/sso/callback" {
		http.Error(w, "unregistered redirect_uri", http.StatusBadRequest)
		return
	}
	v := back.Query()
	v.Set("code", code)
	v.Set("state", q.Get("state"))
	back.RawQuery = v.Encode()
	http.Redirect(w, r, back.String(), http.StatusFound) //nolint:gosec // G710: the path was checked above.
}

func (p *Provider) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		tokenError(w, "invalid_request")
		return
	}
	id, secret, ok := r.BasicAuth()
	if !ok {
		id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}
	if id != p.ClientID || secret != p.ClientSecret {
		tokenError(w, "invalid_client")
		return
	}
	code := r.PostForm.Get("code")
	p.mu.Lock()
	g, found := p.codes[code]
	delete(p.codes, code) // codes work once
	wrongNonce, emailOnlyInUserinfo := p.wrongNonce, p.emailOnlyInUserinfo
	p.mu.Unlock()
	sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
	switch {
	case !found, r.PostForm.Get("grant_type") != "authorization_code":
		tokenError(w, "invalid_grant")
		return
	case r.PostForm.Get("redirect_uri") != g.redirectURI:
		tokenError(w, "invalid_grant")
		return
	case base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge:
		tokenError(w, "invalid_grant") // the PKCE verifier doesn't match
		return
	}

	now := time.Now()
	claims := map[string]any{
		"iss": p.Issuer, "sub": g.identity.Subject, "aud": p.ClientID,
		"iat": now.Unix(), "exp": now.Add(5 * time.Minute).Unix(), "nonce": g.nonce,
		"name": g.identity.Name,
	}
	if wrongNonce {
		claims["nonce"] = "not-the-nonce"
	}
	if !emailOnlyInUserinfo {
		addEmail(claims, g.identity)
	}
	payload, _ := json.Marshal(claims)
	jws, err := p.signer.Sign(payload)
	if err != nil {
		tokenError(w, "server_error")
		return
	}
	idToken, _ := jws.CompactSerialize()
	access := rand.Text()
	p.mu.Lock()
	p.tokens[access] = g.identity
	p.mu.Unlock()
	writeJSON(w, map[string]any{"access_token": access, "token_type": "Bearer", "expires_in": 300, "id_token": idToken})
}

func (p *Provider) userinfo(w http.ResponseWriter, r *http.Request) {
	access, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	p.mu.Lock()
	id, ok := p.tokens[access]
	p.mu.Unlock()
	if !ok {
		http.Error(w, "unknown token", http.StatusUnauthorized)
		return
	}
	claims := map[string]any{"sub": id.Subject, "name": id.Name}
	addEmail(claims, id)
	writeJSON(w, claims)
}

func addEmail(claims map[string]any, id Identity) {
	if id.Email != "" {
		claims["email"] = id.Email
	}
	if id.EmailVerified != nil {
		claims["email_verified"] = id.EmailVerified
	}
}

func tokenError(w http.ResponseWriter, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// Authorize follows a sign-in URL from StartSSO through the provider, as a
// browser would, and returns the state and code it sends back.
func Authorize(tb testing.TB, signInURL string) (state, code string) {
	tb.Helper()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(signInURL) //nolint:noctx // a test helper
	if err != nil {
		tb.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		tb.Fatalf("authorize answered %s", resp.Status)
	}
	back, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		tb.Fatal(err)
	}
	return back.Query().Get("state"), back.Query().Get("code")
}
