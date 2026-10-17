// SPDX-License-Identifier: Apache-2.0

package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pb33f/libopenapi"
	validator "github.com/pb33f/libopenapi-validator"
	"github.com/pb33f/libopenapi-validator/config"

	"github.com/CemTbi/Signalbox/internal/buildinfo"
	"github.com/CemTbi/Signalbox/internal/health"
	"github.com/CemTbi/Signalbox/internal/httpapi"
	"github.com/CemTbi/Signalbox/internal/modules/identity"
	"github.com/CemTbi/Signalbox/internal/modules/workspaces"
	"github.com/CemTbi/Signalbox/internal/platform/oidctest"
	"github.com/CemTbi/Signalbox/internal/platform/postgres/postgrestest"
	"github.com/CemTbi/Signalbox/internal/roles"
)

// The tests in this file run the API against a real database over TLS, as a
// browser would use it, and check every request and response against
// api/openapi.yaml. See docs/adr/0005-http-api-conventions.md.

const (
	setupCode = "TEST-SETU-PCOD-E123"
	password  = "correct horse battery staple"
)

var cheapArgon2 = identity.Argon2Params{Memory: 64, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32}

var (
	contractOnce sync.Once
	contract     validator.Validator
	contractErr  error
)

func loadContract(t *testing.T) validator.Validator {
	t.Helper()
	contractOnce.Do(func() {
		spec, err := os.ReadFile(filepath.Join("..", "..", "api", "openapi.yaml"))
		if err != nil {
			contractErr = err
			return
		}
		doc, err := libopenapi.NewDocument(spec)
		if err != nil {
			contractErr = err
			return
		}
		// Who may call what is tested by behavior below, not by the
		// validator, so it doesn't check the security requirements.
		v, errs := validator.NewValidator(doc, config.WithFormatAssertions(), config.WithoutSecurityValidation())
		if len(errs) > 0 {
			contractErr = fmt.Errorf("load api/openapi.yaml: %v", errs)
			return
		}
		contract = v
	})
	if contractErr != nil {
		t.Fatal(contractErr)
	}
	return contract
}

// harness is a running API with its own database.
type harness struct {
	t      *testing.T
	srv    *httptest.Server
	client *http.Client
	pool   *pgxpool.Pool
	ids    *identity.Service
	// csrf is sent with unsafe requests once someone has signed in.
	csrf string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	return startHarness(t, nil)
}

// newHarnessWithSSO starts a harness whose single sign-on goes to idp.
func newHarnessWithSSO(t *testing.T, idp *oidctest.Provider) *harness {
	t.Helper()
	return startHarness(t, func(cfg *identity.Config, public *url.URL) {
		cfg.SSO = identity.SSOConfig{
			Issuer: idp.Issuer, ClientID: idp.ClientID, ClientSecret: idp.ClientSecret, Name: "Test IdP",
			RedirectURL: public.JoinPath("api/v1/sso/callback").String(),
		}
	})
}

func startHarness(t *testing.T, configure func(*identity.Config, *url.URL)) *harness {
	t.Helper()
	loadContract(t)
	pool := postgrestest.New(t)
	logger := slog.New(slog.DiscardHandler)
	srv := httptest.NewUnstartedServer(nil)
	public := &url.URL{Scheme: "https", Host: srv.Listener.Addr().String()}
	cfg := identity.Config{SetupCode: setupCode, Argon2: cheapArgon2}
	if configure != nil {
		configure(&cfg, public)
	}
	ids, err := identity.New(pool, logger, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := ids.PrepareSetup(context.Background(), public.JoinPath("setup").String()); err != nil {
		t.Fatal(err)
	}
	srv.Config.Handler = httpapi.NewHandler(httpapi.Deps{
		Logger:     logger,
		Health:     health.New("signalbox-server", "test"),
		Build:      buildinfo.Info{Version: "1.2.3"},
		Roles:      []roles.Name{roles.API},
		Identity:   ids,
		Workspaces: workspaces.New(pool, logger),
		PublicURL:  public,
	})
	srv.StartTLS()
	t.Cleanup(srv.Close)

	client := srv.Client()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client.Jar = jar
	return &harness{t: t, srv: srv, client: client, pool: pool, ids: ids}
}

// call is one request. Body is sent as JSON unless it is a string.
type call struct {
	method, path string
	body         any
	contentType  string
	headers      map[string]string
	// invalid marks a request that deliberately breaks the contract, so
	// only the response is checked against it.
	invalid bool
	// stopAtRedirect returns a redirect instead of following it.
	stopAtRedirect bool
}

type result struct {
	status int
	header http.Header
	body   []byte
}

func (h *harness) do(c call) result {
	h.t.Helper()
	var raw []byte
	switch b := c.body.(type) {
	case nil:
	case string:
		raw = []byte(b)
	default:
		var err error
		if raw, err = json.Marshal(b); err != nil {
			h.t.Fatal(err)
		}
	}
	build := func() *http.Request {
		var body io.Reader
		if raw != nil {
			body = bytes.NewReader(raw)
		}
		req, err := http.NewRequest(c.method, h.srv.URL+c.path, body)
		if err != nil {
			h.t.Fatal(err)
		}
		if raw != nil {
			ct := c.contentType
			if ct == "" {
				ct = "application/json"
			}
			req.Header.Set("Content-Type", ct)
		}
		if c.method != http.MethodGet && c.method != http.MethodHead {
			req.Header.Set("Origin", h.srv.URL)
			if h.csrf != "" {
				req.Header.Set(httpapi.CSRFHeader, h.csrf)
			}
		}
		for k, v := range c.headers {
			if v == "" {
				req.Header.Del(k)
			} else {
				req.Header.Set(k, v)
			}
		}
		return req
	}

	client := h.client
	if c.stopAtRedirect {
		copied := *h.client
		copied.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		client = &copied
	}
	resp, err := client.Do(build())
	if err != nil {
		h.t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		h.t.Fatal(err)
	}

	// The contract check gets its own copies, because validation reads
	// the bodies.
	vreq := build()
	if !c.invalid {
		if ok, errs := contract.ValidateHttpRequest(vreq); !ok {
			h.t.Errorf("%s %s: the request breaks the contract:%s", c.method, c.path, describe(errs))
		}
		vreq = build()
	}
	vresp := &http.Response{
		StatusCode: resp.StatusCode, Header: resp.Header, Request: vreq,
		Body: io.NopCloser(bytes.NewReader(body)),
	}
	if ok, errs := contract.ValidateHttpResponse(vreq, vresp); !ok {
		h.t.Errorf("%s %s: the %d response breaks the contract:%s\n%s", c.method, c.path, resp.StatusCode, describe(errs), body)
	}
	return result{status: resp.StatusCode, header: resp.Header, body: body}
}

func describe[E interface{ Error() string }](errs []E) string {
	var b strings.Builder
	for _, e := range errs {
		b.WriteString("\n  - ")
		b.WriteString(e.Error())
	}
	return b.String()
}

func (r result) decode(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal(r.body, v); err != nil {
		t.Fatalf("decode %s: %v", r.body, err)
	}
}

// expect fails the test unless the response has the status and, for
// errors, the problem code.
func (r result) expect(t *testing.T, status int, code string) {
	t.Helper()
	if r.status != status {
		t.Fatalf("status %d, want %d: %s", r.status, status, r.body)
	}
	if code == "" {
		return
	}
	var p httpapi.Problem
	r.decode(t, &p)
	if p.Code != code {
		t.Fatalf("problem code %q, want %q: %s", p.Code, code, r.body)
	}
}

// setUp completes first-run setup through the API and keeps the session.
func (h *harness) setUp() httpapi.SessionJSON {
	h.t.Helper()
	r := h.do(call{method: http.MethodPost, path: "/api/v1/setup", body: map[string]string{
		"setupCode": setupCode, "name": "Ada Lovelace", "email": "ada@example.com", "password": password,
	}})
	r.expect(h.t, http.StatusCreated, "")
	var s httpapi.SessionJSON
	r.decode(h.t, &s)
	h.csrf = s.CSRFToken
	return s
}

// signIn signs in through the API and keeps the session.
func (h *harness) signIn(email string) httpapi.SessionJSON {
	h.t.Helper()
	r := h.do(call{method: http.MethodPost, path: "/api/v1/sessions", body: map[string]string{"email": email, "password": password}})
	r.expect(h.t, http.StatusCreated, "")
	var s httpapi.SessionJSON
	r.decode(h.t, &s)
	h.csrf = s.CSRFToken
	return s
}

// signOutLocally forgets the session without telling the server.
func (h *harness) signOutLocally() {
	jar, _ := cookiejar.New(nil)
	h.client.Jar = jar
	h.csrf = ""
}
